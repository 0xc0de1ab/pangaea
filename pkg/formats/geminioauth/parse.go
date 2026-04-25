package geminioauth

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

// fileShape mirrors google-auth-library Credentials. All fields nullable;
// pointers let us distinguish "absent" from "empty string" / "zero".
type fileShape struct {
	AccessToken  *string `json:"access_token"`
	RefreshToken *string `json:"refresh_token"`
	Scope        *string `json:"scope"`
	TokenType    *string `json:"token_type"`
	IDToken      *string `json:"id_token"`
	ExpiryDate   *int64  `json:"expiry_date"`
}

// snapshot is the package-private Snapshot implementation.
type snapshot struct {
	accessToken  string
	refreshToken string
	idToken      string
	tokenType    string
	scopes       []string
	expiryDate   time.Time // converted from expiry_date epoch ms; zero if absent

	// Account identifiers pulled from id_token claims at parse time. sub is
	// Google's stable user identifier; email is a friendlier fallback.
	googleSub   string
	googleEmail string

	raw         []byte
	fingerprint string
	identity    string
}

func (s *snapshot) Identity() string     { return s.identity }
func (s *snapshot) ExpiresAt() time.Time { return s.expiryDate }
func (s *snapshot) Fingerprint() string  { return s.fingerprint }

func (s *snapshot) Raw() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

// Parse decodes a gemini oauth_creds.json file. The minimum viable shape is
// non-empty access_token + refresh_token. expiry_date is allowed to be absent
// at parse time — Validate decides expired vs ok. Files without a refresh
// token are rejected because, like codex's API-key mode, there is no way for
// a peer to keep the token alive on its own.
func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(raw) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty gemini oauth bytes")
	}

	body := bytes.TrimPrefix(raw, utf8BOM)

	var f fileShape
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode gemini-oauth-creds-json-format")
	}
	if f.AccessToken == nil || *f.AccessToken == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing access_token")
	}
	if f.RefreshToken == nil || *f.RefreshToken == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing refresh_token")
	}

	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)
	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])

	idSum := sha256.Sum256([]byte(*f.AccessToken))
	identity := hex.EncodeToString(idSum[:])[:16]

	s := &snapshot{
		accessToken:  *f.AccessToken,
		refreshToken: *f.RefreshToken,
		raw:          rawCopy,
		fingerprint:  fp,
		identity:     identity,
	}
	if f.IDToken != nil {
		s.idToken = *f.IDToken
		s.googleSub, s.googleEmail = decodeIDTokenAccount(*f.IDToken)
	}
	if f.TokenType != nil {
		s.tokenType = *f.TokenType
	}
	if f.Scope != nil && *f.Scope != "" {
		s.scopes = strings.Fields(*f.Scope)
	}
	if f.ExpiryDate != nil && *f.ExpiryDate > 0 {
		s.expiryDate = time.UnixMilli(*f.ExpiryDate).UTC()
	}
	return s, nil
}

// decodeIDTokenAccount returns (sub, email) from a Google id_token JWT.
// Failures yield empty strings — id_token is optional and tolerating a bad
// JWT is preferable to refusing the whole snapshot.
func decodeIDTokenAccount(jwt string) (string, string) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", ""
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	return sub, email
}
