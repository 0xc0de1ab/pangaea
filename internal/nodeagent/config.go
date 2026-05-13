package nodeagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"gopkg.in/yaml.v3"
)

const ConfigVersion = "node-agent/v1"

const (
	codexDefaultAuthHostPathRelative = "assets/.codex/auth.json"
	codexDefaultAuthContainerPath    = "/var/lib/pangaea/auth/codex/auth.json"
	codexDefaultAuthFormat           = "codex-auth-json-format"
)

type Config struct {
	Version   string         `json:"version" yaml:"version"`
	Node      NodeConfig     `json:"node,omitempty" yaml:"node,omitempty"`
	Runtime   RuntimeConfig  `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Providers []ProviderSpec `json:"providers,omitempty" yaml:"providers,omitempty"`
}

type NodeConfig struct {
	ID           string   `json:"id,omitempty" yaml:"id,omitempty"`
	HostName     string   `json:"host_name,omitempty" yaml:"host_name,omitempty"`
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

type RuntimeConfig struct {
	Kind     string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version  string `json:"version,omitempty" yaml:"version,omitempty"`
	Rootless bool   `json:"rootless,omitempty" yaml:"rootless,omitempty"`
}

type ProviderSpec struct {
	ProviderType    string            `json:"provider_type" yaml:"provider_type"`
	InstanceID      string            `json:"instance_id,omitempty" yaml:"instance_id,omitempty"`
	Kind            provider.Kind     `json:"kind" yaml:"kind"`
	ProviderMode    string            `json:"provider_mode,omitempty" yaml:"provider_mode,omitempty"`
	Image           string            `json:"image,omitempty" yaml:"image,omitempty"`
	ImagePullPolicy string            `json:"image_pull_policy,omitempty" yaml:"image_pull_policy,omitempty"`
	NetworkMode     string            `json:"network_mode,omitempty" yaml:"network_mode,omitempty"`
	HostName        string            `json:"host_name,omitempty" yaml:"host_name,omitempty"`
	AccountHint     string            `json:"account_hint,omitempty" yaml:"account_hint,omitempty"`
	Service         provider.Service  `json:"service" yaml:"service"`
	Models          []provider.Model  `json:"models,omitempty" yaml:"models,omitempty"`
	Env             map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Auth            AuthSpec          `json:"auth,omitempty" yaml:"auth,omitempty"`
	Refresh         RefreshSpec       `json:"refresh,omitempty" yaml:"refresh,omitempty"`
	Shim            ShimSpec          `json:"shim,omitempty" yaml:"shim,omitempty"`
	Storage         StorageSpec       `json:"storage,omitempty" yaml:"storage,omitempty"`
	Resources       ResourceSpec      `json:"resources,omitempty" yaml:"resources,omitempty"`
	Upstream        UpstreamSpec      `json:"upstream,omitempty" yaml:"upstream,omitempty"`
}

type AuthSpec struct {
	Mode          string       `json:"mode,omitempty" yaml:"mode,omitempty"`
	Format        string       `json:"format,omitempty" yaml:"format,omitempty"`
	Bootstrap     string       `json:"bootstrap,omitempty" yaml:"bootstrap,omitempty"`
	HostPath      string       `json:"host_path,omitempty" yaml:"host_path,omitempty"`
	ContainerPath string       `json:"container_path,omitempty" yaml:"container_path,omitempty"`
	OwnerUID      *int         `json:"owner_uid,omitempty" yaml:"owner_uid,omitempty"`
	OwnerGID      *int         `json:"owner_gid,omitempty" yaml:"owner_gid,omitempty"`
	FileMode      string       `json:"file_mode,omitempty" yaml:"file_mode,omitempty"`
	Sync          AuthSyncSpec `json:"sync,omitempty" yaml:"sync,omitempty"`
}

type AuthSyncSpec struct {
	ContainerToHost bool   `json:"container_to_host,omitempty" yaml:"container_to_host,omitempty"`
	HostToContainer string `json:"host_to_container,omitempty" yaml:"host_to_container,omitempty"`
}

type RefreshSpec struct {
	Threshold string   `json:"threshold,omitempty" yaml:"threshold,omitempty"`
	Command   []string `json:"command,omitempty" yaml:"command,omitempty"`
	Cooldown  string   `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`
	Timeout   string   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

type ShimSpec struct {
	Listen       string                `json:"listen,omitempty" yaml:"listen,omitempty"`
	Protocols    []string              `json:"protocols,omitempty" yaml:"protocols,omitempty"`
	Capabilities []provider.Capability `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Entrypoint   []string              `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	Command      []string              `json:"command,omitempty" yaml:"command,omitempty"`
	WorkingDir   string                `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
}

