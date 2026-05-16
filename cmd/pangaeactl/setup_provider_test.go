package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestBuildSetupProviderDockerPlanUsesHostIdentityAndGeminiSettings(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "oauth_creds.json")
	if err := os.WriteFile(authPath, geminiSetupAuthJSON(t, "operator-sub", "operator@example.test"), 0o600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"mcpServers":{"pangaea-fixture":{"command":"node","args":["server.mjs"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:          "docker",
		Service:       "gemini",
		AuthPath:      authPath,
		SettingsPath:  settingsPath,
		OutDir:        filepath.Join(dir, "out"),
		HostName:      "snowbox",
		NodeID:        "node-snowbox",
		RouterControl: "ws://router/router/v1/control/ws",
	})
	if err != nil {
		t.Fatalf("build setup provider plan: %v", err)
	}
	if plan.Spec.ProviderType != "gemini-cli" || plan.Spec.InstanceID != "gemini-operator-example.test" {
		t.Fatalf("unexpected provider types: %#v", plan.Spec)
	}
	if len(plan.Spec.Models) == 0 || plan.Spec.Models[0].ID != "auto-gemini-3" {
		t.Fatalf("gemini default model should point at Gemini auto: %#v", plan.Spec.Models)
	}
	if plan.Spec.Models[0].Kind != "" || len(plan.Spec.Models[0].GroupMembers) != 0 {
		t.Fatalf("setup should leave model metadata classification to the provider shim: %#v", plan.Spec.Models[0])
	}
	if got := strings.Join(plan.Spec.Models[0].Aliases, ","); !strings.HasPrefix(got, "gemini-default,") {
		t.Fatalf("gemini default alias should point at auto group: %#v", plan.Spec.Models[0].Aliases)
	}
	if plan.Spec.HostName != "snowbox" || plan.Config.Node.HostName != "snowbox" {
		t.Fatalf("host names not preserved: spec=%q config=%q", plan.Spec.HostName, plan.Config.Node.HostName)
	}
	if plan.Spec.Env["PANGAEA_MCP_SERVERS_JSON"] == "" || !strings.Contains(plan.Spec.Env["PANGAEA_MCP_SERVERS_JSON"], "pangaea-fixture") {
		t.Fatalf("gemini settings mcp env missing: %#v", plan.Spec.Env)
	}
	if len(plan.Artifacts) != 1 || !strings.HasSuffix(plan.Artifacts[0].Path, "node-agent.yaml") {
		t.Fatalf("unexpected artifacts: %#v", plan.Artifacts)
	}
	if !strings.Contains(string(plan.Artifacts[0].Content), "host_name: snowbox") {
		t.Fatalf("node-agent yaml does not include physical host:\n%s", plan.Artifacts[0].Content)
	}
}

func TestBuildSetupProviderKindManifestUsesLocalHostNameAndDownwardContainerIdentity(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "oauth_creds.json")
	if err := os.WriteFile(authPath, geminiSetupAuthJSON(t, "operator-sub", "operator@example.test"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:       "kind",
		Service:    "gemini",
		AuthPath:   authPath,
		OutDir:     filepath.Join(dir, "out"),
		RouterData: "ws://router/router/v1/data/ws",
	})
	if err != nil {
		t.Fatalf("build setup provider plan: %v", err)
	}
	if plan.Type != "kind" || plan.Config.Runtime.Kind != "kubernetes" {
		t.Fatalf("unexpected type/runtime: %#v", plan)
	}
	if len(plan.Artifacts) != 1 {
		t.Fatalf("unexpected artifacts: %#v", plan.Artifacts)
	}
	manifest := string(plan.Artifacts[0].Content)
	for _, want := range []string{"namespace: pangaea-e2e", "fieldPath: spec.nodeName", "fieldPath: metadata.uid", "PANGAEA_CONTAINER_KIND", "kubernetes"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
	if plan.HostName == "" || !strings.Contains(manifest, "value: "+plan.HostName) {
		t.Fatalf("manifest should report setup host name %q:\n%s", plan.HostName, manifest)
	}
	if !strings.Contains(manifest, "value: auto-gemini-3") {
		t.Fatalf("manifest should use Gemini auto group as default model:\n%s", manifest)
	}
	if strings.Contains(manifest, "PANGAEA_MODELS") {
		t.Fatalf("Gemini direct-http manifest should discover models from Code Assist quota buckets instead of static PANGAEA_MODELS:\n%s", manifest)
	}
	if strings.Contains(manifest, "value: $(PANGAEA_HOST_HOSTNAME)") {
		t.Fatalf("manifest should not use k8s node name as provider host by default:\n%s", manifest)
	}
	if plan.Spec.Service != provider.ServiceGemini {
		t.Fatalf("service = %q", plan.Spec.Service)
	}
	if !strings.Contains(manifest, "provider_instance_id=gemini-operator-example.test") {
		t.Fatalf("manifest should add provider instance id to data ws url:\n%s", manifest)
	}
}

func TestSetupProviderModeSelectsProviderMode(t *testing.T) {
	dir := t.TempDir()
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "docker",
		Mode:    "cli-adapter",
		Service: "gemini",
		OutDir:  filepath.Join(dir, "out"),
	})
	if err != nil {
		t.Fatalf("build setup provider plan with --mode: %v", err)
	}
	if plan.Type != "docker" || plan.Config.Runtime.Kind != "docker" || plan.Mode != "cli-adapter" {
		t.Fatalf("unexpected setup type/runtime/mode: type=%q runtime=%q mode=%q", plan.Type, plan.Config.Runtime.Kind, plan.Mode)
	}
	if plan.Spec.Kind != provider.KindCLIContainer || plan.Spec.ProviderMode != "cli-adapter" || plan.Spec.Upstream.BaseURL != "" {
		t.Fatalf("unexpected cli-adapter spec: kind=%q provider_mode=%q upstream=%#v", plan.Spec.Kind, plan.Spec.ProviderMode, plan.Spec.Upstream)
	}

	codex, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "kind",
		Mode:    "app-server",
		Service: "codex",
		OutDir:  filepath.Join(dir, "codex"),
	})
	if err != nil {
		t.Fatalf("build codex app-server setup provider plan: %v", err)
	}
	if codex.Spec.Kind != provider.KindAppServer || codex.Spec.ProviderMode != "app-server" || !strings.HasPrefix(codex.Spec.Upstream.BaseURL, "ws://") {
		t.Fatalf("unexpected codex app-server spec: kind=%q provider_mode=%q upstream=%#v", codex.Spec.Kind, codex.Spec.ProviderMode, codex.Spec.Upstream)
	}

	codexDirect, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "kind",
		Mode:    "http-direct",
		Service: "codex",
		OutDir:  filepath.Join(dir, "codex-http-direct"),
	})
	if err != nil {
		t.Fatalf("build codex http-direct setup provider plan: %v", err)
	}
	if codexDirect.Spec.Kind != provider.KindCLIContainer || codexDirect.Spec.ProviderMode != "http-direct" || codexDirect.Spec.Upstream.BaseURL != "" || len(codexDirect.Spec.Shim.Command) != 0 {
		t.Fatalf("unexpected codex http-direct spec: kind=%q provider_mode=%q upstream=%#v command=%v", codexDirect.Spec.Kind, codexDirect.Spec.ProviderMode, codexDirect.Spec.Upstream, codexDirect.Spec.Shim.Command)
	}

	antigravity, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "kind",
		Mode:    "ls-core-sidecar",
		Service: "antigravity",
		OutDir:  filepath.Join(dir, "antigravity"),
	})
	if err != nil {
		t.Fatalf("build antigravity ls-core-sidecar setup provider plan: %v", err)
	}
	if antigravity.Spec.Kind != provider.KindSidecar || antigravity.Spec.ProviderMode != "ls-core-sidecar" || antigravity.Spec.Upstream.BaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected antigravity spec: kind=%q provider_mode=%q upstream=%#v", antigravity.Spec.Kind, antigravity.Spec.ProviderMode, antigravity.Spec.Upstream)
	}

	copilotSDK, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "docker",
		Mode:    "sdk",
		Service: "github-copilot",
		OutDir:  filepath.Join(dir, "copilot-sdk"),
	})
	if err != nil {
		t.Fatalf("build copilot sdk setup provider plan: %v", err)
	}
	if copilotSDK.Spec.Kind != provider.KindSidecar || copilotSDK.Spec.ProviderMode != "sdk" || copilotSDK.Spec.Upstream.BaseURL != "http://127.0.0.1:4141" {
		t.Fatalf("unexpected copilot sdk spec: kind=%q provider_mode=%q upstream=%#v", copilotSDK.Spec.Kind, copilotSDK.Spec.ProviderMode, copilotSDK.Spec.Upstream)
	}
	if got := strings.Join(copilotSDK.Spec.Shim.Command, " "); got != "/usr/local/bin/copilot-relay --listen 127.0.0.1:4141" {
		t.Fatalf("unexpected copilot sdk command: %q", got)
	}
	if len(copilotSDK.Spec.Models) != 0 {
		t.Fatalf("copilot sdk should discover models dynamically, got static models: %#v", copilotSDK.Spec.Models)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE} {
		if !hasSetupCapability(copilotSDK.Spec.Shim.Capabilities, capability) {
			t.Fatalf("copilot sdk capabilities %v missing %s", copilotSDK.Spec.Shim.Capabilities, capability)
		}
	}

	copilotACP, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "docker",
		Mode:    "acp",
		Service: "github-copilot",
		OutDir:  filepath.Join(dir, "copilot-acp"),
	})
	if err != nil {
		t.Fatalf("build copilot acp setup provider plan: %v", err)
	}
	if copilotACP.Spec.Kind != provider.KindCLIContainer || copilotACP.Spec.ProviderMode != "acp" || copilotACP.Spec.Upstream.BaseURL != "" || len(copilotACP.Spec.Shim.Command) != 0 {
		t.Fatalf("unexpected copilot acp spec: kind=%q provider_mode=%q upstream=%#v command=%v", copilotACP.Spec.Kind, copilotACP.Spec.ProviderMode, copilotACP.Spec.Upstream, copilotACP.Spec.Shim.Command)
	}
	if hasSetupCapability(copilotACP.Spec.Shim.Capabilities, provider.CapabilityStreamSSE) {
		t.Fatalf("copilot acp should not advertise streaming until implemented: %v", copilotACP.Spec.Shim.Capabilities)
	}
	for _, capability := range []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent} {
		if !hasSetupCapability(copilotACP.Spec.Shim.Capabilities, capability) {
			t.Fatalf("copilot acp capabilities %v missing %s", copilotACP.Spec.Shim.Capabilities, capability)
		}
	}
}

