package server

import (
	"context"
	"crypto/tls"
	"log/slog"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/client"
	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/jwtauth"
	"github.com/0xc0de1ab/pangaea/internal/pki"
)

// SelfClientFactory builds a Run-compatible closure for a given profile. The
// cmd layer calls this once per --also-client profile and hands the resulting
// closure to server.Options.SelfClientFn. The server's errgroup then invokes
// each closure once the listener is ready.
//
// The resolved profile's Dir and optional WatchFiles drive the embedded
// agent's watcher. The
// self-client always dials the server's own listen URL under the "localhost"
// SAN, authenticating with the dedicated self_node client keypair configured
// in server.yaml.
func SelfClientFactory(
	serverCfg *config.ServerConfig,
	ps config.ProfileStore,
	log *slog.Logger,
	agentVersion string,
) func(ctx context.Context, profile string) error {
	return func(ctx context.Context, profileName string) error {
		p, ok := ps.Get(profileName)
		if !ok {
			return common.Wrap(nil, common.ErrProfileNotFound, common.MsgProfileUnknown, profileName)
		}

		// Build an inline ClientConfig. The server URL is the HTTPS listen
		// address under wss://localhost:<port> so the SAN "localhost"
		// matches. We expect server.yaml to list localhost among the server
		// cert SANs.
		serverURL := "wss://" + listenToLocalhost(serverCfg.Listen)
		clientCfg := &config.ClientConfig{
			Server:   serverURL,
			AuthMode: serverCfg.AuthMode,
			NodeID:   "server-self",
			Profiles: []config.ProfileBinding{{
				Name:       profileName,
				Format:     p.Format,
				Dir:        p.Dir,
				WatchFiles: append([]string(nil), p.WatchFiles...),
			}},
			PKI: config.ClientPKIPaths{
				CACert:     serverCfg.PKI.CACert,
				ClientCert: serverCfg.SelfNode.ClientCert,
				ClientKey:  serverCfg.SelfNode.ClientKey,
			},
		}

		tlsCfg, err := pki.ClientTLSConfig(
			clientCfg.PKI.CACert,
			clientCfg.PKI.ClientCert,
			clientCfg.PKI.ClientKey,
			"localhost",
		)
		if err != nil {
			return err
		}
		// mTLS protects loopback the same as remote — operators get a single
		// model to reason about. (specs §15.3)
		_ = (*tls.Config)(tlsCfg)

		var jwtToken string
		if serverCfg.AuthMode == config.AuthModeJWT {
			secret, err := jwtauth.LoadSecretFile(serverCfg.JWT.SecretKeyFile)
			if err != nil {
				return err
			}
			token, err := jwtauth.Issue(
				secret,
				clientCfg.NodeID,
				[]string{profileName},
				serverCfg.JWT.Issuer,
				serverCfg.JWT.Audience,
				time.Now(),
				3650*24*time.Hour,
			)
			if err != nil {
				return err
			}
			jwtToken = token
		}

		return client.Run(ctx, clientCfg, client.Options{
			AgentVersion: agentVersion,
			TLSConfig:    tlsCfg,
			JWTToken:     jwtToken,
		}, log)
	}
}

// listenToLocalhost rewrites "0.0.0.0:8443" / ":8443" / "[::]:8443" style
// listen strings to "localhost:8443" for use with the server's own cert SAN.
// Listen strings that already carry a hostname are returned unchanged.
func listenToLocalhost(listen string) string {
	// Split on the last ':' to preserve IPv6 hosts.
	host, port := splitHostPort(listen)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return "localhost:" + port
	}
	return listen
}

func splitHostPort(s string) (host, port string) {
	// simple split — avoid net.SplitHostPort returning an error on ":8443".
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return "", s
}
