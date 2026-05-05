package nodeagent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/runtime"
)

type ReconcileResult struct {
	ContainerID runtime.ContainerID     `json:"container_id"`
	Spec        runtime.ContainerSpec   `json:"spec"`
	Report      control.ContainerReport `json:"report"`
}

type ContainerSpecOptions struct {
	RouterControlURL string
	RouterDataURL    string
	StreamTokenKey   string
}

type containerFinder interface {
	FindByLabels(context.Context, map[string]string) (runtime.ContainerStatus, bool, error)
}

func ReconcileProviderContainer(ctx context.Context, rt runtime.Runtime, spec ProviderSpec, nodeID string, hostName string) (ReconcileResult, error) {
	return ReconcileProviderContainerWithOptions(ctx, rt, spec, nodeID, hostName, ContainerSpecOptions{})
}

func ReconcileProviderContainerWithOptions(ctx context.Context, rt runtime.Runtime, spec ProviderSpec, nodeID string, hostName string, opts ContainerSpecOptions) (ReconcileResult, error) {
	if rt == nil {
		return ReconcileResult{}, fmt.Errorf("%w: runtime is required", ErrNodeAgentConfig)
	}
	containerSpec, err := ContainerSpecFromProviderSpecWithOptions(spec, nodeID, hostName, opts)
	if err != nil {
		return ReconcileResult{}, err
	}
	if finder, ok := rt.(containerFinder); ok {
		status, found, err := finder.FindByLabels(ctx, containerSpec.Labels)
		if err != nil {
			return ReconcileResult{}, err
		}
		if found {
			state := status.State
			if state != "running" {
				if err := rt.Start(ctx, status.ID); err != nil {
					return ReconcileResult{}, err
				}
				state = "running"
			}
			if containerSpec.AuthCopy != nil {
				if spec.Auth.Sync.ContainerToHost {
					if err := rt.CopyFrom(ctx, status.ID, *containerSpec.AuthCopy); err != nil {
						return ReconcileResult{}, err
					}
				}
				if shouldSyncHostToContainerOnReconcile(spec.Auth.Sync.HostToContainer) {
					if err := rt.CopyTo(ctx, status.ID, *containerSpec.AuthCopy); err != nil {
						return ReconcileResult{}, err
					}
				}
			}
			now := time.Now().UTC()
			report := control.ContainerReport{
				ContainerID:        status.ID.String(),
				ProviderID:         containerSpec.ProviderID,
				ProviderInstanceID: containerSpec.ProviderInstanceID,
				Image:              status.Image.String(),
				State:              state,
				Health:             control.HealthReport{Status: "ready", CheckedAt: now},
				Labels:             cloneRuntimeLabels(containerSpec.Labels),
				StartedAt:          now,
			}
			if report.Image == "" {
				report.Image = containerSpec.Image.String()
			}
			populateContainerStats(ctx, rt, status.ID, &report)
			return ReconcileResult{ContainerID: status.ID, Spec: containerSpec, Report: report}, nil
		}
	}
	if err := rt.Pull(ctx, containerSpec.Image); err != nil {
		return ReconcileResult{}, err
	}
	containerID, err := rt.Create(ctx, containerSpec)
	if err != nil {
		return ReconcileResult{}, err
	}
	if err := rt.Start(ctx, containerID); err != nil {
		return ReconcileResult{}, err
	}
	if containerSpec.AuthCopy != nil {
		if err := rt.CopyTo(ctx, containerID, *containerSpec.AuthCopy); err != nil {
			return ReconcileResult{}, err
		}
	}
	now := time.Now().UTC()
	report := control.ContainerReport{
		ContainerID:        containerID.String(),
		ProviderID:         containerSpec.ProviderID,
		ProviderInstanceID: containerSpec.ProviderInstanceID,
		Image:              containerSpec.Image.String(),
		State:              "running",
		Health:             control.HealthReport{Status: "starting", CheckedAt: now},
		Labels:             cloneRuntimeLabels(containerSpec.Labels),
		StartedAt:          now,
	}
	populateContainerStats(ctx, rt, containerID, &report)
	return ReconcileResult{ContainerID: containerID, Spec: containerSpec, Report: report}, nil
}

func populateContainerStats(ctx context.Context, rt runtime.Runtime, id runtime.ContainerID, report *control.ContainerReport) {
	stats, err := rt.Stats(ctx, id)
	if err != nil {
		if report.Extensions == nil {
			report.Extensions = map[string]any{}
		}
		report.Extensions["stats_error"] = err.Error()
		return
	}
	report.Resources = control.ResourceUsage{
		CPUPercent:      stats.CPUPercent,
		MemoryBytes:     uint64ToInt64(stats.MemoryBytes),
		MemoryPeakBytes: uint64ToInt64(stats.MemoryPeakBytes),
		OOMCount:        uint64ToInt64(stats.OOMCount),
	}
}

func uint64ToInt64(value uint64) int64 {
	if value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(value)
}

func ContainerSpecFromProviderSpec(spec ProviderSpec, nodeID string, hostName string) (runtime.ContainerSpec, error) {
	return ContainerSpecFromProviderSpecWithOptions(spec, nodeID, hostName, ContainerSpecOptions{})
}

