package reversebridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

type Options struct {
	SocketPath string
}

func Run(ctx context.Context, serverCfg *config.ServerConfig, ps config.ProfileStore, opts Options, log *slog.Logger) error {
	if err := validate(serverCfg, opts); err != nil {
		return err
	}
	targets := collectTargets(ps)
	if len(targets) == 0 {
		return common.Wrap(nil, common.ErrConfigInvalid, "no reverse_targets configured")
	}

	eg, egCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		target := target
		eg.Go(func() error {
			return bridgeLoop(egCtx, serverCfg, opts, target, log.With(
				slog.String("profile", target.Profile),
				slog.String("node_id", target.NodeID),
			))
		})
	}
	err := eg.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type bridgeTarget struct {
	Profile   string
	NodeID    string
	Transport config.ReverseTransport
	URL       string
}

func collectTargets(ps config.ProfileStore) []bridgeTarget {
	var out []bridgeTarget
	for _, p := range ps.List() {
		for _, t := range p.ReverseTargets {
			out = append(out, bridgeTarget{
				Profile:   p.Name,
				NodeID:    t.NodeID,
				Transport: t.Transport,
				URL:       t.URL,
			})
		}
	}
	return out
}

func validate(serverCfg *config.ServerConfig, opts Options) error {
	if opts.SocketPath == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "reverse connector socket path is required")
	}
	if !serverCfg.SelfNode.Enabled {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.self_node.enabled must be true for reverse connector")
	}
	if serverCfg.SelfNode.ClientCert == "" || serverCfg.SelfNode.ClientKey == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.self_node client cert/key are required for reverse connector")
	}
	return nil
}

func bridgeLoop(ctx context.Context, serverCfg *config.ServerConfig, opts Options, target bridgeTarget, log *slog.Logger) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := bridgeOnce(ctx, serverCfg, opts, target)
		if ctx.Err() != nil {
			return nil
		}
		attempt++
		delay := backoff(attempt)
		log.Warn("reverse bridge reconnect scheduled",
			slog.Int("attempt", attempt),
			slog.Duration("delay", delay),
			slog.String("reason", errString(err)),
			slog.String("transport", string(target.Transport)),
			slog.String("target", targetDescriptor(target)),
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func bridgeOnce(ctx context.Context, serverCfg *config.ServerConfig, opts Options, target bridgeTarget) error {
	if target.Transport == config.ReverseTransportSSH {
		return bridgeOnceSSH(ctx, serverCfg, opts, target)
	}
	return bridgeOnceDirect(ctx, serverCfg, opts, target)
}

func bridgeOnceDirect(ctx context.Context, serverCfg *config.ServerConfig, opts Options, target bridgeTarget) error {
	remoteConn, err := dialRemote(ctx, serverCfg, target)
	if err != nil {
		return err
	}
	defer remoteConn.Close()

	localConn, err := dialLocalAttach(ctx, opts.SocketPath, target)
	if err != nil {
		return err
	}
	defer localConn.Close()

	errCh := make(chan error, 2)
	go proxyWebsocket(localConn, remoteConn, errCh)
	go proxyWebsocket(remoteConn, localConn, errCh)

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func dialRemote(ctx context.Context, serverCfg *config.ServerConfig, target bridgeTarget) (*websocket.Conn, error) {
	u, err := url.Parse(target.URL)
	if err != nil {
		return nil, err
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = common.DefaultReverseWSPath + target.Profile
	}
	tlsCfg, err := pki.ClientTLSConfig(
		serverCfg.PKI.CACert,
		serverCfg.SelfNode.ClientCert,
		serverCfg.SelfNode.ClientKey,
		u.Hostname(),
	)
	if err != nil {
		return nil, err
	}
	d := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: common.WriteTimeout,
	}
	conn, _, err := d.DialContext(ctx, u.String(), nil)
	return conn, err
}

func dialLocalAttach(ctx context.Context, socketPath string, target bridgeTarget) (*websocket.Conn, error) {
	d := &websocket.Dialer{
		HandshakeTimeout: common.WriteTimeout,
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	headers := http.Header{}
	headers.Set("X-Pangaea-Node-ID", target.NodeID)
	headers.Set("X-Pangaea-Identity", target.NodeID)
	headers.Set("X-Pangaea-Peer-CN", target.NodeID)
	headers.Set("X-Pangaea-Auth-Mode", string(config.AuthModeMTLS))
	u := "ws://unix" + common.DefaultAttachWSPath + target.Profile
	conn, _, err := d.DialContext(ctx, u, headers)
	return conn, err
}

func proxyWebsocket(dst, src *websocket.Conn, errCh chan<- error) {
	for {
		msgType, payload, err := src.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if err := dst.WriteMessage(msgType, payload); err != nil {
			errCh <- err
			return
		}
	}
}

func backoff(attempt int) time.Duration {
	delay := common.ReconnectInitial
	for i := 1; i < attempt && delay < common.ReconnectMax; i++ {
		delay *= 2
	}
	if delay > common.ReconnectMax {
		delay = common.ReconnectMax
	}
	return delay
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func targetDescriptor(target bridgeTarget) string {
	if target.Transport == config.ReverseTransportSSH {
		return target.NodeID
	}
	return target.URL
}

func ProfileSet(ps config.ProfileStore, filter string) (config.ProfileStore, error) {
	if strings.TrimSpace(filter) == "" {
		return ps, nil
	}
	want := map[string]bool{}
	for _, part := range strings.Split(filter, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			want[part] = true
		}
	}
	out := &config.ProfilesFile{}
	for _, p := range ps.List() {
		if want[p.Name] {
			out.Profiles = append(out.Profiles, p)
			delete(want, p.Name)
		}
	}
	if len(want) > 0 {
		var missing []string
		for name := range want {
			missing = append(missing, name)
		}
		return nil, fmt.Errorf("%w: reverse profile filter names not found: %v", common.ErrConfigInvalid, missing)
	}
	return config.NewProfileStore(out), nil
}
