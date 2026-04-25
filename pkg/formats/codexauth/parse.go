package codexauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// fileShape mirrors codex-rs/login/src/auth/storage.rs::AuthDotJson. All
// fields are optional in source; we require enough to make a freshness
// decision (tokens.access_token) and treat the rest as metadata.
type fileShape struct {
	AuthMode      string     `json:"auth_mode,omitempty"`
	OpenAIAPIKey  string     `json:"OPENAI_API_KEY,omitempty"`
	Tokens        *tokenData `json:"tokens,omitempty"`
	LastRefresh   *time.Time `json:"last_refresh,omitempty"`
	AgentIdentity any        `json:"agent_identity,omitempty"`
}

// tokenData mirrors codex-rs/login/src/token_data.rs::TokenData. The on-disk
// shape stores id_token and access_token as raw JWT strings; codex parses
// id_token's claims at deserialize time, but for this format we only need to
// pull the `exp` claim from access_token to drive validity.
type tokenData struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

// snapshot is the package-private Snapshot implementation.
type snapshot struct {
	authMode         string
	accessToken      string
	refreshToken     string
	idToken          string
	accountID        string
	jwtExp           time.Time // exp claim of access_token JWT, zero if unparseable
	lastRefresh      time.Time // file-level last_refresh, zero if absent
	chatgptEmail     string    // pulled from id_token claims if present (for redact)
	chatgptUserID    string    // stable account id pulled from id_token claims
	chatgptAccountID string    // chatgpt_account_id claim — required by /backend-api header

	raw         []byte
	fingerprint string
	identity    string
}

func (s *snapshot) Identity() string     { return s.identity }
func (s *snapshot) ExpiresAt() time.Time { return s.jwtExp }
func (s *snapshot) Fingerprint() string  { return s.fingerprint }

func (s *snapshot) Raw() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

// Parse decodes a codex auth.json file. The minimum-viable shape is a
// non-empty `tokens.access_token`. Files that lack tokens entirely (e.g. an
// API-key-only login) are rejected because there is nothing to share — the
// API key is a static secret that doesn't go stale.
func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(raw) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty codex auth bytes")
	}

	body := raw
	if bytes.HasPrefix(body, utf8BOM) {
		body = body[len(utf8BOM):]
	}

	var f fileShape
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode codex-auth-json-format")
	}

	if f.Tokens == nil || f.Tokens.AccessToken == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing tokens.access_token")
	}
	if f.Tokens.RefreshToken == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing tokens.refresh_token")
	}

	exp, _ := jwtExp(f.Tokens.AccessToken)
	email, _ := jwtEmail(f.Tokens.IDToken)
	chatgptUserID, _ := jwtChatGPTUserID(f.Tokens.IDToken)
	chatgptAccountID, _ := jwtChatGPTAccountID(f.Tokens.IDToken)

	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)
	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])

	idSum := sha256.Sum256([]byte(f.Tokens.AccessToken))
	identity := hex.EncodeToString(idSum[:])[:16]

	var lastRefresh time.Time
	if f.LastRefresh != nil {
		lastRefresh = *f.LastRefresh
	}

	return &snapshot{
		authMode:         f.AuthMode,
		accessToken:      f.Tokens.AccessToken,
		refreshToken:     f.Tokens.RefreshToken,
		idToken:          f.Tokens.IDToken,
		accountID:        f.Tokens.AccountID,
		jwtExp:           exp,
		lastRefresh:      lastRefresh,
		chatgptEmail:     email,
		chatgptUserID:    chatgptUserID,
		chatgptAccountID: chatgptAccountID,
		raw:              rawCopy,
		fingerprint:      fp,
		identity:         identity,
	}, nil
}

// jwtChatGPTAccountID pulls chatgpt_account_id from id_token claims. The
// codex backend requires this as the ChatGPT-Account-ID header on usage
// calls; we also fall back to it for Account() when chatgpt_user_id is
// missing.
func jwtChatGPTAccountID(jwt string) (string, error) {
	if jwt == "" {
		return "", nil
	}
	claims, err := decodeJWTClaims(jwt)
	if err != nil {
		return "", err
	}
	auth, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return "", nil
	}
	if id, ok := auth["chatgpt_account_id"].(string); ok && id != "" {
		return id, nil
	}
	return "", nil
}

// jwtChatGPTUserID pulls the stable ChatGPT user id from codex's id_token.
// codex-rs/login/src/token_data.rs::parse_chatgpt_jwt_claims puts it under
// `https://api.openai.com/auth.chatgpt_user_id`. Stable across token
// refreshes; preferred over email for partitioning.
func jwtChatGPTUserID(jwt string) (string, error) {
	if jwt == "" {
		return "", nil
	}
	claims, err := decodeJWTClaims(jwt)
	if err != nil {
		return "", err
	}
	auth, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return "", nil
	}
	if id, ok := auth["chatgpt_user_id"].(string); ok && id != "" {
		return id, nil
	}
	if id, ok := auth["chatgpt_account_id"].(string); ok && id != "" {
		return id, nil
	}
	return "", nil
}

// jwtExp pulls the standard `exp` claim (Unix seconds) from a JWT. A failed
// decode returns the zero time; callers must treat that as "expiry unknown"
// rather than a parse error — codex itself tolerates malformed tokens at
// deserialize time and only acts on them when refreshing.
func jwtExp(jwt string) (time.Time, error) {
	claims, err := decodeJWTClaims(jwt)
	if err != nil {
		return time.Time{}, err
	}
	expRaw, ok := claims["exp"]
	if !ok {
		return time.Time{}, common.Wrap(nil, common.ErrParseFailed, "JWT has no exp claim")
	}
	switch v := expRaw.(type) {
	case float64:
		return time.Unix(int64(v), 0).UTC(), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return time.Time{}, common.Wrap(err, common.ErrParseFailed, "JWT exp not an integer")
		}
		return time.Unix(n, 0).UTC(), nil
	default:
		return time.Time{}, common.Wrap(nil, common.ErrParseFailed, "JWT exp has unexpected type")
	}
}

// jwtEmail pulls the email claim from codex's namespaced id_token. Codex puts
// the email under the `https://api.openai.com/profile` claim per
// codex-rs/login/src/token_data.rs::parse_chatgpt_jwt_claims.
func jwtEmail(jwt string) (string, error) {
	if jwt == "" {
		return "", nil
	}
	claims, err := decodeJWTClaims(jwt)
	if err != nil {
		return "", err
	}
	profile, ok := claims["https://api.openai.com/profile"].(map[string]any)
	if !ok {
		return "", nil
	}
	email, _ := profile["email"].(string)
	return email, nil
}

func decodeJWTClaims(jwt string) (map[string]any, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "JWT has fewer than 2 segments")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some JWTs use std base64 with padding; try that as a fallback.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, common.Wrap(err, common.ErrParseFailed, "decode JWT claims segment")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var claims map[string]any
	if err := dec.Decode(&claims); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "parse JWT claims JSON")
	}
	return claims, nil
}
