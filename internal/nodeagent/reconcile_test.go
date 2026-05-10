package nodeagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/runtime"
)

type fakeContainerRuntime struct {
	pulled     runtime.ImageRef
	created    runtime.ContainerSpec
	copied     *runtime.CopySpec
	copiedFrom *runtime.CopySpec
	started    runtime.ContainerID
	stopped    runtime.ContainerID
	removed    runtime.ContainerID
	stats      runtime.Stats
	calls      []string
}

func (r *fakeContainerRuntime) Info(context.Context) (runtime.RuntimeInfo, error) {
	return runtime.RuntimeInfo{Kind: "fake"}, nil
}

func (r *fakeContainerRuntime) Pull(_ context.Context, image runtime.ImageRef) error {
	r.calls = append(r.calls, "pull")
	r.pulled = image
	return nil
}

func (r *fakeContainerRuntime) Create(_ context.Context, spec runtime.ContainerSpec) (runtime.ContainerID, error) {
	r.calls = append(r.calls, "create")
	r.created = spec
	return "container-1", nil
}

func (r *fakeContainerRuntime) Start(_ context.Context, id runtime.ContainerID) error {
	r.calls = append(r.calls, "start")
	r.started = id
	return nil
}

func (r *fakeContainerRuntime) Stop(_ context.Context, id runtime.ContainerID, _ time.Duration) error {
	r.calls = append(r.calls, "stop")
	r.stopped = id
	return nil
}

func (r *fakeContainerRuntime) Exec(context.Context, runtime.ContainerID, runtime.ExecSpec) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}

func (r *fakeContainerRuntime) CopyTo(_ context.Context, _ runtime.ContainerID, spec runtime.CopySpec) error {
	r.calls = append(r.calls, "copy")
	r.copied = &spec
	return nil
}

func (r *fakeContainerRuntime) CopyFrom(_ context.Context, _ runtime.ContainerID, spec runtime.CopySpec) error {
	r.calls = append(r.calls, "copy-from")
	r.copiedFrom = &spec
	return nil
}

func (r *fakeContainerRuntime) Stats(context.Context, runtime.ContainerID) (runtime.Stats, error) {
	return r.stats, nil
}

func (r *fakeContainerRuntime) Logs(context.Context, runtime.ContainerID, runtime.LogSpec) (<-chan runtime.LogEvent, error) {
	ch := make(chan runtime.LogEvent)
	close(ch)
	return ch, nil
}

func (r *fakeContainerRuntime) Remove(_ context.Context, id runtime.ContainerID, _ runtime.RemoveOptions) error {
	r.calls = append(r.calls, "remove")
	r.removed = id
	return nil
}

type fakeExistingContainerRuntime struct {
	fakeContainerRuntime
	status runtime.ContainerStatus
	found  bool
}

func (r *fakeExistingContainerRuntime) FindByLabels(context.Context, map[string]string) (runtime.ContainerStatus, bool, error) {
	return r.status, r.found, nil
}

