package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
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
	t.Setenv("PANGAEA_PROVIDER_ID", "codex-samtest")
	t.Setenv("PANGAEA_PROVIDER_INSTANCE_ID", "codex-samtest-a1")
	t.Setenv("PANGAEA_NODE_ID", "node-a1")
	t.Setenv("PANGAEA_HOST_NAME", "snowbox")
	t.Setenv("PANGAEA_SERVICE", "codex")
	t.Setenv("PANGAEA_ACCOUNT_DISPLAY", "codex@example.test")
	t.Setenv("PANGAEA_UPSTREAM_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("PANGAEA_UPSTREAM_DIALECT", "openai")
	t.Setenv("PANGAEA_MODEL", "gpt-5-codex")
	t.Setenv("PANGAEA_UPSTREAM_API_KEY_MODE", "header")
	t.Setenv("PANGAEA_UPSTREAM_API_KEY_HEADER", "x-api-key")
	t.Setenv("PANGAEA_UPSTREAM_API_KEY_QUERY_PARAM", "key")
	t.Setenv("PANGAEA_MODEL_ALIAS", "codex-default")
	t.Setenv("PANGAEA_AUTH_PATH", "/var/lib/pangaea/auth/codex/auth.json")
	t.Setenv("PANGAEA_AUTH_FORMAT", "codex-auth-json-format")
	t.Setenv("PANGAEA_REFRESH_COMMAND", "codex exec ping")
	t.Setenv("PANGAEA_REFRESH_LOGIN_SHELL", "false")
	t.Setenv("PANGAEA_REFRESH_TIMEOUT", "45s")
	t.Setenv("PANGAEA_REFRESH_THRESHOLD", "5m")
	t.Setenv("PANGAEA_REFRESH_COOLDOWN", "90s")

	opts := applyProviderShimEnvDefaults(providerShimRunOptions{RefreshLoginShell: true})
	if !opts.CLIContainer || opts.RouterControlURL != "ws://router/control" || opts.ProviderID != "codex-samtest" {
		t.Fatalf("env defaults did not populate identity/mode: %#v", opts)
	}
	if opts.Account != "codex@example.test" || opts.UpstreamBaseURL != "http://127.0.0.1:8080" || opts.Model != "gpt-5-codex" {
		t.Fatalf("env defaults did not populate provider config: %#v", opts)
	}
	if opts.UpstreamAPIKeyMode != "header" || opts.UpstreamAPIKeyHeader != "x-api-key" || opts.UpstreamAPIKeyQueryParam != "key" {
		t.Fatalf("env defaults did not populate upstream api key auth config: %#v", opts)
	}
	if opts.AuthPath != "/var/lib/pangaea/auth/codex/auth.json" || opts.AuthFormat != "codex-auth-json-format" || opts.RefreshCommand != "codex exec ping" {
		t.Fatalf("env defaults did not populate auth config: %#v", opts)
	}
	if opts.RefreshLoginShell || opts.RefreshTimeout != 45*time.Second || opts.RefreshThreshold != 5*time.Minute || opts.RefreshCooldown != 90*time.Second {
		t.Fatalf("env defaults did not populate refresh options: %#v", opts)
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
	if cmd.Flags().Lookup("stream-token-key") == nil {
		t.Fatalf("expected stream-token-key flag")
	}
	for _, name := range []string{"api-compatible", "cli-container", "provider-id", "provider-instance-id", "node-id", "host-name", "service", "account", "upstream-base-url", "upstream-dialect", "upstream-api-key", "upstream-api-key-file", "upstream-api-key-mode", "upstream-api-key-header", "upstream-api-key-query-param", "model", "model-alias", "auth-path", "auth-format", "refresh-command", "refresh-login-shell", "refresh-timeout", "refresh-threshold", "refresh-cooldown"} {
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
		ProviderID:         "codex-samtest",
		ProviderInstanceID: "codex-samtest-a1",
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

type providerShimTestSnapshot struct {
	raw       []byte
	expiresAt time.Time
	status    formats.ValidationStatus
}

func (s providerShimTestSnapshot) Identity() string     { return "provider-shim-test-snapshot" }
func (s providerShimTestSnapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s providerShimTestSnapshot) Raw() []byte          { return append([]byte(nil), s.raw...) }
func (s providerShimTestSnapshot) Fingerprint() string  { return "provider-shim-test-fingerprint" }