type ResourceSpec struct {
	CPUs      string `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Memory    string `json:"memory,omitempty" yaml:"memory,omitempty"`
	PidsLimit int    `json:"pids_limit,omitempty" yaml:"pids_limit,omitempty"`
}

type StorageSpec struct {
	Mode           string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	HostPath       string   `json:"host_path,omitempty" yaml:"host_path,omitempty"`
	ContainerPaths []string `json:"container_paths,omitempty" yaml:"container_paths,omitempty"`
}

type UpstreamSpec struct {
	BaseURL          string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Compat           string `json:"compat,omitempty" yaml:"compat,omitempty"`
	APIKey           string `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	APIKeyFile       string `json:"api_key_file,omitempty" yaml:"api_key_file,omitempty"`
	APIKeyMode       string `json:"api_key_mode,omitempty" yaml:"api_key_mode,omitempty"`
	APIKeyHeader     string `json:"api_key_header,omitempty" yaml:"api_key_header,omitempty"`
	APIKeyQueryParam string `json:"api_key_query_param,omitempty" yaml:"api_key_query_param,omitempty"`
}

func LoadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	baseDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, err
	}
	if err := applyConfigLoadDefaults(&cfg, baseDir); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyConfigLoadDefaults(cfg *Config, baseDir string) error {
	for i := range cfg.Providers {
		spec := &cfg.Providers[i]
		auth := &spec.Auth
		if auth.HostPath != "" {
			expanded, err := config.ExpandPathFromDir(baseDir, auth.HostPath)
			if err != nil {
				return err
			}
			auth.HostPath = expanded
		}
		if auth.ContainerPath != "" {
			expanded, err := config.ExpandPath(auth.ContainerPath)
			if err != nil {
				return err
			}
			auth.ContainerPath = expanded
		}
		if spec.Storage.HostPath != "" {
			expanded, err := config.ExpandPathFromDir(baseDir, spec.Storage.HostPath)
			if err != nil {
				return err
			}
			spec.Storage.HostPath = expanded
		}
		if spec.Service == provider.ServiceCodex && auth.Mode == "file" {
			if auth.HostPath == "" {
				resolved, err := DefaultCodexAuthHostPath(baseDir)
				if err != nil {
					return fmt.Errorf("%w: provider %q %v", ErrNodeAgentConfig, spec.ProviderType, err)
				}
				auth.HostPath = resolved
			}
			if auth.ContainerPath == "" {
				auth.ContainerPath = codexDefaultAuthContainerPath
			}
			if auth.Format == "" {
				auth.Format = codexDefaultAuthFormat
			}
		}
	}
	return nil
}

func ParseConfigYAML(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func DefaultCodexAuthHostPath(baseDir string) (string, error) {
	candidates := CodexAuthHostPathCandidates(baseDir)
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return candidate, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("stat codex auth candidate %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("codex auth file not found; checked %s", strings.Join(candidates, ", "))
}

func CodexAuthHostPathCandidates(baseDir string) []string {
	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "." || path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		candidates = append(candidates, path)
	}
	if baseDir != "" {
		if abs, err := filepath.Abs(baseDir); err == nil {
			for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
				add(filepath.Join(dir, codexDefaultAuthHostPathRelative))
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".codex", "auth.json"))
	}
	return candidates
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Version) == "" {
		return fmt.Errorf("%w: missing version", ErrNodeAgentConfig)
	}
	if c.Version != ConfigVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrNodeAgentConfig, c.Version)
	}
	seenInstances := make(map[string]struct{}, len(c.Providers))
	for _, spec := range c.Providers {
		if err := spec.Validate(); err != nil {
			return err
		}
		instanceID := spec.InstanceID
		if instanceID == "" {
			instanceID = spec.ProviderType + "-local"
		}
		if _, ok := seenInstances[instanceID]; ok {
			return fmt.Errorf("%w: duplicate provider instance_id %q", ErrNodeAgentConfig, instanceID)
		}
		seenInstances[instanceID] = struct{}{}
	}
	return nil
}