func TestContainerSpecFromProviderSpecIncludesIdentityAuthAndSecurity(t *testing.T) {
	uid := 10001
	gid := 10001
	spec, err := ContainerSpecFromProviderSpec(ProviderSpec{
		ProviderType: "codex-primary",
		InstanceID:   "codex-primary-a1",
		Kind:         provider.KindAppServer,
		Image:        "pangaea/provider-codex:test",
		AccountHint:  "primary@example.test",
		Service:      provider.ServiceCodex,
		Models: []provider.Model{{
			ID:           "gpt-5-codex",
			Aliases:      []string{"codex-default"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE},
		}},
		Auth: AuthSpec{
			Mode:          "file",
			Format:        "codex-auth-json-format",
			Bootstrap:     "copy",
			HostPath:      "/srv/pangaea/auth/codex/primary/auth.json",
			ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
			OwnerUID:      &uid,
			OwnerGID:      &gid,
			FileMode:      "0600",
		},
		Refresh: RefreshSpec{Command: []string{"codex", "exec", "Reply with OK only."}, Threshold: "5m", Cooldown: "90s", Timeout: "2m"},
		Env:     map[string]string{"PANGAEA_CUSTOM_ENV": "custom"},
		Shim: ShimSpec{
			Protocols:    []string{"openai", "anthropic", "gemini"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead},
			Entrypoint:   []string{"/usr/local/bin/provider-entrypoint"},
			Command:      []string{"codex", "app-server", "--listen", "ws://127.0.0.1:8080"},
			WorkingDir:   "/var/lib/pangaea/provider",
		},
		Resources: ResourceSpec{
			CPUs:      "2",
			Memory:    "2GiB",
			PidsLimit: 512,
		},
		Upstream: UpstreamSpec{
			BaseURL:          "http://127.0.0.1:8080",
			APIKeyFile:       "/run/secrets/codex-api-key",
			APIKeyMode:       "header",
			APIKeyHeader:     "x-api-key",
			APIKeyQueryParam: "key",
		},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("container spec from provider spec: %v", err)
	}
	if spec.ProviderInstanceID != "codex-primary-a1" || spec.HostName != "snowbox" || spec.Image != "pangaea/provider-codex:test" {
		t.Fatalf("unexpected container spec identity: %#v", spec)
	}
	if spec.Name != "pangaea-codex-primary-codex-primary-a1" {
		t.Fatalf("container name = %q", spec.Name)
	}
	if got, want := strings.Join(spec.Entrypoint, " "), "/usr/local/bin/provider-entrypoint"; got != want {
		t.Fatalf("entrypoint = %q, want %q", got, want)
	}
	if got, want := strings.Join(spec.Command, " "), "codex app-server --listen ws://127.0.0.1:8080"; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if spec.WorkingDir != "/var/lib/pangaea/provider" {
		t.Fatalf("working dir = %q", spec.WorkingDir)
	}
	if spec.Env["PANGAEA_AUTH_PATH"] != "/var/lib/pangaea/auth/codex/auth.json" {
		t.Fatalf("missing auth env: %#v", spec.Env)
	}
	for key, want := range map[string]string{
		"PANGAEA_SHIM_MODE":                    "app-server",
		"PANGAEA_ACCOUNT_DISPLAY":              "primary@example.test",
		"PANGAEA_UPSTREAM_BASE_URL":            "http://127.0.0.1:8080",
		"PANGAEA_UPSTREAM_API_KEY_FILE":        "/run/secrets/codex-api-key",
		"PANGAEA_UPSTREAM_API_KEY_MODE":        "header",
		"PANGAEA_UPSTREAM_API_KEY_HEADER":      "x-api-key",
		"PANGAEA_UPSTREAM_API_KEY_QUERY_PARAM": "key",
		"PANGAEA_UPSTREAM_DIALECT":             "openai",
		"PANGAEA_SHIM_PROTOCOLS":               "openai,anthropic,gemini",
		"PANGAEA_SHIM_CAPABILITIES":            "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,stream.sse,usage.read,models.read",
		"PANGAEA_MODEL":                        "gpt-5-codex",
		"PANGAEA_MODEL_ALIAS":                  "codex-default",
		"PANGAEA_MODEL_CAPABILITIES":           "api.openai.chat,api.anthropic.messages,api.gemini.generateContent,stream.sse",
		"PANGAEA_AUTH_FORMAT":                  "codex-auth-json-format",
		"PANGAEA_REFRESH_COMMAND":              "'codex' 'exec' 'Reply with OK only.'",
		"PANGAEA_REFRESH_THRESHOLD":            "5m",
		"PANGAEA_REFRESH_COOLDOWN":             "90s",
		"PANGAEA_REFRESH_TIMEOUT":              "2m",
		"PANGAEA_PROVIDER_INSTANCE_ID":         "codex-primary-a1",
		"PANGAEA_CONTAINER_NAME":               "pangaea-codex-primary-codex-primary-a1",
		"PANGAEA_CUSTOM_ENV":                   "custom",
	} {
		if spec.Env[key] != want {
			t.Fatalf("env[%s] = %q, want %q in %#v", key, spec.Env[key], want, spec.Env)
		}
	}
	if spec.AuthCopy == nil || spec.AuthCopy.OwnerUID != 10001 || spec.AuthCopy.FileMode != 0o600 {
		t.Fatalf("unexpected auth copy: %#v", spec.AuthCopy)
	}
	if !spec.Security.RunAsNonRoot || !spec.Security.ReadOnlyRootFS {
		t.Fatalf("expected hardened defaults: %#v", spec.Security)
	}
	if spec.Resources.CPUs != "2" || spec.Resources.Memory != "2GiB" || spec.Resources.PIDsLimit != 512 {
		t.Fatalf("unexpected resource limits: %#v", spec.Resources)
	}
}

func TestContainerSpecFromProviderSpecWithOptionsIncludesRouterURLs(t *testing.T) {
	spec, err := ContainerSpecFromProviderSpecWithOptions(ProviderSpec{
		ProviderType: "codex-primary",
		InstanceID:   "codex-primary-a1",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-codex:test",
		Service:      provider.ServiceCodex,
		Shim:         ShimSpec{Protocols: []string{"openai"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox", ContainerSpecOptions{
		RouterControlURL: "ws://router/router/v1/control/ws",
		RouterDataURL:    "ws://router/router/v1/data/ws",
		StreamTokenKey:   "test-token-key",
		ContainerKind:    "docker",
	})
	if err != nil {
		t.Fatalf("container spec from provider spec with options: %v", err)
	}
	for key, want := range map[string]string{
		"PANGAEA_ROUTER_CONTROL_URL": "ws://router/router/v1/control/ws",
		"PANGAEA_ROUTER_DATA_URL":    "ws://router/router/v1/data/ws?provider_instance_id=codex-primary-a1",
		"PANGAEA_STREAM_TOKEN_KEY":   "test-token-key",
		"PANGAEA_CONTAINER_KIND":     "docker",
	} {
		if spec.Env[key] != want {
			t.Fatalf("env[%s] = %q, want %q in %#v", key, spec.Env[key], want, spec.Env)
		}
	}
}

func TestContainerSpecFromProviderSpecCopiesAPIKeyAuth(t *testing.T) {
	spec, err := ContainerSpecFromProviderSpec(ProviderSpec{
		ProviderType: "glm-api",
		InstanceID:   "glm-api-a1",
		Kind:         provider.KindAPICompatible,
		Image:        "pangaea/provider-api-compatible:test",
		Service:      provider.ServiceGLM,
		Models: []provider.Model{{
			ID:      "glm-4.6",
			Aliases: []string{"glm-default"},
		}},
		Auth: AuthSpec{
			Mode:          "api_key",
			Bootstrap:     "copy",
			HostPath:      "/srv/pangaea/secrets/glm.key",
			ContainerPath: "/run/pangaea/secrets/glm.key",
			FileMode:      "0600",
		},
		Shim: ShimSpec{
			Protocols:    []string{"anthropic"},
			Capabilities: []provider.Capability{provider.CapabilityAnthropicMessages, provider.CapabilityAuthAPIKey},
		},
		Upstream: UpstreamSpec{
			BaseURL:    "https://open.bigmodel.cn/api/anthropic",
			Compat:     "anthropic",
			APIKeyMode: "bearer",
		},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("container spec from api key provider spec: %v", err)
	}
	if spec.AuthCopy == nil {
		t.Fatalf("expected api key auth copy")
	}
	if spec.AuthCopy.HostPath != "/srv/pangaea/secrets/glm.key" || spec.AuthCopy.ContainerPath != "/run/pangaea/secrets/glm.key" || spec.AuthCopy.FileMode != 0o600 {
		t.Fatalf("unexpected api key auth copy: %#v", spec.AuthCopy)
	}
	if got := spec.Env["PANGAEA_UPSTREAM_API_KEY_FILE"]; got != "/run/pangaea/secrets/glm.key" {
		t.Fatalf("PANGAEA_UPSTREAM_API_KEY_FILE = %q, want copied key path", got)
	}
	if _, ok := spec.Env["PANGAEA_AUTH_PATH"]; ok {
		t.Fatalf("api_key auth should not expose PANGAEA_AUTH_PATH: %#v", spec.Env)
	}
	if got := spec.Env["PANGAEA_AUTH_DIR"]; got != "/run/pangaea/secrets" {
		t.Fatalf("PANGAEA_AUTH_DIR = %q, want /run/pangaea/secrets", got)
	}
	if got := spec.Env["PANGAEA_UPSTREAM_DIALECT"]; got != "anthropic" {
		t.Fatalf("PANGAEA_UPSTREAM_DIALECT = %q, want anthropic", got)
	}
}

func TestContainerSpecFromProviderSpecAddsPersistentStorageMounts(t *testing.T) {
	spec, err := ContainerSpecFromProviderSpec(ProviderSpec{
		ProviderType: "codex-kind",
		InstanceID:   "codex-kind-a1",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-codex:kind",
		Service:      provider.ServiceCodex,
		Storage: StorageSpec{
			Mode:     "persistent",
			HostPath: "/srv/pangaea/runtime/providers/codex-kind-a1",
		},
		Shim: ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("container spec from persistent provider spec: %v", err)
	}
	if len(spec.Mounts) != 2 {
		t.Fatalf("expected two persistent mounts, got %#v", spec.Mounts)
	}
	wantMounts := map[string]string{
		"/var/lib/pangaea": "/srv/pangaea/runtime/providers/codex-kind-a1/var-lib-pangaea",
		"/work":            "/srv/pangaea/runtime/providers/codex-kind-a1/work",
	}
	for _, mount := range spec.Mounts {
		if mount.Type != "bind" || !mount.Directory {
			t.Fatalf("unexpected mount: %#v", mount)
		}
		if mount.OwnerUID != 10001 || mount.OwnerGID != 10001 || mount.DirectoryMode != 0o700 {
			t.Fatalf("persistent mount should be prepared for provider uid/gid: %#v", mount)
		}
		if want := wantMounts[mount.Target]; mount.Source != want {
			t.Fatalf("mount source for %s = %q, want %q", mount.Target, mount.Source, want)
		}
	}
	for _, path := range spec.Security.WritablePaths {
		if path == "/var/lib/pangaea" || path == "/work" {
			t.Fatalf("persistent mount target should not also be tmpfs writable path: %#v", spec.Security.WritablePaths)
		}
	}
	if spec.Env["PANGAEA_STORAGE_MODE"] != "persistent" || spec.Env["PANGAEA_PROVIDER_STATE_DIR"] != "/var/lib/pangaea" {
		t.Fatalf("missing persistent storage env: %#v", spec.Env)
	}
}

func TestReconcileProviderContainerPullsCreatesCopiesAuthAndStarts(t *testing.T) {
	uid := 10001
	rt := &fakeContainerRuntime{stats: runtime.Stats{CPUPercent: 12.5, MemoryBytes: 64 * 1024 * 1024, MemoryPeakBytes: 96 * 1024 * 1024, OOMCount: 1}}
	result, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ProviderType: "gemini-secondary",
		InstanceID:   "gemini-secondary-a3",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-gemini:test",
		Service:      provider.ServiceGemini,
		Auth: AuthSpec{
			Mode:          "file",
			HostPath:      "/srv/pangaea/auth/gemini/secondary/oauth.json",
			ContainerPath: "/var/lib/pangaea/auth/gemini/oauth.json",
			OwnerUID:      &uid,
			FileMode:      "0600",
		},
		Shim: ShimSpec{Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent}},
	}, "node-a3", "snowbox")
	if err != nil {
		t.Fatalf("reconcile provider container: %v", err)
	}
	if rt.pulled != "pangaea/provider-gemini:test" || rt.created.ProviderType != "gemini-secondary" || rt.copied == nil || rt.started != "container-1" {
		t.Fatalf("runtime calls not recorded as expected: pulled=%q created=%#v copied=%#v started=%q", rt.pulled, rt.created, rt.copied, rt.started)
	}
	if got, want := strings.Join(rt.calls, ","), "pull,create,start,copy"; got != want {
		t.Fatalf("runtime call order = %s, want %s", got, want)
	}
	if result.Report.ProviderInstanceID != "gemini-secondary-a3" || result.Report.State != "running" || result.Report.Labels["pangaea.service"] != "gemini" {
		t.Fatalf("unexpected reconcile report: %#v", result.Report)
	}
	if result.Report.Resources.CPUPercent != 12.5 || result.Report.Resources.MemoryBytes != 64*1024*1024 || result.Report.Resources.OOMCount != 1 {
		t.Fatalf("unexpected reconcile resources: %#v", result.Report.Resources)
	}
}

func TestReconcileProviderContainerSkipsPullWhenImagePullPolicyNever(t *testing.T) {
	rt := &fakeContainerRuntime{}
	_, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ProviderType:    "codex-kind",
		InstanceID:      "codex-kind-a1",
		Kind:            provider.KindCLIContainer,
		Image:           "pangaea/provider-codex:kind",
		ImagePullPolicy: "never",
		Service:         provider.ServiceCodex,
		Shim:            ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("reconcile provider container: %v", err)
	}
	if rt.pulled != "" {
		t.Fatalf("image should not be pulled when image_pull_policy=never, pulled %q", rt.pulled)
	}
	if got, want := strings.Join(rt.calls, ","), "create,start"; got != want {
		t.Fatalf("runtime call order = %s, want %s", got, want)
	}
}

func TestReconcileProviderContainerReusesExistingContainer(t *testing.T) {
	rt := &fakeExistingContainerRuntime{
		status: runtime.ContainerStatus{
			ID:    "container-existing",
			Image: "pangaea/provider-codex:test",
			State: "exited",
		},
		found: true,
	}
	result, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ProviderType: "codex-primary",
		InstanceID:   "codex-primary-a1",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-codex:test",
		Service:      provider.ServiceCodex,
		Shim:         ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("reconcile existing provider container: %v", err)
	}
	if rt.pulled != "" || rt.created.Image != "" {
		t.Fatalf("existing container should not be pulled or created: pulled=%q created=%#v", rt.pulled, rt.created)
	}
	if rt.started != "container-existing" {
		t.Fatalf("expected existing container start, got %q", rt.started)
	}
	if result.ContainerID != "container-existing" || result.Report.State != "running" {
		t.Fatalf("unexpected existing container result: %#v", result)
	}
}