func TestBuildSetupProviderAntigravityKindManifestUsesRuntimeAndShimContainers(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "state.vscdb")
	if err := os.WriteFile(authPath, []byte("not-real-but-non-empty"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:     "kind",
		Service:  "antigravity",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
	})
	if err != nil {
		t.Fatalf("build setup provider plan: %v", err)
	}
	if plan.Spec.ProviderType != "antigravity-sidecar" || plan.Spec.ProviderMode != "ls-core-sidecar" {
		t.Fatalf("unexpected antigravity identity: %#v", plan.Spec)
	}
	manifest := string(plan.Artifacts[0].Content)
	for _, want := range []string{
		"bootstrap-antigravity",
		"name: runtime",
		"image: pangaea/antigravity-runtime:kind",
		"name: shim",
		"image: pangaea/provider-antigravity-sidecar:kind",
		"PANGAEA_SHIM_MODE",
		"sidecar-agent",
		"PANGAEA_AUTH_FORMAT",
		"antigravity-state-vscdb-format",
		"state.vscdb",
		"runAsNonRoot: true",
		"runAsUser: 1000",
		"runAsGroup: 1000",
		"fsGroup: 1000",
		"allowPrivilegeEscalation: false",
		"readOnlyRootFilesystem: true",
		"defaultMode: 288",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("antigravity manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "runAsUser: 0") || strings.Contains(manifest, "/root/.antigravity-server") {
		t.Fatalf("antigravity manifest must be rootless and avoid /root paths:\n%s", manifest)
	}
}

