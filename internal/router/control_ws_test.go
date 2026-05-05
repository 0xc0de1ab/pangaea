package router

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gorilla/websocket"
)

func TestControlWSProviderRegisterUpdatesRegistry(t *testing.T) {
	engine, _ := testEngine(t)
	conn := dialControlWS(t, engine)
	defer conn.Close()

	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	writeControlEnvelope(t, conn, control.MessageTypeProviderRegister, "msg_register", reg)
	readControlAck(t, conn, "msg_register")

	providers := engine.Providers()
	found := false
	for _, registration := range providers {
		if registration.Identity.ProviderInstanceID == "codex-control-a1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected provider registered via control websocket, got %#v", providers)
	}
}

func TestControlWSProviderHeartbeatUpdatesHealth(t *testing.T) {
	engine, _ := testEngine(t)
	conn := dialControlWS(t, engine)
	defer conn.Close()

	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	writeControlEnvelope(t, conn, control.MessageTypeProviderRegister, "msg_register", reg)
	readControlAck(t, conn, "msg_register")

	writeControlEnvelope(t, conn, control.MessageTypeProviderHeartbeat, "msg_heartbeat", control.ProviderHeartbeat{
		ProviderInstanceID: "codex-control-a1",
		Health: provider.Health{
			Status:    provider.HealthDegraded,
			Reason:    "test degraded",
			CheckedAt: time.Now().UTC(),
		},
		Limits: provider.LimitState{QueueDepth: 3},
	})
	readControlAck(t, conn, "msg_heartbeat")

	providers := engine.Providers()
	for _, registration := range providers {
		if registration.Identity.ProviderInstanceID == "codex-control-a1" {
			if registration.Health.Status != provider.HealthDegraded {
				t.Fatalf("expected degraded health, got %#v", registration.Health)
			}
			if registration.Limits.QueueDepth != 3 {
				t.Fatalf("expected queue depth update, got %#v", registration.Limits)
			}
			return
		}
	}
	t.Fatalf("registered provider missing: %#v", providers)
}

func TestControlWSProviderAuthReportUpdatesAuth(t *testing.T) {
	engine, _ := testEngine(t)
	conn := dialControlWS(t, engine)
	defer conn.Close()

	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	writeControlEnvelope(t, conn, control.MessageTypeProviderRegister, "msg_register", reg)
	readControlAck(t, conn, "msg_register")

	writeControlEnvelope(t, conn, control.MessageTypeProviderAuthReport, "msg_auth", control.ProviderAuthReport{
		ProviderInstanceID: "codex-control-a1",
		Auth: provider.AuthState{
			Status:      provider.AuthRefreshSoon,
			Refreshable: true,
		},
	})
	readControlAck(t, conn, "msg_auth")

	for _, registration := range engine.Providers() {
		if registration.Identity.ProviderInstanceID == "codex-control-a1" {
			if registration.Auth.Status != provider.AuthRefreshSoon {
				t.Fatalf("expected refresh_soon auth, got %#v", registration.Auth)
			}
			return
		}
	}
	t.Fatalf("registered provider missing")
}

func TestControlWSProviderUsageReportUpdatesUsage(t *testing.T) {
	engine, _ := testEngine(t)
	conn := dialControlWS(t, engine)
	defer conn.Close()

	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	writeControlEnvelope(t, conn, control.MessageTypeProviderRegister, "msg_register", reg)
	readControlAck(t, conn, "msg_register")

	reportedAt := time.Now().UTC()
	writeControlEnvelope(t, conn, control.MessageTypeProviderUsageReport, "msg_usage", control.ProviderUsageReport{
		ProviderInstanceID: "codex-control-a1",
		Usage: provider.UsageReport{
			ObservedAt:    reportedAt,
			Source:        "test",
			Requests:      3,
			InputTokens:   100,
			OutputTokens:  40,
			TotalTokens:   140,
			NativeSummary: map[string]any{"window": "5h"},
		},
		ReportedAt: reportedAt,
	})
	readControlAck(t, conn, "msg_usage")

	usages := engine.ProviderUsages()
	if len(usages) != 1 {
		t.Fatalf("expected one usage snapshot, got %#v", usages)
	}
	got := usages[0]
	if got.ProviderInstanceID != "codex-control-a1" || got.HostName != "snowbox" || got.Service != provider.ServiceCodex {
		t.Fatalf("usage snapshot lost provider identity: %#v", got)
	}
	if got.Account.Display != "control@example.test" {
		t.Fatalf("usage snapshot account = %#v", got.Account)
	}
	if got.Usage.TotalTokens != 140 || got.Usage.Requests != 3 {
		t.Fatalf("unexpected usage totals: %#v", got.Usage)
	}
}

