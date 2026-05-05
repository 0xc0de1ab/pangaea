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
	ID          string           `json:"id" yaml:"id"`
	InstanceID  string           `json:"instance_id,omitempty" yaml:"instance_id,omitempty"`
	Kind        provider.Kind    `json:"kind" yaml:"kind"`
	Image       string           `json:"image,omitempty" yaml:"image,omitempty"`
	HostName    string           `json:"host_name,omitempty" yaml:"host_name,omitempty"`
	AccountHint string           `json:"account_hint,omitempty" yaml:"account_hint,omitempty"`
	Service     provider.Service `json:"service" yaml:"service"`
	Auth        AuthSpec         `json:"auth,omitempty" yaml:"auth,omitempty"`
	Refresh     RefreshSpec      `json:"refresh,omitempty" yaml:"refresh,omitempty"`
	Shim        ShimSpec         `json:"shim,omitempty" yaml:"shim,omitempty"`
	Resources   ResourceSpec     `json:"resources,omitempty" yaml:"resources,omitempty"`
	Upstream    UpstreamSpec     `json:"upstream,omitempty" yaml:"upstream,omitempty"`
}

type AuthSpec struct {
	Mode          string       `json:"mode,omitempty" yaml:"mode,omitempty"`
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
}

type ResourceSpec struct {
	CPUs      string `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Memory    string `json:"memory,omitempty" yaml:"memory,omitempty"`
	PidsLimit int    `json:"pids_limit,omitempty" yaml:"pids_limit,omitempty"`
}

type UpstreamSpec struct {
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Compat  string `json:"compat,omitempty" yaml:"compat,omitempty"`
}

func LoadConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := ParseConfigYAML(data)
	if err != nil {
		return Config{}, err
	}
	baseDir := filepath.Dir(path)
	for i := range cfg.Providers {
		if cfg.Providers[i].Auth.HostPath, err = config.ExpandPathFromDir(baseDir, cfg.Providers[i].Auth.HostPath); err != nil {
			return Config{}, err
		}
		if cfg.Providers[i].Auth.ContainerPath, err = config.ExpandPath(cfg.Providers[i].Auth.ContainerPath); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
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

func (c Config) Validate() error {
	if strings.TrimSpace(c.Version) == "" {
		return fmt.Errorf("%w: missing version", ErrNodeAgentConfig)
	}
	if c.Version != ConfigVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrNodeAgentConfig, c.Version)
	}
	seen := make(map[string]struct{}, len(c.Providers))
	for _, spec := range c.Providers {
		if err := spec.Validate(); err != nil {
			return err
		}
		if _, ok := seen[spec.ID]; ok {
			return fmt.Errorf("%w: duplicate provider id %q", ErrNodeAgentConfig, spec.ID)
		}
		seen[spec.ID] = struct{}{}
	}
	return nil
}

func (p ProviderSpec) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: provider id is required", ErrNodeAgentConfig)
	}
	if p.Kind == "" {
		return fmt.Errorf("%w: provider %q kind is required", ErrNodeAgentConfig, p.ID)
	}
	if !validProviderKind(p.Kind) {
		return fmt.Errorf("%w: provider %q kind %q is invalid", ErrNodeAgentConfig, p.ID, p.Kind)
	}
	if p.Service == "" {
		return fmt.Errorf("%w: provider %q service is required", ErrNodeAgentConfig, p.ID)
	}
	if len(p.Shim.Capabilities) == 0 {
		return fmt.Errorf("%w: provider %q shim.capabilities is required", ErrNodeAgentConfig, p.ID)
	}
	if err := p.Auth.Validate(p.ID); err != nil {
		return err
	}
	for _, raw := range []string{p.Refresh.Threshold, p.Refresh.Cooldown, p.Refresh.Timeout} {
		if raw == "" {
			continue
		}
		if _, err := time.ParseDuration(raw); err != nil {
			return fmt.Errorf("%w: provider %q invalid refresh duration %q", ErrNodeAgentConfig, p.ID, raw)
		}
	}
	return nil
}

func (a AuthSpec) Validate(providerID string) error {
	mode := a.Mode
	if mode == "" {
		return nil
	}
	switch mode {
	case "file":
	default:
		return fmt.Errorf("%w: provider %q unsupported auth mode %q", ErrNodeAgentConfig, providerID, mode)
	}
	bootstrap := a.Bootstrap
	if bootstrap == "" {
		bootstrap = "copy"
	}
	if bootstrap != "copy" {
		return fmt.Errorf("%w: provider %q unsupported auth bootstrap %q", ErrNodeAgentConfig, providerID, bootstrap)
	}
	if strings.TrimSpace(a.HostPath) == "" {
		return fmt.Errorf("%w: provider %q auth.host_path is required for file bootstrap", ErrNodeAgentConfig, providerID)
	}
	if strings.TrimSpace(a.ContainerPath) == "" {
		return fmt.Errorf("%w: provider %q auth.container_path is required for file bootstrap", ErrNodeAgentConfig, providerID)
	}
	if _, err := a.FilePerm(); err != nil {
		return fmt.Errorf("%w: provider %q invalid auth.file_mode: %v", ErrNodeAgentConfig, providerID, err)
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
		p.InstanceID = p.ID + "-local"
	}
	if p.HostName != "" {
		hostName = p.HostName
	}
	account := provider.Account{Display: p.AccountHint}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         p.ID,
			ProviderInstanceID: p.InstanceID,
			NodeID:             nodeID,
			HostName:           hostName,
			Service:            p.Service,
			Kind:               p.Kind,
			Account:            account,
		},
		Capabilities: append([]provider.Capability(nil), p.Shim.Capabilities...),
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

func (c Config) ProviderByID(id string) (ProviderSpec, bool) {
	for _, spec := range c.Providers {
		if spec.ID == id {
			return spec, true
		}
	}
	return ProviderSpec{}, false
}

func validProviderKind(kind provider.Kind) bool {
	switch kind {
	case provider.KindCLIContainer, provider.KindAPICompatible, provider.KindSidecar, provider.KindSimulator:
		return true
	default:
		return false
	}
}
