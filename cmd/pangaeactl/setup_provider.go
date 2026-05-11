package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/nodeagent"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	containerruntime "github.com/0xc0de1ab/pangaea/internal/runtime"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const defaultSetupProviderOut = "./deploy/provider"

type setupProviderOptions struct {
	Type               string
	Mode               string
	Service            string
	ProviderType       string
	InstanceID         string
	AuthPath           string
	SettingsPath       string
	OutDir             string
	NodeID             string
	HostName           string
	Image              string
	RuntimeImage       string
	ImagePullPolicy    string
	NetworkMode        string
	Namespace          string
	StorageMode        string
	StoragePath        string
	UpstreamBaseURL    string
	UpstreamAPIKey     string
	UpstreamAPIKeyFile string
	UpstreamAPIKeyMode string
	RouterControl      string
	RouterData         string
	RouterPeerToken    string
	StreamTokenKey     string
	DockerBin          string
	PodmanBin          string
	KubectlBin         string
	Apply              bool
	DryRun             bool
}

type setupProviderArtifact struct {
	Path    string
	Content []byte
}

type setupProviderPlan struct {
	Type                string
	Mode                string
	Service             provider.Service
	Spec                nodeagent.ProviderSpec
	Config              nodeagent.Config
	NodeID              string
	HostName            string
	OutDir              string
	RuntimeSettingsPath string
	Artifacts           []setupProviderArtifact
}

type setupProviderRuntimeSettings struct {
	Version            string `json:"version"`
	Mode               string `json:"mode,omitempty"`
	Type               string `json:"type"`
	Service            string `json:"service"`
	ProviderType       string `json:"provider_type"`
	ProviderInstanceID string `json:"provider_instance_id"`
	NodeID             string `json:"node_id"`
	HostName           string `json:"host_name"`
	UpdatedAt          string `json:"updated_at"`
}

type setupProviderDefaults struct {
	Service             provider.Service
	ImageName           string
	RuntimeImageName    string
	DefaultProviderType string
	DefaultMode         string
	ProviderKind        provider.Kind
	AuthFormat          string
	AuthContainerPath   string
	AuthSecretKey       string
	AuthCandidates      []string
	ProviderMode        string
	UpstreamBaseURL     string
	UpstreamDialect     string
	ShimCommand         []string
	Models              []provider.Model
	ExtraCapabilities   []provider.Capability
	RefreshCommand      []string
	ExtraEnv            map[string]string
}

func newSetupProviderCmd() *cobra.Command {
	opts := setupProviderOptions{}
	cmd := &cobra.Command{
		Use:           "setup-provider",
		Short:         "generate or apply a provider runtime setup",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetupProvider(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.Type, "type", "", "provider runtime type (native-systemd|docker|podman|kind|k8s|kubernetes)")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "provider adapter mode (app-server|http-direct|cli-adapter|acp|ls-core-sidecar)")
	cmd.Flags().StringVar(&opts.Service, "service", "gemini", "provider service (codex|claude|gemini|antigravity)")
	cmd.Flags().StringVar(&opts.ProviderType, "provider-type", "", "logical provider type; defaults to <service>-cli")
	cmd.Flags().StringVar(&opts.InstanceID, "instance-id", "", "provider instance id; defaults from derived auth account or provider type")
	cmd.Flags().StringVar(&opts.AuthPath, "auth-path", "", "host auth file path to copy/bootstrap")
	cmd.Flags().StringVar(&opts.SettingsPath, "settings-path", "", "optional Gemini settings.json path for MCP settings")
	cmd.Flags().StringVar(&opts.OutDir, "out-dir", defaultSetupProviderOut, "directory for generated setup artifacts")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "node id to report; defaults to host name")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "physical host name to report; defaults to OS host name")
	cmd.Flags().StringVar(&opts.Image, "image", "", "provider image; defaults from service and type")
	cmd.Flags().StringVar(&opts.RuntimeImage, "runtime-image", "", "sidecar runtime image for providers that need a companion runtime")
	cmd.Flags().StringVar(&opts.ImagePullPolicy, "image-pull-policy", "", "image pull policy for docker/podman node-agent config (always|never)")
	cmd.Flags().StringVar(&opts.NetworkMode, "network-mode", "", "container network mode/name for docker/podman providers")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", "", "Kubernetes namespace for kind/k8s manifests")
	cmd.Flags().StringVar(&opts.StorageMode, "storage", "persistent", "provider state storage mode (persistent|ephemeral)")
	cmd.Flags().StringVar(&opts.StoragePath, "storage-path", "", "host path for persistent provider state")
	cmd.Flags().StringVar(&opts.UpstreamBaseURL, "upstream-base-url", "", "upstream compatible API base URL for sidecar/app-server modes")
	cmd.Flags().StringVar(&opts.UpstreamAPIKey, "upstream-api-key", "", "upstream API key for sidecar/app-server modes")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyFile, "upstream-api-key-file", "", "path to an upstream API key file for sidecar/app-server modes")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyMode, "upstream-api-key-mode", "", "upstream API key placement (bearer|header|query|none)")
	cmd.Flags().StringVar(&opts.RouterControl, "router-control", "", "router control WebSocket URL for the provider shim")
	cmd.Flags().StringVar(&opts.RouterData, "router-data", "", "router data WebSocket URL for reverse streams")
	cmd.Flags().StringVar(&opts.RouterPeerToken, "router-peer-token", "", "router peer bearer token")
	cmd.Flags().StringVar(&opts.StreamTokenKey, "stream-token-key", defaultStreamTokenKey, "shared stream token HMAC key")
	cmd.Flags().StringVar(&opts.DockerBin, "docker-bin", "docker", "docker CLI binary")
	cmd.Flags().StringVar(&opts.PodmanBin, "podman-bin", "podman", "podman CLI binary")
	cmd.Flags().StringVar(&opts.KubectlBin, "kubectl-bin", "kubectl", "kubectl CLI binary")
	cmd.Flags().BoolVar(&opts.Apply, "apply", false, "apply the generated setup to the selected runtime")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "render and validate without writing files or applying")
	return cmd
}

