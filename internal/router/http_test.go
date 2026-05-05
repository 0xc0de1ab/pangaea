package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	"github.com/0xc0de1ab/pangaea/internal/security"
)

func TestHTTPModels(t *testing.T) {
	engine, _ := testEngine(t)
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{Engine: engine}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out openAIModelList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "gpt-5-codex" {
		t.Fatalf("unexpected model list: %#v", out)
	}
}

func TestHTTPDryRun(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	body := []byte(`{"tenant_id":"team-a","model":"gpt-5-codex","api_dialect":"openai","stream":true}`)

	req := httptest.NewRequest(http.MethodPost, "/router/v1/routes/dry-run", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var decision RouteDecision
	if err := json.Unmarshal(rec.Body.Bytes(), &decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if !decision.Allowed || decision.Selected == "" {
		t.Fatalf("expected selected route, got %#v", decision)
	}
}

func TestHTTPDryRunDenied(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	body := []byte(`{"tenant_id":"team-a","model":"gpt-5-codex","api_dialect":"anthropic"}`)

	req := httptest.NewRequest(http.MethodPost, "/router/v1/routes/dry-run", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPProviders(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodGet, "/router/v1/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("codex-samtest-a1")) {
		t.Fatalf("expected provider instance in body, got %s", rec.Body.String())
	}
}

func TestHTTPNodesAndContainers(t *testing.T) {
	engine, _ := testEngine(t)
	if err := engine.UpdateNodeHello(control.NodeHello{
		NodeID:       "node-a1",
		AgentVersion: "test-agent",
		OS:           "linux",
		Arch:         "arm64",
		Runtime:      control.RuntimeInfo{Kind: "docker"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("update node hello: %v", err)
	}
	if err := engine.UpdateNodeHeartbeat(control.NodeHeartbeat{
		NodeID:   "node-a1",
		HostName: "snowbox",
		Health:   control.HealthReport{Status: "ready"},
	}); err != nil {
		t.Fatalf("update node heartbeat: %v", err)
	}
	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		NodeID:   "node-a1",
		HostName: "snowbox",
		Containers: []control.ContainerReport{{
			ContainerID:        "container-1",
			ProviderID:         "codex-cli",
			ProviderInstanceID: "codex-samtest-a1",
			State:              "running",
		}},
	}); err != nil {
		t.Fatalf("apply inventory: %v", err)
	}
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodGet, "/router/v1/nodes", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected nodes 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var nodesOut struct {
		Nodes []NodeSnapshot `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nodesOut); err != nil {
		t.Fatalf("decode nodes: %v", err)
	}
	if len(nodesOut.Nodes) != 1 || nodesOut.Nodes[0].HostName != "snowbox" {
		t.Fatalf("unexpected nodes response: %#v", nodesOut)
	}

	req = httptest.NewRequest(http.MethodGet, "/router/v1/containers", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected containers 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var containersOut struct {
		Containers []ContainerSnapshot `json:"containers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &containersOut); err != nil {
		t.Fatalf("decode containers: %v", err)
	}
	if len(containersOut.Containers) != 1 || containersOut.Containers[0].ContainerID != "container-1" {
		t.Fatalf("unexpected containers response: %#v", containersOut)
	}
}

func TestHTTPProviderUsage(t *testing.T) {
	engine, _ := testEngine(t)
	observedAt := time.Now().UTC()
	if err := engine.UpdateProviderUsage("codex-samtest-a1", provider.UsageReport{
		ObservedAt:   observedAt,
		Source:       "test",
		Requests:     2,
		InputTokens:  20,
		OutputTokens: 10,
		TotalTokens:  30,
	}, observedAt); err != nil {
		t.Fatalf("update provider usage: %v", err)
	}
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodGet, "/router/v1/usage/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Usage []ProviderUsageSnapshot `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Usage) != 1 {
		t.Fatalf("expected one usage snapshot, got %#v", out)
	}
	got := out.Usage[0]
	if got.HostName != "snowbox" || got.Account.Display != "samtest4u@gmail.com" {
		t.Fatalf("usage response lost host/account dimensions: %#v", got)
	}
	if got.Usage.TotalTokens != 30 {
		t.Fatalf("unexpected usage: %#v", got.Usage)
	}
}

func TestHTTPQuotaAdmin(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	body := []byte(`{
		"scope":{"tenant_id":"team-a","user_id":"usr_1","api_key_id":"key_1","model":"gpt-5-codex"},
		"limit":{"max_tokens":123,"max_requests":7}
	}`)

	req := httptest.NewRequest(http.MethodPut, "/router/v1/quotas/limits", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var snapshot quota.SnapshotRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode set limit response: %v", err)
	}
	if snapshot.Limit.MaxTokens != 123 || snapshot.Limit.MaxRequests != 7 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	req = httptest.NewRequest(http.MethodPost, "/router/v1/quotas/snapshot", bytes.NewReader([]byte(`{"tenant_id":"team-a","user_id":"usr_1","api_key_id":"key_1","model":"gpt-5-codex"}`)))
	req.Header.Set("content-type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 snapshot, got %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot response: %v", err)
	}
	if snapshot.Limit.MaxTokens != 123 {
		t.Fatalf("unexpected snapshot after query: %#v", snapshot)
	}

	req = httptest.NewRequest(http.MethodGet, "/router/v1/quotas", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 list, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Quotas []quota.SnapshotRecord `json:"quotas"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode quota list: %v", err)
	}
	if len(out.Quotas) == 0 {
		t.Fatalf("expected quota list")
	}
}

func TestHTTPOpenAIChatCompletionsWithSimulator(t *testing.T) {
	engine, _ := testEngine(t)
	sim, err := providersim.New(providersim.Options{
		Registration: registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0),
	})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model":"gpt-5-codex",
		"messages":[{"role":"user","content":"hello"}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_http_1")
	req.Header.Set("x-pangaea-tenant-id", "team-a")
	req.Header.Set("x-pangaea-user-id", "usr_1")
	req.Header.Set("x-pangaea-api-key-id", "key_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response compat.OpenAIChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "providersim: hello" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestHTTPTraceAfterOpenAIChatCompletion(t *testing.T) {
	engine, _ := testEngine(t)
	sim, err := providersim.New(providersim.Options{
		Registration: registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0),
	})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model":"gpt-5-codex",
		"messages":[{"role":"user","content":"hello trace"}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_http_trace_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/router/v1/traces/req_http_trace_1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 trace, got %d body=%s", rec.Code, rec.Body.String())
	}
	var trace RequestTrace
	if err := json.Unmarshal(rec.Body.Bytes(), &trace); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if trace.Status != "completed" || trace.Provider == nil || trace.Provider.HostName != "snowbox" {
		t.Fatalf("unexpected trace: %#v", trace)
	}
	if trace.ActualUsage.Tokens == 0 || trace.EstimatedUsage.Tokens == 0 {
		t.Fatalf("expected trace usage, got %#v", trace)
	}

	req = httptest.NewRequest(http.MethodGet, "/router/v1/traces?limit=1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 traces, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Traces []RequestTrace `json:"traces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode traces: %v", err)
	}
	if len(out.Traces) != 1 || out.Traces[0].RequestID != "req_http_trace_1" {
		t.Fatalf("unexpected trace list: %#v", out)
	}
}

