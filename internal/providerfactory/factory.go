package providerfactory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/antigravity"
	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/cliprovider"
	"github.com/0xc0de1ab/pangaea/internal/codexdirect"
	"github.com/0xc0de1ab/pangaea/internal/codexprovider"
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/copilotacp"
	"github.com/0xc0de1ab/pangaea/internal/cursoracp"
	"github.com/0xc0de1ab/pangaea/internal/cursordirect"
	"github.com/0xc0de1ab/pangaea/internal/geminidirect"
	"github.com/0xc0de1ab/pangaea/internal/grokacp"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const authExpiryRefreshThreshold = 5 * time.Minute
const antigravityStateFormatName = "antigravity-state-vscdb-format"

type Config struct {
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
			NormalizeCLIAdapter: normalizeCLICommandAdapter(),
		},
		Definition{
			Service:                  provider.ServiceGemini,
			DefaultAuthFormat:        "gemini-oauth-creds-json-format",
			NormalizeCLIAdapter:      normalizeCLICommandAdapter(),
			BuildCLIContainerAdapter: buildGeminiCLIContainerAdapter,
		},
		Definition{
			Service:                  provider.ServiceCursor,
			DefaultAuthFormat:        "cursor-auth-json-format",
			NormalizeCLIAdapter:      normalizeCursorCLIAdapter,
			BuildCLIContainerAdapter: buildCursorCLIContainerAdapter,
		},
		Definition{
			Service:                  provider.ServiceGrokBuild,
			DefaultAuthFormat:        "grok-auth-json-format",
			NormalizeCLIAdapter:      normalizeGrokBuildCLIAdapter,
			BuildCLIContainerAdapter: buildGrokBuildCLIContainerAdapter,
		},
		Definition{
			Service:           provider.ServiceAntigravity,
			DefaultAuthFormat: "antigravity-state-vscdb-format",
			SidecarCapabilities: []provider.Capability{
				provider.CapabilityAntigravitySidecar,
				provider.CapabilityAgentToolUse,
				provider.CapabilityAgentWorkspaceRead,
				provider.CapabilityAgentWorkspaceWrite,
			},
		},
		Definition{
			Service:                  provider.ServiceGitHubCopilot,
			DefaultAuthFormat:        "github-copilot-config-json-format",
			NormalizeCLIAdapter:      normalizeGitHubCopilotCLIAdapter,
			BuildCLIContainerAdapter: buildGitHubCopilotCLIContainerAdapter,
			SidecarCapabilities: []provider.Capability{
				provider.CapabilityAnthropicMessages,
				provider.CapabilityGeminiGenerateContent,
				provider.CapabilityCodeCompletion,
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

func BuildSidecarProvider(cfg Config) (providershim.APICompatibleProvider, error) {
	return DefaultRegistry().BuildSidecarProvider(cfg)
}

func (r *Registry) BuildSidecarProvider(cfg Config) (providershim.APICompatibleProvider, error) {
	result, err := r.BuildSidecarProviderWithRefresh(cfg)
	if err != nil {
		return nil, err
	}
	return result.Provider, nil
}

func BuildSidecarProviderWithRefresh(cfg Config) (BuildResult, error) {
	return DefaultRegistry().BuildSidecarProviderWithRefresh(cfg)
}

func (r *Registry) BuildSidecarProviderWithRefresh(cfg Config) (BuildResult, error) {
	service := provider.Service(cfg.Service)
	definition, _ := r.Definition(service)
	account := provider.Account{Display: cfg.Account}
	auth := provider.AuthState{Status: provider.AuthHealthy, Account: account, SelectedSource: "sidecar"}
	extraCaps := append([]provider.Capability(nil), definition.SidecarCapabilities...)
	var authFormat formats.Format
	var refresher providershim.AuthRefresher
	if strings.TrimSpace(cfg.AuthPath) != "" {
		authFormatName := cfg.AuthFormat
		if authFormatName == "" {
			authFormatName = definition.DefaultAuthFormat
		}
		if authFormatName != "" {
			format, ok := formats.Get(authFormatName)
			if !ok {
				return BuildResult{}, fmt.Errorf("unknown --auth-format %q (known: %s)", authFormatName, strings.Join(formats.List(), ", "))
			}
			if err := waitForAuthBootstrap(context.Background(), cfg.AuthPath, DefaultAuthBootstrapTimeout(cfg.AuthBootstrapTimeout)); err != nil {
				return BuildResult{}, err
			}
			fileAuth, err := initialAuthStateFromFile(context.Background(), cfg.AuthPath, format, time.Now)
			if err != nil {
				return BuildResult{}, err
			}
			if fileAuth.Account.Display == "" {
				fileAuth.Account.Display = cfg.Account
			}
			auth = fileAuth
			authFormat = format
			extraCaps = append(extraCaps, provider.CapabilityAuthFile)
			if service == provider.ServiceAntigravity && strings.TrimSpace(cfg.UpstreamBaseURL) != "" {
				auth.Refreshable = true
				extraCaps = append(extraCaps, provider.CapabilityAuthRefreshOneshot, provider.CapabilityAuthRefreshProtocol)
				var err error
				refresher, err = antigravity.NewAuthRefresher(antigravity.RefreshOptions{
					BaseURL:          cfg.UpstreamBaseURL,
					APIKey:           cfg.UpstreamAPIKey,
					APIKeyFile:       cfg.UpstreamAPIKeyFile,
					APIKeyMode:       cfg.UpstreamAPIKeyMode,
					APIKeyHeader:     cfg.UpstreamAPIKeyHeader,
					APIKeyQueryParam: cfg.UpstreamAPIKeyQueryParam,
					AuthPath:         cfg.AuthPath,
					Format:           authFormat,
					Timeout:          cfg.RefreshTimeout,
				})
				if err != nil {
					return BuildResult{}, err
				}
			}
		}
	}
	apiProvider, err := r.buildCompatibleProvider(cfg, provider.KindSidecar, auth, extraCaps)
	if err != nil {
		return BuildResult{}, err
	}
	apiCompatibleProvider := providershim.APICompatibleProvider(apiProvider)
	if authFormat != nil {
		apiCompatibleProvider = WrapNativeUsageProbe(apiProvider, cfg.AuthPath, authFormat)
	}
	return BuildResult{
		Provider:             apiCompatibleProvider,
		AuthRefresher:        refresher,
		AutoRefreshThreshold: DefaultRefreshThreshold(cfg.RefreshThreshold),
		AutoRefreshCooldown:  DefaultRefreshCooldown(cfg.RefreshCooldown),
	}, nil
}

func BuildCLIContainerProvider(ctx context.Context, cfg Config) (BuildResult, error) {
	return DefaultRegistry().BuildCLIContainerProvider(ctx, cfg)
}

func (r *Registry) BuildCLIContainerProvider(ctx context.Context, cfg Config) (BuildResult, error) {
	service := provider.Service(cfg.Service)
	definition, _ := r.Definition(service)
	auth := noLoginAuthState()
	extraCaps := []provider.Capability(nil)
	var authFormat formats.Format
	var refresher providershim.AuthRefresher
	if strings.TrimSpace(cfg.AuthPath) != "" {
		authFormatName := cfg.AuthFormat
		if authFormatName == "" {
			authFormatName = definition.DefaultAuthFormat
		}
		if authFormatName == "" {
			return BuildResult{}, fmt.Errorf("--auth-format is required with --cli-container for service %q", cfg.Service)
		}
		var ok bool
		authFormat, ok = formats.Get(authFormatName)
		if !ok {
			return BuildResult{}, fmt.Errorf("unknown --auth-format %q (known: %s)", authFormatName, strings.Join(formats.List(), ", "))
		}
		if err := waitForAuthBootstrap(ctx, cfg.AuthPath, DefaultAuthBootstrapTimeout(cfg.AuthBootstrapTimeout)); err != nil {
			return BuildResult{}, err
		}
		var err error
		auth, err = initialAuthStateFromFile(ctx, cfg.AuthPath, authFormat, time.Now)
		if err != nil {
			return BuildResult{}, err
		}
		auth.Refreshable = strings.TrimSpace(cfg.RefreshCommand) != ""
		if auth.Account.Display == "" {
			auth.Account.Display = cfg.Account
		}
		extraCaps = append(extraCaps, provider.CapabilityAuthFile)
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
			provider := apiProvider
			if authFormat != nil && strings.TrimSpace(cfg.AuthPath) != "" {
				provider = WrapNativeUsageProbe(apiProvider, cfg.AuthPath, authFormat)
			}
			return BuildResult{
				Provider:             provider,
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
	provider := apiProvider
	if authFormat != nil && strings.TrimSpace(cfg.AuthPath) != "" {
		provider = WrapNativeUsageProbe(apiProvider, cfg.AuthPath, authFormat)
	}
	return BuildResult{
		Provider:             provider,
		AuthRefresher:        refresher,
		AutoRefreshThreshold: DefaultRefreshThreshold(cfg.RefreshThreshold),
		AutoRefreshCooldown:  DefaultRefreshCooldown(cfg.RefreshCooldown),
	}, nil
}

func noLoginAuthState() provider.AuthState {
	return provider.AuthState{
		Status:         provider.AuthNoLogin,
		LastRefreshErr: "auth path is not configured",
		SelectedSource: "none",
	}
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
	if adapter, ok, err := adapterFromProviderMode(cfg); ok || err != nil {
		return adapter, err
	}
	return "direct-http", nil
}

func normalizeCodexCLIAdapter(cfg Config) (string, error) {
	if adapter, ok, err := adapterFromProviderMode(cfg); ok || err != nil {
		return adapter, err
	}
	if isWebSocketURL(cfg.UpstreamBaseURL) {
		return "codex-websocket", nil
	}
	return "direct-http", nil
}

func normalizeCLICommandAdapter() func(Config) (string, error) {
	return func(cfg Config) (string, error) {
		if adapter, ok, err := adapterFromProviderMode(cfg); ok || err != nil {
			return adapter, err
		}
		if strings.TrimSpace(cfg.UpstreamBaseURL) == "" {
			return "cli-adapter", nil
		}
		return "direct-http", nil
	}
}

func adapterFromProviderMode(cfg Config) (string, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.ProviderMode))
	if mode == "" {
		return "", false, nil
	}
	switch mode {
	case "http-direct":
		return "direct-http", true, nil
	case "cli-adapter":
		return "cli-adapter", true, nil
	case "app-server":
		if provider.Service(cfg.Service) != provider.ServiceCodex {
			return "", true, fmt.Errorf("--provider-mode app-server is currently supported only for service codex")
		}
		return "codex-websocket", true, nil
	case "sdk":
		if provider.Service(cfg.Service) != provider.ServiceGitHubCopilot {
			return "", true, fmt.Errorf("--provider-mode sdk is only supported for service=github-copilot")
		}
		return "copilot-sdk", true, nil
	case "acp":
		switch provider.Service(cfg.Service) {
		case provider.ServiceCursor:
			return "cursor-acp", true, nil
		case provider.ServiceGrokBuild:
			return "grok-build-acp", true, nil
		case provider.ServiceGitHubCopilot:
			return "copilot-acp", true, nil
		default:
			return "", true, fmt.Errorf("--provider-mode acp is only supported for service=cursor, service=grok-build, or service=github-copilot")
		}
	case "ls-core-sidecar":
		return "", true, fmt.Errorf("--provider-mode ls-core-sidecar is not implemented by provider-shim yet")
	default:
		return "", true, fmt.Errorf("unsupported --provider-mode %q", cfg.ProviderMode)
	}
}

func buildCodexCLIContainerAdapter(_ context.Context, buildCtx BuildContext, adapter string) (providershim.APICompatibleProvider, bool, error) {
	cfg := buildCtx.Config
	switch adapter {
	case "direct-http":
		registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, buildCtx.Auth, buildCtx.ExtraCapabilities)
		if err != nil {
			return nil, true, err
		}
		codexProvider, err := codexdirect.New(codexdirect.Options{
			Registration: registration,
			BaseURL:      cfg.UpstreamBaseURL,
			AuthPath:     cfg.AuthPath,
		})
		return codexProvider, true, err
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
	default:
		return nil, false, nil
	}
}

func buildGeminiCLIContainerAdapter(_ context.Context, buildCtx BuildContext, adapter string) (providershim.APICompatibleProvider, bool, error) {
	if adapter != "direct-http" {
		return nil, false, nil
	}
	cfg := buildCtx.Config
	registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, buildCtx.Auth, buildCtx.ExtraCapabilities)
	if err != nil {
		return nil, true, err
	}
	var toolDispatcher geminidirect.ToolDispatcher
	mcpServersJSON := strings.TrimSpace(cfg.MCPServersJSON)
	if mcpServersJSON == "" {
		mcpServersJSON = geminiMCPServersJSONFromSettings()
	}
	if mcpServersJSON != "" {
		toolDispatcher, err = geminidirect.NewMCPStdioDispatcherFromJSON(mcpServersJSON)
		if err != nil {
			return nil, true, err
		}
	}
	geminiProvider, err := geminidirect.New(geminidirect.Options{
		Registration:   registration,
		BaseURL:        cfg.UpstreamBaseURL,
		AuthPath:       cfg.AuthPath,
		ToolDispatcher: toolDispatcher,
		MaxToolRounds:  cfg.MCPToolRounds,
	})
	return geminiProvider, true, err
}

