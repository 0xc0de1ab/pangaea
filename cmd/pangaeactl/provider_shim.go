package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/cliprovider"
	"github.com/0xc0de1ab/pangaea/internal/codexprovider"
	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
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
	Model                    string
	ModelAlias               string
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
	cmd.Flags().StringVar(&opts.Model, "model", "", "canonical upstream model id for --api-compatible; if omitted, shim attempts upstream model discovery")
	cmd.Flags().StringVar(&opts.ModelAlias, "model-alias", "", "optional public model alias for --api-compatible")
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
		apiProvider, err := buildAPICompatibleProvider(opts)
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
		sidecarProvider, err := buildSidecarProvider(opts)
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
		apiProvider, refresher, err := buildCLIContainerProvider(ctx, opts)
		if err != nil {
			return err
		}
		return providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:           opts.RouterControlURL,
			DataURL:              opts.RouterDataURL,
			PeerToken:            opts.RouterPeerToken,
			HeartbeatInterval:    opts.HeartbeatInterval,
			TokenKey:             []byte(opts.StreamTokenKey),
			Provider:             apiProvider,
			AuthRefresher:        refresher,
			AutoRefreshThreshold: defaultRefreshThreshold(opts.RefreshThreshold),
			AutoRefreshCooldown:  defaultRefreshCooldown(opts.RefreshCooldown),
		})
	default:
		return fmt.Errorf("one of --simulator, --api-compatible, --cli-container, or --sidecar is required")
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
		case "cli-container":
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
	opts.Model = stringEnvDefault(opts.Model, "PANGAEA_MODEL")
	opts.ModelAlias = stringEnvDefault(opts.ModelAlias, "PANGAEA_MODEL_ALIAS")
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

func defaultRefreshThreshold(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return 5 * time.Minute
}

func defaultRefreshCooldown(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return 5 * time.Minute
}

func defaultAuthBootstrapTimeout(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return 30 * time.Second
}

func buildAPICompatibleProvider(opts providerShimRunOptions) (*apiprovider.Provider, error) {
	account := provider.Account{Display: opts.Account}
	return buildCompatibleProvider(opts, provider.KindAPICompatible, provider.AuthState{Status: provider.AuthHealthy, Account: account}, nil)
}

func buildSidecarProvider(opts providerShimRunOptions) (*apiprovider.Provider, error) {
	account := provider.Account{Display: opts.Account}
	auth := provider.AuthState{Status: provider.AuthHealthy, Account: account, SelectedSource: "sidecar"}
	return buildCompatibleProvider(opts, provider.KindSidecar, auth, defaultSidecarCapabilities(provider.Service(opts.Service)))
}

func buildCLIContainerProvider(ctx context.Context, opts providerShimRunOptions) (providershim.APICompatibleProvider, providershim.AuthRefresher, error) {
	if opts.AuthPath == "" {
		return nil, nil, fmt.Errorf("--auth-path is required with --cli-container")
	}
	authFormatName := opts.AuthFormat
	if authFormatName == "" {
		authFormatName = defaultAuthFormatForService(provider.Service(opts.Service))
	}
	if authFormatName == "" {
		return nil, nil, fmt.Errorf("--auth-format is required with --cli-container for service %q", opts.Service)
	}
	authFormat, ok := formats.Get(authFormatName)
	if !ok {
		return nil, nil, fmt.Errorf("unknown --auth-format %q (known: %s)", authFormatName, strings.Join(formats.List(), ", "))
	}
	if err := waitForAuthBootstrap(ctx, opts.AuthPath, defaultAuthBootstrapTimeout(opts.AuthBootstrapTimeout)); err != nil {
		return nil, nil, err
	}
	auth, err := initialAuthStateFromFile(ctx, opts.AuthPath, authFormat, time.Now)
	if err != nil {
		return nil, nil, err
	}
	auth.Refreshable = strings.TrimSpace(opts.RefreshCommand) != ""
	if auth.Account.Display == "" {
		auth.Account.Display = opts.Account
	}
	extraCaps := []provider.Capability{provider.CapabilityAuthFile}
	var refresher providershim.AuthRefresher
	if auth.Refreshable {
		extraCaps = append(extraCaps, provider.CapabilityAuthRefreshOneshot)
		refresher, err = providershim.NewCommandAuthRefresher(providershim.CommandAuthRefresherOptions{
			Command:  refreshCommandArgs(opts.RefreshCommand, opts.RefreshLoginShell),
			Timeout:  opts.RefreshTimeout,
			AuthPath: opts.AuthPath,
			Format:   authFormat,
		})
		if err != nil {
			return nil, nil, err
		}
	}
	adapter, err := normalizedCLIContainerAdapter(opts)
	if err != nil {
		return nil, nil, err
	}
	if adapter == "codex-websocket" {
		codexProvider, err := buildCodexWebSocketProvider(opts, auth, extraCaps)
		if err != nil {
			return nil, nil, err
		}
		return wrapNativeUsageProbe(codexProvider, opts.AuthPath, authFormat), refresher, nil
	}
	if adapter == "cli-oneshot" || adapter == "claude-cli" || adapter == "gemini-cli" {
		cliProvider, err := buildCLICommandProvider(opts, auth, extraCaps)
		if err != nil {
			return nil, nil, err
		}
		return wrapNativeUsageProbe(cliProvider, opts.AuthPath, authFormat), refresher, nil
	}
	if adapter == "codex-reverse-http" && isWebSocketURL(opts.UpstreamBaseURL) {
		return nil, nil, fmt.Errorf("--upstream-adapter reverse-http requires an HTTP-compatible bridge URL, got %q", opts.UpstreamBaseURL)
	}
	apiProvider, err := buildCompatibleProvider(opts, provider.KindCLIContainer, auth, extraCaps)
	if err != nil {
		return nil, nil, err
	}
	return wrapNativeUsageProbe(apiProvider, opts.AuthPath, authFormat), refresher, nil
}