func TestHTTPOpenAIChatCompletionsStreamsSSEWithSimulator(t *testing.T) {
	engine, _ := testEngine(t)
	sim, err := providersim.New(providersim.Options{
		Registration: registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0),
	})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{
		"model":"gpt-5-codex",
		"stream":true,
		"messages":[{"role":"user","content":"hello stream"}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_http_stream_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("content-type"); got != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", got)
	}
	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("data:")) || !bytes.Contains([]byte(body), []byte("providersim: hello stream")) || !bytes.Contains([]byte(body), []byte("data: [DONE]")) {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}

func TestHTTPAnthropicMessagesWithSimulator(t *testing.T) {
	engine, sim := testDialectEngine(t, compat.APIDialectAnthropic, provider.CapabilityAnthropicMessages, provider.ServiceAnthropic, "claude-sim", "claude-native")
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"claude-sim",
		"max_tokens":64,
		"messages":[{"role":"user","content":"hello anthropic"}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_anthropic_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response compat.AnthropicMessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Model != "claude-native" {
		t.Fatalf("expected canonical model, got %q", response.Model)
	}
	if len(response.Content) != 1 || response.Content[0].Text != "providersim: hello anthropic" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.Usage.InputTokens == 0 || response.Usage.OutputTokens == 0 {
		t.Fatalf("expected usage response, got %#v", response.Usage)
	}
}

func TestHTTPGeminiGenerateContentWithSimulator(t *testing.T) {
	engine, sim := testDialectEngine(t, compat.APIDialectGemini, provider.CapabilityGeminiGenerateContent, provider.ServiceGemini, "gemini-sim", "gemini-native")
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-sim:generateContent", bytes.NewReader([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello gemini"}]}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_gemini_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response compat.GeminiGenerateContentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ModelVersion != "gemini-native" {
		t.Fatalf("expected canonical model, got %q", response.ModelVersion)
	}
	if len(response.Candidates) != 1 || response.Candidates[0].Content.Parts[0].Text != "providersim: hello gemini" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.UsageMetadata == nil || response.UsageMetadata.TotalTokenCount == 0 {
		t.Fatalf("expected usage response, got %#v", response.UsageMetadata)
	}
}

