package nodeagent

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/runtime"
)

const defaultContainerStopTimeout = 30 * time.Second

type ReconcileResult struct {
	ContainerID runtime.ContainerID     `json:"container_id"`
	Spec        runtime.ContainerSpec   `json:"spec"`
	Report      control.ContainerReport `json:"report"`
}

type ContainerSpecOptions struct {
	RouterControlURL string
	RouterDataURL    string
	StreamTokenKey   string
	RouterPeerToken  string
	ContainerKind    string
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
			if shouldRecreateExistingContainer(status, containerSpec) {
				if shouldPullProviderImage(spec) {
					if err := rt.Pull(ctx, containerSpec.Image); err != nil {
						return ReconcileResult{}, err
					}
				}
				if status.State == "running" {
					if err := rt.Stop(ctx, status.ID, defaultContainerStopTimeout); err != nil {
						return ReconcileResult{}, err
					}
				}
				if err := rt.Remove(ctx, status.ID, runtime.RemoveOptions{Force: true}); err != nil {
					return ReconcileResult{}, err
				}
				return createProviderContainer(ctx, rt, containerSpec, opts.ContainerKind)
			}
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
				ContainerKind:      opts.ContainerKind,
				ContainerName:      firstNonBlank(status.Name, containerSpec.Name),
				ProviderType:       containerSpec.ProviderType,
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
	if shouldPullProviderImage(spec) {
		if err := rt.Pull(ctx, containerSpec.Image); err != nil {
			return ReconcileResult{}, err
		}
	}
	return createProviderContainer(ctx, rt, containerSpec, opts.ContainerKind)
}

func shouldPullProviderImage(spec ProviderSpec) bool {
	return normalizedImagePullPolicy(spec.ImagePullPolicy) != "never"
}

