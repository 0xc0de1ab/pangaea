package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/antigravity-compat-proxy/internal/interfaces"
	"github.com/google/antigravity-compat-proxy/internal/models"
)

func TestOpenAIStreamingUsesInvokeStreamChunks(t *testing.T) {
	server := NewServer(&testEngineBridge{stream: []string{"hello ", "from antigravity"}}, APIKeys{OpenAI: "openai-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"antigravity-default",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`))
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("content-type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected event-stream response, got %q", contentType)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"hello "`) || !strings.Contains(body, `"content":"from antigravity"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected stream body: %s", body)
	}
}

func TestOpenAIUnaryConvertsAntigravityToolJSONToToolCalls(t *testing.T) {
	server := NewServer(&testEngineBridge{response: "```json\n{\"tool\":\"get_weather\",\"parameters\":{\"city\":\"Seoul\",\"unit\":\"celsius\"}}\n```"}, APIKeys{OpenAI: "openai-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"antigravity-default",
		"messages":[{"role":"user","content":"weather"}],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]
	}`))
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out models.ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Choices[0].FinishReason != "tool_calls" || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call response, got %#v", out)
	}
	if out.Choices[0].Message.Content != "" {
		t.Fatalf("tool call content should be empty, got %#v", out.Choices[0].Message.Content)
	}
	call := out.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "get_weather" || call.Function.Arguments != `{"city":"Seoul","unit":"celsius"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
}

func TestOpenAIStreamingConvertsAntigravityToolJSONToToolCalls(t *testing.T) {
	server := NewServer(&testEngineBridge{stream: []string{"```json\n{\"tool\":\"get_weather\",", "\"parameters\":{\"city\":\"Seoul\"}}\n```"}}, APIKeys{OpenAI: "openai-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"antigravity-default",
		"messages":[{"role":"user","content":"weather"}],
		"stream":true,
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]
	}`))
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`"tool_calls"`,
		`"name":"get_weather"`,
		`"arguments":"{\"city\":\"Seoul\"}"`,
		`"finish_reason":"tool_calls"`,
		"data: [DONE]",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in stream body: %s", expected, body)
		}
	}
}

func TestGeminiStreamingEndpointUsesSSE(t *testing.T) {
	server := NewServer(&testEngineBridge{stream: []string{"hello ", "from gemini stream"}}, APIKeys{Gemini: "gemini-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/antigravity-default:streamGenerateContent?alt=sse", strings.NewReader(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}]
	}`))
	req.Header.Set("x-goog-api-key", "gemini-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("content-type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected event-stream response, got %q", contentType)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"text":"hello "`) || !strings.Contains(body, `"text":"from gemini stream"`) {
		t.Fatalf("unexpected stream body: %s", body)
	}
}

func TestModelEndpointsExposeDefaultAliasMetadata(t *testing.T) {
	server := NewServer(&testEngineBridge{models: []string{"gpt-oss-120b-medium"}}, APIKeys{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list models.OpenAIModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	var alias models.OpenAIModel
	for _, model := range list.Data {
		if model.ID == "antigravity-default" {
			alias = model
		}
	}
	if alias.ID == "" || alias.Kind != "alias" {
		t.Fatalf("default alias missing from /v1/models: %#v", list.Data)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models/status", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var status map[string]models.ModelDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["antigravity-default"].Kind != "alias" {
		t.Fatalf("default alias missing from /v1/models/status: %#v", status)
	}
}

func TestOpenAIStreamingInitialProviderErrorUsesHTTPStatus(t *testing.T) {
	server := NewServer(&testEngineBridge{streamErr: &interfaces.ProviderError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_exceeded",
		Message:    "quota resets soon",
	}}, APIKeys{OpenAI: "openai-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-oss-120b-medium",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`))
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "rate_limit_exceeded") || !strings.Contains(body, "quota resets soon") {
		t.Fatalf("unexpected error body: %s", body)
	}
}

func TestAnthropicStreamingInitialProviderErrorUsesHTTPStatus(t *testing.T) {
	server := NewServer(&testEngineBridge{streamErr: &interfaces.ProviderError{
		StatusCode: http.StatusServiceUnavailable,
		Code:       "model_capacity_exhausted",
		Message:    "no capacity available",
	}}, APIKeys{Anthropic: "anthropic-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"gemini-2.5-pro",
		"max_tokens":128,
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`))
	req.Header.Set("x-api-key", "anthropic-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "model_capacity_exhausted") || !strings.Contains(body, "no capacity available") {
		t.Fatalf("unexpected error body: %s", body)
	}
}

func TestGeminiStreamingInitialProviderErrorUsesHTTPStatus(t *testing.T) {
	server := NewServer(&testEngineBridge{streamErr: &interfaces.ProviderError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_exceeded",
		Message:    "quota resets soon",
	}}, APIKeys{Gemini: "gemini-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gpt-oss-120b-medium:streamGenerateContent?alt=sse", strings.NewReader(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}]
	}`))
	req.Header.Set("x-goog-api-key", "gemini-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "rate_limit_exceeded") || !strings.Contains(body, "quota resets soon") {
		t.Fatalf("unexpected error body: %s", body)
	}
}

type testEngineBridge struct {
	response  string
	stream    []string
	streamErr error
	models    []string
}

func (e *testEngineBridge) Invoke(context.Context, string, string, []models.ToolDefinition, []models.Media) (*interfaces.ModelResponse, error) {
	return &interfaces.ModelResponse{Content: e.response}, nil
}

func (e *testEngineBridge) InvokeStream(context.Context, string, string, []models.ToolDefinition, []models.Media) (<-chan *interfaces.StreamChunk, error) {
	ch := make(chan *interfaces.StreamChunk, len(e.stream)+1)
	go func() {
		defer close(ch)
		if e.streamErr != nil {
			ch <- &interfaces.StreamChunk{Error: e.streamErr}
			return
		}
		if len(e.stream) == 0 {
			ch <- &interfaces.StreamChunk{Content: e.response}
			return
		}
		for _, part := range e.stream {
			ch <- &interfaces.StreamChunk{Content: part}
		}
	}()
	return ch, nil
}

func (e *testEngineBridge) GetModels(context.Context) ([]string, error) {
	if len(e.models) > 0 {
		return e.models, nil
	}
	return []string{"antigravity-default"}, nil
}

func (e *testEngineBridge) GetDetailedModels(context.Context) (map[string]models.ModelDetail, error) {
	return map[string]models.ModelDetail{"antigravity-default": {Model: "antigravity-default"}}, nil
}

func (e *testEngineBridge) GetUsage(context.Context) (map[string]int, error) {
	return map[string]int{}, nil
}

func (e *testEngineBridge) GetAccount(context.Context) (*models.UserStatus, error) {
	return &models.UserStatus{Email: "antigravity@example.test"}, nil
}

func (e *testEngineBridge) SetCoreCSRF(string) {}

func (e *testEngineBridge) VerifyProtocol(context.Context) error {
	return nil
}

func (e *testEngineBridge) UpdateBackend(context.Context) error {
	return nil
}
