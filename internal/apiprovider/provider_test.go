package apiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestProviderInvokeOpenAICompatibleUpstream(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("authorization") == "Bearer sk_test" {
			sawAuth = true
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "gpt-upstream" || len(request.Messages) != 1 || request.Messages[0].Content != "hello" {
			t.Fatalf("unexpected upstream request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-test",
			Object: "chat.completion",
			Model:  "gpt-upstream",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "world"},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		})
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "sk_test")
	response, err := client.Invoke(context.Background(), mustRegistration(t, client), compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-upstream",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if response.Message.Content[0].Text != "world" || response.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected response: %#v", response)
	}
	usage, err := client.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Requests != 1 || usage.TotalTokens != 7 {
		t.Fatalf("unexpected accumulated usage: %#v", usage)
	}
}

func TestProviderInvokeAnthropicCompatibleUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request compat.AnthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "claude-upstream" || len(request.Messages) != 1 || !strings.Contains(string(request.Messages[0].Content), "hello") {
			t.Fatalf("unexpected upstream request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(compat.AnthropicMessagesResponse{
			ID:         "msg-test",
			Type:       "message",
			Role:       "assistant",
			Model:      "claude-upstream",
			StopReason: "end_turn",
			Content:    []compat.AnthropicContentBlock{{Type: "text", Text: "anthropic world"}},
			Usage:      compat.AnthropicUsage{InputTokens: 5, OutputTokens: 6},
		})
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectAnthropic, "")
	response, err := client.Invoke(context.Background(), mustRegistration(t, client), compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "claude-upstream",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if response.Dialect != compat.APIDialectOpenAI || response.Message.Content[0].Text != "anthropic world" || response.Usage.TotalTokens != 11 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestProviderInvokeDisablesUpstreamStreamingForWrappedSSE(t *testing.T) {
	streamFlags := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var request compat.OpenAIChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode openai request: %v", err)
			}
			streamFlags["openai"] = request.Stream
			_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
				ID:     "chatcmpl-test",
				Object: "chat.completion",
				Model:  "gpt-upstream",
				Choices: []compat.OpenAIChatChoice{{
					Index:        0,
					Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "ok"},
					FinishReason: "stop",
				}},
				Usage: &compat.OpenAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
			})
		case "/v1/messages":
			var request compat.AnthropicMessagesRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode anthropic request: %v", err)
			}
			streamFlags["anthropic"] = request.Stream
			_ = json.NewEncoder(w).Encode(compat.AnthropicMessagesResponse{
				ID:         "msg-test",
				Type:       "message",
				Role:       "assistant",
				Model:      "claude-upstream",
				StopReason: "end_turn",
				Content:    []compat.AnthropicContentBlock{{Type: "text", Text: "ok"}},
				Usage:      compat.AnthropicUsage{InputTokens: 1, OutputTokens: 1},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	openai := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	openaiRequest := testOpenAIRequest("hello")
	openaiRequest.Stream = true
	if _, err := openai.Invoke(context.Background(), mustRegistration(t, openai), openaiRequest); err != nil {
		t.Fatalf("invoke openai: %v", err)
	}

	anthropic := newTestProvider(t, server.URL, compat.APIDialectAnthropic, "")
	anthropicRequest := compat.Request{
		Dialect: compat.APIDialectAnthropic,
		Model:   "claude-upstream",
		Stream:  true,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}
	if _, err := anthropic.Invoke(context.Background(), mustRegistration(t, anthropic), anthropicRequest); err != nil {
		t.Fatalf("invoke anthropic: %v", err)
	}
	if streamFlags["openai"] || streamFlags["anthropic"] {
		t.Fatalf("api-compatible provider should request JSON upstream responses for wrapped SSE: %#v", streamFlags)
	}
}

func TestProviderInvokeStreamOpenAICompatibleUpstreamSSE(t *testing.T) {
	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawStream = request.Stream
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-stream\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-stream\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-stream\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"stream\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	events := []compat.Event{}
	request := testOpenAIRequest("hello")
	request.Stream = true
	response, err := client.InvokeStream(context.Background(), mustRegistration(t, client), request, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if !sawStream {
		t.Fatalf("expected upstream request stream=true")
	}
	if response.ID != "chatcmpl-stream" || response.Model != "gpt-upstream" || response.Message.Content[0].Text != "hello stream" || response.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected stream response: %#v", response)
	}
	if len(events) != 5 || events[0].Type != compat.EventMessageStart || events[1].ContentDelta.Text != "hello " || events[2].ContentDelta.Text != "stream" || events[3].UsageDelta.TotalTokens != 5 || events[4].DoneReason != "stop" {
		t.Fatalf("unexpected stream events: %#v", events)
	}
	usage, err := client.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Requests != 1 || usage.TotalTokens != 5 {
		t.Fatalf("unexpected accumulated usage: %#v", usage)
	}
}

func TestProviderInvokeStreamAnthropicCompatibleUpstreamSSE(t *testing.T) {
	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request compat.AnthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawStream = request.Stream
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-stream\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-upstream\",\"content\":[],\"usage\":{\"input_tokens\":4}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"anthropic \"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"stream\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectAnthropic, "")
	events := []compat.Event{}
	request := compat.Request{
		Dialect: compat.APIDialectAnthropic,
		Model:   "claude-upstream",
		Stream:  true,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}
	response, err := client.InvokeStream(context.Background(), mustRegistration(t, client), request, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if !sawStream {
		t.Fatalf("expected upstream request stream=true")
	}
	if response.ID != "msg-stream" || response.Model != "claude-upstream" || response.Message.Content[0].Text != "anthropic stream" || response.Usage.TotalTokens != 10 {
		t.Fatalf("unexpected stream response: %#v", response)
	}
	if len(events) != 6 || events[0].Type != compat.EventMessageStart || events[2].ContentDelta.Text != "anthropic " || events[3].ContentDelta.Text != "stream" || events[4].UsageDelta.OutputTokens != 6 || events[5].DoneReason != "end_turn" {
		t.Fatalf("unexpected stream events: %#v", events)
	}
}

func TestProviderInvokeReturnsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	_, err := client.Invoke(context.Background(), mustRegistration(t, client), compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-upstream",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected upstream error, got %v", err)
	}
}

