// Package notifier is the periodic out-of-band reporter: it iterates over
// the server's currently-known per-account truths, optionally probes each
// upstream provider for usage / quota info via formats.UsageProbe, and
// fans out a redacted summary to one or more Sinks (Telegram, Slack, ...).
//
// The notifier deliberately does not own any state — it reads truth from a
// pluggable TruthSource (the Hub.SnapshotTruths method in production) and
// uses pluggable Sinks. This keeps the package testable without spinning
// up a server.
package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const (
	EventSessionConnected    = "session.connected"
	EventSessionDisconnected = "session.disconnected"
	EventSessionReconnected  = "session.reconnected"
	EventTruthRestored       = "truth.restored"
	EventTruthLost           = "truth.lost"
	startupRetryInterval     = 15 * time.Second
)

var (
	sessionEventDebounce        = 5 * time.Second
	sessionEventDuplicateWindow = 30 * time.Second
)

// TruthRecord is the input shape the notifier consumes. It is what
// server.Hub.SnapshotTruths produces, restated in a format-package-free
// shape so this package does not need to import internal/server.
type TruthRecord struct {
	Profile     string
	Account     string
	Format      string
	Fingerprint string
	NoTruth     bool
	IssuedAt    time.Time
	PushedAt    time.Time
	RawB64      string
	Summary     transport.SummaryCarrier
	PrevSummary transport.SummaryCarrier
	SourceNode  string
	TargetNodes []string
	Nodes       []string
	EventKind   string
	NodeID      string
	PeerCN      string
	AuthMode    string
}

// TruthSource is the function the notifier polls each tick. In production
// this wraps server.Hub.SnapshotTruths; tests pass a static list.
type TruthSource func(ctx context.Context) []TruthRecord

// FormatLookup resolves a format name to its registered Format
// implementation. Production uses formats.Get; tests inject a fake.
type FormatLookup func(name string) (formats.Format, bool)

// Sink is one delivery target — Telegram, Slack, future destinations. Each
// sink owns its routing rules and message rendering. The notifier just
// fans out per-truth events to every sink that opts in for that
// (profile, account).
type Sink interface {
	// Name is a short human label used in logs.
	Name() string
	// Notify delivers one message. ok=false means "this sink did not
	// match the (profile, account)"; the notifier moves on without
	// counting that as a failure. err non-nil means a real send error
	// (network, auth, etc.).
	Notify(ctx context.Context, r TruthRecord, u formats.UsageReport) (ok bool, err error)
}

// ReportRecord is one notifier-ready truth plus its optional usage probe
// result. Periodic sinks receive batches of these so they can aggregate
// multiple profiles into one outbound message.
type ReportRecord struct {
	Truth TruthRecord
	Usage formats.UsageReport
}

// PeriodicSink is an optional extension for sinks that want batched periodic
// summaries while still keeping single-record event notifications.
type PeriodicSink interface {
	Sink
	NotifyPeriodic(ctx context.Context, records []ReportRecord) error
}

// SessionEventSink is an optional extension for sinks that want batched
// connect/disconnect notifications grouped by node and rendered as a single
// multi-profile message.
type SessionEventSink interface {
	Sink
	NotifySessionBatch(ctx context.Context, events []TruthRecord) error
}

// Config tunes the notifier loop.
type Config struct {
	// Interval is the polling cadence for the periodic summary.
	Interval time.Duration
	// ProbeTimeout caps each UsageProbe HTTP call. Zero falls back to 8s.
	ProbeTimeout time.Duration
}

// Notifier is the periodic reporter. Construct with New and call Run from
// the server's errgroup.
type Notifier struct {
	cfg        Config
	sinks      []Sink
	truthSrc   TruthSource
	formats    FormatLookup
	httpClient *http.Client
	log        *slog.Logger

	mu       sync.Mutex
	warnSent map[string]bool

	sessionMu     sync.Mutex
	sessionRecent map[string]recentSessionBatch
	sessionWait   map[string]*pendingSessionBatch
}

type pendingSessionBatch struct {
	timer           *time.Timer
	events          map[string]*pendingSessionEvent
	lastKind        string
	sawConnected    bool
	sawDisconnected bool
}

type pendingSessionEvent struct {
	record          TruthRecord
	connectedNodes  map[string]struct{}
	sawConnected    bool
	sawDisconnected bool
}

type recentSessionBatch struct {
	digest string
	at     time.Time
}

// New builds a Notifier ready to Run. Pass nil for httpClient to use a
// 10s-timeout default. An empty sinks list is allowed but means the
// notifier does nothing useful — typically the cmd layer skips
// constructing one in that case.
func New(cfg Config, sinks []Sink, ts TruthSource, fl FormatLookup, httpClient *http.Client, log *slog.Logger) *Notifier {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 8 * time.Second
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	} else if cfg.Interval < time.Hour {
		cfg.Interval = time.Hour
	}
	return &Notifier{
		cfg:        cfg,
		sinks:      sinks,
		truthSrc:   ts,
		formats:    fl,
		httpClient: httpClient,
		log: log.With(
			slog.String(logging.FieldComponent, "notifier"),
		),
		warnSent:      map[string]bool{},
		sessionRecent: map[string]recentSessionBatch{},
		sessionWait:   map[string]*pendingSessionBatch{},
	}
}