func runSetupProvider(ctx context.Context, opts setupProviderOptions, out io.Writer) error {
	plan, err := buildSetupProviderPlan(opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		for _, artifact := range plan.Artifacts {
			content := redactSetupProviderDryRunContent(artifact)
			fmt.Fprintf(out, "--- %s ---\n%s", artifact.Path, content)
			if len(content) == 0 || content[len(content)-1] != '\n' {
				fmt.Fprintln(out)
			}
		}
		return nil
	}
	if err := persistSetupProviderRuntimeSettings(plan); err != nil {
		return err
	}
	for _, artifact := range plan.Artifacts {
		if err := os.MkdirAll(filepath.Dir(artifact.Path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(artifact.Path, artifact.Content, fileModeForArtifact(artifact.Path)); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", artifact.Path)
	}
	if !opts.Apply {
		printSetupProviderNextStep(out, plan)
		return nil
	}
	return applySetupProvider(ctx, opts, plan, out)
}

func buildSetupProviderPlan(opts setupProviderOptions) (setupProviderPlan, error) {
	setupType, err := normalizeSetupProviderType(opts.Type)
	if err != nil {
		return setupProviderPlan{}, err
	}
	service := provider.Service(strings.TrimSpace(opts.Service))
	defaults, err := setupDefaultsForService(service)
	if err != nil {
		return setupProviderPlan{}, err
	}
	mode, err := applySetupProviderMode(&defaults, opts.Mode)
	if err != nil {
		return setupProviderPlan{}, err
	}
	outDir, err := ensureAbsDir(opts.OutDir)
	if err != nil {
		return setupProviderPlan{}, err
	}
	hostName := strings.TrimSpace(opts.HostName)
	if hostName == "" {
		hostName = localHostname()
	}
	authPath, err := setupProviderAuthPath(opts.AuthPath)
	if err != nil {
		return setupProviderPlan{}, err
	}
	account := ""
	if authPath != "" {
		account = accountDisplayFromAuth(authPath, defaults.AuthFormat)
	}
	providerType := strings.TrimSpace(opts.ProviderType)
	if providerType == "" {
		providerType = strings.TrimSpace(defaults.DefaultProviderType)
	}
	if providerType == "" {
		providerType = string(service) + "-cli"
	}
	instanceID := strings.TrimSpace(opts.InstanceID)
	if instanceID == "" {
		if account != "" {
			instanceID = string(service) + "-" + sanitizeSetupToken(account)
		} else {
			instanceID = providerType
		}
	}
	runtimeSettingsPath, err := setupProviderRuntimeSettingsPath(setupType, service, instanceID)
	if err != nil {
		return setupProviderPlan{}, err
	}
	nodeID := strings.TrimSpace(opts.NodeID)
	if nodeID == "" {
		if settings, ok := loadSetupProviderRuntimeSettings(runtimeSettingsPath); ok {
			nodeID = strings.TrimSpace(settings.NodeID)
		}
	}
	if nodeID == "" {
		nodeID, err = randomSetupNodeID()
		if err != nil {
			return setupProviderPlan{}, err
		}
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = setupProviderDefaultImage(defaults.ImageName, setupType)
	}
	storageMode := strings.ToLower(strings.TrimSpace(opts.StorageMode))
	if storageMode == "" {
		storageMode = "persistent"
	}
	storagePath := strings.TrimSpace(opts.StoragePath)
	if storageMode == "persistent" && storagePath == "" {
		storagePath = filepath.Join("/var/lib/pangaea", instanceID)
	}
	if storagePath != "" {
		if expanded, err := config.ExpandPath(storagePath); err == nil {
			storagePath = expanded
		}
	}
	capabilities := append(defaultSetupCapabilities(authPath != ""), defaults.ExtraCapabilities...)
	if service == provider.ServiceAntigravity && authPath != "" {
		capabilities = append(capabilities, provider.CapabilityAuthRefreshProtocol)
	}
	capabilities = dedupeSetupCapabilities(capabilities)
	refreshSpec := nodeagent.RefreshSpec{}
	if authPath != "" {
		refreshSpec = nodeagent.RefreshSpec{
			Command:   append([]string(nil), defaults.RefreshCommand...),
			Threshold: "5m",
			Cooldown:  "5m",
			Timeout:   "2m",
		}
	}
	spec := nodeagent.ProviderSpec{
		ProviderType:    providerType,
		InstanceID:      instanceID,
		Kind:            defaults.ProviderKind,
		ProviderMode:    mode,
		Image:           image,
		ImagePullPolicy: opts.ImagePullPolicy,
		NetworkMode:     strings.TrimSpace(opts.NetworkMode),
		HostName:        hostName,
		AccountHint:     account,
		Service:         service,
		Models:          cloneSetupModels(defaults.Models),
		Env:             cloneSetupStringMap(defaults.ExtraEnv),
		Refresh:         refreshSpec,
		Shim: nodeagent.ShimSpec{
			Protocols:    []string{"openai", "anthropic", "gemini"},
			Capabilities: capabilities,
			Command:      append([]string(nil), defaults.ShimCommand...),
			WorkingDir:   "/work",
		},
		Storage: nodeagent.StorageSpec{
			Mode:           storageMode,
			HostPath:       storagePath,
			ContainerPaths: setupProviderStorageContainerPaths(service),
		},
		Upstream: nodeagent.UpstreamSpec{
			BaseURL:    firstNonBlankString(opts.UpstreamBaseURL, defaults.UpstreamBaseURL),
			Compat:     defaults.UpstreamDialect,
			APIKey:     strings.TrimSpace(opts.UpstreamAPIKey),
			APIKeyFile: strings.TrimSpace(opts.UpstreamAPIKeyFile),
			APIKeyMode: strings.TrimSpace(opts.UpstreamAPIKeyMode),
		},
	}
	if authPath != "" {
		spec.Auth = nodeagent.AuthSpec{
			Mode:          "file",
			Format:        defaults.AuthFormat,
			Bootstrap:     "copy",
			HostPath:      authPath,
			ContainerPath: defaults.AuthContainerPath,
			FileMode:      "0600",
			Sync:          setupProviderAuthSync(service),
		}
	}
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	if authPath == "" {
		spec.Env["PANGAEA_AUTH_REQUIRED"] = "false"
	}
	if settingsJSON := geminiMCPServersJSONForEnv(opts.SettingsPath, service); settingsJSON != "" {
		spec.Env["PANGAEA_MCP_SERVERS_JSON"] = settingsJSON
	}
	if setupType == "native-systemd" {
		if authPath != "" {
			spec.Auth.ContainerPath = authPath
		}
		spec.Storage = nodeagent.StorageSpec{}
		spec.Env = nativeSetupProviderEnv(service, authPath, spec.Env)
	}
	cfg := nodeagent.Config{
		Version: nodeagent.ConfigVersion,
		Node: nodeagent.NodeConfig{
			ID:       nodeID,
			HostName: hostName,
		},
		Runtime: nodeagent.RuntimeConfig{
			Kind: setupProviderRuntimeKind(setupType),
		},
		Providers: []nodeagent.ProviderSpec{spec},
	}
	if err := cfg.Validate(); err != nil {
		return setupProviderPlan{}, err
	}
	artifacts, err := setupProviderArtifacts(setupType, outDir, opts, cfg, spec, nodeID, hostName, defaults)
	if err != nil {
		return setupProviderPlan{}, err
	}
	return setupProviderPlan{
		Type:                setupType,
		Mode:                mode,
		Service:             service,
		Spec:                spec,
		Config:              cfg,
		NodeID:              nodeID,
		HostName:            hostName,
		OutDir:              outDir,
		RuntimeSettingsPath: runtimeSettingsPath,
		Artifacts:           artifacts,
	}, nil
}

func setupProviderStorageContainerPaths(service provider.Service) []string {
	paths := []string{"/var/lib/pangaea", "/work"}
	if service == provider.ServiceAntigravity {
		paths = append(paths, "/var/lib/antigravity")
	}
	return paths
}

func setupProviderAuthSync(service provider.Service) nodeagent.AuthSyncSpec {
	spec := nodeagent.AuthSyncSpec{ContainerToHost: true, HostToContainer: "reconcile"}
	if service == provider.ServiceAntigravity {
		spec.ContainerToHost = false
	}
	return spec
}

func setupProviderArtifacts(setupType string, outDir string, opts setupProviderOptions, cfg nodeagent.Config, spec nodeagent.ProviderSpec, nodeID string, hostName string, defaults setupProviderDefaults) ([]setupProviderArtifact, error) {
	switch setupType {
	case "docker", "podman":
		raw, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		return []setupProviderArtifact{{Path: filepath.Join(outDir, "node-agent.yaml"), Content: raw}}, nil
	case "kind", "kubernetes":
		raw, err := renderSetupProviderKubernetesManifest(setupType, opts, spec, nodeID, defaults)
		if err != nil {
			return nil, err
		}
		return []setupProviderArtifact{{Path: filepath.Join(outDir, "provider.k8s.yaml"), Content: raw}}, nil
	case "native-systemd":
		env := renderSetupProviderEnv(spec, nodeID, hostName, "")
		unit := renderSetupProviderSystemdUnit(spec, filepath.Join(outDir, "provider.env"))
		return []setupProviderArtifact{
			{Path: filepath.Join(outDir, "provider.env"), Content: []byte(env)},
			{Path: filepath.Join(outDir, "pangaea-provider-"+spec.InstanceID+".service"), Content: []byte(unit)},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported setup provider type %q", setupType)
	}
}

func applySetupProvider(ctx context.Context, opts setupProviderOptions, plan setupProviderPlan, out io.Writer) error {
	switch plan.Type {
	case "docker", "podman":
		binary := opts.DockerBin
		if plan.Type == "podman" {
			binary = opts.PodmanBin
		}
		rt := containerruntime.NewDockerRuntime(binary)
		result, err := nodeagent.ReconcileProviderContainerWithOptions(ctx, rt, plan.Spec, plan.NodeID, plan.HostName, nodeagent.ContainerSpecOptions{
			RouterControlURL: opts.RouterControl,
			RouterDataURL:    opts.RouterData,
			StreamTokenKey:   opts.StreamTokenKey,
			RouterPeerToken:  opts.RouterPeerToken,
			ContainerKind:    plan.Type,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "provider %s running in %s container %s\n", plan.Spec.InstanceID, plan.Type, result.ContainerID)
		return nil
	case "kind", "kubernetes":
		if len(plan.Artifacts) == 0 {
			return fmt.Errorf("no Kubernetes manifest was generated")
		}
		args := []string{"apply", "--server-side=true", "--force-conflicts", "-f", plan.Artifacts[0].Path}
		cmd := exec.CommandContext(ctx, opts.KubectlBin, args...)
		cmd.Stdout = out
		cmd.Stderr = out
		return cmd.Run()
	case "native-systemd":
		printSetupProviderNextStep(out, plan)
		return nil
	default:
		return fmt.Errorf("unsupported setup provider type %q", plan.Type)
	}
}

func printSetupProviderNextStep(out io.Writer, plan setupProviderPlan) {
	switch plan.Type {
	case "docker", "podman":
		fmt.Fprintf(out, "next: pangaeactl node-agent reconcile-provider --config %s --provider-instance %s --runtime-kind %s --router-control <ws-url>\n", filepath.Join(plan.OutDir, "node-agent.yaml"), plan.Spec.InstanceID, plan.Type)
	case "kind", "kubernetes":
		fmt.Fprintf(out, "next: kubectl apply -f %s\n", filepath.Join(plan.OutDir, "provider.k8s.yaml"))
	case "native-systemd":
		fmt.Fprintf(out, "next: install %s and start it with systemctl\n", filepath.Join(plan.OutDir, "pangaea-provider-"+plan.Spec.InstanceID+".service"))
	}
}

func normalizeSetupProviderType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "native-systemd", "systemd":
		return "native-systemd", nil
	case "docker":
		return "docker", nil
	case "podman":
		return "podman", nil
	case "kind":
		return "kind", nil
	case "k8s", "kubernetes":
		return "kubernetes", nil
	default:
		return "", fmt.Errorf("--type must be one of native-systemd, docker, podman, kind, k8s, kubernetes")
	}
}

func applySetupProviderMode(defaults *setupProviderDefaults, raw string) (string, error) {
	if defaults == nil {
		return "", fmt.Errorf("setup provider defaults are required")
	}
	mode := normalizeSetupProviderMode(raw)
	if mode == "" {
		mode = defaults.DefaultMode
	}
	if mode == "" {
		return "", fmt.Errorf("--mode is required for service %q", defaults.Service)
	}
	switch mode {
	case "app-server":
		if defaults.Service != provider.ServiceCodex {
			return "", fmt.Errorf("--mode app-server is currently supported only for service codex")
		}
		defaults.ProviderKind = provider.KindAppServer
		defaults.ProviderMode = "app-server"
		if strings.TrimSpace(defaults.UpstreamBaseURL) == "" {
			defaults.UpstreamBaseURL = "ws://127.0.0.1:8080"
		}
		if len(defaults.ShimCommand) == 0 {
			defaults.ShimCommand = []string{"codex", "app-server", "--listen", defaults.UpstreamBaseURL}
		}
	case "http-direct":
		switch defaults.Service {
		case provider.ServiceGemini:
			defaults.ProviderKind = provider.KindCLIContainer
			defaults.ProviderMode = "http-direct"
			defaults.UpstreamBaseURL = ""
			defaults.ShimCommand = nil
		case provider.ServiceCodex:
			defaults.ProviderKind = provider.KindCLIContainer
			defaults.ProviderMode = "http-direct"
			defaults.UpstreamBaseURL = ""
			defaults.ShimCommand = nil
		default:
			defaults.ProviderKind = provider.KindCLIContainer
			defaults.ProviderMode = "http-direct"
			defaults.ShimCommand = nil
		}
	case "cli-adapter":
		switch defaults.Service {
		case provider.ServiceClaude, provider.ServiceGemini:
			defaults.ProviderKind = provider.KindCLIContainer
			defaults.ProviderMode = "cli-adapter"
			defaults.UpstreamBaseURL = ""
			defaults.ShimCommand = nil
		default:
			return "", fmt.Errorf("--mode cli-adapter is not implemented for service %s", defaults.Service)
		}
	case "acp":
		return "", fmt.Errorf("--mode acp is not implemented yet")
	case "ls-core-sidecar":
		if defaults.Service != provider.ServiceAntigravity {
			return "", fmt.Errorf("--mode ls-core-sidecar is currently supported only for service antigravity")
		}
		defaults.ProviderKind = provider.KindSidecar
		defaults.ProviderMode = "ls-core-sidecar"
		if strings.TrimSpace(defaults.UpstreamBaseURL) == "" {
			defaults.UpstreamBaseURL = "http://127.0.0.1:8080"
		}
		if strings.TrimSpace(defaults.UpstreamDialect) == "" {
			defaults.UpstreamDialect = "openai"
		}
		defaults.ShimCommand = nil
	default:
		return "", fmt.Errorf("--mode must be one of app-server, http-direct, cli-adapter, acp, ls-core-sidecar")
	}
	return mode, nil
}

func normalizeSetupProviderMode(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func setupProviderRuntimeKind(setupType string) string {
	switch setupType {
	case "kind", "kubernetes":
		return "kubernetes"
	default:
		return setupType
	}
}

func setupProviderDefaultImage(imageName string, setupType string) string {
	tag := "latest"
	if setupType == "kind" {
		tag = "kind"
	}
	return imageName + ":" + tag
}

func firstNonBlankString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func setupDefaultsForService(service provider.Service) (setupProviderDefaults, error) {
	modelCaps := []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE}
	switch service {
	case provider.ServiceCodex:
		return setupProviderDefaults{
			Service:           service,
			ImageName:         "pangaea/provider-codex",
			DefaultMode:       "app-server",
			ProviderKind:      provider.KindAppServer,
			AuthFormat:        "codex-auth-json-format",
			AuthContainerPath: "/var/lib/pangaea/auth/codex/auth.json",
			AuthSecretKey:     "auth.json",
			AuthCandidates:    []string{"assets/.codex/auth.json", "~/.codex/auth.json"},
			ProviderMode:      "app-server",
			UpstreamBaseURL:   "ws://127.0.0.1:8080",
			UpstreamDialect:   "openai",
			ShimCommand:       []string{"codex", "app-server", "--listen", "ws://127.0.0.1:8080"},
			RefreshCommand:    []string{"codex", "exec", "--skip-git-repo-check", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--color", "never", "Reply with OK only."},
			Models: []provider.Model{{
				ID:           "gpt-5.5",
				Aliases:      []string{"codex-default"},
				Capabilities: modelCaps,
			}},
			ExtraEnv: map[string]string{
				"CODEX_HOME": "/var/lib/pangaea/auth/codex",
				"HOME":       "/var/lib/pangaea/home/codex",
				"TMPDIR":     "/var/lib/pangaea/tmp",
			},
		}, nil
	case provider.ServiceClaude:
		return setupProviderDefaults{
			Service:           service,
			ImageName:         "pangaea/provider-claude",
			DefaultMode:       "cli-adapter",
			ProviderKind:      provider.KindCLIContainer,
			AuthFormat:        "claude-credentials-json-format",
			AuthContainerPath: "/var/lib/pangaea/auth/claude/.credentials.json",
			AuthSecretKey:     ".credentials.json",
			AuthCandidates:    []string{"assets/.claude/.credentials.json", "~/.claude/.credentials.json"},
			ProviderMode:      "cli-adapter",
			UpstreamDialect:   "anthropic",
			RefreshCommand:    []string{"claude", "-p", "Reply with OK only.", "--permission-mode", "plan", "--tools", "", "--output-format", "text"},
			Models: []provider.Model{{
				ID:           "claude-default",
				Aliases:      []string{"Claude default"},
				Capabilities: modelCaps,
			}},
			ExtraEnv: map[string]string{
				"CLAUDE_CONFIG_DIR": "/var/lib/pangaea/auth/claude",
				"HOME":              "/var/lib/pangaea/home/claude",
				"TMPDIR":            "/var/lib/pangaea/tmp",
			},
		}, nil
	case provider.ServiceGemini:
		return setupProviderDefaults{
			Service:           service,
			ImageName:         "pangaea/provider-gemini",
			DefaultMode:       "http-direct",
			ProviderKind:      provider.KindCLIContainer,
			AuthFormat:        "gemini-oauth-creds-json-format",
			AuthContainerPath: "/var/lib/pangaea/home/gemini/.gemini/oauth_creds.json",
			AuthSecretKey:     "oauth_creds.json",
			AuthCandidates:    []string{"assets/.gemini/oauth_creds.json", "~/.gemini/oauth_creds.json"},
			ProviderMode:      "http-direct",
			UpstreamDialect:   "gemini",
			RefreshCommand:    []string{"gemini", "-p", "Reply with OK only.", "--skip-trust", "--approval-mode", "plan", "--output-format", "json", "--model", "gemini-2.5-flash"},
			Models:            defaultSetupGeminiModels(modelCaps),
			ExtraEnv: map[string]string{
				"HOME":                       "/var/lib/pangaea/home/gemini",
				"TMPDIR":                     "/var/lib/pangaea/tmp",
				"GEMINI_CLI_TRUST_WORKSPACE": "true",
				"TERM":                       "xterm-256color",
				"COLORTERM":                  "truecolor",
				"FORCE_COLOR":                "1",
			},
		}, nil
	case provider.ServiceAntigravity:
		sidecarCaps := []provider.Capability{
			provider.CapabilityAntigravitySidecar,
			provider.CapabilityAgentToolUse,
			provider.CapabilityAgentWorkspaceRead,
			provider.CapabilityAgentWorkspaceWrite,
		}
		caps := dedupeSetupCapabilities(append(modelCaps, sidecarCaps...))
		return setupProviderDefaults{
			Service:             service,
			ImageName:           "pangaea/provider-antigravity-sidecar",
			RuntimeImageName:    "pangaea/antigravity-runtime",
			DefaultProviderType: "antigravity-sidecar",
			DefaultMode:         "ls-core-sidecar",
			ProviderKind:        provider.KindSidecar,
			AuthFormat:          "antigravity-state-vscdb-format",
			AuthContainerPath:   "/var/lib/antigravity/state/User/globalStorage/state.vscdb",
			AuthSecretKey:       "state.vscdb",
			AuthCandidates: []string{
				"assets/.antigravity/state.vscdb",
				"assets/antigravity/state.vscdb",
				"~/.antigravity-server/data/User/globalStorage/state.vscdb",
				"~/.config/Antigravity/User/globalStorage/state.vscdb",
			},
			ProviderMode:    "ls-core-sidecar",
			UpstreamBaseURL: "http://127.0.0.1:8080",
			UpstreamDialect: "openai",
			Models: []provider.Model{{
				ID:           "antigravity-default",
				Aliases:      []string{"antigravity-default"},
				Capabilities: caps,
			}},
			ExtraCapabilities: sidecarCaps,
			ExtraEnv: map[string]string{
				"HOME": "/var/lib/pangaea/home/antigravity",
			},
		}, nil
	default:
		return setupProviderDefaults{}, fmt.Errorf("unsupported --service %q", service)
	}
}

func defaultSetupCapabilities(hasAuth bool) []provider.Capability {
	capabilities := []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityStreamSSE,
		provider.CapabilityUsageRead,
		provider.CapabilityModelsRead,
	}
	if hasAuth {
		capabilities = append(capabilities, provider.CapabilityAuthFile, provider.CapabilityAuthRefreshOneshot)
	}
	return capabilities
}

func dedupeSetupCapabilities(in []provider.Capability) []provider.Capability {
	out := make([]provider.Capability, 0, len(in))
	seen := map[provider.Capability]struct{}{}
	for _, capability := range in {
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func defaultSetupGeminiModels(caps []provider.Capability) []provider.Model {
	return []provider.Model{
		{ID: "auto-gemini-3", Aliases: []string{"gemini-default", "gemini-auto", "Auto Gemini 3", "Auto (Gemini 3)"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576, MaxOutputTokens: 65_536, Kind: "group", GroupMembers: []string{"gemini-3.1-pro-preview", "gemini-3-flash-preview"}},
	}
}

func setupProviderRuntimeSettingsPath(setupType string, service provider.Service, instanceID string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || strings.TrimSpace(home) == "" {
			if err != nil {
				return "", err
			}
			return "", homeErr
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "pangaea", "setup-provider", setupType, string(service), sanitizeSetupToken(instanceID), "runtime.json"), nil
}

func loadSetupProviderRuntimeSettings(path string) (setupProviderRuntimeSettings, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return setupProviderRuntimeSettings{}, false
	}
	var settings setupProviderRuntimeSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return setupProviderRuntimeSettings{}, false
	}
	if !validSetupNodeID(settings.NodeID) {
		return setupProviderRuntimeSettings{}, false
	}
	return settings, true
}

func persistSetupProviderRuntimeSettings(plan setupProviderPlan) error {
	if strings.TrimSpace(plan.RuntimeSettingsPath) == "" {
		return nil
	}
	settings := setupProviderRuntimeSettings{
		Version:            "setup-provider/runtime/v1",
		Mode:               plan.Mode,
		Type:               plan.Type,
		Service:            string(plan.Service),
		ProviderType:       plan.Spec.ProviderType,
		ProviderInstanceID: plan.Spec.InstanceID,
		NodeID:             plan.NodeID,
		HostName:           plan.HostName,
		UpdatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(plan.RuntimeSettingsPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(plan.RuntimeSettingsPath, raw, 0o600)
}

func randomSetupNodeID() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

func validSetupNodeID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func setupProviderAuthPath(raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		expanded, err := config.ExpandPath(raw)
		if err != nil {
			return "", err
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("stat --auth-path %q: %w", abs, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return "", fmt.Errorf("--auth-path %q must be a non-empty regular file", abs)
		}
		return abs, nil
	}
	return "", nil
}

func accountDisplayFromAuth(path string, formatName string) string {
	format, ok := formats.Get(formatName)
	if !ok {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	snap, err := format.Parse(raw)
	if err != nil {
		return ""
	}
	if displayAware, ok := format.(formats.AccountDisplayAware); ok {
		display, _ := displayAware.AccountDisplay(context.Background(), snap, path)
		return strings.TrimSpace(display)
	}
	if accountAware, ok := format.(formats.AccountAware); ok {
		account, _ := accountAware.Account(context.Background(), snap, path)
		return strings.TrimSpace(account)
	}
	return ""
}

func geminiMCPServersJSONForEnv(path string, service provider.Service) string {
	if service != provider.ServiceGemini {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	expanded, err := config.ExpandPath(path)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(expanded)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(raw)
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &settings); err != nil {
		return string(trimmed)
	}
	mcpServers, ok := settings["mcpServers"]
	if !ok || len(bytes.TrimSpace(mcpServers)) == 0 || bytes.Equal(bytes.TrimSpace(mcpServers), []byte("null")) {
		return ""
	}
	var servers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(mcpServers, &servers); err != nil {
		return string(trimmed)
	}
	filtered := map[string]map[string]json.RawMessage{}
	for name, server := range servers {
		if len(bytes.TrimSpace(server["command"])) == 0 {
			continue
		}
		filtered[name] = server
	}
	if len(filtered) == 0 {
		return ""
	}
	encoded, err := json.Marshal(map[string]any{"mcpServers": filtered})
	if err != nil {
		return ""
	}
	return string(encoded)
}

func geminiSettingsJSONForSecret(path string, service provider.Service) string {
	if service != provider.ServiceGemini {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	expanded, err := config.ExpandPath(path)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(expanded)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	return string(bytes.TrimSpace(raw))
}

func nativeSetupProviderEnv(service provider.Service, authPath string, base map[string]string) map[string]string {
	out := cloneSetupStringMap(base)
	if out == nil {
		out = map[string]string{}
	}
	delete(out, "TMPDIR")
	if strings.TrimSpace(authPath) == "" {
		delete(out, "CODEX_HOME")
		delete(out, "CLAUDE_CONFIG_DIR")
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			out["HOME"] = home
		} else {
			delete(out, "HOME")
		}
		for key, value := range out {
			if strings.TrimSpace(value) == "" {
				delete(out, key)
			}
		}
		return out
	}
	switch service {
	case provider.ServiceCodex:
		out["CODEX_HOME"] = filepath.Dir(authPath)
		out["HOME"] = nativeHomeFromConfigDir(filepath.Dir(authPath), ".codex")
	case provider.ServiceClaude:
		out["CLAUDE_CONFIG_DIR"] = filepath.Dir(authPath)
		out["HOME"] = nativeHomeFromConfigDir(filepath.Dir(authPath), ".claude")
	case provider.ServiceGemini:
		configDir := filepath.Dir(authPath)
		out["HOME"] = nativeHomeFromConfigDir(configDir, ".gemini")
	}
	for key, value := range out {
		if strings.TrimSpace(value) == "" {
			delete(out, key)
		}
	}
	return out
}

func nativeHomeFromConfigDir(configDir string, marker string) string {
	if filepath.Base(configDir) == marker {
		return filepath.Dir(configDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

func renderSetupProviderKubernetesManifest(setupType string, opts setupProviderOptions, spec nodeagent.ProviderSpec, nodeID string, defaults setupProviderDefaults) ([]byte, error) {
	if spec.Service == provider.ServiceAntigravity && spec.ProviderMode == "ls-core-sidecar" {
		return renderSetupProviderAntigravityKubernetesManifest(setupType, opts, spec, nodeID, defaults)
	}
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" {
		if setupType == "kind" {
			namespace = "pangaea-e2e"
		} else {
			namespace = "pangaea"
		}
	}
	labels := map[string]string{
		"app.kubernetes.io/name":      "pangaea-" + spec.InstanceID,
		"app.kubernetes.io/component": "provider-runtime",
		"pangaea/provider-type":       spec.ProviderType,
		"pangaea/provider-instance":   spec.InstanceID,
	}
	secretData := map[string]string{}
	if strings.TrimSpace(spec.Auth.HostPath) != "" {
		authRaw, err := os.ReadFile(spec.Auth.HostPath)
		if err != nil {
			return nil, err
		}
		secretData[defaults.AuthSecretKey] = base64.StdEncoding.EncodeToString(authRaw)
	}
	settingsKey, settingsData := setupProviderGeminiSettingsSecret(opts.SettingsPath, spec.Service)
	if settingsKey != "" {
		secretData[settingsKey] = settingsData
	}
	secretName := "pangaea-" + spec.InstanceID + "-auth"
	stateVolume := map[string]any{"name": "provider-state"}
	if strings.ToLower(strings.TrimSpace(spec.Storage.Mode)) == "persistent" {
		stateVolume["hostPath"] = map[string]any{"path": spec.Storage.HostPath, "type": "DirectoryOrCreate"}
	} else {
		stateVolume["emptyDir"] = map[string]any{}
	}
	initVolumeMounts := []any{
		map[string]any{"name": "provider-state", "mountPath": "/var/lib/pangaea"},
	}
	volumes := []any{
		stateVolume,
		map[string]any{"name": "provider-work", "emptyDir": map[string]any{}},
	}
	objects := []any{}
	if len(secretData) > 0 {
		secret := map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      secretName,
				"namespace": namespace,
				"labels":    labels,
			},
			"type": "Opaque",
			"data": secretData,
		}
		objects = append(objects, secret)
		initVolumeMounts = append([]any{map[string]any{"name": "provider-auth", "mountPath": "/auth-src", "readOnly": true}}, initVolumeMounts...)
		volumes = append([]any{map[string]any{"name": "provider-auth", "secret": map[string]any{"secretName": secretName, "defaultMode": 0400}}}, volumes...)
	}
	deployment := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "pangaea-" + spec.InstanceID,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": map[string]string{
				"app.kubernetes.io/name": "pangaea-" + spec.InstanceID,
			}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"initContainers": []any{map[string]any{
						"name":    "bootstrap-" + string(spec.Service),
						"image":   "alpine:3.22",
						"command": []string{"sh", "-c", setupProviderBootstrapScript(spec.Service)},
						"env":     setupProviderKubernetesBootstrapEnv(spec, nodeID, setupProviderRuntimeKind(setupType), opts),
						"securityContext": map[string]any{
							"runAsUser": 0,
						},
						"volumeMounts": initVolumeMounts,
					}},
					"containers": []any{map[string]any{
						"name":            "shim",
						"image":           spec.Image,
						"imagePullPolicy": setupProviderKubernetesPullPolicy(opts.ImagePullPolicy, setupType),
						"args":            spec.Shim.Command,
						"env":             setupProviderKubernetesEnv(spec, nodeID, setupProviderRuntimeKind(setupType), opts),
						"volumeMounts": []any{
							map[string]any{"name": "provider-state", "mountPath": "/var/lib/pangaea"},
							map[string]any{"name": "provider-work", "mountPath": "/work"},
						},
					}},
					"volumes": volumes,
				},
			},
		},
	}
	objects = append(objects, deployment)
	return yamlDocuments(objects...)
}

func renderSetupProviderAntigravityKubernetesManifest(setupType string, opts setupProviderOptions, spec nodeagent.ProviderSpec, nodeID string, defaults setupProviderDefaults) ([]byte, error) {
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" {
		if setupType == "kind" {
			namespace = "pangaea-e2e"
		} else {
			namespace = "pangaea"
		}
	}
	runtimeImage := strings.TrimSpace(opts.RuntimeImage)
	if runtimeImage == "" {
		if strings.TrimSpace(defaults.RuntimeImageName) == "" {
			return nil, fmt.Errorf("runtime image is required for antigravity sidecar setup")
		}
		runtimeImage = setupProviderDefaultImage(defaults.RuntimeImageName, setupType)
	}
	labels := map[string]string{
		"app.kubernetes.io/name":      "pangaea-" + spec.InstanceID,
		"app.kubernetes.io/component": "provider-runtime",
		"pangaea/provider-type":       spec.ProviderType,
		"pangaea/provider-instance":   spec.InstanceID,
	}
	runtimeSecretName := "pangaea-" + spec.InstanceID + "-runtime-secrets"
	authSecretName := "pangaea-" + spec.InstanceID + "-auth"
	objects := []any{
		map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      runtimeSecretName,
				"namespace": namespace,
				"labels":    labels,
			},
			"type": "Opaque",
			"data": map[string]string{
				"openai-key":    base64.StdEncoding.EncodeToString([]byte("pangaea-antigravity-openai")),
				"anthropic-key": base64.StdEncoding.EncodeToString([]byte("pangaea-antigravity-anthropic")),
				"gemini-key":    base64.StdEncoding.EncodeToString([]byte("pangaea-antigravity-gemini")),
			},
		},
	}
	if strings.TrimSpace(spec.Auth.HostPath) != "" {
		authRaw, err := os.ReadFile(spec.Auth.HostPath)
		if err != nil {
			return nil, err
		}
		objects = append(objects, map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      authSecretName,
				"namespace": namespace,
				"labels":    labels,
			},
			"type": "Opaque",
			"data": map[string]string{
				defaults.AuthSecretKey: base64.StdEncoding.EncodeToString(authRaw),
			},
		})
	}
	stateVolume := map[string]any{"name": "antigravity-state"}
	if strings.ToLower(strings.TrimSpace(spec.Storage.Mode)) == "persistent" {
		stateVolume["hostPath"] = map[string]any{"path": spec.Storage.HostPath, "type": "DirectoryOrCreate"}
	} else {
		stateVolume["emptyDir"] = map[string]any{}
	}
	valueFromRuntimeSecret := func(key string) map[string]any {
		return map[string]any{"secretKeyRef": map[string]any{"name": runtimeSecretName, "key": key}}
	}
	shimEnv := setupProviderKubernetesEnv(spec, nodeID, setupProviderRuntimeKind(setupType), opts)
	shimEnv = append(shimEnv, map[string]any{"name": "PANGAEA_UPSTREAM_API_KEY", "valueFrom": valueFromRuntimeSecret("openai-key")})
	deployment := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "pangaea-" + spec.InstanceID,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": map[string]string{
				"app.kubernetes.io/name": "pangaea-" + spec.InstanceID,
			}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"initContainers": []any{map[string]any{
						"name":    "bootstrap-antigravity",
						"image":   "alpine:3.22",
						"command": []string{"sh", "-c", setupProviderBootstrapScript(provider.ServiceAntigravity)},
						"env":     setupProviderKubernetesBootstrapEnv(spec, nodeID, setupProviderRuntimeKind(setupType), opts),
						"securityContext": map[string]any{
							"runAsUser": 0,
						},
						"volumeMounts": []any{
							map[string]any{"name": "antigravity-auth-secret", "mountPath": "/auth-src", "readOnly": true},
							map[string]any{"name": "antigravity-state", "mountPath": "/var/lib/antigravity/state"},
							map[string]any{"name": "antigravity-state", "mountPath": "/var/lib/pangaea"},
						},
					}},
					"containers": []any{
						map[string]any{
							"name":            "runtime",
							"image":           runtimeImage,
							"imagePullPolicy": setupProviderKubernetesPullPolicy(opts.ImagePullPolicy, setupType),
							"command":         []string{"sh", "-c"},
							"args": []string{strings.TrimSpace(`
mkdir -p /var/lib/antigravity/state/User/globalStorage
exec antigravity-compat-proxy serve \
  --proxy-addr 0.0.0.0:8080 \
  --db-path /var/lib/antigravity/state/User/globalStorage/state.vscdb
`)},
							"env": []any{
								map[string]any{"name": "OPENAI_API_KEY", "valueFrom": valueFromRuntimeSecret("openai-key")},
								map[string]any{"name": "ANTHROPIC_API_KEY", "valueFrom": valueFromRuntimeSecret("anthropic-key")},
								map[string]any{"name": "GOOGLE_API_KEY", "valueFrom": valueFromRuntimeSecret("gemini-key")},
								map[string]any{"name": "ANTIGRAVITY_GEMINI_DIR", "value": "/root/.antigravity-server"},
								map[string]any{"name": "ANTIGRAVITY_APP_DATA_DIR", "value": "data"},
								map[string]any{"name": "ANTIGRAVITY_STREAM_CAPTURE_PATH", "value": "/var/lib/antigravity/state/stream-captures/ag-stream-capture.jsonl"},
							},
							"ports": []any{map[string]any{"name": "http", "containerPort": 8080}},
							"volumeMounts": []any{
								map[string]any{"name": "antigravity-state", "mountPath": "/var/lib/antigravity/state"},
								map[string]any{"name": "antigravity-state", "mountPath": "/root/.antigravity-server/data"},
							},
							"readinessProbe": map[string]any{
								"httpGet":          map[string]any{"path": "/v1/health", "port": "http"},
								"periodSeconds":    2,
								"failureThreshold": 60,
							},
							"livenessProbe": map[string]any{
								"httpGet":          map[string]any{"path": "/v1/health", "port": "http"},
								"periodSeconds":    10,
								"failureThreshold": 6,
							},
						},
						map[string]any{
							"name":            "shim",
							"image":           spec.Image,
							"imagePullPolicy": setupProviderKubernetesPullPolicy(opts.ImagePullPolicy, setupType),
							"env":             shimEnv,
							"volumeMounts": []any{
								map[string]any{"name": "antigravity-state", "mountPath": "/var/lib/antigravity/state"},
								map[string]any{"name": "antigravity-state", "mountPath": "/var/lib/pangaea"},
							},
						},
					},
					"volumes": []any{
						map[string]any{"name": "antigravity-auth-secret", "secret": map[string]any{"secretName": authSecretName, "optional": true, "defaultMode": 0400}},
						stateVolume,
					},
				},
			},
		},
	}
	objects = append(objects, deployment)
	objects = append(objects, map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      "pangaea-" + spec.InstanceID,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"selector": map[string]string{"app.kubernetes.io/name": "pangaea-" + spec.InstanceID},
			"ports": []any{map[string]any{
				"name":       "http",
				"port":       8080,
				"targetPort": "http",
			}},
		},
	})
	return yamlDocuments(objects...)
}

