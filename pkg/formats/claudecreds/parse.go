package claudecreds

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// utf8BOM is the byte-order mark sometimes prepended by Windows tooling.
// We accept it transparently because end-users may have edited the
// credentials file with a BOM-emitting editor.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// oauthBlock matches the §9.1 schema. JSON tags accommodate the canonical
// camelCase keys; we apply a manual fallback for the legacy snake_case
// `access_token` key inside Parse.
type oauthBlock struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"`
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`

	// LegacyAccessToken captures the historical snake_case spelling so the
	// format can read older files without forcing users to migrate.
	LegacyAccessToken string `json:"access_token"`
}

type fileShape struct {
	ClaudeAiOauth oauthBlock `json:"claudeAiOauth"`
}

// snapshot is the package-private Snapshot implementation. It carries the
// parsed OAuth fields plus a defensive copy of the original bytes and a
// pre-computed fingerprint so repeated callers don't pay the hash cost.
type snapshot struct {
	accessToken      string
	refreshToken     string
	expiresAt        time.Time
	scopes           []string
	subscriptionType string
	rateLimitTier    string

	raw         []byte
	fingerprint string
	identity    string
}

func (s *snapshot) Identity() string     { return s.identity }
func (s *snapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s *snapshot) Fingerprint() string  { return s.fingerprint }

// Raw returns a defensive copy. Callers may safeio.Zeroize the result without
// invalidating the snapshot's internal state.
func (s *snapshot) Raw() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

// Parse decodes a credential file. It accepts the canonical schema, the
// legacy `access_token` spelling, and an optional UTF-8 BOM. Past expiresAt is
// allowed at parse time — Validate decides expired/ok.
func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(raw) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty credential bytes")
	}

	// Strip an optional UTF-8 BOM.
	body := raw
	if bytes.HasPrefix(body, utf8BOM) {
		body = body[len(utf8BOM):]
	}

	var f fileShape
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode claude-credentials-json-format")
	}

	access := f.ClaudeAiOauth.AccessToken
	if access == "" {
		// Compatibility fallback: older Claude CLI builds wrote `access_token`.
		access = f.ClaudeAiOauth.LegacyAccessToken
	}
	if access == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing claudeAiOauth.accessToken")
	}
	if f.ClaudeAiOauth.RefreshToken == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing claudeAiOauth.refreshToken")
	}
	if f.ClaudeAiOauth.ExpiresAt == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing or zero claudeAiOauth.expiresAt")
	}

	// Defensive copy of the input so the snapshot owns its raw bytes.
	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])

	idSum := sha256.Sum256([]byte(access))
	identity := hex.EncodeToString(idSum[:])[:16]

	scopesCopy := append([]string(nil), f.ClaudeAiOauth.Scopes...)

	return &snapshot{
		accessToken:      access,
		refreshToken:     f.ClaudeAiOauth.RefreshToken,
		expiresAt:        time.UnixMilli(f.ClaudeAiOauth.ExpiresAt),
		scopes:           scopesCopy,
		subscriptionType: f.ClaudeAiOauth.SubscriptionType,
		rateLimitTier:    f.ClaudeAiOauth.RateLimitTier,
		raw:              rawCopy,
		fingerprint:      fp,
		identity:         identity,
	}, nil
}
