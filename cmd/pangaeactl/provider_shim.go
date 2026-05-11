package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/providerfactory"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/spf13/cobra"
)

type providerShimRunOptions struct {
	RouterControlURL         string
	RouterDataURL            string
	RouterPeerToken          string
	Simulator                bool
	APICompatible            bool
	CLIContainer             bool
	Sidecar                  bool
	HeartbeatInterval        time.Duration
	StreamTokenKey           string
	ProviderType             string
	ProviderInstanceID       string
	NodeID                   string
	HostName                 string
	ContainerID              string
	ContainerKind            string
	ContainerName            string
	TargetVersion            string
	Service                  string
	Account                  string
	ProviderMode             string
	UpstreamBaseURL          string
	UpstreamDialect          string
	UpstreamAPIKey           string
	UpstreamAPIKeyFile       string
	UpstreamAPIKeyMode       string
	UpstreamAPIKeyHeader     string
	UpstreamAPIKeyQueryParam string
	ShimProtocols            string
	ShimCapabilities         string
	Model                    string
	Models                   string
	ModelAlias               string
	ModelCapabilities        string
	AuthPath                 string
	AuthFormat               string
	RefreshCommand           string
	RefreshLoginShell        bool
	CLIRequestTimeout        time.Duration
	RefreshTimeout           time.Duration
	RefreshThreshold         time.Duration
	RefreshCooldown          time.Duration
	AuthBootstrapTimeout     time.Duration
	MCPServersJSON           string
	MCPToolRounds            int
}

func newProviderShimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "provider-shim",
		Short:         common.CLIShortShim,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newProviderShimRunCmd())
	return cmd
}

