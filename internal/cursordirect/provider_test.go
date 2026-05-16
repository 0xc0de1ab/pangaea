package cursordirect

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestNewRequiresBaseURLWithoutEnv(t *testing.T) {
	t.Setenv(envBaseURL, "")
	reg := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "cursor",
			ProviderInstanceID: "cursor-1",
			NodeID:             "n1",
			HostName:           "h1",
			Service:            provider.ServiceCursor,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models:       []provider.Model{{ID: "gpt-test"}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}
	if _, err := New(Options{Registration: reg}); err == nil || !strings.Contains(err.Error(), "base url") {
		t.Fatalf("expected base url error, got %v", err)
	}
}

func TestInvokeOpenAIChatRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			Object: "chat.completion",
			Model:  "gpt-test",
			Choices: []compat.OpenAIChatChoice{{
				Message: compat.OpenAIChatMessage{
					Role:    "assistant",
					Content: "hello",
				},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{
				PromptTokens:     3,
				CompletionTokens: 2,
				TotalTokens:      5,
			},
		})
	}))
	defer srv.Close()

	reg := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "cursor",
			ProviderInstanceID: "cursor-1",
			NodeID:             "n1",
			HostName:           "h1",
			Service:            provider.ServiceCursor,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models:       []provider.Model{{ID: "gpt-test"}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}
	p, err := New(Options{
		Registration: reg,
		BaseURL:      srv.URL,
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Invoke(t.Context(), reg, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-test",
		Messages: []compat.Message{{
			Role: compat.MessageRoleUser,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: "hi",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content[0].Text != "hello" {
		t.Fatalf("unexpected reply: %#v", resp.Message.Content)
	}
}

func TestModelsAnnotateCursorAutoGroup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	reg := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "cursor",
			ProviderInstanceID: "cursor-1",
			NodeID:             "n1",
			HostName:           "h1",
			Service:            provider.ServiceCursor,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models: []provider.Model{
			{ID: "auto"},
			{ID: "composer-2"},
			{ID: "gpt-5.2"},
		},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}
	p, err := New(Options{
		Registration: reg,
		BaseURL:      srv.URL,
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ForceModelDiscovery() {
		t.Fatalf("cursor direct provider should force model metadata refresh")
	}
	models, err := p.Models(t.Context())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	byID := map[string]provider.Model{}
	for _, model := range models {
		byID[model.ID] = model
	}
	if byID["auto"].Kind != "group" || !containsString(byID["auto"].GroupMembers, "composer-2") || !containsString(byID["auto"].GroupMembers, "gpt-5.2") {
		t.Fatalf("auto metadata = %#v", byID["auto"])
	}
}

func TestInvokeStreamOpenAISSE(t *testing.T) {
	var sawStream bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Fatalf("Accept: %q", r.Header.Get("Accept"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true {
			t.Fatalf("expected stream true, got %#v", body["stream"])
		}
		sawStream = true
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flush")
		}
		chunk := `{"choices":[{"delta":{"content":"hello"},"index":0}],"id":"x","model":"gpt-test"}`
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"finish_reason":"stop","delta":{}}],"id":"x"}`)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	reg := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "cursor",
			ProviderInstanceID: "cursor-1",
			NodeID:             "n1",
			HostName:           "h1",
			Service:            provider.ServiceCursor,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models:       []provider.Model{{ID: "gpt-test"}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}
	p, err := New(Options{
		Registration: reg,
		BaseURL:      srv.URL,
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	var events []compat.Event
	emit := func(ev compat.Event) error {
		events = append(events, ev)
		return nil
	}
	resp, err := p.InvokeStream(t.Context(), reg, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-test",
		Stream:  true,
		Messages: []compat.Message{{
			Role: compat.MessageRoleUser,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: "hi",
			}},
		}},
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if !sawStream {
		t.Fatal("handler never ran")
	}
	if resp.Message.Content[0].Text != "hello" {
		t.Fatalf("reply %q", resp.Message.Content[0].Text)
	}
	if len(events) < 3 {
		t.Fatalf("events: %+v", events)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestInvokeStreamNonStreamEmitsSyntheticEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			Object: "chat.completion",
			Model:  "gpt-test",
			Choices: []compat.OpenAIChatChoice{{
				Message: compat.OpenAIChatMessage{
					Role:    "assistant",
					Content: "ok",
				},
				FinishReason: "stop",
			}},
		})
	}))
	defer srv.Close()

	reg := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "cursor",
			ProviderInstanceID: "cursor-1",
			NodeID:             "n1",
			HostName:           "h1",
			Service:            provider.ServiceCursor,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models:       []provider.Model{{ID: "gpt-test"}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}
	p, err := New(Options{
		Registration: reg,
		BaseURL:      srv.URL,
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	var types []compat.EventType
	emit := func(ev compat.Event) error {
		types = append(types, ev.Type)
		return nil
	}
	_, err = p.InvokeStream(t.Context(), reg, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-test",
		Stream:  false,
		Messages: []compat.Message{{
			Role: compat.MessageRoleUser,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: "hi",
			}},
		}},
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) < 3 || types[0] != compat.EventMessageStart || types[len(types)-1] != compat.EventDone {
		t.Fatalf("got %+v", types)
	}
	hasDelta := false
	for _, ty := range types {
		if ty == compat.EventContentDelta {
			hasDelta = true
			break
		}
	}
	if !hasDelta {
		t.Fatalf("missing content_delta in %+v", types)
	}
}

func TestInvokeStreamOpenAISSE_toolCallsDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flush")
		}
		chunks := []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather"}}]}}],"model":"gpt-test","id":"rid"}`,
			fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, `{"city":"`),
			fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, `NYC"}`),
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	reg := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "cursor",
			ProviderInstanceID: "cursor-1",
			NodeID:             "n1",
			HostName:           "h1",
			Service:            provider.ServiceCursor,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models:       []provider.Model{{ID: "gpt-test"}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}
	p, err := New(Options{
		Registration: reg,
		BaseURL:      srv.URL,
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	var toolEv int
	emit := func(ev compat.Event) error {
		if ev.Type == compat.EventToolCallDelta {
			toolEv++
		}
		return nil
	}
	resp, err := p.InvokeStream(t.Context(), reg, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-test",
		Stream:  true,
		Messages: []compat.Message{{
			Role: compat.MessageRoleUser,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: "forecast?",
			}},
		}},
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls: %+v", resp.Message.ToolCalls)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "weather" || tc.Arguments != `{"city":"NYC"}` {
		t.Fatalf("merged tool call: %+v", tc)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("stop reason %q", resp.StopReason)
	}
	if toolEv < 2 {
		t.Fatalf("expected multiple tool_call_delta events, got %d", toolEv)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(tc.Arguments), &payload); err != nil || payload["city"] != "NYC" {
		t.Fatalf("arguments JSON: %q err=%v", tc.Arguments, err)
	}
}