func TestControlWSNodeHelloHeartbeatAndInventory(t *testing.T) {
	engine, _ := testEngine(t)
	conn := dialControlWS(t, engine)
	defer conn.Close()

	writeControlEnvelope(t, conn, control.MessageTypeNodeHello, "msg_node_hello", control.NodeHello{
		NodeID:       "node-a1",
		AgentVersion: "test-agent",
		OS:           "linux",
		Arch:         "arm64",
		Runtime:      control.RuntimeInfo{Kind: "docker", Version: "26.1.0", Rootless: true},
		Capabilities: []string{"container.create", "container.stats"},
	})
	readControlAck(t, conn, "msg_node_hello")

	reportedAt := time.Now().UTC()
	writeControlEnvelope(t, conn, control.MessageTypeNodeHeartbeat, "msg_node_heartbeat", control.NodeHeartbeat{
		NodeID:   "node-a1",
		HostName: "snowbox",
		Health:   control.HealthReport{Status: "ready", CheckedAt: reportedAt},
		Resources: control.ResourceUsage{
			CPUPercent:  12.5,
			MemoryBytes: 1024,
		},
		ReportedAt: reportedAt,
	})
	readControlAck(t, conn, "msg_node_heartbeat")

	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	writeControlEnvelope(t, conn, control.MessageTypeProviderInventoryReport, "msg_inventory", control.ProviderInventoryReport{
		Mode:     "full",
		NodeID:   "node-a1",
		HostName: "snowbox",
		Providers: []provider.Registration{
			reg,
		},
		Containers: []control.ContainerReport{{
			ContainerID:        "container-1",
			ProviderID:         "codex-cli",
			ProviderInstanceID: "codex-control-a1",
			Image:              "pangaea/codex:latest",
			State:              "running",
			Resources:          control.ResourceUsage{MemoryBytes: 2048},
			Labels:             map[string]string{"pangaea.provider": "codex-cli"},
		}},
		Resources:  control.ResourceUsage{MemoryBytes: 4096},
		ReportedAt: reportedAt,
	})
	readControlAck(t, conn, "msg_inventory")

	nodes := engine.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("expected one node, got %#v", nodes)
	}
	if nodes[0].NodeID != "node-a1" || nodes[0].HostName != "snowbox" || nodes[0].Runtime.Kind != "docker" {
		t.Fatalf("unexpected node snapshot: %#v", nodes[0])
	}
	containers := engine.Containers()
	if len(containers) != 1 {
		t.Fatalf("expected one container, got %#v", containers)
	}
	if containers[0].ContainerID != "container-1" || containers[0].HostName != "snowbox" || containers[0].ProviderInstanceID != "codex-control-a1" {
		t.Fatalf("unexpected container snapshot: %#v", containers[0])
	}
	found := false
	for _, provider := range engine.Providers() {
		if provider.Identity.ProviderInstanceID == "codex-control-a1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inventory provider was not upserted")
	}
}

func TestControlWSUnknownProviderReportsError(t *testing.T) {
	engine, _ := testEngine(t)
	conn := dialControlWS(t, engine)
	defer conn.Close()

	writeControlEnvelope(t, conn, control.MessageTypeProviderHeartbeat, "msg_missing", control.ProviderHeartbeat{
		ProviderInstanceID: "missing-provider",
		Health:             provider.Health{Status: provider.HealthReady},
	})

	env := readControlEnvelope(t, conn)
	if env.Type != control.MessageTypeControlError {
		t.Fatalf("expected control.error, got %q", env.Type)
	}
	var payload control.ControlError
	if err := control.DecodePayload(env, control.MessageTypeControlError, &payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Code != "provider_not_found" {
		t.Fatalf("expected provider_not_found error, got %#v", payload)
	}
}

func dialControlWS(t *testing.T, engine *Engine) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{Engine: engine}))
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/router/v1/control/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial control ws: %v", err)
	}
	return conn
}

func writeControlEnvelope(t *testing.T, conn *websocket.Conn, messageType control.MessageType, id string, payload any) {
	t.Helper()
	data, err := control.Marshal(messageType, id, time.Now().UTC(), payload)
	if err != nil {
		t.Fatalf("marshal control envelope: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write control envelope: %v", err)
	}
}

func readControlAck(t *testing.T, conn *websocket.Conn, replyTo string) {
	t.Helper()
	env := readControlEnvelope(t, conn)
	if env.Type != control.MessageTypeAck {
		t.Fatalf("expected control.ack, got %q", env.Type)
	}
	var payload control.Ack
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("decode ack payload: %v", err)
	}
	if !payload.OK {
		t.Fatalf("expected ok ack, got %#v", payload)
	}
	if payload.ReplyTo != replyTo {
		t.Fatalf("ack reply_to got %q want %q", payload.ReplyTo, replyTo)
	}
}

func readControlEnvelope(t *testing.T, conn *websocket.Conn) control.Envelope {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read control envelope: %v", err)
	}
	env, err := control.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal control envelope: %v", err)
	}
	return env
}
