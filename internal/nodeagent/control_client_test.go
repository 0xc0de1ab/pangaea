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
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{Engine: engine}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunControlClient(ctx, ControlClientOptions{
			ControlURL:        controlURL(server.URL),
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
