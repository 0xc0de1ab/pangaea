package codexprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gorilla/websocket"
)

func TestProviderInvokeCodexAppServerWebSocket(t *testing.T) {
	var sawAuth atomic.Bool
	var sawNeverApproval atomic.Bool
	server := newTestCodexAppServer(t, &sawAuth, &sawNeverApproval)
	defer server.Close()

	authPath := writeTestCodexAuth(t)
	p, err := New(Options{
		Registration: testRegistration(),
		AppServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		AuthPath:     authPath,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	response, err := p.Invoke(context.Background(), testRegistration(), compat.Request{
		ID:      "req_codex_1",
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-5-codex",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if response.Message.Content[0].Text != "hello from codex" || response.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if !sawAuth.Load() {
		t.Fatalf("expected appserver authorization header")
	}
	if !sawNeverApproval.Load() {
		t.Fatalf("expected turn/start approvalPolicy=never")
	}
	usage, err := p.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Requests != 1 || usage.TotalTokens != 5 || usage.Source != usageSource {
		t.Fatalf("unexpected cumulative usage: %#v", usage)
	}
}

func TestProviderInvokeStreamCodexAppServerWebSocket(t *testing.T) {
	server := newTestCodexAppServer(t, nil, nil)
	defer server.Close()

	p, err := New(Options{
		Registration: testRegistration(),
		AppServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		AuthPath:     writeTestCodexAuth(t),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	var events []compat.Event
	response, err := p.InvokeStream(context.Background(), testRegistration(), compat.Request{
		ID:      "req_codex_stream_1",
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-5-codex",
		Stream:  true,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if response.Message.Content[0].Text != "hello from codex" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(events) != 5 {
		t.Fatalf("events len = %d, want 5: %#v", len(events), events)
	}
	if events[0].Type != compat.EventMessageStart || events[1].Type != compat.EventContentDelta || events[4].Type != compat.EventDone {
		t.Fatalf("unexpected stream events: %#v", events)
	}
}

func newTestCodexAppServer(t *testing.T, sawAuth *atomic.Bool, sawNeverApproval *atomic.Bool) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer test-access-token" && sawAuth != nil {
			sawAuth.Store(true)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			switch req.Method {
			case "initialize":
				writeTestRPCResponse(t, conn, req.ID, map[string]any{"serverInfo": map[string]any{"name": "test-codex"}})
			case "thread/start":
				writeTestRPCResponse(t, conn, req.ID, map[string]any{"thread": map[string]any{"id": "thread_1"}})
			case "turn/start":
				var params struct {
					ApprovalPolicy string `json:"approvalPolicy"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if params.ApprovalPolicy == "never" && sawNeverApproval != nil {
					sawNeverApproval.Store(true)
				}
				writeTestRPCResponse(t, conn, req.ID, map[string]any{"turn": map[string]any{"id": "turn_1"}})
				writeTestNotification(t, conn, "item/agentMessage/delta", map[string]any{"turnId": "turn_1", "delta": "hello "})
				writeTestNotification(t, conn, "item/agentMessage/delta", map[string]any{"turnId": "turn_1", "delta": "from codex"})
				writeTestNotification(t, conn, "thread/tokenUsage/updated", map[string]any{
					"turnId": "turn_1",
					"tokenUsage": map[string]any{"last": map[string]any{
						"inputTokens": 2, "outputTokens": 3, "totalTokens": 5,
					}},
				})
				writeTestNotification(t, conn, "turn/completed", map[string]any{
					"threadId": "thread_1",
					"turn":     map[string]any{"id": "turn_1"},
				})
			default:
				t.Errorf("unexpected method %q", req.Method)
				return
			}
		}
	}))
}

func writeTestRPCResponse(t *testing.T, conn *websocket.Conn, id json.RawMessage, result any) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatalf("write rpc response: %v", err)
	}
}

func writeTestNotification(t *testing.T, conn *websocket.Conn, method string, params any) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
		t.Fatalf("write notification: %v", err)
	}
}

func writeTestCodexAuth(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/auth.json"
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"test-access-token","refresh_token":"test-refresh-token"}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	return path
}

func testRegistration() provider.Registration {
	now := time.Now().UTC()
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "codex",
			ProviderInstanceID: "codex-a1",
			NodeID:             "node-a1",
			HostName:           "host-a1",
			Service:            provider.ServiceCodex,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
		},
		Models:       []provider.Model{{ID: "gpt-5-codex", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy},
		RegisteredAt: now,
	}
}
