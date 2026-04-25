package server

import (
	"context"
	"encoding/base64"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier"
	"github.com/0xc0de1ab/pangaea/internal/safeio"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// candidate is the per-(node, account) view the mediator keeps while
// deciding truth. snap is nil for "absent" reports; status is derived from
// the client's live_check metadata plus server-side re-validation against
// the clock. Account is the identifier reported by the client; an empty
// value buckets the candidate into the format's "shared" partition.
type candidate struct {
	nodeID       string
	account      string
	session      *Session
	snap         formats.Snapshot
	fingerprint  string
	status       formats.ValidationStatus
	reportedAt   time.Time
	path         string
	summary      transport.SummaryCarrier
	liveCheckRes string
	absent       bool
	lastApplied  string // last fingerprint this node ack'd — used for stale-only routing
}

// truthState tracks the most recent pushed truth for one account so repeated
// identical reports don't re-trigger pushes. It also gates propagation by
// cooldown. Each account in a profile has its own truthState — two accounts
// sharing a profile name produce two independent truths.
type truthState struct {
	account     string
	fingerprint string
	issuedAt    time.Time
	summary     transport.SummaryCarrier
	rawB64      string
	format      string
	pushedAt    time.Time
}

// Mediator is the single goroutine that owns the per-profile decision state.
// It receives events through its ingress channels (Report/Absent/Ack/
// NotifyDisconnect) and produces truth.push messages via the ph (profileHub)
// back-reference.
type Mediator struct {
	profile    config.Profile
	format     formats.Format
	targetPath string
	log        *slog.Logger
	ph         *profileHub
	notifyFn   func(context.Context, notifier.TruthRecord)

	reports     chan mediatorReport
	absents     chan mediatorAbsent
	acks        chan mediatorAck
	disconnects chan string
	done        chan struct{}
	stopOnce    sync.Once

	// mirror holds a read-side copy of the current truths map (keyed by
	// account). The actor updates it under mirrorMu after each reselect so
	// outside callers (status endpoint, notifier) can read state without
	// stealing the actor's monopoly on mutation.
	mirrorMu sync.RWMutex
	mirror   map[string]MirrorTruth
}

// MirrorTruth is the read-only view of one account's current truth, used by
// the status endpoint and the notifier. Callers MUST treat it as immutable;
// further refreshes from the mediator allocate a new map.
type MirrorTruth struct {
	Account     string
	Fingerprint string
	IssuedAt    time.Time
	PushedAt    time.Time
	Format      string
	RawB64      string
	Summary     transport.SummaryCarrier
}

// SnapshotTruths returns a copy of the current per-account truth state.
// Safe for concurrent callers; the returned map can be mutated freely.
func (m *Mediator) SnapshotTruths() map[string]MirrorTruth {
	m.mirrorMu.RLock()
	defer m.mirrorMu.RUnlock()
	out := make(map[string]MirrorTruth, len(m.mirror))
	for k, v := range m.mirror {
		out[k] = v
	}
	return out
}

// updateMirror is called from the actor goroutine at the end of each
// reselect. It rebuilds the mirror from scratch — the cost is small (a
// handful of accounts) and a stale entry from a removed account would
// confuse downstream readers.
func (m *Mediator) updateMirror(truths map[string]*truthState) {
	next := make(map[string]MirrorTruth, len(truths))
	for a, t := range truths {
		next[a] = MirrorTruth{
			Account:     t.account,
			Fingerprint: t.fingerprint,
			IssuedAt:    t.issuedAt,
			PushedAt:    t.pushedAt,
			Format:      t.format,
			RawB64:      t.rawB64,
			Summary:     t.summary,
		}
	}
	m.mirrorMu.Lock()
	m.mirror = next
	m.mirrorMu.Unlock()
}

type mediatorReport struct {
	session *Session
	payload transport.SnapshotReport
}

type mediatorAbsent struct {
	session *Session
	payload transport.SnapshotAbsent
}

type mediatorAck struct {
	session *Session
	payload transport.TruthAck
}

func newMediator(
	p config.Profile,
	f formats.Format,
	targetPath string,
	log *slog.Logger,
	ph *profileHub,
	notifyFn func(context.Context, notifier.TruthRecord),
) *Mediator {
	return &Mediator{
		profile:    p,
		format:     f,
		targetPath: targetPath,
		log: log.With(
			slog.String(logging.FieldComponent, logging.ComponentMediator),
			slog.String(logging.FieldProfile, p.Name),
			slog.String(logging.FieldFormat, f.Name()),
		),
		ph:          ph,
		notifyFn:    notifyFn,
		reports:     make(chan mediatorReport, common.ChannelBuf),
		absents:     make(chan mediatorAbsent, common.ChannelBuf),
		acks:        make(chan mediatorAck, common.ChannelBuf),
		disconnects: make(chan string, common.ChannelBuf),
		done:        make(chan struct{}),
	}
}

// Report hands a snapshot.report off to the mediator goroutine. Non-blocking
// up to the channel buffer; on overflow the oldest report for the same node
// is effectively retained because the mediator only keeps the latest.
func (m *Mediator) Report(r mediatorReport) {
	select {
	case m.reports <- r:
	case <-m.done:
	}
}

// Absent hands a snapshot.absent off to the mediator goroutine.
func (m *Mediator) Absent(a mediatorAbsent) {
	select {
	case m.absents <- a:
	case <-m.done:
	}
}

// Ack hands a truth.ack off to the mediator goroutine.
func (m *Mediator) Ack(a mediatorAck) {
	select {
	case m.acks <- a:
	case <-m.done:
	}
}

// NotifyDisconnect informs the mediator that a node has disconnected. Stored
// candidate state for that node is cleared; if it was the source of the
// current truth we re-select on the next event.
func (m *Mediator) NotifyDisconnect(nodeID string) {
	select {
	case m.disconnects <- nodeID:
	case <-m.done:
	}
}

// Stop terminates the mediator goroutine. Idempotent.
func (m *Mediator) Stop() {
	m.stopOnce.Do(func() { close(m.done) })
}

// run is the mediator's event loop. All state is local to this goroutine.
// truths is keyed by account: each account in the profile is reconciled
// independently so two distinct LLM accounts cannot overwrite each other.
func (m *Mediator) run(ctx context.Context) {
	candidates := make(map[string]*candidate)
	truths := make(map[string]*truthState)

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.done:
			return
		case r := <-m.reports:
			m.onReport(ctx, candidates, truths, r)
		case a := <-m.absents:
			m.onAbsent(ctx, candidates, truths, a)
		case a := <-m.acks:
			m.onAck(candidates, a)
		case nid := <-m.disconnects:
			delete(candidates, nid)
			// If the disconnected node was the one we last pulled truth from
			// we still keep `truths` — the raw bytes are already broadcast; a
			// re-selection will happen on the next report.
		}
	}
}

