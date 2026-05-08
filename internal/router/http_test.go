package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHTTPAnthropicModels(t *testing.T) {
	engine, _ := testDialectEngine(t, compat.APIDialectAnthropic, provider.CapabilityAnthropicMessages, provider.ServiceAnthropic, "claude-default", "claude-native")
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out anthropicModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "claude-default" || out.Data[0].Type != "model" || out.FirstID != "claude-default" || out.LastID != "claude-default" {
		t.Fatalf("unexpected Anthropic model list: %#v", out)
	}
}

func TestHTTPGeminiModels(t *testing.T) {
	engine, _ := testGeminiModelsEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out geminiModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Models) != 1 || out.Models[0].Name != "models/gemini-default" || out.Models[0].Version != "gemini-native" {
		t.Fatalf("unexpected Gemini model list: %#v", out)
	}
	if len(out.Models[0].SupportedGenerationMethods) != 2 || out.Models[0].SupportedGenerationMethods[1] != "streamGenerateContent" {
		t.Fatalf("unexpected Gemini generation methods: %#v", out.Models[0].SupportedGenerationMethods)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1beta/models/gemini-default", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected model get 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var model geminiModel
	if err := json.Unmarshal(rec.Body.Bytes(), &model); err != nil {
		t.Fatalf("decode model response: %v", err)
	}
	if model.Name != "models/gemini-default" || model.Version != "gemini-native" {
		t.Fatalf("unexpected Gemini model: %#v", model)
	}
}

