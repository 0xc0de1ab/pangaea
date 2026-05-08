package main

import (
	"context"
	"fmt"
	"os"
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
	ProviderID               string
	ProviderInstanceID       string
	NodeID                   string
	HostName                 string
	Service                  string
	Account                  string
	UpstreamAdapter          string
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
	cmd.Flags().StringVar(&opts.ProviderID, "provider-id", "", "logical provider id for --api-compatible")
	cmd.Flags().StringVar(&opts.ProviderInstanceID, "provider-instance-id", "", "provider instance id for --api-compatible")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "node id for --api-compatible")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "operator-facing host name for --api-compatible")
	cmd.Flags().StringVar(&opts.Service, "service", "", "provider service family for --api-compatible, such as glm, minimax, deepseek")
	cmd.Flags().StringVar(&opts.Account, "account", "", "operator-facing account label for --api-compatible")
	cmd.Flags().StringVar(&opts.UpstreamAdapter, "upstream-adapter", "", "upstream adapter for --cli-container (api-compatible|websocket|reverse-http|cli-oneshot|codex-websocket|codex-reverse-http|claude-cli|gemini-cli)")
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
	cmd.Flags().StringVar(&opts.ModelAlias, "model-alias", "", "optional public model alias for --api-compatible")
	cmd.Flags().StringVar(&opts.ModelCapabilities, "model-capabilities", "", "comma-separated advertised capabilities for --model")
	cmd.Flags().StringVar(&opts.AuthPath, "auth-path", "", "container-local auth file path for --cli-container")
	cmd.Flags().StringVar(&opts.AuthFormat, "auth-format", "", "auth format name for --cli-container; defaults from --service when known")
	cmd.Flags().DurationVar(&opts.AuthBootstrapTimeout, "auth-bootstrap-timeout", 30*time.Second, "maximum time for --cli-container to wait for copied auth file")
	cmd.Flags().StringVar(&opts.RefreshCommand, "refresh-command", "", "oneshot auth refresh command for --cli-container")
	cmd.Flags().BoolVar(&opts.RefreshLoginShell, "refresh-login-shell", true, "run --refresh-command through bash with ~/.bashrc sourced")
	cmd.Flags().DurationVar(&opts.CLIRequestTimeout, "cli-request-timeout", 5*time.Minute, "maximum duration for --cli-container cli-oneshot invocations")
	cmd.Flags().DurationVar(&opts.RefreshTimeout, "refresh-timeout", 2*time.Minute, "maximum duration for --refresh-command")
	cmd.Flags().DurationVar(&opts.RefreshThreshold, "refresh-threshold", 5*time.Minute, "auth expiry window that triggers automatic --refresh-command for --cli-container")
	cmd.Flags().DurationVar(&opts.RefreshCooldown, "refresh-cooldown", 5*time.Minute, "minimum interval between automatic auth refresh attempts for --cli-container")
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
		sidecarProvider, err := providerfactory.BuildSidecarProvider(providerFactoryConfigFromOptions(opts))
		if err != nil {
			return err
		}
		return providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:        opts.RouterControlURL,
			DataURL:           opts.RouterDataURL,
			PeerToken:         opts.RouterPeerToken,
			HeartbeatInterval: opts.HeartbeatInterval,
			TokenKey:          []byte(opts.StreamTokenKey),
			Provider:          sidecarProvider,
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
		ProviderID:               opts.ProviderID,
		ProviderInstanceID:       opts.ProviderInstanceID,
		NodeID:                   opts.NodeID,
		HostName:                 opts.HostName,
		Service:                  opts.Service,
		Account:                  opts.Account,
		UpstreamAdapter:          opts.UpstreamAdapter,
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
	opts.ProviderID = stringEnvDefault(opts.ProviderID, "PANGAEA_PROVIDER_ID")
	opts.ProviderInstanceID = stringEnvDefault(opts.ProviderInstanceID, "PANGAEA_PROVIDER_INSTANCE_ID")
	opts.NodeID = stringEnvDefault(opts.NodeID, "PANGAEA_NODE_ID")
	opts.HostName = stringEnvDefault(opts.HostName, "PANGAEA_HOST_NAME")
	opts.Service = stringEnvDefault(opts.Service, "PANGAEA_SERVICE")
	opts.Account = stringEnvDefault(opts.Account, "PANGAEA_ACCOUNT")
	opts.Account = stringEnvDefault(opts.Account, "PANGAEA_ACCOUNT_DISPLAY")
	opts.UpstreamAdapter = stringEnvDefault(opts.UpstreamAdapter, "PANGAEA_UPSTREAM_ADAPTER")
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
	return opts
}

func stringEnvDefault(current string, name string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(os.Getenv(name))
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
