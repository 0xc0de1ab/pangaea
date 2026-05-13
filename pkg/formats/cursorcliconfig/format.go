// Package cursorcliconfig implements Cursor Agent's ~/.cursor/cli-config.json
// credential/config format.
package cursorcliconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var _ formats.AccountAware = Format{}
var _ formats.AccountDisplayAware = Format{}
var _ formats.DirResolver = Format{}
var _ formats.UsageProbe = Format{}

const (
	// FormatName is the registry key for Cursor Agent's cli-config.json.
	FormatName = "cursor-cli-config-json-format"

	strategyConfigLatest = "config_latest"
)

type Format struct{}

func init() {
	formats.Register(Format{})
}

func (Format) Name() string { return FormatName }

func (Format) Strategies() []string { return []string{strategyConfigLatest} }

func (Format) CredentialPath(dir string) string {
	return filepath.Join(dir, "cli-config.json")
}

func (Format) Parse(raw []byte) (formats.Snapshot, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, common.Wrap(nil, common.ErrParseFailed, "empty cursor cli config")
	}
	var doc cliConfigDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, common.Wrap(err, common.ErrParseFailed, "decode cursor-cli-config-json-format")
	}
	email := strings.TrimSpace(doc.AuthInfo.Email)
	displayName := strings.TrimSpace(doc.AuthInfo.DisplayName)
	userID := strings.TrimSpace(doc.AuthInfo.UserID.String())
	authID := strings.TrimSpace(doc.AuthInfo.AuthID)
	if email == "" && userID == "" && authID == "" {
		return nil, common.Wrap(nil, common.ErrParseFailed, "missing cursor authInfo account")
	}
	sum := sha256.Sum256(raw)
	fp := hex.EncodeToString(sum[:])
	accountSeed := firstNonEmpty(userID, authID, email, fp)
	accountHash := sha256.Sum256([]byte(accountSeed))
	identity := "cursor:" + hex.EncodeToString(accountHash[:])[:16]
	rawCopy := append([]byte(nil), raw...)
	return snapshot{
		raw:              rawCopy,
		fp:               fp,
		identity:         identity,
		email:            email,
		displayName:      displayName,
		userID:           userID,
		authID:           authID,
		subscriptionTier: strings.TrimSpace(firstNonEmpty(doc.SubscriptionTier, doc.Plan, doc.PlanName, doc.Subscription, doc.UserTier, doc.BillingPlan)),
		modelID:          strings.TrimSpace(firstNonEmpty(doc.SelectedModel.ModelID, doc.Model.ModelID, doc.Model.DisplayModelID)),
		modelName:        strings.TrimSpace(firstNonEmpty(doc.Model.DisplayName, doc.Model.DisplayNameShort)),
	}, nil
}

func (Format) Validate(ctx context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	_ = ctx
	if snap == nil {
		return formats.ValidationResult{Status: formats.StatusParseError, Detail: "nil snapshot", CheckedAt: clockNow(opts)}, nil
	}
	if strings.TrimSpace(snap.Identity()) == "" || len(bytes.TrimSpace(snap.Raw())) == 0 {
		return formats.ValidationResult{Status: formats.StatusExpired, Detail: "missing cursor cli config", CheckedAt: clockNow(opts)}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: clockNow(opts)}, nil
}