func setupProviderGeminiSettingsSecret(path string, service provider.Service) (string, string) {
	if service != provider.ServiceGemini {
		return "", ""
	}
	settings := geminiSettingsJSONForSecret(path, service)
	if settings == "" {
		return "", ""
	}
	return "settings.json", base64.StdEncoding.EncodeToString([]byte(settings))
}

func setupProviderBootstrapScript(service provider.Service) string {
	switch service {
	case provider.ServiceCodex:
		return strings.TrimSpace(`
set -eu
mkdir -p /var/lib/pangaea/auth/codex /var/lib/pangaea/home/codex /var/lib/pangaea/tmp /work
write_runtime_settings() {
  runtime_settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
  runtime_dir="$(dirname "$runtime_settings")"
  mkdir -p "$runtime_dir"
  if [ ! -s "$runtime_settings" ]; then
    {
      printf 'PANGAEA_NODE_ID=%s\n' "${PANGAEA_NODE_ID:-}"
      printf 'PANGAEA_HOST_NAME=%s\n' "${PANGAEA_HOST_NAME:-}"
      printf 'PANGAEA_PROVIDER_TYPE=%s\n' "${PANGAEA_PROVIDER_TYPE:-}"
      printf 'PANGAEA_PROVIDER_INSTANCE_ID=%s\n' "${PANGAEA_PROVIDER_INSTANCE_ID:-}"
      printf 'PANGAEA_PROVIDER_MODE=%s\n' "${PANGAEA_PROVIDER_MODE:-}"
      printf 'PANGAEA_SERVICE=%s\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%s\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%s\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%s\n' "${PANGAEA_CONTAINER_ID:-}"
    } > "$runtime_settings"
    chmod 0600 "$runtime_settings" 2>/dev/null || true
  fi
}
write_runtime_settings
if [ -s /auth-src/auth.json ]; then
  cp /auth-src/auth.json /var/lib/pangaea/auth/codex/auth.json
  chmod 0600 /var/lib/pangaea/auth/codex/auth.json
fi
chown -R 10001:10001 /var/lib/pangaea /work
chmod 0700 /var/lib/pangaea/auth/codex /var/lib/pangaea/home/codex /var/lib/pangaea/tmp
`)
	case provider.ServiceClaude:
		return strings.TrimSpace(`
set -eu
mkdir -p /var/lib/pangaea/auth/claude /var/lib/pangaea/home/claude /var/lib/pangaea/tmp /work
write_runtime_settings() {
  runtime_settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
  runtime_dir="$(dirname "$runtime_settings")"
  mkdir -p "$runtime_dir"
  if [ ! -s "$runtime_settings" ]; then
    {
      printf 'PANGAEA_NODE_ID=%s\n' "${PANGAEA_NODE_ID:-}"
      printf 'PANGAEA_HOST_NAME=%s\n' "${PANGAEA_HOST_NAME:-}"
      printf 'PANGAEA_PROVIDER_TYPE=%s\n' "${PANGAEA_PROVIDER_TYPE:-}"
      printf 'PANGAEA_PROVIDER_INSTANCE_ID=%s\n' "${PANGAEA_PROVIDER_INSTANCE_ID:-}"
      printf 'PANGAEA_PROVIDER_MODE=%s\n' "${PANGAEA_PROVIDER_MODE:-}"
      printf 'PANGAEA_SERVICE=%s\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%s\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%s\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%s\n' "${PANGAEA_CONTAINER_ID:-}"
    } > "$runtime_settings"
    chmod 0600 "$runtime_settings" 2>/dev/null || true
  fi
}
write_runtime_settings
if [ -s /auth-src/.credentials.json ]; then
  cp /auth-src/.credentials.json /var/lib/pangaea/auth/claude/.credentials.json
  chmod 0600 /var/lib/pangaea/auth/claude/.credentials.json
fi
chown -R 10001:10001 /var/lib/pangaea /work
chmod 0700 /var/lib/pangaea/auth/claude /var/lib/pangaea/home/claude /var/lib/pangaea/tmp
`)
	case provider.ServiceAntigravity:
		return strings.TrimSpace(`
set -eu
state_dir=/var/lib/antigravity/state/User/globalStorage
mkdir -p "${state_dir}" /var/lib/pangaea/home/antigravity /var/lib/pangaea/runtime /work
write_runtime_settings() {
  runtime_settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
  runtime_dir="$(dirname "$runtime_settings")"
  mkdir -p "$runtime_dir"
  if [ ! -s "$runtime_settings" ]; then
    {
      printf 'PANGAEA_NODE_ID=%s\n' "${PANGAEA_NODE_ID:-}"
      printf 'PANGAEA_HOST_NAME=%s\n' "${PANGAEA_HOST_NAME:-}"
      printf 'PANGAEA_PROVIDER_TYPE=%s\n' "${PANGAEA_PROVIDER_TYPE:-}"
      printf 'PANGAEA_PROVIDER_INSTANCE_ID=%s\n' "${PANGAEA_PROVIDER_INSTANCE_ID:-}"
      printf 'PANGAEA_PROVIDER_MODE=%s\n' "${PANGAEA_PROVIDER_MODE:-}"
      printf 'PANGAEA_SERVICE=%s\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%s\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%s\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%s\n' "${PANGAEA_CONTAINER_ID:-}"
    } > "$runtime_settings"
    chmod 0600 "$runtime_settings" 2>/dev/null || true
  fi
}
write_runtime_settings
if [ -s /auth-src/state.vscdb ]; then
  cp /auth-src/state.vscdb "${state_dir}/state.vscdb"
  chmod 0600 "${state_dir}/state.vscdb"
fi
chown -R 10001:10001 /var/lib/antigravity/state /var/lib/pangaea /work
chmod 0700 /var/lib/antigravity/state "${state_dir}" /var/lib/pangaea/home/antigravity
`)
	default:
		return strings.TrimSpace(`
set -eu
mkdir -p /var/lib/pangaea/home/gemini/.gemini /var/lib/pangaea/tmp /work
write_runtime_settings() {
  runtime_settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
  runtime_dir="$(dirname "$runtime_settings")"
  mkdir -p "$runtime_dir"
  if [ ! -s "$runtime_settings" ]; then
    {
      printf 'PANGAEA_NODE_ID=%s\n' "${PANGAEA_NODE_ID:-}"
      printf 'PANGAEA_HOST_NAME=%s\n' "${PANGAEA_HOST_NAME:-}"
      printf 'PANGAEA_PROVIDER_TYPE=%s\n' "${PANGAEA_PROVIDER_TYPE:-}"
      printf 'PANGAEA_PROVIDER_INSTANCE_ID=%s\n' "${PANGAEA_PROVIDER_INSTANCE_ID:-}"
      printf 'PANGAEA_PROVIDER_MODE=%s\n' "${PANGAEA_PROVIDER_MODE:-}"
      printf 'PANGAEA_SERVICE=%s\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%s\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%s\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%s\n' "${PANGAEA_CONTAINER_ID:-}"
    } > "$runtime_settings"
    chmod 0600 "$runtime_settings" 2>/dev/null || true
  fi
}
write_runtime_settings
if [ -s /auth-src/oauth_creds.json ]; then
  cp /auth-src/oauth_creds.json /var/lib/pangaea/home/gemini/.gemini/oauth_creds.json
  chmod 0600 /var/lib/pangaea/home/gemini/.gemini/oauth_creds.json
fi
if [ -s /auth-src/settings.json ]; then
  cp /auth-src/settings.json /var/lib/pangaea/home/gemini/.gemini/settings.json
else
  printf '%s\n' '{"selectedAuthType":"oauth-personal","security":{"auth":{"selectedType":"oauth-personal"}}}' > /var/lib/pangaea/home/gemini/.gemini/settings.json
fi
chown -R 10001:10001 /var/lib/pangaea /work
chmod 0700 /var/lib/pangaea/home/gemini /var/lib/pangaea/home/gemini/.gemini /var/lib/pangaea/tmp
chmod 0600 /var/lib/pangaea/home/gemini/.gemini/settings.json
`)
	}
}