func createProviderContainer(ctx context.Context, rt runtime.Runtime, containerSpec runtime.ContainerSpec, containerKind string) (ReconcileResult, error) {
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
		ContainerKind:      containerKind,
		ContainerName:      containerSpec.Name,
		ProviderType:       containerSpec.ProviderType,
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

func shouldRecreateExistingContainer(status runtime.ContainerStatus, spec runtime.ContainerSpec) bool {
	currentImage := strings.TrimSpace(status.Image.String())
	desiredImage := strings.TrimSpace(spec.Image.String())
	return currentImage != "" && desiredImage != "" && currentImage != desiredImage
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
		return runtime.ContainerSpec{}, fmt.Errorf("%w: provider %q image is required", ErrNodeAgentConfig, spec.ProviderType)
	}
	instanceID := spec.InstanceID
	if instanceID == "" {
		instanceID = spec.ProviderType + "-local"
	}
	if spec.HostName != "" {
		hostName = spec.HostName
	}
	containerName := defaultContainerName(spec.ProviderType, instanceID)
	labels := map[string]string{
		"pangaea.provider_type":        spec.ProviderType,
		"pangaea.provider_instance_id": instanceID,
		"pangaea.service":              string(spec.Service),
	}
	env := map[string]string{
		"PANGAEA_PROVIDER_TYPE":         spec.ProviderType,
		"PANGAEA_PROVIDER_INSTANCE_ID":  instanceID,
		"PANGAEA_NODE_ID":               nodeID,
		"PANGAEA_HOST_NAME":             hostName,
		"PANGAEA_CONTAINER_NAME":        containerName,
		"PANGAEA_RUNTIME_SETTINGS_PATH": "/var/lib/pangaea/runtime/provider.env",
		"PANGAEA_SERVICE":               string(spec.Service),
		"PANGAEA_SHIM_MODE":             string(spec.Kind),
	}
	if opts.ContainerKind != "" {
		env["PANGAEA_CONTAINER_KIND"] = opts.ContainerKind
	}
	if opts.RouterControlURL != "" {
		env["PANGAEA_ROUTER_CONTROL_URL"] = opts.RouterControlURL
	}
	if opts.RouterDataURL != "" {
		dataURL, err := routerDataURLForProvider(opts.RouterDataURL, instanceID)
		if err != nil {
			return runtime.ContainerSpec{}, err
		}
		env["PANGAEA_ROUTER_DATA_URL"] = dataURL
	}
	if opts.StreamTokenKey != "" {
		env["PANGAEA_STREAM_TOKEN_KEY"] = opts.StreamTokenKey
	}
	if opts.RouterPeerToken != "" {
		env["PANGAEA_ROUTER_PEER_TOKEN"] = opts.RouterPeerToken
	}
	if spec.AccountHint != "" {
		env["PANGAEA_ACCOUNT_DISPLAY"] = spec.AccountHint
	}
	if protocols := joinStringList(spec.Shim.Protocols); protocols != "" {
		env["PANGAEA_SHIM_PROTOCOLS"] = protocols
	}
	if capabilities := joinCapabilityList(spec.Shim.Capabilities); capabilities != "" {
		env["PANGAEA_SHIM_CAPABILITIES"] = capabilities
	}
	if spec.ProviderMode != "" {
		env["PANGAEA_PROVIDER_MODE"] = spec.ProviderMode
	}
	if spec.Upstream.BaseURL != "" {
		env["PANGAEA_UPSTREAM_BASE_URL"] = spec.Upstream.BaseURL
	}
	if spec.Upstream.APIKey != "" {
		env["PANGAEA_UPSTREAM_API_KEY"] = spec.Upstream.APIKey
	}
	if spec.Upstream.APIKeyFile != "" {
		env["PANGAEA_UPSTREAM_API_KEY_FILE"] = spec.Upstream.APIKeyFile
	}
	if spec.Upstream.APIKeyMode != "" {
		env["PANGAEA_UPSTREAM_API_KEY_MODE"] = spec.Upstream.APIKeyMode
	}
	if spec.Upstream.APIKeyHeader != "" {
		env["PANGAEA_UPSTREAM_API_KEY_HEADER"] = spec.Upstream.APIKeyHeader
	}
	if spec.Upstream.APIKeyQueryParam != "" {
		env["PANGAEA_UPSTREAM_API_KEY_QUERY_PARAM"] = spec.Upstream.APIKeyQueryParam
	}
	if dialect := providerDialect(spec); dialect != "" {
		env["PANGAEA_UPSTREAM_DIALECT"] = dialect
	}
	if len(spec.Models) > 0 && shouldEmitDefaultModelEnv(spec) {
		env["PANGAEA_MODEL"] = spec.Models[0].ID
		if models := providerModelEnv(spec); models != "" {
			env["PANGAEA_MODELS"] = models
		}
		if len(spec.Models[0].Aliases) > 0 {
			env["PANGAEA_MODEL_ALIAS"] = spec.Models[0].Aliases[0]
		}
		if capabilities := joinCapabilityList(spec.Models[0].Capabilities); capabilities != "" {
			env["PANGAEA_MODEL_CAPABILITIES"] = capabilities
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
	for key, value := range spec.Env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env[key] = value
	}
	containerSpec := runtime.ContainerSpec{
		ProviderType:       spec.ProviderType,
		ProviderInstanceID: instanceID,
		NodeID:             nodeID,
		HostName:           hostName,
		Name:               containerName,
		Image:              runtime.ImageRef(spec.Image),
		Entrypoint:         append([]string(nil), spec.Shim.Entrypoint...),
		Command:            append([]string(nil), spec.Shim.Command...),
		Env:                env,
		Labels:             labels,
		NetworkMode:        normalizedNetworkMode(spec.NetworkMode),
		WorkingDir:         strings.TrimSpace(spec.Shim.WorkingDir),
		Security:           runtime.DefaultSecurityProfile(),
		Resources: runtime.ResourceLimits{
			CPUs:      strings.TrimSpace(spec.Resources.CPUs),
			Memory:    strings.TrimSpace(spec.Resources.Memory),
			PIDsLimit: spec.Resources.PidsLimit,
		},
	}
	containerSpec.Mounts = persistentStorageMounts(spec)
	if len(containerSpec.Mounts) > 0 {
		env["PANGAEA_STORAGE_MODE"] = "persistent"
		env["PANGAEA_PROVIDER_STATE_DIR"] = "/var/lib/pangaea"
		containerSpec.Security.WritablePaths = writablePathsWithoutMountTargets(containerSpec.Security.WritablePaths, containerSpec.Mounts)
	} else if normalizedStorageMode(spec.Storage.Mode) != "" {
		env["PANGAEA_STORAGE_MODE"] = normalizedStorageMode(spec.Storage.Mode)
	}
	if spec.Auth.Mode == "file" || apiKeyAuthCopyConfigured(spec.Auth) {
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
		switch spec.Auth.Mode {
		case "file":
			env["PANGAEA_AUTH_PATH"] = spec.Auth.ContainerPath
			env["PANGAEA_AUTH_DIR"] = filepath.Dir(spec.Auth.ContainerPath)
			if spec.Auth.Format != "" {
				env["PANGAEA_AUTH_FORMAT"] = spec.Auth.Format
			}
		case "api_key":
			if strings.TrimSpace(env["PANGAEA_UPSTREAM_API_KEY_FILE"]) == "" {
				env["PANGAEA_UPSTREAM_API_KEY_FILE"] = spec.Auth.ContainerPath
			}
			env["PANGAEA_AUTH_DIR"] = filepath.Dir(spec.Auth.ContainerPath)
		}
	}
	if err := containerSpec.Validate(); err != nil {
		return runtime.ContainerSpec{}, err
	}
	return containerSpec, nil
}

func shouldEmitDefaultModelEnv(spec ProviderSpec) bool {
	return !(spec.Service == provider.ServiceGitHubCopilot && spec.ProviderMode == "sdk")
}

func providerModelEnv(spec ProviderSpec) string {
	if spec.Service == provider.ServiceGitHubCopilot && spec.ProviderMode == "sdk" {
		return ""
	}
	items := make([]string, 0, len(spec.Models))
	for _, model := range spec.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		item := id
		aliases := make([]string, 0, len(model.Aliases))
		for _, alias := range model.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" && alias != id {
				aliases = append(aliases, alias)
			}
		}
		if len(aliases) > 0 {
			item += "=" + strings.Join(aliases, "|")
		}
		items = append(items, item)
	}
	return strings.Join(items, ",")
}

