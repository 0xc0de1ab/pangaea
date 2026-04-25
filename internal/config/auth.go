package config

import (
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

type AuthMode string

const (
	AuthModeMTLS AuthMode = "mtls"
	AuthModeJWT  AuthMode = "jwt"
)

type JWTServerConfig struct {
	SecretKeyFile           string        `yaml:"secret_key_file"`
	Issuer                  string        `yaml:"issuer"`
	Audience                string        `yaml:"audience"`
	AllowFirstFrameFallback *bool         `yaml:"allow_first_frame_fallback,omitempty"`
	AuthTimeout             time.Duration `yaml:"-"`
	AuthTimeoutRaw          string        `yaml:"auth_timeout"`
}

type JWTClientConfig struct {
	TokenEnv  string `yaml:"token_env"`
	TokenFile string `yaml:"token_file"`
	SendVia   string `yaml:"send_via"`
}

const (
	JWTSendViaAuto       = "auto"
	JWTSendViaHeader     = "header"
	JWTSendViaFirstFrame = "first_frame"
)

func normalizeAuthMode(mode AuthMode) (AuthMode, error) {
	if mode == "" {
		return AuthModeMTLS, nil
	}
	switch mode {
	case AuthModeMTLS, AuthModeJWT:
		return mode, nil
	default:
		return "", common.Wrap(nil, common.ErrConfigInvalid, "auth_mode %q must be %q or %q", mode, AuthModeMTLS, AuthModeJWT)
	}
}

func normalizeJWTSendVia(v string) (string, error) {
	if v == "" {
		return JWTSendViaAuto, nil
	}
	switch v {
	case JWTSendViaAuto, JWTSendViaHeader, JWTSendViaFirstFrame:
		return v, nil
	default:
		return "", common.Wrap(nil, common.ErrConfigInvalid, "jwt.send_via %q must be %q, %q, or %q", v, JWTSendViaAuto, JWTSendViaHeader, JWTSendViaFirstFrame)
	}
}

func defaultJWTFirstFrameFallback(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}
