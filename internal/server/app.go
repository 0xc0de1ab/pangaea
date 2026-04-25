package server

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier"
	"github.com/0xc0de1ab/pangaea/internal/notifier/discord"
	"github.com/0xc0de1ab/pangaea/internal/notifier/mattermost"
	"github.com/0xc0de1ab/pangaea/internal/notifier/ntfy"
	"github.com/0xc0de1ab/pangaea/internal/notifier/slack"
	"github.com/0xc0de1ab/pangaea/internal/notifier/teams"
	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"golang.org/x/sync/errgroup"
)

// Options controls the server.Run entry point. ServerVersion is embedded in
// the welcome envelope so operators can correlate client logs with a build.
// StatusSocket, when non-empty, causes a unix-socket status endpoint to be
// served alongside the main HTTPS listener.
//
// SelfClientFn is an optional hook: if the operator passed --also-client
// profile names on the command line, the cmd layer supplies a factory here.
// It is kept as a hook rather than a direct import so internal/server does not
// depend on internal/client at package-graph build time.
type Options struct {
	ServerVersion string
	StatusSocket  string
	SelfClientFn  func(ctx context.Context, profile string) error
	AlsoClients   []string
	ProfilesPath  string
}

// Run starts the mediating server. It blocks until ctx is cancelled or a
// component goroutine returns a fatal error, then orchestrates a graceful
// shutdown bounded by common.ShutdownGrace.
func Run(ctx context.Context, cfg *config.ServerConfig, ps config.ProfileStore, opts Options, log *slog.Logger) error {
	tlsCfg, err := pki.ServerTLSConfig(cfg.PKI.CACert, cfg.PKI.ServerCert, cfg.PKI.ServerKey, cfg.AuthMode == config.AuthModeMTLS)
	if err != nil {
		return err
	}
	auth, err := newAuthenticator(cfg)
	if err != nil {
		return err
	}

	hub := NewHub(ps, log)
	router := newRouter(hub, ps, opts.ServerVersion, auth, log)

	sinks := buildNotifierSinks(cfg.Notifier, log)
	intvl, probeTO := notifierTimings(cfg.Notifier)
	var n *notifier.Notifier
	if len(sinks) > 0 {
		n = notifier.New(notifier.Config{
			Interval:     intvl,
			ProbeTimeout: probeTO,
		}, sinks,
			func(_ context.Context) []notifier.TruthRecord {
				raw := hub.SnapshotTruths()
				out := make([]notifier.TruthRecord, 0, len(raw))
				for _, t := range raw {
					out = append(out, notifier.TruthRecord{
						Profile:     t.Profile,
						Account:     t.Account,
						Format:      t.Format,
						Fingerprint: t.Fingerprint,
						IssuedAt:    t.IssuedAt,
						PushedAt:    t.PushedAt,
						RawB64:      t.RawB64,
						Summary:     t.Summary,
					})
				}
				return out
			},
			formats.Get,
			nil,
			log,
		)
		hub.SetPropagationNotifier(func(ctx context.Context, r notifier.TruthRecord) {
			n.Emit(ctx, r)
		})
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: common.WriteTimeout,
		// Disable HTTP/2 so gorilla/websocket's RFC 6455 Upgrade works.
		// HTTP/2 frames the stream differently and does not support the
		// Upgrade mechanism out of the box.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}

	// Listener-ready channel lets the self-client wait for the server socket
	// to be accepting before dialing (specs §E.9 corner: selfclient starts
	// before listener → dial fails).
	ready := make(chan struct{})

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		log.Info("starting TLS listener",
			slog.String(logging.FieldComponent, logging.ComponentServer),
			slog.String(logging.FieldEvent, logging.EvtStartup),
			slog.String("listen", cfg.Listen),
		)
		close(ready)
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	// Status endpoint on a unix socket. Optional; if the socket path is empty
	// we skip it entirely.
	if opts.StatusSocket != "" {
		eg.Go(func() error {
			return runStatusEndpoint(egCtx, opts.StatusSocket, hub, ps, opts.ServerVersion, log)
		})
	}

	// SIGHUP-driven reload: re-subscribe here so we get every change. The
	// cmd layer converts the signal into a Reload call on ps; we just log it.
	eg.Go(func() error {
		sub := ps.Subscribe()
		for {
			select {
			case <-egCtx.Done():
				return nil
			case snap := <-sub:
				log.Info("profiles reloaded",
					slog.String(logging.FieldComponent, logging.ComponentServer),
					slog.String(logging.FieldEvent, logging.EvtConfigReload),
					slog.Int("profiles", len(snap)),
				)
			}
		}
	})

	// Notifier (Telegram + Slack). Each sink is optional and configured
	// independently. A misconfigured sink (missing env-var, empty routes)
	// is logged and skipped — the server itself must not fail to start.
	if n != nil {
		eg.Go(func() error { return n.Run(egCtx) })
	}

	// Self-client launch (specs §15.3 --also-client).
	if opts.SelfClientFn != nil {
		for _, name := range opts.AlsoClients {
			if _, ok := ps.Get(name); !ok {
				return common.Wrap(nil, common.ErrProfileNotFound, common.MsgProfileUnknown, name)
			}
			name := name
			eg.Go(func() error {
				// Wait until the listener is ready.
				select {
				case <-ready:
				case <-egCtx.Done():
					return nil
				}
				log.Info("starting self-client",
					slog.String(logging.FieldComponent, logging.ComponentSelfClient),
					slog.String(logging.FieldProfile, name),
					slog.String(logging.FieldEvent, logging.EvtStartup),
				)
				return opts.SelfClientFn(egCtx, name)
			})
		}
	}

	// Wait for ctx cancellation or a goroutine error.
	go func() {
		<-egCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), common.ShutdownGrace)
		defer cancel()
		hub.Shutdown(shutdownCtx)
		_ = srv.Shutdown(shutdownCtx)
	}()

	err = eg.Wait()
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	log.Info("server stopped",
		slog.String(logging.FieldComponent, logging.ComponentServer),
		slog.String(logging.FieldEvent, logging.EvtShutdown),
	)
	return err
}

