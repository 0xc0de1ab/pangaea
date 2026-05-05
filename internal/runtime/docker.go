package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type CommandRunner interface {
	Run(context.Context, string, []string, []byte) (ExecResult, error)
}

type DockerRuntime struct {
	Binary string
	Runner CommandRunner
}

func NewDockerRuntime(binary string) *DockerRuntime {
	if binary == "" {
		binary = "docker"
	}
	return &DockerRuntime{Binary: binary, Runner: shellRunner{}}
}

func (d *DockerRuntime) Info(ctx context.Context) (RuntimeInfo, error) {
	out, err := d.run(ctx, []string{"version", "--format", "{{.Server.Version}}"}, nil)
	if err != nil {
		return RuntimeInfo{}, err
	}
	version := strings.TrimSpace(string(out.Stdout))
	return RuntimeInfo{
		Kind:         "docker",
		Version:      version,
		Capabilities: []string{"container.create", "container.start", "container.stop", "container.exec", "container.copy", "container.stats"},
	}, nil
}

func (d *DockerRuntime) Pull(ctx context.Context, image ImageRef) error {
	if err := image.Validate(); err != nil {
		return err
	}
	_, err := d.run(ctx, []string{"pull", image.String()}, nil)
	return err
}

func (d *DockerRuntime) Create(ctx context.Context, spec ContainerSpec) (ContainerID, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	args := []string{"create"}
	if spec.Name != "" {
		args = append(args, "--name", spec.Name)
	}
	for key, value := range spec.Labels {
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, "--label", "pangaea.provider_id="+spec.ProviderID)
	if spec.ProviderInstanceID != "" {
		args = append(args, "--label", "pangaea.provider_instance_id="+spec.ProviderInstanceID)
	}
	for key, value := range spec.Env {
		args = append(args, "--env", key+"="+value)
	}
	if spec.WorkingDir != "" {
		args = append(args, "--workdir", spec.WorkingDir)
	}
	if len(spec.Entrypoint) > 0 {
		args = append(args, "--entrypoint", strings.Join(spec.Entrypoint, " "))
	}
	args = appendSecurityArgs(args, spec.Security)
	args = append(args, spec.Image.String())
	args = append(args, spec.Command...)
	out, err := d.run(ctx, args, nil)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out.Stdout))
	if id == "" {
		return "", fmt.Errorf("docker create returned empty container id")
	}
	return ContainerID(id), nil
}

func (d *DockerRuntime) Start(ctx context.Context, id ContainerID) error {
	_, err := d.run(ctx, []string{"start", id.String()}, nil)
	return err
}

func (d *DockerRuntime) Stop(ctx context.Context, id ContainerID, timeout time.Duration) error {
	args := []string{"stop"}
	if timeout > 0 {
		args = append(args, "--time", strconv.Itoa(int(timeout.Seconds())))
	}
	args = append(args, id.String())
	_, err := d.run(ctx, args, nil)
	return err
}

