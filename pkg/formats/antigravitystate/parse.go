package antigravitystate

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var (
	emailRE    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	base64RE   = regexp.MustCompile(`[A-Za-z0-9+/_=\-]{32,}`)
	oauthB64RE = regexp.MustCompile(`eWEyOS5[A-Za-z0-9+/_=\-]{64,}`)
	tokenRE    = regexp.MustCompile(`ya29\.[A-Za-z0-9._\-]+`)
)

type snapshot struct {
	raw         []byte
	fingerprint string
	identity    string
	expiresAt   time.Time
	account     string
	tokenTail4  string
}

func (s *snapshot) Identity() string     { return s.identity }
func (s *snapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s *snapshot) Fingerprint() string  { return s.fingerprint }

func (s *snapshot) Raw() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(raw) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty antigravity state bytes")
	}

	account, _ := accountFromState(raw)
	expiresAt, hasExpiry := oauthExpiryFromState(raw)
	token := tokenFromState(raw)
	if !hasExpiry && token == "" && account == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "state.vscdb does not contain recognizable Antigravity auth state")
	}

	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)
	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])

	identitySource := account
	if identitySource == "" {
		identitySource = token
	}
	if identitySource == "" {
		identitySource = fp
	}
	idSum := sha256.Sum256([]byte(identitySource))

	tail4 := ""
	if n := len(token); n >= 4 {
		tail4 = token[n-4:]
	} else {
		tail4 = token
	}

	return &snapshot{
		raw:         rawCopy,
		fingerprint: fp,
		identity:    hex.EncodeToString(idSum[:])[:16],
		expiresAt:   expiresAt,
		account:     account,
		tokenTail4:  tail4,
	}, nil
}

func accountFromState(raw []byte) (string, bool) {
	const key = "antigravityUnifiedStateSync.userStatus"
	keyBytes := []byte(key)
	for offset := 0; ; {
		idx := bytes.Index(raw[offset:], keyBytes)
		if idx < 0 {
			break
		}
		idx += offset
		end := idx + 64*1024
		if end > len(raw) {
			end = len(raw)
		}
		if account, ok := accountFromUserStatusValue(raw[idx+len(keyBytes) : end]); ok {
			return account, true
		}
		offset = idx + len(keyBytes)
	}
	return accountFromBytes(raw, false)
}

func accountFromUserStatusValue(raw []byte) (string, bool) {
	loc := base64RE.FindIndex(raw)
	if len(loc) != 2 || loc[0] > 8 {
		return "", false
	}
	decoded, ok := decodeBase64(raw[loc[0]:loc[1]])
	if !ok {
		return "", false
	}
	return accountFromBytes(decoded, false)
}

func accountFromBytes(raw []byte, skipRawEmail bool) (string, bool) {
	queue := [][]byte{append([]byte(nil), raw...)}
	seen := map[string]struct{}{}
	for depth := 0; depth < 3 && len(queue) > 0; depth++ {
		next := [][]byte{}
		for _, item := range queue {
			if !skipRawEmail {
				if email := emailRE.Find(item); len(email) > 0 {
					return string(email), true
				}
			}
			for _, candidate := range base64RE.FindAll(item, -1) {
				decoded, ok := decodeBase64(candidate)
				if !ok || len(decoded) == 0 {
					continue
				}
				key := string(candidate)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				next = append(next, decoded)
			}
		}
		queue = next
	}
	return "", false
}

func oauthExpiryFromState(raw []byte) (time.Time, bool) {
	const key = "antigravityUnifiedStateSync.oauthToken"
	keyBytes := []byte(key)
	var best time.Time
	for offset := 0; ; {
		idx := bytes.Index(raw[offset:], keyBytes)
		if idx < 0 {
			if best.IsZero() {
				return time.Time{}, false
			}
			return best, true
		}
		idx += offset
		end := idx + 64*1024
		if end > len(raw) {
			end = len(raw)
		}
		if expiresAt, ok := oauthExpiryFromBytes(raw[idx+len(keyBytes) : end]); ok {
			if expiresAt.After(best) {
				best = expiresAt
			}
		}
		offset = idx + len(keyBytes)
	}
}

