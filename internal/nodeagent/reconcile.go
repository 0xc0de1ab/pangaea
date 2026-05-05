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

func ReconcileProviderContainer(ctx context.Context, rt runtime.Runtime, spec ProviderSpec, nodeID string, hostName string) (ReconcileResult, error) {
	if rt == nil {
		return ReconcileResult{}, fmt.Errorf("%w: runtime is required", ErrNodeAgentConfig)
	}
	containerSpec, err := ContainerSpecFromProviderSpec(spec, nodeID, hostName)
	if err != nil {
		return ReconcileResult{}, err
	}
	if err := rt.Pull(ctx, containerSpec.Image); err != nil {
		return ReconcileResult{}, err
	}
	containerID, err := rt.Create(ctx, containerSpec)
	if err != nil {
		return ReconcileResult{}, err
	}
	if containerSpec.AuthCopy != nil {
		if err := rt.CopyTo(ctx, containerID, *containerSpec.AuthCopy); err != nil {
			return ReconcileResult{}, err
		}
	}
	if err := rt.Start(ctx, containerID); err != nil {
		return ReconcileResult{}, err
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
	return ReconcileResult{ContainerID: containerID, Spec: containerSpec, Report: report}, nil
}

func ContainerSpecFromProviderSpec(spec ProviderSpec, nodeID string, hostName string) (runtime.ContainerSpec, error) {
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
	}
	if err := containerSpec.Validate(); err != nil {
		return runtime.ContainerSpec{}, err
	}
	return containerSpec, nil
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
