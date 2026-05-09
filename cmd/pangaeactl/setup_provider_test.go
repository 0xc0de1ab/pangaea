package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
	if plan.Spec.ID != "gemini-cli" || plan.Spec.InstanceID != "gemini-operator-example.test" {
		t.Fatalf("unexpected provider ids: %#v", plan.Spec)
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
		Type:     "kind",
		Service:  "gemini",
		AuthPath: authPath,
		OutDir:   filepath.Join(dir, "out"),
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
	if strings.Contains(manifest, "value: $(PANGAEA_HOST_HOSTNAME)") {
		t.Fatalf("manifest should not use k8s node name as provider host by default:\n%s", manifest)
	}
	if plan.Spec.Service != provider.ServiceGemini {
		t.Fatalf("service = %q", plan.Spec.Service)
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
	if !validSetupNodeID(settings.NodeID) {
		t.Fatalf("node id = %q, want six lower-case base36 chars", settings.NodeID)
	}
	if settings.Mode != "http-direct" || settings.Type != "kind" {
		t.Fatalf("runtime settings mode/type = %q/%q, want http-direct/kind", settings.Mode, settings.Type)
	}
	firstManifest, err := os.ReadFile(filepath.Join(firstOut, "provider.k8s.yaml"))
	if err != nil {
		t.Fatalf("read first manifest: %v", err)
	}
	if !strings.Contains(string(firstManifest), "value: "+settings.NodeID) {
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
	if !strings.Contains(string(secondManifest), "value: "+settings.NodeID) {
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

func TestRootCommandIncludesSetupProvider(t *testing.T) {
	root := newRootCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "setup-provider" {
			if cmd.Flags().Lookup("mode") == nil {
				t.Fatal("setup-provider command missing --mode flag")
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

func hasSetupCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