func (m *Mediator) onReport(ctx context.Context, cands map[string]*candidate, truths map[string]*truthState, r mediatorReport) {
	// Parse raw bytes into a snapshot. We need the server-side parsed snapshot
	// to call format.Compare — we cannot trust the client's fingerprint blindly.
	raw, err := base64Decode(r.payload.RawB64)
	if err != nil {
		m.log.Warn("malformed raw_b64 in snapshot.report",
			slog.String(logging.FieldNodeID, r.session.nodeID),
			slog.String(logging.FieldReason, err.Error()),
		)
		return
	}
	defer safeio.Zeroize(raw)

	snap, err := m.format.Parse(raw)
	if err != nil {
		m.log.Warn("format.Parse failed on report",
			slog.String(logging.FieldNodeID, r.session.nodeID),
			slog.String(logging.FieldEvent, logging.EvtSnapshotParsed),
			slog.String(logging.FieldOutcome, logging.OutcomeError),
			slog.String(logging.FieldReason, err.Error()),
		)
		cands[r.session.nodeID] = &candidate{
			nodeID:     r.session.nodeID,
			account:    r.payload.Account,
			session:    r.session,
			status:     formats.StatusParseError,
			reportedAt: time.Now(),
			path:       r.payload.Path,
			absent:     false,
		}
		return
	}
	// Server-side re-validation (local only): decide status from clock.
	res, _ := m.format.Validate(ctx, snap, formats.ValidateOpts{LiveCheck: false})
	status := res.Status
	// If client reported a live check result we trust expired/revoked outcomes
	// (they can only downgrade; never upgrade to ok if local is expired).
	if r.payload.LiveCheck.Performed {
		switch formats.ValidationStatus(r.payload.LiveCheck.Result) {
		case formats.StatusExpired, formats.StatusRevoked, formats.StatusUnreachable:
			if status == formats.StatusOK {
				status = formats.ValidationStatus(r.payload.LiveCheck.Result)
			}
		}
	}

	c := &candidate{
		nodeID:       r.session.nodeID,
		account:      r.payload.Account,
		session:      r.session,
		snap:         snap,
		fingerprint:  snap.Fingerprint(),
		status:       status,
		reportedAt:   time.Now(),
		path:         r.payload.Path,
		summary:      r.payload.Summary,
		liveCheckRes: r.payload.LiveCheck.Result,
	}
	cands[r.session.nodeID] = c

	m.log.Info("snapshot validated",
		slog.String(logging.FieldEvent, logging.EvtSnapshotValidated),
		slog.String(logging.FieldNodeID, r.session.nodeID),
		slog.String(logging.FieldAccount, c.account),
		slog.String(logging.FieldFingerprint, c.fingerprint),
		slog.String(logging.FieldStatus, string(status)),
	)

	m.reselect(ctx, cands, truths, r.payload.Format)
}

