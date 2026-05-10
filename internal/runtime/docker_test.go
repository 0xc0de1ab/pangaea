package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var _ Runtime = (*DockerRuntime)(nil)

type recordedCommand struct {
	binary string
	args   []string
	stdin  int
}

type recordingRunner struct {
	commands []recordedCommand
	outputs  map[string]ExecResult
}

func (r *recordingRunner) Run(_ context.Context, binary string, args []string, stdin []byte) (ExecResult, error) {
	r.commands = append(r.commands, recordedCommand{binary: binary, args: append([]string(nil), args...), stdin: len(stdin)})
	key := strings.Join(args, " ")
	if out, ok := r.outputs[key]; ok {
		return out, nil
	}
	return ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}, nil
}

type streamingRunner struct {
	recordingRunner
	logs chan LogEvent
}

func (r *streamingRunner) RunLogs(_ context.Context, binary string, args []string) (<-chan LogEvent, error) {
	r.commands = append(r.commands, recordedCommand{binary: binary, args: append([]string(nil), args...)})
	return r.logs, nil
}

func TestDockerRuntimeCreateStartCopyExecAndRemove(t *testing.T) {
	hostAuthPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(hostAuthPath, []byte(`{"token":"test"}`), 0o644); err != nil {
		t.Fatalf("write host auth: %v", err)
	}
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"create --name pangaea-codex-primary --label pangaea.provider_type=codex-primary --label pangaea.provider_instance_id=codex-primary-a1 --env PANGAEA_PROVIDER_TYPE=codex-primary --workdir /work --security-opt no-new-privileges --cap-drop ALL --read-only --tmpfs /var/lib/pangaea:uid=10001,gid=10001,mode=0700 --tmpfs /run/pangaea:uid=10001,gid=10001,mode=0700 --tmpfs /tmp:uid=10001,gid=10001,mode=1777 --tmpfs /work:uid=10001,gid=10001,mode=0700 --user 10001:10001 pangaea/provider-codex:test /usr/local/bin/provider-entrypoint": {ExitCode: 0, Stdout: []byte("container-1\n")},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	spec := ContainerSpec{
		ProviderType:       "codex-primary",
		ProviderInstanceID: "codex-primary-a1",
		Name:               "pangaea-codex-primary",
		Image:              "pangaea/provider-codex:test",
		Command:            []string{"/usr/local/bin/provider-entrypoint"},
		WorkingDir:         "/work",
		Env:                map[string]string{"PANGAEA_PROVIDER_TYPE": "codex-primary"},
		Security:           DefaultSecurityProfile(),
	}
	id, err := rt.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "container-1" {
		t.Fatalf("container id = %q", id)
	}
	if err := rt.Start(context.Background(), id); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := rt.Exec(context.Background(), id, ExecSpec{Command: []string{"true"}}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := rt.CopyTo(context.Background(), id, CopySpec{HostPath: hostAuthPath, ContainerPath: "/container/auth.json", OwnerUID: 10001, OwnerGID: 10001, FileMode: 0o600}); err != nil {
		t.Fatalf("copy to: %v", err)
	}
	if err := rt.Stop(context.Background(), id, 2*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := rt.Remove(context.Background(), id, RemoveOptions{Force: true, Volumes: true}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := joinedCommands(runner.commands)
	for _, want := range []string{
		"docker create --name pangaea-codex-primary",
		"docker start container-1",
		"docker exec container-1 true",
		"docker exec --user 10001:10001 container-1 mkdir -p /container",
		"docker exec -i --user 10001:10001 container-1 sh -c cat > \"$1\" && chmod \"$2\" \"$1\" sh /container/auth.json 0600",
		"docker stop --time 2 container-1",
		"docker rm --force --volumes container-1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected command %q in:\n%s", want, got)
		}
	}
	if runner.commands[4].stdin != len(`{"token":"test"}`) {
		t.Fatalf("expected auth payload on copy stdin, got %#v", runner.commands[4])
	}
}

func TestDockerRuntimeCreateAddsBindMounts(t *testing.T) {
	hostDir := filepath.Join(t.TempDir(), "provider-state")
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"create --name pangaea-codex-primary --label pangaea.provider_type=codex-primary --label pangaea.provider_instance_id=codex-primary-a1 --mount type=bind,source=" + hostDir + ",target=/var/lib/pangaea --security-opt no-new-privileges --cap-drop ALL --read-only --tmpfs /run/pangaea:uid=10001,gid=10001,mode=0700 --tmpfs /tmp:uid=10001,gid=10001,mode=1777 --tmpfs /work:uid=10001,gid=10001,mode=0700 --user 10001:10001 pangaea/provider-codex:test": {ExitCode: 0, Stdout: []byte("container-1\n")},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	spec := ContainerSpec{
		ProviderType:       "codex-primary",
		ProviderInstanceID: "codex-primary-a1",
		Name:               "pangaea-codex-primary",
		Image:              "pangaea/provider-codex:test",
		Mounts: []MountSpec{{
			Type:      "bind",
			Source:    hostDir,
			Target:    "/var/lib/pangaea",
			Directory: true,
		}},
		Security: DefaultSecurityProfile(),
	}
	spec.Security.WritablePaths = []string{"/run/pangaea", "/tmp", "/work"}
	id, err := rt.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("create with bind mount: %v", err)
	}
	if id != "container-1" {
		t.Fatalf("container id = %q", id)
	}
	if info, err := os.Stat(hostDir); err != nil || !info.IsDir() {
		t.Fatalf("expected bind source directory to be prepared, info=%#v err=%v", info, err)
	}
	if got := joinedCommands(runner.commands); !strings.Contains(got, "--mount type=bind,source="+hostDir+",target=/var/lib/pangaea") {
		t.Fatalf("missing bind mount in:\n%s", got)
	}
}

