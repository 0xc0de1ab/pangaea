package providershim

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/router"
)

func TestRegisterSimulatorOnceRegistersProvider(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	if err := RegisterSimulatorOnce(context.Background(), controlURL(server.URL), sim); err != nil {
		t.Fatalf("register simulator once: %v", err)
	}

	providers := engine.Providers()
	found := false
	for _, provider := range providers {
		if provider.Identity.ProviderInstanceID == "providersim-openai-0001" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected providersim registration, got %#v", providers)
	}
}

func TestRunSimulatorControlClientSendsHeartbeat(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunSimulatorControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
			HeartbeatInterval: 10 * time.Millisecond,
			Simulator:         sim,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, provider := range engine.Providers() {
			if provider.Identity.ProviderInstanceID == "providersim-openai-0001" && !provider.Health.CheckedAt.IsZero() {
				cancel()
				if err := <-errCh; err != nil {
					t.Fatalf("control client returned error: %v", err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("provider heartbeat did not update registry")
}

func TestRunSimulatorControlClientSendsUsage(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunSimulatorControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
			HeartbeatInterval: time.Second,
			Simulator:         sim,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, usage := range engine.ProviderUsages() {
			if usage.ProviderInstanceID == "providersim-openai-0001" && usage.HostName == "providersim-host" && usage.Usage.TotalTokens > 0 {
				cancel()
				if err := <-errCh; err != nil {
					t.Fatalf("control client returned error: %v", err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("provider usage did not update registry")
}

func TestRunSimulatorControlClientSendsInventoryAndAuth(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunSimulatorControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
			HeartbeatInterval: 10 * time.Millisecond,
			Simulator:         sim,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, node := range engine.Nodes() {
			if node.NodeID == "providersim-node" && node.HostName == "providersim-host" && !node.LastInventoryAt.IsZero() {
				goto inventorySeen
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("provider inventory did not create node snapshot")

inventorySeen:
	sim.SetAuthStatus(provider.AuthRefreshSoon)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, registration := range engine.Providers() {
			if registration.Identity.ProviderInstanceID == "providersim-openai-0001" && registration.Auth.Status == provider.AuthRefreshSoon {
				cancel()
				if err := <-errCh; err != nil {
					t.Fatalf("control client returned error: %v", err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("provider auth heartbeat did not update registry")
}

func controlURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/router/v1/control/ws"
}

func testRouterEngine(t *testing.T) *router.Engine {
	t.Helper()
	policy, err := router.ParseRoutingPolicyYAML([]byte(`
version: routing-policy/v1
model_aliases:
  providersim-default:
    canonical_model: gpt-5-sim
    required_capabilities: [api.openai.chat]
routes:
  - id: providersim-openai
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-openai
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
`))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := router.NewEngine(policy, provider.NewRegistry(), nil)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return engine
}
