// Package cursorapitoken registers a minimal plain-text Format for a Cursor
// API token stored as a single line (or short header=value line). The raw
// secret never leaves Snapshot.Raw(); Identity and summaries stay correlation
// safe.
package cursorapitoken

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var _ formats.AccountAware = Format{}
var _ formats.AccountDisplayAware = Format{}

// FormatName is the registry key for pangaeactl --auth-format with Cursor shims.
const FormatName = "cursor-api-token-plain-format"

const strategyOpaque = "opaque_latest"

// Format is registered in init().
type Format struct{}

func init() {
	formats.Register(Format{})
}

func (Format) Name() string { return FormatName }

func (Format) Strategies() []string { return []string{strategyOpaque} }

func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty cursor api token file")
	}
	if idx := strings.IndexAny(token, "\r\n"); idx >= 0 {
		token = strings.TrimSpace(token[:idx])
	}
	if strings.Contains(strings.ToLower(token), "begin ") {
		return nil, common.Wrap(nil, common.ErrParseFailed, "cursor api token file looks like a PEM block")
	}
	if strings.Contains(token, "=") && !strings.Contains(token, " ") {
		parts := strings.SplitN(token, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(strings.ToLower(parts[0])) == "cursor_api_key" {
			token = strings.TrimSpace(parts[1])
		}
	}
	if token == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty cursor api token after normalization")
	}
	sum := sha256.Sum256([]byte(token))
	fp := hex.EncodeToString(sum[:])
	id := "cursor-api-token:" + fp[:12]
	return snapshot{token: token, fp: fp, id: id}, nil
}

func (Format) Validate(ctx context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	_ = ctx
	if snap == nil {
		return formats.ValidationResult{Status: formats.StatusParseError, Detail: "nil snapshot", CheckedAt: clockNow(opts)}, nil
	}
	if strings.TrimSpace(string(snap.Raw())) == "" {
		return formats.ValidationResult{Status: formats.StatusExpired, Detail: "missing token", CheckedAt: clockNow(opts)}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: clockNow(opts)}, nil
}

func (Format) Compare(strategy string, _, _ formats.Snapshot) int {
	if strategy != strategyOpaque {
		panic(fmt.Sprintf("cursorapitoken: unknown strategy %q", strategy))
	}
	return 0
}

func (Format) Redact(snap formats.Snapshot) formats.Summary {
	if snap == nil {
		return formats.Summary{}
	}
	fp := snap.Fingerprint()
	if len(fp) > 12 {
		fp = fp[:12]
	}
	return formats.Summary{
		Identity:         snap.Identity(),
		FingerprintShort: fp,
		ExpiresAt:        snap.ExpiresAt(),
	}
}

func (Format) Account(ctx context.Context, snap formats.Snapshot, _ string) (string, error) {
	if snap == nil {
		return "", nil
	}
	token := strings.TrimSpace(string(snap.Raw()))
	if token == "" {
		return "", nil
	}
	doc, err := fetchCursorMe(ctx, token, probeHTTPClient(nil))
	if err != nil || strings.TrimSpace(doc.UserEmail) == "" {
		return snap.Identity(), nil
	}
	return strings.TrimSpace(doc.UserEmail), nil
}

func (Format) AccountDisplay(ctx context.Context, snap formats.Snapshot, _ string) (string, error) {
	if snap == nil {
		return "", nil
	}
	token := strings.TrimSpace(string(snap.Raw()))
	if token == "" {
		return "", nil
	}
	doc, err := fetchCursorMe(ctx, token, probeHTTPClient(nil))
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(doc.UserEmail), nil
}

type snapshot struct {
	token string
	fp    string
	id    string
}

func (s snapshot) Identity() string   { return s.id }
func (snapshot) ExpiresAt() time.Time { return time.Time{} }

func (s snapshot) Raw() []byte {
	b := []byte(s.token)
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func (s snapshot) Fingerprint() string { return s.fp }

func clockNow(opts formats.ValidateOpts) time.Time {
	if opts.Clock != nil {
		return opts.Clock()
	}
	return time.Now().UTC()
}
