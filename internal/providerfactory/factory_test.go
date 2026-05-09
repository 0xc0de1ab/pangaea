package providerfactory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"github.com/0xc0de1ab/pangaea/pkg/formats/antigravitystate"
)

func TestRegistryRejectsDuplicateDefinitions(t *testing.T) {
	_, err := NewRegistry(
		Definition{Service: provider.ServiceCodex},
		Definition{Service: provider.ServiceCodex},
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate provider definition") {
		t.Fatalf("expected duplicate definition error, got %v", err)
	}
}

func TestDefaultRegistryHasProviderDefinitions(t *testing.T) {
	registry := DefaultRegistry()
	for _, service := range []provider.Service{
		provider.ServiceCodex,
		provider.ServiceClaude,
		provider.ServiceGemini,
		provider.ServiceAntigravity,
		provider.ServiceGitHubCopilot,
	} {
		if _, ok := registry.Definition(service); !ok {
			t.Fatalf("default registry missing %s definition", service)
		}
	}
}

func TestAuthStatusFromValidationMarksStaleOKExpiryRefreshSoon(t *testing.T) {
	now := time.Now().UTC()
	got := authStatusFromValidation(formats.StatusOK, now.Add(-time.Minute), now)
	if got != provider.AuthRefreshSoon {
		t.Fatalf("auth status = %q, want %q", got, provider.AuthRefreshSoon)
	}
	got = authStatusFromValidation(formats.StatusOK, now.Add(time.Hour), now)
	if got != provider.AuthHealthy {
		t.Fatalf("auth status = %q, want %q", got, provider.AuthHealthy)
	}
}

func TestAuthStateFromValidationTreatsAntigravityStaleExpiryAsAdvisory(t *testing.T) {
	now := time.Now().UTC()
	status, expiresAt, detail := authStateFromValidation(
		antigravitystate.Format{},
		formats.ValidationResult{
			Status: formats.StatusOK,
			Detail: "antigravity oauth expiry is stale in state.vscdb but may be refreshed in ls-core memory",
		},
		now.Add(-time.Minute),
		now,
	)
	if status != provider.AuthHealthy {
		t.Fatalf("auth status = %q, want %q", status, provider.AuthHealthy)
	}
	if !expiresAt.IsZero() {
		t.Fatalf("expiresAt = %s, want zero advisory expiry", expiresAt)
	}
	if detail != "" {
		t.Fatalf("detail = %q, want empty", detail)
	}
}