func newProviderShimRunCmd() *cobra.Command {
	opts := providerShimRunOptions{}
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "connect a provider shim to the v2 router control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProviderShim(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.RouterControlURL, "router-control", "", "router control WebSocket URL")
	cmd.Flags().StringVar(&opts.RouterDataURL, "router-data", "", "router data WebSocket URL; defaults to --router-control with /data/ws and provider_instance_id")
	cmd.Flags().StringVar(&opts.RouterPeerToken, "router-peer-token", "", "optional bearer token for router control and data websocket connections")
	cmd.Flags().BoolVar(&opts.Simulator, "simulator", false, "run the built-in simulator shim")
	cmd.Flags().BoolVar(&opts.APICompatible, "api-compatible", false, "run a generic API-compatible provider shim")
	cmd.Flags().BoolVar(&opts.CLIContainer, "cli-container", false, "run a CLI-container provider shim against a local compatible upstream")
	cmd.Flags().BoolVar(&opts.Sidecar, "sidecar", false, "run a sidecar provider shim against a local compatible upstream")
	cmd.Flags().DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", 30*time.Second, "control heartbeat interval")
	cmd.Flags().StringVar(&opts.StreamTokenKey, "stream-token-key", defaultStreamTokenKey, "shared HMAC key for router-to-shim stream capability tokens")
	cmd.Flags().StringVar(&opts.ProviderType, "provider-type", "", "logical provider type for --api-compatible")
	cmd.Flags().StringVar(&opts.ProviderInstanceID, "provider-instance-id", "", "provider instance id for --api-compatible")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "node id for --api-compatible")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "operator-facing host name for --api-compatible")
	cmd.Flags().StringVar(&opts.ContainerID, "container-id", "", "container runtime id for containerized shims")
	cmd.Flags().StringVar(&opts.ContainerKind, "container-kind", "", "container runtime kind for containerized shims")
	cmd.Flags().StringVar(&opts.ContainerName, "container-name", "", "container name for containerized shims")
	cmd.Flags().StringVar(&opts.TargetVersion, "target-version", "", "target CLI/server version reported for this provider")
	cmd.Flags().StringVar(&opts.Service, "service", "", "provider service family for --api-compatible, such as glm, minimax, deepseek")
	cmd.Flags().StringVar(&opts.Account, "account", "", "operator-facing account label for --api-compatible")
	cmd.Flags().StringVar(&opts.ProviderMode, "provider-mode", "", "provider adapter mode for --cli-container (http-direct|app-server|cli-adapter|acp|ls-core-sidecar)")
	cmd.Flags().StringVar(&opts.UpstreamBaseURL, "upstream-base-url", "", "upstream compatible API base URL for --api-compatible")
	cmd.Flags().StringVar(&opts.UpstreamDialect, "upstream-dialect", "openai", "upstream API dialect for --api-compatible (openai|anthropic|gemini)")
	cmd.Flags().StringVar(&opts.UpstreamAPIKey, "upstream-api-key", "", "upstream API key for --api-compatible")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyFile, "upstream-api-key-file", "", "path to an upstream API key file; re-read before each upstream request")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyMode, "upstream-api-key-mode", "", "upstream API key placement (bearer|header|query|none; default bearer)")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyHeader, "upstream-api-key-header", "", "header name for --upstream-api-key-mode bearer or header")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyQueryParam, "upstream-api-key-query-param", "", "query parameter name for --upstream-api-key-mode query")
	cmd.Flags().StringVar(&opts.ShimProtocols, "shim-protocols", "", "comma-separated advertised API protocols (openai,anthropic,gemini); used to derive capabilities when --shim-capabilities is omitted")
	cmd.Flags().StringVar(&opts.ShimCapabilities, "shim-capabilities", "", "comma-separated advertised provider capabilities")
	cmd.Flags().StringVar(&opts.Model, "model", "", "canonical upstream model id for --api-compatible; if omitted, shim attempts upstream model discovery")
	cmd.Flags().StringVar(&opts.Models, "models", "", "comma-separated provider model list; each item may be model or model=alias1|alias2")
	cmd.Flags().StringVar(&opts.ModelAlias, "model-alias", "", "optional public model alias for --api-compatible")
	cmd.Flags().StringVar(&opts.ModelCapabilities, "model-capabilities", "", "comma-separated advertised capabilities for --model")
	cmd.Flags().StringVar(&opts.AuthPath, "auth-path", "", "container-local auth file path for --cli-container")
	cmd.Flags().StringVar(&opts.AuthFormat, "auth-format", "", "auth format name for --cli-container; defaults from --service when known")
	cmd.Flags().DurationVar(&opts.AuthBootstrapTimeout, "auth-bootstrap-timeout", 30*time.Second, "maximum time for --cli-container to wait for copied auth file")
	cmd.Flags().StringVar(&opts.RefreshCommand, "refresh-command", "", "oneshot auth refresh command for --cli-container")
	cmd.Flags().BoolVar(&opts.RefreshLoginShell, "refresh-login-shell", true, "run --refresh-command through bash with ~/.bashrc sourced")
	cmd.Flags().DurationVar(&opts.CLIRequestTimeout, "cli-request-timeout", 5*time.Minute, "maximum duration for --cli-container cli-adapter invocations")
	cmd.Flags().DurationVar(&opts.RefreshTimeout, "refresh-timeout", 2*time.Minute, "maximum duration for --refresh-command")
	cmd.Flags().DurationVar(&opts.RefreshThreshold, "refresh-threshold", 5*time.Minute, "auth expiry window that triggers automatic --refresh-command for --cli-container")
	cmd.Flags().DurationVar(&opts.RefreshCooldown, "refresh-cooldown", 5*time.Minute, "minimum interval between automatic auth refresh attempts for --cli-container")
	cmd.Flags().StringVar(&opts.MCPServersJSON, "mcp-servers-json", "", "JSON MCP stdio server config for direct-http CLI-container providers")
	cmd.Flags().IntVar(&opts.MCPToolRounds, "mcp-tool-rounds", 4, "maximum direct-http MCP/tool continuation rounds")
	return cmd
}