func (Format) Compare(strategy string, _, _ formats.Snapshot) int {
	if strategy != strategyConfigLatest {
		panic(fmt.Sprintf("cursorcliconfig: unknown strategy %q", strategy))
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
	out := formats.Summary{
		Identity:         snap.Identity(),
		FingerprintShort: fp,
		ExpiresAt:        snap.ExpiresAt(),
	}
	if s, ok := snap.(snapshot); ok {
		out.Subscription = strings.TrimSpace(s.subscriptionTier)
		extra := map[string]string{}
		if s.modelID != "" {
			extra["model_id"] = s.modelID
		}
		if s.modelName != "" {
			extra["model_name"] = s.modelName
		}
		if len(extra) > 0 {
			out.Extra = extra
		}
	}
	return out
}

func (Format) Account(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	if s, ok := snap.(snapshot); ok {
		return firstNonEmpty(s.userID, s.authID, s.email, s.identity), nil
	}
	if snap == nil {
		return "", nil
	}
	return snap.Identity(), nil
}

func (Format) AccountDisplay(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	if s, ok := snap.(snapshot); ok {
		if s.email != "" {
			return s.email, nil
		}
		return firstNonEmpty(s.displayName, s.userID, s.authID), nil
	}
	return "", nil
}

func (Format) Probe(ctx context.Context, snap formats.Snapshot, path string, _ *http.Client) (formats.UsageReport, error) {
	if snap == nil {
		return formats.UsageReport{}, fmt.Errorf("cursorcliconfig.Probe: nil snapshot")
	}
	rep := formats.UsageReport{}
	if s, ok := snap.(snapshot); ok {
		rep.PlanTier = strings.TrimSpace(s.subscriptionTier)
		if s.email != "" {
			rep.Notes = append(rep.Notes, "email:"+s.email)
		}
		if s.modelName != "" {
			rep.Notes = append(rep.Notes, "model:"+s.modelName)
		}
	}
	about, err := cursorAgentAbout(ctx, path)
	if err != nil {
		if rep.PlanTier != "" || len(rep.Notes) > 0 {
			rep.Notes = append(rep.Notes, "status:Cursor Agent about unavailable")
			return rep, nil
		}
		return formats.UsageReport{}, err
	}
	if tier := strings.TrimSpace(about.SubscriptionTier); tier != "" {
		rep.PlanTier = tier
	}
	if email := strings.TrimSpace(about.UserEmail); email != "" && !hasNote(rep.Notes, "email:"+email) {
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

type cliConfigDoc struct {
	AuthInfo struct {
		Email       string      `json:"email"`
		DisplayName string      `json:"displayName"`
		UserID      json.Number `json:"userId"`
		AuthID      string      `json:"authId"`
	} `json:"authInfo"`
	Model struct {
		ModelID          string `json:"modelId"`
		DisplayModelID   string `json:"displayModelId"`
		DisplayName      string `json:"displayName"`
		DisplayNameShort string `json:"displayNameShort"`
	} `json:"model"`
	SelectedModel struct {
		ModelID string `json:"modelId"`
	} `json:"selectedModel"`
	SubscriptionTier string `json:"subscriptionTier,omitempty"`
	Plan             string `json:"plan,omitempty"`
	PlanName         string `json:"planName,omitempty"`
	Subscription     string `json:"subscription,omitempty"`
	UserTier         string `json:"userTier,omitempty"`
	BillingPlan      string `json:"billingPlan,omitempty"`
}

type snapshot struct {
	raw              []byte
	fp               string
	identity         string
	email            string
	displayName      string
	userID           string
	authID           string
	subscriptionTier string
	modelID          string
	modelName        string
}

func (s snapshot) Identity() string    { return s.identity }
func (snapshot) ExpiresAt() time.Time  { return time.Time{} }
func (s snapshot) Fingerprint() string { return s.fp }

func (s snapshot) Raw() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

type cursorAboutDoc struct {
	CLIVersion       string `json:"cliVersion"`
	Model            string `json:"model"`
	SubscriptionTier string `json:"subscriptionTier"`
	UserEmail        string `json:"userEmail"`
}

func cursorAgentAbout(ctx context.Context, authPath string) (cursorAboutDoc, error) {
	var zero cursorAboutDoc
	out, err := runCursorAgentJSON(ctx, authPath, "about", "--format", "json")
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(out, &zero); err != nil {
		return zero, fmt.Errorf("cursorcliconfig: decode cursor-agent about: %w", err)
	}
	return zero, nil
}

type cursorStatusDoc struct {
	Status          string `json:"status"`
	IsAuthenticated bool   `json:"isAuthenticated"`
	UserInfo        struct {
		Email  string      `json:"email"`
		UserID json.Number `json:"userId"`
	} `json:"userInfo"`
}

func cursorAgentStatus(ctx context.Context, authPath string) (cursorStatusDoc, error) {
	var zero cursorStatusDoc
	out, err := runCursorAgentJSON(ctx, authPath, "status", "--format", "json")
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(out, &zero); err != nil {
		return zero, fmt.Errorf("cursorcliconfig: decode cursor-agent status: %w", err)
	}
	return zero, nil
}

func runCursorAgentJSON(ctx context.Context, authPath string, args ...string) ([]byte, error) {
	exe := firstNonEmpty(os.Getenv("PANGAEA_CURSOR_AGENT_EXE"), os.Getenv("CURSOR_AGENT_EXE"), "cursor-agent")
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runCursorAgentCommand(ctx, exe, authPath, args...)
	if err != nil && exe == "cursor-agent" {
		return runCursorAgentCommand(ctx, "agent", authPath, args...)
	}
	return out, err
}

func runCursorAgentCommand(ctx context.Context, exe string, authPath string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	if home := homeFromCursorPath(authPath); home != "" {
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	return cmd.Output()
}

func homeFromCursorPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if filepath.Base(clean) != "cli-config.json" {
		if filepath.Base(clean) == "auth.json" {
			cursorDir := filepath.Dir(clean)
			if filepath.Base(cursorDir) != "cursor" {
				return ""
			}
			configDir := filepath.Dir(cursorDir)
			if filepath.Base(configDir) != ".config" {
				return ""
			}
			return filepath.Dir(configDir)
		}
		return ""
	}
	cursorDir := filepath.Dir(clean)
	if filepath.Base(cursorDir) != ".cursor" {
		return ""
	}
	return filepath.Dir(cursorDir)
}

func clockNow(opts formats.ValidateOpts) time.Time {
	if opts.Clock != nil {
		return opts.Clock()
	}
	return time.Now().UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hasNote(notes []string, needle string) bool {
	for _, note := range notes {
		if note == needle {
			return true
		}
	}
	return false
}