func TestBuildSetupProviderAntigravityDockerPlanMountsRuntimeState(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "state.vscdb")
	if err := os.WriteFile(authPath, []byte("sqlite fixture placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:               "docker",
		Mode:               "ls-core-sidecar",
		Service:            "antigravity",
		InstanceID:         "antigravity-sidecar",
		AuthPath:           authPath,
		OutDir:             filepath.Join(dir, "out"),
		NetworkMode:        "antigravity-cluster",
		UpstreamBaseURL:    "http://antigravity-cli-proxy-1-1:8080",
		UpstreamAPIKey:     "test-upstream-key",
		UpstreamAPIKeyMode: "bearer",
	})
	if err != nil {
		t.Fatalf("build antigravity docker plan: %v", err)
	}
	if !slices.Contains(plan.Spec.Storage.ContainerPaths, "/var/lib/antigravity") {
		t.Fatalf("antigravity docker storage should include runtime state path: %#v", plan.Spec.Storage.ContainerPaths)
	}
	if plan.Spec.Auth.Sync.ContainerToHost {
		t.Fatalf("antigravity Windows/Linux state DB should not default to container-to-host sync: %#v", plan.Spec.Auth.Sync)
	}
	if plan.Spec.Auth.Sync.HostToContainer != "reconcile" {
		t.Fatalf("antigravity should still reconcile host auth into container: %#v", plan.Spec.Auth.Sync)
	}
	if plan.Spec.NetworkMode != "antigravity-cluster" || plan.Spec.Upstream.BaseURL != "http://antigravity-cli-proxy-1-1:8080" {
		t.Fatalf("antigravity docker plan should preserve network/upstream override: network=%q upstream=%#v", plan.Spec.NetworkMode, plan.Spec.Upstream)
	}
	if plan.Spec.Upstream.APIKey != "test-upstream-key" || plan.Spec.Upstream.APIKeyMode != "bearer" {
		t.Fatalf("antigravity docker plan should preserve upstream auth config: %#v", plan.Spec.Upstream)
	}
	if !slices.Contains(plan.Spec.Shim.Capabilities, provider.CapabilityAuthRefreshProtocol) {
		t.Fatalf("antigravity docker plan should advertise protocol refresh: %#v", plan.Spec.Shim.Capabilities)
	}
}

