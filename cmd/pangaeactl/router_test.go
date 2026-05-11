package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v2router "github.com/0xc0de1ab/pangaea/internal/router"
)

func TestLoadRouterPolicyRequiresPolicyWithoutSimulator(t *testing.T) {
	if _, err := loadRouterPolicy("", false); err == nil {
		t.Fatalf("expected error without policy or simulator")
	}
}

func TestBuildRouterEngineWithSimulator(t *testing.T) {
	engine, err := buildRouterEngine(routerServeOptions{Simulator: true})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	decision := engine.DryRun(v2router.RouteRequest{
		Model:      "providersim-default",
		APIDialect: "openai",
		Stream:     true,
	})
	if !decision.Allowed {
		t.Fatalf("expected simulator route allowed: %#v", decision)
	}
}

func TestBuildRouterAPIKeyStore(t *testing.T) {
	store := buildRouterAPIKeyStore(routerServeOptions{APIKey: "pk_test_router", TenantID: "team-a", UserID: "usr_1"})
	if store == nil {
		t.Fatalf("expected API key store")
	}
	principal, ok := store.Authenticate("pk_test_router")
	if !ok {
		t.Fatalf("expected auth success")
	}
	if principal.TenantID != "team-a" || principal.UserID != "usr_1" {
		t.Fatalf("unexpected principal: %#v", principal)
	}
}

func TestRouterServeCommandExposesModelsWithSimulatorEngine(t *testing.T) {
	engine, err := buildRouterEngine(routerServeOptions{Simulator: true})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRouterServeCommandExposesStreamTokenKeyFlag(t *testing.T) {
	cmd := newRouterServeCmd()
	if cmd.Flags().Lookup("stream-token-key") == nil {
		t.Fatalf("expected stream-token-key flag")
	}
	if cmd.Flags().Lookup("peer-token") == nil {
		t.Fatalf("expected peer-token flag")
	}
}

func TestLoadGoogleOAuthEnvDefaultsAcceptsUnprefixedNames(t *testing.T) {
	t.Setenv("GOOGLE_OAUTH_ENABLED", "true")
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://127.0.0.1:18080/router/v1/auth/google/callback")
	t.Setenv("SESSION_SECRET", "session-secret")
	t.Setenv("GOOGLE_ALLOWED_EMAILS", "a@example.test,b@example.test")
	t.Setenv("GOOGLE_ALLOWED_DOMAIN", "example.org")
	t.Setenv("GOOGLE_COOKIE_SECURE", "true")
	t.Setenv("GOOGLE_SESSION_TTL", "30m")

	opts := loadGoogleOAuthEnvDefaults(v2router.GoogleOAuthOptions{})
	if !opts.Enabled || !opts.CookieSecure {
		t.Fatalf("expected enabled secure oauth opts: %#v", opts)
	}
	if opts.ClientID != "client-id" || opts.ClientSecret != "client-secret" || opts.SessionSecret != "session-secret" {
		t.Fatalf("expected unprefixed credentials to load: %#v", opts)
	}
	if opts.RedirectURL != "http://127.0.0.1:18080/router/v1/auth/google/callback" {
		t.Fatalf("unexpected redirect url: %q", opts.RedirectURL)
	}
	if len(opts.AllowedEmails) != 2 || opts.AllowedEmails[0] != "a@example.test" || opts.AllowedEmails[1] != "b@example.test" {
		t.Fatalf("unexpected allowed emails: %#v", opts.AllowedEmails)
	}
	if len(opts.AllowedDomains) != 1 || opts.AllowedDomains[0] != "example.org" {
		t.Fatalf("unexpected allowed domains: %#v", opts.AllowedDomains)
	}
	if opts.SessionTTL != 30*time.Minute {
		t.Fatalf("unexpected session ttl: %s", opts.SessionTTL)
	}
}

func TestLoadRouterNotifierEnvDefaultsAcceptsTelegramAliases(t *testing.T) {
	t.Setenv("TELEGRAM_API_TOKEN", "telegram-token")
	t.Setenv("TELEGRAM_CHAT_ID", "-100123")
	t.Setenv("TELEGRAM_ENDPOINT", "http://telegram.example.test")
	t.Setenv("TELEGRAM_DISABLE_NOTIFICATION", "true")
	t.Setenv("NOTIFIER_INTERVAL", "5m")

	opts := loadRouterNotifierEnvDefaults(v2router.RouterNotifierOptions{})
	if !opts.Telegram.Enabled {
		t.Fatalf("expected telegram notifier to auto-enable when token and chat are present: %#v", opts)
	}
	if opts.Telegram.BotToken != "telegram-token" || opts.Telegram.ChatID != "-100123" {
		t.Fatalf("unexpected telegram credentials: %#v", opts.Telegram)
	}
	if opts.Telegram.Endpoint != "http://telegram.example.test" || !opts.Telegram.DisableNotification {
		t.Fatalf("unexpected telegram options: %#v", opts.Telegram)
	}
	if opts.Interval != 5*time.Minute {
		t.Fatalf("unexpected notifier interval: %s", opts.Interval)
	}
}