func TestCLIAdapterNames(t *testing.T) {
	normalizer := normalizeCLICommandAdapter()
	for _, mode := range []string{"", "cli-adapter"} {
		got, err := normalizer(Config{Service: "gemini", ProviderMode: mode})
		if err != nil {
			t.Fatalf("normalize %q: %v", mode, err)
		}
		if got != "cli-adapter" {
			t.Fatalf("normalize %q = %q, want cli-adapter", mode, got)
		}
	}
	got, err := normalizer(Config{Service: "gemini", ProviderMode: "http-direct", UpstreamBaseURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("normalize http-direct: %v", err)
	}
	if got != "direct-http" {
		t.Fatalf("http-direct normalized to %q, want direct-http", got)
	}
	for _, mode := range []string{"api-compatible", "cli-oneshot", "gemini-cli", "claude-cli", "direct-http"} {
		if got, err := normalizer(Config{Service: "gemini", ProviderMode: mode}); err == nil {
			t.Fatalf("normalize %q = %q, want error", mode, got)
		}
	}
}

func TestGeminiDirectHTTPCLIContainerDoesNotRequireUpstreamBaseURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PANGAEA_GEMINI_SETTINGS_PATH", filepath.Join(dir, "missing-settings.json"))
	registry := DefaultRegistry()
	definition, ok := registry.Definition(provider.ServiceGemini)
	if !ok {
		t.Fatal("missing gemini definition")
	}
	apiProvider, handled, err := definition.BuildCLIContainerAdapter(nil, BuildContext{
		Config: Config{
			ProviderID:         "gemini-cli",
			ProviderInstanceID: "gemini-cli",
			NodeID:             "node-1",
			HostName:           "host-1",
			Service:            "gemini",
			ProviderMode:       "http-direct",
			UpstreamDialect:    "gemini",
			AuthPath:           filepath.Join(dir, "oauth_creds.json"),
			Models:             "gemini-2.5-flash",
		},
		Auth: provider.AuthState{Status: provider.AuthHealthy, Refreshable: true, ExpiresAt: time.Now().Add(time.Hour)},
	}, "direct-http")
	if err != nil {
		t.Fatalf("BuildCLIContainerAdapter: %v", err)
	}
	if !handled {
		t.Fatal("gemini direct-http was not handled")
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer {
		t.Fatalf("kind = %q, want %q", registration.Identity.Kind, provider.KindCLIContainer)
	}
}

func TestCodexDirectHTTPCLIContainerDoesNotRequireUpstreamBaseURL(t *testing.T) {
	dir := t.TempDir()
	prependFakeCodexVersion(t, "0.129.0")
	registry := DefaultRegistry()
	definition, ok := registry.Definition(provider.ServiceCodex)
	if !ok {
		t.Fatal("missing codex definition")
	}
	apiProvider, handled, err := definition.BuildCLIContainerAdapter(nil, BuildContext{
		Config: Config{
			ProviderID:         "codex-cli",
			ProviderInstanceID: "codex-cli",
			NodeID:             "node-1",
			HostName:           "host-1",
			Service:            "codex",
			ProviderMode:       "http-direct",
			UpstreamDialect:    "openai",
			AuthPath:           filepath.Join(dir, "auth.json"),
			Models:             "gpt-5.5=codex-default",
		},
		Auth: provider.AuthState{Status: provider.AuthHealthy, Refreshable: true, ExpiresAt: time.Now().Add(time.Hour)},
	}, "direct-http")
	if err != nil {
		t.Fatalf("BuildCLIContainerAdapter: %v", err)
	}
	if !handled {
		t.Fatal("codex direct-http was not handled")
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer {
		t.Fatalf("kind = %q, want %q", registration.Identity.Kind, provider.KindCLIContainer)
	}
	if len(registration.Models) != 1 || registration.Models[0].ID != "gpt-5.5" {
		t.Fatalf("models = %#v", registration.Models)
	}
}

func prependFakeCodexVersion(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  printf 'codex-cli " + version + "\\n'\n  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestBuildCLIContainerProviderWithoutAuthPathReportsNoLogin(t *testing.T) {
	t.Setenv("PANGAEA_GEMINI_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing-settings.json"))
	result, err := BuildCLIContainerProvider(context.Background(), Config{
		ProviderID:         "gemini-cli",
		ProviderInstanceID: "gemini-cli",
		NodeID:             "node-1",
		HostName:           "host-1",
		Service:            "gemini",
		ProviderMode:       "http-direct",
		UpstreamDialect:    "gemini",
		Models:             "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("BuildCLIContainerProvider: %v", err)
	}
	if result.AuthRefresher != nil {
		t.Fatalf("no-login provider should not configure auth refresher")
	}
	registration, err := result.Provider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Auth.Status != provider.AuthNoLogin || registration.Auth.Refreshable {
		t.Fatalf("auth = %#v", registration.Auth)
	}
	if hasFactoryCapability(registration.Capabilities, provider.CapabilityAuthFile) || hasFactoryCapability(registration.Capabilities, provider.CapabilityAuthRefreshOneshot) {
		t.Fatalf("no-login capabilities include auth operations: %v", registration.Capabilities)
	}
	authReporter, ok := result.Provider.(interface {
		Auth() (provider.AuthState, error)
	})
	if !ok {
		t.Fatalf("provider does not expose Auth reporter")
	}
	auth, err := authReporter.Auth()
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if auth.Status != provider.AuthNoLogin {
		t.Fatalf("reported auth = %#v", auth)
	}
}

func TestGeminiMCPServersJSONFromSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"selectedAuthType":"oauth-personal","mcpServers":{"pangaea-fixture":{"command":"node","args":["server.mjs"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PANGAEA_GEMINI_SETTINGS_PATH", settingsPath)
	got := geminiMCPServersJSONFromSettings()
	if !strings.Contains(got, "pangaea-fixture") {
		t.Fatalf("settings mcp json = %q", got)
	}
}

func TestRegistrationModelsParsesMultipleModelAliases(t *testing.T) {
	models, err := registrationModels(Config{
		Service: "gemini",
		Models:  "gemini-2.5-flash=gemini-default|flash,gemini-2.5-pro=pro",
	}, []provider.Capability{provider.CapabilityOpenAIChat}, nil)
	if err != nil {
		t.Fatalf("registration models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2: %#v", len(models), models)
	}
	if models[0].ID != "gemini-2.5-flash" || strings.Join(models[0].Aliases, ",") != "gemini-default,flash" {
		t.Fatalf("unexpected first model: %#v", models[0])
	}
	if models[0].MaxContextTokens != 1_048_576 {
		t.Fatalf("gemini context = %d, want 1048576", models[0].MaxContextTokens)
	}
}

func hasFactoryCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func TestRegistrationModelsAnnotatesGeminiAutoGroups(t *testing.T) {
	models, err := registrationModels(Config{
		Service: "gemini",
		Models:  "auto-gemini-3=gemini-auto,auto-gemini-2.5",
	}, []provider.Capability{provider.CapabilityGeminiGenerateContent}, nil)
	if err != nil {
		t.Fatalf("registration models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2: %#v", len(models), models)
	}
	if models[0].Kind != "group" || strings.Join(models[0].GroupMembers, ",") != "gemini-3.1-pro-preview,gemini-3-flash-preview" {
		t.Fatalf("unexpected auto-gemini-3 metadata: %#v", models[0])
	}
	if strings.Join(models[0].Aliases, ",") != "gemini-auto,Auto (Gemini 3)" {
		t.Fatalf("unexpected auto-gemini-3 aliases: %#v", models[0].Aliases)
	}
	if models[1].Kind != "group" || strings.Join(models[1].GroupMembers, ",") != "gemini-2.5-pro,gemini-2.5-flash" {
		t.Fatalf("unexpected auto-gemini-2.5 metadata: %#v", models[1])
	}
}
