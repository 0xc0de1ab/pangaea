package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/compat"
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
