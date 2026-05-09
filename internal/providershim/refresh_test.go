package providershim

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

type refreshTestSnapshot struct {
	raw       []byte
	expiresAt time.Time
}

func (s refreshTestSnapshot) Identity() string     { return "refresh-test-snapshot" }
func (s refreshTestSnapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s refreshTestSnapshot) Raw() []byte          { return append([]byte(nil), s.raw...) }
func (s refreshTestSnapshot) Fingerprint() string  { return "refresh-test-fingerprint" }

type refreshTestFormat struct {
	expiresAt time.Time
	status    formats.ValidationStatus
	account   string
	display   string
	parsedRaw string
}

func (f *refreshTestFormat) Name() string         { return "refresh-test-format" }
func (f *refreshTestFormat) Strategies() []string { return []string{"default"} }
func (f *refreshTestFormat) Parse(raw []byte) (formats.Snapshot, error) {
	f.parsedRaw = string(raw)
	return refreshTestSnapshot{raw: raw, expiresAt: f.expiresAt}, nil
}
func (f *refreshTestFormat) Validate(_ context.Context, _ formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	checkedAt := time.Now()
	if opts.Clock != nil {
		checkedAt = opts.Clock()
	}
	return formats.ValidationResult{Status: f.status, CheckedAt: checkedAt}, nil
}
func (f *refreshTestFormat) Compare(_ string, _ formats.Snapshot, _ formats.Snapshot) int {
	return 0
}
func (f *refreshTestFormat) Redact(_ formats.Snapshot) formats.Summary {
	return formats.Summary{}
}
func (f *refreshTestFormat) Account(_ context.Context, _ formats.Snapshot, _ string) (string, error) {
	return f.account, nil
}
func (f *refreshTestFormat) AccountDisplay(_ context.Context, _ formats.Snapshot, _ string) (string, error) {
	return f.display, nil
}