func TestBuildSetupProviderNativeSystemdUsesHostAuthPath(t *testing.T) {
	dir := t.TempDir()
	authDir := filepath.Join(dir, ".gemini")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(authDir, "oauth_creds.json")
	if err := os.WriteFile(authPath, geminiSetupAuthJSON(t, "operator-sub", "operator@example.test"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:     "native-systemd",
		Service:  "gemini",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
		HostName: "snowbox",
	})
	if err != nil {
		t.Fatalf("build setup provider plan: %v", err)
	}
	if plan.Spec.Auth.ContainerPath != authPath {
		t.Fatalf("native auth path = %q, want host path %q", plan.Spec.Auth.ContainerPath, authPath)
	}
	if len(plan.Artifacts) != 2 {
		t.Fatalf("unexpected artifacts: %#v", plan.Artifacts)
	}
	env := string(plan.Artifacts[0].Content)
	if !strings.Contains(env, `PANGAEA_AUTH_PATH="`+authPath+`"`) {
		t.Fatalf("native env missing host auth path:\n%s", env)
	}
	if strings.Contains(env, "PANGAEA_CONTAINER_KIND") {
		t.Fatalf("native env should not set container metadata:\n%s", env)
	}
}

func TestRunSetupProviderDryRunRedactsKubernetesSecretData(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	authPath := filepath.Join(dir, "oauth_creds.json")
	if err := os.WriteFile(authPath, geminiSetupAuthJSON(t, "operator-sub", "operator@example.test"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runSetupProvider(t.Context(), setupProviderOptions{
		Type:     "kind",
		Service:  "gemini",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
		DryRun:   true,
	}, &out)
	if err != nil {
		t.Fatalf("run setup provider: %v", err)
	}
	if strings.Contains(out.String(), "secret-token") || !strings.Contains(out.String(), "oauth_creds.json: <redacted>") {
		t.Fatalf("dry-run output did not redact secret data:\n%s", out.String())
	}
}

func TestBuildSetupProviderWithoutAuthPathRegistersNoLogin(t *testing.T) {
	dir := t.TempDir()
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:    "kind",
		Service: "gemini",
		OutDir:  filepath.Join(dir, "out"),
	})
	if err != nil {
		t.Fatalf("build setup provider plan: %v", err)
	}
	if plan.Spec.AccountHint != "" {
		t.Fatalf("account should not be guessed without auth path: %q", plan.Spec.AccountHint)
	}
	if plan.Spec.Auth.Mode != "" || plan.Spec.Auth.HostPath != "" || plan.Spec.Auth.ContainerPath != "" {
		t.Fatalf("auth spec should be disabled without auth path: %#v", plan.Spec.Auth)
	}
	if hasSetupCapability(plan.Spec.Shim.Capabilities, provider.CapabilityAuthFile) || hasSetupCapability(plan.Spec.Shim.Capabilities, provider.CapabilityAuthRefreshOneshot) {
		t.Fatalf("no-login provider should not advertise auth file/refresh capabilities: %v", plan.Spec.Shim.Capabilities)
	}
	manifest := string(plan.Artifacts[0].Content)
	if strings.Contains(manifest, "kind: Secret") || strings.Contains(manifest, "provider-auth") || strings.Contains(manifest, "PANGAEA_AUTH_PATH") {
		t.Fatalf("no-login manifest should not copy auth:\n%s", manifest)
	}
}