func TestHTTPHandlerRequiresEngine(t *testing.T) {
	handler := NewHTTPHandler(HTTPOptions{})

	req := httptest.NewRequest(http.MethodPost, "/router/v1/routes/dry-run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHTTPOpenAIChatCompletionsRequiresAPIKeyWhenConfigured(t *testing.T) {
	engine, _ := testEngine(t)
	store := security.NewAPIKeyStore([]byte("pepper"))
	if _, err := store.AddRawKey("key_1", "pk_test_router", "team-a", "usr_1"); err != nil {
		t.Fatalf("add key: %v", err)
	}
	handler := NewHTTPHandler(HTTPOptions{Engine: engine, APIKeys: store})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("authorization", "Bearer pk_test_router")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAPIKeyAdminCreatesListsAndDeletesKey(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/router/v1/api-keys", bytes.NewReader([]byte(`{"tenant_id":"team-a","user_id":"usr_1"}`)))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created apiKeyCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created key: %v", err)
	}
	if created.RawKey == "" || created.APIKey.ID == "" || created.APIKey.TenantID != "team-a" {
		t.Fatalf("unexpected created key: %#v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/router/v1/api-keys", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 list, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(created.RawKey)) {
		t.Fatalf("api key list leaked raw key: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(created.APIKey.ID)) {
		t.Fatalf("api key list missing id: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("authorization", "Bearer "+created.RawKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected generated key to authenticate, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/router/v1/api-keys/"+created.APIKey.ID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 delete, got %d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/router/v1/api-keys", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 list after delete, got %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(created.APIKey.ID)) {
		t.Fatalf("deleted key still listed: %s", rec.Body.String())
	}
}

var _ = compat.APIDialectOpenAI

func testDialectEngine(t *testing.T, dialect compat.APIDialect, capability provider.Capability, service provider.Service, publicModel string, canonicalModel string) (*Engine, *providersim.Simulator) {
	t.Helper()
	reg := registration("providersim-"+string(dialect)+"-0001", "providersim-"+string(dialect), string(dialect)+"@example.test", 10, 0)
	reg.Identity.Service = service
	reg.Identity.Kind = provider.KindAPICompatible
	reg.Capabilities = []provider.Capability{capability, provider.CapabilityUsageRead}
	reg.Models = []provider.Model{{
		ID:           canonicalModel,
		Aliases:      []string{publicModel},
		Capabilities: []provider.Capability{capability},
	}}
	policy, err := ParseRoutingPolicyYAML([]byte(fmt.Sprintf(`
version: routing-policy/v1
model_aliases:
  %s:
    canonical_model: %s
    required_capabilities: [%s]
routes:
  - id: providersim-%s
    match:
      models: [%s]
      api_dialects: [%s]
    candidates:
      - provider: %s
        account: %s@example.test
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
`, publicModel, canonicalModel, capability, dialect, publicModel, dialect, reg.Identity.ProviderID, dialect)))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	registry := provider.NewRegistry()
	if err := registry.Upsert(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(policy, registry, nil)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	sim, err := providersim.New(providersim.Options{Registration: reg})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	return engine, sim
}
