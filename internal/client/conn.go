package client

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
)

// reconnectLoop is the outer dial-and-reconnect driver. It invokes
// sessionFn for each successful handshake; sessionFn returns when the
// connection terminates. On TLS-handshake failures (most likely a config
// problem) a longer fixed backoff kicks in so the operator's logs aren't
// flooded.
func reconnectLoop(
	ctx context.Context,
	cfg *config.ClientConfig,
	profile string,
	log *slog.Logger,
	dialFn func(ctx context.Context, server string) (transport.Conn, error),
	sessionFn func(ctx context.Context, conn transport.Conn) error,
) error {
	wsURL := buildWSURL(cfg.Server, profile)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}

		conn, err := dialFn(ctx, wsURL)
		if err != nil {
			attempt++
			delay := backoffDelay(cfg, attempt, err)
			log.Warn("reconnect scheduled",
				slog.String(logging.FieldEvent, logging.EvtReconnectScheduled),
				slog.Int(logging.FieldAttempt, attempt),
				slog.Duration(logging.FieldDelay, delay),
				slog.String(logging.FieldReason, err.Error()),
			)
			if !sleepCtx(ctx, delay) {
				return nil
			}
			continue
		}

		attempt = 0
		log.Info("session connected",
			slog.String(logging.FieldEvent, logging.EvtConnected),
			slog.String("server", cfg.Server),
		)

		// sessionFn returns when the connection terminates (either peer
		// close, local ctx cancel, or session error). On return we evaluate
		// whether to reconnect.
		sessErr := sessionFn(ctx, conn)
		_ = conn.Close(1000, "client-session-end")
		log.Info("session disconnected",
			slog.String(logging.FieldEvent, logging.EvtDisconnected),
			slog.String(logging.FieldReason, errString(sessErr)),
		)
		if ctx.Err() != nil {
			return nil
		}

		attempt = 1
		delay := backoffDelay(cfg, attempt, sessErr)
		log.Info("reconnect scheduled",
			slog.String(logging.FieldEvent, logging.EvtReconnectScheduled),
			slog.Int(logging.FieldAttempt, attempt),
			slog.Duration(logging.FieldDelay, delay),
		)
		if !sleepCtx(ctx, delay) {
			return nil
		}
	}
}

// dial constructs the mTLS WebSocket conn. The URL must carry the profile in
// its path; we append /ws/profile/<name> if the config.Server value is a
// host-only wss:// URL.
func dial(ctx context.Context, server string, tlsCfg *tls.Config, headers http.Header) (transport.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, common.WriteTimeout)
	defer cancel()
	return transport.Dial(dialCtx, server, tlsCfg, headers)
}

func (a *agent) dial(ctx context.Context, server string) (transport.Conn, error) {
	var headers http.Header
	if a.cfg.AuthMode == config.AuthModeJWT {
		token, err := a.loadJWTToken()
		if err != nil {
			return nil, err
		}
		headers = a.dialHeaders(token)
	}
	return dial(ctx, server, a.tlsCfg, headers)
}

// sleepCtx waits for d or for ctx to cancel. Returns false on cancellation
// so the caller can exit the loop.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// backoffDelay returns the wait before the next attempt. TLS handshake
// failures use a fixed common.ReconnectTLSFailure (config-level error,
// tight retry loops are counter-productive). Other failures use expo
// backoff from common.ReconnectInitial capped at common.ReconnectMax with
// ±common.ReconnectJitter.
func backoffDelay(cfg *config.ClientConfig, attempt int, err error) time.Duration {
	if err != nil && errors.Is(err, common.ErrTLSHandshake) {
		return common.ReconnectTLSFailure
	}
	initial := cfg.Reconnect.InitialDelay
	if initial <= 0 {
		initial = common.ReconnectInitial
	}
	max := cfg.Reconnect.MaxDelay
	if max <= 0 {
		max = common.ReconnectMax
	}
	jitter := cfg.Reconnect.Jitter
	if jitter < 0 {
		jitter = 0
	}

	delay := initial
	for i := 1; i < attempt && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	if jitter > 0 {
		//nolint:gosec // Jitter does not need cryptographic randomness.
		j := time.Duration(rand.Int63n(int64(jitter)*2)) - jitter
		delay += j
		if delay < 0 {
			delay = 0
		}
	}
	return delay
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// checkServerURL returns an error if the configured server URL is clearly
// malformed. We keep this strict-ish check out of config.LoadClient because
// self-client callers construct their own URLs and should get a descriptive
// error here.
func checkServerURL(u string) error {
	if !strings.HasPrefix(u, "wss://") {
		return common.Wrap(nil, common.ErrConfigInvalid, "server URL must start with wss://: %q", u)
	}
	return nil
}

// buildWSURL appends the per-profile WebSocket route to the configured server
// base URL. Operators specify cfg.Server as a host-only wss:// URL ("wss://host:port"),
// and the per-profile path is mechanical: there's no value in making the user
// hand-write /ws/profile/<name> in client.yaml. If the configured URL already
// includes a path other than "/", we leave it alone — escape hatch for tests.
func buildWSURL(server, profile string) string {
	u, err := url.Parse(server)
	if err != nil {
		return server
	}
	if u.Path != "" && u.Path != "/" {
		return server
	}
	u.Path = "/ws/profile/" + profile
	return u.String()
}