func setupProviderKubernetesEnv(spec nodeagent.ProviderSpec, nodeID string, setupType string, opts setupProviderOptions) []any {
	env := renderSetupProviderEnvMap(spec, nodeID, "", setupType)
	if opts.RouterControl != "" {
		env["PANGAEA_ROUTER_CONTROL_URL"] = opts.RouterControl
	}
	if opts.RouterData != "" {
		env["PANGAEA_ROUTER_DATA_URL"] = setupProviderRouterDataURL(opts.RouterData, spec.InstanceID)
	}
	if opts.RouterPeerToken != "" {
		env["PANGAEA_ROUTER_PEER_TOKEN"] = opts.RouterPeerToken
	}
	if opts.StreamTokenKey != "" {
		env["PANGAEA_STREAM_TOKEN_KEY"] = opts.StreamTokenKey
	}
	delete(env, "PANGAEA_HOST_NAME")
	delete(env, "PANGAEA_CONTAINER_ID")
	delete(env, "PANGAEA_CONTAINER_NAME")
	out := setupProviderEnvEntries(env)
	reportedHostName := strings.TrimSpace(spec.HostName)
	if reportedHostName == "" {
		reportedHostName = "$(PANGAEA_HOST_HOSTNAME)"
	}
	out = append(out,
		map[string]any{"name": "PANGAEA_HOST_HOSTNAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "spec.nodeName"}}},
		map[string]any{"name": "PANGAEA_HOST_NAME", "value": reportedHostName},
		map[string]any{"name": "PANGAEA_CONTAINER_UID", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.uid"}}},
		map[string]any{"name": "PANGAEA_CONTAINER_ID", "value": "$(PANGAEA_CONTAINER_UID)"},
		map[string]any{"name": "PANGAEA_CONTAINER_NAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.name"}}},
	)
	return out
}

