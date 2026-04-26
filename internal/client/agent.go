package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/0xc0de1ab/pangaea/internal/safeio"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/0xc0de1ab/pangaea/internal/watcher"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"golang.org/x/sync/errgroup"
)

// Options fine-tunes Run beyond what the config file carries. The zero value
// is valid: Run falls back to config-driven defaults. TLSConfig lets callers
// (notably the server's self-client loop) inject a pre-built *tls.Config
// instead of loading one from disk.
type Options struct {
	// AgentVersion is advertised in Hello. If empty, "dev" is used.
	AgentVersion string
	// TLSConfig overrides the on-disk PKI lookup. Use this when running
	// in-process (e.g. --also-client) where the cert paths are already
	// resolved and we want to reuse them.
	TLSConfig *tls.Config
	// FailFast makes the first dial failure a hard error instead of entering
	// the reconnect loop (CLI ergonomics).
	FailFast bool
	// JWTToken overrides config-driven token loading. Used by in-process
	// callers such as the server's self-client path.
	JWTToken string
}

// Run is the client entry point. It blocks until ctx is cancelled or an
// unrecoverable error occurs. The typical terminal condition is ctx
// cancellation from the parent (SIGTERM → errgroup cancel). One agent runs
// per profile binding; the agents run concurrently under a shared errgroup
// so any unrecoverable per-binding error tears the whole client down.
func Run(ctx context.Context, cfg *config.ClientConfig, opts Options, log *slog.Logger) error {
	if err := checkServerURL(cfg.Server); err != nil {
		return err
	}
	if len(cfg.Profiles) == 0 {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.profiles must not be empty")
	}

	tlsCfg := opts.TLSConfig
	if tlsCfg == nil {
		built, err := pki.ClientTLSConfig(cfg.PKI.CACert, cfg.PKI.ClientCert, cfg.PKI.ClientKey, extractHost(cfg.Server))
		if err != nil {
			return err
		}
		tlsCfg = built
	}

	agentVer := opts.AgentVersion
	if agentVer == "" {
		agentVer = "dev"
	}

	eg, egCtx := errgroup.WithContext(ctx)
	for _, b := range cfg.Profiles {
		f, err := resolveFormat(b)
		if err != nil {
			return err
		}
		path, watchPaths, err := resolveBindingPaths(f, b)
		if err != nil {
			return err
		}
		bindLog := log.With(
			slog.String(logging.FieldComponent, logging.ComponentClient),
			slog.String(logging.FieldProfile, b.Name),
			slog.String(logging.FieldNodeID, cfg.NodeID),
		)
		ag := &agent{
			cfg:              cfg,
			tlsCfg:           tlsCfg,
			log:              bindLog,
			format:           f,
			profile:          b.Name,
			dir:              b.Dir,
			path:             path,
			watchPaths:       watchPaths,
			accountMetaPath:  b.AccountMetaPath,
			agentVer:         agentVer,
			failFast:         opts.FailFast,
			jwtTokenOverride: opts.JWTToken,
			now:              time.Now,
			runCommand:       defaultRunCommand,
		}
		eg.Go(func() error { return ag.run(egCtx) })
	}
	err := eg.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// resolveFormat looks up the format for one profile binding. Operators must
// declare each binding's format explicitly; we no longer guess from the
// registry. An empty value falls back to the only registered format if there
// is exactly one (single-format binaries — the typical case).
func resolveFormat(b config.ProfileBinding) (formats.Format, error) {
	name := b.Format
	if name == "" {
		names := formats.List()
		if len(names) == 0 {
			return nil, common.Wrap(nil, common.ErrFormatNotRegistered, "no formats registered")
		}
		if len(names) > 1 {
			return nil, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: format is required (registered: %v)", b.Name, names)
		}
		name = names[0]
	}
	f, ok := formats.Get(name)
	if !ok {
		return nil, common.Wrap(nil, common.ErrFormatNotRegistered, "profile %q: format %q not registered", b.Name, name)
	}
	return f, nil
}

func resolveBindingPaths(f formats.Format, b config.ProfileBinding) (string, []string, error) {
	credPath, err := formats.ResolveCredentialPath(f, b.Dir)
	if err != nil {
		return "", nil, common.Wrap(err, common.ErrConfigInvalid, "profile %q: resolve credential path from dir %q", b.Name, b.Dir)
	}

	var watchPaths []string
	if len(b.WatchFiles) > 0 {
		watchPaths = append(watchPaths, credPath)
		watchPaths = append(watchPaths, b.WatchFiles...)
	} else {
		watchPaths, err = formats.ResolveWatchPaths(f, b.Dir)
		if err != nil {
			return "", nil, common.Wrap(err, common.ErrConfigInvalid, "profile %q: resolve watch paths from dir %q", b.Name, b.Dir)
		}
	}
	if b.AccountMetaPath != "" {
		watchPaths = append(watchPaths, b.AccountMetaPath)
	}
	return credPath, dedupePaths(watchPaths), nil
}

// agent holds the per-run state. One agent = one profile binding's lifetime.
type agent struct {
	cfg              *config.ClientConfig
	tlsCfg           *tls.Config
	log              *slog.Logger
	format           formats.Format
	profile          string
	dir              string
	path             string
	watchPaths       []string
	accountMetaPath  string
	agentVer         string
	failFast         bool
	jwtTokenOverride string
	jwtUseFirstFrame bool
	now              func() time.Time
	runCommand       func(ctx context.Context, cmd refreshCommand) ([]byte, error)

	// pendingMu guards pending — the "latest watcher event per path" buffer
	// used while the session is disconnected.
	pendingMu sync.Mutex
	pending   map[string]watcher.Event // path -> latest

	refreshMu              sync.Mutex
	lastRefreshAttemptAt   time.Time
	lastRefreshFingerprint string
	lastRefreshReason      string
}

// resolveAccount calls the format's AccountAware hook (if implemented) and
// returns the resulting account identifier. Errors are logged but never
// propagated — an unknown account just falls back to the shared bucket on
// the server, which is the same behaviour formats that don't implement
// AccountAware get for free.
func (a *agent) resolveAccount(ctx context.Context, snap formats.Snapshot) string {
	aw, ok := a.format.(formats.AccountAware)
	if !ok {
		return ""
	}
	accountHint := a.accountHint()
	id, err := aw.Account(ctx, snap, accountHint)
	if err != nil {
		a.log.Warn("account resolution failed",
			slog.String(logging.FieldPath, accountHint),
			slog.String(logging.FieldReason, err.Error()),
		)
		return ""
	}
	return id
}

func (a *agent) resolveAccountWithoutSnapshot(ctx context.Context) string {
	aw, ok := a.format.(formats.AccountAware)
	if !ok {
		return ""
	}
	accountHint := a.accountHint()
	id, err := aw.Account(ctx, nil, accountHint)
	if err != nil {
		a.log.Warn("account resolution failed",
			slog.String(logging.FieldPath, accountHint),
			slog.String(logging.FieldReason, err.Error()),
		)
		return ""
	}
	return id
}

func (a *agent) accountHint() string {
	if a.accountMetaPath != "" {
		return a.accountMetaPath
	}
	return a.dir
}

// run owns the watcher + reconnect loop.
func (a *agent) run(ctx context.Context) error {
	w, err := watcher.New(a.watchPaths, watcher.Options{})
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Start(ctx); err != nil {
		return err
	}

	a.pending = make(map[string]watcher.Event)

	// Buffer watcher events in a background goroutine so they are retained
	// while we are reconnecting. When a session is live, the session pump
	// drains pending under a.pendingMu.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events():
				if !ok {
					return
				}
				a.pendingMu.Lock()
				a.pending[ev.Path] = ev
				a.pendingMu.Unlock()
			}
		}
	}()

	go a.refreshLoop(ctx)

	if a.failFast {
		conn, err := a.dial(ctx, buildWSURL(a.cfg.Server, a.profile))
		if err != nil {
			return err
		}
		defer conn.Close(1000, "fail-fast exit")
		return a.session(ctx, conn)
	}

	return reconnectLoop(ctx, a.cfg, a.profile, a.log, a.dial, a.session)
}

