package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestOpenAIStreamingFlushesHeadersBeforeFirstChunk(t *testing.T) {
	oldGrace := streamFirstChunkGrace
	streamFirstChunkGrace = 10 * time.Millisecond
	t.Cleanup(func() { streamFirstChunkGrace = oldGrace })

	server := httptest.NewServer(NewServer(&testEngineBridge{
		stream:      []string{"late chunk"},
		streamDelay: 100 * time.Millisecond,
	}, APIKeys{OpenAI: "openai-key"}, "test").Handler())
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{
		"model":"antigravity-default",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond}}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("stream headers were not flushed before first chunk: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, ": stream opened") || !strings.Contains(body, `"content":"late chunk"`) {
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

func TestOpenAIUnaryUsesAntigravityIRToolCalls(t *testing.T) {
	bridge := &testEngineBridge{
		responseToolCalls: []models.ToolCall{{
			ID:   "call_ir_0",
			Type: "function",
			Function: models.ToolFunction{
				Name:      "get_weather",
				Arguments: `{"city":"Seoul"}`,
			},
		}},
	}
	server := NewServer(bridge, APIKeys{OpenAI: "openai-key"}, "test")
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
	if strings.Contains(bridge.seenPrompt, "Available tools JSON") || strings.Contains(bridge.seenPrompt, "<tool_call>{\"name\"") {
		t.Fatalf("tool call text protocol leaked into AG prompt: %s", bridge.seenPrompt)
	}
	if len(bridge.seenTools) != 1 || bridge.seenTools[0].Name != "get_weather" {
		t.Fatalf("tool definitions were not passed through AG IR: %#v", bridge.seenTools)
	}
	var out models.ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Choices[0].FinishReason != "tool_calls" || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected IR tool call response, got %#v", out)
	}
	if out.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("unexpected tool call: %#v", out.Choices[0].Message.ToolCalls[0])
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

func TestOpenAIStreamingUsesAntigravityIRToolCalls(t *testing.T) {
	server := NewServer(&testEngineBridge{
		streamToolCalls: []models.ToolCall{{
			ID:   "call_ir_0",
			Type: "function",
			Function: models.ToolFunction{
				Name:      "get_weather",
				Arguments: `{"city":"Seoul"}`,
			},
		}},
	}, APIKeys{OpenAI: "openai-key"}, "test")
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
		t.Fatalf("expected streaming 200, got %d: %s", rec.Code, rec.Body.String())
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
	if strings.Contains(body, "Available tools JSON") || strings.Contains(body, `<tool_call>{"name"`) {
		t.Fatalf("tool call text protocol leaked into stream body: %s", body)
	}
}

func TestOpenAIUnaryRejectsIncompleteTextToolCall(t *testing.T) {
	server := NewServer(&testEngineBridge{response: `<tool_call>{"name":"write_file","arguments":{"path":"a.yaml"}`}, APIKeys{OpenAI: "openai-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"antigravity-default",
		"messages":[{"role":"user","content":"write"}],
		"tools":[{"type":"function","function":{"name":"write_file","parameters":{"type":"object"}}}]
	}`))
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "malformed_tool_call") || !strings.Contains(body, "incomplete tool call") {
		t.Fatalf("unexpected error body: %s", body)
	}
	if strings.Contains(body, "<tool_call>") {
		t.Fatalf("raw tool protocol leaked into response: %s", body)
	}
}

func TestOpenAIStreamingRejectsIncompleteTextToolCall(t *testing.T) {
	server := NewServer(&testEngineBridge{stream: []string{`<tool_call>{"name":"write_file",`, `"arguments":{"path":"a.yaml"}`}}, APIKeys{OpenAI: "openai-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"antigravity-default",
		"messages":[{"role":"user","content":"write"}],
		"stream":true,
		"tools":[{"type":"function","function":{"name":"write_file","parameters":{"type":"object"}}}]
	}`))
	req.Header.Set("authorization", "Bearer openai-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected streaming 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "malformed_tool_call") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected stream body: %s", body)
	}
	if strings.Contains(body, `"content":"<tool_call>`) {
		t.Fatalf("raw tool protocol leaked as assistant content: %s", body)
	}
}

func TestAnthropicUnaryRejectsIncompleteTextToolCall(t *testing.T) {
	server := NewServer(&testEngineBridge{response: `<tool_call>{"name":"write_file","arguments":{"path":"a.yaml"}`}, APIKeys{Anthropic: "anthropic-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"antigravity-default",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"write"}],
		"tools":[{"name":"write_file","input_schema":{"type":"object"}}]
	}`))
	req.Header.Set("x-api-key", "anthropic-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "malformed_tool_call") || !strings.Contains(body, "incomplete tool call") {
		t.Fatalf("unexpected error body: %s", body)
	}
	if strings.Contains(body, "<tool_call>") {
		t.Fatalf("raw tool protocol leaked into response: %s", body)
	}
}

func TestAnthropicStreamingRejectsIncompleteTextToolCall(t *testing.T) {
	server := NewServer(&testEngineBridge{stream: []string{`<tool_call>{"name":"write_file",`, `"arguments":{"path":"a.yaml"}`}}, APIKeys{Anthropic: "anthropic-key"}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"antigravity-default",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"write"}],
		"stream":true,
		"tools":[{"name":"write_file","input_schema":{"type":"object"}}]
	}`))
	req.Header.Set("x-api-key", "anthropic-key")
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected streaming 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "malformed_tool_call") {
		t.Fatalf("unexpected stream body: %s", body)
	}
	if strings.Contains(body, "content_block_delta") && strings.Contains(body, "<tool_call>") {
		t.Fatalf("raw tool protocol leaked as Anthropic text delta: %s", body)
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
	response          string
	responseToolCalls []models.ToolCall
	stream            []string
	streamToolCalls   []models.ToolCall
	streamErr         error
	models            []string
	streamDelay       time.Duration
	seenPrompt        string
	seenTools         []models.ToolDefinition
}

func (e *testEngineBridge) Invoke(_ context.Context, _ string, prompt string, tools []models.ToolDefinition, _ []models.Media) (*interfaces.ModelResponse, error) {
	e.seenPrompt = prompt
	e.seenTools = append([]models.ToolDefinition(nil), tools...)
	return &interfaces.ModelResponse{Content: e.response, ToolCalls: e.responseToolCalls}, nil
}

func (e *testEngineBridge) InvokeStream(_ context.Context, _ string, prompt string, tools []models.ToolDefinition, _ []models.Media) (<-chan *interfaces.StreamChunk, error) {
	e.seenPrompt = prompt
	e.seenTools = append([]models.ToolDefinition(nil), tools...)
	ch := make(chan *interfaces.StreamChunk, len(e.stream)+len(e.streamToolCalls)+1)
	go func() {
		defer close(ch)
		if e.streamDelay > 0 {
			time.Sleep(e.streamDelay)
		}
		if e.streamErr != nil {
			ch <- &interfaces.StreamChunk{Error: e.streamErr}
			return
		}
		if len(e.stream) == 0 {
			ch <- &interfaces.StreamChunk{Content: e.response, ToolCalls: e.streamToolCalls}
			return
		}
		for _, part := range e.stream {
			ch <- &interfaces.StreamChunk{Content: part}
		}
		if len(e.streamToolCalls) > 0 {
			ch <- &interfaces.StreamChunk{ToolCalls: e.streamToolCalls}
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