func normalizedCLIContainerAdapter(opts providerShimRunOptions) (string, error) {
	adapter := strings.ToLower(strings.TrimSpace(opts.UpstreamAdapter))
	service := provider.Service(opts.Service)
	if adapter == "" {
		if service == provider.ServiceCodex && isWebSocketURL(opts.UpstreamBaseURL) {
			return "codex-websocket", nil
		}
		if (service == provider.ServiceClaude || service == provider.ServiceGemini) && strings.TrimSpace(opts.UpstreamBaseURL) == "" {
			return "cli-oneshot", nil
		}
		return "api-compatible", nil
	}
	switch adapter {
	case "api-compatible":
		return adapter, nil
	case "websocket":
		if service != provider.ServiceCodex {
			return "", fmt.Errorf("--upstream-adapter websocket is currently only supported for service codex")
		}
		return "codex-websocket", nil
	case "reverse-http":
		if service == provider.ServiceCodex {
			return "codex-reverse-http", nil
		}
		return "api-compatible", nil
	case "cli-oneshot":
		if service != provider.ServiceClaude && service != provider.ServiceGemini {
			return "", fmt.Errorf("--upstream-adapter cli-oneshot is currently supported for service claude or gemini")
		}
		return adapter, nil
	case "claude-cli":
		if service != provider.ServiceClaude {
			return "", fmt.Errorf("--upstream-adapter claude-cli requires --service claude")
		}
		return adapter, nil
	case "gemini-cli":
		if service != provider.ServiceGemini {
			return "", fmt.Errorf("--upstream-adapter gemini-cli requires --service gemini")
		}
		return adapter, nil
	case "codex-websocket", "codex-reverse-http":
		if service != provider.ServiceCodex {
			return "", fmt.Errorf("--upstream-adapter %s requires --service codex", adapter)
		}
		return adapter, nil
	default:
		return "", fmt.Errorf("unsupported --upstream-adapter %q", opts.UpstreamAdapter)
	}
}

func isWebSocketURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://")
}

func buildCodexWebSocketProvider(opts providerShimRunOptions, auth provider.AuthState, extraCapabilities []provider.Capability) (*codexprovider.Provider, error) {
	registration, err := buildProviderRegistration(opts, provider.KindCLIContainer, auth, extraCapabilities)
	if err != nil {
		return nil, err
	}
	return codexprovider.New(codexprovider.Options{
		Registration: registration,
		AppServerURL: opts.UpstreamBaseURL,
		AuthPath:     opts.AuthPath,
	})
}

func buildCLICommandProvider(opts providerShimRunOptions, auth provider.AuthState, extraCapabilities []provider.Capability) (*cliprovider.Provider, error) {
	registration, err := buildProviderRegistrationWithoutUpstream(opts, provider.KindCLIContainer, auth, extraCapabilities)
	if err != nil {
		return nil, err
	}
	return cliprovider.New(cliprovider.Options{
		Registration:   registration,
		Service:        provider.Service(opts.Service),
		RequestTimeout: opts.CLIRequestTimeout,
	})
}