func runProviderShim(ctx context.Context, opts providerShimRunOptions) error {
	opts = applyProviderShimEnvDefaults(opts)
	if opts.RouterControlURL == "" {
		return fmt.Errorf("--router-control is required")
	}
	switch {
	case enabledModes(opts) > 1:
		return fmt.Errorf("choose only one of --simulator, --api-compatible, --cli-container, or --sidecar")
	case opts.Simulator:
		sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible})
		if err != nil {
			return err
		}
		return providershim.RunSimulatorShim(ctx, providershim.SimulatorShimOptions{
			ControlURL:        opts.RouterControlURL,
			DataURL:           opts.RouterDataURL,
			PeerToken:         opts.RouterPeerToken,
			HeartbeatInterval: opts.HeartbeatInterval,
			TokenKey:          []byte(opts.StreamTokenKey),
			Simulator:         sim,
		})
	case opts.APICompatible:
		apiProvider, err := providerfactory.BuildAPICompatibleProvider(providerFactoryConfigFromOptions(opts))
		if err != nil {
			return err
		}
		return providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:        opts.RouterControlURL,
			DataURL:           opts.RouterDataURL,
			PeerToken:         opts.RouterPeerToken,
			HeartbeatInterval: opts.HeartbeatInterval,
			TokenKey:          []byte(opts.StreamTokenKey),
			Provider:          apiProvider,
		})
	case opts.Sidecar:
		result, err := providerfactory.BuildSidecarProviderWithRefresh(providerFactoryConfigFromOptions(opts))
		if err != nil {
			return err
		}
		return providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:           opts.RouterControlURL,
			DataURL:              opts.RouterDataURL,
			PeerToken:            opts.RouterPeerToken,
			HeartbeatInterval:    opts.HeartbeatInterval,
			TokenKey:             []byte(opts.StreamTokenKey),
			Provider:             result.Provider,
			AuthRefresher:        result.AuthRefresher,
			AutoRefreshThreshold: result.AutoRefreshThreshold,
			AutoRefreshCooldown:  result.AutoRefreshCooldown,
		})
	case opts.CLIContainer:
		result, err := providerfactory.BuildCLIContainerProvider(ctx, providerFactoryConfigFromOptions(opts))
		if err != nil {
			return err
		}
		return providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:           opts.RouterControlURL,
			DataURL:              opts.RouterDataURL,
			PeerToken:            opts.RouterPeerToken,
			HeartbeatInterval:    opts.HeartbeatInterval,
			TokenKey:             []byte(opts.StreamTokenKey),
			Provider:             result.Provider,
			AuthRefresher:        result.AuthRefresher,
			AutoRefreshThreshold: result.AutoRefreshThreshold,
			AutoRefreshCooldown:  result.AutoRefreshCooldown,
		})
	default:
		return fmt.Errorf("one of --simulator, --api-compatible, --cli-container, or --sidecar is required")
	}
}

func providerFactoryConfigFromOptions(opts providerShimRunOptions) providerfactory.Config {
	return providerfactory.Config{
		ProviderType:             opts.ProviderType,
		ProviderInstanceID:       opts.ProviderInstanceID,
		NodeID:                   opts.NodeID,
		HostName:                 opts.HostName,
		ContainerID:              opts.ContainerID,
		ContainerKind:            opts.ContainerKind,
		ContainerName:            opts.ContainerName,
		TargetVersion:            opts.TargetVersion,
		Service:                  opts.Service,
		Account:                  opts.Account,
		ProviderMode:             opts.ProviderMode,
		UpstreamBaseURL:          opts.UpstreamBaseURL,
		UpstreamDialect:          opts.UpstreamDialect,
		UpstreamAPIKey:           opts.UpstreamAPIKey,
		UpstreamAPIKeyFile:       opts.UpstreamAPIKeyFile,
		UpstreamAPIKeyMode:       opts.UpstreamAPIKeyMode,
		UpstreamAPIKeyHeader:     opts.UpstreamAPIKeyHeader,
		UpstreamAPIKeyQueryParam: opts.UpstreamAPIKeyQueryParam,
		ShimProtocols:            opts.ShimProtocols,
		ShimCapabilities:         opts.ShimCapabilities,
		Model:                    opts.Model,
		Models:                   opts.Models,
		ModelAlias:               opts.ModelAlias,
		ModelCapabilities:        opts.ModelCapabilities,
		AuthPath:                 opts.AuthPath,
		AuthFormat:               opts.AuthFormat,
		RefreshCommand:           opts.RefreshCommand,
		RefreshLoginShell:        opts.RefreshLoginShell,
		CLIRequestTimeout:        opts.CLIRequestTimeout,
		RefreshTimeout:           opts.RefreshTimeout,
		RefreshThreshold:         opts.RefreshThreshold,
		RefreshCooldown:          opts.RefreshCooldown,
		AuthBootstrapTimeout:     opts.AuthBootstrapTimeout,
		MCPServersJSON:           opts.MCPServersJSON,
		MCPToolRounds:            opts.MCPToolRounds,
	}
}