func TestRunSetupProviderPersistsGeneratedNodeID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	firstOut := filepath.Join(dir, "first")
	var first bytes.Buffer
	if err := runSetupProvider(t.Context(), setupProviderOptions{
		Type:    "kind",
		Service: "gemini",
		OutDir:  firstOut,
	}, &first); err != nil {
		t.Fatalf("first run setup provider: %v", err)
	}
	settingsPath := filepath.Join(dir, "config", "pangaea", "setup-provider", "kind", "gemini", "gemini-cli", "runtime.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read runtime settings: %v", err)
	}
	var settings setupProviderRuntimeSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("decode runtime settings: %v", err)
	}
	if !setupNodeIDIsSixDigits(settings.NodeID) {
		t.Fatalf("node id = %q, want six decimal digits", settings.NodeID)
	}
	if settings.Mode != "http-direct" || settings.Type != "kind" {
		t.Fatalf("runtime settings mode/type = %q/%q, want http-direct/kind", settings.Mode, settings.Type)
	}
	firstManifest, err := os.ReadFile(filepath.Join(firstOut, "provider.k8s.yaml"))
	if err != nil {
		t.Fatalf("read first manifest: %v", err)
	}
	if !setupManifestContainsNodeID(firstManifest, settings.NodeID) {
		t.Fatalf("first manifest missing persisted node id %q:\n%s", settings.NodeID, firstManifest)
	}

	secondOut := filepath.Join(dir, "second")
	var second bytes.Buffer
	if err := runSetupProvider(t.Context(), setupProviderOptions{
		Type:    "kind",
		Service: "gemini",
		OutDir:  secondOut,
	}, &second); err != nil {
		t.Fatalf("second run setup provider: %v", err)
	}
	secondManifest, err := os.ReadFile(filepath.Join(secondOut, "provider.k8s.yaml"))
	if err != nil {
		t.Fatalf("read second manifest: %v", err)
	}
	if !setupManifestContainsNodeID(secondManifest, settings.NodeID) {
		t.Fatalf("second manifest did not reuse node id %q:\n%s", settings.NodeID, secondManifest)
	}
}

