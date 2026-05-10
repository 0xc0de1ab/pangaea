package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	v2router "github.com/0xc0de1ab/pangaea/internal/router"
	"github.com/0xc0de1ab/pangaea/internal/security"
	"github.com/spf13/cobra"
)

type routerServeOptions struct {
	Listen         string
	Policy         string
	Simulator      bool
	APIKey         string
	TenantID       string
	UserID         string
	StreamTokenKey string
	PeerToken      string
	StateDir       string
	StateMode      string
	StateFlush     time.Duration
	DataRequestTO  time.Duration
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
	cmd.Flags().StringVar(&opts.APIKey, "api-key", "", "optional public API bearer key")
	cmd.Flags().StringVar(&opts.TenantID, "tenant-id", "dev", "tenant id assigned to --api-key")
	cmd.Flags().StringVar(&opts.UserID, "user-id", "dev", "user id assigned to --api-key")
	cmd.Flags().StringVar(&opts.StreamTokenKey, "stream-token-key", defaultStreamTokenKey, "shared HMAC key for router-to-shim stream capability tokens")
	cmd.Flags().StringVar(&opts.PeerToken, "peer-token", "", "optional bearer token required for node-agent and provider-shim websocket connections")
	cmd.Flags().StringVar(&opts.StateDir, "state-dir", "", "optional directory for router state snapshots")
	cmd.Flags().StringVar(&opts.StateMode, "state-mode", "persistent", "router state mode (persistent|ephemeral)")
	cmd.Flags().DurationVar(&opts.StateFlush, "state-flush-interval", 10*time.Second, "interval for writing router state snapshots")
	cmd.Flags().DurationVar(&opts.DataRequestTO, "data-request-timeout", 10*time.Minute, "maximum duration for one router-to-provider data request")
	return cmd
}

func runRouterServe(ctx context.Context, opts routerServeOptions) error {
	opts.APIKey = stringEnvDefault(opts.APIKey, "PANGAEA_ROUTER_API_KEY")
	opts.PeerToken = stringEnvDefault(opts.PeerToken, "PANGAEA_ROUTER_PEER_TOKEN")
	opts.StreamTokenKey = stringEnvDefaultWhenDefault(opts.StreamTokenKey, defaultStreamTokenKey, "PANGAEA_STREAM_TOKEN_KEY")
	opts.StateDir = stringEnvDefault(opts.StateDir, "PANGAEA_ROUTER_STATE_DIR")
	opts.StateMode = stringEnvDefaultWhenDefault(opts.StateMode, "persistent", "PANGAEA_ROUTER_STATE_MODE")
	if value := strings.TrimSpace(os.Getenv("PANGAEA_ROUTER_DATA_REQUEST_TIMEOUT")); value != "" && (opts.DataRequestTO <= 0 || opts.DataRequestTO == 10*time.Minute) {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse PANGAEA_ROUTER_DATA_REQUEST_TIMEOUT: %w", err)
		}
		opts.DataRequestTO = parsed
	}
	engine, err := buildRouterEngine(opts)
	if err != nil {
		return err
	}
	if err := restoreRouterState(opts, engine); err != nil {
		return err
	}
	stateStop := startRouterStateWriter(ctx, opts, engine)
	defer stateStop()
	dataBroker, err := v2router.NewDataBrokerWithOptions([]byte(opts.StreamTokenKey), v2router.DataBrokerOptions{
		RequestTimeout: opts.DataRequestTO,
	})
	if err != nil {
		return err
	}
	if !opts.Simulator {
		engine.SetInvoker(dataBroker)
	}
	srv := &http.Server{
		Addr:              opts.Listen,
		Handler:           v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, APIKeys: buildRouterAPIKeyStore(opts), DataBroker: dataBroker, PeerToken: opts.PeerToken}),
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
		logRouterStateError("write final router state", writeRouterState(opts, engine))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), common.ShutdownGrace)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		logRouterStateError("write final router state", writeRouterState(opts, engine))
		return err
	}
}

func routerStateEnabled(opts routerServeOptions) bool {
	if strings.TrimSpace(opts.StateDir) == "" {
		return false
	}
	return strings.ToLower(strings.TrimSpace(opts.StateMode)) != "ephemeral"
}

func routerStatePath(opts routerServeOptions) string {
	return filepath.Join(opts.StateDir, "router-state.json")
}

func restoreRouterState(opts routerServeOptions, engine *v2router.Engine) error {
	if !routerStateEnabled(opts) || engine == nil {
		return nil
	}
	data, err := os.ReadFile(routerStatePath(opts))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read router state: %w", err)
	}
	var snapshot v2router.StateSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode router state: %w", err)
	}
	engine.RestoreState(snapshot)
	return nil
}

func startRouterStateWriter(ctx context.Context, opts routerServeOptions, engine *v2router.Engine) func() {
	if !routerStateEnabled(opts) || engine == nil {
		return func() {}
	}
	interval := opts.StateFlush
	if interval <= 0 {
		interval = 10 * time.Second
	}
	writerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-writerCtx.Done():
				return
			case <-ticker.C:
				logRouterStateError("write periodic router state", writeRouterState(opts, engine))
			}
		}
	}()
	return func() {
		cancel()
		<-done
		logRouterStateError("write final router state", writeRouterState(opts, engine))
	}
}

func logRouterStateError(action string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: %s: %v\n", action, err)
	}
}

func writeRouterState(opts routerServeOptions, engine *v2router.Engine) error {
	if !routerStateEnabled(opts) || engine == nil {
		return nil
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return fmt.Errorf("create router state dir: %w", err)
	}
	data, err := json.MarshalIndent(engine.SnapshotState(time.Now().UTC()), "", "  ")
	if err != nil {
		return fmt.Errorf("encode router state: %w", err)
	}
	path := routerStatePath(opts)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write router state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace router state: %w", err)
	}
	return nil
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

func buildRouterAPIKeyStore(opts routerServeOptions) *security.APIKeyStore {
	if opts.APIKey == "" {
		return nil
	}
	store := security.NewAPIKeyStore(nil)
	_, _ = store.AddRawKey("dev-key", opts.APIKey, opts.TenantID, opts.UserID)
	return store
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
      - provider_type: providersim-openai
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`

const defaultStreamTokenKey = "pangaea-dev-stream-token-key"
