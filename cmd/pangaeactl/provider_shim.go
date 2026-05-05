package main

import (
	"context"
	"fmt"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/spf13/cobra"
)

type providerShimRunOptions struct {
	RouterControlURL   string
	RouterDataURL      string
	Simulator          bool
	APICompatible      bool
	HeartbeatInterval  time.Duration
	StreamTokenKey     string
	ProviderID         string
	ProviderInstanceID string
	NodeID             string
	HostName           string
	Service            string
	Account            string
	UpstreamBaseURL    string
	UpstreamDialect    string
	UpstreamAPIKey     string
	Model              string
	ModelAlias         string
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
	cmd.Flags().StringVar(&opts.Model, "model", "", "canonical upstream model id for --api-compatible")
	cmd.Flags().StringVar(&opts.ModelAlias, "model-alias", "", "optional public model alias for --api-compatible")
	return cmd
}

func runProviderShim(ctx context.Context, opts providerShimRunOptions) error {
	if opts.RouterControlURL == "" {
		return fmt.Errorf("--router-control is required")
	}
	switch {
	case opts.Simulator && opts.APICompatible:
		return fmt.Errorf("choose only one of --simulator or --api-compatible")
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
	default:
		return fmt.Errorf("one of --simulator or --api-compatible is required")
	}
}

func buildAPICompatibleProvider(opts providerShimRunOptions) (*apiprovider.Provider, error) {
	if opts.ProviderID == "" {
		return nil, fmt.Errorf("--provider-id is required with --api-compatible")
	}
	if opts.ProviderInstanceID == "" {
		return nil, fmt.Errorf("--provider-instance-id is required with --api-compatible")
	}
	if opts.NodeID == "" {
		return nil, fmt.Errorf("--node-id is required with --api-compatible")
	}
	if opts.HostName == "" {
		return nil, fmt.Errorf("--host-name is required with --api-compatible")
	}
	if opts.Service == "" {
		return nil, fmt.Errorf("--service is required with --api-compatible")
	}
	service := provider.Service(opts.Service)
	if !service.Valid() {
		return nil, fmt.Errorf("invalid --service %q", opts.Service)
	}
	if opts.UpstreamBaseURL == "" {
		return nil, fmt.Errorf("--upstream-base-url is required with --api-compatible")
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
	account := provider.Account{Display: opts.Account}
	now := time.Now().UTC()
	aliases := []string(nil)
	if opts.ModelAlias != "" {
		aliases = []string{opts.ModelAlias}
	}
	return apiprovider.New(apiprovider.Options{
		Registration: provider.Registration{
			Identity: provider.ProviderIdentity{
				ProviderID:         opts.ProviderID,
				ProviderInstanceID: opts.ProviderInstanceID,
				NodeID:             opts.NodeID,
				HostName:           opts.HostName,
				Service:            service,
				Kind:               provider.KindAPICompatible,
				Account:            account,
			},
			Capabilities: []provider.Capability{capability, provider.CapabilityUsageRead},
			Models: []provider.Model{{
				ID:           opts.Model,
				Aliases:      aliases,
				Capabilities: []provider.Capability{capability},
			}},
			Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
			Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account},
			RegisteredAt: now,
		},
		BaseURL: opts.UpstreamBaseURL,
		Dialect: dialect,
		APIKey:  opts.UpstreamAPIKey,
	})
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
