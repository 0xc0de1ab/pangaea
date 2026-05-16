package cursorcliconfig

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestProbeUsesCursorDashboardCurrentPeriodUsage(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/aiserver.v1.DashboardService/GetCurrentPeriodUsage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Fatalf("missing connect protocol header")
		}
		if r.Header.Get("Authorization") == "Bearer cursor-access-test" {
			sawAuth = true
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{
			"billingCycleStart":"1778222860000",
			"billingCycleEnd":"1780901260000",
			"planUsage":{"totalSpend":1998,"includedSpend":1998,"remaining":2,"limit":2000,"autoPercentUsed":44.4,"apiPercentUsed":0,"totalPercentUsed":22.2},
			"displayMessage":"You've used 100% of your included usage"
		}`))
	}))
	defer server.Close()
	t.Setenv("PANGAEA_CURSOR_API_BASE_URL", "")
	t.Setenv("CURSOR_API_BASE_URL", "")

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	authPath := filepath.Join(home, ".config", "cursor", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{"accessToken":"cursor-access-test","refreshToken":"cursor-refresh-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cliConfigPath := filepath.Join(home, ".cursor", "cli-config.json")
	if err := os.MkdirAll(filepath.Dir(cliConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	var f Format
	raw := strings.Replace(cursorCLIConfigFixture("dev@example.com", "Pro"), `"version": 1,`, `"version": 1, "serverConfigCache":{"backendUrl":"`+server.URL+`"},`, 1)
	snap, err := f.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := f.Probe(context.Background(), snap, cliConfigPath, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("dashboard request did not use Cursor access token")
	}
	if rep.Used != 1998 || rep.Limit != 2000 || rep.Unit != "usd_cents" {
		t.Fatalf("usage summary = %#v", rep)
	}
	if len(rep.Windows) != 1 || rep.Windows[0].Label != "Included usage" || rep.Windows[0].RemainingPct < 0.09 || rep.Windows[0].RemainingPct > 0.11 {
		t.Fatalf("usage windows = %#v", rep.Windows)
	}
	if len(rep.Notes) == 0 || !strings.Contains(strings.Join(rep.Notes, "\n"), "display-message") {
		t.Fatalf("expected display message note: %#v", rep.Notes)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"planUsage":{"totalSpend":100,"remaining":1900,"limit":2000}}`))
	}))
	defer server.Close()
	t.Setenv("PANGAEA_CURSOR_API_BASE_URL", server.URL)

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
