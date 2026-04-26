package client

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"github.com/0xc0de1ab/pangaea/pkg/formats/claudecreds"
	"github.com/0xc0de1ab/pangaea/pkg/formats/codexauth"
	"github.com/0xc0de1ab/pangaea/pkg/formats/geminioauth"
)

type testSnapshot struct {
	expiresAt   time.Time
	fingerprint string
}

func (s testSnapshot) Identity() string     { return "id" }
func (s testSnapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s testSnapshot) Raw() []byte          { return nil }
func (s testSnapshot) Fingerprint() string  { return s.fingerprint }

func TestShouldRefreshNudge(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		snap formats.Snapshot
		res  formats.ValidationResult
		want bool
	}{
		{
			name: "expired validation nudges immediately",
			snap: testSnapshot{expiresAt: now.Add(-time.Minute), fingerprint: "a"},
			res:  formats.ValidationResult{Status: formats.StatusExpired},
			want: true,
		},
		{
			name: "revoked validation nudges immediately",
			snap: testSnapshot{expiresAt: now.Add(20 * time.Minute), fingerprint: "a2"},
			res:  formats.ValidationResult{Status: formats.StatusRevoked},
			want: true,
		},
		{
			name: "near expiry nudges proactively",
			snap: testSnapshot{expiresAt: now.Add(20 * time.Minute), fingerprint: "b"},
			res:  formats.ValidationResult{Status: formats.StatusOK},
			want: true,
		},
		{
			name: "far expiry stays idle",
			snap: testSnapshot{expiresAt: now.Add(2 * time.Hour), fingerprint: "c"},
			res:  formats.ValidationResult{Status: formats.StatusOK},
			want: false,
		},
		{
			name: "scope warn does not nudge",
			snap: testSnapshot{expiresAt: now.Add(time.Hour), fingerprint: "d"},
			res:  formats.ValidationResult{Status: formats.StatusScopeWarn},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRefreshNudge(now, tt.snap, tt.res); got != tt.want {
				t.Fatalf("shouldRefreshNudge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClaimRefreshAttemptCooldown(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	ag := &agent{now: func() time.Time { return now }}

	if !ag.claimRefreshAttempt("fp-1", "expired") {
		t.Fatalf("first claim should succeed")
	}
	if ag.claimRefreshAttempt("fp-1", "expired") {
		t.Fatalf("second claim within cooldown should be suppressed")
	}

	ag.now = func() time.Time { return now.Add(refreshCooldown + time.Minute) }
	if !ag.claimRefreshAttempt("fp-1", "expired") {
		t.Fatalf("claim after cooldown should succeed")
	}
}

func TestClaudeRefreshCommands(t *testing.T) {
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "sk-ant-oat01-" + strings.Repeat("X", 40),
			"refreshToken":     "sk-ant-ort01-" + strings.Repeat("Y", 40),
			"expiresAt":        time.Now().Add(time.Hour).UnixMilli(),
			"scopes":           []string{"user:profile", "user:inference"},
			"subscriptionType": "max",
		},
	}
	raw, _ := json.Marshal(body)

	ag := &agent{
		dir:    "/tmp/custom-claude",
		format: claudecreds.Format{},
	}
	cmds, err := ag.refreshCommands(raw)
	if err != nil {
		t.Fatalf("refreshCommands() error = %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("len(cmds) = %d, want 2", len(cmds))
	}
	if cmds[0].Name != "claude" || len(cmds[0].Args) != 2 || cmds[0].Args[0] != "auth" || cmds[0].Args[1] != "login" {
		t.Fatalf("unexpected primary command: %+v", cmds[0])
	}
	env := strings.Join(cmds[0].Env, "\n")
	if !strings.Contains(env, "CLAUDE_CONFIG_DIR=/tmp/custom-claude") {
		t.Fatalf("CLAUDE_CONFIG_DIR missing from env: %v", cmds[0].Env)
	}
	if !strings.Contains(env, "CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference") {
		t.Fatalf("space-separated scopes missing from env: %v", cmds[0].Env)
	}
}

func TestGeminiRefreshCommands(t *testing.T) {
	ag := &agent{
		dir:    "/tmp/home/.gemini",
		format: geminioauth.Format{},
	}
	cmds, err := ag.refreshCommands(nil)
	if err != nil {
		t.Fatalf("refreshCommands() error = %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1", len(cmds))
	}
	env := strings.Join(cmds[0].Env, "\n")
	if !strings.Contains(env, "HOME=/tmp/home") {
		t.Fatalf("HOME override missing: %v", cmds[0].Env)
	}

	ag.dir = "/tmp/not-dot-gemini"
	cmds, err = ag.refreshCommands(nil)
	if err != nil {
		t.Fatalf("refreshCommands() error = %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected no commands for unsupported gemini dir, got %d", len(cmds))
	}
}

func TestCodexRefreshCommands(t *testing.T) {
	ag := &agent{
		dir:    "/tmp/custom-codex",
		format: codexauth.Format{},
	}
	cmds, err := ag.refreshCommands(nil)
	if err != nil {
		t.Fatalf("refreshCommands() error = %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1", len(cmds))
	}
	if cmds[0].Name != "codex" {
		t.Fatalf("command name = %q, want codex", cmds[0].Name)
	}
	if !strings.Contains(strings.Join(cmds[0].Env, "\n"), "CODEX_HOME=/tmp/custom-codex") {
		t.Fatalf("CODEX_HOME missing from env: %v", cmds[0].Env)
	}
}

func TestFilterAvailableRefreshCommands(t *testing.T) {
	oldLookPath := lookPath
	t.Cleanup(func() { lookPath = oldLookPath })

	lookPath = func(file string) (string, error) {
		switch file {
		case "claude", "codex":
			return "/usr/bin/" + file, nil
		default:
			return "", errors.New("not found")
		}
	}

	got := filterAvailableRefreshCommands([]refreshCommand{
		{Name: "claude", Description: "claude auth login"},
		{Name: "gemini", Description: "gemini oneshot"},
		{Name: "codex", Description: "codex exec"},
	})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Name != "claude" || got[1].Name != "codex" {
		t.Fatalf("unexpected filtered commands: %+v", got)
	}
}