func (m *Mediator) onAbsent(ctx context.Context, cands map[string]*candidate, truths map[string]*truthState, a mediatorAbsent) {
	cands[a.session.nodeID] = &candidate{
		nodeID:     a.session.nodeID,
		account:    a.payload.Account,
		session:    a.session,
		reportedAt: time.Now(),
		absent:     true,
	}
	m.log.Info("snapshot absent",
		slog.String(logging.FieldEvent, logging.EvtSnapshotAbsent),
		slog.String(logging.FieldNodeID, a.session.nodeID),
		slog.String(logging.FieldAccount, a.payload.Account),
	)
	m.reselect(ctx, cands, truths, m.profile.Format)
}

func (m *Mediator) onAck(cands map[string]*candidate, a mediatorAck) {
	c, ok := cands[a.session.nodeID]
	if !ok {
		// Accept acks from nodes we have no candidate for (e.g., still in
		// absent state after first push).
		c = &candidate{nodeID: a.session.nodeID, account: a.payload.Account, session: a.session, absent: true}
		cands[a.session.nodeID] = c
	}
	outcome := logging.OutcomeOK
	if !a.payload.OK {
		outcome = logging.OutcomeError
	} else {
		c.lastApplied = a.payload.Fingerprint
	}
	evt := logging.EvtTruthApplied
	if !a.payload.OK {
		evt = logging.EvtTruthApplyFailed
	}
	m.log.Info("truth ack",
		slog.String(logging.FieldEvent, evt),
		slog.String(logging.FieldNodeID, a.session.nodeID),
		slog.String(logging.FieldAccount, a.payload.Account),
		slog.String(logging.FieldFingerprint, a.payload.Fingerprint),
		slog.String(logging.FieldOutcome, outcome),
		slog.String(logging.FieldReason, a.payload.Reason),
	)
}

