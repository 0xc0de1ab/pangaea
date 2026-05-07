package runtime

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CommandRunner interface {
	Run(context.Context, string, []string, []byte) (ExecResult, error)
}

type LogCommandRunner interface {
	RunLogs(context.Context, string, []string) (<-chan LogEvent, error)
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
		if isManagedPangaeaLabel(key) {
			continue
		}
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
	args = appendResourceArgs(args, spec.Resources)
	if err := prepareMountSources(spec.Mounts); err != nil {
		return "", err
	}
	args = appendMountArgs(args, spec.Mounts)
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

func prepareMountSources(mounts []MountSpec) error {
	for _, mount := range mounts {
		if strings.ToLower(strings.TrimSpace(mount.Type)) != "bind" || strings.TrimSpace(mount.Source) == "" {
			continue
		}
		if !mount.Directory {
			continue
		}
		mode := mount.DirectoryMode.Perm()
		if mode == 0 {
			mode = 0o700
		}
		if err := os.MkdirAll(mount.Source, mode); err != nil {
			return err
		}
		if mount.OwnerUID > 0 || mount.OwnerGID > 0 {
			uid := mount.OwnerUID
			if uid <= 0 {
				uid = -1
			}
			gid := mount.OwnerGID
			if gid <= 0 {
				gid = -1
			}
			if err := os.Chown(mount.Source, uid, gid); err != nil {
				if !os.IsPermission(err) {
					return err
				}
				// Rootless node-agents cannot chown bind sources for non-root
				// containers. Limit the fallback to this mount source so the
				// provider can still bootstrap copied auth/state files.
				if chmodErr := os.Chmod(mount.Source, 0o777); chmodErr != nil {
					return chmodErr
				}
				continue
			}
		}
		if err := os.Chmod(mount.Source, mode); err != nil {
			return err
		}
	}
	return nil
}

func appendMountArgs(args []string, mounts []MountSpec) []string {
	for _, mount := range mounts {
		switch strings.ToLower(strings.TrimSpace(mount.Type)) {
		case "bind":
			value := "type=bind,source=" + mount.Source + ",target=" + mount.Target
			if mount.ReadOnly {
				value += ",readonly"
			}
			args = append(args, "--mount", value)
		}
	}
	return args
}

func isManagedPangaeaLabel(key string) bool {
	switch key {
	case "pangaea.provider_id", "pangaea.provider_instance_id":
		return true
	default:
		return false
	}
}

func (d *DockerRuntime) Start(ctx context.Context, id ContainerID) error {
	_, err := d.run(ctx, []string{"start", id.String()}, nil)
	return err
}

func (d *DockerRuntime) FindByLabels(ctx context.Context, labels map[string]string) (ContainerStatus, bool, error) {
	args := []string{"ps", "-a", "--format", "{{json .}}"}
	filterCount := 0
	for key, value := range labels {
		if strings.TrimSpace(key) == "" {
			continue
		}
		filter := "label=" + key
		if value != "" {
			filter += "=" + value
		}
		args = append(args, "--filter", filter)
		filterCount++
	}
	if filterCount == 0 {
		return ContainerStatus{}, false, nil
	}
	out, err := d.run(ctx, args, nil)
	if err != nil {
		return ContainerStatus{}, false, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(out.Stdout))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var raw dockerPSContainer
		if err := json.Unmarshal(line, &raw); err != nil {
			return ContainerStatus{}, false, err
		}
		state := strings.ToLower(strings.TrimSpace(raw.State))
		if state == "" {
			state = dockerStateFromStatus(raw.Status)
		}
		return ContainerStatus{
			ID:    ContainerID(raw.ID),
			Image: ImageRef(raw.Image),
			Name:  raw.Names,
			State: state,
		}, true, nil
	}
	if err := scanner.Err(); err != nil {
		return ContainerStatus{}, false, err
	}
	return ContainerStatus{}, false, nil
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
	info, err := os.Stat(spec.HostPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: host_path must be a regular file", ErrInvalidCopySpec)
	}
	data, err := os.ReadFile(spec.HostPath)
	if err != nil {
		return err
	}
	containerDir := path.Dir(spec.ContainerPath)
	mkdirArgs := append([]string{"exec"}, dockerExecUserArgs(spec)...)
	mkdirArgs = append(mkdirArgs, id.String(), "mkdir", "-p", containerDir)
	if _, err := d.run(ctx, mkdirArgs, nil); err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if spec.FileMode != 0 {
		mode = spec.FileMode.Perm()
	}
	copyArgs := append([]string{"exec", "-i"}, dockerExecUserArgs(spec)...)
	copyArgs = append(copyArgs, id.String(), "sh", "-c", `cat > "$1" && chmod "$2" "$1"`, "sh", spec.ContainerPath, fmt.Sprintf("%04o", mode))
	_, err = d.run(ctx, copyArgs, data)
	return err
}