func normalizeGitHubCopilotCLIAdapter(cfg Config) (string, error) {
	if adapter, ok, err := adapterFromProviderMode(cfg); ok || err != nil {
		return adapter, err
	}
	return "copilot-acp", nil
}

func buildGitHubCopilotCLIContainerAdapter(_ context.Context, buildCtx BuildContext, adapter string) (providershim.APICompatibleProvider, bool, error) {
	cfg := buildCtx.Config
	switch adapter {
	case "copilot-acp":
		registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, buildCtx.Auth, buildCtx.ExtraCapabilities)
		if err != nil {
			return nil, true, err
		}
		p, err := copilotacp.New(copilotacp.Options{Registration: registration})
		return p, true, err
	case "copilot-sdk":
		return nil, true, fmt.Errorf("--provider-mode sdk requires --sidecar for service=github-copilot")
	default:
		return nil, false, nil
	}
}

func normalizeCursorCLIAdapter(cfg Config) (string, error) {
	if adapter, ok, err := adapterFromProviderMode(cfg); ok || err != nil {
		return adapter, err
	}
	return "cursor-acp", nil
}

func buildCursorCLIContainerAdapter(_ context.Context, buildCtx BuildContext, adapter string) (providershim.APICompatibleProvider, bool, error) {
	cfg := buildCtx.Config
	switch adapter {
	case "direct-http":
		registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, buildCtx.Auth, buildCtx.ExtraCapabilities)
		if err != nil {
			return nil, true, err
		}
		p, err := cursordirect.New(cursordirect.Options{
			Registration: registration,
			BaseURL:      cfg.UpstreamBaseURL,
			AuthPath:     cfg.AuthPath,
			APIKey:       cfg.UpstreamAPIKey,
		})
		return p, true, err
	case "cursor-acp":
		registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, buildCtx.Auth, buildCtx.ExtraCapabilities)
		if err != nil {
			return nil, true, err
		}
		p, err := cursoracp.New(cursoracp.Options{
			Registration:   registration,
			MCPServersJSON: cfg.MCPServersJSON,
		})
		return p, true, err
	default:
		return nil, false, nil
	}
}

