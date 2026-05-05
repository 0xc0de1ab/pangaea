package providershim

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
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

func TestRunSimulatorControlClientHandlesAuthRefreshRequest(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	sim.SetAuthStatus(provider.AuthRefreshSoon)
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

	waitForProvider(t, engine, "providersim-openai-0001")
	body := bytes.NewBufferString(`{"refresh_id":"refresh_test","reason":"manual","timeout_seconds":2}`)
	resp, err := http.Post(server.URL+"/router/v1/providers/providersim-openai-0001/auth/refresh", "application/json", body)
	if err != nil {
		t.Fatalf("post refresh: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result control.AuthRefreshResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.OK || result.Auth.Status != provider.AuthHealthy {
		t.Fatalf("unexpected refresh result: %#v", result)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("control client returned error: %v", err)
	}
}

func TestRunSimulatorControlClientHandlesProviderDrain(t *testing.T) {
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

	waitForProvider(t, engine, "providersim-openai-0001")
	body := bytes.NewBufferString(`{"drain":true,"reason":"maintenance","timeout_seconds":1}`)
	resp, err := http.Post(server.URL+"/router/v1/providers/providersim-openai-0001/drain", "application/json", body)
	if err != nil {
		t.Fatalf("post drain: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, registration := range engine.Providers() {
			if registration.Identity.ProviderInstanceID == "providersim-openai-0001" && registration.Health.Status == provider.HealthDraining {
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
	t.Fatalf("provider drain command did not update provider health")
}

func controlURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/router/v1/control/ws"
}

func waitForProvider(t *testing.T, engine *router.Engine, providerInstanceID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, registration := range engine.Providers() {
			if registration.Identity.ProviderInstanceID == providerInstanceID {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider %s was not registered", providerInstanceID)
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
