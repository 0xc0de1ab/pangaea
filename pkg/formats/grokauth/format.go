package grokauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const (
	// FormatName is the registry key for Grok Build's ~/.grok/auth.json.
	FormatName = "grok-auth-json-format"

	strategyOpaque = "opaque"
)

var (
	_ formats.AccountAware        = Format{}
	_ formats.AccountDisplayAware = Format{}
	_ formats.DirResolver         = Format{}
)

type Format struct{}

type authEntry struct {
	Key   string `json:"key"`
	Email string `json:"email"`
	User  string `json:"user"`
}

type snapshot struct {
	raw         []byte
	fingerprint string
	identity    string
	scope       string
	tokenTail4  string
	account     string
}

func init() {
	formats.Register(Format{})
}

func (Format) Name() string { return FormatName }

func (Format) Strategies() []string { return []string{strategyOpaque} }

func (Format) CredentialPath(dir string) string {
	if filepath.Base(filepath.Clean(dir)) == ".grok" {
		return filepath.Join(dir, "auth.json")
	}
	return filepath.Join(dir, ".grok", "auth.json")
}

func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty grok auth json")
	}
	var doc map[string]authEntry
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode grok-auth-json-format")
	}
	scope, entry := selectAuthEntry(doc)
	token := strings.TrimSpace(entry.Key)
	if token == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing grok auth token")
	}
	rawCopy := append([]byte(nil), raw...)
	fpSum := sha256.Sum256(rawCopy)
	fp := hex.EncodeToString(fpSum[:])
	account := strings.TrimSpace(firstNonEmpty(entry.Email, entry.User))
	idSeed := strings.Join([]string{scope, account, token}, "\x00")
	idSum := sha256.Sum256([]byte(idSeed))
	return &snapshot{
		raw:         rawCopy,
		fingerprint: fp,
		identity:    "grok:" + hex.EncodeToString(idSum[:])[:16],
		scope:       scope,
		tokenTail4:  tokenTail(token),
		account:     account,
	}, nil
}

func (Format) Validate(_ context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	checkedAt := time.Now().UTC()
	if opts.Clock != nil {
		checkedAt = opts.Clock()
	}
	gs, ok := snap.(*snapshot)
	if !ok || gs == nil || strings.TrimSpace(gs.tokenTail4) == "" {
		return formats.ValidationResult{Status: formats.StatusParseError, Detail: "missing grok auth token", CheckedAt: checkedAt}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: checkedAt}, nil
}

func (Format) Compare(strategy string, _, _ formats.Snapshot) int {
	if strategy != strategyOpaque {
		panic(fmt.Sprintf("grokauth: unknown strategy %q", strategy))
	}
	return 0
}

func (Format) Redact(snap formats.Snapshot) formats.Summary {
	gs, ok := snap.(*snapshot)
	if !ok || gs == nil {
		fp := snap.Fingerprint()
		return formats.Summary{Identity: snap.Identity(), FingerprintShort: shortFingerprint(fp), ExpiresAt: snap.ExpiresAt()}
	}
	extra := map[string]string{}
	if gs.scope != "" {
		extra["scope"] = gs.scope
	}
	if len(extra) == 0 {
		extra = nil
	}
	return formats.Summary{
		Identity:         gs.identity,
		FingerprintShort: shortFingerprint(gs.fingerprint),
		TokenTail4:       gs.tokenTail4,
		Extra:            extra,
	}
}

func (Format) Account(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	gs, ok := snap.(*snapshot)
	if !ok || gs == nil {
		return "", nil
	}
	return firstNonEmpty(gs.account, gs.identity), nil
}

func (Format) AccountDisplay(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	gs, ok := snap.(*snapshot)
	if !ok || gs == nil {
		return "", nil
	}
	return gs.account, nil
}

func (s *snapshot) Identity() string     { return s.identity }
func (s *snapshot) ExpiresAt() time.Time { return time.Time{} }
func (s *snapshot) Fingerprint() string  { return s.fingerprint }

func (s *snapshot) Raw() []byte {
	return append([]byte(nil), s.raw...)
}

func selectAuthEntry(doc map[string]authEntry) (string, authEntry) {
	for _, preferred := range []string{
		"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828",
		"https://accounts.x.ai/sign-in",
	} {
		if entry, ok := doc[preferred]; ok && strings.TrimSpace(entry.Key) != "" {
			return preferred, entry
		}
	}
	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := doc[key]
		if strings.TrimSpace(entry.Key) != "" {
			return key, entry
		}
	}
	return "", authEntry{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func tokenTail(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 4 {
		return token
	}
	return token[len(token)-4:]
}

func shortFingerprint(fp string) string {
	fp = strings.TrimSpace(fp)
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