func (d *DockerRuntime) Exec(ctx context.Context, id ContainerID, spec ExecSpec) (ExecResult, error) {
	if len(spec.Command) == 0 {
		return ExecResult{}, fmt.Errorf("exec command is required")
	}
	args := []string{"exec"}
	if spec.TTY {
		args = append(args, "-t")
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	if spec.WorkingDir != "" {
		args = append(args, "--workdir", spec.WorkingDir)
	}
	for key, value := range spec.Env {
		args = append(args, "--env", key+"="+value)
	}
	args = append(args, id.String())
	args = append(args, spec.Command...)
	return d.run(ctx, args, nil)
}

func (d *DockerRuntime) CopyTo(ctx context.Context, id ContainerID, spec CopySpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if _, err := d.run(ctx, []string{"cp", spec.HostPath, id.String() + ":" + spec.ContainerPath}, nil); err != nil {
		return err
	}
	if spec.FileMode != 0 {
		if _, err := d.Exec(ctx, id, ExecSpec{Command: []string{"chmod", fmt.Sprintf("%04o", spec.FileMode.Perm()), spec.ContainerPath}}); err != nil {
			return err
		}
	}
	if spec.OwnerUID != 0 || spec.OwnerGID != 0 {
		owner := fmt.Sprintf("%d:%d", spec.OwnerUID, spec.OwnerGID)
		if _, err := d.Exec(ctx, id, ExecSpec{Command: []string{"chown", owner, spec.ContainerPath}}); err != nil {
			return err
		}
	}
	return nil
}

func (d *DockerRuntime) CopyFrom(ctx context.Context, id ContainerID, spec CopySpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	_, err := d.run(ctx, []string{"cp", id.String() + ":" + spec.ContainerPath, spec.HostPath}, nil)
	return err
}

func (d *DockerRuntime) Stats(ctx context.Context, id ContainerID) (Stats, error) {
	out, err := d.run(ctx, []string{"stats", "--no-stream", "--format", "{{json .}}", id.String()}, nil)
	if err != nil {
		return Stats{}, err
	}
	var raw dockerStats
	if err := json.Unmarshal(bytes.TrimSpace(out.Stdout), &raw); err != nil {
		return Stats{}, err
	}
	memory, limit := parseDockerMemUsage(raw.MemUsage)
	pids, _ := strconv.ParseUint(strings.TrimSpace(raw.PIDs), 10, 64)
	return Stats{
		ObservedAt:       time.Now().UTC(),
		CPUPercent:       parseDockerPercent(raw.CPUPerc),
		MemoryBytes:      memory,
		MemoryLimitBytes: limit,
		PIDs:             pids,
	}, nil
}

func (d *DockerRuntime) Logs(ctx context.Context, id ContainerID, spec LogSpec) (<-chan LogEvent, error) {
	args := []string{"logs"}
	if spec.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(spec.Tail))
	}
	if !spec.Since.IsZero() {
		args = append(args, "--since", spec.Since.Format(time.RFC3339))
	}
	if spec.Follow {
		args = append(args, "--follow")
	}
	args = append(args, id.String())
	out, err := d.run(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	ch := make(chan LogEvent)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(bytes.NewReader(out.Stdout))
		for scanner.Scan() {
			ch <- LogEvent{Stream: LogStreamStdout, Line: append([]byte(nil), scanner.Bytes()...)}
		}
		scanner = bufio.NewScanner(bytes.NewReader(out.Stderr))
		for scanner.Scan() {
			ch <- LogEvent{Stream: LogStreamStderr, Line: append([]byte(nil), scanner.Bytes()...)}
		}
	}()
	return ch, nil
}

func (d *DockerRuntime) Remove(ctx context.Context, id ContainerID, opts RemoveOptions) error {
	args := []string{"rm"}
	if opts.Force {
		args = append(args, "--force")
	}
	if opts.Volumes {
		args = append(args, "--volumes")
	}
	args = append(args, id.String())
	_, err := d.run(ctx, args, nil)
	return err
}

func (d *DockerRuntime) run(ctx context.Context, args []string, stdin []byte) (ExecResult, error) {
	binary := d.Binary
	if binary == "" {
		binary = "docker"
	}
	runner := d.Runner
	if runner == nil {
		runner = shellRunner{}
	}
	return runner.Run(ctx, binary, args, stdin)
}

type shellRunner struct{}

func (shellRunner) Run(ctx context.Context, binary string, args []string, stdin []byte) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	} else if err != nil {
		result.ExitCode = -1
	} else {
		result.ExitCode = 0
	}
	if err != nil {
		return result, fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return result, nil
}

type dockerStats struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	PIDs     string `json:"PIDs"`
}

func appendSecurityArgs(args []string, profile SecurityProfile) []string {
	if profile.NoNewPrivileges {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	for _, capability := range profile.DropCapabilities {
		args = append(args, "--cap-drop", capability)
	}
	if profile.ReadOnlyRootFS {
		args = append(args, "--read-only")
	}
	if profile.RunAsNonRoot && profile.RunAsUser > 0 {
		user := strconv.Itoa(profile.RunAsUser)
		if profile.RunAsGroup > 0 {
			user += ":" + strconv.Itoa(profile.RunAsGroup)
		}
		args = append(args, "--user", user)
	}
	return args
}

func parseDockerPercent(raw string) float64 {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "%")
	value, _ := strconv.ParseFloat(raw, 64)
	return value
}

func parseDockerMemUsage(raw string) (uint64, uint64) {
	left, right, ok := strings.Cut(raw, "/")
	if !ok {
		return parseDockerBytes(raw), 0
	}
	return parseDockerBytes(left), parseDockerBytes(right)
}

func parseDockerBytes(raw string) uint64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	fields := strings.Fields(raw)
	if len(fields) == 2 {
		raw = fields[0] + fields[1]
	}
	unitStart := len(raw)
	for i, r := range raw {
		if (r < '0' || r > '9') && r != '.' {
			unitStart = i
			break
		}
	}
	number, _ := strconv.ParseFloat(raw[:unitStart], 64)
	unit := strings.ToLower(strings.TrimSpace(raw[unitStart:]))
	multiplier := float64(1)
	switch unit {
	case "kb", "kib":
		multiplier = 1024
	case "mb", "mib":
		multiplier = 1024 * 1024
	case "gb", "gib":
		multiplier = 1024 * 1024 * 1024
	}
	return uint64(number * multiplier)
}