// Run blocks until ctx is cancelled. It emits one event per known
// (profile, account) per Interval and dispatches to every sink. Failures
// in any single sink are logged but do not stop the loop.
func (n *Notifier) Run(ctx context.Context) error {
	tick := time.NewTicker(n.cfg.Interval)
	retry := time.NewTicker(startupRetryInterval)
	defer tick.Stop()
	defer retry.Stop()
	ready := n.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-retry.C:
			if ready {
				continue
			}
			ready = n.runOnce(ctx)
		case <-tick.C:
			ready = true
			n.runOnce(ctx)
		}
	}
}

// Emit sends one record immediately. It is used for event-driven propagation
// notifications; periodic summaries still flow through Run.
func (n *Notifier) Emit(ctx context.Context, r TruthRecord) {
	if isSessionEvent(r) {
		n.bufferSessionEvent(r)
		return
	}
	usage := n.probe(ctx, r)
	for _, sink := range n.sinks {
		n.dispatchEventToSink(ctx, sink, r, usage)
	}
}

func (n *Notifier) bufferSessionEvent(r TruthRecord) {
	key := sessionEventGroupKey(r)
	n.sessionMu.Lock()
	defer n.sessionMu.Unlock()

	batch, ok := n.sessionWait[key]
	if !ok {
		batch = &pendingSessionBatch{
			events:   make(map[string]*pendingSessionEvent),
			lastKind: r.EventKind,
		}
		batch.timer = time.AfterFunc(sessionEventDebounce, func() {
			n.flushSessionEvent(key)
		})
		n.sessionWait[key] = batch
	} else {
		batch.timer.Reset(sessionEventDebounce)
	}
	switch r.EventKind {
	case EventSessionConnected:
		batch.sawConnected = true
	case EventSessionDisconnected:
		batch.sawDisconnected = true
	}
	batch.lastKind = r.EventKind
	state, ok := batch.events[r.Profile]
	if !ok {
		state = &pendingSessionEvent{
			record:         r,
			connectedNodes: map[string]struct{}{},
		}
		batch.events[r.Profile] = state
	}
	state.record = r
	switch r.EventKind {
	case EventSessionConnected:
		state.sawConnected = true
	case EventSessionDisconnected:
		state.sawDisconnected = true
	case EventSessionReconnected:
		state.sawConnected = true
		state.sawDisconnected = true
	}
	for _, node := range normalizedNodes(r) {
		state.connectedNodes[node] = struct{}{}
	}
}

func (n *Notifier) flushSessionEvent(key string) {
	n.sessionMu.Lock()
	batch, ok := n.sessionWait[key]
	if !ok {
		n.sessionMu.Unlock()
		return
	}
	delete(n.sessionWait, key)
	finalKind := batch.lastKind
	if batch.sawConnected && batch.sawDisconnected && batch.lastKind == EventSessionConnected {
		finalKind = EventSessionReconnected
	}
	events := make([]TruthRecord, 0, len(batch.events))
	for _, state := range batch.events {
		event := state.record
		event.EventKind = finalKind
		switch finalKind {
		case EventSessionConnected, EventSessionReconnected:
			event.Nodes = sortedSessionNodes(state.connectedNodes, event.Nodes)
		case EventSessionDisconnected:
			event.Nodes = normalizedNodes(event)
		}
		events = append(events, event)
	}
	slices.SortFunc(events, func(a, b TruthRecord) int {
		if a.Profile < b.Profile {
			return -1
		}
		if a.Profile > b.Profile {
			return 1
		}
		return 0
	})
	digest := sessionEventDigest(events)
	now := nowFunc()
	if prev, ok := n.sessionRecent[key]; ok &&
		prev.digest == digest &&
		now.Sub(prev.at) < sessionEventDuplicateWindow {
		n.sessionMu.Unlock()
		return
	}
	n.sessionRecent[key] = recentSessionBatch{digest: digest, at: now}
	n.sessionMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, sink := range n.sinks {
		if bs, ok := sink.(SessionEventSink); ok {
			if err := bs.NotifySessionBatch(ctx, events); err != nil {
				n.log.Warn("notifier session sink failed",
					slog.String("sink", sink.Name()),
					slog.String(logging.FieldNodeID, events[0].NodeID),
					slog.String(logging.FieldReason, err.Error()),
				)
			}
			continue
		}
		for _, event := range events {
			n.dispatchEventToSink(ctx, sink, event, formats.UsageReport{})
		}
	}
}

