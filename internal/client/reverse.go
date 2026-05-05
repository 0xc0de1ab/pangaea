package client

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"golang.org/x/sync/errgroup"
)

// RunReverse starts one reverse-client listener that accepts outbound
// connections from the separate reverse-connector process running on the
// server host. The application protocol on the accepted WebSocket is
// identical to the direct client->server session; only the TCP initiation
// direction changes.
func RunReverse(ctx context.Context, cfg *config.ClientConfig, opts Options, log *slog.Logger) error {
	if err := validateReverseConfig(cfg); err != nil {
		return err
	}

	agentVer := opts.AgentVersion
	if agentVer == "" {
		agentVer = "dev"
	}
	agents, err := buildAgents(cfg, nil, agentVer, opts, log)
	if err != nil {
		return err
	}
	byProfile := make(map[string]*reverseAgent, len(agents))
	for _, ag := range agents {
		byProfile[ag.profile] = &reverseAgent{agent: ag}
	}

	tlsCfg, err := pki.ServerTLSConfig(
		cfg.Reverse.PKI.CACert,
		cfg.Reverse.PKI.ServerCert,
		cfg.Reverse.PKI.ServerKey,
		true,
	)
	if err != nil {
		return err
	}

	eg, egCtx := errgroup.WithContext(ctx)
	if maintainer := newCLIUpgradeMaintainer(cfg.Maintenance.CLIUpgrade, agents, log); maintainer != nil {
		eg.Go(func() error { return maintainer.run(egCtx) })
	}
	for _, ra := range byProfile {
		ra := ra
		eg.Go(func() error { return ra.agent.startStatePumps(egCtx) })
	}

	srv := &http.Server{
		TLSConfig: tlsCfg,
		Handler: reverseMux(
			byProfile,
			cfg.Reverse.AllowedPeers,
			log,
		),
		ReadHeaderTimeout: common.WriteTimeout,
	}
	ln, err := net.Listen("tcp", cfg.Reverse.Listen)
	if err != nil {
		return err
	}
	if opts.OnReverseListening != nil {
		opts.OnReverseListening(ln.Addr().String())
	}
	eg.Go(func() error {
		if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
	go func() {
		<-egCtx.Done()
		_ = ln.Close()
		_ = srv.Shutdown(context.Background())
	}()

	if err := eg.Wait(); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

type reverseAgent struct {
	agent *agent

	mu   sync.Mutex
	conn transport.Conn
}

func (r *reverseAgent) serve(ctx context.Context, conn transport.Conn) error {
	r.mu.Lock()
	prev := r.conn
	r.conn = conn
	r.mu.Unlock()
	if prev != nil {
		_ = prev.Close(1008, common.ErrSessionDisplaced.Error())
	}
	defer func() {
		r.mu.Lock()
		if r.conn == conn {
			r.conn = nil
		}
		r.mu.Unlock()
	}()
	return r.agent.session(ctx, conn)
}

func reverseMux(byProfile map[string]*reverseAgent, allowedPeers []string, log *slog.Logger) http.Handler {
	allowed := make(map[string]struct{}, len(allowedPeers))
	for _, peer := range allowedPeers {
		allowed[peer] = struct{}{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc(common.DefaultReverseWSPath, func(w http.ResponseWriter, r *http.Request) {
		profile := strings.TrimPrefix(r.URL.Path, common.DefaultReverseWSPath)
		profile = strings.TrimSpace(profile)
		ra, ok := byProfile[profile]
		if !ok {
			http.Error(w, "unknown profile", http.StatusNotFound)
			return
		}
		if r.TLS == nil {
			http.Error(w, "TLS required", http.StatusBadRequest)
			return
		}
		cn, err := pki.PeerCN(r.TLS)
		if err != nil {
			http.Error(w, "peer cert invalid", http.StatusUnauthorized)
			return
		}
		if _, ok := allowed[cn]; !ok {
			http.Error(w, "peer not allowed", http.StatusForbidden)
			return
		}
		conn, err := transport.Upgrade(w, r, cn)
		if err != nil {
			log.Warn("reverse-client upgrade failed",
				slog.String("profile", profile),
				slog.String("peer_cn", cn),
				slog.String("reason", err.Error()),
			)
			return
		}
		if err := ra.serve(r.Context(), conn); err != nil && err != context.Canceled {
			log.Warn("reverse-client session ended",
				slog.String("profile", profile),
				slog.String("peer_cn", cn),
				slog.String("reason", err.Error()),
			)
		}
	})
	return mux
}

func validateReverseConfig(cfg *config.ClientConfig) error {
	if cfg.Reverse.Listen == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.reverse.listen is required")
	}
	if cfg.Reverse.PKI.CACert == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.reverse.pki.ca_cert is required")
	}
	if cfg.Reverse.PKI.ServerCert == "" || cfg.Reverse.PKI.ServerKey == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.reverse.pki.{server_cert,server_key} are required")
	}
	if len(cfg.Reverse.AllowedPeers) == 0 {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.reverse.allowed_peers must not be empty")
	}
	return nil
}
