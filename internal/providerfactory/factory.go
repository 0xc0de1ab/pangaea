package providerfactory

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
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

type Config struct {
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

type BuildResult struct {
	Provider             providershim.APICompatibleProvider
	AuthRefresher        providershim.AuthRefresher
	AutoRefreshThreshold time.Duration
	AutoRefreshCooldown  time.Duration
}

type Definition struct {
	Service                  provider.Service
	DefaultAuthFormat        string
	SidecarCapabilities      []provider.Capability
	NormalizeCLIAdapter      func(Config) (string, error)
	BuildCLIContainerAdapter func(context.Context, BuildContext, string) (providershim.APICompatibleProvider, bool, error)
}

type BuildContext struct {
	Config            Config
	Auth              provider.AuthState
	ExtraCapabilities []provider.Capability
	AuthFormat        formats.Format
}

type Registry struct {
	definitions map[provider.Service]Definition
}

func NewRegistry(definitions ...Definition) (*Registry, error) {
	registry := &Registry{definitions: make(map[provider.Service]Definition, len(definitions))}
	for _, definition := range definitions {
		service := definition.Service
		if !service.Valid() {
			return nil, fmt.Errorf("invalid provider definition service %q", service)
		}
		if _, exists := registry.definitions[service]; exists {
			return nil, fmt.Errorf("duplicate provider definition for service %q", service)
		}
		registry.definitions[service] = definition
	}
	return registry, nil
}

func (r *Registry) Definition(service provider.Service) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.definitions[service]
	return definition, ok
}

