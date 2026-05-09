package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

var _ Runtime = (*fakeRuntime)(nil)

type fakeRuntime struct {
	created ContainerSpec
	id      ContainerID
}

func (r *fakeRuntime) Info(context.Context) (RuntimeInfo, error) {
	return RuntimeInfo{Kind: "fake", Version: "test", Rootless: true}, nil
}

func (r *fakeRuntime) Pull(context.Context, ImageRef) error {
	return nil
}

func (r *fakeRuntime) Create(_ context.Context, spec ContainerSpec) (ContainerID, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	r.created = spec
	r.id = ContainerID("fake://provider")
	return r.id, nil
}

func (r *fakeRuntime) Start(context.Context, ContainerID) error {
	return nil
}

func (r *fakeRuntime) Stop(context.Context, ContainerID, time.Duration) error {
	return nil
}

func (r *fakeRuntime) Exec(context.Context, ContainerID, ExecSpec) (ExecResult, error) {
	return ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}, nil
}

func (r *fakeRuntime) CopyTo(context.Context, ContainerID, CopySpec) error {
	return nil
}

func (r *fakeRuntime) CopyFrom(context.Context, ContainerID, CopySpec) error {
	return nil
}

func (r *fakeRuntime) Stats(context.Context, ContainerID) (Stats, error) {
	return Stats{ObservedAt: time.Now(), MemoryBytes: 64}, nil
}

func (r *fakeRuntime) Logs(context.Context, ContainerID, LogSpec) (<-chan LogEvent, error) {
	ch := make(chan LogEvent, 1)
	ch <- LogEvent{Stream: LogStreamStdout, Line: []byte("ready")}
	close(ch)
	return ch, nil
}

func (r *fakeRuntime) Remove(context.Context, ContainerID, RemoveOptions) error {
	return nil
}

func TestFakeRuntimeExercisesInterface(t *testing.T) {
	ctx := context.Background()
	rt := &fakeRuntime{}
	spec := validContainerSpec()

	info, err := rt.Info(ctx)
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info.Kind != "fake" || !info.Rootless {
		t.Fatalf("unexpected runtime info: %+v", info)
	}
	if err := rt.Pull(ctx, spec.Image); err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	id, err := rt.Create(ctx, spec)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected container id")
	}
	if err := rt.Start(ctx, id); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if result, err := rt.Exec(ctx, id, ExecSpec{Command: []string{"true"}}); err != nil || result.ExitCode != 0 {
		t.Fatalf("Exec result=%+v err=%v", result, err)
	}
	if err := rt.CopyTo(ctx, id, *spec.AuthCopy); err != nil {
		t.Fatalf("CopyTo failed: %v", err)
	}
	if err := rt.CopyFrom(ctx, id, *spec.AuthCopy); err != nil {
		t.Fatalf("CopyFrom failed: %v", err)
	}
	if stats, err := rt.Stats(ctx, id); err != nil || stats.MemoryBytes == 0 {
		t.Fatalf("Stats result=%+v err=%v", stats, err)
	}
	logs, err := rt.Logs(ctx, id, LogSpec{Tail: 1})
	if err != nil {
		t.Fatalf("Logs failed: %v", err)
	}
	if event := <-logs; event.Stream != LogStreamStdout {
		t.Fatalf("unexpected log event: %+v", event)
	}
	if err := rt.Stop(ctx, id, time.Second); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if err := rt.Remove(ctx, id, RemoveOptions{Force: true}); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
}

func TestDefaultSecurityProfile(t *testing.T) {
	profile := DefaultSecurityProfile()
	if !profile.RunAsNonRoot || profile.RunAsUser == 0 || profile.RunAsGroup == 0 {
		t.Fatalf("expected non-root defaults: %+v", profile)
	}
	if !profile.NoNewPrivileges {
		t.Fatalf("expected no-new-privileges default: %+v", profile)
	}
	if len(profile.DropCapabilities) != 1 || profile.DropCapabilities[0] != "ALL" {
		t.Fatalf("expected all capabilities dropped: %+v", profile)
	}
	if !profile.ReadOnlyRootFS {
		t.Fatalf("expected read-only rootfs default: %+v", profile)
	}
}

func TestContainerSpecValidateAcceptsMinimalSpec(t *testing.T) {
	spec := ContainerSpec{
		ProviderID: "codex-primary",
		Image:      ImageRef("pangaea/provider-codex:2026.05.1"),
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("expected valid spec: %v", err)
	}
}

func TestContainerSpecValidateAcceptsOptionalIdentityFields(t *testing.T) {
	spec := validContainerSpec()

	if err := spec.Validate(); err != nil {
		t.Fatalf("expected valid spec with optional identity fields: %v", err)
	}
}

func TestContainerSpecValidateRejectsMissingProviderID(t *testing.T) {
	spec := validContainerSpec()
	spec.ProviderID = " "

	if err := spec.Validate(); !errors.Is(err, ErrInvalidContainerSpec) {
		t.Fatalf("expected ErrInvalidContainerSpec, got %v", err)
	}
}

func TestContainerSpecValidateRejectsInvalidImage(t *testing.T) {
	spec := validContainerSpec()
	spec.Image = "bad image"

	if err := spec.Validate(); !errors.Is(err, ErrInvalidImageRef) {
		t.Fatalf("expected ErrInvalidImageRef, got %v", err)
	}
}

func TestContainerSpecValidateRejectsInvalidOptionalIdentity(t *testing.T) {
	spec := validContainerSpec()
	spec.NodeID = "node a1"

	if err := spec.Validate(); !errors.Is(err, ErrInvalidContainerSpec) {
		t.Fatalf("expected ErrInvalidContainerSpec, got %v", err)
	}
}

func TestContainerSpecValidateRejectsIncompleteAuthCopy(t *testing.T) {
	spec := validContainerSpec()
	spec.AuthCopy.ContainerPath = ""

	if err := spec.Validate(); !errors.Is(err, ErrInvalidCopySpec) {
		t.Fatalf("expected ErrInvalidCopySpec, got %v", err)
	}
}

func TestContainerSpecValidateRejectsRelativeAuthCopyPath(t *testing.T) {
	spec := validContainerSpec()
	spec.AuthCopy.HostPath = "auth/codex/auth.json"

	if err := spec.Validate(); !errors.Is(err, ErrInvalidCopySpec) {
		t.Fatalf("expected ErrInvalidCopySpec, got %v", err)
	}
}

func TestContainerSpecValidateRejectsInvalidResourceLimits(t *testing.T) {
	spec := validContainerSpec()
	spec.Resources = ResourceLimits{CPUs: " 2", PIDsLimit: -1}

	if err := spec.Validate(); !errors.Is(err, ErrInvalidContainerSpec) {
		t.Fatalf("expected ErrInvalidContainerSpec, got %v", err)
	}
}

func validContainerSpec() ContainerSpec {
	return ContainerSpec{
		ProviderID:         "codex-primary",
		ProviderInstanceID: "codex-primary/a1/01",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Name:               "pangaea-codex-primary",
		Image:              ImageRef("pangaea/provider-codex:2026.05.1"),
		Command:            []string{"/usr/local/bin/provider-entrypoint"},
		Env: map[string]string{
			"PANGAEA_PROVIDER_ID": "codex-primary",
		},
		AuthCopy: &CopySpec{
			HostPath:      "/srv/pangaea/auth/codex/primary/auth.json",
			ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
			OwnerUID:      10001,
			OwnerGID:      10001,
			FileMode:      0o600,
		},
		Security: DefaultSecurityProfile(),
	}
}