func TestBuildSetupProviderDerivesAccountOnlyFromAuthPath(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "oauth_creds.json")
	if err := os.WriteFile(authPath, geminiSetupAuthJSON(t, "operator-sub", "operator@example.test"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:     "docker",
		Service:  "gemini",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
	})
	if err != nil {
		t.Fatalf("build setup provider plan: %v", err)
	}
	if plan.Spec.AccountHint != "operator@example.test" {
		t.Fatalf("account hint = %q", plan.Spec.AccountHint)
	}
	if plan.Spec.InstanceID != "gemini-operator-example.test" {
		t.Fatalf("instance id = %q", plan.Spec.InstanceID)
	}
}

func TestBuildSetupProviderCopilotDerivesAccountAndBootstrapsConfigJSON(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(authPath, copilotSetupConfigJSON("octocat"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:     "kind",
		Service:  "github-copilot",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
	})
	if err != nil {
		t.Fatalf("build copilot setup provider plan: %v", err)
	}
	if plan.Spec.AccountHint != "octocat" {
		t.Fatalf("account hint = %q, want octocat", plan.Spec.AccountHint)
	}
	if plan.Spec.InstanceID != "github-copilot-octocat" {
		t.Fatalf("instance id = %q, want github-copilot-octocat", plan.Spec.InstanceID)
	}
	if plan.Spec.Auth.Format != "github-copilot-config-json-format" || !strings.HasSuffix(plan.Spec.Auth.ContainerPath, "/.copilot/config.json") {
		t.Fatalf("unexpected copilot auth spec: %#v", plan.Spec.Auth)
	}
	if plan.Spec.Auth.Sync.ContainerToHost {
		t.Fatalf("copilot config auth must not sync container-to-host by default: %#v", plan.Spec.Auth.Sync)
	}
	if plan.Spec.Auth.Sync.HostToContainer != "reconcile" {
		t.Fatalf("copilot config auth should sync host-to-container on reconcile: %#v", plan.Spec.Auth.Sync)
	}
	manifest := string(plan.Artifacts[0].Content)
	for _, want := range []string{
		"bootstrap-github-copilot",
		"config.json",
		"github-copilot-config-json-format",
		"/var/lib/pangaea/home/copilot/.copilot/config.json",
		"PANGAEA_ACCOUNT_DISPLAY",
		"value: octocat",
		"runAsNonRoot: true",
		"runAsUser: 1000",
		"runAsGroup: 1000",
		"fsGroup: 1000",
		"allowPrivilegeEscalation: false",
		"readOnlyRootFilesystem: true",
		"defaultMode: 288",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("copilot manifest missing %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "runAsUser: 0") {
		t.Fatalf("copilot manifest must not run any container as root:\n%s", manifest)
	}
	if strings.Contains(manifest, "oauth_creds.json") {
		t.Fatalf("copilot manifest should not use Gemini oauth bootstrap:\n%s", manifest)
	}
	if strings.Contains(manifest, "PANGAEA_MODEL") || strings.Contains(manifest, "PANGAEA_MODELS") {
		t.Fatalf("copilot sdk manifest should discover models dynamically instead of injecting static model env:\n%s", manifest)
	}
}

func TestBuildSetupProviderCursorDerivesAccountAndBootstrapsAuthJSON(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "cursor-agent")
	if err := os.WriteFile(bin, []byte(cursorSetupAgentScript("dev@example.test", "Pro")), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PANGAEA_CURSOR_AGENT_EXE", bin)
	authPath := filepath.Join(dir, ".config", "cursor", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(cursorSetupAuthJSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSetupProviderPlan(setupProviderOptions{
		Type:     "kind",
		Service:  "cursor",
		Mode:     "acp",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
	})
	if err != nil {
		t.Fatalf("build cursor setup provider plan: %v", err)
	}
	if plan.Spec.AccountHint != "dev@example.test" {
		t.Fatalf("account hint = %q, want dev@example.test", plan.Spec.AccountHint)
	}
	if plan.Spec.InstanceID != "cursor-dev-example.test" {
		t.Fatalf("instance id = %q, want cursor-dev-example.test", plan.Spec.InstanceID)
	}
	if plan.Spec.Auth.Format != "cursor-auth-json-format" || !strings.HasSuffix(plan.Spec.Auth.ContainerPath, "/.config/cursor/auth.json") {
		t.Fatalf("unexpected cursor auth spec: %#v", plan.Spec.Auth)
	}
	manifest := string(plan.Artifacts[0].Content)
	for _, want := range []string{
		"bootstrap-cursor",
		"auth.json",
		"cursor-auth-json-format",
		"/var/lib/pangaea/home/cursor/.config/cursor/auth.json",
		"PANGAEA_ACCOUNT_DISPLAY",
		"value: dev@example.test",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("cursor manifest missing %q:\n%s", want, manifest)
		}
	}
	for _, forbidden := range []string{
		"PANGAEA_MODEL",
		"PANGAEA_MODELS",
		"composer-2",
	} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("cursor manifest should not contain default model %q:\n%s", forbidden, manifest)
		}
	}
}