func TestReconcileExistingContainerRecreatesWhenImageChanges(t *testing.T) {
	rt := &fakeExistingContainerRuntime{
		status: runtime.ContainerStatus{
			ID:    "container-existing",
			Image: "pangaea/provider-codex:old",
			State: "running",
		},
		found: true,
	}
	result, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ProviderType: "codex-primary",
		InstanceID:   "codex-primary-a1",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-codex:new",
		Service:      provider.ServiceCodex,
		Shim:         ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("reconcile existing provider container: %v", err)
	}
	if rt.pulled != "pangaea/provider-codex:new" || rt.stopped != "container-existing" || rt.removed != "container-existing" {
		t.Fatalf("expected old container replaced: pulled=%q stopped=%q removed=%q", rt.pulled, rt.stopped, rt.removed)
	}
	if rt.created.Image != "pangaea/provider-codex:new" || rt.started != "container-1" {
		t.Fatalf("expected replacement container created and started: created=%#v started=%q", rt.created, rt.started)
	}
	if got, want := strings.Join(rt.calls, ","), "pull,stop,remove,create,start"; got != want {
		t.Fatalf("runtime call order = %s, want %s", got, want)
	}
	if result.ContainerID != "container-1" || result.Report.Image != "pangaea/provider-codex:new" {
		t.Fatalf("unexpected replacement result: %#v", result)
	}
}