func applyProviderShimEnvDefaults(opts providerShimRunOptions) providerShimRunOptions {
	mode := strings.TrimSpace(os.Getenv("PANGAEA_SHIM_MODE"))
	if enabledModes(opts) == 0 {
		switch mode {
		case "simulator":
			opts.Simulator = true
		case "api-compatible":
			opts.APICompatible = true
		case "cli-container", "app-server":
			opts.CLIContainer = true
		case "sidecar", "sidecar-agent":
			opts.Sidecar = true
		}
	}
	opts.RouterControlURL = stringEnvDefault(opts.RouterControlURL, "PANGAEA_ROUTER_CONTROL_URL")
	opts.RouterDataURL = stringEnvDefault(opts.RouterDataURL, "PANGAEA_ROUTER_DATA_URL")
	opts.RouterPeerToken = stringEnvDefault(opts.RouterPeerToken, "PANGAEA_ROUTER_PEER_TOKEN")
	opts.StreamTokenKey = stringEnvDefaultWhenDefault(opts.StreamTokenKey, defaultStreamTokenKey, "PANGAEA_STREAM_TOKEN_KEY")
	opts.ProviderType = stringEnvDefault(opts.ProviderType, "PANGAEA_PROVIDER_TYPE")
	opts.ProviderInstanceID = stringEnvDefault(opts.ProviderInstanceID, "PANGAEA_PROVIDER_INSTANCE_ID")
	opts.NodeID = stringEnvDefault(opts.NodeID, "PANGAEA_NODE_ID")
	if strings.TrimSpace(opts.NodeID) == "" {
		opts.NodeID = providerShimNodeIDFromRuntimeSettings()
	}
	if strings.TrimSpace(opts.HostName) == "" {
		opts.HostName = providerShimHostNameFromEnv()
	}
	opts.ContainerID = stringEnvDefault(opts.ContainerID, "PANGAEA_CONTAINER_ID")
	opts.ContainerKind = stringEnvDefault(opts.ContainerKind, "PANGAEA_CONTAINER_KIND")
	opts.ContainerName = stringEnvDefault(opts.ContainerName, "PANGAEA_CONTAINER_NAME")
	opts.TargetVersion = stringEnvDefault(opts.TargetVersion, "PANGAEA_TARGET_VERSION")
	if strings.TrimSpace(opts.ContainerID) == "" && strings.TrimSpace(opts.ContainerKind) != "" {
		opts.ContainerID = firstStringEnv("PANGAEA_CONTAINER_UID", "POD_UID", "HOSTNAME")
	}
	opts.Service = stringEnvDefault(opts.Service, "PANGAEA_SERVICE")
	opts.Account = stringEnvDefault(opts.Account, "PANGAEA_ACCOUNT")
	opts.Account = stringEnvDefault(opts.Account, "PANGAEA_ACCOUNT_DISPLAY")
	opts.ProviderMode = stringEnvDefault(opts.ProviderMode, "PANGAEA_PROVIDER_MODE")
	opts.UpstreamBaseURL = stringEnvDefault(opts.UpstreamBaseURL, "PANGAEA_UPSTREAM_BASE_URL")
	opts.UpstreamDialect = stringEnvDefaultWhenDefault(opts.UpstreamDialect, "openai", "PANGAEA_UPSTREAM_DIALECT")
	opts.UpstreamAPIKey = stringEnvDefault(opts.UpstreamAPIKey, "PANGAEA_UPSTREAM_API_KEY")
	opts.UpstreamAPIKeyFile = stringEnvDefault(opts.UpstreamAPIKeyFile, "PANGAEA_UPSTREAM_API_KEY_FILE")
	opts.UpstreamAPIKeyMode = stringEnvDefault(opts.UpstreamAPIKeyMode, "PANGAEA_UPSTREAM_API_KEY_MODE")
	opts.UpstreamAPIKeyHeader = stringEnvDefault(opts.UpstreamAPIKeyHeader, "PANGAEA_UPSTREAM_API_KEY_HEADER")
	opts.UpstreamAPIKeyQueryParam = stringEnvDefault(opts.UpstreamAPIKeyQueryParam, "PANGAEA_UPSTREAM_API_KEY_QUERY_PARAM")
	opts.ShimProtocols = stringEnvDefault(opts.ShimProtocols, "PANGAEA_SHIM_PROTOCOLS")
	opts.ShimCapabilities = stringEnvDefault(opts.ShimCapabilities, "PANGAEA_SHIM_CAPABILITIES")
	opts.ShimCapabilities = stringEnvDefault(opts.ShimCapabilities, "PANGAEA_PROVIDER_CAPABILITIES")
	opts.Model = stringEnvDefault(opts.Model, "PANGAEA_MODEL")
	opts.Models = stringEnvDefault(opts.Models, "PANGAEA_MODELS")
	opts.ModelAlias = stringEnvDefault(opts.ModelAlias, "PANGAEA_MODEL_ALIAS")
	opts.ModelCapabilities = stringEnvDefault(opts.ModelCapabilities, "PANGAEA_MODEL_CAPABILITIES")
	opts.AuthPath = stringEnvDefault(opts.AuthPath, "PANGAEA_AUTH_PATH")
	opts.AuthFormat = stringEnvDefault(opts.AuthFormat, "PANGAEA_AUTH_FORMAT")
	opts.RefreshCommand = stringEnvDefault(opts.RefreshCommand, "PANGAEA_REFRESH_COMMAND")
	if raw, ok := os.LookupEnv("PANGAEA_REFRESH_LOGIN_SHELL"); ok {
		opts.RefreshLoginShell = parseEnvBool(raw, opts.RefreshLoginShell)
	}
	if raw, ok := os.LookupEnv("PANGAEA_CLI_REQUEST_TIMEOUT"); ok {
		if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil {
			opts.CLIRequestTimeout = parsed
		}
	}
	if raw, ok := os.LookupEnv("PANGAEA_REFRESH_TIMEOUT"); ok {
		if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil {
			opts.RefreshTimeout = parsed
		}
	}
	if raw, ok := os.LookupEnv("PANGAEA_REFRESH_THRESHOLD"); ok {
		if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil {
			opts.RefreshThreshold = parsed
		}
	}
	if raw, ok := os.LookupEnv("PANGAEA_REFRESH_COOLDOWN"); ok {
		if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil {
			opts.RefreshCooldown = parsed
		}
	}
	if raw, ok := os.LookupEnv("PANGAEA_AUTH_BOOTSTRAP_TIMEOUT"); ok {
		if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil {
			opts.AuthBootstrapTimeout = parsed
		}
	}
	opts.MCPServersJSON = stringEnvDefault(opts.MCPServersJSON, "PANGAEA_MCP_SERVERS_JSON")
	if raw, ok := os.LookupEnv("PANGAEA_MCP_TOOL_ROUNDS"); ok {
		if parsed, err := parsePositiveInt(raw); err == nil {
			opts.MCPToolRounds = parsed
		}
	}
	return opts
}

