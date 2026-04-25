package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/jwtauth"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/0xc0de1ab/pangaea/internal/transport"
)

type handshakeAuth struct {
	mode        config.AuthMode
	identity    string
	peerCN      string
	jwtProfiles []string
	needsJWT    bool
}

type authenticator struct {
	mode                    config.AuthMode
	jwtSecret               []byte
	jwtIssuer               string
	jwtAudience             string
	allowFirstFrameFallback bool
	authTimeout             time.Duration
}

func newAuthenticator(cfg *config.ServerConfig) (*authenticator, error) {
	a := &authenticator{
		mode:                    cfg.AuthMode,
		allowFirstFrameFallback: configDefaultJWTFallback(cfg.JWT.AllowFirstFrameFallback),
		authTimeout:             cfg.JWT.AuthTimeout,
	}
	if cfg.AuthMode != config.AuthModeJWT {
		return a, nil
	}
	secret, err := jwtauth.LoadSecretFile(cfg.JWT.SecretKeyFile)
	if err != nil {
		return nil, err
	}
	a.jwtSecret = secret
	a.jwtIssuer = cfg.JWT.Issuer
	a.jwtAudience = cfg.JWT.Audience
	return a, nil
}

func (a *authenticator) preUpgrade(r *http.Request, profile string) (handshakeAuth, int, error) {
	if r.TLS == nil {
		return handshakeAuth{}, http.StatusBadRequest, fmt.Errorf("TLS required")
	}
	if a.mode == config.AuthModeMTLS {
		cn, err := pki.PeerCN(r.TLS)
		if err != nil {
			return handshakeAuth{}, http.StatusUnauthorized, fmt.Errorf("peer cert invalid: %w", err)
		}
		return handshakeAuth{
			mode:     config.AuthModeMTLS,
			identity: cn,
			peerCN:   cn,
		}, http.StatusSwitchingProtocols, nil
	}

	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "" {
		if a.allowFirstFrameFallback {
			return handshakeAuth{
				mode:     config.AuthModeJWT,
				needsJWT: true,
			}, http.StatusSwitchingProtocols, nil
		}
		return handshakeAuth{}, http.StatusUnauthorized, fmt.Errorf("Authorization header required")
	}
	token, err := bearerToken(authz)
	if err != nil {
		return handshakeAuth{}, http.StatusUnauthorized, err
	}
	claims, err := a.verifyJWT(token, profile)
	if err != nil {
		return handshakeAuth{}, http.StatusUnauthorized, err
	}
	return handshakeAuth{
		mode:        config.AuthModeJWT,
		identity:    claims.Subject,
		jwtProfiles: append([]string(nil), claims.Profiles...),
	}, http.StatusSwitchingProtocols, nil
}

func (a *authenticator) completeJWT(ctx context.Context, conn transport.Conn, profile string) (handshakeAuth, error) {
	env, ok := readKind(ctx, conn, transport.KindAuthJWT)
	if !ok {
		return handshakeAuth{}, errors.New(common.MsgAuthJWTRequired)
	}
	var payload transport.AuthJWT
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return handshakeAuth{}, errors.New(common.MsgAuthJWTInvalid)
	}
	claims, err := a.verifyJWT(payload.Token, profile)
	if err != nil {
		return handshakeAuth{}, errors.New(common.MsgAuthJWTInvalid)
	}
	return handshakeAuth{
		mode:        config.AuthModeJWT,
		identity:    claims.Subject,
		jwtProfiles: append([]string(nil), claims.Profiles...),
	}, nil
}

func (a *authenticator) verifyJWT(token, profile string) (*jwtauth.Claims, error) {
	claims, err := jwtauth.Verify(a.jwtSecret, token, a.jwtIssuer, a.jwtAudience, time.Now())
	if err != nil {
		return nil, err
	}
	if !claims.AllowsProfile(profile) {
		return nil, common.Wrap(nil, common.ErrCNMismatch, common.MsgJWTProfileDenied, claims.Subject, profile)
	}
	return claims, nil
}

func bearerToken(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("Authorization must be Bearer")
	}
	if parts[1] == "" {
		return "", fmt.Errorf("Authorization token is empty")
	}
	return parts[1], nil
}

func configDefaultJWTFallback(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}
