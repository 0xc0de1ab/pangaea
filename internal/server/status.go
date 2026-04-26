package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
)

// statusReport is the JSON shape emitted by the /status endpoint. It is
// read-only and intentionally redaction-safe — it must not include raw
// credential bytes.
type statusReport struct {
	Version  string          `json:"version"`
	Profiles []profileStatus `json:"profiles"`
}

type profileStatus struct {
	Name     string          `json:"name"`
	Format   string          `json:"format"`
	Sessions []sessionStatus `json:"sessions"`
}

type sessionStatus struct {
	NodeID   string `json:"node_id"`
	PeerCN   string `json:"peer_cn,omitempty"`
	Identity string `json:"identity,omitempty"`
	AuthMode string `json:"auth_mode,omitempty"`
}

// runStatusEndpoint serves an HTTP JSON status API on a unix socket. The
// socket's parent directory must exist; the socket file is unlinked at
// startup if stale.
func runStatusEndpoint(ctx context.Context, sockPath string, h *Hub, ps config.ProfileStore, version string, log *slog.Logger) error {
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "status socket parent dir")
	}
	if _, err := os.Stat(sockPath); err == nil {
		if err := os.Remove(sockPath); err != nil {
			return common.Wrap(err, common.ErrConfigInvalid, "remove stale status socket %q", sockPath)
		}
	}
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "listen unix %q", sockPath)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = l.Close()
		return common.Wrap(err, common.ErrConfigInvalid, "chmod status socket")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, h, ps, version)
	})
	mux.HandleFunc(common.DefaultAttachWSPath, func(w http.ResponseWriter, r *http.Request) {
		profile := strings.TrimPrefix(r.URL.Path, common.DefaultAttachWSPath)
		profile = strings.TrimSpace(profile)
		if profile == "" {
			http.Error(w, "profile required", http.StatusBadRequest)
			return
		}
		if _, ok := ps.Get(profile); !ok {
			http.Error(w, "unknown profile", http.StatusNotFound)
			return
		}
		hs, err := attachHandshakeFromHeaders(r.Header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		conn, err := transport.Upgrade(w, r, hs.peerCN)
		if err != nil {
			log.Warn("attach upgrade failed",
				slog.String(logging.FieldComponent, logging.ComponentServer),
				slog.String(logging.FieldProfile, profile),
				slog.String(logging.FieldReason, err.Error()),
			)
			return
		}
		serveConn(r.Context(), h, profile, version, nil, hs, conn, "unix-attach", log)
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(l) }()

	log.Info("status endpoint listening",
		slog.String(logging.FieldComponent, logging.ComponentServer),
		slog.String(logging.FieldEvent, logging.EvtStartup),
		slog.String("socket", sockPath),
	)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = os.Remove(sockPath)
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func attachHandshakeFromHeaders(h http.Header) (handshakeAuth, error) {
	nodeID := strings.TrimSpace(h.Get("X-Pangaea-Node-ID"))
	identity := strings.TrimSpace(h.Get("X-Pangaea-Identity"))
	peerCN := strings.TrimSpace(h.Get("X-Pangaea-Peer-CN"))
	mode := strings.TrimSpace(h.Get("X-Pangaea-Auth-Mode"))
	if nodeID == "" {
		return handshakeAuth{}, common.Wrap(nil, common.ErrConfigInvalid, "X-Pangaea-Node-ID header is required")
	}
	if identity == "" {
		identity = nodeID
	}
	if peerCN == "" {
		peerCN = identity
	}
	if mode == "" {
		mode = string(config.AuthModeMTLS)
	}
	switch config.AuthMode(mode) {
	case config.AuthModeMTLS, config.AuthModeJWT:
	default:
		return handshakeAuth{}, common.Wrap(nil, common.ErrConfigInvalid, "unsupported attach auth mode %q", mode)
	}
	return handshakeAuth{
		mode:     config.AuthMode(mode),
		identity: identity,
		peerCN:   peerCN,
	}, nil
}

// writeStatus renders the per-profile session view.
func writeStatus(w http.ResponseWriter, h *Hub, ps config.ProfileStore, version string) {
	rep := statusReport{Version: version}
	for _, p := range ps.List() {
		pstat := profileStatus{Name: p.Name, Format: p.Format}
		h.mu.Lock()
		ph, ok := h.byProfile[p.Name]
		h.mu.Unlock()
		if ok {
			for _, s := range ph.snapshotSessions() {
				pstat.Sessions = append(pstat.Sessions, sessionStatus{
					NodeID:   s.nodeID,
					PeerCN:   s.peerCN,
					Identity: s.identity,
					AuthMode: string(s.authMode),
				})
			}
		}
		rep.Profiles = append(rep.Profiles, pstat)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}
