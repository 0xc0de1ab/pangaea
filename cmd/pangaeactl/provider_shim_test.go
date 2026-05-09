package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providerfactory"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func TestRunProviderShimRequiresRouterControlURL(t *testing.T) {
	err := runProviderShim(context.Background(), providerShimRunOptions{Simulator: true})
	if err == nil {
		t.Fatalf("expected router control url error")
	}
}

func TestRunProviderShimRequiresSimulatorForNow(t *testing.T) {
	err := runProviderShim(context.Background(), providerShimRunOptions{RouterControlURL: "ws://127.0.0.1/unused"})
	if err == nil {
		t.Fatalf("expected provider mode required error")
	}
}

func TestProviderShimRunOptionsApplyEnvDefaults(t *testing.T) {
	t.Setenv("PANGAEA_SHIM_MODE", "cli-container")
	t.Setenv("PANGAEA_ROUTER_CONTROL_URL", "ws://router/control")
	t.Setenv("PANGAEA_ROUTER_PEER_TOKEN", "peer-secret")
	t.Setenv("PANGAEA_STREAM_TOKEN_KEY", "env-stream-token-key")
	t.Setenv("PANGAEA_PROVIDER_ID", "codex-primary")
	t.Setenv("PANGAEA_PROVIDER_INSTANCE_ID", "codex-primary-a1")
	t.Setenv("PANGAEA_NODE_ID", "node-a1")
	t.Setenv("PANGAEA_HOST_NAME", "snowbox")
	t.Setenv("PANGAEA_CONTAINER_ID", "container-abc123")
	t.Setenv("PANGAEA_CONTAINER_KIND", "docker")
	t.Setenv("PANGAEA_CONTAINER_NAME", "pangaea-codex-primary")
	t.Setenv("PANGAEA_SERVICE", "codex")
	t.Setenv("PANGAEA_ACCOUNT_DISPLAY", "codex@example.test")
	t.Setenv("PANGAEA_PROVIDER_MODE", "app-server")
	t.Setenv("PANGAEA_UPSTREAM_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("PANGAEA_UPSTREAM_DIALECT", "openai")
	t.Setenv("PANGAEA_MODEL", "gpt-5-codex")
	t.Setenv("PANGAEA_MODELS", "gpt-5-codex=codex-default|codex-latest")
	t.Setenv("PANGAEA_UPSTREAM_API_KEY_MODE", "header")
	t.Setenv("PANGAEA_UPSTREAM_API_KEY_HEADER", "x-api-key")
	t.Setenv("PANGAEA_UPSTREAM_API_KEY_QUERY_PARAM", "key")
	t.Setenv("PANGAEA_SHIM_PROTOCOLS", "openai,anthropic,gemini")
	t.Setenv("PANGAEA_SHIM_CAPABILITIES", "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,stream.sse,usage.read,models.read")
	t.Setenv("PANGAEA_MODEL_ALIAS", "codex-default")
	t.Setenv("PANGAEA_MODEL_CAPABILITIES", "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,stream.sse")
	t.Setenv("PANGAEA_AUTH_PATH", "/var/lib/pangaea/auth/codex/auth.json")
	t.Setenv("PANGAEA_AUTH_FORMAT", "codex-auth-json-format")
	t.Setenv("PANGAEA_REFRESH_COMMAND", "codex exec ping")
	t.Setenv("PANGAEA_REFRESH_LOGIN_SHELL", "false")
	t.Setenv("PANGAEA_CLI_REQUEST_TIMEOUT", "70s")
	t.Setenv("PANGAEA_REFRESH_TIMEOUT", "45s")
	t.Setenv("PANGAEA_REFRESH_THRESHOLD", "5m")
	t.Setenv("PANGAEA_REFRESH_COOLDOWN", "90s")
	t.Setenv("PANGAEA_AUTH_BOOTSTRAP_TIMEOUT", "3s")
	t.Setenv("PANGAEA_MCP_SERVERS_JSON", `{"mcpServers":{"pangaea-fixture":{"command":"node","args":["server.mjs"]}}}`)
	t.Setenv("PANGAEA_MCP_TOOL_ROUNDS", "7")

	opts := applyProviderShimEnvDefaults(providerShimRunOptions{RefreshLoginShell: true, StreamTokenKey: defaultStreamTokenKey, UpstreamDialect: "openai"})
	if !opts.CLIContainer || opts.RouterControlURL != "ws://router/control" || opts.RouterPeerToken != "peer-secret" || opts.ProviderID != "codex-primary" {
		t.Fatalf("env defaults did not populate identity/mode: %#v", opts)
	}
	if opts.StreamTokenKey != "env-stream-token-key" {
		t.Fatalf("env defaults did not override default stream token key: %#v", opts)
	}
	if opts.ContainerID != "container-abc123" || opts.ContainerKind != "docker" || opts.ContainerName != "pangaea-codex-primary" {
		t.Fatalf("env defaults did not populate container metadata: %#v", opts)
	}
	if opts.Account != "codex@example.test" || opts.ProviderMode != "app-server" || opts.UpstreamBaseURL != "http://127.0.0.1:8080" || opts.Model != "gpt-5-codex" || opts.Models != "gpt-5-codex=codex-default|codex-latest" {
		t.Fatalf("env defaults did not populate provider config: %#v", opts)
	}
	if opts.UpstreamAPIKeyMode != "header" || opts.UpstreamAPIKeyHeader != "x-api-key" || opts.UpstreamAPIKeyQueryParam != "key" {
		t.Fatalf("env defaults did not populate upstream api key auth config: %#v", opts)
	}
	if opts.ShimProtocols != "openai,anthropic,gemini" || !strings.Contains(opts.ShimCapabilities, "api.gemini.generateContent") || !strings.Contains(opts.ModelCapabilities, "api.anthropic.messages") {
		t.Fatalf("env defaults did not populate shim/model capabilities: %#v", opts)
	}
	if opts.AuthPath != "/var/lib/pangaea/auth/codex/auth.json" || opts.AuthFormat != "codex-auth-json-format" || opts.RefreshCommand != "codex exec ping" {
		t.Fatalf("env defaults did not populate auth config: %#v", opts)
	}
	if opts.RefreshLoginShell || opts.CLIRequestTimeout != 70*time.Second || opts.RefreshTimeout != 45*time.Second || opts.RefreshThreshold != 5*time.Minute || opts.RefreshCooldown != 90*time.Second || opts.AuthBootstrapTimeout != 3*time.Second {
		t.Fatalf("env defaults did not populate refresh options: %#v", opts)
	}
	if !strings.Contains(opts.MCPServersJSON, "pangaea-fixture") || opts.MCPToolRounds != 7 {
		t.Fatalf("env defaults did not populate mcp options: %#v", opts)
	}
}

func TestProviderShimRunOptionsApplySidecarEnvMode(t *testing.T) {
	t.Setenv("PANGAEA_SHIM_MODE", "sidecar-agent")
	opts := applyProviderShimEnvDefaults(providerShimRunOptions{StreamTokenKey: defaultStreamTokenKey, UpstreamDialect: "openai"})
	if !opts.Sidecar || opts.Simulator || opts.APICompatible || opts.CLIContainer {
		t.Fatalf("expected sidecar mode from env, got %#v", opts)
	}
}

func TestProviderShimRunOptionsReadsNodeIDFromRuntimeSettings(t *testing.T) {
	path := t.TempDir() + "/provider.env"
	if err := os.WriteFile(path, []byte("PANGAEA_NODE_ID=ab12cd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PANGAEA_RUNTIME_SETTINGS_PATH", path)
	opts := applyProviderShimEnvDefaults(providerShimRunOptions{StreamTokenKey: defaultStreamTokenKey, UpstreamDialect: "openai"})
	if opts.NodeID != "ab12cd" {
		t.Fatalf("node id = %q, want runtime settings node id", opts.NodeID)
	}
}

func TestProviderShimHostNamePrefersHostSideNameOverContainerHostname(t *testing.T) {
	t.Setenv("HOSTNAME", "pod-abc123")
	t.Setenv("PANGAEA_HOST_NAME", "pod-abc123")
	t.Setenv("PANGAEA_HOST_HOSTNAME", "snowbox")
	t.Setenv("PANGAEA_CONTAINER_KIND", "kubernetes")

	opts := applyProviderShimEnvDefaults(providerShimRunOptions{StreamTokenKey: defaultStreamTokenKey, UpstreamDialect: "openai"})
	if opts.HostName != "snowbox" {
		t.Fatalf("host name = %q, want host-side name", opts.HostName)
	}
	if opts.ContainerID != "pod-abc123" {
		t.Fatalf("container id = %q, want pod/container hostname fallback", opts.ContainerID)
	}
}

func TestProviderShimRunOptionsApplyAppServerEnvMode(t *testing.T) {
	t.Setenv("PANGAEA_SHIM_MODE", "app-server")
	opts := applyProviderShimEnvDefaults(providerShimRunOptions{StreamTokenKey: defaultStreamTokenKey, UpstreamDialect: "openai"})
	if !opts.CLIContainer || opts.Simulator || opts.APICompatible || opts.Sidecar {
		t.Fatalf("expected app-server env mode to use cli-container runner, got %#v", opts)
	}
}

func TestProviderShimRunCommandExists(t *testing.T) {
	cmd := newProviderShimRunCmd()
	if cmd.Use != "run" {
		t.Fatalf("expected run command, got %q", cmd.Use)
	}
	flag := cmd.Flags().Lookup("heartbeat-interval")
	if flag == nil {
		t.Fatalf("expected heartbeat-interval flag")
	}
	if _, err := time.ParseDuration(flag.DefValue); err != nil {
		t.Fatalf("heartbeat interval default is not a duration: %v", err)
	}
	if cmd.Flags().Lookup("router-data") == nil {
		t.Fatalf("expected router-data flag")
	}
	if cmd.Flags().Lookup("router-peer-token") == nil {
		t.Fatalf("expected router-peer-token flag")
	}
	if cmd.Flags().Lookup("stream-token-key") == nil {
		t.Fatalf("expected stream-token-key flag")
	}
	for _, name := range []string{"api-compatible", "cli-container", "sidecar", "provider-id", "provider-instance-id", "node-id", "host-name", "container-id", "container-kind", "container-name", "service", "account", "provider-mode", "upstream-base-url", "upstream-dialect", "upstream-api-key", "upstream-api-key-file", "upstream-api-key-mode", "upstream-api-key-header", "upstream-api-key-query-param", "shim-protocols", "shim-capabilities", "model", "models", "model-alias", "model-capabilities", "auth-path", "auth-format", "auth-bootstrap-timeout", "refresh-command", "refresh-login-shell", "cli-request-timeout", "refresh-timeout", "refresh-threshold", "refresh-cooldown"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected %s flag", name)
		}
	}
}

func TestBuildAPICompatibleProviderRejectsIncompleteAPIKeyHeaderMode(t *testing.T) {
	_, err := buildAPICompatibleProvider(providerShimRunOptions{
		ProviderID:         "gemini-api",
		ProviderInstanceID: "gemini-api-0001",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "gemini",
		UpstreamBaseURL:    "https://generativelanguage.googleapis.com",
		UpstreamDialect:    "gemini",
		UpstreamAPIKey:     "key",
		UpstreamAPIKeyMode: "header",
		Model:              "gemini-2.5-pro",
	})
	if err == nil {
		t.Fatalf("expected incomplete api key header mode error")
	}
}

func TestBuildAPICompatibleProvider(t *testing.T) {
	apiProvider, err := buildAPICompatibleProvider(providerShimRunOptions{
		ProviderID:         "deepseek-api",
		ProviderInstanceID: "deepseek-api-0001",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "deepseek",
		Account:            "deepseek@example.test",
		UpstreamBaseURL:    "https://api.example.test",
		UpstreamDialect:    "openai",
		Model:              "deepseek-chat",
		ModelAlias:         "deepseek-default",
	})
	if err != nil {
		t.Fatalf("build api-compatible provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != "api-compatible" || registration.Identity.Service != "deepseek" {
		t.Fatalf("unexpected registration identity: %#v", registration.Identity)
	}
	if len(registration.Models) != 1 || registration.Models[0].Aliases[0] != "deepseek-default" {
		t.Fatalf("unexpected model registration: %#v", registration.Models)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead} {
		if !hasCapability(registration.Capabilities, capability) {
			t.Fatalf("capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
	if !hasCapability(registration.Models[0].Capabilities, provider.CapabilityStreamSSE) {
		t.Fatalf("model capabilities %v missing %s", registration.Models[0].Capabilities, provider.CapabilityStreamSSE)
	}
}

func TestBuildAPICompatibleProviderRequiresFields(t *testing.T) {
	_, err := buildAPICompatibleProvider(providerShimRunOptions{
		ProviderID:         "deepseek-api",
		ProviderInstanceID: "deepseek-api-0001",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "deepseek",
		UpstreamDialect:    "openai",
		Model:              "deepseek-chat",
	})
	if err == nil {
		t.Fatalf("expected upstream base url error")
	}
}

func TestBuildCLIContainerProviderUsesAuthFileAndRefreshCommand(t *testing.T) {
	registerProviderShimTestFormat()
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	if err := os.WriteFile(authPath, []byte("healthy"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	apiProvider, refresher, err := buildCLIContainerProvider(context.Background(), providerShimRunOptions{
		ProviderID:         "codex-primary",
		ProviderInstanceID: "codex-primary-a1",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "codex",
		Account:            "fallback@example.test",
		UpstreamBaseURL:    "http://127.0.0.1:4848",
		UpstreamDialect:    "openai",
		Model:              "gpt-5-codex",
		ModelAlias:         "codex-default",
		AuthPath:           authPath,
		AuthFormat:         "provider-shim-test-format",
		RefreshCommand:     "codex exec --skip-git-repo-check ping",
		RefreshLoginShell:  true,
		RefreshTimeout:     time.Minute,
	})
	if err != nil {
		t.Fatalf("build cli-container provider: %v", err)
	}
	if refresher == nil {
		t.Fatalf("expected auth refresher")
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer || registration.Identity.Service != provider.ServiceCodex {
		t.Fatalf("unexpected registration identity: %#v", registration.Identity)
	}
	if registration.Auth.Status != provider.AuthHealthy || !registration.Auth.Refreshable || registration.Auth.SelectedSource != "container" {
		t.Fatalf("unexpected auth state: %#v", registration.Auth)
	}
	if registration.Auth.Account.ID != "test-account" || registration.Auth.Account.Display != "test@example.test" {
		t.Fatalf("unexpected auth account: %#v", registration.Auth.Account)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead, provider.CapabilityAuthFile, provider.CapabilityAuthRefreshOneshot} {
		if !hasCapability(registration.Capabilities, capability) {
			t.Fatalf("capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
	if !hasCapability(registration.Models[0].Capabilities, provider.CapabilityStreamSSE) {
		t.Fatalf("model capabilities %v missing %s", registration.Models[0].Capabilities, provider.CapabilityStreamSSE)
	}
}

func TestBuildCLIContainerProviderReportsCodexWebSocketAsAppServer(t *testing.T) {
	registerProviderShimTestFormat()
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	if err := os.WriteFile(authPath, []byte("healthy"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	apiProvider, _, err := buildCLIContainerProvider(context.Background(), providerShimRunOptions{
		ProviderID:         "codex-cli",
		ProviderInstanceID: "codex-cli",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "codex",
		ProviderMode:       "app-server",
		UpstreamBaseURL:    "ws://127.0.0.1:8080",
		UpstreamDialect:    "openai",
		Model:              "gpt-5.5",
		ModelAlias:         "codex-default",
		AuthPath:           authPath,
		AuthFormat:         "provider-shim-test-format",
	})
	if err != nil {
		t.Fatalf("build codex websocket provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindAppServer {
		t.Fatalf("codex websocket kind = %q, want %q", registration.Identity.Kind, provider.KindAppServer)
	}
	if registration.Identity.Account.Display != "test@example.test" {
		t.Fatalf("identity account display = %q, want test@example.test", registration.Identity.Account.Display)
	}
}

func TestBuildCLIContainerProviderUsesConfiguredMultiDialectCapabilities(t *testing.T) {
	registerProviderShimTestFormat()
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	if err := os.WriteFile(authPath, []byte("healthy"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	apiProvider, _, err := buildCLIContainerProvider(context.Background(), providerShimRunOptions{
		ProviderID:           "codex-cli",
		ProviderInstanceID:   "codex-cli",
		NodeID:               "node-a1",
		HostName:             "snowbox",
		Service:              "codex",
		ProviderMode:         "app-server",
		UpstreamBaseURL:      "ws://127.0.0.1:8080",
		UpstreamDialect:      "openai",
		ShimCapabilities:     "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,stream.sse,usage.read,models.read",
		Model:                "gpt-5.5",
		ModelAlias:           "codex-default",
		ModelCapabilities:    "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,stream.sse",
		AuthPath:             authPath,
		AuthFormat:           "provider-shim-test-format",
		RefreshCommand:       "codex exec ping",
		RefreshLoginShell:    true,
		RefreshTimeout:       time.Minute,
		RefreshThreshold:     5 * time.Minute,
		RefreshCooldown:      5 * time.Minute,
		CLIRequestTimeout:    time.Minute,
		AuthBootstrapTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("build codex websocket provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	for _, capability := range []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityStreamSSE,
		provider.CapabilityUsageRead,
		provider.CapabilityModelsRead,
		provider.CapabilityAuthFile,
		provider.CapabilityAuthRefreshOneshot,
	} {
		if !hasCapability(registration.Capabilities, capability) {
			t.Fatalf("registration capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
	if len(registration.Models) != 1 {
		t.Fatalf("unexpected models: %#v", registration.Models)
	}
	for _, capability := range []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityStreamSSE,
	} {
		if !hasCapability(registration.Models[0].Capabilities, capability) {
			t.Fatalf("model capabilities %v missing %s", registration.Models[0].Capabilities, capability)
		}
	}
	if hasCapability(registration.Models[0].Capabilities, provider.CapabilityUsageRead) {
		t.Fatalf("model capabilities should not include provider-only usage capability: %v", registration.Models[0].Capabilities)
	}
}

func TestNativeUsageProbeProviderPreservesStreaming(t *testing.T) {
	base := &streamingUsageProbeTestProvider{}
	wrapped := wrapNativeUsageProbe(base, filepath.Join(t.TempDir(), "auth.json"), providerShimTestUsageProbeFormat{})
	streamInvoker, ok := wrapped.(interface {
		InvokeStream(context.Context, provider.Registration, compat.Request, func(compat.Event) error) (compat.Response, error)
	})
	if !ok {
		t.Fatalf("wrapped provider does not implement InvokeStream")
	}
	var deltas []string
	response, err := streamInvoker.InvokeStream(context.Background(), provider.Registration{}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "stream-model",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "ping"}},
		}},
		Stream: true,
	}, func(event compat.Event) error {
		if event.Type == compat.EventContentDelta && event.ContentDelta != nil {
			deltas = append(deltas, event.ContentDelta.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if !base.streamCalled {
		t.Fatalf("expected native stream provider to be called")
	}
	if response.Message.Content[0].Text != "hello stream" || strings.Join(deltas, "") != "hello stream" {
		t.Fatalf("unexpected stream response=%#v deltas=%#v", response, deltas)
	}
}

func TestBuildCLIContainerProviderUsesClaudeCLIAdapterWithoutUpstreamURL(t *testing.T) {
	registerProviderShimTestFormat()
	dir := t.TempDir()
	authPath := dir + "/credentials.json"
	if err := os.WriteFile(authPath, []byte("healthy"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	apiProvider, refresher, err := buildCLIContainerProvider(context.Background(), providerShimRunOptions{
		ProviderID:         "claude-primary",
		ProviderInstanceID: "claude-primary-a1",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "claude",
		Account:            "fallback@example.test",
		ProviderMode:       "cli-adapter",
		UpstreamDialect:    "anthropic",
		Model:              "claude-default",
		ModelAlias:         "claude-default",
		AuthPath:           authPath,
		AuthFormat:         "provider-shim-test-format",
		RefreshLoginShell:  true,
		CLIRequestTimeout:  time.Minute,
	})
	if err != nil {
		t.Fatalf("build claude cli-container provider: %v", err)
	}
	if refresher != nil {
		t.Fatalf("did not expect auth refresher without refresh command")
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer || registration.Identity.Service != provider.ServiceClaude {
		t.Fatalf("unexpected registration identity: %#v", registration.Identity)
	}
	if len(registration.Models) != 1 || registration.Models[0].ID != "claude-default" {
		t.Fatalf("unexpected models: %#v", registration.Models)
	}
	for _, capability := range []provider.Capability{provider.CapabilityAnthropicMessages, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead, provider.CapabilityAuthFile} {
		if !hasCapability(registration.Capabilities, capability) {
			t.Fatalf("capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
}

func TestBuildSidecarProviderForGitHubCopilot(t *testing.T) {
	apiProvider, err := buildSidecarProvider(providerShimRunOptions{
		ProviderID:         "copilot-sidecar",
		ProviderInstanceID: "copilot-sidecar-a1",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "github-copilot",
		Account:            "copilot@example.test",
		UpstreamBaseURL:    "http://127.0.0.1:4141",
		UpstreamDialect:    "openai",
		Model:              "github-copilot-default",
		ModelAlias:         "copilot-default",
	})
	if err != nil {
		t.Fatalf("build sidecar provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindSidecar || registration.Identity.Service != provider.ServiceGitHubCopilot {
		t.Fatalf("unexpected registration identity: %#v", registration.Identity)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityCodeCompletion, provider.CapabilityAgentWorkspaceRead} {
		if !hasCapability(registration.Capabilities, capability) {
			t.Fatalf("capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
}

func TestBuildSidecarProviderForAntigravity(t *testing.T) {
	apiProvider, err := buildSidecarProvider(providerShimRunOptions{
		ProviderID:         "antigravity-sidecar",
		ProviderInstanceID: "antigravity-sidecar-a1",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		ContainerID:        "container-abc123",
		ContainerKind:      "kubernetes",
		ContainerName:      "shim",
		Service:            "antigravity",
		Account:            "antigravity@example.test",
		UpstreamBaseURL:    "http://127.0.0.1:8080",
		UpstreamDialect:    "openai",
		ShimProtocols:      "openai,anthropic,gemini",
		Model:              "antigravity-default",
		ModelAlias:         "antigravity-default",
	})
	if err != nil {
		t.Fatalf("build antigravity sidecar provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != provider.KindSidecar || registration.Identity.Service != provider.ServiceAntigravity {
		t.Fatalf("unexpected registration identity: %#v", registration.Identity)
	}
	if registration.Identity.HostName != "snowbox" || registration.Identity.ContainerName != "shim" || registration.Identity.ContainerKind != "kubernetes" || registration.Identity.ContainerID != "container-abc123" {
		t.Fatalf("unexpected container metadata: %#v", registration.Identity)
	}
	for _, capability := range []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityStreamSSE,
		provider.CapabilityUsageRead,
		provider.CapabilityModelsRead,
		provider.CapabilityAntigravitySidecar,
		provider.CapabilityAgentToolUse,
		provider.CapabilityAgentWorkspaceRead,
		provider.CapabilityAgentWorkspaceWrite,
	} {
		if !hasCapability(registration.Capabilities, capability) {
			t.Fatalf("capabilities %v missing %s", registration.Capabilities, capability)
		}
	}
	if !hasCapability(registration.Models[0].Capabilities, provider.CapabilityAgentToolUse) {
		t.Fatalf("antigravity model capabilities missing agent tool-use: %v", registration.Models[0].Capabilities)
	}
}

func TestBuildSidecarProviderReportsAuthSnapshot(t *testing.T) {
	registerProviderShimTestFormat()
	dir := t.TempDir()
	authPath := dir + "/state.vscdb"
	if err := os.WriteFile(authPath, []byte("healthy"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	apiProvider, err := buildSidecarProvider(providerShimRunOptions{
		ProviderID:         "antigravity-sidecar",
		ProviderInstanceID: "antigravity-sidecar-a1",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "antigravity",
		Account:            "fallback@example.test",
		UpstreamBaseURL:    "http://127.0.0.1:8080",
		UpstreamDialect:    "openai",
		ShimProtocols:      "openai,anthropic,gemini",
		Model:              "antigravity-default",
		ModelAlias:         "antigravity-default",
		AuthPath:           authPath,
		AuthFormat:         "provider-shim-test-format",
	})
	if err != nil {
		t.Fatalf("build antigravity sidecar provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Auth.Status != provider.AuthHealthy || registration.Auth.Account.Display != "test@example.test" {
		t.Fatalf("unexpected sidecar auth: %#v", registration.Auth)
	}
	if !hasCapability(registration.Capabilities, provider.CapabilityAuthFile) {
		t.Fatalf("sidecar capabilities %v missing %s", registration.Capabilities, provider.CapabilityAuthFile)
	}
	reporter, ok := apiProvider.(interface {
		AuthSnapshot(context.Context) (providershim.AuthSnapshotReport, error)
	})
	if !ok {
		t.Fatalf("sidecar provider does not expose auth snapshots")
	}
	report, err := reporter.AuthSnapshot(context.Background())
	if err != nil {
		t.Fatalf("auth snapshot: %v", err)
	}
	if string(report.Raw) != "healthy" || report.Format != "provider-shim-test-format" {
		t.Fatalf("unexpected auth snapshot: %#v", report)
	}
}

func TestBuildCLIContainerProviderWaitsForAuthBootstrap(t *testing.T) {
	registerProviderShimTestFormat()
	dir := t.TempDir()
	authPath := dir + "/auth.json"
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = os.WriteFile(authPath, []byte("healthy"), 0o600)
	}()

	apiProvider, _, err := buildCLIContainerProvider(context.Background(), providerShimRunOptions{
		ProviderID:           "codex-wait",
		ProviderInstanceID:   "codex-wait-a1",
		NodeID:               "node-a1",
		HostName:             "snowbox",
		Service:              "codex",
		UpstreamBaseURL:      "http://127.0.0.1:4848",
		UpstreamDialect:      "openai",
		Model:                "gpt-5-codex",
		AuthPath:             authPath,
		AuthFormat:           "provider-shim-test-format",
		AuthBootstrapTimeout: time.Second,
		RefreshLoginShell:    true,
		RefreshTimeout:       time.Minute,
		RefreshThreshold:     5 * time.Minute,
		RefreshCooldown:      5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("build cli-container provider with delayed auth: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Auth.Status != provider.AuthHealthy {
		t.Fatalf("unexpected auth after delayed bootstrap: %#v", registration.Auth)
	}
}

func TestBuildCLIContainerProviderTimesOutWaitingForAuthBootstrap(t *testing.T) {
	registerProviderShimTestFormat()
	_, _, err := buildCLIContainerProvider(context.Background(), providerShimRunOptions{
		ProviderID:           "codex-missing",
		ProviderInstanceID:   "codex-missing-a1",
		NodeID:               "node-a1",
		HostName:             "snowbox",
		Service:              "codex",
		UpstreamBaseURL:      "http://127.0.0.1:4848",
		UpstreamDialect:      "openai",
		Model:                "gpt-5-codex",
		AuthPath:             t.TempDir() + "/missing-auth.json",
		AuthFormat:           "provider-shim-test-format",
		AuthBootstrapTimeout: 10 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "auth bootstrap file") {
		t.Fatalf("expected auth bootstrap timeout, got %v", err)
	}
}

func TestRefreshCommandArgsSourcesBashRC(t *testing.T) {
	got := refreshCommandArgs("gemini --prompt ping", true)
	if len(got) != 3 || got[0] != "bash" || got[1] != "-lc" {
		t.Fatalf("unexpected login shell command: %v", got)
	}
	if !strings.Contains(got[2], ".bashrc") || !strings.Contains(got[2], "gemini --prompt ping") {
		t.Fatalf("login shell command did not source bashrc and execute command: %v", got)
	}
	got = refreshCommandArgs("codex exec ping", false)
	want := []string{"sh", "-c", "codex exec ping"}
	if len(got) != len(want) {
		t.Fatalf("plain shell command length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("plain shell command[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func buildAPICompatibleProvider(opts providerShimRunOptions) (*apiprovider.Provider, error) {
	return providerfactory.BuildAPICompatibleProvider(providerFactoryConfigFromOptions(opts))
}

func buildSidecarProvider(opts providerShimRunOptions) (providershim.APICompatibleProvider, error) {
	return providerfactory.BuildSidecarProvider(providerFactoryConfigFromOptions(opts))
}

func buildCLIContainerProvider(ctx context.Context, opts providerShimRunOptions) (providershim.APICompatibleProvider, providershim.AuthRefresher, error) {
	result, err := providerfactory.BuildCLIContainerProvider(ctx, providerFactoryConfigFromOptions(opts))
	return result.Provider, result.AuthRefresher, err
}

func wrapNativeUsageProbe(base providershim.APICompatibleProvider, authPath string, format formats.Format) providershim.APICompatibleProvider {
	return providerfactory.WrapNativeUsageProbe(base, authPath, format)
}

func refreshCommandArgs(command string, loginShell bool) []string {
	return providerfactory.RefreshCommandArgs(command, loginShell)
}

func hasCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

var registerProviderShimTestFormatOnce sync.Once

func registerProviderShimTestFormat() {
	registerProviderShimTestFormatOnce.Do(func() {
		formats.Register(providerShimTestFormat{})
	})
}

type providerShimTestFormat struct{}

func (providerShimTestFormat) Name() string         { return "provider-shim-test-format" }
func (providerShimTestFormat) Strategies() []string { return []string{"default"} }
func (providerShimTestFormat) Parse(raw []byte) (formats.Snapshot, error) {
	status := formats.StatusOK
	if strings.Contains(string(raw), "expired") {
		status = formats.StatusExpired
	}
	return providerShimTestSnapshot{
		raw:       append([]byte(nil), raw...),
		expiresAt: time.Now().UTC().Add(time.Hour),
		status:    status,
	}, nil
}
func (providerShimTestFormat) Validate(_ context.Context, snapshot formats.Snapshot, _ formats.ValidateOpts) (formats.ValidationResult, error) {
	status := formats.StatusOK
	if testSnapshot, ok := snapshot.(providerShimTestSnapshot); ok {
		status = testSnapshot.status
	}
	return formats.ValidationResult{Status: status, CheckedAt: time.Now().UTC()}, nil
}
func (providerShimTestFormat) Compare(_ string, _ formats.Snapshot, _ formats.Snapshot) int {
	return 0
}
func (providerShimTestFormat) Redact(_ formats.Snapshot) formats.Summary {
	return formats.Summary{}
}
func (providerShimTestFormat) Account(context.Context, formats.Snapshot, string) (string, error) {
	return "test-account", nil
}
func (providerShimTestFormat) AccountDisplay(context.Context, formats.Snapshot, string) (string, error) {
	return "test@example.test", nil
}

type providerShimTestUsageProbeFormat struct {
	providerShimTestFormat
}

func (providerShimTestUsageProbeFormat) Probe(context.Context, formats.Snapshot, string, *http.Client) (formats.UsageReport, error) {
	return formats.UsageReport{RemainingPct: 99}, nil
}

type streamingUsageProbeTestProvider struct {
	streamCalled bool
}

func (p *streamingUsageProbeTestProvider) Registration() (provider.Registration, error) {
	return provider.Registration{}, nil
}

func (p *streamingUsageProbeTestProvider) Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error) {
	return compat.Response{
		ID:      "buffered-test",
		Dialect: compat.APIDialectOpenAI,
		Model:   "stream-model",
		Message: compat.Message{Role: compat.MessageRoleAssistant, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "buffered"}}},
	}, nil
}

func (p *streamingUsageProbeTestProvider) InvokeStream(_ context.Context, _ provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	p.streamCalled = true
	events := []compat.Event{
		{ResponseID: "stream-test", Dialect: request.Dialect, Model: request.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}},
		{ResponseID: "stream-test", Dialect: request.Dialect, Model: request.Model, Type: compat.EventContentDelta, ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: "hello "}},
		{ResponseID: "stream-test", Dialect: request.Dialect, Model: request.Model, Type: compat.EventContentDelta, ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: "stream"}},
		{ResponseID: "stream-test", Dialect: request.Dialect, Model: request.Model, Type: compat.EventDone, DoneReason: "stop"},
	}
	response := compat.Response{ID: "stream-test", Dialect: request.Dialect, Model: request.Model, Message: compat.Message{Role: compat.MessageRoleAssistant}}
	for _, event := range events {
		if emit != nil {
			if err := emit(event); err != nil {
				return compat.Response{}, err
			}
		}
		if err := compat.ApplyEventToResponse(&response, event); err != nil {
			return compat.Response{}, err
		}
	}
	return response, nil
}

func (p *streamingUsageProbeTestProvider) Usage() (provider.UsageReport, error) {
	return provider.UsageReport{}, nil
}

func (p *streamingUsageProbeTestProvider) Models(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "stream-model"}}, nil
}

type providerShimTestSnapshot struct {
	raw       []byte
	expiresAt time.Time
	status    formats.ValidationStatus
}

func (s providerShimTestSnapshot) Identity() string     { return "provider-shim-test-snapshot" }
func (s providerShimTestSnapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s providerShimTestSnapshot) Raw() []byte          { return append([]byte(nil), s.raw...) }
func (s providerShimTestSnapshot) Fingerprint() string  { return "provider-shim-test-fingerprint" }