// reselect runs the truth decision logic once per event. Candidates are
// grouped by account; each group runs an independent selection. A push for
// account X never reaches a node that last reported account Y.
func (m *Mediator) reselect(ctx context.Context, cands map[string]*candidate, truths map[string]*truthState, formatName string) {
	// Bucket viable candidates by account. status filtering matches specs §12.
	byAccount := map[string][]*candidate{}
	for _, c := range cands {
		if c.absent || c.snap == nil {
			continue
		}
		switch c.status {
		case formats.StatusOK, formats.StatusScopeWarn, formats.StatusUnreachable:
			byAccount[c.account] = append(byAccount[c.account], c)
		}
	}

	m.log.Debug("candidates",
		slog.String(logging.FieldEvent, logging.EvtMediatorCandidates),
		slog.Int("total", len(cands)),
		slog.Int("accounts", len(byAccount)),
	)

	if len(byAccount) == 0 {
		m.log.Warn("no viable candidate",
			slog.String(logging.FieldEvent, logging.EvtMediatorCandidates),
			slog.String(logging.FieldOutcome, logging.OutcomeDegraded),
		)
		return
	}

	strategy := m.profile.Validate.Strategy
	if !slices.Contains(m.format.Strategies(), strategy) {
		if strategy != "" {
			m.log.Warn("unsupported compare strategy; falling back to format default",
				slog.String(logging.FieldStrategy, strategy),
			)
		}
		if len(m.format.Strategies()) > 0 {
			strategy = m.format.Strategies()[0]
		}
	}

	for account, viable := range byAccount {
		best := viable[0]
		for _, c := range viable[1:] {
			if m.format.Compare(strategy, c.snap, best.snap) > 0 {
				best = c
			}
		}

		prev := truths[account]
		truthChanged := prev == nil || prev.fingerprint != best.fingerprint

		// Cooldown gate applies only to *changes* — otherwise a node that
		// connects after the first push would be permanently locked out of
		// catching up.
		if truthChanged && prev != nil && m.profile.Propagate.Cooldown > 0 {
			if time.Since(prev.pushedAt) < m.profile.Propagate.Cooldown {
				m.log.Debug("truth push suppressed by cooldown",
					slog.String(logging.FieldEvent, logging.EvtTruthUnchanged),
					slog.String(logging.FieldAccount, account),
					slog.String(logging.FieldReason, "cooldown"),
				)
				continue
			}
		}

		ts := prev
		if truthChanged {
			now := time.Now()
			ts = &truthState{
				account:     account,
				fingerprint: best.fingerprint,
				issuedAt:    now,
				summary:     best.summary,
				rawB64:      encodeBase64(best.snap.Raw()),
				format:      formatName,
				pushedAt:    now,
			}
			truths[account] = ts
			m.updateMirror(truths)

			m.log.Info("truth selected",
				slog.String(logging.FieldEvent, logging.EvtTruthSelected),
				slog.String(logging.FieldAccount, account),
				slog.String(logging.FieldFingerprint, best.fingerprint),
				slog.String(logging.FieldNodeID, best.nodeID),
				slog.String(logging.FieldStrategy, strategy),
			)
		} else {
			m.log.Debug("truth unchanged; checking stale members",
				slog.String(logging.FieldEvent, logging.EvtTruthUnchanged),
				slog.String(logging.FieldAccount, account),
				slog.String(logging.FieldFingerprint, best.fingerprint),
			)
		}

		// Always run the push pass: pushTruth's stale-filter is the
		// authoritative dedupe so a node that just reported with an older
		// fingerprint catches up even when the truth itself didn't change.
		targets := m.pushTruth(ctx, cands, ts, best.nodeID)
		if truthChanged && len(targets) > 0 && m.notifyFn != nil {
			record := notifier.TruthRecord{
				Profile:     m.profile.Name,
				Account:     ts.account,
				Format:      ts.format,
				Fingerprint: ts.fingerprint,
				IssuedAt:    ts.issuedAt,
				PushedAt:    time.Now(),
				RawB64:      ts.rawB64,
				Summary:     ts.summary,
				SourceNode:  best.nodeID,
				TargetNodes: targets,
			}
			go m.notifyFn(context.WithoutCancel(ctx), record)
		}
	}
}