// readyDelay is exposed for tests that need to tune the ready-signal race.
// Production code should not need to touch it; the listener-ready channel
// replaces a time-based sleep.
var readyDelay = 100 * time.Millisecond

var _ = readyDelay

// buildNotifierSinks constructs every enabled sink. Each sink decides for
// itself whether its config is complete; we surface an empty sink list
// when nothing is enabled so Run can skip the notifier goroutine entirely.
func buildNotifierSinks(cfg config.NotifierConfig, log *slog.Logger) []notifier.Sink {
	var sinks []notifier.Sink

	if cfg.Telegram.Enabled {
		token := os.Getenv(cfg.Telegram.BotTokenEnv)
		switch {
		case token == "":
			log.Warn("telegram notifier disabled: bot token env var is empty",
				slog.String(logging.FieldComponent, logging.ComponentServer),
				slog.String("env_var", cfg.Telegram.BotTokenEnv),
			)
		default:
			tgClient := telegram.New(token)
			if cfg.Telegram.Endpoint != "" {
				tgClient.Endpoint = cfg.Telegram.Endpoint
			}
			routes := make([]notifier.TelegramRoute, 0, len(cfg.Telegram.Routes))
			for _, r := range cfg.Telegram.Routes {
				routes = append(routes, notifier.TelegramRoute{Profile: r.Profile, Account: r.Account, ChatID: r.ChatID})
			}
			sinks = append(sinks, notifier.NewTelegramSink(notifier.TelegramSinkConfig{
				Routes:              routes,
				DefaultChatID:       cfg.Telegram.DefaultChatID,
				DisableNotification: cfg.Telegram.DisableNotification,
			}, tgClient))
		}
	}

	if cfg.Slack.Enabled {
		routes, defaultURL, ok := resolveSlackURLs(cfg.Slack, log)
		if !ok {
			log.Warn("slack notifier disabled: no usable webhook url",
				slog.String(logging.FieldComponent, logging.ComponentServer),
			)
		} else {
			sinks = append(sinks, notifier.NewSlackSink(notifier.SlackSinkConfig{
				Routes:            routes,
				DefaultWebhookURL: defaultURL,
			}, slack.New()))
		}
	}

	if cfg.Discord.Enabled {
		routes, defaultURL, ok := resolveURLEnvRoutes(
			cfg.Discord.Routes, cfg.Discord.DefaultWebhookURLEnv, "discord", log,
			func(r config.DiscordRoute) (string, string, string) {
				return r.Profile, r.Account, r.WebhookURLEnv
			})
		if !ok {
			log.Warn("discord notifier disabled: no usable webhook url",
				slog.String(logging.FieldComponent, logging.ComponentServer))
		} else {
			drs := make([]notifier.DiscordRoute, 0, len(routes))
			for _, r := range routes {
				drs = append(drs, notifier.DiscordRoute{Profile: r.profile, Account: r.account, WebhookURL: r.url})
			}
			sinks = append(sinks, notifier.NewDiscordSink(notifier.DiscordSinkConfig{
				Routes:            drs,
				DefaultWebhookURL: defaultURL,
			}, discord.New()))
		}
	}

	if cfg.Mattermost.Enabled {
		routes, defaultURL, ok := resolveURLEnvRoutes(
			cfg.Mattermost.Routes, cfg.Mattermost.DefaultWebhookURLEnv, "mattermost", log,
			func(r config.MattermostRoute) (string, string, string) {
				return r.Profile, r.Account, r.WebhookURLEnv
			})
		if !ok {
			log.Warn("mattermost notifier disabled: no usable webhook url",
				slog.String(logging.FieldComponent, logging.ComponentServer))
		} else {
			mrs := make([]notifier.MattermostRoute, 0, len(routes))
			for _, r := range routes {
				mrs = append(mrs, notifier.MattermostRoute{Profile: r.profile, Account: r.account, WebhookURL: r.url})
			}
			sinks = append(sinks, notifier.NewMattermostSink(notifier.MattermostSinkConfig{
				Routes:            mrs,
				DefaultWebhookURL: defaultURL,
			}, mattermost.New()))
		}
	}

	if cfg.Ntfy.Enabled {
		routes, defaultURL, ok := resolveURLEnvRoutes(
			cfg.Ntfy.Routes, cfg.Ntfy.DefaultTopicURLEnv, "ntfy", log,
			func(r config.NtfyRoute) (string, string, string) {
				return r.Profile, r.Account, r.TopicURLEnv
			})
		if !ok {
			log.Warn("ntfy notifier disabled: no usable topic url",
				slog.String(logging.FieldComponent, logging.ComponentServer))
		} else {
			nrs := make([]notifier.NtfyRoute, 0, len(routes))
			for _, r := range routes {
				nrs = append(nrs, notifier.NtfyRoute{Profile: r.profile, Account: r.account, TopicURL: r.url})
			}
			authToken := ""
			if cfg.Ntfy.AuthTokenEnv != "" {
				authToken = os.Getenv(cfg.Ntfy.AuthTokenEnv)
			}
			sinks = append(sinks, notifier.NewNtfySink(notifier.NtfySinkConfig{
				Routes:          nrs,
				DefaultTopicURL: defaultURL,
				AuthToken:       authToken,
				Priority:        cfg.Ntfy.Priority,
				Tags:            cfg.Ntfy.Tags,
			}, ntfy.New()))
		}
	}

	if cfg.Teams.Enabled {
		routes, defaultURL, ok := resolveURLEnvRoutes(
			cfg.Teams.Routes, cfg.Teams.DefaultWebhookURLEnv, "teams", log,
			func(r config.TeamsRoute) (string, string, string) {
				return r.Profile, r.Account, r.WebhookURLEnv
			})
		if !ok {
			log.Warn("teams notifier disabled: no usable webhook url",
				slog.String(logging.FieldComponent, logging.ComponentServer))
		} else {
			trs := make([]notifier.TeamsRoute, 0, len(routes))
			for _, r := range routes {
				trs = append(trs, notifier.TeamsRoute{Profile: r.profile, Account: r.account, WebhookURL: r.url})
			}
			sinks = append(sinks, notifier.NewTeamsSink(notifier.TeamsSinkConfig{
				Routes:            trs,
				DefaultWebhookURL: defaultURL,
				ThemeColor:        cfg.Teams.ThemeColor,
			}, teams.New()))
		}
	}

	return sinks
}