func apiKeyAuthCopyConfigured(auth AuthSpec) bool {
	return auth.Mode == "api_key" && strings.TrimSpace(auth.HostPath) != "" && strings.TrimSpace(auth.ContainerPath) != ""
}

func persistentStorageMounts(spec ProviderSpec) []runtime.MountSpec {
	if normalizedStorageMode(spec.Storage.Mode) != "persistent" {
		return nil
	}
	root := strings.TrimSpace(spec.Storage.HostPath)
	if root == "" {
		return nil
	}
	paths := spec.Storage.ContainerPaths
	if len(paths) == 0 {
		paths = []string{"/var/lib/pangaea", "/work"}
	}
	mounts := make([]runtime.MountSpec, 0, len(paths))
	for _, containerPath := range paths {
		containerPath = strings.TrimSpace(containerPath)
		if containerPath == "" {
			continue
		}
		hostPath := filepath.Join(root, storagePathToken(containerPath))
		mounts = append(mounts, runtime.MountSpec{
			Type:          "bind",
			Source:        hostPath,
			Target:        containerPath,
			Directory:     true,
			OwnerUID:      runtime.DefaultSecurityProfile().RunAsUser,
			OwnerGID:      runtime.DefaultSecurityProfile().RunAsGroup,
			DirectoryMode: 0o700,
		})
	}
	return mounts
}

func storagePathToken(containerPath string) string {
	token := strings.Trim(strings.TrimSpace(containerPath), "/")
	if token == "" {
		token = "root"
	}
	token = strings.ReplaceAll(token, "/", "-")
	return sanitizeContainerToken(token)
}

func writablePathsWithoutMountTargets(paths []string, mounts []runtime.MountSpec) []string {
	targets := make(map[string]struct{}, len(mounts))
	for _, mount := range mounts {
		target := strings.TrimSpace(mount.Target)
		if target != "" {
			targets[target] = struct{}{}
		}
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := targets[strings.TrimSpace(path)]; ok {
			continue
		}
		out = append(out, path)
	}
	return out
}

func routerDataURLForProvider(raw string, providerInstanceID string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: invalid router data url %q: %v", ErrNodeAgentConfig, raw, err)
	}
	q := u.Query()
	if strings.TrimSpace(q.Get("provider_instance_id")) == "" {
		q.Set("provider_instance_id", providerInstanceID)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
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

func joinStringList(items []string) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return strings.Join(out, ",")
}

func joinCapabilityList(capabilities []provider.Capability) string {
	out := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability != "" {
			out = append(out, string(capability))
		}
	}
	return strings.Join(out, ",")
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

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func defaultContainerName(providerType string, instanceID string) string {
	return "pangaea-" + sanitizeContainerToken(providerType) + "-" + sanitizeContainerToken(instanceID)
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