func TestDockerRuntimeCreateAddsNetworkMode(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"create --name pangaea-gemini-cli --label pangaea.provider_type=gemini-cli --label pangaea.provider_instance_id=gemini-cli-opi5 --network host --security-opt no-new-privileges --cap-drop ALL --read-only --tmpfs /var/lib/pangaea:uid=10001,gid=10001,mode=0700 --tmpfs /run/pangaea:uid=10001,gid=10001,mode=0700 --tmpfs /tmp:uid=10001,gid=10001,mode=1777 --tmpfs /work:uid=10001,gid=10001,mode=0700 --user 10001:10001 pangaea/provider-gemini:opi5": {ExitCode: 0, Stdout: []byte("container-1\n")},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	spec := ContainerSpec{
		ProviderType:       "gemini-cli",
		ProviderInstanceID: "gemini-cli-opi5",
		Name:               "pangaea-gemini-cli",
		Image:              "pangaea/provider-gemini:opi5",
		NetworkMode:        "host",
		Security:           DefaultSecurityProfile(),
	}
	id, err := rt.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("create with network host: %v", err)
	}
	if id != "container-1" {
		t.Fatalf("container id = %q", id)
	}
	if got := joinedCommands(runner.commands); !strings.Contains(got, "--network host") {
		t.Fatalf("missing network mode in:\n%s", got)
	}
}