func TestHTTPRouterDashboard(t *testing.T) {
	handler := NewHTTPHandler(HTTPOptions{})
	req := httptest.NewRequest(http.MethodGet, "/router/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("content-type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected html content type, got %q", got)
	}
	if rec.Header().Get("x-content-type-options") != "nosniff" {
		t.Fatalf("expected nosniff header")
	}
	if rec.Header().Get("content-security-policy") == "" {
		t.Fatalf("expected content security policy")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Pangaea Router")) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`id="root"`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte("/router/ui/assets/")) {
		t.Fatalf("dashboard body missing expected content: %s", rec.Body.String())
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

func TestHTTPRouteErrorMapsUpstreamRateLimit(t *testing.T) {
	engine, _ := testEngine(t)
	engine.SetInvoker(upstreamRateLimitInvoker{})
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	body := []byte(`{"model":"gpt-5-codex","messages":[{"role":"user","content":"hello"}]}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("retry-after") != "8" {
		t.Fatalf("retry-after header = %q, want 8", rec.Header().Get("retry-after"))
	}
	var out struct {
		Error          string `json:"error"`
		Code           string `json:"code"`
		UpstreamCode   string `json:"upstream_code"`
		UpstreamStatus int    `json:"upstream_status"`
		RetryAfter     string `json:"retry_after"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if out.Code != "upstream_error" || out.UpstreamCode != "rate_limit_exceeded" || out.UpstreamStatus != http.StatusTooManyRequests || out.RetryAfter != "8" || !strings.Contains(out.Error, "upstream quota exhausted") {
		t.Fatalf("unexpected route error payload: %#v", out)
	}
}

func TestHTTPControlCommandsRequireConfirmationAndReason(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "refresh requires confirm",
			path: "/router/v1/providers/codex-samtest-a1/auth/refresh",
			body: `{"reason":"manual"}`,
		},
		{
			name: "refresh requires reason",
			path: "/router/v1/providers/codex-samtest-a1/auth/refresh",
			body: `{"confirm":true}`,
		},
		{
			name: "drain requires confirm",
			path: "/router/v1/providers/codex-samtest-a1/drain",
			body: `{"drain":true,"reason":"maintenance"}`,
		},
		{
			name: "drain requires reason",
			path: "/router/v1/providers/codex-samtest-a1/drain",
			body: `{"drain":true,"confirm":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader([]byte(tt.body)))
			req.Header.Set("content-type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
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

func TestHTTPAnthropicMessagesStreamsSSEWithSimulator(t *testing.T) {
	engine, sim := testDialectEngine(t, compat.APIDialectAnthropic, provider.CapabilityAnthropicMessages, provider.ServiceAnthropic, "claude-sim", "claude-native")
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{
		"model":"claude-sim",
		"max_tokens":64,
		"stream":true,
		"messages":[{"role":"user","content":"hello anthropic stream"}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_anthropic_stream_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("content-type"); got != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"event: message_start",
		"event: content_block_delta",
		`"type":"text_delta"`,
		"providersim: hello anthropic stream",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected Anthropic stream body to contain %q, got %s", want, body)
		}
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

func TestHTTPDashboardCompatGeminiGenerateContentAlias(t *testing.T) {
	engine, sim := testDialectEngine(t, compat.APIDialectGemini, provider.CapabilityGeminiGenerateContent, provider.ServiceGemini, "gemini-sim", "gemini-native")
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/router/v1/compat/v1beta/models/gemini-sim:generateContent", bytes.NewReader([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello gemini compat"}]}]
	}`)))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response compat.GeminiGenerateContentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Candidates) != 1 || response.Candidates[0].Content.Parts[0].Text != "providersim: hello gemini compat" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestHTTPGeminiStreamGenerateContentWithSimulator(t *testing.T) {
	engine, sim := testDialectEngine(t, compat.APIDialectGemini, provider.CapabilityGeminiGenerateContent, provider.ServiceGemini, "gemini-sim", "gemini-native")
	engine.SetInvoker(sim)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-sim:streamGenerateContent?alt=sse", bytes.NewReader([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello gemini stream"}]}]
	}`)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_gemini_stream_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("content-type"); got != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data:") || !strings.Contains(body, "providersim: hello gemini stream") || !strings.Contains(body, `"modelVersion":"gemini-native"`) {
		t.Fatalf("unexpected Gemini stream body: %s", body)
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

func TestHTTPRouterAdminRequiresAPIKeyWhenConfigured(t *testing.T) {
	engine, _ := testEngine(t)
	store := security.NewAPIKeyStore([]byte("pepper"))
	if _, err := store.AddRawKey("admin_key", "pk_admin_router", "ops", "admin_1"); err != nil {
		t.Fatalf("add key: %v", err)
	}
	handler := NewHTTPHandler(HTTPOptions{Engine: engine, APIKeys: store})

	req := httptest.NewRequest(http.MethodGet, "/router/v1/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer token, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/router/v1/providers", nil)
	req.Header.Set("authorization", "Bearer pk_admin_router")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d body=%s", rec.Code, rec.Body.String())
	}

	body := []byte(`{"scope":{"tenant_id":"ops","user_id":"admin_1","api_key_id":"admin_key","model":"gpt-5-codex"},"limit":{"max_tokens":50,"max_requests":5}}`)
	req = httptest.NewRequest(http.MethodPut, "/router/v1/quotas/limits", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer pk_admin_router")
	req.Header.Set("x-request-id", "req_admin_auth_quota")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected quota update 200 with bearer token, got %d body=%s", rec.Code, rec.Body.String())
	}
	events := engine.AuditEvents(1)
	if len(events) != 1 || events[0].Actor.APIKeyID != "admin_key" || events[0].Actor.UserID != "admin_1" || events[0].Actor.TenantID != "ops" {
		t.Fatalf("audit actor did not use authenticated admin principal: %#v", events)
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
	req.Header.Set("authorization", "Bearer "+created.RawKey)
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
	req.Header.Set("authorization", "Bearer "+created.RawKey)
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

func TestHTTPAPIKeyAdminCreatesDisabledKey(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	body := []byte(`{"id":"key_disabled","raw_key":"pk_disabled_router","tenant_id":"team-a","user_id":"usr_1","disabled":true}`)

	req := httptest.NewRequest(http.MethodPost, "/router/v1/api-keys", bytes.NewReader(body))
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
	if !created.APIKey.Disabled {
		t.Fatalf("expected disabled key principal, got %#v", created.APIKey)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("authorization", "Bearer pk_disabled_router")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected disabled key to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPAuditEventsRecordsAdminActions(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})

	createReq := httptest.NewRequest(http.MethodPost, "/router/v1/api-keys", bytes.NewReader([]byte(`{"tenant_id":"team-a","user_id":"usr_1"}`)))
	createReq.Header.Set("content-type", "application/json")
	createReq.Header.Set("x-pangaea-user-id", "admin_1")
	createReq.Header.Set("x-request-id", "req_admin_create")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected create 201, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	var created apiKeyCreateResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created key: %v", err)
	}

	quotaReq := httptest.NewRequest(http.MethodPut, "/router/v1/quotas/limits", bytes.NewReader([]byte(`{
		"scope":{"tenant_id":"team-a","user_id":"usr_1","api_key_id":"key_1","model":"gpt-5-codex"},
		"limit":{"max_tokens":123,"max_requests":7}
	}`)))
	quotaReq.Header.Set("content-type", "application/json")
	quotaReq.Header.Set("authorization", "Bearer "+created.RawKey)
	quotaReq.Header.Set("x-pangaea-user-id", "admin_1")
	quotaRec := httptest.NewRecorder()
	handler.ServeHTTP(quotaRec, quotaReq)
	if quotaRec.Code != http.StatusOK {
		t.Fatalf("expected quota 200, got %d body=%s", quotaRec.Code, quotaRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/router/v1/api-keys/"+created.APIKey.ID, nil)
	deleteReq.Header.Set("authorization", "Bearer "+created.RawKey)
	deleteReq.Header.Set("x-pangaea-user-id", "admin_1")
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected delete 204, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/router/v1/providers/codex-samtest-a1/auth/refresh", bytes.NewReader([]byte(`{"reason":"manual test","timeout_seconds":1,"confirm":true}`)))
	refreshReq.Header.Set("content-type", "application/json")
	refreshReq.Header.Set("x-pangaea-user-id", "admin_1")
	refreshRec := httptest.NewRecorder()
	handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusConflict {
		t.Fatalf("expected refresh conflict without control session, got %d body=%s", refreshRec.Code, refreshRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/router/v1/audit/events?limit=10", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected audit 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(created.RawKey)) {
		t.Fatalf("audit events leaked raw api key: %s", rec.Body.String())
	}
	var out struct {
		Events []AuditEvent `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode audit events: %v", err)
	}
	if len(out.Events) != 4 {
		t.Fatalf("expected four audit events, got %#v", out.Events)
	}
	if out.Events[0].Type != AuditEventProviderAuthRefresh || out.Events[0].Outcome != AuditOutcomeFailed {
		t.Fatalf("expected newest failed refresh event, got %#v", out.Events[0])
	}
	if out.Events[0].Target.ProviderInstanceID != "codex-samtest-a1" || out.Events[0].Reason != "manual test" {
		t.Fatalf("unexpected refresh audit target: %#v", out.Events[0])
	}
	if out.Events[1].Type != AuditEventAPIKeyDelete || out.Events[1].Target.APIKeyID != created.APIKey.ID {
		t.Fatalf("expected api key delete event, got %#v", out.Events[1])
	}
	if out.Events[2].Type != AuditEventQuotaLimitSet || out.Events[2].Target.Model != "gpt-5-codex" {
		t.Fatalf("expected quota audit event, got %#v", out.Events[2])
	}
	if out.Events[3].Type != AuditEventAPIKeyCreate || out.Events[3].Actor.UserID != "admin_1" {
		t.Fatalf("expected api key create audit event with actor, got %#v", out.Events[3])
	}
}

