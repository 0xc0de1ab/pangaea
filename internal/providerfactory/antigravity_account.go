package providerfactory

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

var (
	antigravityEmailRE    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	antigravityBase64RE   = regexp.MustCompile(`[A-Za-z0-9+/_=\-]{32,}`)
	antigravityOAuthB64RE = regexp.MustCompile(`eWEyOS5[A-Za-z0-9+/_=\-]{64,}`)
)

func antigravityAccountFromStateFile(path string) (provider.Account, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return provider.Account{}, false
	}
	return antigravityAccountFromState(raw)
}

func antigravityAccountFromState(raw []byte) (provider.Account, bool) {
	if len(raw) == 0 {
		return provider.Account{}, false
	}
	const key = "antigravityUnifiedStateSync.userStatus"
	keyBytes := []byte(key)
	foundUserStatusKey := false
	for offset := 0; ; {
		idx := bytes.Index(raw[offset:], keyBytes)
		if idx < 0 {
			break
		}
		foundUserStatusKey = true
		idx += offset
		end := idx + 2*1024*1024
		if end > len(raw) {
			end = len(raw)
		}
		if account, ok := antigravityAccountFromUserStatusValue(raw[idx+len(keyBytes) : end]); ok {
			return account, true
		}
		offset = idx + len(keyBytes)
	}
	if foundUserStatusKey {
		return provider.Account{}, false
	}
	return antigravityAccountFromBytes(raw, false)
}

func antigravityOAuthExpiryFromStateFile(path string) (time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return time.Time{}, false
	}
	return antigravityOAuthExpiryFromState(raw)
}

func antigravityOAuthExpiryFromState(raw []byte) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
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
		if expiresAt, ok := antigravityOAuthExpiryFromBytes(raw[idx+len(keyBytes) : end]); ok {
			if expiresAt.After(best) {
				best = expiresAt
			}
		}
		offset = idx + len(keyBytes)
	}
}

func antigravityOAuthExpiryFromBytes(raw []byte) (time.Time, bool) {
	queue := [][]byte{append([]byte(nil), raw...)}
	seen := map[string]struct{}{}
	for depth := 0; depth < 3 && len(queue) > 0; depth++ {
		next := [][]byte{}
		for _, item := range queue {
			if expiresAt, ok := antigravityOAuthExpiryFromTokenInfo(item); ok {
				return expiresAt, true
			}
			for _, candidate := range antigravityOAuthB64RE.FindAll(item, -1) {
				decoded, ok := decodeAntigravityBase64(candidate)
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
			for _, candidate := range antigravityBase64RE.FindAll(item, -1) {
				decoded, ok := decodeAntigravityBase64(candidate)
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

func antigravityOAuthExpiryFromTokenInfo(raw []byte) (time.Time, bool) {
	// The VS Code state stores a nested token-info protobuf. In observed builds,
	// the access-token expiry is encoded as a google.protobuf.Timestamp-like
	// submessage following tag 4: 0x22 <len> 0x08 <unix-seconds-varint>.
	for offset := 0; ; {
		idx := bytes.Index(raw[offset:], []byte{0x22})
		if idx < 0 {
			return time.Time{}, false
		}
		idx += offset
		if idx+3 < len(raw) && raw[idx+2] == 0x08 {
			seconds, n, ok := readAntigravityVarint(raw[idx+3:])
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

func readAntigravityVarint(raw []byte) (uint64, int, bool) {
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

func antigravityAccountFromUserStatusValue(raw []byte) (provider.Account, bool) {
	loc := antigravityBase64RE.FindIndex(raw)
	if len(loc) != 2 || loc[0] > 8 {
		return provider.Account{}, false
	}
	decoded, ok := decodeAntigravityBase64(raw[loc[0]:loc[1]])
	if !ok {
		return provider.Account{}, false
	}
	return antigravityAccountFromBytes(decoded, false)
}

func antigravityAccountFromBytes(raw []byte, skipRawEmail bool) (provider.Account, bool) {
	seen := map[string]struct{}{}
	queue := [][]byte{append([]byte(nil), raw...)}
	for depth := 0; depth < 3 && len(queue) > 0; depth++ {
		next := [][]byte{}
		for _, item := range queue {
			if !(skipRawEmail && depth == 0) {
				if email := antigravityEmailRE.Find(item); len(email) > 0 {
					value := string(email)
					return provider.Account{ID: value, Display: value}, true
				}
			}
			for _, candidate := range antigravityBase64RE.FindAll(item, -1) {
				decoded, ok := decodeAntigravityBase64(candidate)
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
	return provider.Account{}, false
}

func decodeAntigravityBase64(candidate []byte) ([]byte, bool) {
	cleaned := bytes.TrimRight(candidate, "\x00")
	maxTrim := 8
	if len(cleaned) < maxTrim {
		maxTrim = len(cleaned)
	}
	for trim := 0; trim <= maxTrim; trim++ {
		if decoded, ok := decodeAntigravityBase64Exact(cleaned[:len(cleaned)-trim]); ok {
			return decoded, true
		}
	}
	return nil, false
}

func decodeAntigravityBase64Exact(cleaned []byte) ([]byte, bool) {
	if len(cleaned) == 0 {
		return nil, false
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded := make([]byte, encoding.DecodedLen(len(cleaned)))
		n, err := encoding.Decode(decoded, cleaned)
		if err == nil && n > 0 {
			return decoded[:n], true
		}
	}
	return nil, false
}
