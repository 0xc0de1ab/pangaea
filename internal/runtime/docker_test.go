package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

var _ Runtime = (*DockerRuntime)(nil)

type recordedCommand struct {
	binary string
	args   []string
}

type recordingRunner struct {
	commands []recordedCommand
	outputs  map[string]ExecResult
}

func (r *recordingRunner) Run(_ context.Context, binary string, args []string, _ []byte) (ExecResult, error) {
	r.commands = append(r.commands, recordedCommand{binary: binary, args: append([]string(nil), args...)})
	key := strings.Join(args, " ")
	if out, ok := r.outputs[key]; ok {
		return out, nil
	}
	return ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}, nil
}

func TestDockerRuntimeCreateStartCopyExecAndRemove(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"create --name pangaea-codex-samtest --label pangaea.provider_id=codex-samtest --label pangaea.provider_instance_id=codex-samtest-a1 --env PANGAEA_PROVIDER_ID=codex-samtest --workdir /work --security-opt no-new-privileges --cap-drop ALL --read-only --tmpfs /var/lib/pangaea --tmpfs /run/pangaea --tmpfs /tmp --user 10001:10001 pangaea/provider-codex:test /usr/local/bin/provider-entrypoint": {ExitCode: 0, Stdout: []byte("container-1\n")},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	spec := ContainerSpec{
		ProviderID:         "codex-samtest",
		ProviderInstanceID: "codex-samtest-a1",
		Name:               "pangaea-codex-samtest",
		Image:              "pangaea/provider-codex:test",
		Command:            []string{"/usr/local/bin/provider-entrypoint"},
		WorkingDir:         "/work",
		Env:                map[string]string{"PANGAEA_PROVIDER_ID": "codex-samtest"},
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
	if err := rt.CopyTo(context.Background(), id, CopySpec{HostPath: "/host/auth.json", ContainerPath: "/container/auth.json", OwnerUID: 10001, OwnerGID: 10001, FileMode: 0o600}); err != nil {
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
		"docker create --name pangaea-codex-samtest",
		"docker start container-1",
		"docker exec container-1 true",
		"docker cp /host/auth.json container-1:/container/auth.json",
		"docker exec container-1 chmod 0600 /container/auth.json",
		"docker exec container-1 chown 10001:10001 /container/auth.json",
		"docker stop --time 2 container-1",
		"docker rm --force --volumes container-1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected command %q in:\n%s", want, got)
		}
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

func TestDockerRuntimeFindByLabels(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]ExecResult{
		"ps -a --format {{json .}} --filter label=pangaea.provider_instance_id=codex-samtest-a1": {
			ExitCode: 0,
			Stdout:   []byte(`{"ID":"container-1","Image":"pangaea/provider-codex:test","Names":"pangaea-codex","Status":"Up 2 minutes"}` + "\n"),
		},
	}}
	rt := &DockerRuntime{Binary: "docker", Runner: runner}
	status, found, err := rt.FindByLabels(context.Background(), map[string]string{"pangaea.provider_instance_id": "codex-samtest-a1"})
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