func providerShimNodeIDFromRuntimeSettings() string {
	path := strings.TrimSpace(os.Getenv("PANGAEA_RUNTIME_SETTINGS_PATH"))
	if path == "" {
		path = "/var/lib/pangaea/runtime/provider.env"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "PANGAEA_NODE_ID" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if validSetupNodeID(value) {
			return value
		}
	}
	return ""
}

func providerShimHostNameFromEnv() string {
	hostName := strings.TrimSpace(os.Getenv("PANGAEA_HOST_NAME"))
	hostSideName := firstStringEnv("PANGAEA_HOST_HOSTNAME", "PANGAEA_NODE_HOST_NAME")
	if hostSideName != "" && (hostName == "" || hostName == strings.TrimSpace(os.Getenv("HOSTNAME"))) {
		return hostSideName
	}
	if hostName != "" {
		return hostName
	}
	return hostSideName
}

func stringEnvDefault(current string, name string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(os.Getenv(name))
}

func firstStringEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func stringEnvDefaultWhenDefault(current string, defaultValue string, name string) string {
	if strings.TrimSpace(current) != "" && strings.TrimSpace(current) != defaultValue {
		return current
	}
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return current
}

func parseEnvBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	case "0", "f", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parsePositiveInt(raw string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("negative integer %d", parsed)
	}
	return parsed, nil
}

func enabledModes(opts providerShimRunOptions) int {
	count := 0
	if opts.Simulator {
		count++
	}
	if opts.APICompatible {
		count++
	}
	if opts.CLIContainer {
		count++
	}
	if opts.Sidecar {
		count++
	}
	return count
}