// resolvedRoute is the post-env-lookup form of a sink route. We keep it
// internal to this file because the public sink-specific Route types
// already cover the same shape; this is just the intermediate result of
// reading env vars.
type resolvedRoute struct {
	profile, account, url string
}

// resolveURLEnvRoutes is the generic env-var-indirection resolver shared
// by every webhook sink (slack/discord/mattermost/teams/ntfy). Each
// route has an env var that holds the actual URL; routes whose env var
// is empty are dropped (with a warning). ok=false means nothing
// resolved — neither a route nor the default — so the sink should be
// skipped entirely.
func resolveURLEnvRoutes[R any](
	routes []R,
	defaultEnvVar, sinkLabel string,
	log *slog.Logger,
	extract func(R) (profile, account, env string),
) ([]resolvedRoute, string, bool) {
	out := make([]resolvedRoute, 0, len(routes))
	any := false
	for _, r := range routes {
		profile, account, env := extract(r)
		url := os.Getenv(env)
		if url == "" {
			log.Warn(sinkLabel+" route has empty env",
				slog.String(logging.FieldComponent, logging.ComponentServer),
				slog.String("env_var", env),
				slog.String(logging.FieldProfile, profile),
				slog.String(logging.FieldAccount, account),
			)
			continue
		}
		out = append(out, resolvedRoute{profile: profile, account: account, url: url})
		any = true
	}
	defaultURL := ""
	if defaultEnvVar != "" {
		defaultURL = os.Getenv(defaultEnvVar)
		if defaultURL != "" {
			any = true
		}
	}
	return out, defaultURL, any
}

