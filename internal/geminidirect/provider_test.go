package geminidirect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestInvokeUsesCodeAssistGenerateContent(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	var sawGenerate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			_ = json.NewEncoder(w).Encode(loadCodeAssistResponse{CloudaiCompanionProject: "fine-canyon-test"})
		case "/v1internal:generateContent":
			sawGenerate = true
			var body codeAssistGenerateRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if body.Project != "fine-canyon-test" {
				t.Fatalf("project = %q", body.Project)
			}
			if body.Model != "gemini-2.5-flash" {
				t.Fatalf("model = %q", body.Model)
			}
			if body.UserPromptID == "" {
				t.Fatal("missing user_prompt_id")
			}
			_ = json.NewEncoder(w).Encode(codeAssistGenerateResponse{
				Response: codeAssistModelResponse{
					ResponseID:   "response-1",
					ModelVersion: "gemini-2.5-flash",
					Candidates: []codeAssistCandidate{{
						FinishReason: "STOP",
						Content: codeAssistContent{Role: "model", Parts: []codeAssistPart{
							{Thought: true, Text: "hidden reasoning"},
							{Text: "hello from direct http"},
						}},
					}},
					UsageMetadata: &compat.GeminiUsage{PromptTokenCount: 3, CandidatesTokenCount: 4, TotalTokenCount: 7},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL, authPath)
	response, err := p.Invoke(context.Background(), testRegistration(), compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gemini-2.5-flash",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !sawGenerate {
		t.Fatal("generateContent was not called")
	}
	if got := response.Message.Content[0].Text; got != "hello from direct http" {
		t.Fatalf("response text = %q", got)
	}
	if strings.Contains(response.Message.Content[0].Text, "hidden reasoning") {
		t.Fatal("thought text leaked into response")
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestInvokeStreamSkipsThoughtChunks(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			_ = json.NewEncoder(w).Encode(loadCodeAssistResponse{CloudaiCompanionProject: "fine-canyon-test"})
		case "/v1internal:streamGenerateContent":
			if r.URL.Query().Get("alt") != "sse" {
				t.Fatalf("alt = %q", r.URL.Query().Get("alt"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSE(t, w, codeAssistGenerateResponse{Response: codeAssistModelResponse{
				ResponseID:   "stream-1",
				ModelVersion: "gemini-3-flash-preview",
				Candidates: []codeAssistCandidate{{
					Content: codeAssistContent{Role: "model", Parts: []codeAssistPart{{Thought: true, Text: "thinking"}}},
				}},
			}})
			writeSSE(t, w, codeAssistGenerateResponse{Response: codeAssistModelResponse{
				ResponseID:   "stream-1",
				ModelVersion: "gemini-3-flash-preview",
				Candidates: []codeAssistCandidate{{
					FinishReason: "STOP",
					Content:      codeAssistContent{Role: "model", Parts: []codeAssistPart{{Text: "visible"}}},
				}},
				UsageMetadata: &compat.GeminiUsage{PromptTokenCount: 2, CandidatesTokenCount: 3, TotalTokenCount: 5},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL, authPath)
	var events []compat.Event
	response, err := p.InvokeStream(context.Background(), testRegistration(), compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gemini-3-flash-preview",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if got := response.Message.Content[0].Text; got != "visible" {
		t.Fatalf("response text = %q", got)
	}
	for _, event := range events {
		if event.ContentDelta != nil && strings.Contains(event.ContentDelta.Text, "thinking") {
			t.Fatal("thought text leaked into stream")
		}
	}
	if response.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestModelsEnrichesQuota(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	resetAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			_ = json.NewEncoder(w).Encode(loadCodeAssistResponse{CloudaiCompanionProject: "fine-canyon-test"})
		case "/v1internal:retrieveUserQuota":
			_ = json.NewEncoder(w).Encode(retrieveUserQuotaResponse{Buckets: []quotaBucket{
				{ModelID: "gemini-2.5-flash", RemainingFraction: 0.75, ResetTime: resetAt},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL, authPath)
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	var flash provider.Model
	for _, model := range models {
		if model.ID == "gemini-2.5-flash" {
			flash = model
			break
		}
	}
	if flash.Quota == nil {
		t.Fatal("gemini-2.5-flash quota missing")
	}
	if flash.Quota.RemainingPct != 75 {
		t.Fatalf("remaining pct = %v", flash.Quota.RemainingPct)
	}
}

func TestModelsAreDiscoveredFromQuotaBuckets(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	resetAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			_ = json.NewEncoder(w).Encode(loadCodeAssistResponse{CloudaiCompanionProject: "fine-canyon-test"})
		case "/v1internal:retrieveUserQuota":
			_ = json.NewEncoder(w).Encode(retrieveUserQuotaResponse{Buckets: []quotaBucket{
				{ModelID: "gemini-3.1-pro", RemainingFraction: 0.9, ResetTime: resetAt},
				{ModelID: "gemini-3-flash-preview", RemainingFraction: 0.8, ResetTime: resetAt},
				{ModelID: "gemini-3.1-flash-lite-preview", RemainingFraction: 1, ResetTime: resetAt},
				{ModelID: "gemma-4-31b-it", RemainingFraction: 0.6, ResetTime: resetAt},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL, authPath)
	if !p.ForceModelDiscovery() {
		t.Fatal("gemini direct provider must force model discovery even when static models are configured")
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	byID := map[string]provider.Model{}
	for _, model := range models {
		byID[model.ID] = model
	}
	pro, ok := byID["gemini-3.1-pro-preview"]
	if !ok {
		t.Fatalf("discovered models missing canonical pro entry: %#v", models)
	}
	if !containsString(pro.Aliases, "gemini-3.1-pro") {
		t.Fatalf("pro aliases = %#v, want raw quota id", pro.Aliases)
	}
	if pro.Quota == nil || pro.Quota.RemainingPct != 90 {
		t.Fatalf("pro quota = %#v", pro.Quota)
	}
	auto := byID["auto-gemini-3"]
	if auto.Kind != "group" {
		t.Fatalf("auto kind = %q", auto.Kind)
	}
	if !reflect.DeepEqual(auto.GroupMembers, []string{"gemini-3.1-pro-preview", "gemini-3-flash-preview"}) {
		t.Fatalf("auto group members = %#v", auto.GroupMembers)
	}
	if auto.Quota == nil || auto.Quota.RemainingPct != 80 {
		t.Fatalf("auto quota = %#v", auto.Quota)
	}
	gemma, ok := byID["gemma-4-31b-it"]
	if !ok {
		t.Fatalf("quota-discovered gemma model missing: %#v", models)
	}
	if gemma.Quota == nil || gemma.Quota.RemainingPct != 60 {
		t.Fatalf("gemma quota = %#v", gemma.Quota)
	}
	if !containsString(gemma.Aliases, "Gemma 4 31B IT") {
		t.Fatalf("gemma aliases = %#v", gemma.Aliases)
	}
}

func TestAuthReadsExpiryFromFile(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(-time.Minute))
	p := newTestProvider(t, "http://127.0.0.1:1", authPath)
	auth, err := p.Auth()
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if auth.Status != provider.AuthExpired {
		t.Fatalf("auth status = %q, want expired", auth.Status)
	}
}

func TestCodeAssistDefaultsDoNotForceThinkingForGemini25Flash(t *testing.T) {
	body, err := codeAssistRequestBody(compat.GeminiGenerateContentRequest{
		Contents: []compat.GeminiContent{{
			Role:  "user",
			Parts: []compat.GeminiPart{{Text: "hello"}},
		}},
		GenerationConfig: &compat.GeminiGenerationConfig{MaxOutputTokens: 1024},
	}, "gemini-2.5-flash", "")
	if err != nil {
		t.Fatalf("codeAssistRequestBody: %v", err)
	}
	generationConfig, ok := body["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig missing: %#v", body)
	}
	if _, exists := generationConfig["thinkingConfig"]; exists {
		t.Fatalf("unexpected default thinkingConfig for non-reasoning request: %#v", generationConfig["thinkingConfig"])
	}
	if generationConfig["maxOutputTokens"] != float64(1024) {
		t.Fatalf("maxOutputTokens = %#v", generationConfig["maxOutputTokens"])
	}
}

func TestCodeAssistDefaultsApplyThinkingWhenReasoningRequested(t *testing.T) {
	body, err := codeAssistRequestBody(compat.GeminiGenerateContentRequest{
		Contents: []compat.GeminiContent{{
			Role:  "user",
			Parts: []compat.GeminiPart{{Text: "hello"}},
		}},
	}, "gemini-2.5-flash", "high")
	if err != nil {
		t.Fatalf("codeAssistRequestBody: %v", err)
	}
	generationConfig := body["generationConfig"].(map[string]any)
	thinkingConfig, ok := generationConfig["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("thinkingConfig missing: %#v", generationConfig)
	}
	if thinkingConfig["includeThoughts"] != true || thinkingConfig["thinkingBudget"] != float64(8192) {
		t.Fatalf("thinkingConfig = %#v", thinkingConfig)
	}
}

func TestDirectHTTPStreamRequestMatchesACPFixtureShape(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	transport := &captureCodeAssistTransport{t: t}
	p, err := New(Options{
		Registration: testRegistration(),
		AuthPath:     authPath,
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	temperature := 1.0
	_, err = p.InvokeStream(context.Background(), testRegistration(), acpFixtureCanonicalRequest(temperature), func(compat.Event) error {
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if transport.streamRequest == nil {
		t.Fatal("streamGenerateContent request was not captured")
	}
	var fixture struct {
		Request normalizedOutboundRequest `json:"request"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "acp_stream_request_shape.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if !reflect.DeepEqual(*transport.streamRequest, fixture.Request) {
		actual, _ := json.MarshalIndent(transport.streamRequest, "", "  ")
		expected, _ := json.MarshalIndent(fixture.Request, "", "  ")
		t.Fatalf("direct-http request differs from ACP fixture shape\nactual:\n%s\nexpected:\n%s", actual, expected)
	}
}

func TestDirectHTTPToolCallbackRequestMatchesACPFixtureShape(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	transport := &captureCodeAssistTransport{t: t}
	p, err := New(Options{
		Registration: testRegistration(),
		AuthPath:     authPath,
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	temperature := 1.0
	_, err = p.InvokeStream(context.Background(), testRegistration(), acpFixtureToolCallbackRequest(temperature), func(compat.Event) error {
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if transport.streamRequest == nil {
		t.Fatal("streamGenerateContent request was not captured")
	}
	var fixture struct {
		Request normalizedOutboundRequest `json:"request"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "acp_tool_callback_request_shape.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if !reflect.DeepEqual(*transport.streamRequest, fixture.Request) {
		actual, _ := json.MarshalIndent(transport.streamRequest, "", "  ")
		expected, _ := json.MarshalIndent(fixture.Request, "", "  ")
		t.Fatalf("direct-http tool callback request differs from ACP fixture shape\nactual:\n%s\nexpected:\n%s", actual, expected)
	}
}

func TestInvokeStreamDispatchesMCPToolAndContinuesWithACPShape(t *testing.T) {
	authPath := writeAuthFile(t, time.Now().Add(time.Hour))
	transport := &captureCodeAssistTransport{
		t: t,
		streamResponses: []string{
			`data: {"response":{"responseId":"stream-tool","modelVersion":"gemini-3-flash-preview","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"functionCall":{"name":"mcp_pangaea-fixture_fixture_echo","id":"mcp_pangaea-fixture_fixture_echo_1778319661619_0","args":{"text":"mcp-ok"}}}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11}}}` + "\n\n",
			`data: {"response":{"responseId":"stream-final","modelVersion":"gemini-3-flash-preview","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"final after mcp"}]}}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3,"totalTokenCount":15}}}` + "\n\n",
		},
	}
	dispatcher := &fakeToolDispatcher{}
	p, err := New(Options{
		Registration:   testRegistration(),
		AuthPath:       authPath,
		HTTPClient:     &http.Client{Transport: transport},
		ToolDispatcher: dispatcher,
		MaxToolRounds:  2,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	temperature := 1.0
	var events []compat.Event
	response, err := p.InvokeStream(context.Background(), testRegistration(), acpFixtureCanonicalRequest(temperature), func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if got := response.Message.Content[0].Text; got != "final after mcp" {
		t.Fatalf("response text = %q", got)
	}
	if response.Usage.TotalTokens != 26 {
		t.Fatalf("aggregated usage = %#v, want total 26", response.Usage)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Name != "mcp_pangaea-fixture_fixture_echo" {
		t.Fatalf("dispatcher calls = %#v", dispatcher.calls)
	}
	for _, event := range events {
		if event.Type == compat.EventToolCallDelta {
			t.Fatal("internal MCP tool call leaked to downstream stream")
		}
	}
	if len(events) == 0 {
		t.Fatal("final stream events were not emitted")
	}
	if len(transport.streamRequests) != 2 {
		t.Fatalf("stream request count = %d, want 2", len(transport.streamRequests))
	}
	var fixture struct {
		Request normalizedOutboundRequest `json:"request"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "acp_tool_callback_request_shape.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if !reflect.DeepEqual(transport.streamRequests[1], fixture.Request) {
		actual, _ := json.MarshalIndent(transport.streamRequests[1], "", "  ")
		expected, _ := json.MarshalIndent(fixture.Request, "", "  ")
		t.Fatalf("continued direct-http request differs from ACP fixture shape\nactual:\n%s\nexpected:\n%s", actual, expected)
	}
}

func newTestProvider(t *testing.T, baseURL string, authPath string) *Provider {
	t.Helper()
	p, err := New(Options{
		Registration: testRegistration(),
		BaseURL:      baseURL,
		AuthPath:     authPath,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func testRegistration() provider.Registration {
	now := time.Now().UTC()
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "gemini-cli",
			ProviderInstanceID: "gemini-cli",
			NodeID:             "node-1",
			HostName:           "host-1",
			Service:            provider.ServiceGemini,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
		},
		Models: []provider.Model{{
			ID:               "gemini-2.5-flash",
			Capabilities:     []provider.Capability{provider.CapabilityGeminiGenerateContent, provider.CapabilityStreamSSE},
			ContextTokens:    1_048_576,
			MaxContextTokens: 1_048_576,
		}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Refreshable: true, ExpiresAt: now.Add(time.Hour)},
		RegisteredAt: now,
	}
}

func acpFixtureCanonicalRequest(temperature float64) compat.Request {
	return compat.Request{
		Dialect:         compat.APIDialectOpenAI,
		Model:           "gemini-3-flash-preview",
		Tools:           acpFixtureToolDefinitions(),
		Temperature:     &temperature,
		ReasoningEffort: "high",
		Messages: []compat.Message{
			{
				Role: compat.MessageRoleSystem,
				Content: []compat.ContentPart{{
					Type: compat.ContentPartText,
					Text: "You are Gemini CLI, an interactive CLI agent specializing in software engineering tasks.",
				}},
			},
			{
				Role: compat.MessageRoleUser,
				Content: []compat.ContentPart{{
					Type: compat.ContentPartText,
					Text: "<session_context>\nThis is the Gemini CLI. We are setting up the context for our chat.\n</session_context>",
				}},
			},
			{
				Role: compat.MessageRoleUser,
				Content: []compat.ContentPart{
					{Type: compat.ContentPartText, Text: "Use the attached text resource and image if present. Reply with exactly ACP_OK plus one word summary.@file:///fixture/sample.md"},
					{Type: compat.ContentPartText, Text: "\n--- Content from referenced context ---"},
					{Type: compat.ContentPartText, Text: "\nContent from @file:///fixture/sample.md:\n"},
					{Type: compat.ContentPartText, Text: "# Fixture Notes\n\n1. Streaming responses should arrive incrementally.\n2. Buffered responses should arrive as one complete object.\n\n```go\nfmt.Println(\"markdown fixture\")\n```"},
				},
			},
		},
	}
}

func acpFixtureToolCallbackRequest(temperature float64) compat.Request {
	request := acpFixtureCanonicalRequest(temperature)
	request.Messages = append(request.Messages,
		compat.Message{
			Role: compat.MessageRoleAssistant,
			ToolCalls: []compat.ToolCall{{
				Index:     0,
				ID:        "mcp_pangaea-fixture_fixture_echo_1778319661619_0",
				Type:      compat.ToolCallFunction,
				Name:      "mcp_pangaea-fixture_fixture_echo",
				Arguments: `{"text":"mcp-ok"}`,
			}},
		},
		compat.Message{
			Role:       compat.MessageRoleTool,
			Name:       "mcp_pangaea-fixture_fixture_echo",
			ToolCallID: "mcp_pangaea-fixture_fixture_echo_1778319661619_0",
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: `{"output":"fixture_echo:mcp-ok"}`,
			}},
		},
	)
	return request
}

func acpFixtureToolDefinitions() []compat.ToolDefinition {
	return []compat.ToolDefinition{
		{
			Name:        "update_topic",
			Description: "Manages your narrative flow. Include `title` and `summary` only when starting a new Chapter (logical phase) or shifting strategic intent.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "The title of the new topic or chapter.",
					},
					"summary": map[string]any{
						"type":        "string",
						"description": "(OPTIONAL) A detailed summary (5-10 sentences) covering both the work completed in the previous topic and the strategic intent of the new topic. This is required when transitioning between topics to maintain continuity.",
					},
					"strategic_intent": map[string]any{
						"type":        "string",
						"description": "A mandatory one-sentence statement of your immediate intent.",
					},
					"wait_for_previous": map[string]any{
						"type":        "boolean",
						"description": "Set to true to wait for all previously requested tools in this turn to complete before starting. Set to false (or omit) to run in parallel. Use true when this tool depends on the output of previous tools.",
					},
				},
				"required": []any{"strategic_intent"},
			},
		},
		{
			Name:        "mcp_pangaea-fixture_fixture_echo",
			Description: "Echo fixture input for Pangaea capture tests.",
			Source:      "mcp:pangaea-fixture",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type": "string",
					},
					"wait_for_previous": map[string]any{
						"type":        "boolean",
						"description": "Set to true to wait for all previously requested tools in this turn to complete before starting. Set to false (or omit) to run in parallel. Use true when this tool depends on the output of previous tools.",
					},
				},
				"required": []any{"text"},
			},
		},
	}
}

type captureCodeAssistTransport struct {
	t               *testing.T
	streamRequest   *normalizedOutboundRequest
	streamRequests  []normalizedOutboundRequest
	streamResponses []string
}

func (t *captureCodeAssistTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.t == nil {
		return nil, io.ErrUnexpectedEOF
	}
	requireBearer(t.t, req)
	switch req.URL.Path {
	case "/v1internal:loadCodeAssist":
		return jsonResponse(http.StatusOK, loadCodeAssistResponse{CloudaiCompanionProject: "fixture-project"}), nil
	case "/v1internal:streamGenerateContent":
		if req.URL.RawQuery != "alt=sse" {
			t.t.Fatalf("stream query = %q", req.URL.RawQuery)
		}
		captured, err := normalizeOutbound(req)
		if err != nil {
			t.t.Fatalf("normalize outbound request: %v", err)
		}
		t.streamRequest = &captured
		t.streamRequests = append(t.streamRequests, captured)
		body := `data: {"response":{"responseId":"stream-fixture","modelVersion":"gemini-3-flash-preview","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"ACP_OK Notes"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}}` + "\n\n"
		if idx := len(t.streamRequests) - 1; idx >= 0 && idx < len(t.streamResponses) {
			body = t.streamResponses[idx]
		}
		return textResponse(http.StatusOK, "text/event-stream", body), nil
	default:
		t.t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, io.ErrUnexpectedEOF
	}
}

type fakeToolDispatcher struct {
	calls []compat.ToolCall
}

func (d *fakeToolDispatcher) DispatchTool(_ context.Context, call compat.ToolCall) (compat.Message, error) {
	d.calls = append(d.calls, call)
	return compat.Message{
		Role:       compat.MessageRoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content: []compat.ContentPart{{
			Type: compat.ContentPartText,
			Text: `{"output":"fixture_echo:mcp-ok"}`,
		}},
	}, nil
}

type normalizedOutboundRequest struct {
	Method        string         `json:"method"`
	Host          string         `json:"host"`
	Path          string         `json:"path"`
	RawQuery      string         `json:"raw_query,omitempty"`
	Authorization string         `json:"authorization"`
	ContentType   string         `json:"content_type"`
	Accept        string         `json:"accept"`
	UserAgent     string         `json:"user_agent"`
	APIClient     string         `json:"x_goog_api_client"`
	Body          map[string]any `json:"body"`
}

func normalizeOutbound(req *http.Request) (normalizedOutboundRequest, error) {
	defer req.Body.Close()
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return normalizedOutboundRequest{}, err
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return normalizedOutboundRequest{}, err
	}
	if _, ok := body["user_prompt_id"]; ok {
		body["user_prompt_id"] = "<uuid>"
	}
	if nested, ok := body["request"].(map[string]any); ok {
		if _, exists := nested["session_id"]; exists {
			nested["session_id"] = "<uuid>"
		}
	}
	auth := req.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		auth = "Bearer <redacted>"
	}
	return normalizedOutboundRequest{
		Method:        req.Method,
		Host:          req.URL.Host,
		Path:          req.URL.Path,
		RawQuery:      req.URL.RawQuery,
		Authorization: auth,
		ContentType:   req.Header.Get("Content-Type"),
		Accept:        req.Header.Get("Accept"),
		UserAgent:     req.Header.Get("User-Agent"),
		APIClient:     req.Header.Get("x-goog-api-client"),
		Body:          body,
	}, nil
}

func jsonResponse(status int, body any) *http.Response {
	data, _ := json.Marshal(body)
	return textResponse(status, "application/json", string(data))
}

func textResponse(status int, contentType string, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func writeAuthFile(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth_creds.json")
	raw, err := json.Marshal(map[string]any{
		"access_token":  "token-1",
		"refresh_token": "refresh-1",
		"expiry_date":   expiresAt.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireBearer(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
		t.Fatalf("authorization = %q", got)
	}
	if got := r.Header.Get("x-goog-api-client"); got == "" {
		t.Fatal("missing x-goog-api-client")
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
		t.Fatal(err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