func setupProviderKubernetesBootstrapEnv(spec nodeagent.ProviderSpec, nodeID string, setupType string, _ setupProviderOptions) []any {
	hostName := strings.TrimSpace(spec.HostName)
	if hostName == "" {
		hostName = "$(PANGAEA_HOST_HOSTNAME)"
	}
	env := map[string]string{
		"PANGAEA_PROVIDER_TYPE":         spec.ProviderType,
		"PANGAEA_PROVIDER_INSTANCE_ID":  spec.InstanceID,
		"PANGAEA_PROVIDER_MODE":         spec.ProviderMode,
		"PANGAEA_NODE_ID":               nodeID,
		"PANGAEA_HOST_NAME":             hostName,
		"PANGAEA_SERVICE":               string(spec.Service),
		"PANGAEA_CONTAINER_KIND":        setupType,
		"PANGAEA_RUNTIME_SETTINGS_PATH": "/var/lib/pangaea/runtime/provider.env",
	}
	out := setupProviderEnvEntries(env)
	out = append(out,
		map[string]any{"name": "PANGAEA_HOST_HOSTNAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "spec.nodeName"}}},
		map[string]any{"name": "PANGAEA_CONTAINER_UID", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.uid"}}},
		map[string]any{"name": "PANGAEA_CONTAINER_ID", "value": "$(PANGAEA_CONTAINER_UID)"},
		map[string]any{"name": "PANGAEA_CONTAINER_NAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.name"}}},
	)
	return out
}