func dockerExecUserArgs(spec CopySpec) []string {
	if spec.OwnerUID <= 0 && spec.OwnerGID <= 0 {
		return nil
	}
	user := strconv.Itoa(spec.OwnerUID)
	if spec.OwnerGID > 0 {
		user += ":" + strconv.Itoa(spec.OwnerGID)
	}
	return []string{"--user", user}
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
	if spec.Follow {
		runner := d.Runner
		if runner == nil {
			runner = shellRunner{}
		}
		if logRunner, ok := runner.(LogCommandRunner); ok {
			return logRunner.RunLogs(ctx, d.binary(), args)
		}
	}
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

func dockerCopyArchive(spec CopySpec) ([]byte, error) {
	info, err := os.Stat(spec.HostPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: host_path must be a regular file", ErrInvalidCopySpec)
	}
	data, err := os.ReadFile(spec.HostPath)
	if err != nil {
		return nil, err
	}
	mode := info.Mode().Perm()
	if spec.FileMode != 0 {
		mode = spec.FileMode.Perm()
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	header := &tar.Header{
		Name:    path.Base(spec.ContainerPath),
		Mode:    int64(mode),
		Size:    int64(len(data)),
		ModTime: info.ModTime(),
		Uid:     spec.OwnerUID,
		Gid:     spec.OwnerGID,
	}
	if err := tw.WriteHeader(header); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (d *DockerRuntime) run(ctx context.Context, args []string, stdin []byte) (ExecResult, error) {
	runner := d.Runner
	if runner == nil {
		runner = shellRunner{}
	}
	return runner.Run(ctx, d.binary(), args, stdin)
}

func (d *DockerRuntime) binary() string {
	if d == nil || d.Binary == "" {
		return "docker"
	}
	return d.Binary
}

func appendResourceArgs(args []string, limits ResourceLimits) []string {
	if limits.CPUs != "" {
		args = append(args, "--cpus", limits.CPUs)
	}
	if limits.Memory != "" {
		args = append(args, "--memory", limits.Memory)
	}
	if limits.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(limits.PIDsLimit))
	}
	return args
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

func (shellRunner) RunLogs(ctx context.Context, binary string, args []string) (<-chan LogEvent, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan LogEvent)
	go func() {
		defer close(ch)
		var wg sync.WaitGroup
		wg.Add(2)
		go scanLogPipe(ctx, &wg, stdout, LogStreamStdout, ch)
		go scanLogPipe(ctx, &wg, stderr, LogStreamStderr, ch)
		wg.Wait()
		_ = cmd.Wait()
	}()
	return ch, nil
}

func scanLogPipe(ctx context.Context, wg *sync.WaitGroup, r io.Reader, stream LogStream, ch chan<- LogEvent) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		event := LogEvent{Stream: stream, Line: append([]byte(nil), scanner.Bytes()...)}
		select {
		case <-ctx.Done():
			return
		case ch <- event:
		}
	}
}

type dockerStats struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	PIDs     string `json:"PIDs"`
}

type dockerPSContainer struct {
	ID     string `json:"ID"`
	Image  string `json:"Image"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

func dockerStateFromStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.HasPrefix(status, "up"):
		return "running"
	case strings.HasPrefix(status, "created"):
		return "created"
	case strings.HasPrefix(status, "exited"):
		return "exited"
	case strings.HasPrefix(status, "paused"):
		return "paused"
	default:
		return status
	}
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
	for _, writablePath := range profile.WritablePaths {
		writablePath = strings.TrimSpace(writablePath)
		if writablePath == "" {
			continue
		}
		args = append(args, "--tmpfs", tmpfsMountSpec(writablePath, profile))
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

func tmpfsMountSpec(path string, profile SecurityProfile) string {
	if strings.Contains(path, ":") || !profile.RunAsNonRoot || profile.RunAsUser <= 0 {
		return path
	}
	mode := "0700"
	if path == "/tmp" {
		mode = "1777"
	}
	gid := profile.RunAsGroup
	if gid <= 0 {
		gid = profile.RunAsUser
	}
	return fmt.Sprintf("%s:uid=%d,gid=%d,mode=%s", path, profile.RunAsUser, gid, mode)
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