func TestReconcileExistingContainerDoesNotOverwriteAuthByDefault(t *testing.T) {
	rt := &fakeExistingContainerRuntime{
		status: runtime.ContainerStatus{
			ID:    "container-existing",
			Image: "pangaea/provider-codex:test",
			State: "running",
		},
		found: true,
	}
	_, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ProviderType: "codex-primary",
		InstanceID:   "codex-primary-a1",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-codex:test",
		Service:      provider.ServiceCodex,
		Auth: AuthSpec{
			Mode:          "file",
			HostPath:      "/srv/pangaea/auth/codex/primary/auth.json",
			ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
		},
		Shim: ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("reconcile existing provider container: %v", err)
	}
	if rt.copied != nil || rt.copiedFrom != nil {
		t.Fatalf("existing container auth should not sync by default: copied=%#v copiedFrom=%#v", rt.copied, rt.copiedFrom)
	}
}

func TestReconcileExistingContainerSyncsAuthByPolicy(t *testing.T) {
	rt := &fakeExistingContainerRuntime{
		status: runtime.ContainerStatus{
			ID:    "container-existing",
			Image: "pangaea/provider-codex:test",
			State: "running",
		},
		found: true,
	}
	_, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ProviderType: "codex-primary",
		InstanceID:   "codex-primary-a1",
		Kind:         provider.KindCLIContainer,
		Image:        "pangaea/provider-codex:test",
		Service:      provider.ServiceCodex,
		Auth: AuthSpec{
			Mode:          "file",
			HostPath:      "/srv/pangaea/auth/codex/primary/auth.json",
			ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
			Sync: AuthSyncSpec{
				ContainerToHost: true,
				HostToContainer: "reconcile",
			},
		},
		Shim: ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("reconcile existing provider container: %v", err)
	}
	if rt.copied == nil || rt.copiedFrom == nil {
		t.Fatalf("expected bidirectional auth sync by policy: copied=%#v copiedFrom=%#v", rt.copied, rt.copiedFrom)
	}
	if got, want := strings.Join(rt.calls, ","), "copy-from,copy"; got != want {
		t.Fatalf("sync call order = %s, want %s", got, want)
	}
}

func TestContainerSpecFromProviderSpecRequiresImage(t *testing.T) {
	_, err := ContainerSpecFromProviderSpec(ProviderSpec{
		ProviderType: "deepseek-api",
		Kind:         provider.KindAPICompatible,
		Service:      provider.ServiceDeepSeek,
		Shim:         ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err == nil {
		t.Fatalf("expected image required error")
	}
}
