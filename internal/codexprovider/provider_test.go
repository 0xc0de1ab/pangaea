package codexprovider

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestProviderInvokeStreamDoesNotDropBurstDeltas(t *testing.T) {
	const deltaCount = 260
	server := newBurstCodexAppServer(t, deltaCount)
	defer server.Close()

	p, err := New(Options{
		Registration: testRegistration(),
		AppServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		AuthPath:     writeTestCodexAuth(t),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var eventDeltas strings.Builder
	response, err := p.InvokeStream(ctx, testRegistration(), compat.Request{
		ID:      "req_codex_stream_burst",
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-5-codex",
		Stream:  true,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "write code"}},
		}},
	}, func(event compat.Event) error {
		if event.Type == compat.EventContentDelta && event.ContentDelta != nil {
			eventDeltas.WriteString(event.ContentDelta.Text)
			time.Sleep(time.Millisecond)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	want := burstText(deltaCount)
	if response.Message.Content[0].Text != want {
		t.Fatalf("response lost deltas: got length %d, want %d", len(response.Message.Content[0].Text), len(want))
	}
	if eventDeltas.String() != want {
		t.Fatalf("stream events lost deltas: got length %d, want %d", eventDeltas.Len(), len(want))
	}
}

func TestProviderModelsDiscoverCodexAppServerModelList(t *testing.T) {
	server := newModelListCodexAppServer(t)
	defer server.Close()

	registration := testRegistration()
	registration.Models = []provider.Model{{
		ID:           "gpt-5.5",
		Aliases:      []string{"codex-default"},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE},
	}}
	p, err := New(Options{
		Registration: registration,
		AppServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		AuthPath:     writeTestCodexAuth(t),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models len = %d, want 3: %#v", len(models), models)
	}
	if models[0].ID != "gpt-5.5" || !sameStrings(models[0].Aliases, []string{"codex-default", "GPT-5.5"}) {
		t.Fatalf("configured and codex display aliases were not preserved: %#v", models[0])
	}
	if models[1].ID != "gpt-5.4" || models[2].ID != "gpt-5.4-mini" {
		t.Fatalf("discovered models were not appended: %#v", models)
	}
	if !sameStrings(models[1].Aliases, []string{"GPT-5.4"}) || !sameStrings(models[2].Aliases, []string{"GPT-5.4 Mini"}) {
		t.Fatalf("discovered model display aliases were not captured: %#v", models)
	}
	if len(models[1].Capabilities) == 0 || models[1].Capabilities[0] != provider.CapabilityOpenAIChat {
		t.Fatalf("discovered model capabilities were not inherited: %#v", models[1])
	}
}

func TestCodexModelsFromAppServerUsesCacheContextWindow(t *testing.T) {
	models := codexModelsFromAppServer([]codexAppServerModel{
		{ID: "gpt-5.4", DisplayName: "gpt-5.4"},
		{ID: "gpt-5.3-codex-spark", DisplayName: "GPT-5.3-Codex-Spark"},
	}, map[string]codexCachedModel{
		"gpt-5.4":             {Slug: "gpt-5.4", ContextWindow: 272000, MaxContextWindow: 1000000},
		"gpt-5.3-codex-spark": {Slug: "gpt-5.3-codex-spark", ContextWindow: 128000, MaxContextWindow: 128000},
	}, []provider.Capability{provider.CapabilityOpenAIChat})
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2", len(models))
	}
	if models[0].ContextTokens != 272000 || models[1].ContextTokens != 128000 {
		t.Fatalf("context tokens were not read from cache: %#v", models)
	}
	if models[0].MaxContextTokens != 1000000 || models[1].MaxContextTokens != 128000 {
		t.Fatalf("max context tokens were not read from cache: %#v", models)
	}
}

func TestTurnStartParamsFromCanonicalSupportsEffortAndImage(t *testing.T) {
	params, cleanup, err := turnStartParamsFromCanonical(compat.Request{
		Dialect:         compat.APIDialectOpenAI,
		Model:           "gpt-5.4",
		ReasoningEffort: "high",
		Messages: []compat.Message{{
			Role: compat.MessageRoleUser,
			Content: []compat.ContentPart{
				{Type: compat.ContentPartText, Text: "describe this"},
				{Type: compat.ContentPartImage, MIME: "image/png", Data: "iVBORw0KGgo="},
			},
		}},
	}, "thread_1")
	defer cleanup()
	if err != nil {
		t.Fatalf("turn params: %v", err)
	}
	if params.Model == nil || *params.Model != "gpt-5.4" {
		t.Fatalf("unexpected model: %#v", params.Model)
	}
	if params.Effort == nil || *params.Effort != "high" {
		t.Fatalf("unexpected effort: %#v", params.Effort)
	}
	if len(params.Input) != 2 || params.Input[0].Type != "text" || params.Input[1].Type != "localImage" || params.Input[1].Path == "" {
		t.Fatalf("unexpected input: %#v", params.Input)
	}
	if _, err := os.Stat(params.Input[1].Path); err != nil {
		t.Fatalf("expected temporary local image to exist: %v", err)
	}
}

func TestCodexDisplayAliasNormalizesCodexModelNames(t *testing.T) {
	cases := map[string]string{
		"gpt-5.4-mini":        "GPT-5.4 Mini",
		"gpt-5.3-codex":       "GPT-5.3 Codex",
		"gpt-5.3-codex-spark": "GPT-5.3 Codex Spark",
	}
	for id, want := range cases {
		if got := codexDisplayAlias(id, ""); got != want {
			t.Fatalf("codexDisplayAlias(%q) = %q, want %q", id, got, want)
		}
	}
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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

func newModelListCodexAppServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			switch req.Method {
			case "initialize":
				writeTestRPCResponse(t, conn, req.ID, map[string]any{"serverInfo": map[string]any{"name": "test-codex"}})
			case "model/list":
				writeTestRPCResponse(t, conn, req.ID, map[string]any{"data": []map[string]any{
					{"id": "gpt-5.5", "displayName": "GPT-5.5", "hidden": false},
					{"id": "gpt-5.4", "displayName": "GPT-5.4", "hidden": false},
					{"model": "gpt-5.4-mini", "displayName": "GPT-5.4 Mini", "hidden": false},
					{"id": "legacy-hidden", "hidden": true},
				}})
			default:
				t.Errorf("unexpected method %q", req.Method)
				return
			}
		}
	}))
}

func newBurstCodexAppServer(t *testing.T, count int) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				writeTestRPCResponse(t, conn, req.ID, map[string]any{"turn": map[string]any{"id": "turn_1"}})
				for i := 0; i < count; i++ {
					writeTestNotification(t, conn, "item/agentMessage/delta", map[string]any{"turnId": "turn_1", "delta": fmt.Sprintf("chunk-%03d\n", i)})
				}
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

func burstText(count int) string {
	var out strings.Builder
	for i := 0; i < count; i++ {
		out.WriteString(fmt.Sprintf("chunk-%03d\n", i))
	}
	return out.String()
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
			ProviderType:       "codex",
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