func defaultRunCommand(ctx context.Context, cmd refreshCommand) ([]byte, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.Dir = cmd.Dir
	if len(cmd.Env) > 0 {
		c.Env = append(os.Environ(), cmd.Env...)
	}
	return c.CombinedOutput()
}

// session runs one connected lifetime: hello → welcome → concurrent read &
// report pumps. Returns when the connection terminates.
func (a *agent) session(ctx context.Context, conn transport.Conn) error {
	if a.cfg.AuthMode == config.AuthModeJWT && a.effectiveJWTSendVia() == config.JWTSendViaFirstFrame {
		token, err := a.loadJWTToken()
		if err != nil {
			return err
		}
		if err := sendEnvelope(ctx, conn, transport.KindAuthJWT, transport.AuthJWT{Token: token}); err != nil {
			return err
		}
	}

	// Send hello.
	hello := transport.Hello{
		NodeID:       a.cfg.NodeID,
		AgentVersion: a.agentVer,
		OS:           runtime.GOOS,
	}
	if err := sendEnvelope(ctx, conn, transport.KindHello, hello); err != nil {
		return err
	}

	// Await welcome.
	welcomeCtx, cancel := context.WithTimeout(ctx, common.ReadTimeout)
	defer cancel()
	if _, err := awaitKind(welcomeCtx, conn, transport.KindWelcome); err != nil {
		if a.cfg.AuthMode == config.AuthModeJWT &&
			a.cfg.JWT.SendVia == config.JWTSendViaAuto &&
			a.effectiveJWTSendVia() == config.JWTSendViaAuto &&
			shouldSwitchJWTToFirstFrame(err) {
			a.jwtUseFirstFrame = true
		}
		return err
	}
	if a.cfg.AuthMode == config.AuthModeJWT && a.effectiveJWTSendVia() == config.JWTSendViaHeader {
		a.jwtUseFirstFrame = false
	}

	// Re-report the current state on every successful connect. This avoids
	// reconnect/displacement races where the watcher buffer has not yet replayed
	// but the server needs a fresh candidate before the next propagation pass.
	if err := a.flushPending(ctx, conn); err != nil {
		return err
	}

	// Session loop.
	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()

	// Reader pump: handle truth.push.
	readErr := make(chan error, 1)
	go func() {
		readErr <- a.readLoop(sessCtx, conn)
	}()

	// Writer pump: flush new watcher events as they arrive.
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- a.writeLoop(sessCtx, conn)
	}()

	select {
	case err := <-readErr:
		sessCancel()
		<-writeErr
		return err
	case err := <-writeErr:
		sessCancel()
		<-readErr
		return err
	case <-ctx.Done():
		sessCancel()
		<-readErr
		<-writeErr
		return nil
	}
}