func oauthExpiryFromBytes(raw []byte) (time.Time, bool) {
	queue := [][]byte{append([]byte(nil), raw...)}
	seen := map[string]struct{}{}
	for depth := 0; depth < 3 && len(queue) > 0; depth++ {
		next := [][]byte{}
		for _, item := range queue {
			if expiresAt, ok := oauthExpiryFromTokenInfo(item); ok {
				return expiresAt, true
			}
			for _, candidate := range oauthB64RE.FindAll(item, -1) {
				decoded, ok := decodeBase64(candidate)
				if !ok || len(decoded) == 0 {
					continue
				}
				key := string(candidate)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				next = append(next, decoded)
			}
			for _, candidate := range base64RE.FindAll(item, -1) {
				decoded, ok := decodeBase64(candidate)
				if !ok || len(decoded) == 0 {
					continue
				}
				key := string(candidate)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				next = append(next, decoded)
			}
		}
		queue = next
	}
	return time.Time{}, false
}

func oauthExpiryFromTokenInfo(raw []byte) (time.Time, bool) {
	for offset := 0; ; {
		idx := bytes.Index(raw[offset:], []byte{0x22})
		if idx < 0 {
			return time.Time{}, false
		}
		idx += offset
		if idx+3 < len(raw) && raw[idx+2] == 0x08 {
			seconds, n, ok := readVarint(raw[idx+3:])
			if ok && n > 0 && seconds > 0 {
				expiresAt := time.Unix(int64(seconds), 0).UTC()
				if expiresAt.After(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) &&
					expiresAt.Before(time.Now().UTC().AddDate(10, 0, 0)) {
					return expiresAt, true
				}
			}
		}
		offset = idx + 1
	}
}

func tokenFromState(raw []byte) string {
	if token := tokenRE.Find(raw); len(token) > 0 {
		return string(token)
	}
	queue := [][]byte{append([]byte(nil), raw...)}
	seen := map[string]struct{}{}
	for depth := 0; depth < 3 && len(queue) > 0; depth++ {
		next := [][]byte{}
		for _, item := range queue {
			if token := tokenRE.Find(item); len(token) > 0 {
				return string(token)
			}
			for _, candidate := range oauthB64RE.FindAll(item, -1) {
				decoded, ok := decodeBase64(candidate)
				if !ok || len(decoded) == 0 {
					continue
				}
				key := string(candidate)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				next = append(next, decoded)
			}
			for _, candidate := range base64RE.FindAll(item, -1) {
				decoded, ok := decodeBase64(candidate)
				if !ok || len(decoded) == 0 {
					continue
				}
				key := string(candidate)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				next = append(next, decoded)
			}
		}
		queue = next
	}
	return ""
}

func readVarint(raw []byte) (uint64, int, bool) {
	var value uint64
	for i, b := range raw {
		if i >= 10 {
			return 0, 0, false
		}
		value |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return value, i + 1, true
		}
	}
	return 0, 0, false
}

func decodeBase64(candidate []byte) ([]byte, bool) {
	cleaned := bytes.TrimRight(candidate, "\x00")
	maxTrim := 8
	if len(cleaned) < maxTrim {
		maxTrim = len(cleaned)
	}
	for trim := 0; trim <= maxTrim; trim++ {
		if decoded, ok := decodeBase64Exact(cleaned[:len(cleaned)-trim]); ok {
			return decoded, true
		}
	}
	return nil, false
}

func decodeBase64Exact(cleaned []byte) ([]byte, bool) {
	if len(cleaned) == 0 {
		return nil, false
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(string(cleaned))
		if err == nil {
			return decoded, true
		}
	}
	return nil, false
}