func setupProviderRouterDataURL(raw string, providerInstanceID string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.TrimSpace(providerInstanceID) == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	if strings.TrimSpace(q.Get("provider_instance_id")) == "" {
		q.Set("provider_instance_id", providerInstanceID)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func setupProviderEnvEntries(env map[string]string) []any {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]any{"name": key, "value": env[key]})
	}
	return out
}

func setupProviderKubernetesPullPolicy(policy string, setupType string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "never":
		return "Never"
	case "always":
		return "Always"
	default:
		if setupType == "kind" {
			return "IfNotPresent"
		}
		return "IfNotPresent"
	}
}

func renderSetupProviderEnv(spec nodeagent.ProviderSpec, nodeID string, hostName string, containerKind string) string {
	env := renderSetupProviderEnvMap(spec, nodeID, hostName, containerKind)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(systemdEnvQuote(env[key]))
		b.WriteByte('\n')
	}
	return b.String()
}

func renderSetupProviderEnvMap(spec nodeagent.ProviderSpec, nodeID string, hostName string, containerKind string) map[string]string {
	instanceID := spec.InstanceID
	if instanceID == "" {
		instanceID = spec.ProviderType + "-local"
	}
	env := map[string]string{
		"PANGAEA_PROVIDER_TYPE":         spec.ProviderType,
		"PANGAEA_PROVIDER_INSTANCE_ID":  instanceID,
		"PANGAEA_PROVIDER_MODE":         spec.ProviderMode,
		"PANGAEA_NODE_ID":               nodeID,
		"PANGAEA_HOST_NAME":             hostName,
		"PANGAEA_CONTAINER_KIND":        containerKind,
		"PANGAEA_CONTAINER_NAME":        "pangaea-" + instanceID,
		"PANGAEA_RUNTIME_SETTINGS_PATH": "/var/lib/pangaea/runtime/provider.env",
		"PANGAEA_SHIM_MODE":             string(spec.Kind),
		"PANGAEA_SERVICE":               string(spec.Service),
		"PANGAEA_ACCOUNT_DISPLAY":       spec.AccountHint,
		"PANGAEA_AUTH_PATH":             spec.Auth.ContainerPath,
		"PANGAEA_AUTH_FORMAT":           spec.Auth.Format,
		"PANGAEA_UPSTREAM_BASE_URL":     spec.Upstream.BaseURL,
		"PANGAEA_UPSTREAM_DIALECT":      spec.Upstream.Compat,
		"PANGAEA_UPSTREAM_API_KEY":      spec.Upstream.APIKey,
		"PANGAEA_UPSTREAM_API_KEY_FILE": spec.Upstream.APIKeyFile,
		"PANGAEA_UPSTREAM_API_KEY_MODE": spec.Upstream.APIKeyMode,
		"PANGAEA_SHIM_PROTOCOLS":        strings.Join(spec.Shim.Protocols, ","),
		"PANGAEA_SHIM_CAPABILITIES":     joinSetupCapabilities(spec.Shim.Capabilities),
		"PANGAEA_REFRESH_COMMAND":       setupProviderShellJoin(spec.Refresh.Command),
		"PANGAEA_REFRESH_THRESHOLD":     spec.Refresh.Threshold,
		"PANGAEA_REFRESH_COOLDOWN":      spec.Refresh.Cooldown,
		"PANGAEA_REFRESH_TIMEOUT":       spec.Refresh.Timeout,
	}
	if len(spec.Models) > 0 {
		env["PANGAEA_MODEL"] = spec.Models[0].ID
		if len(spec.Models[0].Aliases) > 0 {
			env["PANGAEA_MODEL_ALIAS"] = spec.Models[0].Aliases[0]
		}
		env["PANGAEA_MODEL_CAPABILITIES"] = joinSetupCapabilities(spec.Models[0].Capabilities)
		if setupProviderShouldEmitModelsEnv(spec) {
			env["PANGAEA_MODELS"] = setupProviderModelsEnv(spec.Models)
		}
	}
	for key, value := range spec.Env {
		env[key] = value
	}
	for key, value := range env {
		if strings.TrimSpace(value) == "" {
			delete(env, key)
		}
	}
	return env
}