func (p ProviderSpec) Validate() error {
	if strings.TrimSpace(p.ProviderType) == "" {
		return fmt.Errorf("%w: provider type is required", ErrNodeAgentConfig)
	}
	if p.Kind == "" {
		return fmt.Errorf("%w: provider %q kind is required", ErrNodeAgentConfig, p.ProviderType)
	}
	if !validProviderKind(p.Kind) {
		return fmt.Errorf("%w: provider %q kind %q is invalid", ErrNodeAgentConfig, p.ProviderType, p.Kind)
	}
	if p.Service == "" {
		return fmt.Errorf("%w: provider %q service is required", ErrNodeAgentConfig, p.ProviderType)
	}
	if err := validateImagePullPolicy(p.ProviderType, p.ImagePullPolicy); err != nil {
		return err
	}
	if err := validateNetworkMode(p.ProviderType, p.NetworkMode); err != nil {
		return err
	}
	if len(p.Shim.Capabilities) == 0 {
		return fmt.Errorf("%w: provider %q shim.capabilities is required", ErrNodeAgentConfig, p.ProviderType)
	}
	if err := p.Auth.Validate(p.ProviderType); err != nil {
		return err
	}
	if err := p.validateAPIKeyAuth(); err != nil {
		return err
	}
	if err := validateProviderMode(p.ProviderType, p.ProviderMode); err != nil {
		return err
	}
	if err := p.Storage.Validate(p.ProviderType); err != nil {
		return err
	}
	for _, raw := range []string{p.Refresh.Threshold, p.Refresh.Cooldown, p.Refresh.Timeout} {
		if raw == "" {
			continue
		}
		if _, err := time.ParseDuration(raw); err != nil {
			return fmt.Errorf("%w: provider %q invalid refresh duration %q", ErrNodeAgentConfig, p.ProviderType, raw)
		}
	}
	return nil
}

func (p ProviderSpec) validateAPIKeyAuth() error {
	if p.Auth.Mode != "api_key" {
		return nil
	}
	if strings.TrimSpace(p.Upstream.APIKey) != "" || strings.TrimSpace(p.Upstream.APIKeyFile) != "" {
		return nil
	}
	if strings.TrimSpace(p.Auth.HostPath) != "" && strings.TrimSpace(p.Auth.ContainerPath) != "" {
		return nil
	}
	if strings.TrimSpace(p.Auth.HostPath) != "" || strings.TrimSpace(p.Auth.ContainerPath) != "" {
		return fmt.Errorf("%w: provider %q api_key auth copy requires both auth.host_path and auth.container_path", ErrNodeAgentConfig, p.ProviderType)
	}
	return fmt.Errorf("%w: provider %q api_key auth requires upstream.api_key, upstream.api_key_file, or auth.host_path copy", ErrNodeAgentConfig, p.ProviderType)
}

func (s StorageSpec) Validate(providerType string) error {
	mode := normalizedStorageMode(s.Mode)
	switch mode {
	case "", "ephemeral":
		return nil
	case "persistent":
	default:
		return fmt.Errorf("%w: provider %q unsupported storage.mode %q", ErrNodeAgentConfig, providerType, s.Mode)
	}
	if strings.TrimSpace(s.HostPath) == "" {
		return fmt.Errorf("%w: provider %q storage.host_path is required for persistent storage", ErrNodeAgentConfig, providerType)
	}
	for _, containerPath := range s.ContainerPaths {
		if strings.TrimSpace(containerPath) == "" {
			return fmt.Errorf("%w: provider %q storage.container_paths must not contain blank entries", ErrNodeAgentConfig, providerType)
		}
		if !strings.HasPrefix(strings.TrimSpace(containerPath), "/") {
			return fmt.Errorf("%w: provider %q storage.container_paths must be absolute", ErrNodeAgentConfig, providerType)
		}
	}
	return nil
}

func normalizedStorageMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func validateProviderMode(providerType string, mode string) error {
	switch normalizedProviderMode(mode) {
	case "", "http-direct", "app-server", "cli-adapter", "sdk", "acp", "ls-core-sidecar":
		return nil
	default:
		return fmt.Errorf("%w: provider %q unsupported provider_mode %q", ErrNodeAgentConfig, providerType, mode)
	}
}

func normalizedProviderMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func validateImagePullPolicy(providerType string, policy string) error {
	switch normalizedImagePullPolicy(policy) {
	case "", "always", "never":
		return nil
	default:
		return fmt.Errorf("%w: provider %q unsupported image_pull_policy %q", ErrNodeAgentConfig, providerType, policy)
	}
}