// resolveSlackURLs delegates to the shared env-var resolver; preserved as
// a thin adapter so the call site reads naturally for the original sink.
func resolveSlackURLs(cfg config.SlackConfig, log *slog.Logger) ([]notifier.SlackRoute, string, bool) {
	resolved, defaultURL, ok := resolveURLEnvRoutes(
		cfg.Routes, cfg.DefaultWebhookURLEnv, "slack", log,
		func(r config.SlackRoute) (string, string, string) {
			return r.Profile, r.Account, r.WebhookURLEnv
		})
	out := make([]notifier.SlackRoute, 0, len(resolved))
	for _, r := range resolved {
		out = append(out, notifier.SlackRoute{Profile: r.profile, Account: r.account, WebhookURL: r.url})
	}
	return out, defaultURL, ok
}

// notifierTimings picks the loop's shared interval/timeout across every
// configured sink. We pick the longest declared values — operators
// expect predictable behaviour, and a slow ticker is safer than a fast
// one against rate-limited webhook endpoints.
func notifierTimings(cfg config.NotifierConfig) (time.Duration, time.Duration) {
	intervals := []time.Duration{
		cfg.Telegram.Interval, cfg.Slack.Interval, cfg.Discord.Interval,
		cfg.Mattermost.Interval, cfg.Ntfy.Interval, cfg.Teams.Interval,
	}
	timeouts := []time.Duration{
		cfg.Telegram.ProbeTimeout, cfg.Slack.ProbeTimeout, cfg.Discord.ProbeTimeout,
		cfg.Mattermost.ProbeTimeout, cfg.Ntfy.ProbeTimeout, cfg.Teams.ProbeTimeout,
	}
	var intvl, probeTO time.Duration
	for _, d := range intervals {
		if d > intvl {
			intvl = d
		}
	}
	for _, d := range timeouts {
		if d > probeTO {
			probeTO = d
		}
	}
	return intvl, probeTO
}
