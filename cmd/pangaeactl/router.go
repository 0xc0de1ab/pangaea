package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	v2router "github.com/0xc0de1ab/pangaea/internal/router"
	"github.com/spf13/cobra"
)

type routerServeOptions struct {
	Listen    string
	Policy    string
	Simulator bool
}

func newRouterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "router",
		Short:         common.CLIShortRouter,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newRouterServeCmd())
	return cmd
}

func newRouterServeCmd() *cobra.Command {
	opts := routerServeOptions{}
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "serve v2 router HTTP APIs",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.Listen == "" {
				return fmt.Errorf("--listen is required")
			}
			return runRouterServe(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	cmd.Flags().StringVar(&opts.Policy, "policy", "", "routing policy YAML path")
	cmd.Flags().BoolVar(&opts.Simulator, "simulator", false, "register a built-in simulator provider")
	return cmd
}

func runRouterServe(ctx context.Context, opts routerServeOptions) error {
	engine, err := buildRouterEngine(opts)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              opts.Listen,
		Handler:           v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine}),
		ReadHeaderTimeout: common.ReadTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), common.ShutdownGrace)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func buildRouterEngine(opts routerServeOptions) (*v2router.Engine, error) {
	policy, err := loadRouterPolicy(opts.Policy, opts.Simulator)
	if err != nil {
		return nil, err
	}
	registry := provider.NewRegistry()
	var invoker *providersim.Simulator
	if opts.Simulator {
		invoker, err = registerSimulatorProvider(registry)
		if err != nil {
			return nil, err
		}
	}
	ledger := quota.NewLedger()
	engine, err := v2router.NewEngine(policy, registry, ledger)
	if err != nil {
		return nil, err
	}
	if invoker != nil {
		engine.SetInvoker(invoker)
	}
	return engine, nil
}

func loadRouterPolicy(path string, simulator bool) (v2router.RoutingPolicy, error) {
	if path == "" {
		if !simulator {
			return v2router.RoutingPolicy{}, fmt.Errorf("--policy is required unless --simulator is set")
		}
		return v2router.ParseRoutingPolicyYAML([]byte(defaultSimulatorRoutingPolicy))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return v2router.RoutingPolicy{}, fmt.Errorf("read routing policy: %w", err)
	}
	return v2router.ParseRoutingPolicyYAML(data)
}

func registerSimulatorProvider(registry *provider.Registry) (*providersim.Simulator, error) {
	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible})
	if err != nil {
		return nil, err
	}
	registration, err := sim.Registration()
	if err != nil {
		return nil, err
	}
	if err := registry.Upsert(registration); err != nil {
		return nil, err
	}
	return sim, nil
}

const defaultSimulatorRoutingPolicy = `
version: routing-policy/v1
model_aliases:
  providersim-default:
    canonical_model: gpt-5-sim
    required_capabilities:
      - api.openai.chat
routes:
  - id: providersim-openai
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-openai
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`
