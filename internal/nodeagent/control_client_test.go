package nodeagent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/router"
)

func TestRunControlClientSendsNodeHelloAndHeartbeat(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine, PeerToken: "peer-secret"}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
			RouterDataURL:     "ws://router.example.test/router/v1/data/ws",
			StreamTokenKey:    "node-stream-token-key",
			PeerToken:         "peer-secret",
			NodeID:            "node-a1",
			HostName:          "snowbox",
			AgentVersion:      "test-agent",
			Runtime:           control.RuntimeInfo{Kind: "docker", Version: "26.1.0", Rootless: true},
			Capabilities:      []string{"container.inventory"},
			HeartbeatInterval: 10 * time.Millisecond,
			Resources:         control.ResourceUsage{CPUPercent: 7.5, MemoryBytes: 4096},
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, node := range engine.Nodes() {
			if node.NodeID == "node-a1" && node.HostName == "snowbox" && !node.LastHelloAt.IsZero() && !node.LastHeartbeatAt.IsZero() {
				if node.Runtime.Kind != "docker" || node.Resources.MemoryBytes != 4096 {
					t.Fatalf("unexpected node snapshot: %#v", node)
				}
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
	t.Fatalf("node control client did not update node snapshot")
}

func TestRunControlClientSendsConfiguredProviderInventory(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
			RouterDataURL:     "ws://router.example.test/router/v1/data/ws",
			StreamTokenKey:    "node-stream-token-key",
			PeerToken:         "node-peer-token",
			NodeID:            "node-a1",
			HostName:          "snowbox",
			HeartbeatInterval: time.Second,
			ProviderSpecs: []ProviderSpec{{
				ID:          "codex-samtest",
				InstanceID:  "codex-samtest-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-codex:test",
				AccountHint: "samtest4u@gmail.com",
				Service:     provider.ServiceCodex,
				Shim:        ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAuthRefreshOneshot}},
			}, {
				ID:          "codex-nullcode",
				InstanceID:  "codex-nullcode-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-codex:test",
				AccountHint: "nullcode@gmail.com",
				Service:     provider.ServiceCodex,
				Shim:        ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
			}},
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(engine.Providers()) == 2 && len(engine.Containers()) == 2 {
			cancel()
			if err := <-errCh; err != nil {
				t.Fatalf("control client returned error: %v", err)
			}
			providers := engine.Providers()
			for _, registration := range providers {
				if registration.Identity.HostName != "snowbox" {
					t.Fatalf("inventory lost host name: %#v", providers)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("node control client did not report configured provider inventory: providers=%#v containers=%#v", engine.Providers(), engine.Containers())
}

func TestRunControlClientReconcilesConfiguredProviderContainers(t *testing.T) {
	engine := testRouterEngine(t)
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	rt := &fakeContainerRuntime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
			RouterDataURL:     "ws://router.example.test/router/v1/data/ws",
			StreamTokenKey:    "node-stream-token-key",
			PeerToken:         "node-peer-token",
			NodeID:            "node-a1",
			HostName:          "snowbox",
			HeartbeatInterval: time.Second,
			ReconcileInterval: time.Second,
			ContainerRuntime:  rt,
			ProviderSpecs: []ProviderSpec{{
				ID:          "codex-samtest",
				InstanceID:  "codex-samtest-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-codex:test",
				AccountHint: "samtest4u@gmail.com",
				Service:     provider.ServiceCodex,
				Shim:        ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
			}},
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		containers := engine.Containers()
		if len(containers) == 1 && containers[0].State == "running" && rt.started == "container-1" {
			cancel()
			if err := <-errCh; err != nil {
				t.Fatalf("control client returned error: %v", err)
			}
			if rt.pulled != "pangaea/provider-codex:test" || rt.created.ProviderInstanceID != "codex-samtest-0001" {
				t.Fatalf("runtime did not reconcile provider: pulled=%q created=%#v", rt.pulled, rt.created)
			}
			if rt.created.Env["PANGAEA_ROUTER_CONTROL_URL"] != controlURL(server.URL) {
				t.Fatalf("reconciled container did not receive router control url: %#v", rt.created.Env)
			}
			if rt.created.Env["PANGAEA_ROUTER_DATA_URL"] != "ws://router.example.test/router/v1/data/ws?provider_instance_id=codex-samtest-0001" || rt.created.Env["PANGAEA_STREAM_TOKEN_KEY"] != "node-stream-token-key" || rt.created.Env["PANGAEA_ROUTER_PEER_TOKEN"] != "node-peer-token" {
				t.Fatalf("reconciled container did not receive data plane config: %#v", rt.created.Env)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("node control client did not report reconciled container inventory: %#v", engine.Containers())
}

func TestRunControlClientRequiresControlURL(t *testing.T) {
	err := RunControlClient(context.Background(), ControlClientOptions{NodeID: "node-a1", HostName: "host-a1"})
	if err == nil {
		t.Fatalf("expected control url error")
	}
}

func TestNormalizeOptionsDefaultsNodeIdentity(t *testing.T) {
	opts, err := normalizeOptions(ControlClientOptions{ControlURL: "ws://127.0.0.1/router/v1/control/ws", HostName: "host-a1"})
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	if opts.NodeID != "host-a1" {
		t.Fatalf("node id default = %q", opts.NodeID)
	}
	if opts.Runtime.Kind != "docker" || len(opts.Capabilities) == 0 || opts.HeartbeatInterval <= 0 {
		t.Fatalf("expected defaults, got %#v", opts)
	}
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