func (n *Notifier) runOnce(ctx context.Context) bool {
	records := n.truthSrc(ctx)
	if len(records) == 0 {
		n.log.Debug("no truths to report",
			slog.String(logging.FieldEvent, "notifier.tick"),
		)
		return false
	}
	if periodicNotReady(records) {
		n.log.Debug("periodic state not ready yet",
			slog.String(logging.FieldEvent, "notifier.tick"),
		)
		return false
	}
	items := make([]ReportRecord, 0, len(records))
	for _, r := range records {
		items = append(items, ReportRecord{
			Truth: r,
			Usage: n.probe(ctx, r),
		})
	}
	for _, sink := range n.sinks {
		if ps, ok := sink.(PeriodicSink); ok {
			if err := ps.NotifyPeriodic(ctx, items); err != nil {
				n.log.Warn("notifier sink failed",
					slog.String("sink", sink.Name()),
					slog.String(logging.FieldReason, err.Error()),
				)
			}
			continue
		}
		for _, item := range items {
			n.dispatchEventToSink(ctx, sink, item.Truth, item.Usage)
		}
	}
	return true
}

func (n *Notifier) dispatchEventToSink(ctx context.Context, sink Sink, r TruthRecord, usage formats.UsageReport) {
	ok, err := sink.Notify(ctx, r, usage)
	switch {
	case err != nil:
		n.log.Warn("notifier sink failed",
			slog.String("sink", sink.Name()),
			slog.String(logging.FieldProfile, r.Profile),
			slog.String(logging.FieldAccount, r.Account),
			slog.String(logging.FieldReason, err.Error()),
		)
	case !ok:
		n.log.Debug("sink declined (no route)",
			slog.String("sink", sink.Name()),
			slog.String(logging.FieldProfile, r.Profile),
			slog.String(logging.FieldAccount, r.Account),
		)
	}
}

// probe runs the format's UsageProbe (if implemented) and returns the
// report. Failures are downgraded to "no usage info" so the message still
// goes out — operators want validity reporting even when the upstream
// provider is unreachable.
func (n *Notifier) probe(ctx context.Context, r TruthRecord) formats.UsageReport {
	if isSessionEvent(r) || r.NoTruth || r.RawB64 == "" {
		return formats.UsageReport{}
	}
	f, ok := n.formats(r.Format)
	if !ok {
		n.log.Warn("format not registered for probe",
			slog.String(logging.FieldFormat, r.Format),
			slog.String(logging.FieldProfile, r.Profile),
			slog.String(logging.FieldAccount, r.Account),
		)
		return formats.UsageReport{}
	}
	probe, ok := f.(formats.UsageProbe)
	if !ok {
		return formats.UsageReport{}
	}
	snap, err := decodeSnapshot(f, r.RawB64)
	if err != nil {
		return formats.UsageReport{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, n.cfg.ProbeTimeout)
	defer cancel()
	rep, err := probe.Probe(probeCtx, snap, "", n.httpClient)
	if err != nil {
		n.log.Debug("usage probe failed",
			slog.String(logging.FieldProfile, r.Profile),
			slog.String(logging.FieldAccount, r.Account),
			slog.String(logging.FieldReason, err.Error()),
		)
		return formats.UsageReport{}
	}
	return rep
}

func isSessionEvent(r TruthRecord) bool {
	return r.EventKind == EventSessionConnected || r.EventKind == EventSessionDisconnected || r.EventKind == EventSessionReconnected
}

func sessionEventGroupKey(r TruthRecord) string {
	return strings.Join([]string{r.NodeID, r.PeerCN, r.AuthMode}, "|")
}

func sessionEventDigest(events []TruthRecord) string {
	type item struct {
		EventKind string   `json:"event_kind"`
		NodeID    string   `json:"node_id"`
		PeerCN    string   `json:"peer_cn,omitempty"`
		AuthMode  string   `json:"auth_mode,omitempty"`
		Profile   string   `json:"profile"`
		Format    string   `json:"format"`
		Nodes     []string `json:"nodes,omitempty"`
	}
	items := make([]item, 0, len(events))
	for _, event := range events {
		items = append(items, item{
			EventKind: event.EventKind,
			NodeID:    event.NodeID,
			PeerCN:    event.PeerCN,
			AuthMode:  event.AuthMode,
			Profile:   event.Profile,
			Format:    event.Format,
			Nodes:     normalizedNodes(event),
		})
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(raw)
}

func sortedSessionNodes(nodes map[string]struct{}, fallback []string) []string {
	if len(nodes) == 0 {
		return normalizedNodes(TruthRecord{Nodes: fallback})
	}
	out := make([]string, 0, len(nodes))
	for node := range nodes {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		out = append(out, node)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func periodicNotReady(records []TruthRecord) bool {
	if len(records) == 0 {
		return true
	}
	for _, record := range records {
		if !record.NoTruth {
			return false
		}
		if len(normalizedNodes(record)) > 0 {
			return false
		}
	}
	return true
}

// decodeSnapshot parses the base64-encoded raw bytes back into a Snapshot.
func decodeSnapshot(f formats.Format, rawB64 string) (formats.Snapshot, error) {
	if rawB64 == "" {
		return nil, fmt.Errorf("empty raw_b64")
	}
	raw, err := base64Decode(rawB64)
	if err != nil {
		return nil, err
	}
	return f.Parse(raw)
}