func TestCommandAuthRefresherRunsCommandAndReadsAuthFile(t *testing.T) {
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	if err := os.WriteFile(authPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale auth: %v", err)
	}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	format := &refreshTestFormat{
		expiresAt: now.Add(time.Hour),
		status:    formats.StatusOK,
		account:   "acct-1",
		display:   "acct@example.test",
	}
	ran := false
	runner := RefreshCommandRunnerFunc(func(ctx context.Context, spec RefreshCommandSpec) error {
		ran = true
		if len(spec.Command) != 2 || spec.Command[0] != "codex" || spec.Command[1] != "exec" {
			t.Fatalf("command = %v, want [codex exec]", spec.Command)
		}
		if spec.Env["PANGAEA_TEST"] != "1" {
			t.Fatalf("env = %v, want PANGAEA_TEST=1", spec.Env)
		}
		if spec.WorkingDir != dir {
			t.Fatalf("working dir = %q, want %q", spec.WorkingDir, dir)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatalf("refresh context did not include a deadline")
		}
		return os.WriteFile(authPath, []byte("fresh"), 0o600)
	})
	refresher, err := NewCommandAuthRefresher(CommandAuthRefresherOptions{
		Command:    []string{"codex", "exec"},
		Env:        map[string]string{"PANGAEA_TEST": "1"},
		WorkingDir: dir,
		Timeout:    time.Minute,
		AuthPath:   authPath,
		Format:     format,
		Runner:     runner,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new refresher: %v", err)
	}

	auth, err := refresher.RefreshAuth(context.Background(), control.AuthRefreshRequest{
		RefreshID: "refresh-test",
	}, provider.Registration{
		Auth: provider.AuthState{Status: provider.AuthRefreshSoon, Refreshable: true},
	})
	if err != nil {
		t.Fatalf("refresh auth: %v", err)
	}
	if !ran {
		t.Fatalf("refresh command was not run")
	}
	if format.parsedRaw != "fresh" {
		t.Fatalf("parsed raw = %q, want fresh", format.parsedRaw)
	}
	if auth.Status != provider.AuthHealthy || !auth.ExpiresAt.Equal(now.Add(time.Hour)) || auth.SelectedSource != "container" {
		t.Fatalf("unexpected auth state: %#v", auth)
	}
	if auth.Account.ID != "acct-1" || auth.Account.Display != "acct@example.test" {
		t.Fatalf("account = %#v", auth.Account)
	}
	if !auth.LastRefreshAt.Equal(now) || auth.LastRefreshErr != "" {
		t.Fatalf("refresh metadata = at %s err %q", auth.LastRefreshAt, auth.LastRefreshErr)
	}
}

func TestCommandAuthRefresherReportsCommandFailure(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	refresher, err := NewCommandAuthRefresher(CommandAuthRefresherOptions{
		Command: []string{"gemini", "--prompt", "ping"},
		Runner: RefreshCommandRunnerFunc(func(context.Context, RefreshCommandSpec) error {
			return errors.New("oneshot failed")
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new refresher: %v", err)
	}

	auth, err := refresher.RefreshAuth(context.Background(), control.AuthRefreshRequest{}, provider.Registration{
		Auth: provider.AuthState{Status: provider.AuthRefreshSoon},
	})
	if err == nil {
		t.Fatalf("expected refresh error")
	}
	if auth.Status != provider.AuthUnavailable || auth.LastRefreshErr != "oneshot failed" || !auth.LastRefreshAt.Equal(now) {
		t.Fatalf("unexpected auth state: %#v", auth)
	}
}

func TestCommandAuthRefresherAcceptsUpdatedAuthFileWhenCommandFails(t *testing.T) {
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	if err := os.WriteFile(authPath, []byte("refreshed"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	format := &refreshTestFormat{
		expiresAt: now.Add(time.Hour),
		status:    formats.StatusOK,
		account:   "gemini-account",
		display:   "gemini@example.test",
	}
	refresher, err := NewCommandAuthRefresher(CommandAuthRefresherOptions{
		Command:  []string{"gemini", "--prompt", "ping"},
		AuthPath: authPath,
		Format:   format,
		Runner: RefreshCommandRunnerFunc(func(context.Context, RefreshCommandSpec) error {
			return errors.New("quota exhausted after token refresh")
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new refresher: %v", err)
	}

	auth, err := refresher.RefreshAuth(context.Background(), control.AuthRefreshRequest{}, provider.Registration{
		Auth: provider.AuthState{Status: provider.AuthRefreshSoon},
	})
	if err != nil {
		t.Fatalf("refresh auth should accept healthy file after command error: %v", err)
	}
	if format.parsedRaw != "refreshed" {
		t.Fatalf("parsed raw = %q, want refreshed", format.parsedRaw)
	}
	if auth.Status != provider.AuthHealthy || !auth.ExpiresAt.Equal(now.Add(time.Hour)) || auth.LastRefreshErr != "" {
		t.Fatalf("unexpected auth state: %#v", auth)
	}
	if auth.Account.ID != "gemini-account" || auth.Account.Display != "gemini@example.test" {
		t.Fatalf("account = %#v", auth.Account)
	}
}

func TestCommandAuthRefresherRejectsExpiredAuthFile(t *testing.T) {
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	if err := os.WriteFile(authPath, []byte("expired"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	refresher, err := NewCommandAuthRefresher(CommandAuthRefresherOptions{
		Command:  []string{"claude", "--print", "ping"},
		AuthPath: authPath,
		Format: &refreshTestFormat{
			expiresAt: now.Add(-time.Minute),
			status:    formats.StatusExpired,
		},
		Runner: RefreshCommandRunnerFunc(func(context.Context, RefreshCommandSpec) error {
			return nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new refresher: %v", err)
	}

	auth, err := refresher.RefreshAuth(context.Background(), control.AuthRefreshRequest{}, provider.Registration{
		Auth: provider.AuthState{Status: provider.AuthRefreshSoon},
	})
	if err == nil || !strings.Contains(err.Error(), "refreshed auth status expired") {
		t.Fatalf("expected expired auth error, got %v", err)
	}
	if auth.Status != provider.AuthExpired {
		t.Fatalf("auth status = %q, want %q", auth.Status, provider.AuthExpired)
	}
}

func TestLoginShellRefreshCommandUsesLoginShell(t *testing.T) {
	got := LoginShellRefreshCommand("gemini", "--prompt", "ping")
	if len(got) != 6 {
		t.Fatalf("command length = %d, want 6: %v", len(got), got)
	}
	if got[0] != "bash" || got[1] != "-lc" || !strings.Contains(got[2], ".bashrc") || !strings.Contains(got[2], `exec 'gemini' "$@"`) {
		t.Fatalf("unexpected shell command: %v", got)
	}
	if got[3] != "gemini-refresh" || got[4] != "--prompt" || got[5] != "ping" {
		t.Fatalf("unexpected shell args: %v", got)
	}
}