// flushPending clears the buffered watcher state and re-reports the current
// credential snapshot once. On reconnect we only care about the latest current
// view; replaying each historical watched-file event is redundant and makes the
// displacement path timing-sensitive.
func (a *agent) flushPending(ctx context.Context, conn transport.Conn) error {
	a.pendingMu.Lock()
	a.pending = make(map[string]watcher.Event)
	a.pendingMu.Unlock()
	return a.reportCurrentState(ctx, conn)
}

// writeLoop drains watcher events while the session is live and sends
// snapshot.reports. It exits on ctx cancellation.
func (a *agent) writeLoop(ctx context.Context, conn transport.Conn) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			a.pendingMu.Lock()
			evs := make([]watcher.Event, 0, len(a.pending))
			for _, ev := range a.pending {
				evs = append(evs, ev)
			}
			a.pending = make(map[string]watcher.Event)
			a.pendingMu.Unlock()
			for _, ev := range evs {
				if err := a.reportEvent(ctx, conn, ev); err != nil {
					return err
				}
			}
		}
	}
}

// reportEvent re-evaluates the current credential state and emits the
// appropriate snapshot.{report,absent} envelope. Any watched-file change
// triggers a re-read of the primary credentials file.
func (a *agent) reportEvent(ctx context.Context, conn transport.Conn, ev watcher.Event) error {
	raw, err := readFileIfExists(a.path)
	if err != nil || raw == nil {
		abs := transport.SnapshotAbsent{
			Profile: a.profile,
			Account: a.resolveAccountWithoutSnapshot(ctx),
			Path:    a.path,
		}
		return sendEnvelope(ctx, conn, transport.KindSnapshotAbsent, abs)
	}
	defer safeio.Zeroize(raw)

	snap, err := a.format.Parse(raw)
	if err != nil {
		// Parse failure is reported as absent — the format-parsing error is
		// logged but the server can't do anything useful with it.
		a.log.Warn("format.Parse failed",
			slog.String(logging.FieldEvent, logging.EvtSnapshotParsed),
			slog.String(logging.FieldPath, a.path),
			slog.String("trigger_path", ev.Path),
			slog.String(logging.FieldOutcome, logging.OutcomeError),
			slog.String(logging.FieldReason, err.Error()),
		)
		abs := transport.SnapshotAbsent{
			Profile: a.profile,
			Account: a.resolveAccountWithoutSnapshot(ctx),
			Path:    a.path,
		}
		return sendEnvelope(ctx, conn, transport.KindSnapshotAbsent, abs)
	}

	// Local validation — no live check for now; the server can re-validate.
	res, _ := a.format.Validate(ctx, snap, formats.ValidateOpts{})

	account := a.resolveAccount(ctx, snap)
	summary := a.format.Redact(snap)
	summaryRaw, _ := json.Marshal(summary)

	report := transport.SnapshotReport{
		Profile:     a.profile,
		Account:     account,
		Path:        a.path,
		Format:      a.format.Name(),
		Fingerprint: snap.Fingerprint(),
		Summary:     summaryRaw,
		LiveCheck: transport.LiveCheckMeta{
			Performed: false,
			Result:    string(res.Status),
			CheckedAt: res.CheckedAt,
		},
		RawSize: len(raw),
		RawB64:  base64.StdEncoding.EncodeToString(raw),
	}

	a.log.Info("snapshot parsed",
		slog.String(logging.FieldEvent, logging.EvtSnapshotParsed),
		slog.String(logging.FieldPath, a.path),
		slog.String("trigger_path", ev.Path),
		slog.String(logging.FieldAccount, account),
		slog.String(logging.FieldFingerprint, snap.Fingerprint()),
		slog.String(logging.FieldStatus, string(res.Status)),
	)
	if res.Status == formats.StatusExpired {
		a.maybeRefreshAsync(ctx)
	}

	return sendEnvelope(ctx, conn, transport.KindSnapshotReport, report)
}