// pushTruth delivers truth for one account to nodes that participate in
// that account, excluding the source under stale_only. A node is considered
// "in this account" if its latest candidate carries the same account
// identifier — nodes that have never reported (or reported a different
// account) are NOT pushed to. This enforces the partition invariant: no
// node ever receives truth for an account it doesn't already hold.
func (m *Mediator) pushTruth(ctx context.Context, cands map[string]*candidate, ts *truthState, sourceNodeID string) []string {
	push := transport.TruthPush{
		Profile:     m.profile.Name,
		Account:     ts.account,
		Format:      ts.format,
		Fingerprint: ts.fingerprint,
		RawB64:      ts.rawB64,
		TargetPath:  m.targetPath,
		IssuedAt:    ts.issuedAt,
		Summary:     ts.summary,
	}

	staleOnly := m.profile.Propagate.Mode != config.PropagateModeAll
	stale := map[string]bool{}
	inAccount := map[string]bool{}
	for nid, c := range cands {
		if c.account != ts.account {
			continue
		}
		inAccount[nid] = true
		if nid == sourceNodeID {
			continue
		}
		if c.absent || c.fingerprint != ts.fingerprint {
			stale[nid] = true
		}
	}

	targets := make([]*Session, 0)
	m.ph.mu.Lock()
	for nid, s := range m.ph.sessions {
		if nid == sourceNodeID {
			continue
		}
		if !inAccount[nid] {
			continue
		}
		if staleOnly && !stale[nid] {
			continue
		}
		targets = append(targets, s)
	}
	m.ph.mu.Unlock()

	sent := make([]string, 0, len(targets))
	for _, s := range targets {
		ctxSend, cancel := context.WithTimeout(ctx, common.WriteTimeout)
		err := s.sendTruthPush(ctxSend, push)
		cancel()
		if err != nil {
			m.log.Warn("truth push send failed",
				slog.String(logging.FieldEvent, logging.EvtTruthPushed),
				slog.String(logging.FieldAccount, ts.account),
				slog.String(logging.FieldNodeID, s.nodeID),
				slog.String(logging.FieldOutcome, logging.OutcomeError),
				slog.String(logging.FieldReason, err.Error()),
			)
			continue
		}
		m.log.Info("truth pushed",
			slog.String(logging.FieldEvent, logging.EvtTruthPushed),
			slog.String(logging.FieldAccount, ts.account),
			slog.String(logging.FieldNodeID, s.nodeID),
			slog.String(logging.FieldFingerprint, ts.fingerprint),
			slog.String(logging.FieldOutcome, logging.OutcomeOK),
		)
		sent = append(sent, s.nodeID)
	}
	sort.Strings(sent)
	return sent
}

// currentTruthMeta returns the truth metadata suitable for welcome.known_truth.
// It reads the profileHub's most recent truth snapshot in a race-safe manner
// via the mediator's actor loop.
func (m *Mediator) currentTruthMeta(ctx context.Context) *transport.TruthMeta {
	// We don't expose truthState across goroutines for MVP; the welcome may
	// lag by one event, which is acceptable. Return nil; clients learn truth
	// on the next push.
	_ = ctx
	return nil
}

// base64Decode/Encode live here to keep dependencies confined to this file.
// The transport payload uses standard-encoded base64 with padding.
func base64Decode(s string) ([]byte, error) {
	if s == "" {
		return nil, common.Wrap(nil, common.ErrInvalidMessage, "empty raw_b64")
	}
	return base64.StdEncoding.DecodeString(s)
}

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
