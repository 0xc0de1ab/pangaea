package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
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
	Simulator                bool
	APICompatible            bool
	CLIContainer             bool
	HeartbeatInterval        time.Duration
	StreamTokenKey           string
	ProviderID               string
	ProviderInstanceID       string
	NodeID                   string
	HostName                 string
	Service                  string
	Account                  string
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
	cmd.Flags().BoolVar(&opts.Simulator, "simulator", false, "run the built-in simulator shim")
	cmd.Flags().BoolVar(&opts.APICompatible, "api-compatible", false, "run a generic API-compatible provider shim")
	cmd.Flags().BoolVar(&opts.CLIContainer, "cli-container", false, "run a CLI-container provider shim against a local compatible upstream")
	cmd.Flags().DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", 30*time.Second, "control heartbeat interval")
	cmd.Flags().StringVar(&opts.StreamTokenKey, "stream-token-key", defaultStreamTokenKey, "shared HMAC key for router-to-shim stream capability tokens")
	cmd.Flags().StringVar(&opts.ProviderID, "provider-id", "", "logical provider id for --api-compatible")
	cmd.Flags().StringVar(&opts.ProviderInstanceID, "provider-instance-id", "", "provider instance id for --api-compatible")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "node id for --api-compatible")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "operator-facing host name for --api-compatible")
	cmd.Flags().StringVar(&opts.Service, "service", "", "provider service family for --api-compatible, such as glm, minimax, deepseek")
	cmd.Flags().StringVar(&opts.Account, "account", "", "operator-facing account label for --api-compatible")
	cmd.Flags().StringVar(&opts.UpstreamBaseURL, "upstream-base-url", "", "upstream compatible API base URL for --api-compatible")
	cmd.Flags().StringVar(&opts.UpstreamDialect, "upstream-dialect", "openai", "upstream API dialect for --api-compatible (openai|anthropic|gemini)")
	cmd.Flags().StringVar(&opts.UpstreamAPIKey, "upstream-api-key", "", "upstream API key for --api-compatible")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyFile, "upstream-api-key-file", "", "path to an upstream API key file; re-read before each upstream request")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyMode, "upstream-api-key-mode", "", "upstream API key placement (bearer|header|query|none; default bearer)")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyHeader, "upstream-api-key-header", "", "header name for --upstream-api-key-mode bearer or header")
	cmd.Flags().StringVar(&opts.UpstreamAPIKeyQueryParam, "upstream-api-key-query-param", "", "query parameter name for --upstream-api-key-mode query")
	cmd.Flags().StringVar(&opts.Model, "model", "", "canonical upstream model id for --api-compatible")
	cmd.Flags().StringVar(&opts.ModelAlias, "model-alias", "", "optional public model alias for --api-compatible")
	cmd.Flags().StringVar(&opts.AuthPath, "auth-path", "", "container-local auth file path for --cli-container")
	cmd.Flags().StringVar(&opts.AuthFormat, "auth-format", "", "auth format name for --cli-container; defaults from --service when known")
	cmd.Flags().DurationVar(&opts.AuthBootstrapTimeout, "auth-bootstrap-timeout", 30*time.Second, "maximum time for --cli-container to wait for copied auth file")
	cmd.Flags().StringVar(&opts.RefreshCommand, "refresh-command", "", "oneshot auth refresh command for --cli-container")
	cmd.Flags().BoolVar(&opts.RefreshLoginShell, "refresh-login-shell", true, "run --refresh-command through bash with ~/.bashrc sourced")
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
		return fmt.Errorf("choose only one of --simulator, --api-compatible, or --cli-container")
	case opts.Simulator:
		sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible})
		if err != nil {
			return err
		}
		return providershim.RunSimulatorShim(ctx, providershim.SimulatorShimOptions{
			ControlURL:        opts.RouterControlURL,
			DataURL:           opts.RouterDataURL,
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
			HeartbeatInterval: opts.HeartbeatInterval,
			TokenKey:          []byte(opts.StreamTokenKey),
			Provider:          apiProvider,
		})
	case opts.CLIContainer:
		apiProvider, refresher, err := buildCLIContainerProvider(ctx, opts)
		if err != nil {
			return err
		}
		return providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:           opts.RouterControlURL,
			DataURL:              opts.RouterDataURL,
			HeartbeatInterval:    opts.HeartbeatInterval,
			TokenKey:             []byte(opts.StreamTokenKey),
			Provider:             apiProvider,
			AuthRefresher:        refresher,
			AutoRefreshThreshold: defaultRefreshThreshold(opts.RefreshThreshold),
			AutoRefreshCooldown:  defaultRefreshCooldown(opts.RefreshCooldown),
		})
	default:
		return fmt.Errorf("one of --simulator, --api-compatible, or --cli-container is required")
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
		}
	}
	opts.RouterControlURL = stringEnvDefault(opts.RouterControlURL, "PANGAEA_ROUTER_CONTROL_URL")
	opts.RouterDataURL = stringEnvDefault(opts.RouterDataURL, "PANGAEA_ROUTER_DATA_URL")
	opts.StreamTokenKey = stringEnvDefault(opts.StreamTokenKey, "PANGAEA_STREAM_TOKEN_KEY")
	opts.ProviderID = stringEnvDefault(opts.ProviderID, "PANGAEA_PROVIDER_ID")
	opts.ProviderInstanceID = stringEnvDefault(opts.ProviderInstanceID, "PANGAEA_PROVIDER_INSTANCE_ID")
	opts.NodeID = stringEnvDefault(opts.NodeID, "PANGAEA_NODE_ID")
	opts.HostName = stringEnvDefault(opts.HostName, "PANGAEA_HOST_NAME")
	opts.Service = stringEnvDefault(opts.Service, "PANGAEA_SERVICE")
	opts.Account = stringEnvDefault(opts.Account, "PANGAEA_ACCOUNT")
	opts.Account = stringEnvDefault(opts.Account, "PANGAEA_ACCOUNT_DISPLAY")
	opts.UpstreamBaseURL = stringEnvDefault(opts.UpstreamBaseURL, "PANGAEA_UPSTREAM_BASE_URL")
	opts.UpstreamDialect = stringEnvDefault(opts.UpstreamDialect, "PANGAEA_UPSTREAM_DIALECT")
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