func normalizedImagePullPolicy(policy string) string {
	return strings.ToLower(strings.TrimSpace(policy))
}

func validateNetworkMode(providerType string, mode string) error {
	normalized := normalizedNetworkMode(mode)
	switch normalized {
	case "", "bridge", "host", "none":
		return nil
	default:
		if isSimpleNetworkName(normalized) {
			return nil
		}
		return fmt.Errorf("%w: provider %q unsupported network_mode %q", ErrNodeAgentConfig, providerType, mode)
	}
}

func normalizedNetworkMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

func isSimpleNetworkName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == '-':
		default:
			return false
		}
		if i == 0 && (r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

func (a AuthSpec) Validate(providerType string) error {
	mode := a.Mode
	if mode == "" {
		return nil
	}
	switch mode {
	case "file":
	case "api_key":
	default:
		return fmt.Errorf("%w: provider %q unsupported auth mode %q", ErrNodeAgentConfig, providerType, mode)
	}
	if mode == "api_key" && strings.TrimSpace(a.HostPath) == "" && strings.TrimSpace(a.ContainerPath) == "" {
		return nil
	}
	bootstrap := a.Bootstrap
	if bootstrap == "" {
		bootstrap = "copy"
	}
	if bootstrap != "copy" {
		return fmt.Errorf("%w: provider %q unsupported auth bootstrap %q", ErrNodeAgentConfig, providerType, bootstrap)
	}
	if strings.TrimSpace(a.HostPath) == "" {
		return fmt.Errorf("%w: provider %q auth.host_path is required for %s bootstrap", ErrNodeAgentConfig, providerType, mode)
	}
	if strings.TrimSpace(a.ContainerPath) == "" {
		return fmt.Errorf("%w: provider %q auth.container_path is required for %s bootstrap", ErrNodeAgentConfig, providerType, mode)
	}
	if _, err := a.FilePerm(); err != nil {
		return fmt.Errorf("%w: provider %q invalid auth.file_mode: %v", ErrNodeAgentConfig, providerType, err)
	}
	return nil
}

func (a AuthSpec) FilePerm() (os.FileMode, error) {
	if strings.TrimSpace(a.FileMode) == "" {
		return 0o600, nil
	}
	raw := strings.TrimPrefix(strings.TrimSpace(a.FileMode), "0")
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(parsed), nil
}

func (p ProviderSpec) Registration(nodeID string, hostName string, now time.Time) provider.Registration {
	if p.InstanceID == "" {
		p.InstanceID = p.ProviderType + "-local"
	}
	if p.HostName != "" {
		hostName = p.HostName
	}
	containerName := defaultContainerName(p.ProviderType, p.InstanceID)
	account := provider.Account{Display: p.AccountHint}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       p.ProviderType,
			ProviderInstanceID: p.InstanceID,
			NodeID:             nodeID,
			HostName:           hostName,
			ContainerName:      containerName,
			Service:            p.Service,
			Kind:               p.Kind,
			Account:            account,
		},
		Capabilities: append([]provider.Capability(nil), p.Shim.Capabilities...),
		Models:       append([]provider.Model(nil), p.Models...),
		Health:       provider.Health{Status: provider.HealthUnknown, CheckedAt: now},
		Auth: provider.AuthState{
			Status:      provider.AuthUnknown,
			Account:     account,
			Refreshable: len(p.Refresh.Command) > 0,
		},
		RegisteredAt: now,
	}
}

func (r RuntimeConfig) ControlRuntimeInfo() control.RuntimeInfo {
	return control.RuntimeInfo{Kind: r.Kind, Version: r.Version, Rootless: r.Rootless}
}

func (c Config) ProviderByInstanceID(instanceID string) (ProviderSpec, bool) {
	for _, spec := range c.Providers {
		candidate := spec.InstanceID
		if candidate == "" {
			candidate = spec.ProviderType + "-local"
		}
		if candidate == instanceID {
			return spec, true
		}
	}
	return ProviderSpec{}, false
}

func validProviderKind(kind provider.Kind) bool {
	switch kind {
	case provider.KindCLIContainer, provider.KindAppServer, provider.KindAPICompatible, provider.KindSidecar, provider.KindSimulator:
		return true
	default:
		return false
	}
}
