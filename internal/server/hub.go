// Package server implements the mediating server: a gin HTTP router behind
// mTLS that accepts WebSocket connections on /ws/profile/:name, groups them
// by profile, and routes snapshot reports through per-profile mediator
// goroutines that select truth and push it back to stale nodes.
//
// Concurrency model — a Hub instance owns a map of profileHub structs, one
// per configured profile. Each profileHub owns a Mediator that runs as a
// single goroutine; all state mutations on candidates/truth happen inside
// that goroutine, which lets us avoid locking mediator state. Sessions talk
// to the mediator via its ingress channel; the mediator talks back to a
// session via the session's Conn.Send — thread-safe per transport contract.
package server

import (
	"context"
	"log/slog"
	"slices"
	"sync"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// Hub groups WebSocket sessions by profile name. It is the single entry point
// the HTTP layer uses to register/unregister sessions and find the mediator
// responsible for a profile.
type Hub struct {
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	byProfile   map[string]*profileHub
	profilesRef config.ProfileStore
	notifyFn    func(context.Context, notifier.TruthRecord)

	// shuttingDown flips to true on Shutdown. New registrations are rejected.
	shuttingDown bool
}

// profileHub is the per-profile state container. Its lifecycle is tied to the
// Hub (mediators are started lazily on first connection and torn down on
// Hub.Shutdown).
type profileHub struct {
	profile  config.Profile
	format   formats.Format
	mediator *Mediator

	mu       sync.Mutex
	sessions map[string]*Session // keyed by nodeID

	// sessionsByIdentity allows fast identity-displacement lookups: a new
	// connection with the same authenticated identity displaces the prior
	// session so operators can reconnect a node without a stuck ghost.
	sessionsByIdentity map[string]*Session
}

// NewHub builds an empty Hub. profileStore is consulted on every
// registration; the hub does not cache profile definitions so SIGHUP-driven
// reloads take effect on the next connection.
func NewHub(ps config.ProfileStore, log *slog.Logger) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		log:         log.With(slog.String(logging.FieldComponent, logging.ComponentServer)),
		ctx:         ctx,
		cancel:      cancel,
		byProfile:   make(map[string]*profileHub),
		profilesRef: ps,
	}
}

// SetPropagationNotifier installs the callback mediators use when a newly
// selected truth is pushed to one or more target nodes.
func (h *Hub) SetPropagationNotifier(fn func(context.Context, notifier.TruthRecord)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notifyFn = fn
}

// getOrStartProfile resolves a profileHub for name, creating and starting its
// mediator if this is the first registration. Returns common.ErrProfileNotFound
// if no such profile exists in the store. Caller must hold h.mu.
func (h *Hub) getOrStartProfile(name string) (*profileHub, error) {
	if ph, ok := h.byProfile[name]; ok {
		return ph, nil
	}
	p, ok := h.profilesRef.Get(name)
	if !ok {
		return nil, common.Wrap(nil, common.ErrProfileNotFound, common.MsgProfileUnknown, name)
	}
	f, ok := formats.Get(p.Format)
	if !ok {
		return nil, common.Wrap(nil, common.ErrFormatNotRegistered, common.MsgFormatUnknown, p.Format)
	}
	targetPath, err := formats.ResolveCredentialPath(f, p.Dir)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "profile %q: resolve credential path from dir %q", p.Name, p.Dir)
	}
	ph := &profileHub{
		profile:            p,
		format:             f,
		sessions:           make(map[string]*Session),
		sessionsByIdentity: make(map[string]*Session),
	}
	ph.mediator = newMediator(p, f, targetPath, h.log, ph, h.notifyFn)
	go ph.mediator.run(h.ctx)
	h.byProfile[name] = ph
	return ph, nil
}

// Register installs a newly-upgraded session in the hub. In mtls mode it
// validates the client CN against the profile's allowed_clients ACL. In jwt
// mode the route handler already validated the profile claim and Register only
// manages session displacement.
func (h *Hub) Register(ctx context.Context, profileName string, auth handshakeAuth, s *Session) error {
	h.mu.Lock()
	if h.shuttingDown {
		h.mu.Unlock()
		return common.Wrap(nil, common.ErrShutdown, "hub shutting down")
	}
	ph, err := h.getOrStartProfile(profileName)
	h.mu.Unlock()
	if err != nil {
		return err
	}

	if auth.mode == config.AuthModeMTLS && !isCNAllowed(ph.profile, auth.identity) {
		return common.Wrap(nil, common.ErrCNMismatch, common.MsgCNNotAllowed, auth.identity, profileName)
	}

	ph.mu.Lock()

	// Displace any prior session for this CN. We close the old conn outside
	// the lock via a deferred call to avoid lock-inversion with session code.
	var displaced *Session
	if prev, ok := ph.sessionsByIdentity[auth.identity]; ok {
		displaced = prev
	}
	ph.sessions[s.nodeID] = s
	ph.sessionsByIdentity[auth.identity] = s
	s.hub = ph
	ph.mu.Unlock()

	if displaced != nil {
		// Close after returning, outside the lock.
		go func(p *Session) {
			h.log.Info("displacing session",
				slog.String(logging.FieldEvent, logging.EvtSessionRejected),
				slog.String(logging.FieldProfile, profileName),
				slog.String(logging.FieldIdentity, auth.identity),
				slog.String(logging.FieldAuthMode, string(auth.mode)),
				slog.String(logging.FieldPeerCN, auth.peerCN),
				slog.String(logging.FieldReason, common.ErrSessionDisplaced.Error()),
			)
			_ = p.conn.Close(1008, common.ErrSessionDisplaced.Error())
		}(displaced)
	}
	if h.notifyFn != nil {
		h.notifyFn(ctx, notifier.TruthRecord{
			Profile:   profileName,
			Format:    ph.format.Name(),
			EventKind: notifier.EventSessionConnected,
			NodeID:    s.nodeID,
			PeerCN:    s.peerCN,
			AuthMode:  string(s.authMode),
			Nodes:     ph.snapshotNodeIDs(),
		})
	}
	return nil
}

