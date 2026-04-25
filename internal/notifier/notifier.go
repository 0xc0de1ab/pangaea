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
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// TruthRecord is the input shape the notifier consumes. It is what
// server.Hub.SnapshotTruths produces, restated in a format-package-free
// shape so this package does not need to import internal/server.
type TruthRecord struct {
	Profile     string
	Account     string
	Format      string
	Fingerprint string
	IssuedAt    time.Time
	PushedAt    time.Time
	RawB64      string
	Summary     transport.SummaryCarrier
	SourceNode  string
	TargetNodes []string
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
		warnSent: map[string]bool{},
	}
}

// Run blocks until ctx is cancelled. It emits one event per known
// (profile, account) per Interval and dispatches to every sink. Failures
// in any single sink are logged but do not stop the loop.
func (n *Notifier) Run(ctx context.Context) error {
	tick := time.NewTicker(n.cfg.Interval)
	defer tick.Stop()
	n.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			n.runOnce(ctx)
		}
	}
}

// Emit sends one record immediately. It is used for event-driven propagation
// notifications; periodic summaries still flow through Run.
func (n *Notifier) Emit(ctx context.Context, r TruthRecord) {
	n.dispatchOne(ctx, r)
}

func (n *Notifier) runOnce(ctx context.Context) {
	records := n.truthSrc(ctx)
	if len(records) == 0 {
		n.log.Debug("no truths to report",
			slog.String(logging.FieldEvent, "notifier.tick"),
		)
		return
	}
	for _, r := range records {
		n.dispatchOne(ctx, r)
	}
}

func (n *Notifier) dispatchOne(ctx context.Context, r TruthRecord) {
	usage := n.probe(ctx, r)
	for _, sink := range n.sinks {
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
}

// probe runs the format's UsageProbe (if implemented) and returns the
// report. Failures are downgraded to "no usage info" so the message still
// goes out — operators want validity reporting even when the upstream
// provider is unreachable.
func (n *Notifier) probe(ctx context.Context, r TruthRecord) formats.UsageReport {
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