func TestDockerCopyArchiveAppliesDestinationMetadata(t *testing.T) {
	hostAuthPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(hostAuthPath, []byte(`{"token":"test"}`), 0o644); err != nil {
		t.Fatalf("write host auth: %v", err)
	}
	archive, err := dockerCopyArchive(CopySpec{
		HostPath:      hostAuthPath,
		ContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
		OwnerUID:      10001,
		OwnerGID:      10002,
		FileMode:      0o600,
	})
	if err != nil {
		t.Fatalf("copy archive: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	header, err := tr.Next()
	if err != nil {
		t.Fatalf("read tar header: %v", err)
	}
	if header.Name != "auth.json" || header.Mode != 0o600 || header.Uid != 10001 || header.Gid != 10002 {
		t.Fatalf("unexpected tar header: %#v", header)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("read tar payload: %v", err)
	}
	if string(data) != `{"token":"test"}` {
		t.Fatalf("unexpected tar payload: %s", string(data))
	}
}

func TestDockerRuntimeInfoStatsAndLogs(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"version --format {{.Server.Version}}":              {ExitCode: 0, Stdout: []byte("26.1.0\n")},
		"stats --no-stream --format {{json .}} container-1": {ExitCode: 0, Stdout: []byte(`{"CPUPerc":"12.5%","MemUsage":"64MiB / 1GiB","PIDs":"7"}`)},
		"logs --tail 2 container-1":                         {ExitCode: 0, Stdout: []byte("ready\nok\n")},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	info, err := rt.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Kind != "docker" || info.Version != "26.1.0" {
		t.Fatalf("unexpected info: %#v", info)
	}
	stats, err := rt.Stats(context.Background(), "container-1")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.CPUPercent != 12.5 || stats.MemoryBytes != 64*1024*1024 || stats.MemoryLimitBytes != 1024*1024*1024 || stats.PIDs != 7 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
	logs, err := rt.Logs(context.Background(), "container-1", LogSpec{Tail: 2})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	var lines []string
	for event := range logs {
		lines = append(lines, string(event.Line))
	}
	if strings.Join(lines, ",") != "ready,ok" {
		t.Fatalf("unexpected logs: %#v", lines)
	}
}

func TestDockerRuntimeCreateIncludesResourceLimits(t *testing.T) {
	runner := &recordingRunner{}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	_, err := rt.Create(context.Background(), ContainerSpec{
		ProviderType: "deepseek-api",
		Image:        "pangaea/provider-api:test",
		Resources: ResourceLimits{
			CPUs:      "1.5",
			Memory:    "768MiB",
			PIDsLimit: 256,
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got := joinedCommands(runner.commands)
	for _, want := range []string{
		"--cpus 1.5",
		"--memory 768MiB",
		"--pids-limit 256",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected resource arg %q in:\n%s", want, got)
		}
	}
}

func TestDockerRuntimeCreateUsesCanonicalManagedLabels(t *testing.T) {
	runner := &recordingRunner{}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	_, err := rt.Create(context.Background(), ContainerSpec{
		ProviderType:       "codex-primary",
		ProviderInstanceID: "codex-primary-a1",
		Image:              "pangaea/provider-codex:test",
		Labels: map[string]string{
			"pangaea.provider_type":        "wrong-provider",
			"pangaea.provider_instance_id": "wrong-instance",
			"pangaea.service":              "codex",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got := joinedCommands(runner.commands)
	for _, bad := range []string{"wrong-provider", "wrong-instance"} {
		if strings.Contains(got, bad) {
			t.Fatalf("managed label used non-canonical value %q in:\n%s", bad, got)
		}
	}
	for _, want := range []string{
		"--label pangaea.provider_type=codex-primary",
		"--label pangaea.provider_instance_id=codex-primary-a1",
		"--label pangaea.service=codex",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected label %q in:\n%s", want, got)
		}
	}
}

func TestDockerRuntimeFollowLogsUsesStreamingRunner(t *testing.T) {
	logs := make(chan LogEvent, 1)
	logs <- LogEvent{Stream: LogStreamStdout, Line: []byte("ready")}
	close(logs)
	runner := &streamingRunner{logs: logs}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	out, err := rt.Logs(context.Background(), "container-1", LogSpec{Tail: 10, Follow: true})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	event, ok := <-out
	if !ok || event.Stream != LogStreamStdout || string(event.Line) != "ready" {
		t.Fatalf("unexpected log event: ok=%v event=%#v", ok, event)
	}
	got := joinedCommands(runner.commands)
	if !strings.Contains(got, "docker logs --tail 10 --follow container-1") {
		t.Fatalf("expected follow logs command in:\n%s", got)
	}
}

func TestDockerRuntimeFindByLabels(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"ps -a --format {{json .}} --filter label=pangaea.provider_instance_id=codex-primary-a1": {
			ExitCode: 0,
			Stdout:   []byte(`{"ID":"container-1","Image":"pangaea/provider-codex:test","Names":"pangaea-codex","Status":"Up 2 minutes"}` + "\n"),
		},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	status, found, err := rt.FindByLabels(context.Background(), map[string]string{"pangaea.provider_instance_id": "codex-primary-a1"})
	if err != nil {
		t.Fatalf("find by labels: %v", err)
	}
	if !found || status.ID != "container-1" || status.State != "running" || status.Image != "pangaea/provider-codex:test" {
		t.Fatalf("unexpected container status: found=%v status=%#v", found, status)
	}
}

func TestDockerParseBytes(t *testing.T) {
	for raw, want := range map[string]uint64{
		"1KiB":  1024,
		"64MiB": 64 * 1024 * 1024,
		"1 GiB": 1024 * 1024 * 1024,
		"42":    42,
	} {
		if got := parseDockerBytes(raw); got != want {
			t.Fatalf("parseDockerBytes(%q)=%d want %d", raw, got, want)
		}
	}
}

func joinedCommands(commands []recordedCommand) string {
	lines := make([]string, 0, len(commands))
	for _, command := range commands {
		lines = append(lines, command.binary+" "+strings.Join(command.args, " "))
	}
	return strings.Join(lines, "\n")
}
