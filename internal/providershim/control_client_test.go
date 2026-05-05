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
	body := bytes.NewBufferString(`{"refresh_id":"refresh_test","reason":"manual","timeout_seconds":2,"confirm":true}`)
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
	body := bytes.NewBufferString(`{"drain":true,"reason":"maintenance","timeout_seconds":1,"confirm":true}`)
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

func TestControlClientsIgnoreUnknownControlMessages(t *testing.T) {
	env := control.Envelope{
		Version: control.ProtocolVersion,
		Type:    control.MessageType("provider.future.command"),
		ID:      "future_1",
		SentAt:  time.Now().UTC(),
		Payload: json.RawMessage(`{}`),
	}
	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	if err := handleSimulatorControlRequest(context.Background(), nil, sim, env); err != nil {
		t.Fatalf("simulator control client should ignore unknown messages: %v", err)
	}
	registration, err := sim.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if err := handleStaticControlRequest(context.Background(), nil, newStaticControlState(registration), nil, env); err != nil {
		t.Fatalf("static control client should ignore unknown messages: %v", err)
	}
}

func TestRunStaticControlClientHandlesAuthRefreshWithRefresher(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	registration, err := sim.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	registration.Auth.Status = provider.AuthRefreshSoon
	refresher := AuthRefresherFunc(func(_ context.Context, request control.AuthRefreshRequest, registration provider.Registration) (provider.AuthState, error) {
		if request.RefreshID != "refresh_static" {
			t.Fatalf("unexpected refresh request: %#v", request)
		}
		auth := registration.Auth
		auth.Status = provider.AuthHealthy
		auth.LastRefreshAt = time.Now().UTC()
		return auth, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunStaticControlClient(ctx, StaticControlClientOptions{
			ControlURL:        controlURL(server.URL),
			HeartbeatInterval: time.Second,
			Registration:      registration,
			AuthRefresher:     refresher,
		})
	}()

	waitForProvider(t, engine, registration.Identity.ProviderInstanceID)
	body := bytes.NewBufferString(`{"refresh_id":"refresh_static","reason":"manual","timeout_seconds":2,"confirm":true}`)
	resp, err := http.Post(server.URL+"/router/v1/providers/"+registration.Identity.ProviderInstanceID+"/auth/refresh", "application/json", body)
	if err != nil {
		t.Fatalf("post refresh: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result control.AuthRefreshResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode refresh result: %v", err)
	}
	if !result.OK || result.Auth.Status != provider.AuthHealthy {
		t.Fatalf("unexpected refresh result: %#v", result)
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("static control client returned error: %v", err)
	}
}

func TestRunStaticControlClientAutoRefreshesNearExpiry(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	sim, err := providersim.New(providersim.Options{})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	registration, err := sim.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	registration.Auth.Status = provider.AuthRefreshSoon
	registration.Auth.Refreshable = true
	registration.Auth.ExpiresAt = time.Now().UTC().Add(time.Minute)
	called := make(chan control.AuthRefreshRequest, 1)
	refresher := AuthRefresherFunc(func(_ context.Context, request control.AuthRefreshRequest, registration provider.Registration) (provider.AuthState, error) {
		called <- request
		auth := registration.Auth
		auth.Status = provider.AuthHealthy
		auth.ExpiresAt = time.Now().UTC().Add(time.Hour)
		auth.LastRefreshAt = time.Now().UTC()
		return auth, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunStaticControlClient(ctx, StaticControlClientOptions{
			ControlURL:           controlURL(server.URL),
			HeartbeatInterval:    10 * time.Millisecond,
			Registration:         registration,
			AuthRefresher:        refresher,
			AutoRefreshThreshold: 5 * time.Minute,
			AutoRefreshCooldown:  time.Hour,
		})
	}()

	waitForProvider(t, engine, registration.Identity.ProviderInstanceID)
	select {
	case request := <-called:
		if !strings.HasPrefix(request.RefreshID, "auto_refresh_") || request.Reason != "auto refresh threshold reached" {
			t.Fatalf("unexpected auto refresh request: %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatalf("auto refresh was not triggered")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, got := range engine.Providers() {
			if got.Identity.ProviderInstanceID == registration.Identity.ProviderInstanceID && got.Auth.Status == provider.AuthHealthy && got.Auth.ExpiresAt.After(time.Now().UTC().Add(30*time.Minute)) {
				cancel()
				if err := <-errCh; err != nil {
					t.Fatalf("static control client returned error: %v", err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("auto refresh result did not update router auth")
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