// Unregister removes s from its profile hub. Safe to call multiple times.
func (h *Hub) Unregister(s *Session) {
	ph := s.hub
	if ph == nil {
		return
	}
	removedCurrent := false
	ph.mu.Lock()
	if cur, ok := ph.sessions[s.nodeID]; ok && cur == s {
		delete(ph.sessions, s.nodeID)
		removedCurrent = true
	}
	if cur, ok := ph.sessionsByIdentity[s.identity]; ok && cur == s {
		delete(ph.sessionsByIdentity, s.identity)
	}
	ph.mu.Unlock()
	// Let mediator update candidate state.
	if removedCurrent {
		ph.mediator.NotifyDisconnect(s.nodeID)
	}
	if removedCurrent && h.notifyFn != nil {
		h.notifyFn(context.Background(), notifier.TruthRecord{
			Profile:   ph.profile.Name,
			Format:    ph.format.Name(),
			EventKind: notifier.EventSessionDisconnected,
			NodeID:    s.nodeID,
			PeerCN:    s.peerCN,
			AuthMode:  string(s.authMode),
			Nodes:     ph.snapshotNodeIDs(),
		})
	}
}

// ProfileTruth bundles a mediator's account → truth mirror with the
// profile + format identifiers needed to interpret it. The notifier and
// status endpoint use this to enumerate currently-known truths.
type ProfileTruth struct {
	Profile string
	Format  string
	Account string
	MirrorTruth
}

// SnapshotTruths returns one ProfileTruth per (profile, account) currently
// held by any mediator. Callers may mutate the returned slice.
func (h *Hub) SnapshotTruths() []ProfileTruth {
	h.mu.Lock()
	phs := make([]*profileHub, 0, len(h.byProfile))
	for _, ph := range h.byProfile {
		phs = append(phs, ph)
	}
	h.mu.Unlock()

	var out []ProfileTruth
	for _, ph := range phs {
		mirror := ph.mediator.SnapshotTruths()
		for account, mt := range mirror {
			out = append(out, ProfileTruth{
				Profile:     ph.profile.Name,
				Format:      ph.format.Name(),
				Account:     account,
				MirrorTruth: mt,
			})
		}
	}
	return out
}

func (h *Hub) RequestSnapshot(ctx context.Context, profile string, reason string) (int, error) {
	h.mu.Lock()
	ph, ok := h.byProfile[profile]
	h.mu.Unlock()
	if !ok {
		if _, exists := h.profilesRef.Get(profile); !exists {
			return 0, common.Wrap(nil, common.ErrProfileNotFound, common.MsgProfileUnknown, profile)
		}
		return 0, nil
	}
	sessions := ph.snapshotSessions()
	req := transport.SnapshotRequest{Profile: profile, Reason: reason}
	sent := 0
	for _, s := range sessions {
		if err := s.sendSnapshotRequest(ctx, req); err != nil {
			h.log.Warn("snapshot request failed",
				slog.String(logging.FieldProfile, profile),
				slog.String(logging.FieldNodeID, s.nodeID),
				slog.String(logging.FieldReason, err.Error()),
			)
			continue
		}
		sent++
	}
	return sent, nil
}

// snapshotSessions returns a point-in-time list of *Session for the status
// endpoint.
func (ph *profileHub) snapshotSessions() []*Session {
	ph.mu.Lock()
	defer ph.mu.Unlock()
	out := make([]*Session, 0, len(ph.sessions))
	for _, s := range ph.sessions {
		out = append(out, s)
	}
	return out
}

func (ph *profileHub) snapshotNodeIDs() []string {
	ph.mu.Lock()
	defer ph.mu.Unlock()
	out := make([]string, 0, len(ph.sessions))
	for _, s := range ph.sessions {
		out = append(out, s.nodeID)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	return out
}

// Shutdown stops accepting new registrations, closes every active session,
// and waits for all mediators to exit. Safe to call once.
func (h *Hub) Shutdown(ctx context.Context) {
	h.mu.Lock()
	h.shuttingDown = true
	h.cancel()
	phs := make([]*profileHub, 0, len(h.byProfile))
	for _, ph := range h.byProfile {
		phs = append(phs, ph)
	}
	h.mu.Unlock()

	for _, ph := range phs {
		ph.mu.Lock()
		sessions := make([]*Session, 0, len(ph.sessions))
		for _, s := range ph.sessions {
			sessions = append(sessions, s)
		}
		ph.mu.Unlock()
		for _, s := range sessions {
			_ = s.conn.Close(1001, "server shutdown")
		}
		ph.mediator.Stop()
	}
}

// isCNAllowed matches cn against the profile's allowed_clients list exactly.
// Wildcards are NOT supported at this layer — profiles are expected to list
// each intended CN.
func isCNAllowed(p config.Profile, cn string) bool {
	return slices.Contains(p.AllowedClients, cn)
}