func ContainerSpecFromProviderSpecWithOptions(spec ProviderSpec, nodeID string, hostName string, opts ContainerSpecOptions) (runtime.ContainerSpec, error) {
	if err := spec.Validate(); err != nil {
		return runtime.ContainerSpec{}, err
	}
	if strings.TrimSpace(spec.Image) == "" {
		return runtime.ContainerSpec{}, fmt.Errorf("%w: provider %q image is required", ErrNodeAgentConfig, spec.ID)
	}
	instanceID := spec.InstanceID
	if instanceID == "" {
		instanceID = spec.ID + "-local"
	}
	if spec.HostName != "" {
		hostName = spec.HostName
	}
	labels := map[string]string{
		"pangaea.provider_id":          spec.ID,
		"pangaea.provider_instance_id": instanceID,
		"pangaea.service":              string(spec.Service),
	}
	env := map[string]string{
		"PANGAEA_PROVIDER_ID":          spec.ID,
		"PANGAEA_PROVIDER_INSTANCE_ID": instanceID,
		"PANGAEA_NODE_ID":              nodeID,
		"PANGAEA_HOST_NAME":            hostName,
		"PANGAEA_SERVICE":              string(spec.Service),
		"PANGAEA_SHIM_MODE":            string(spec.Kind),
	}
	if opts.RouterControlURL != "" {
		env["PANGAEA_ROUTER_CONTROL_URL"] = opts.RouterControlURL
	}
	if opts.RouterDataURL != "" {
		env["PANGAEA_ROUTER_DATA_URL"] = opts.RouterDataURL
	}
	if opts.StreamTokenKey != "" {
		env["PANGAEA_STREAM_TOKEN_KEY"] = opts.StreamTokenKey
	}
	if spec.AccountHint != "" {
		env["PANGAEA_ACCOUNT_DISPLAY"] = spec.AccountHint
	}
	if spec.Upstream.BaseURL != "" {
		env["PANGAEA_UPSTREAM_BASE_URL"] = spec.Upstream.BaseURL
	}
	if dialect := providerDialect(spec); dialect != "" {
		env["PANGAEA_UPSTREAM_DIALECT"] = dialect
	}
	if len(spec.Models) > 0 {
		env["PANGAEA_MODEL"] = spec.Models[0].ID
		if len(spec.Models[0].Aliases) > 0 {
			env["PANGAEA_MODEL_ALIAS"] = spec.Models[0].Aliases[0]
		}
	}
	if len(spec.Refresh.Command) > 0 {
		env["PANGAEA_REFRESH_COMMAND"] = shellJoin(spec.Refresh.Command)
	}
	if spec.Refresh.Threshold != "" {
		env["PANGAEA_REFRESH_THRESHOLD"] = spec.Refresh.Threshold
	}
	if spec.Refresh.Cooldown != "" {
		env["PANGAEA_REFRESH_COOLDOWN"] = spec.Refresh.Cooldown
	}
	if spec.Refresh.Timeout != "" {
		env["PANGAEA_REFRESH_TIMEOUT"] = spec.Refresh.Timeout
	}
	containerSpec := runtime.ContainerSpec{
		ProviderID:         spec.ID,
		ProviderInstanceID: instanceID,
		NodeID:             nodeID,
		HostName:           hostName,
		Name:               defaultContainerName(spec.ID, instanceID),
		Image:              runtime.ImageRef(spec.Image),
		Env:                env,
		Labels:             labels,
		Security:           runtime.DefaultSecurityProfile(),
	}
	if spec.Auth.Mode == "file" {
		perm, err := spec.Auth.FilePerm()
		if err != nil {
			return runtime.ContainerSpec{}, err
		}
		copySpec := runtime.CopySpec{
			HostPath:      spec.Auth.HostPath,
			ContainerPath: spec.Auth.ContainerPath,
			FileMode:      perm,
		}
		if spec.Auth.OwnerUID != nil {
			copySpec.OwnerUID = *spec.Auth.OwnerUID
		}
		if spec.Auth.OwnerGID != nil {
			copySpec.OwnerGID = *spec.Auth.OwnerGID
		}
		containerSpec.AuthCopy = &copySpec
		env["PANGAEA_AUTH_PATH"] = spec.Auth.ContainerPath
		env["PANGAEA_AUTH_DIR"] = filepath.Dir(spec.Auth.ContainerPath)
		if spec.Auth.Format != "" {
			env["PANGAEA_AUTH_FORMAT"] = spec.Auth.Format
		}
	}
	if err := containerSpec.Validate(); err != nil {
		return runtime.ContainerSpec{}, err
	}
	return containerSpec, nil
}

func providerDialect(spec ProviderSpec) string {
	if spec.Upstream.Compat != "" {
		return spec.Upstream.Compat
	}
	for _, protocol := range spec.Shim.Protocols {
		protocol = strings.TrimSpace(protocol)
		if protocol != "" {
			return protocol
		}
	}
	return ""
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shouldSyncHostToContainerOnReconcile(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "always", "reconcile":
		return true
	default:
		return false
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func defaultContainerName(providerID string, instanceID string) string {
	return "pangaea-" + sanitizeContainerToken(providerID) + "-" + sanitizeContainerToken(instanceID)
}

func sanitizeContainerToken(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(raw) {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "provider"
	}
	return out
}

func cloneRuntimeLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
