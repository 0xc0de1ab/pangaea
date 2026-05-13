package cursorcliconfig

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseDerivesCursorAccount(t *testing.T) {
	var f Format
	snap, err := f.Parse([]byte(cursorCLIConfigFixture("dev@example.com", "Pro")))
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.Account(context.Background(), snap, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "350474099" {
		t.Fatalf("account id = %q, want user id", id)
	}
	display, err := f.AccountDisplay(context.Background(), snap, "")
	if err != nil {
		t.Fatal(err)
	}
	if display != "dev@example.com" {
		t.Fatalf("display = %q", display)
	}
	summary := f.Redact(snap)
	if summary.Subscription != "Pro" {
		t.Fatalf("subscription = %q, want Pro", summary.Subscription)
	}
}

func TestProbeUsesCursorAgentAbout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cursor-agent")
	script := `#!/bin/sh
if [ "$1" = "about" ] && [ "$2" = "--format" ] && [ "$3" = "json" ]; then
  printf '%s\n' '{"cliVersion":"2026.05.09-test","model":"Composer 2 Fast","subscriptionTier":"Pro","userEmail":"dev@example.com"}'
  exit 0
fi
exit 2
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PANGAEA_CURSOR_AGENT_EXE", bin)
	home := filepath.Join(dir, "home")
	authPath := filepath.Join(home, ".cursor", "cli-config.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}

	var f Format
	snap, err := f.Parse([]byte(cursorCLIConfigFixture("dev@example.com", "")))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := f.Probe(context.Background(), snap, authPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PlanTier != "Pro" {
		t.Fatalf("plan tier = %q, want Pro", rep.PlanTier)
	}
	if len(rep.Notes) == 0 {
		t.Fatal("expected notes")
	}
}

func TestParseRejectsMissingAuthInfo(t *testing.T) {
	var f Format
	if _, err := f.Parse([]byte(`{"version":1}`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAuthFormatUsesCursorAgentStatusAndAbout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cursor-agent")
	script := `#!/bin/sh
if [ "$1" = "status" ]; then
  printf '%s\n' '{"status":"authenticated","isAuthenticated":true,"hasAccessToken":true,"hasRefreshToken":true,"userInfo":{"email":"dev@example.com","userId":350474099}}'
  exit 0
fi
if [ "$1" = "about" ]; then
  printf '%s\n' '{"cliVersion":"2026.05.09-test","model":"Composer 2 Fast","subscriptionTier":"Pro","userEmail":"dev@example.com"}'
  exit 0
fi
exit 2
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PANGAEA_CURSOR_AGENT_EXE", bin)
	home := filepath.Join(dir, "home")
	authPath := filepath.Join(home, ".config", "cursor", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}

	var f AuthFormat
	snap, err := f.Parse([]byte(`{"accessToken":"a","refreshToken":"r"}`))
	if err != nil {
		t.Fatal(err)
	}
	display, err := f.AccountDisplay(context.Background(), snap, authPath)
	if err != nil || display != "dev@example.com" {
		t.Fatalf("display = %q err=%v", display, err)
	}
	rep, err := f.Probe(context.Background(), snap, authPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PlanTier != "Pro" {
		t.Fatalf("plan tier = %q, want Pro", rep.PlanTier)
	}
}

func cursorCLIConfigFixture(email string, tier string) string {
	if tier == "" {
		return `{
  "version": 1,
  "model": {"modelId":"composer-2","displayModelId":"composer-2","displayName":"Composer 2 Fast"},
  "selectedModel": {"modelId":"composer-2"},
  "authInfo": {"email":"` + email + `","displayName":"","userId":350474099,"authId":"auth0|fixture"}
}`
	}
	return `{
  "version": 1,
  "subscriptionTier": "` + tier + `",
  "model": {"modelId":"composer-2","displayModelId":"composer-2","displayName":"Composer 2 Fast"},
  "selectedModel": {"modelId":"composer-2"},
  "authInfo": {"email":"` + email + `","displayName":"","userId":350474099,"authId":"auth0|fixture"}
}`
}