func (a *agent) reportCurrentState(ctx context.Context, conn transport.Conn) error {
	return a.reportEvent(ctx, conn, watcher.Event{Path: a.path})
}

// readLoop handles truth.push envelopes and writes truth.ack responses.
func (a *agent) readLoop(ctx context.Context, conn transport.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case env, ok := <-conn.Recv():
			if !ok {
				if e := conn.Err(); e != nil {
					return e
				}
				return nil
			}
			if err := a.handleEnvelope(ctx, conn, env); err != nil {
				return err
			}
		}
	}
}

func (a *agent) handleEnvelope(ctx context.Context, conn transport.Conn, env transport.Envelope) error {
	switch env.Type {
	case transport.KindTruthPush:
		var push transport.TruthPush
		if err := json.Unmarshal(env.Payload, &push); err != nil {
			return common.Wrap(err, common.ErrInvalidMessage, "truth.push payload")
		}
		ack := applyTruth(ctx, a.log, push, a.path)
		return sendEnvelope(ctx, conn, transport.KindTruthAck, ack)
	case transport.KindError:
		var p transport.ErrorPayload
		_ = json.Unmarshal(env.Payload, &p)
		a.log.Warn("server reported error",
			slog.String(logging.FieldEvent, logging.EvtSessionRejected),
			slog.String(logging.FieldReason, p.Message),
		)
		return nil
	case transport.KindAuthJWT, transport.KindWelcome, transport.KindHello, transport.KindSnapshotReport, transport.KindSnapshotAbsent, transport.KindTruthAck:
		// Unexpected direction; close.
		return common.Wrap(nil, common.ErrInvalidMessage, "unexpected envelope kind %q", env.Type)
	default:
		return common.Wrap(nil, common.ErrInvalidMessage, "unknown envelope kind %q", env.Type)
	}
}

func dedupePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		p = filepath.Clean(p)
		if p == "." || p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
