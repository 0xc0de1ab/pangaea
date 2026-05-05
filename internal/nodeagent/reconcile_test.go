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
	pulled  runtime.ImageRef
	created runtime.ContainerSpec
	copied  *runtime.CopySpec
	started runtime.ContainerID
	stats   runtime.Stats
	calls   []string
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

func (r *fakeContainerRuntime) Stop(context.Context, runtime.ContainerID, time.Duration) error {
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

func (r *fakeContainerRuntime) CopyFrom(context.Context, runtime.ContainerID, runtime.CopySpec) error {
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

func (r *fakeContainerRuntime) Remove(context.Context, runtime.ContainerID, runtime.RemoveOptions) error {
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
		ID:          "codex-samtest",
		InstanceID:  "codex-samtest-a1",
		Kind:        provider.KindCLIContainer,
		Image:       "pangaea/provider-codex:test",
		AccountHint: "samtest4u@gmail.com",
		Service:     provider.ServiceCodex,
		Models: []provider.Model{{
			ID:      "gpt-5-codex",
			Aliases: []string{"codex-default"},
		}},
		Auth: AuthSpec{
			Mode:          "file",
			Format:        "codex-auth-json-format",
			Bootstrap:     "copy",
			HostPath:      "/srv/pangaea/auth/codex/samtest/auth.json",
			ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
			OwnerUID:      &uid,
			OwnerGID:      &gid,
			FileMode:      "0600",
		},
		Refresh: RefreshSpec{Command: []string{"codex", "exec", "Reply with OK only."}, Threshold: "5m", Cooldown: "90s", Timeout: "2m"},
		Shim:    ShimSpec{Protocols: []string{"openai"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
		Upstream: UpstreamSpec{
			BaseURL: "http://127.0.0.1:8080",
		},
	}, "node-a1", "snowbox")
	if err != nil {
		t.Fatalf("container spec from provider spec: %v", err)
	}
	if spec.ProviderInstanceID != "codex-samtest-a1" || spec.HostName != "snowbox" || spec.Image != "pangaea/provider-codex:test" {
		t.Fatalf("unexpected container spec identity: %#v", spec)
	}
	if spec.Env["PANGAEA_AUTH_PATH"] != "/var/lib/pangaea/auth/codex/auth.json" {
		t.Fatalf("missing auth env: %#v", spec.Env)
	}
	for key, want := range map[string]string{
		"PANGAEA_SHIM_MODE":            "cli-container",
		"PANGAEA_ACCOUNT_DISPLAY":      "samtest4u@gmail.com",
		"PANGAEA_UPSTREAM_BASE_URL":    "http://127.0.0.1:8080",
		"PANGAEA_UPSTREAM_DIALECT":     "openai",
		"PANGAEA_MODEL":                "gpt-5-codex",
		"PANGAEA_MODEL_ALIAS":          "codex-default",
		"PANGAEA_AUTH_FORMAT":          "codex-auth-json-format",
		"PANGAEA_REFRESH_COMMAND":      "'codex' 'exec' 'Reply with OK only.'",
		"PANGAEA_REFRESH_THRESHOLD":    "5m",
		"PANGAEA_REFRESH_COOLDOWN":     "90s",
		"PANGAEA_REFRESH_TIMEOUT":      "2m",
		"PANGAEA_PROVIDER_INSTANCE_ID": "codex-samtest-a1",
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
}

func TestContainerSpecFromProviderSpecWithOptionsIncludesRouterURLs(t *testing.T) {
	spec, err := ContainerSpecFromProviderSpecWithOptions(ProviderSpec{
		ID:         "codex-samtest",
		InstanceID: "codex-samtest-a1",
		Kind:       provider.KindCLIContainer,
		Image:      "pangaea/provider-codex:test",
		Service:    provider.ServiceCodex,
		Shim:       ShimSpec{Protocols: []string{"openai"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox", ContainerSpecOptions{
		RouterControlURL: "ws://router/router/v1/control/ws",
		RouterDataURL:    "ws://router/router/v1/data/ws",
		StreamTokenKey:   "test-token-key",
	})
	if err != nil {
		t.Fatalf("container spec from provider spec with options: %v", err)
	}
	for key, want := range map[string]string{
		"PANGAEA_ROUTER_CONTROL_URL": "ws://router/router/v1/control/ws",
		"PANGAEA_ROUTER_DATA_URL":    "ws://router/router/v1/data/ws",
		"PANGAEA_STREAM_TOKEN_KEY":   "test-token-key",
	} {
		if spec.Env[key] != want {
			t.Fatalf("env[%s] = %q, want %q in %#v", key, spec.Env[key], want, spec.Env)
		}
	}
}

func TestReconcileProviderContainerPullsCreatesCopiesAuthAndStarts(t *testing.T) {
	uid := 10001
	rt := &fakeContainerRuntime{stats: runtime.Stats{CPUPercent: 12.5, MemoryBytes: 64 * 1024 * 1024, MemoryPeakBytes: 96 * 1024 * 1024, OOMCount: 1}}
	result, err := ReconcileProviderContainer(context.Background(), rt, ProviderSpec{
		ID:         "gemini-nullcode",
		InstanceID: "gemini-nullcode-a3",
		Kind:       provider.KindCLIContainer,
		Image:      "pangaea/provider-gemini:test",
		Service:    provider.ServiceGemini,
		Auth: AuthSpec{
			Mode:          "file",
			HostPath:      "/srv/pangaea/auth/gemini/nullcode/oauth.json",
			ContainerPath: "/var/lib/pangaea/auth/gemini/oauth.json",
			OwnerUID:      &uid,
			FileMode:      "0600",
		},
		Shim: ShimSpec{Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent}},
	}, "node-a3", "snowbox")
	if err != nil {
		t.Fatalf("reconcile provider container: %v", err)
	}
	if rt.pulled != "pangaea/provider-gemini:test" || rt.created.ProviderID != "gemini-nullcode" || rt.copied == nil || rt.started != "container-1" {
		t.Fatalf("runtime calls not recorded as expected: pulled=%q created=%#v copied=%#v started=%q", rt.pulled, rt.created, rt.copied, rt.started)
	}
	if got, want := strings.Join(rt.calls, ","), "pull,create,start,copy"; got != want {
		t.Fatalf("runtime call order = %s, want %s", got, want)
	}
	if result.Report.ProviderInstanceID != "gemini-nullcode-a3" || result.Report.State != "running" || result.Report.Labels["pangaea.service"] != "gemini" {
		t.Fatalf("unexpected reconcile report: %#v", result.Report)
	}
	if result.Report.Resources.CPUPercent != 12.5 || result.Report.Resources.MemoryBytes != 64*1024*1024 || result.Report.Resources.OOMCount != 1 {
		t.Fatalf("unexpected reconcile resources: %#v", result.Report.Resources)
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
		ID:         "codex-samtest",
		InstanceID: "codex-samtest-a1",
		Kind:       provider.KindCLIContainer,
		Image:      "pangaea/provider-codex:test",
		Service:    provider.ServiceCodex,
		Shim:       ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
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

func TestContainerSpecFromProviderSpecRequiresImage(t *testing.T) {
	_, err := ContainerSpecFromProviderSpec(ProviderSpec{
		ID:      "deepseek-api",
		Kind:    provider.KindAPICompatible,
		Service: provider.ServiceDeepSeek,
		Shim:    ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}, "node-a1", "snowbox")
	if err == nil {
		t.Fatalf("expected image required error")
	}
}