func buildCompatibleProvider(opts providerShimRunOptions, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (*apiprovider.Provider, error) {
	registration, err := buildProviderRegistration(opts, kind, auth, extraCapabilities)
	if err != nil {
		return nil, err
	}
	dialect := compat.APIDialect(opts.UpstreamDialect)
	return apiprovider.New(apiprovider.Options{
		Registration:     registration,
		BaseURL:          opts.UpstreamBaseURL,
		Dialect:          dialect,
		APIKey:           opts.UpstreamAPIKey,
		APIKeyFile:       opts.UpstreamAPIKeyFile,
		APIKeyMode:       opts.UpstreamAPIKeyMode,
		APIKeyHeader:     opts.UpstreamAPIKeyHeader,
		APIKeyQueryParam: opts.UpstreamAPIKeyQueryParam,
	})
}

func buildProviderRegistration(opts providerShimRunOptions, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (provider.Registration, error) {
	return buildProviderRegistrationWithOptions(opts, kind, auth, extraCapabilities, true)
}

func buildProviderRegistrationWithoutUpstream(opts providerShimRunOptions, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (provider.Registration, error) {
	return buildProviderRegistrationWithOptions(opts, kind, auth, extraCapabilities, false)
}

func buildProviderRegistrationWithOptions(opts providerShimRunOptions, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability, requireUpstreamBaseURL bool) (provider.Registration, error) {
	if opts.ProviderID == "" {
		return provider.Registration{}, fmt.Errorf("--provider-id is required")
	}
	if opts.ProviderInstanceID == "" {
		return provider.Registration{}, fmt.Errorf("--provider-instance-id is required")
	}
	if opts.NodeID == "" {
		return provider.Registration{}, fmt.Errorf("--node-id is required")
	}
	if opts.HostName == "" {
		return provider.Registration{}, fmt.Errorf("--host-name is required")
	}
	if opts.Service == "" {
		return provider.Registration{}, fmt.Errorf("--service is required")
	}
	service := provider.Service(opts.Service)
	if !service.Valid() {
		return provider.Registration{}, fmt.Errorf("invalid --service %q", opts.Service)
	}
	if requireUpstreamBaseURL && opts.UpstreamBaseURL == "" {
		return provider.Registration{}, fmt.Errorf("--upstream-base-url is required")
	}
	dialect := compat.APIDialect(opts.UpstreamDialect)
	if !dialect.Valid() {
		return provider.Registration{}, fmt.Errorf("invalid --upstream-dialect %q", opts.UpstreamDialect)
	}
	capability, err := capabilityForDialect(dialect)
	if err != nil {
		return provider.Registration{}, err
	}
	account := auth.Account
	if account.Display == "" {
		account.Display = opts.Account
	}
	auth.Account = account
	if auth.Status == "" {
		auth.Status = provider.AuthUnknown
	}
	now := time.Now().UTC()
	aliases := []string(nil)
	if opts.ModelAlias != "" {
		aliases = []string{opts.ModelAlias}
	}
	capabilities := append([]provider.Capability{capability, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead}, extraCapabilities...)
	modelCapabilities := []provider.Capability{capability, provider.CapabilityStreamSSE}
	models := []provider.Model(nil)
	if opts.Model != "" {
		models = []provider.Model{{
			ID:           opts.Model,
			Aliases:      aliases,
			Capabilities: dedupeCapabilities(modelCapabilities),
		}}
	}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         opts.ProviderID,
			ProviderInstanceID: opts.ProviderInstanceID,
			NodeID:             opts.NodeID,
			HostName:           opts.HostName,
			Service:            service,
			Kind:               kind,
			Account:            account,
		},
		Capabilities: dedupeCapabilities(capabilities),
		Models:       models,
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         auth,
		RegisteredAt: now,
	}, nil
}

func defaultSidecarCapabilities(service provider.Service) []provider.Capability {
	switch service {
	case provider.ServiceGitHubCopilot:
		return []provider.Capability{
			provider.CapabilityCodeCompletion,
			provider.CapabilityAgentWorkspaceRead,
		}
	default:
		return nil
	}
}

type nativeUsageProbeProvider struct {
	providershim.APICompatibleProvider
	authPath string
	format   formats.Format
	probe    formats.UsageProbe
	client   *http.Client
}

func wrapNativeUsageProbe(base providershim.APICompatibleProvider, authPath string, format formats.Format) providershim.APICompatibleProvider {
	probe, ok := format.(formats.UsageProbe)
	if !ok || base == nil {
		return base
	}
	return &nativeUsageProbeProvider{
		APICompatibleProvider: base,
		authPath:              authPath,
		format:                format,
		probe:                 probe,
		client:                &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *nativeUsageProbeProvider) Usage() (provider.UsageReport, error) {
	base, baseErr := p.APICompatibleProvider.Usage()
	if base.ObservedAt.IsZero() {
		base.ObservedAt = time.Now().UTC()
	}
	if p == nil || p.probe == nil || p.format == nil || strings.TrimSpace(p.authPath) == "" {
		return base, baseErr
	}
	raw, err := os.ReadFile(p.authPath)
	if err != nil {
		return withNativeUsageProbeError(base, err), nil
	}
	snapshot, err := p.format.Parse(raw)
	if err != nil {
		return withNativeUsageProbeError(base, err), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	native, err := p.probe.Probe(ctx, snapshot, p.authPath, p.client)
	if err != nil {
		return withNativeUsageProbeError(base, err), nil
	}
	base.ObservedAt = time.Now().UTC()
	base.Source = joinUsageSources(base.Source, p.format.Name()+"/usage-probe")
	base.NativeSummary = native
	return base, nil
}

func withNativeUsageProbeError(base provider.UsageReport, err error) provider.UsageReport {
	base.ObservedAt = time.Now().UTC()
	base.Source = joinUsageSources(base.Source, "usage-probe-error")
	base.NativeSummary = map[string]any{"probe_error": err.Error()}
	return base
}

func joinUsageSources(values ...string) string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, "+") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return strings.Join(out, "+")
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

func defaultAuthFormatForService(service provider.Service) string {
	switch service {
	case provider.ServiceCodex:
		return "codex-auth-json-format"
	case provider.ServiceClaude:
		return "claude-credentials-json-format"
	case provider.ServiceGemini:
		return "gemini-oauth-creds-json-format"
	default:
		return ""
	}
}

func initialAuthStateFromFile(ctx context.Context, path string, format formats.Format, now func() time.Time) (provider.AuthState, error) {
	if now == nil {
		now = time.Now
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return provider.AuthState{}, fmt.Errorf("read auth file: %w", err)
	}
	snapshot, err := format.Parse(raw)
	if err != nil {
		return provider.AuthState{}, err
	}
	result, err := format.Validate(ctx, snapshot, formats.ValidateOpts{Clock: now})
	if err != nil {
		return provider.AuthState{}, err
	}
	auth := provider.AuthState{
		Status:          authStatusFromValidation(result.Status),
		ExpiresAt:       snapshot.ExpiresAt(),
		SelectedSource:  "container",
		BootstrapSource: "copy",
	}
	if result.Status != formats.StatusOK && result.Detail != "" {
		auth.LastRefreshErr = result.Detail
	}
	if accountAware, ok := format.(formats.AccountAware); ok {
		if id, err := accountAware.Account(ctx, snapshot, path); err == nil {
			auth.Account.ID = id
		}
	}
	if displayAware, ok := format.(formats.AccountDisplayAware); ok {
		if display, err := displayAware.AccountDisplay(ctx, snapshot, path); err == nil {
			auth.Account.Display = display
		}
	}
	return auth, nil
}

func waitForAuthBootstrap(ctx context.Context, authPath string, timeout time.Duration) error {
	if ok, err := authBootstrapReady(authPath); ok || err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("auth bootstrap file %q not ready", authPath)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("auth bootstrap file %q not ready: %w", authPath, waitCtx.Err())
		case <-ticker.C:
			ok, err := authBootstrapReady(authPath)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
	}
}

func authBootstrapReady(authPath string) (bool, error) {
	info, err := os.Stat(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat auth bootstrap file: %w", err)
	}
	if info.IsDir() {
		return false, fmt.Errorf("auth bootstrap file %q is a directory", authPath)
	}
	if info.Size() == 0 {
		return false, nil
	}
	return true, nil
}

func authStatusFromValidation(status formats.ValidationStatus) provider.AuthStatus {
	switch status {
	case formats.StatusOK:
		return provider.AuthHealthy
	case formats.StatusScopeWarn:
		return provider.AuthConflict
	case formats.StatusExpired:
		return provider.AuthExpired
	case formats.StatusRevoked:
		return provider.AuthRevoked
	case formats.StatusUnreachable:
		return provider.AuthUnavailable
	default:
		return provider.AuthUnavailable
	}
}

func refreshCommandArgs(command string, loginShell bool) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	if loginShell {
		command = `if [ -f "$HOME/.bashrc" ]; then . "$HOME/.bashrc"; fi; ` + command
		return []string{"bash", "-lc", command}
	}
	return []string{"sh", "-c", command}
}

func dedupeCapabilities(in []provider.Capability) []provider.Capability {
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

func capabilityForDialect(dialect compat.APIDialect) (provider.Capability, error) {
	switch dialect {
	case compat.APIDialectOpenAI:
		return provider.CapabilityOpenAIChat, nil
	case compat.APIDialectAnthropic:
		return provider.CapabilityAnthropicMessages, nil
	case compat.APIDialectGemini:
		return provider.CapabilityGeminiGenerateContent, nil
	default:
		return "", fmt.Errorf("unsupported upstream dialect %q", dialect)
	}
}