func setupProviderShouldEmitModelsEnv(spec nodeagent.ProviderSpec) bool {
	return !(spec.Service == provider.ServiceGemini && spec.ProviderMode == "http-direct")
}

func renderSetupProviderSystemdUnit(spec nodeagent.ProviderSpec, envPath string) string {
	execStart := "/usr/local/bin/pangaeactl provider-shim run"
	if len(spec.Shim.Command) > 0 {
		execStart = "/bin/bash -lc " + systemdExecQuote(setupProviderShellJoin(spec.Shim.Command)+" & exec /usr/local/bin/pangaeactl provider-shim run")
	}
	return renderSystemdUnit(systemdUnitSpec{
		Description: "Pangaea provider " + spec.InstanceID,
		WorkingDir:  "/work",
		ExecStart:   execStart,
		EnvFile:     envPath,
	})
}

func setupProviderModelsEnv(models []provider.Model) string {
	items := make([]string, 0, len(models))
	for _, model := range models {
		item := model.ID
		aliases := make([]string, 0, len(model.Aliases))
		for _, alias := range model.Aliases {
			if alias != "" && alias != model.ID {
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

func setupProviderShellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func joinSetupCapabilities(capabilities []provider.Capability) string {
	items := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability != "" {
			items = append(items, string(capability))
		}
	}
	return strings.Join(items, ",")
}

func systemdEnvQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value) + `"`
}

func systemdExecQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func yamlDocuments(objects ...any) ([]byte, error) {
	var out bytes.Buffer
	for i, object := range objects {
		if i > 0 {
			out.WriteString("---\n")
		}
		raw, err := yaml.Marshal(object)
		if err != nil {
			return nil, err
		}
		out.Write(raw)
	}
	return out.Bytes(), nil
}

func fileModeForArtifact(path string) os.FileMode {
	if strings.HasSuffix(path, ".env") {
		return 0o600
	}
	if strings.HasSuffix(path, "node-agent.yaml") || strings.HasSuffix(path, "node-agent.yml") {
		return 0o600
	}
	return 0o644
}

func redactSetupProviderDryRunContent(artifact setupProviderArtifact) []byte {
	if !strings.HasSuffix(artifact.Path, ".yaml") && !strings.HasSuffix(artifact.Path, ".yml") {
		return artifact.Content
	}
	lines := strings.Split(string(artifact.Content), "\n")
	inSecretData := false
	secretDataIndent := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "data:" {
			inSecretData = true
			secretDataIndent = indent
			continue
		}
		if inSecretData {
			if trimmed == "" {
				continue
			}
			if indent <= secretDataIndent {
				inSecretData = false
				continue
			}
			key, _, ok := strings.Cut(trimmed, ":")
			if ok {
				lines[i] = strings.Repeat(" ", indent) + key + ": <redacted>"
			}
		}
		if isSetupProviderSecretYAMLKey(trimmed) {
			key, _, ok := strings.Cut(trimmed, ":")
			if ok {
				lines[i] = strings.Repeat(" ", indent) + key + ": <redacted>"
			}
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func isSetupProviderSecretYAMLKey(trimmed string) bool {
	key, _, ok := strings.Cut(trimmed, ":")
	if !ok {
		return false
	}
	switch strings.TrimSpace(key) {
	case "api_key":
		return true
	default:
		return false
	}
}

func sanitizeSetupToken(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' {
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

func cloneSetupStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSetupModels(in []provider.Model) []provider.Model {
	out := make([]provider.Model, 0, len(in))
	for _, model := range in {
		copied := model
		copied.Aliases = append([]string(nil), model.Aliases...)
		copied.Capabilities = append([]provider.Capability(nil), model.Capabilities...)
		copied.GroupMembers = append([]string(nil), model.GroupMembers...)
		out = append(out, copied)
	}
	return out
}
