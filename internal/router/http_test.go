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
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
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