func buildCLIContainerProvider(ctx context.Context, opts providerShimRunOptions) (*apiprovider.Provider, providershim.AuthRefresher, error) {
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
	apiProvider, err := buildCompatibleProvider(opts, provider.KindCLIContainer, auth, extraCaps)
	if err != nil {
		return nil, nil, err
	}
	return apiProvider, refresher, nil
}

func buildCompatibleProvider(opts providerShimRunOptions, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (*apiprovider.Provider, error) {
	if opts.ProviderID == "" {
		return nil, fmt.Errorf("--provider-id is required")
	}
	if opts.ProviderInstanceID == "" {
		return nil, fmt.Errorf("--provider-instance-id is required")
	}
	if opts.NodeID == "" {
		return nil, fmt.Errorf("--node-id is required")
	}
	if opts.HostName == "" {
		return nil, fmt.Errorf("--host-name is required")
	}
	if opts.Service == "" {
		return nil, fmt.Errorf("--service is required")
	}
	service := provider.Service(opts.Service)
	if !service.Valid() {
		return nil, fmt.Errorf("invalid --service %q", opts.Service)
	}
	if opts.UpstreamBaseURL == "" {
		return nil, fmt.Errorf("--upstream-base-url is required")
	}
	dialect := compat.APIDialect(opts.UpstreamDialect)
	if !dialect.Valid() {
		return nil, fmt.Errorf("invalid --upstream-dialect %q", opts.UpstreamDialect)
	}
	if opts.Model == "" {
		return nil, fmt.Errorf("--model is required with --api-compatible")
	}
	capability, err := capabilityForDialect(dialect)
	if err != nil {
		return nil, err
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
	return apiprovider.New(apiprovider.Options{
		Registration: provider.Registration{
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
			Models: []provider.Model{{
				ID:           opts.Model,
				Aliases:      aliases,
				Capabilities: dedupeCapabilities(modelCapabilities),
			}},
			Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
			Auth:         auth,
			RegisteredAt: now,
		},
		BaseURL:          opts.UpstreamBaseURL,
		Dialect:          dialect,
		APIKey:           opts.UpstreamAPIKey,
		APIKeyFile:       opts.UpstreamAPIKeyFile,
		APIKeyMode:       opts.UpstreamAPIKeyMode,
		APIKeyHeader:     opts.UpstreamAPIKeyHeader,
		APIKeyQueryParam: opts.UpstreamAPIKeyQueryParam,
	})
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
