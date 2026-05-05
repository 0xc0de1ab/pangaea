package nodeagent

import (
	"context"
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
}

func (r *fakeContainerRuntime) Info(context.Context) (runtime.RuntimeInfo, error) {
	return runtime.RuntimeInfo{Kind: "fake"}, nil
}

func (r *fakeContainerRuntime) Pull(_ context.Context, image runtime.ImageRef) error {
	r.pulled = image
	return nil
}

func (r *fakeContainerRuntime) Create(_ context.Context, spec runtime.ContainerSpec) (runtime.ContainerID, error) {
	r.created = spec
	return "container-1", nil
}

func (r *fakeContainerRuntime) Start(_ context.Context, id runtime.ContainerID) error {
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
	r.copied = &spec
	return nil
}

func (r *fakeContainerRuntime) CopyFrom(context.Context, runtime.ContainerID, runtime.CopySpec) error {
	return nil
}

func (r *fakeContainerRuntime) Stats(context.Context, runtime.ContainerID) (runtime.Stats, error) {
	return runtime.Stats{}, nil
}

func (r *fakeContainerRuntime) Logs(context.Context, runtime.ContainerID, runtime.LogSpec) (<-chan runtime.LogEvent, error) {
	ch := make(chan runtime.LogEvent)
	close(ch)
	return ch, nil
}

func (r *fakeContainerRuntime) Remove(context.Context, runtime.ContainerID, runtime.RemoveOptions) error {
	return nil
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
		Auth: AuthSpec{
			Mode:          "file",
			Bootstrap:     "copy",
			HostPath:      "/srv/pangaea/auth/codex/samtest/auth.json",
			ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
			OwnerUID:      &uid,
			OwnerGID:      &gid,
			FileMode:      "0600",
		},
		Shim: ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
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
	if spec.AuthCopy == nil || spec.AuthCopy.OwnerUID != 10001 || spec.AuthCopy.FileMode != 0o600 {
		t.Fatalf("unexpected auth copy: %#v", spec.AuthCopy)
	}
	if !spec.Security.RunAsNonRoot || !spec.Security.ReadOnlyRootFS {
		t.Fatalf("expected hardened defaults: %#v", spec.Security)
	}
}

func TestReconcileProviderContainerPullsCreatesCopiesAuthAndStarts(t *testing.T) {
	uid := 10001
	rt := &fakeContainerRuntime{}
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
	if result.Report.ProviderInstanceID != "gemini-nullcode-a3" || result.Report.State != "running" || result.Report.Labels["pangaea.service"] != "gemini" {
		t.Fatalf("unexpected reconcile report: %#v", result.Report)
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
