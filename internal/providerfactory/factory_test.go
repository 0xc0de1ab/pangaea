package providerfactory

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"github.com/0xc0de1ab/pangaea/pkg/formats/antigravitystate"
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/cursorapitoken"
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/cursorcliconfig"
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/githubcopilotapps"
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
	staleExpiry := now.Add(-time.Minute)
	status, expiresAt, detail := authStateFromValidation(
		antigravitystate.Format{},
		formats.ValidationResult{
			Status: formats.StatusOK,
			Detail: "antigravity oauth expiry is stale in state.vscdb but may be refreshed in ls-core memory",
		},
		staleExpiry,
		now,
	)
	if status != provider.AuthHealthy {
		t.Fatalf("auth status = %q, want %q", status, provider.AuthHealthy)
	}
	if !expiresAt.Equal(staleExpiry) {
		t.Fatalf("expiresAt = %s, want stale advisory expiry %s", expiresAt, staleExpiry)
	}
	if detail != "" {
		t.Fatalf("detail = %q, want empty", detail)
	}
}

func TestBuildSidecarProviderAddsAntigravityRefreshProtocol(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "state.vscdb")
	if err := os.WriteFile(authPath, antigravityTestStateBytes(t, "ag@example.test", time.Now().UTC().Add(time.Hour), "test-refresh-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := BuildSidecarProviderWithRefresh(Config{
		ProviderType:       "antigravity-sidecar",
		ProviderInstanceID: "antigravity-sidecar",
		NodeID:             "node-1",
		HostName:           "host-1",
		Service:            string(provider.ServiceAntigravity),
		UpstreamBaseURL:    "http://127.0.0.1:8080",
		UpstreamDialect:    "openai",
		UpstreamAPIKey:     "proxy-key",
		AuthPath:           authPath,
		AuthFormat:         antigravitystate.FormatName,
		Models:             "antigravity-default",
	})
	if err != nil {
		t.Fatalf("BuildSidecarProviderWithRefresh: %v", err)
	}
	if result.AuthRefresher == nil {
		t.Fatal("antigravity sidecar should install a protocol auth refresher")
	}
	registration, err := result.Provider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if !registration.Auth.Refreshable {
		t.Fatalf("antigravity registration should be refreshable: %#v", registration.Auth)
	}
	if !hasCapability(registration.Capabilities, provider.CapabilityAuthRefreshProtocol) {
		t.Fatalf("antigravity registration missing refresh protocol capability: %v", registration.Capabilities)
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
			ProviderType:       "gemini-cli",
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
			ProviderType:       "codex-cli",
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

func TestCursorACPCLIContainerAdapterHandled(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tok")
	if err := os.WriteFile(tokenPath, []byte("test-api-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := DefaultRegistry()
	definition, ok := registry.Definition(provider.ServiceCursor)
	if !ok {
		t.Fatal("missing cursor definition")
	}
	apiProvider, handled, err := definition.BuildCLIContainerAdapter(nil, BuildContext{
		Config: Config{
			ProviderType:       "cursor-cli",
			ProviderInstanceID: "cursor-cli",
			NodeID:             "node-1",
			HostName:           "host-1",
			Service:            "cursor",
			ProviderMode:       "acp",
			UpstreamDialect:    "openai",
			AuthPath:           tokenPath,
			AuthFormat:         "cursor-api-token-plain-format",
			Models:             "gpt-5",
		},
		Auth: provider.AuthState{Status: provider.AuthHealthy},
	}, "cursor-acp")
	if err != nil {
		t.Fatalf("BuildCLIContainerAdapter: %v", err)
	}
	if !handled {
		t.Fatal("cursor acp adapter was not handled")
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer {
		t.Fatalf("kind = %q", registration.Identity.Kind)
	}
}

func TestCursorDirectHTTPCLIContainerUsesEnvBaseURL(t *testing.T) {
	t.Setenv("PANGAEA_CURSOR_DIRECT_BASE_URL", "http://127.0.0.1:9")
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tok")
	if err := os.WriteFile(tokenPath, []byte("test-api-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := DefaultRegistry()
	definition, ok := registry.Definition(provider.ServiceCursor)
	if !ok {
		t.Fatal("missing cursor definition")
	}
	apiProvider, handled, err := definition.BuildCLIContainerAdapter(nil, BuildContext{
		Config: Config{
			ProviderType:       "cursor-cli",
			ProviderInstanceID: "cursor-cli",
			NodeID:             "node-1",
			HostName:           "host-1",
			Service:            "cursor",
			ProviderMode:       "http-direct",
			UpstreamDialect:    "openai",
			AuthPath:           tokenPath,
			AuthFormat:         "cursor-api-token-plain-format",
			Models:             "gpt-5",
		},
		Auth: provider.AuthState{Status: provider.AuthHealthy},
	}, "direct-http")
	if err != nil {
		t.Fatalf("BuildCLIContainerAdapter: %v", err)
	}
	if !handled {
		t.Fatal("cursor http-direct adapter was not handled")
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Identity.Service != provider.ServiceCursor {
		t.Fatalf("service = %q", registration.Identity.Service)
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

func antigravityTestStateBytes(t *testing.T, email string, exp time.Time, token string) []byte {
	t.Helper()
	userOuter := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString([]byte(email))))
	tokenInfo := append([]byte(token+"|"), antigravityTestTimestampMessage(exp.Unix())...)
	tokenOuter := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(tokenInfo)))
	return []byte("antigravityUnifiedStateSync.userStatus|" + userOuter + "|antigravityUnifiedStateSync.oauthToken|" + tokenOuter)
}

func antigravityTestTimestampMessage(seconds int64) []byte {
	var out []byte
	out = append(out, 0x22, 0x01, 0x08)
	for seconds >= 0x80 {
		out = append(out, byte(seconds)|0x80)
		seconds >>= 7
	}
	return append(out, byte(seconds))
}

func hasCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func TestBuildCLIContainerProviderWithoutAuthPathReportsNoLogin(t *testing.T) {
	t.Setenv("PANGAEA_GEMINI_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing-settings.json"))
	result, err := BuildCLIContainerProvider(context.Background(), Config{
		ProviderType:       "gemini-cli",
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

func TestBuildGitHubCopilotSidecarSDKProvider(t *testing.T) {
	result, err := BuildSidecarProviderWithRefresh(Config{
		ProviderType:       "github-copilot-sidecar",
		ProviderInstanceID: "github-copilot-sidecar",
		NodeID:             "node-1",
		HostName:           "host-1",
		Service:            "github-copilot",
		ProviderMode:       "sdk",
		UpstreamBaseURL:    "http://127.0.0.1:4141",
		UpstreamDialect:    "openai",
		ShimProtocols:      "openai",
		Model:              "github-copilot-default",
		ModelAlias:         "copilot-default",
	})
	if err != nil {
		t.Fatalf("BuildSidecarProviderWithRefresh: %v", err)
	}
	registration, err := result.Provider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindSidecar || registration.Identity.Service != provider.ServiceGitHubCopilot {
		t.Fatalf("identity = %#v", registration.Identity)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead, provider.CapabilityCodeCompletion} {
		if !hasFactoryCapability(registration.Capabilities, capability) {
			t.Fatalf("capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
	if hasFactoryCapability(registration.Capabilities, provider.CapabilityAgentWorkspaceWrite) {
		t.Fatalf("sdk provider should not advertise workspace write: %v", registration.Capabilities)
	}
}

func TestBuildGitHubCopilotSidecarUsesDefaultAuthFormat(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(authPath, []byte(`// User settings belong in settings.json.
{
  "lastLoggedInUser": {
    "host": "https://github.com",
    "login": "octocat"
  },
  "copilotTokens": {
    "https://github.com:octocat": "copilot_secret_tail"
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := BuildSidecarProviderWithRefresh(Config{
		ProviderType:       "github-copilot-sidecar",
		ProviderInstanceID: "github-copilot-sidecar",
		NodeID:             "node-1",
		HostName:           "host-1",
		Service:            "github-copilot",
		ProviderMode:       "sdk",
		UpstreamBaseURL:    "http://127.0.0.1:4141",
		UpstreamDialect:    "openai",
		AuthPath:           authPath,
		Model:              "github-copilot-default",
		ModelAlias:         "copilot-default",
	})
	if err != nil {
		t.Fatalf("BuildSidecarProviderWithRefresh: %v", err)
	}
	registration, err := result.Provider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Auth.Account.Display != "octocat" || registration.Identity.Account.Display != "octocat" {
		t.Fatalf("account not derived from default auth format: identity=%#v auth=%#v", registration.Identity.Account, registration.Auth.Account)
	}
	if !hasFactoryCapability(registration.Capabilities, provider.CapabilityAuthFile) {
		t.Fatalf("capabilities missing auth file: %v", registration.Capabilities)
	}
}

func TestBuildGitHubCopilotACPProvider(t *testing.T) {
	result, err := BuildCLIContainerProvider(context.Background(), Config{
		ProviderType:       "github-copilot-acp",
		ProviderInstanceID: "github-copilot-acp",
		NodeID:             "node-1",
		HostName:           "host-1",
		Service:            "github-copilot",
		ProviderMode:       "acp",
		UpstreamDialect:    "openai",
		ShimCapabilities:   "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,code.completion,usage.read,models.read",
		Model:              "github-copilot-default",
		ModelAlias:         "copilot-default",
	})
	if err != nil {
		t.Fatalf("BuildCLIContainerProvider: %v", err)
	}
	registration, err := result.Provider.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer || registration.Identity.Service != provider.ServiceGitHubCopilot {
		t.Fatalf("identity = %#v", registration.Identity)
	}
	if hasFactoryCapability(registration.Capabilities, provider.CapabilityStreamSSE) {
		t.Fatalf("acp provider should not advertise streaming until implemented: %v", registration.Capabilities)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent} {
		if !hasFactoryCapability(registration.Capabilities, capability) {
			t.Fatalf("acp capabilities %v missing %s", registration.Capabilities, capability)
		}
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

func TestRegistrationModelsUsesMiniMAXM2Metadata(t *testing.T) {
	models, err := registrationModels(Config{
		Service: "minimax",
		Models:  "MiniMax-M2.7=minimax-default",
	}, []provider.Capability{provider.CapabilityAnthropicMessages}, nil)
	if err != nil {
		t.Fatalf("registration models: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1: %#v", len(models), models)
	}
	if models[0].MaxContextTokens != 204_800 || models[0].MaxOutputTokens != 2_048 {
		t.Fatalf("MiniMax-M2.7 metadata = context %d output %d, want 204800/2048", models[0].MaxContextTokens, models[0].MaxOutputTokens)
	}
}

func TestBuildMiniMAXAPIProviderAdvertisesPublicDialects(t *testing.T) {
	client, err := BuildAPICompatibleProvider(Config{
		ProviderType:       "minimax-api",
		ProviderInstanceID: "minimax-api",
		NodeID:             "node-1",
		HostName:           "host-1",
		Service:            "minimax",
		Account:            "minimax@example.test",
		UpstreamBaseURL:    "https://api.minimax.io/anthropic",
		UpstreamDialect:    "anthropic",
		Model:              "MiniMax-M2.7",
		ModelAlias:         "minimax-default",
	})
	if err != nil {
		t.Fatalf("BuildAPICompatibleProvider: %v", err)
	}
	registration, err := client.Registration()
	if err != nil {
		t.Fatalf("Registration: %v", err)
	}
	for _, capability := range []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
	} {
		if !hasFactoryCapability(registration.Capabilities, capability) {
			t.Fatalf("MiniMAX advertised capabilities %v missing %s", registration.Capabilities, capability)
		}
		if len(registration.Models) != 1 || !hasFactoryCapability(registration.Models[0].Capabilities, capability) {
			t.Fatalf("MiniMAX model capabilities %#v missing %s", registration.Models, capability)
		}
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