func normalizeGrokBuildCLIAdapter(cfg Config) (string, error) {
	if adapter, ok, err := adapterFromProviderMode(cfg); ok || err != nil {
		return adapter, err
	}
	return "grok-build-acp", nil
}

func buildGrokBuildCLIContainerAdapter(_ context.Context, buildCtx BuildContext, adapter string) (providershim.APICompatibleProvider, bool, error) {
	if adapter != "grok-build-acp" {
		return nil, false, nil
	}
	cfg := buildCtx.Config
	registration, err := buildProviderRegistrationWithoutUpstream(cfg, provider.KindCLIContainer, buildCtx.Auth, buildCtx.ExtraCapabilities)
	if err != nil {
		return nil, true, err
	}
	p, err := grokacp.New(grokacp.Options{
		Registration:   registration,
		MCPServersJSON: cfg.MCPServersJSON,
	})
	return p, true, err
}

func geminiMCPServersJSONFromSettings() string {
	path := strings.TrimSpace(os.Getenv("PANGAEA_GEMINI_SETTINGS_PATH"))
	if path == "" {
		home := strings.TrimSpace(os.Getenv("HOME"))
		if home == "" {
			return ""
		}
		path = filepath.Join(home, ".gemini", "settings.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var parsed struct {
		MCPServers map[string]any `json:"mcpServers,omitempty"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.MCPServers) == 0 {
		return ""
	}
	return string(raw)
}

func (r *Registry) buildGenericCLIContainerAdapter(cfg Config, auth provider.AuthState, extraCaps []provider.Capability, adapter string) (providershim.APICompatibleProvider, error) {
	switch adapter {
	case "cli-adapter":
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
	if cfg.ProviderType == "" {
		return provider.Registration{}, fmt.Errorf("--provider-type is required")
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
	capabilities = registrationServiceCapabilities(service, capabilities)
	modelCapabilities, err := registrationModelCapabilities(cfg, capabilities)
	if err != nil {
		return provider.Registration{}, err
	}
	modelCapabilities = registrationServiceModelCapabilities(service, modelCapabilities)
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
	models, err := registrationModels(cfg, modelCapabilities, aliases)
	if err != nil {
		return provider.Registration{}, err
	}
	targetVersion := resolveTargetVersion(cfg, service)
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       cfg.ProviderType,
			ProviderInstanceID: cfg.ProviderInstanceID,
			NodeID:             cfg.NodeID,
			HostName:           cfg.HostName,
			ContainerID:        cfg.ContainerID,
			ContainerKind:      cfg.ContainerKind,
			ContainerName:      cfg.ContainerName,
			TargetVersion:      targetVersion,
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

func registrationModels(cfg Config, modelCapabilities []provider.Capability, singleAliases []string) ([]provider.Model, error) {
	if strings.TrimSpace(cfg.Models) != "" {
		return parseModelList(provider.Service(cfg.Service), cfg.Models, modelCapabilities)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, nil
	}
	model := provider.Model{
		ID:               strings.TrimSpace(cfg.Model),
		Aliases:          singleAliases,
		Capabilities:     dedupeCapabilities(modelCapabilities),
		ContextTokens:    defaultContextTokens(provider.Service(cfg.Service), strings.TrimSpace(cfg.Model)),
		MaxContextTokens: defaultContextTokens(provider.Service(cfg.Service), strings.TrimSpace(cfg.Model)),
		MaxOutputTokens:  defaultOutputTokens(provider.Service(cfg.Service), strings.TrimSpace(cfg.Model)),
	}
	return []provider.Model{model}, nil
}

func resolveTargetVersion(cfg Config, service provider.Service) string {
	if version := strings.TrimSpace(cfg.TargetVersion); version != "" {
		return version
	}
	switch service {
	case provider.ServiceCodex:
		return detectCommandTargetVersion("codex")
	case provider.ServiceGemini:
		return detectCommandTargetVersion("gemini")
	case provider.ServiceClaude:
		return detectCommandTargetVersion("claude")
	case provider.ServiceCursor:
		return detectCommandTargetVersion("agent")
	case provider.ServiceGitHubCopilot:
		return detectCommandTargetVersion("copilot")
	case provider.ServiceGrokBuild:
		return detectCommandTargetVersion("grok")
	case provider.ServiceAntigravity:
		if version := detectHTTPHealthTargetVersion(cfg.UpstreamBaseURL); version != "" {
			return version
		}
	}
	return ""
}

func detectCommandTargetVersion(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, "--version").Output()
	if err != nil {
		return ""
	}
	return parseTargetVersionOutput(string(output))
}

func parseTargetVersionOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	for _, field := range strings.Fields(trimmed) {
		field = strings.Trim(field, " \t\r\n,;()[]{}")
		field = strings.TrimPrefix(field, "v")
		if looksLikeVersionNumber(field) {
			return field
		}
	}
	if len(trimmed) > 96 {
		return trimmed[:96]
	}
	return trimmed
}

func looksLikeVersionNumber(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if (r < '0' || r > '9') && r != '-' && r != '+' {
				return false
			}
		}
	}
	return true
}

func detectHTTPHealthTargetVersion(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || strings.HasPrefix(baseURL, "ws://") || strings.HasPrefix(baseURL, "wss://") {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/health", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ""
	}
	var parsed struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Version)
}

func parseModelList(service provider.Service, raw string, capabilities []provider.Capability) ([]provider.Model, error) {
	items := strings.Split(raw, ",")
	models := make([]provider.Model, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, aliasesRaw, _ := strings.Cut(item, "=")
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("model id must not be empty in --models")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		aliases := []string(nil)
		for _, alias := range strings.Split(aliasesRaw, "|") {
			alias = strings.TrimSpace(alias)
			if alias != "" && alias != id {
				aliases = append(aliases, alias)
			}
		}
		models = append(models, provider.Model{
			ID:               id,
			Aliases:          aliases,
			Capabilities:     dedupeCapabilities(capabilities),
			ContextTokens:    defaultContextTokens(service, id),
			MaxContextTokens: defaultContextTokens(service, id),
			MaxOutputTokens:  defaultOutputTokens(service, id),
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("--models did not contain any model ids")
	}
	return models, nil
}

func defaultContextTokens(service provider.Service, model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	switch service {
	case provider.ServiceGemini:
		if strings.Contains(model, "gemini-") {
			return 1_048_576
		}
	case provider.ServiceMiniMAX:
		if strings.HasPrefix(model, "minimax-m2") {
			return 204_800
		}
	case provider.ServiceGrokBuild:
		if model == "grok-build" || model == "grok-build-0.1" {
			return 512_000
		}
	}
	return 0
}

func defaultOutputTokens(service provider.Service, model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	switch service {
	case provider.ServiceGemini:
		if strings.Contains(model, "gemini-") {
			return 65_536
		}
	case provider.ServiceMiniMAX:
		if strings.HasPrefix(model, "minimax-m2") {
			return 2_048
		}
	}
	return 0
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

func registrationServiceCapabilities(service provider.Service, capabilities []provider.Capability) []provider.Capability {
	switch service {
	case provider.ServiceMiniMAX:
		capabilities = append(capabilities,
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
		)
	}
	return dedupeCapabilities(capabilities)
}

func registrationServiceModelCapabilities(service provider.Service, capabilities []provider.Capability) []provider.Capability {
	switch service {
	case provider.ServiceMiniMAX:
		capabilities = append(capabilities,
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
		)
	}
	return dedupeCapabilities(capabilities)
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
	now := time.Now().UTC()
	validationStatus, validationExpiresAt, validationErr := authStateFromValidation(p.format, result, snapshot.ExpiresAt(), now)
	auth := push.Auth
	if auth.Status == "" || shouldPreferValidationAuthStatus(p.format, auth.Status, validationStatus, validationExpiresAt) {
		auth.Status = validationStatus
	}
	auth.ExpiresAt = validationExpiresAt
	auth.SelectedSource = firstNonEmptyString(push.Source, "router")
	auth.LastRefreshAt = now
	auth.LastRefreshErr = validationErr
	if auth.Status != validationStatus && auth.Status != provider.AuthRefreshSoon {
		auth.LastRefreshErr = ""
	}
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
	auth.Subscription = subscriptionFromAuthSummary(p.format, snapshot)
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
	base.PlanTier = firstNonEmptyString(base.PlanTier, native.PlanTier)
	base.Subscription = mergeSubscriptionInfo(base.Subscription, subscriptionFromNativeUsage(p.format.Name(), native))
	base.NativeSummary = native
	return base, nil
}

func (p *nativeUsageProbeProvider) Health() (provider.Health, error) {
	if p == nil || p.APICompatibleProvider == nil {
		return provider.Health{}, fmt.Errorf("health reporter unavailable")
	}
	if reporter, ok := p.APICompatibleProvider.(interface {
		Health() (provider.Health, error)
	}); ok {
		return reporter.Health()
	}
	registration, err := p.APICompatibleProvider.Registration()
	if err != nil {
		return provider.Health{}, err
	}
	return registration.Health, nil
}

func (p *nativeUsageProbeProvider) Auth() (provider.AuthState, error) {
	if p == nil || p.APICompatibleProvider == nil {
		return provider.AuthState{}, fmt.Errorf("auth reporter unavailable")
	}
	if reporter, ok := p.APICompatibleProvider.(interface {
		Auth() (provider.AuthState, error)
	}); ok {
		return reporter.Auth()
	}
	registration, err := p.APICompatibleProvider.Registration()
	if err != nil {
		return provider.AuthState{}, err
	}
	return registration.Auth, nil
}

func (p *nativeUsageProbeProvider) ForceModelDiscovery() bool {
	if p == nil || p.APICompatibleProvider == nil {
		return false
	}
	if reporter, ok := p.APICompatibleProvider.(interface {
		ForceModelDiscovery() bool
	}); ok {
		return reporter.ForceModelDiscovery()
	}
	return false
}

func (p *nativeUsageProbeProvider) TargetVersion(ctx context.Context) (string, error) {
	if p == nil || p.APICompatibleProvider == nil {
		return "", fmt.Errorf("target version reporter unavailable")
	}
	if reporter, ok := p.APICompatibleProvider.(interface {
		TargetVersion(context.Context) (string, error)
	}); ok {
		return reporter.TargetVersion(ctx)
	}
	registration, err := p.APICompatibleProvider.Registration()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(registration.Identity.TargetVersion), nil
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
	case "antigravity-state-vscdb-format":
		return "state.vscdb"
	case "github-copilot-apps-json-format":
		return "apps.json"
	case "github-copilot-config-json-format":
		return "config.json"
	case "cursor-auth-json-format":
		return "auth.json"
	case "cursor-cli-config-json-format":
		return "cli-config.json"
	case "cursor-api-token-plain-format":
		return "api_token"
	case "grok-auth-json-format":
		return "auth.json"
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
	checkedAt := now().UTC()
	status, expiresAt, lastRefreshErr := authStateFromValidation(format, result, snapshot.ExpiresAt(), checkedAt)
	auth := provider.AuthState{
		Status:          status,
		ExpiresAt:       expiresAt,
		LastRefreshErr:  lastRefreshErr,
		SelectedSource:  "container",
		BootstrapSource: "copy",
		Subscription:    subscriptionFromAuthSummary(format, snapshot),
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

func subscriptionFromAuthSummary(format formats.Format, snapshot formats.Snapshot) *provider.SubscriptionInfo {
	if format == nil || snapshot == nil {
		return nil
	}
	summary := format.Redact(snapshot)
	tier := strings.TrimSpace(summary.Subscription)
	if tier == "" {
		return nil
	}
	return &provider.SubscriptionInfo{
		Tier:   tier,
		Source: format.Name() + "/auth-summary",
	}
}

func subscriptionFromNativeUsage(formatName string, native formats.UsageReport) *provider.SubscriptionInfo {
	info := provider.SubscriptionInfo{
		Tier:   strings.TrimSpace(native.PlanTier),
		Source: strings.TrimSpace(formatName) + "/usage-probe",
	}
	if info.Source == "/usage-probe" {
		info.Source = "usage-probe"
	}
	for _, note := range native.Notes {
		key, value, ok := strings.Cut(strings.TrimSpace(note), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "tier":
			info.Name = value
		case "status":
			info.Status = value
		case "paid-tier":
			info.PaidTier = value
		case "rate-limit-tier":
			info.RateLimitTier = value
		}
	}
	if info.Tier == "" && info.Name == "" && info.Status == "" && info.PaidTier == "" && info.RateLimitTier == "" {
		return nil
	}
	return &info
}

func mergeSubscriptionInfo(base, native *provider.SubscriptionInfo) *provider.SubscriptionInfo {
	if base == nil || *base == (provider.SubscriptionInfo{}) {
		return native
	}
	if native == nil || *native == (provider.SubscriptionInfo{}) {
		return base
	}
	merged := *base
	if merged.Tier == "" {
		merged.Tier = native.Tier
	}
	if merged.Name == "" {
		merged.Name = native.Name
	}
	if merged.Status == "" {
		merged.Status = native.Status
	}
	if merged.PaidTier == "" {
		merged.PaidTier = native.PaidTier
	}
	if merged.RateLimitTier == "" {
		merged.RateLimitTier = native.RateLimitTier
	}
	if merged.Source == "" {
		merged.Source = native.Source
	} else if native.Source != "" && !strings.Contains(merged.Source, native.Source) {
		merged.Source = joinUsageSources(merged.Source, native.Source)
	}
	return &merged
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

func authStatusFromValidation(status formats.ValidationStatus, expiresAt time.Time, checkedAt time.Time) provider.AuthStatus {
	switch status {
	case formats.StatusOK:
		if !expiresAt.IsZero() && !checkedAt.Add(authExpiryRefreshThreshold).Before(expiresAt) {
			return provider.AuthRefreshSoon
		}
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

func authStateFromValidation(format formats.Format, result formats.ValidationResult, expiresAt time.Time, checkedAt time.Time) (provider.AuthStatus, time.Time, string) {
	formatName := ""
	if format != nil {
		formatName = format.Name()
	}
	if formatName == antigravityStateFormatName &&
		result.Status == formats.StatusOK &&
		!expiresAt.IsZero() &&
		!checkedAt.Add(authExpiryRefreshThreshold).Before(expiresAt) {
		return provider.AuthHealthy, expiresAt, ""
	}
	status := authStatusFromValidation(result.Status, expiresAt, checkedAt)
	lastRefreshErr := ""
	if (result.Status != formats.StatusOK || status == provider.AuthRefreshSoon) && result.Detail != "" {
		lastRefreshErr = result.Detail
	}
	return status, expiresAt, lastRefreshErr
}

func shouldPreferValidationAuthStatus(format formats.Format, current provider.AuthStatus, validationStatus provider.AuthStatus, validationExpiresAt time.Time) bool {
	if format == nil || format.Name() != antigravityStateFormatName {
		return false
	}
	return current == provider.AuthRefreshSoon && validationStatus == provider.AuthHealthy
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

func mergeStringLists(base []string, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	seen := map[string]struct{}{}
	for _, value := range append(append([]string(nil), base...), extra...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
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
