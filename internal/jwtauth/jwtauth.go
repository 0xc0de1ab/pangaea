package jwtauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/golang-jwt/jwt/v5"
)

const (
	secretSize    = 32
	secretMode    = 0o600
	defaultLeeway = 30 * time.Second
)

type Claims struct {
	Profiles []string `json:"profiles"`
	jwt.RegisteredClaims
}

func (c *Claims) AllowsProfile(name string) bool {
	return slices.Contains(c.Profiles, name)
}

func GenerateSecret() ([]byte, error) {
	secret := make([]byte, secretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "generate JWT secret")
	}
	return secret, nil
}

func EncodeSecret(secret []byte) string {
	return base64.RawURLEncoding.EncodeToString(secret)
}

func WriteSecretFile(path string, secret []byte) error {
	if len(secret) == 0 {
		return common.Wrap(nil, common.ErrConfigInvalid, "JWT secret must not be empty")
	}
	encoded := EncodeSecret(secret) + "\n"
	if err := os.WriteFile(path, []byte(encoded), secretMode); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "write JWT secret %s", path)
	}
	if err := os.Chmod(path, secretMode); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "chmod JWT secret %s", path)
	}
	return nil
}

func LoadSecretFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read JWT secret %s", path)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "JWT secret file %s is empty", path)
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil && len(decoded) > 0 {
		return decoded, nil
	}
	return []byte(trimmed), nil
}

func Issue(secret []byte, subject string, profiles []string, issuer, audience string, now time.Time, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT secret must not be empty")
	}
	if subject == "" {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT subject must not be empty")
	}
	profiles = normalizeProfiles(profiles)
	if len(profiles) == 0 {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT profiles must not be empty")
	}
	if issuer == "" || audience == "" {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT issuer and audience are required")
	}
	if ttl <= 0 {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "JWT ttl must be positive")
	}
	claims := Claims{
		Profiles: profiles,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuer,
			Audience:  []string{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", common.Wrap(err, common.ErrConfigInvalid, "sign JWT")
	}
	return signed, nil
}

func Verify(secret []byte, token, issuer, audience string, now time.Time) (*Claims, error) {
	if len(secret) == 0 {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "JWT secret must not be empty")
	}
	if strings.TrimSpace(token) == "" {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "JWT token must not be empty")
	}
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(tok *jwt.Token) (any, error) {
		alg := "<nil>"
		if tok.Method != nil {
			alg = tok.Method.Alg()
		}
		if tok.Method == nil || alg != jwt.SigningMethodHS256.Alg() {
			return nil, common.Wrap(nil, common.ErrConfigInvalid, "unexpected JWT signing method %q", alg)
		}
		return secret, nil
	},
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithLeeway(defaultLeeway),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "verify JWT")
	}
	if !parsed.Valid {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "invalid JWT")
	}
	claims.Profiles = normalizeProfiles(claims.Profiles)
	switch {
	case claims.Subject == "":
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "JWT subject is required")
	case len(claims.Profiles) == 0:
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "JWT profiles must not be empty")
	}
	return claims, nil
}

func normalizeProfiles(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

var _ = errors.Is