var _ = compat.APIDialectOpenAI

type upstreamRateLimitInvoker struct{}

func (upstreamRateLimitInvoker) Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error) {
	return compat.Response{}, &provider.UpstreamError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_exceeded",
		Message:    "upstream quota exhausted",
		RetryAfter: "8",
	}
}

func testDialectEngine(t *testing.T, dialect compat.APIDialect, capability provider.Capability, service provider.Service, publicModel string, canonicalModel string) (*Engine, *providersim.Simulator) {
	t.Helper()
	reg := registration("providersim-"+string(dialect)+"-0001", "providersim-"+string(dialect), string(dialect)+"@example.test", 10, 0)
	reg.Identity.Service = service
	reg.Identity.Kind = provider.KindAPICompatible
	reg.Capabilities = []provider.Capability{capability, provider.CapabilityStreamSSE, provider.CapabilityUsageRead}
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

func testGeminiModelsEngine(t *testing.T) (*Engine, *providersim.Simulator) {
	t.Helper()
	reg := registration("providersim-gemini-0001", "providersim-gemini", "gemini@example.test", 10, 0)
	reg.Identity.Service = provider.ServiceGemini
	reg.Identity.Kind = provider.KindAPICompatible
	reg.Capabilities = []provider.Capability{provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE, provider.CapabilityUsageRead}
	reg.Models = []provider.Model{{
		ID:           "gemini-native",
		Aliases:      []string{"gemini-default"},
		Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE},
	}}
	policy, err := ParseRoutingPolicyYAML([]byte(`
version: routing-policy/v1
model_aliases:
  gemini-default:
    canonical_model: gemini-native
    required_capabilities: [api.gemini.generateContent, stream.sse]
routes:
  - id: providersim-gemini
    match:
      models: [gemini-default]
      api_dialects: [gemini]
    candidates:
      - provider: providersim-gemini
        account: gemini@example.test
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
`))
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
