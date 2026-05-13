package githubcopilotapps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func TestParseDerivesCopilotAccount(t *testing.T) {
	raw := []byte(`{
  "github.com:Iv23ctfURkiMfJ4xr5mv": {
    "user": "octocat",
    "oauth_token": "gho_secret_tail",
    "githubAppId": "Iv23ctfURkiMfJ4xr5mv"
  }
}`)
	format := Format{}
	snap, err := format.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	account, err := format.Account(context.Background(), snap, "")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account != "octocat" {
		t.Fatalf("account = %q, want octocat", account)
	}
	display, err := format.AccountDisplay(context.Background(), snap, "")
	if err != nil {
		t.Fatalf("account display: %v", err)
	}
	if display != "octocat" {
		t.Fatalf("account display = %q, want octocat", display)
	}
	result, err := format.Validate(context.Background(), snap, formats.ValidateOpts{})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if result.Status != formats.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	summary := format.Redact(snap)
	if summary.TokenTail4 != "tail" {
		t.Fatalf("token tail = %q, want tail", summary.TokenTail4)
	}
	if strings.Contains(summary.Identity, "octocat") || strings.Contains(summary.FingerprintShort, "secret") {
		t.Fatalf("summary should not expose raw account in identity or token: %#v", summary)
	}
	if summary.Extra["user"] != "octocat" || summary.Extra["host"] != "github.com" {
		t.Fatalf("summary extra = %#v", summary.Extra)
	}
}

func TestParseDerivesCopilotAccountFromConfigJSON(t *testing.T) {
	raw := []byte(`// User settings belong in settings.json.
// This file is managed automatically.
{
  "lastLoggedInUser": {"host": "https://github.com", "login": "octocat"},
  "loggedInUsers": [
    {"host": "https://github.com", "login": "octocat"}
  ],
  "copilotTokens": {
    "https://github.com:octocat": "copilot_secret_tail"
  }
}`)
	format := ConfigFormat{}
	snap, err := format.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	account, err := format.Account(context.Background(), snap, "")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account != "octocat" {
		t.Fatalf("account = %q, want octocat", account)
	}
	result, err := format.Validate(context.Background(), snap, formats.ValidateOpts{})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if result.Status != formats.StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	summary := format.Redact(snap)
	if summary.TokenTail4 != "tail" {
		t.Fatalf("token tail = %q, want tail", summary.TokenTail4)
	}
	if summary.Extra["user"] != "octocat" || summary.Extra["host"] != "https://github.com" {
		t.Fatalf("summary extra = %#v", summary.Extra)
	}
	if strings.Contains(summary.FingerprintShort, "secret") {
		t.Fatalf("summary should not expose token: %#v", summary)
	}
}

func TestConfigFormatFallsBackToTokenKeyUser(t *testing.T) {
	raw := []byte(`{"copilotTokens":{"https://github.com:ghost":"copilot_test_secret"}}`)
	format := ConfigFormat{}
	snap, err := format.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	account, err := format.Account(context.Background(), snap, "")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account != "ghost" {
		t.Fatalf("account = %q, want ghost", account)
	}
}

func TestConfigFormatRejectsMissingToken(t *testing.T) {
	_, err := ConfigFormat{}.Parse([]byte(`{"lastLoggedInUser":{"login":"octocat"}}`))
	if err == nil {
		t.Fatal("parse succeeded without token")
	}
}

func TestParseRejectsAppsWithoutToken(t *testing.T) {
	_, err := Format{}.Parse([]byte(`{"github.com:app":{"user":"octocat"}}`))
	if err == nil {
		t.Fatal("parse succeeded without token")
	}
}

func TestProbeSurfacesCopilotPlan(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/copilot_internal/v2/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("authorization") == "Bearer gho_secret_tail" {
			sawAuth = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sku":      "copilot_for_business_seat",
			"planName": "Copilot Business",
			"status":   "active",
		})
	}))
	defer server.Close()
	t.Setenv("GITHUB_COPILOT_API_BASE_URL", server.URL)

	format := Format{}
	snap, err := format.Parse([]byte(`{
  "github.com:Iv23ctfURkiMfJ4xr5mv": {
    "user": "octocat",
    "oauth_token": "gho_secret_tail"
  }
}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	report, err := format.Probe(context.Background(), snap, "", server.Client())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !sawAuth {
		t.Fatalf("probe did not send oauth token")
	}
	if report.PlanTier != "copilot_for_business_seat" {
		t.Fatalf("plan tier = %q", report.PlanTier)
	}
	if !containsString(report.Notes, "tier:Copilot Business") || !containsString(report.Notes, "status:active") {
		t.Fatalf("notes = %#v", report.Notes)
	}
}

func TestAccountLooksUpGitHubLoginWhenUserMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"login": "ghost"})
	}))
	defer srv.Close()
	t.Setenv("GITHUB_USER_API_LOOKUP_BASE", srv.URL)

	raw := []byte(`{"github.com:Iv23":{"oauth_token":"gho_test"}}`)
	format := Format{}
	snap, err := format.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	login, err := format.Account(context.Background(), snap, "")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if login != "ghost" {
		t.Fatalf("login = %q want ghost", login)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
