// Package runtime defines the container runtime contract used by node-agent.
package runtime

import (
	"context"
	"io/fs"
	"time"
)

type Runtime interface {
	Info(context.Context) (RuntimeInfo, error)
	Pull(context.Context, ImageRef) error
	Create(context.Context, ContainerSpec) (ContainerID, error)
	Start(context.Context, ContainerID) error
	Stop(context.Context, ContainerID, time.Duration) error
	Exec(context.Context, ContainerID, ExecSpec) (ExecResult, error)
	CopyTo(context.Context, ContainerID, CopySpec) error
	CopyFrom(context.Context, ContainerID, CopySpec) error
	Stats(context.Context, ContainerID) (Stats, error)
	Logs(context.Context, ContainerID, LogSpec) (<-chan LogEvent, error)
	Remove(context.Context, ContainerID, RemoveOptions) error
}

type RuntimeInfo struct {
	Kind         string   `json:"kind"`
	Version      string   `json:"version,omitempty"`
	Rootless     bool     `json:"rootless"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type ContainerID string

func (id ContainerID) String() string {
	return string(id)
}

type ContainerStatus struct {
	ID     ContainerID       `json:"id"`
	Image  ImageRef          `json:"image,omitempty"`
	Name   string            `json:"name,omitempty"`
	State  string            `json:"state,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type ImageRef string

func (ref ImageRef) String() string {
	return string(ref)
}

type ContainerSpec struct {
	ProviderID         string            `json:"provider_id"`
	ProviderInstanceID string            `json:"provider_instance_id,omitempty"`
	NodeID             string            `json:"node_id,omitempty"`
	HostName           string            `json:"host_name,omitempty"`
	Name               string            `json:"name,omitempty"`
	Image              ImageRef          `json:"image"`
	Entrypoint         []string          `json:"entrypoint,omitempty"`
	Command            []string          `json:"command,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	NetworkMode        string            `json:"network_mode,omitempty"`
	WorkingDir         string            `json:"working_dir,omitempty"`
	AuthCopy           *CopySpec         `json:"auth_copy,omitempty"`
	Mounts             []MountSpec       `json:"mounts,omitempty"`
	Security           SecurityProfile   `json:"security,omitempty"`
	Resources          ResourceLimits    `json:"resources,omitempty"`
}

type CopySpec struct {
	HostPath      string      `json:"host_path"`
	ContainerPath string      `json:"container_path"`
	OwnerUID      int         `json:"owner_uid,omitempty"`
	OwnerGID      int         `json:"owner_gid,omitempty"`
	FileMode      fs.FileMode `json:"file_mode,omitempty"`
}

type MountSpec struct {
	Type          string      `json:"type"`
	Source        string      `json:"source,omitempty"`
	Target        string      `json:"target"`
	ReadOnly      bool        `json:"read_only,omitempty"`
	Directory     bool        `json:"directory,omitempty"`
	OwnerUID      int         `json:"owner_uid,omitempty"`
	OwnerGID      int         `json:"owner_gid,omitempty"`
	DirectoryMode fs.FileMode `json:"directory_mode,omitempty"`
}

type ResourceLimits struct {
	CPUs      string `json:"cpus,omitempty"`
	Memory    string `json:"memory,omitempty"`
	PIDsLimit int    `json:"pids_limit,omitempty"`
}

type ExecSpec struct {
	Command    []string          `json:"command"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	User       string            `json:"user,omitempty"`
	TTY        bool              `json:"tty,omitempty"`
}

type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   []byte `json:"stdout,omitempty"`
	Stderr   []byte `json:"stderr,omitempty"`
}

type Stats struct {
	ObservedAt       time.Time `json:"observed_at,omitempty"`
	CPUUsageNanos    uint64    `json:"cpu_usage_nanos,omitempty"`
	CPUPercent       float64   `json:"cpu_percent,omitempty"`
	MemoryBytes      uint64    `json:"memory_bytes,omitempty"`
	MemoryLimitBytes uint64    `json:"memory_limit_bytes,omitempty"`
	MemoryPeakBytes  uint64    `json:"memory_peak_bytes,omitempty"`
	OOMCount         uint64    `json:"oom_count,omitempty"`
	PIDs             uint64    `json:"pids,omitempty"`
	RestartCount     uint64    `json:"restart_count,omitempty"`
}

type LogSpec struct {
	Since  time.Time `json:"since,omitempty"`
	Tail   int       `json:"tail,omitempty"`
	Follow bool      `json:"follow,omitempty"`
	Stdout bool      `json:"stdout,omitempty"`
	Stderr bool      `json:"stderr,omitempty"`
}

type LogStream string

const (
	LogStreamStdout LogStream = "stdout"
	LogStreamStderr LogStream = "stderr"
)

type LogEvent struct {
	Time   time.Time `json:"time,omitempty"`
	Stream LogStream `json:"stream"`
	Line   []byte    `json:"line"`
}

type RemoveOptions struct {
	Force   bool `json:"force,omitempty"`
	Volumes bool `json:"volumes,omitempty"`
}

type SecurityProfile struct {
	RunAsNonRoot     bool     `json:"run_as_non_root"`
	RunAsUser        int      `json:"run_as_user,omitempty"`
	RunAsGroup       int      `json:"run_as_group,omitempty"`
	NoNewPrivileges  bool     `json:"no_new_privileges"`
	DropCapabilities []string `json:"drop_capabilities,omitempty"`
	ReadOnlyRootFS   bool     `json:"read_only_rootfs"`
	WritablePaths    []string `json:"writable_paths,omitempty"`
	SeccompProfile   string   `json:"seccomp_profile,omitempty"`
	AppArmorProfile  string   `json:"apparmor_profile,omitempty"`
	SELinuxLabel     string   `json:"selinux_label,omitempty"`
}

func DefaultSecurityProfile() SecurityProfile {
	return SecurityProfile{
		RunAsNonRoot:     true,
		RunAsUser:        10001,
		RunAsGroup:       10001,
		NoNewPrivileges:  true,
		DropCapabilities: []string{"ALL"},
		ReadOnlyRootFS:   true,
		WritablePaths: []string{
			"/var/lib/pangaea",
			"/run/pangaea",
			"/tmp",
			"/work",
		},
	}
}
