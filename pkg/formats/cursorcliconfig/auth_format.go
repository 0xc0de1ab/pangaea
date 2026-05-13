package cursorcliconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var _ formats.AccountAware = AuthFormat{}
var _ formats.AccountDisplayAware = AuthFormat{}
var _ formats.UsageProbe = AuthFormat{}
var _ formats.DirResolver = AuthFormat{}

const (
	// AuthFormatName is the registry key for Cursor Agent's ~/.config/cursor/auth.json.
	AuthFormatName = "cursor-auth-json-format"

	strategyAuthLatest = "auth_latest"
)

type AuthFormat struct{}

func init() {
	formats.Register(AuthFormat{})
}

func (AuthFormat) Name() string { return AuthFormatName }

func (AuthFormat) Strategies() []string { return []string{strategyAuthLatest} }

func (AuthFormat) CredentialPath(dir string) string {
	return filepath.Join(dir, "auth.json")
}

func (AuthFormat) Parse(raw []byte) (formats.Snapshot, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty cursor auth json")
	}
	var doc cursorAuthDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode cursor-auth-json-format")
	}
	if strings.TrimSpace(doc.AccessToken) == "" && strings.TrimSpace(doc.RefreshToken) == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing cursor accessToken/refreshToken")
	}
	sum := sha256.Sum256(raw)
	fp := hex.EncodeToString(sum[:])
	idSum := sha256.Sum256([]byte(firstNonEmpty(doc.RefreshToken, doc.AccessToken, fp)))
	return authSnapshot{
		raw:      append([]byte(nil), raw...),
		fp:       fp,
		identity: "cursor-auth:" + hex.EncodeToString(idSum[:])[:16],
	}, nil
}

func (AuthFormat) Validate(ctx context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	_ = ctx
	if snap == nil {
		return formats.ValidationResult{Status: formats.StatusParseError, Detail: "nil snapshot", CheckedAt: clockNow(opts)}, nil
	}
	if len(bytes.TrimSpace(snap.Raw())) == 0 {
		return formats.ValidationResult{Status: formats.StatusExpired, Detail: "missing cursor auth json", CheckedAt: clockNow(opts)}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: clockNow(opts)}, nil
}

func (AuthFormat) Compare(strategy string, _, _ formats.Snapshot) int {
	if strategy != strategyAuthLatest {
		panic(fmt.Sprintf("cursorauth: unknown strategy %q", strategy))
	}
	return 0
}

func (AuthFormat) Redact(snap formats.Snapshot) formats.Summary {
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

func (AuthFormat) Account(ctx context.Context, snap formats.Snapshot, path string) (string, error) {
	if snap == nil {
		return "", nil
	}
	status, err := cursorAgentStatus(ctx, path)
	if err == nil {
		return firstNonEmpty(status.UserInfo.UserID.String(), status.UserInfo.Email, snap.Identity()), nil
	}
	return snap.Identity(), nil
}

func (AuthFormat) AccountDisplay(ctx context.Context, snap formats.Snapshot, path string) (string, error) {
	if snap == nil {
		return "", nil
	}
	status, err := cursorAgentStatus(ctx, path)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(status.UserInfo.Email), nil
}

func (AuthFormat) Probe(ctx context.Context, snap formats.Snapshot, path string, _ *http.Client) (formats.UsageReport, error) {
	if snap == nil {
		return formats.UsageReport{}, fmt.Errorf("cursorauth.Probe: nil snapshot")
	}
	about, err := cursorAgentAbout(ctx, path)
	if err != nil {
		return formats.UsageReport{}, err
	}
	rep := formats.UsageReport{PlanTier: strings.TrimSpace(about.SubscriptionTier)}
	if email := strings.TrimSpace(about.UserEmail); email != "" {
		rep.Notes = append(rep.Notes, "email:"+email)
	}
	if model := strings.TrimSpace(about.Model); model != "" {
		rep.Notes = append(rep.Notes, "model:"+model)
	}
	if version := strings.TrimSpace(about.CLIVersion); version != "" {
		rep.Notes = append(rep.Notes, "cli-version:"+version)
	}
	if rep.PlanTier == "" {
		rep.Notes = append(rep.Notes, "status:Cursor subscription not reported")
	}
	return rep, nil
}

type cursorAuthDoc struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

type authSnapshot struct {
	raw      []byte
	fp       string
	identity string
}

func (s authSnapshot) Identity() string    { return s.identity }
func (authSnapshot) ExpiresAt() time.Time  { return time.Time{} }
func (s authSnapshot) Fingerprint() string { return s.fp }
func (s authSnapshot) Raw() []byte         { return append([]byte(nil), s.raw...) }