func DefaultRegistry() *Registry {
	registry, err := NewRegistry(
		Definition{
			Service:                  provider.ServiceCodex,
			DefaultAuthFormat:        "codex-auth-json-format",
			NormalizeCLIAdapter:      normalizeCodexCLIAdapter,
			BuildCLIContainerAdapter: buildCodexCLIContainerAdapter,
		},
		Definition{
			Service:             provider.ServiceClaude,
			DefaultAuthFormat:   "claude-credentials-json-format",
			NormalizeCLIAdapter: normalizeCLIOneshotAdapter(provider.ServiceClaude),
		},
		Definition{
			Service:             provider.ServiceGemini,
			DefaultAuthFormat:   "gemini-oauth-creds-json-format",
			NormalizeCLIAdapter: normalizeCLIOneshotAdapter(provider.ServiceGemini),
		},
		Definition{
			Service: provider.ServiceAntigravity,
			SidecarCapabilities: []provider.Capability{
				provider.CapabilityAntigravitySidecar,
				provider.CapabilityAgentToolUse,
				provider.CapabilityAgentWorkspaceRead,
				provider.CapabilityAgentWorkspaceWrite,
			},
		},
		Definition{
			Service: provider.ServiceGitHubCopilot,
			SidecarCapabilities: []provider.Capability{
				provider.CapabilityCodeCompletion,
				provider.CapabilityAgentWorkspaceRead,
			},
		},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func BuildAPICompatibleProvider(cfg Config) (*apiprovider.Provider, error) {
	return DefaultRegistry().BuildAPICompatibleProvider(cfg)
}

func (r *Registry) BuildAPICompatibleProvider(cfg Config) (*apiprovider.Provider, error) {
	account := provider.Account{Display: cfg.Account}
	extraCaps := []provider.Capability{provider.CapabilityAuthAPIKey}
	return r.buildCompatibleProvider(cfg, provider.KindAPICompatible, provider.AuthState{Status: provider.AuthHealthy, Account: account}, extraCaps)
}

func BuildSidecarProvider(cfg Config) (*apiprovider.Provider, error) {
	return DefaultRegistry().BuildSidecarProvider(cfg)
}

func (r *Registry) BuildSidecarProvider(cfg Config) (*apiprovider.Provider, error) {
	service := provider.Service(cfg.Service)
	definition, _ := r.Definition(service)
	account := provider.Account{Display: cfg.Account}
	selectedSource := "sidecar"
	expiresAt := time.Time{}
	if service == provider.ServiceAntigravity && strings.TrimSpace(cfg.AuthPath) != "" {
		if extracted, ok := antigravityAccountFromStateFile(cfg.AuthPath); ok {
			account = extracted
			selectedSource = "antigravity-state-vscdb"
		}
		if expiry, ok := antigravityOAuthExpiryFromStateFile(cfg.AuthPath); ok {
			expiresAt = expiry
		}
	}
	auth := provider.AuthState{Status: provider.AuthHealthy, Account: account, ExpiresAt: expiresAt, SelectedSource: selectedSource}
	if service == provider.ServiceAntigravity && !expiresAt.IsZero() {
		now := time.Now().UTC()
		switch {
		case !expiresAt.After(now):
			auth.Status = provider.AuthExpired
			auth.LastRefreshErr = "antigravity oauth token expired"
		case expiresAt.Before(now.Add(5 * time.Minute)):
			auth.Status = provider.AuthRefreshSoon
		}
	}
	return r.buildCompatibleProvider(cfg, provider.KindSidecar, auth, definition.SidecarCapabilities)
}

func BuildCLIContainerProvider(ctx context.Context, cfg Config) (BuildResult, error) {
	return DefaultRegistry().BuildCLIContainerProvider(ctx, cfg)
}

func (r *Registry) BuildCLIContainerProvider(ctx context.Context, cfg Config) (BuildResult, error) {
	if cfg.AuthPath == "" {
		return BuildResult{}, fmt.Errorf("--auth-path is required with --cli-container")
	}
	service := provider.Service(cfg.Service)
	definition, _ := r.Definition(service)
	authFormatName := cfg.AuthFormat
	if authFormatName == "" {
		authFormatName = definition.DefaultAuthFormat
	}
	if authFormatName == "" {
		return BuildResult{}, fmt.Errorf("--auth-format is required with --cli-container for service %q", cfg.Service)
	}
	authFormat, ok := formats.Get(authFormatName)
	if !ok {
		return BuildResult{}, fmt.Errorf("unknown --auth-format %q (known: %s)", authFormatName, strings.Join(formats.List(), ", "))
	}
	if err := waitForAuthBootstrap(ctx, cfg.AuthPath, DefaultAuthBootstrapTimeout(cfg.AuthBootstrapTimeout)); err != nil {
		return BuildResult{}, err
	}
	auth, err := initialAuthStateFromFile(ctx, cfg.AuthPath, authFormat, time.Now)
	if err != nil {
		return BuildResult{}, err
	}
	auth.Refreshable = strings.TrimSpace(cfg.RefreshCommand) != ""
	if auth.Account.Display == "" {
		auth.Account.Display = cfg.Account
	}
	extraCaps := []provider.Capability{provider.CapabilityAuthFile}
	var refresher providershim.AuthRefresher
	if auth.Refreshable {
		extraCaps = append(extraCaps, provider.CapabilityAuthRefreshOneshot)
		refresher, err = providershim.NewCommandAuthRefresher(providershim.CommandAuthRefresherOptions{
			Command:  RefreshCommandArgs(cfg.RefreshCommand, cfg.RefreshLoginShell),
			Timeout:  cfg.RefreshTimeout,
			AuthPath: cfg.AuthPath,
			Format:   authFormat,
		})
		if err != nil {
			return BuildResult{}, err
		}
	}
	adapter, err := r.normalizeCLIContainerAdapter(cfg, definition)
	if err != nil {
		return BuildResult{}, err
	}
	buildCtx := BuildContext{
		Config:            cfg,
		Auth:              auth,
		ExtraCapabilities: extraCaps,
		AuthFormat:        authFormat,
	}
	if definition.BuildCLIContainerAdapter != nil {
		apiProvider, handled, err := definition.BuildCLIContainerAdapter(ctx, buildCtx, adapter)
		if err != nil {
			return BuildResult{}, err
		}
		if handled {
			return BuildResult{
				Provider:             WrapNativeUsageProbe(apiProvider, cfg.AuthPath, authFormat),
				AuthRefresher:        refresher,
				AutoRefreshThreshold: DefaultRefreshThreshold(cfg.RefreshThreshold),
				AutoRefreshCooldown:  DefaultRefreshCooldown(cfg.RefreshCooldown),
			}, nil
		}
	}
	apiProvider, err := r.buildGenericCLIContainerAdapter(cfg, auth, extraCaps, adapter)
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		Provider:             WrapNativeUsageProbe(apiProvider, cfg.AuthPath, authFormat),
		AuthRefresher:        refresher,
		AutoRefreshThreshold: DefaultRefreshThreshold(cfg.RefreshThreshold),
		AutoRefreshCooldown:  DefaultRefreshCooldown(cfg.RefreshCooldown),
	}, nil
}

func DefaultRefreshThreshold(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return 5 * time.Minute
}

func DefaultRefreshCooldown(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return 5 * time.Minute
}

func DefaultAuthBootstrapTimeout(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return 30 * time.Second
}

func (r *Registry) normalizeCLIContainerAdapter(cfg Config, definition Definition) (string, error) {
	if definition.NormalizeCLIAdapter != nil {
		return definition.NormalizeCLIAdapter(cfg)
	}
	return normalizeGenericCLIAdapter(cfg)
}

func normalizeGenericCLIAdapter(cfg Config) (string, error) {
	adapter := strings.ToLower(strings.TrimSpace(cfg.UpstreamAdapter))
	if adapter == "" {
		return "api-compatible", nil
	}
	switch adapter {
	case "api-compatible":
		return adapter, nil
	case "reverse-http":
		return "api-compatible", nil
	case "websocket":
		return "", fmt.Errorf("--upstream-adapter websocket is currently only supported for service codex")
	default:
		return "", fmt.Errorf("unsupported --upstream-adapter %q", cfg.UpstreamAdapter)
	}
}

func normalizeCodexCLIAdapter(cfg Config) (string, error) {
	adapter := strings.ToLower(strings.TrimSpace(cfg.UpstreamAdapter))
	if adapter == "" {
		if isWebSocketURL(cfg.UpstreamBaseURL) {
			return "codex-websocket", nil
		}
		return "api-compatible", nil
	}
	switch adapter {
	case "api-compatible":
		return adapter, nil
	case "websocket", "codex-websocket":
		return "codex-websocket", nil
	case "reverse-http", "codex-reverse-http":
		return "codex-reverse-http", nil
	default:
		return "", fmt.Errorf("unsupported --upstream-adapter %q", cfg.UpstreamAdapter)
	}
}

func normalizeCLIOneshotAdapter(service provider.Service) func(Config) (string, error) {
	return func(cfg Config) (string, error) {
		adapter := strings.ToLower(strings.TrimSpace(cfg.UpstreamAdapter))
		if adapter == "" {
			if strings.TrimSpace(cfg.UpstreamBaseURL) == "" {
				return "cli-oneshot", nil
			}
			return "api-compatible", nil
		}
		switch adapter {
		case "api-compatible":
			return adapter, nil
		case "reverse-http":
			return "api-compatible", nil
		case "cli-oneshot":
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
		case "websocket", "codex-websocket", "codex-reverse-http":
			return "", fmt.Errorf("--upstream-adapter %s requires --service codex", adapter)
		default:
			return "", fmt.Errorf("unsupported --upstream-adapter %q", cfg.UpstreamAdapter)
		}
	}
}

func buildCodexCLIContainerAdapter(_ context.Context, buildCtx BuildContext, adapter string) (providershim.APICompatibleProvider, bool, error) {
	cfg := buildCtx.Config
	switch adapter {
	case "codex-websocket":
		registration, err := buildProviderRegistration(cfg, provider.KindAppServer, buildCtx.Auth, buildCtx.ExtraCapabilities)
		if err != nil {
			return nil, true, err
		}
		codexProvider, err := codexprovider.New(codexprovider.Options{
			Registration: registration,
			AppServerURL: cfg.UpstreamBaseURL,
			AuthPath:     cfg.AuthPath,
		})
		return codexProvider, true, err
	case "codex-reverse-http":
		if isWebSocketURL(cfg.UpstreamBaseURL) {
			return nil, true, fmt.Errorf("--upstream-adapter reverse-http requires an HTTP-compatible bridge URL, got %q", cfg.UpstreamBaseURL)
		}
		apiProvider, err := buildCompatibleProvider(cfg, provider.KindAppServer, buildCtx.Auth, buildCtx.ExtraCapabilities)
		return apiProvider, true, err
	default:
		return nil, false, nil
	}
}

func (r *Registry) buildGenericCLIContainerAdapter(cfg Config, auth provider.AuthState, extraCaps []provider.Capability, adapter string) (providershim.APICompatibleProvider, error) {
	switch adapter {
	case "cli-oneshot", "claude-cli", "gemini-cli":
		return buildCLICommandProvider(cfg, auth, extraCaps)
	default:
		return r.buildCompatibleProvider(cfg, provider.KindCLIContainer, auth, extraCaps)
	}
}

func isWebSocketURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://")
}