func TestRootCommandIncludesSetupProvider(t *testing.T) {
	root := newRootCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "setup-provider" {
			if cmd.Flags().Lookup("mode") == nil {
				t.Fatal("setup-provider command missing --mode flag")
			}
			for _, name := range []string{"network-mode", "upstream-base-url", "upstream-api-key", "upstream-api-key-file", "upstream-api-key-mode"} {
				if cmd.Flags().Lookup(name) == nil {
					t.Fatalf("setup-provider command missing --%s flag", name)
				}
			}
			return
		}
	}
	t.Fatal("setup-provider command not registered")
}

func geminiSetupAuthJSON(t *testing.T, sub string, email string) []byte {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadRaw, err := json.Marshal(map[string]string{"sub": sub, "email": email})
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadRaw)
	return []byte(`{"access_token":"secret-token","refresh_token":"refresh","expiry_date":1999999999999,"id_token":"` + header + "." + payload + `.sig"}`)
}

func copilotSetupAuthJSON(user string) []byte {
	return []byte(`{"github.com:Iv23ctfURkiMfJ4xr5mv":{"user":"` + user + `","oauth_token":"gho_test_secret","githubAppId":"Iv23ctfURkiMfJ4xr5mv"}}`)
}

func copilotSetupConfigJSON(user string) []byte {
	return []byte(`// User settings belong in settings.json.
// This file is managed automatically.
{
  "lastLoggedInUser": {"host": "https://github.com", "login": "` + user + `"},
  "loggedInUsers": [{"host": "https://github.com", "login": "` + user + `"}],
  "copilotTokens": {
    "https://github.com:` + user + `": "copilot_test_secret"
  }
}`)
}

func cursorSetupAuthJSON() string {
	return `{"accessToken":"cursor-access-test","refreshToken":"cursor-refresh-test"}`
}

func cursorSetupAgentScript(email string, tier string) string {
	return `#!/bin/sh
if [ "$1" = "status" ]; then
  printf '%s\n' '{"status":"authenticated","isAuthenticated":true,"userInfo":{"email":"` + email + `","userId":350474099}}'
  exit 0
fi
if [ "$1" = "about" ]; then
  printf '%s\n' '{"cliVersion":"2026.05.09-test","model":"Composer 2 Fast","subscriptionTier":"` + tier + `","userEmail":"` + email + `"}'
  exit 0
fi
exit 2
`
}

func hasSetupCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func setupNodeIDIsSixDigits(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func setupManifestContainsNodeID(manifest []byte, nodeID string) bool {
	text := string(manifest)
	return strings.Contains(text, "value: "+nodeID) || strings.Contains(text, `value: "`+nodeID+`"`)
}
