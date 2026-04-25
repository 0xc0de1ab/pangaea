package client

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/gorilla/websocket"
)

func (a *agent) loadJWTToken() (string, error) {
	if a.jwtTokenOverride != "" {
		return strings.TrimSpace(a.jwtTokenOverride), nil
	}
	if env := strings.TrimSpace(a.cfg.JWT.TokenEnv); env != "" {
		token := strings.TrimSpace(os.Getenv(env))
		if token == "" {
			return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT token env %q is empty", env)
		}
		return token, nil
	}
	raw, err := os.ReadFile(a.cfg.JWT.TokenFile)
	if err != nil {
		return "", common.Wrap(err, common.ErrConfigInvalid, "read JWT token file %s", a.cfg.JWT.TokenFile)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT token file %s is empty", a.cfg.JWT.TokenFile)
	}
	return token, nil
}

func (a *agent) effectiveJWTSendVia() string {
	if a.cfg.AuthMode != config.AuthModeJWT {
		return ""
	}
	if a.cfg.JWT.SendVia == config.JWTSendViaAuto && a.jwtUseFirstFrame {
		return config.JWTSendViaFirstFrame
	}
	return a.cfg.JWT.SendVia
}

func (a *agent) dialHeaders(token string) http.Header {
	if a.cfg.AuthMode != config.AuthModeJWT {
		return nil
	}
	if via := a.effectiveJWTSendVia(); via == config.JWTSendViaHeader || via == config.JWTSendViaAuto {
		h := make(http.Header, 1)
		h.Set("Authorization", "Bearer "+token)
		return h
	}
	return nil
}

func shouldSwitchJWTToFirstFrame(err error) bool {
	var closeErr *websocket.CloseError
	return errors.As(err, &closeErr) &&
		closeErr.Code == websocket.ClosePolicyViolation &&
		closeErr.Text == common.MsgAuthJWTRequired
}