func buildCLICommandProvider(cfg Config, auth provider.AuthState, extraCapabilities []provider.Capability) (*cliprovider.Provider, error) {
	registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, auth, extraCapabilities)
	if err != nil {
		return nil, err
	}
	return cliprovider.New(cliprovider.Options{
		Registration:   registration,
		Service:        provider.Service(cfg.Service),
		RequestTimeout: cfg.CLIRequestTimeout,
	})
}

func (r *Registry) buildCompatibleProvider(cfg Config, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (*apiprovider.Provider, error) {
	return buildCompatibleProvider(cfg, kind, auth, extraCapabilities)
}

func buildCompatibleProvider(cfg Config, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (*apiprovider.Provider, error) {
	registration, err := buildProviderRegistration(cfg, kind, auth, extraCapabilities)
	if err != nil {
		return nil, err
	}
	dialect := compat.APIDialect(cfg.UpstreamDialect)
	return apiprovider.New(apiprovider.Options{
		Registration:     registration,
		BaseURL:          cfg.UpstreamBaseURL,
		Dialect:          dialect,
		APIKey:           cfg.UpstreamAPIKey,
		APIKeyFile:       cfg.UpstreamAPIKeyFile,
		APIKeyMode:       cfg.UpstreamAPIKeyMode,
		APIKeyHeader:     cfg.UpstreamAPIKeyHeader,
		APIKeyQueryParam: cfg.UpstreamAPIKeyQueryParam,
	})
}

func buildProviderRegistration(cfg Config, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (provider.Registration, error) {
	return buildProviderRegistrationWithOptions(cfg, kind, auth, extraCapabilities, true)
}

func buildProviderRegistrationWithoutUpstream(cfg Config, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability) (provider.Registration, error) {
	return buildProviderRegistrationWithOptions(cfg, kind, auth, extraCapabilities, false)
}

func buildProviderRegistrationWithOptions(cfg Config, kind provider.Kind, auth provider.AuthState, extraCapabilities []provider.Capability, requireUpstreamBaseURL bool) (provider.Registration, error) {
	if cfg.ProviderID == "" {
		return provider.Registration{}, fmt.Errorf("--provider-id is required")
	}
	if cfg.ProviderInstanceID == "" {
		return provider.Registration{}, fmt.Errorf("--provider-instance-id is required")
	}
	if cfg.NodeID == "" {
		return provider.Registration{}, fmt.Errorf("--node-id is required")
	}
	if cfg.HostName == "" {
		return provider.Registration{}, fmt.Errorf("--host-name is required")
	}
	if cfg.Service == "" {
		return provider.Registration{}, fmt.Errorf("--service is required")
	}
	service := provider.Service(cfg.Service)
	if !service.Valid() {
		return provider.Registration{}, fmt.Errorf("invalid --service %q", cfg.Service)
	}
	if requireUpstreamBaseURL && cfg.UpstreamBaseURL == "" {
		return provider.Registration{}, fmt.Errorf("--upstream-base-url is required")
	}
	dialect := compat.APIDialect(cfg.UpstreamDialect)
	if !dialect.Valid() {
		return provider.Registration{}, fmt.Errorf("invalid --upstream-dialect %q", cfg.UpstreamDialect)
	}
	defaultCapability, err := capabilityForDialect(dialect)
	if err != nil {
		return provider.Registration{}, err
	}
	capabilities, err := registrationCapabilities(cfg, defaultCapability, extraCapabilities)
	if err != nil {
		return provider.Registration{}, err
	}
	modelCapabilities, err := registrationModelCapabilities(cfg, capabilities)
	if err != nil {
		return provider.Registration{}, err
	}
	account := auth.Account
	if account.Display == "" {
		account.Display = cfg.Account
	}
	auth.Account = account
	if auth.Status == "" {
		auth.Status = provider.AuthUnknown
	}
	now := time.Now().UTC()
	aliases := []string(nil)
	if cfg.ModelAlias != "" {
		aliases = []string{cfg.ModelAlias}
	}
	models := []provider.Model(nil)
	if cfg.Model != "" {
		models = []provider.Model{{
			ID:           cfg.Model,
			Aliases:      aliases,
			Capabilities: dedupeCapabilities(modelCapabilities),
		}}
	}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         cfg.ProviderID,
			ProviderInstanceID: cfg.ProviderInstanceID,
			NodeID:             cfg.NodeID,
			HostName:           cfg.HostName,
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

func registrationCapabilities(cfg Config, defaultCapability provider.Capability, extraCapabilities []provider.Capability) ([]provider.Capability, error) {
	var capabilities []provider.Capability
	var err error
	switch {
	case strings.TrimSpace(cfg.ShimCapabilities) != "":
		capabilities, err = parseCapabilityList(cfg.ShimCapabilities)
	case strings.TrimSpace(cfg.ShimProtocols) != "":
		capabilities, err = capabilitiesForProtocols(cfg.ShimProtocols)
		capabilities = append(capabilities, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead)
	default:
		capabilities = []provider.Capability{defaultCapability, provider.CapabilityStreamSSE, provider.CapabilityUsageRead, provider.CapabilityModelsRead}
	}
	if err != nil {
		return nil, err
	}
	capabilities = append(capabilities, extraCapabilities...)
	capabilities = dedupeCapabilities(capabilities)
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("provider capabilities must not be empty")
	}
	return capabilities, nil
}

func registrationModelCapabilities(cfg Config, registrationCapabilities []provider.Capability) ([]provider.Capability, error) {
	if strings.TrimSpace(cfg.ModelCapabilities) != "" {
		capabilities, err := parseCapabilityList(cfg.ModelCapabilities)
		if err != nil {
			return nil, err
		}
		return dedupeCapabilities(capabilities), nil
	}
	capabilities := make([]provider.Capability, 0, len(registrationCapabilities))
	for _, capability := range registrationCapabilities {
		if isModelCapability(capability) {
			capabilities = append(capabilities, capability)
		}
	}
	return dedupeCapabilities(capabilities), nil
}

func isModelCapability(capability provider.Capability) bool {
	switch capability {
	case provider.CapabilityOpenAIChat,
		provider.CapabilityOpenAIResponses,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityStreamSSE,
		provider.CapabilityAgentToolUse,
		provider.CapabilityAgentWorkspaceRead,
		provider.CapabilityAgentWorkspaceWrite,
		provider.CapabilityAgentTerminal,
		provider.CapabilityCodeCompletion:
		return true
	default:
		return false
	}
}

func parseCapabilityList(raw string) ([]provider.Capability, error) {
	items := strings.Split(raw, ",")
	capabilities := make([]provider.Capability, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		capability := provider.Capability(item)
		if !capability.Valid() {
			return nil, fmt.Errorf("invalid provider capability %q", item)
		}
		capabilities = append(capabilities, capability)
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("provider capabilities must not be empty")
	}
	return capabilities, nil
}

func capabilitiesForProtocols(raw string) ([]provider.Capability, error) {
	items := strings.Split(raw, ",")
	capabilities := []provider.Capability(nil)
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "":
			continue
		case "openai", "chatgpt":
			capabilities = append(capabilities, provider.CapabilityOpenAIChat)
		case "anthropic", "claude":
			capabilities = append(capabilities, provider.CapabilityAnthropicMessages)
		case "gemini":
			capabilities = append(capabilities, provider.CapabilityGeminiGenerateContent)
		default:
			return nil, fmt.Errorf("unsupported shim protocol %q", item)
		}
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("shim protocols must not be empty")
	}
	return dedupeCapabilities(capabilities), nil
}

type nativeUsageProbeProvider struct {
	providershim.APICompatibleProvider
	authPath string
	format   formats.Format
	probe    formats.UsageProbe
	client   *http.Client
}

func WrapNativeUsageProbe(base providershim.APICompatibleProvider, authPath string, format formats.Format) providershim.APICompatibleProvider {
	if base == nil {
		return base
	}
	probe, _ := format.(formats.UsageProbe)
	if probe == nil && (format == nil || strings.TrimSpace(authPath) == "") {
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

func (p *nativeUsageProbeProvider) AuthSnapshot(ctx context.Context) (providershim.AuthSnapshotReport, error) {
	if p == nil || p.format == nil || strings.TrimSpace(p.authPath) == "" {
		return providershim.AuthSnapshotReport{}, fmt.Errorf("auth snapshot unavailable")
	}
	raw, snapshot, err := p.readAuthSnapshot()
	if err != nil {
		return providershim.AuthSnapshotReport{}, err
	}
	return providershim.AuthSnapshotReport{
		Raw:         raw,
		Fingerprint: snapshot.Fingerprint(),
		Filename:    authFilenameForFormat(p.format.Name()),
		Format:      p.format.Name(),
	}, nil
}

func (p *nativeUsageProbeProvider) ApplyAuthPush(ctx context.Context, push control.AuthPush, registration provider.Registration) (provider.AuthState, error) {
	if p == nil || p.format == nil || strings.TrimSpace(p.authPath) == "" {
		return registration.Auth, fmt.Errorf("auth push unavailable")
	}
	raw := append([]byte(nil), push.Raw...)
	if len(raw) == 0 {
		return push.Auth, nil
	}
	if err := os.WriteFile(p.authPath, raw, 0o600); err != nil {
		auth := registration.Auth
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	snapshot, err := p.format.Parse(raw)
	if err != nil {
		auth := registration.Auth
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	result, err := p.format.Validate(ctx, snapshot, formats.ValidateOpts{})
	if err != nil {
		auth := registration.Auth
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	auth := push.Auth
	if auth.Status == "" {
		auth.Status = authStatusFromValidation(result.Status)
	}
	auth.ExpiresAt = snapshot.ExpiresAt()
	auth.SelectedSource = firstNonEmptyString(push.Source, "router")
	auth.LastRefreshAt = time.Now().UTC()
	auth.LastRefreshErr = ""
	if auth.Account == (provider.Account{}) {
		auth.Account = registration.Identity.Account
	}
	if accountAware, ok := p.format.(formats.AccountAware); ok {
		if id, err := accountAware.Account(ctx, snapshot, p.authPath); err == nil && id != "" {
			auth.Account.ID = id
		}
	}
	if displayAware, ok := p.format.(formats.AccountDisplayAware); ok {
		if display, err := displayAware.AccountDisplay(ctx, snapshot, p.authPath); err == nil && display != "" {
			auth.Account.Display = display
		}
	}
	if auth.Status == provider.AuthExpired || auth.Status == provider.AuthRevoked || auth.Status == provider.AuthUnavailable {
		if result.Detail != "" {
			auth.LastRefreshErr = result.Detail
		}
		return auth, fmt.Errorf("pushed auth status %s", result.Status)
	}
	return auth, nil
}

func (p *nativeUsageProbeProvider) Usage() (provider.UsageReport, error) {
	base, baseErr := p.APICompatibleProvider.Usage()
	if base.ObservedAt.IsZero() {
		base.ObservedAt = time.Now().UTC()
	}
	if p == nil || p.probe == nil || p.format == nil || strings.TrimSpace(p.authPath) == "" {
		return base, baseErr
	}
	_, snapshot, err := p.readAuthSnapshot()
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

func (p *nativeUsageProbeProvider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if streamInvoker, ok := p.APICompatibleProvider.(interface {
		InvokeStream(context.Context, provider.Registration, compat.Request, func(compat.Event) error) (compat.Response, error)
	}); ok {
		return streamInvoker.InvokeStream(ctx, registration, request, emit)
	}
	response, err := p.APICompatibleProvider.Invoke(ctx, registration, request)
	if err != nil {
		return compat.Response{}, err
	}
	if emit == nil {
		return response, nil
	}
	events, err := compat.EventsFromResponse(response)
	if err != nil {
		return compat.Response{}, err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return compat.Response{}, err
		}
	}
	return response, nil
}

func (p *nativeUsageProbeProvider) readAuthSnapshot() ([]byte, formats.Snapshot, error) {
	raw, err := os.ReadFile(p.authPath)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := p.format.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, snapshot, nil
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

func authFilenameForFormat(format string) string {
	switch format {
	case "codex-auth-json-format":
		return "auth.json"
	case "claude-credentials-json-format":
		return ".credentials.json"
	case "gemini-oauth-creds-json-format":
		return "oauth_creds.json"
	default:
		return "auth.json"
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func RefreshCommandArgs(command string, loginShell bool) []string {
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