func TestProviderReloadsAPIKeyFilePerRequest(t *testing.T) {
	authHeaders := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("authorization"))
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-test",
			Object: "chat.completion",
			Model:  "gpt-upstream",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer server.Close()

	keyPath := filepath.Join(t.TempDir(), "api.key")
	if err := os.WriteFile(keyPath, []byte("sk_first\n"), 0o600); err != nil {
		t.Fatalf("write first key: %v", err)
	}
	client, err := New(Options{
		Registration: testRegistration(),
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_static",
		APIKeyFile:   keyPath,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello")); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("sk_second\n"), 0o600); err != nil {
		t.Fatalf("write second key: %v", err)
	}
	if _, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("again")); err != nil {
		t.Fatalf("second invoke: %v", err)
	}
	if len(authHeaders) != 2 || authHeaders[0] != "Bearer sk_first" || authHeaders[1] != "Bearer sk_second" {
		t.Fatalf("api key file was not reloaded per request: %#v", authHeaders)
	}
}

func TestProviderSupportsRawHeaderAPIKeyAuth(t *testing.T) {
	var sawKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("x-goog-api-key")
		_ = json.NewEncoder(w).Encode(compat.GeminiGenerateContentResponse{
			ModelVersion: "gemini-upstream",
			Candidates: []compat.GeminiCandidate{{
				Content:      compat.GeminiContent{Role: "model", Parts: []compat.GeminiPart{{Text: "ok"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &compat.GeminiUsage{PromptTokenCount: 1, CandidatesTokenCount: 1, TotalTokenCount: 2},
		})
	}))
	defer server.Close()

	client, err := New(Options{
		Registration: testRegistration(),
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectGemini,
		APIKey:       "gemini-key",
		APIKeyMode:   "header",
		APIKeyHeader: "x-goog-api-key",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := client.Invoke(context.Background(), mustRegistration(t, client), compat.Request{
		Dialect: compat.APIDialectGemini,
		Model:   "gemini-upstream",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if sawKey != "gemini-key" {
		t.Fatalf("raw header api key = %q, want gemini-key", sawKey)
	}
}

func TestProviderSupportsQueryParamAPIKeyAuth(t *testing.T) {
	var sawKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.URL.Query().Get("key")
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-test",
			Object: "chat.completion",
			Model:  "gpt-upstream",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer server.Close()

	client, err := New(Options{
		Registration:     testRegistration(),
		BaseURL:          server.URL,
		Dialect:          compat.APIDialectOpenAI,
		APIKey:           "query-key",
		APIKeyMode:       "query",
		APIKeyQueryParam: "key",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello")); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if sawKey != "query-key" {
		t.Fatalf("query api key = %q, want query-key", sawKey)
	}
}

func TestProviderRejectsIncompleteAPIKeyAuthConfig(t *testing.T) {
	_, err := New(Options{
		Registration: testRegistration(),
		BaseURL:      "https://api.example.test",
		Dialect:      compat.APIDialectOpenAI,
		APIKeyMode:   "header",
	})
	if err == nil {
		t.Fatalf("expected missing header error")
	}
}

func newTestProvider(t *testing.T, baseURL string, dialect compat.APIDialect, apiKey string) *Provider {
	t.Helper()
	client, err := New(Options{
		Registration: testRegistration(),
		BaseURL:      baseURL,
		Dialect:      dialect,
		APIKey:       apiKey,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return client
}

func testOpenAIRequest(text string) compat.Request {
	return compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-upstream",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: text}},
		}},
	}
}

func mustRegistration(t *testing.T, client *Provider) provider.Registration {
	t.Helper()
	registration, err := client.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	return registration
}

func testRegistration() provider.Registration {
	account := provider.Account{ID: "acct-api", Display: "api@example.test"}
	now := time.Now().UTC()
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "api-compatible-test",
			ProviderInstanceID: "api-compatible-test-0001",
			NodeID:             "node-api",
			HostName:           "api-host",
			Service:            provider.ServiceDeepSeek,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityUsageRead},
		Models:       []provider.Model{{ID: "gpt-upstream", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account},
		RegisteredAt: now,
	}
}
