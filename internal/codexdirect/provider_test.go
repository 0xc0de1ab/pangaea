package codexdirect

import (
	"context"
	"encoding/base64"
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

func TestInvokeStreamPostsCodexResponsesRequest(t *testing.T) {
	authPath := writeAuthFile(t, "acc_test", time.Now().Add(time.Hour))
	var gotPath string
	var gotHeaders http.Header
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			`data: {"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`,
			`data: {"type":"response.content_part.added","part":{"type":"output_text","text":""}}`,
			`data: {"type":"response.output_text.delta","delta":"hello "}`,
			`data: {"type":"response.output_text.delta","delta":"codex"}`,
			`data: {"type":"response.output_item.done","item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello codex"}]}}`,
			`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	p := newTestProvider(t, authPath, server.URL)
	request := testRequest()
	request.MaxOutputTokens = 128
	var events []compat.Event
	response, err := p.InvokeStream(context.Background(), testRegistration(), request, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	if gotPath != "/codex/responses" {
		t.Fatalf("path = %q, want /codex/responses", gotPath)
	}
	if gotHeaders.Get("Authorization") == "" || gotHeaders.Get("chatgpt-account-id") != "acc_test" {
		t.Fatalf("missing auth headers: %#v", gotHeaders)
	}
	if gotHeaders.Get("OpenAI-Beta") != "responses=experimental" || gotHeaders.Get("Accept") != "text/event-stream" {
		t.Fatalf("unexpected stream headers: %#v", gotHeaders)
	}
	if gotBody["model"] != "gpt-5.5" || gotBody["store"] != false || gotBody["stream"] != true {
		t.Fatalf("unexpected body basics: %#v", gotBody)
	}
	if _, ok := gotBody["max_output_tokens"]; ok {
		t.Fatalf("codex backend request should not include unsupported max_output_tokens: %#v", gotBody)
	}
	if gotBody["instructions"] != "system prompt" {
		t.Fatalf("instructions = %#v", gotBody["instructions"])
	}
	if reasoning, ok := gotBody["reasoning"].(map[string]any); !ok || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", gotBody["reasoning"])
	}
	if response.Message.Content[0].Text != "hello codex" || response.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(events) != 5 || events[0].Type != compat.EventMessageStart || events[1].ContentDelta.Text != "hello " || events[2].ContentDelta.Text != "codex" || events[3].UsageDelta.TotalTokens != 5 || events[4].Type != compat.EventDone {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestInvokeBuffersCodexResponsesSSE(t *testing.T) {
	authPath := writeAuthFile(t, "acc_test", time.Now().Add(time.Hour))
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_item.added`,
			`data: {"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`,
			`event: response.content_part.added`,
			`data: {"type":"response.content_part.added","part":{"type":"output_text","text":""}}`,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"buffered"}`,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	p := newTestProvider(t, authPath, server.URL)
	request := testRequest()
	request.Messages = []compat.Message{{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "Say hello"}}}}
	response, err := p.Invoke(context.Background(), testRegistration(), request)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if response.Message.Content[0].Text != "buffered" || response.ID != "resp_2" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if gotBody["instructions"] != defaultInstructions {
		t.Fatalf("default instructions = %#v", gotBody["instructions"])
	}
}

func TestModelsFallsBackToCodexDirectDefaults(t *testing.T) {
	registration := testRegistration()
	registration.Models = nil
	p, err := New(Options{Registration: registration, BaseURL: "https://chatgpt.com/backend-api", ClientVersion: "9.9.9"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) == 0 || models[0].ID != "gpt-5.5" || models[0].MaxContextTokens == 0 {
		t.Fatalf("unexpected defaults: %#v", models)
	}
}

func TestModelsFetchesCodexBackendModels(t *testing.T) {
	authPath := writeAuthFile(t, "acc_test", time.Now().Add(time.Hour))
	var gotPath string
	var gotClientVersion string
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotClientVersion = r.URL.Query().Get("client_version")
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"slug":               "gpt-5.5",
					"display_name":       "GPT-5.5",
					"visibility":         "list",
					"context_window":     272000,
					"max_context_window": 272000,
				},
				{
					"slug":               "gpt-5.4",
					"display_name":       "gpt-5.4",
					"visibility":         "list",
					"context_window":     272000,
					"max_context_window": 1000000,
				},
				{
					"slug":               "codex-auto-review",
					"display_name":       "Codex Auto Review",
					"visibility":         "hide",
					"context_window":     272000,
					"max_context_window": 1000000,
				},
			},
		})
	}))
	defer server.Close()

	p, err := New(Options{
		Registration:  testRegistration(),
		BaseURL:       server.URL,
		AuthPath:      authPath,
		ClientVersion: "0.129.0",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if gotPath != "/codex/models" || gotClientVersion != "0.129.0" {
		t.Fatalf("unexpected model request path=%q client_version=%q", gotPath, gotClientVersion)
	}
	if gotHeaders.Get("Authorization") == "" || gotHeaders.Get("chatgpt-account-id") != "acc_test" || gotHeaders.Get("Accept") != "application/json" {
		t.Fatalf("missing model request headers: %#v", gotHeaders)
	}
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2: %#v", len(models), models)
	}
	if models[0].ID != "gpt-5.5" || !sameStrings(models[0].Aliases, []string{"codex-default", "GPT-5.5"}) {
		t.Fatalf("configured model metadata not merged: %#v", models[0])
	}
	if models[0].MaxContextTokens != 272000 {
		t.Fatalf("gpt-5.5 max context = %d, want discovered 272000", models[0].MaxContextTokens)
	}
	if models[1].ID != "gpt-5.4" || models[1].Aliases[0] != "GPT-5.4" || models[1].MaxContextTokens != 1000000 {
		t.Fatalf("discovered model missing metadata: %#v", models[1])
	}
}

func TestRecordHealthIgnoresClientRequestErrors(t *testing.T) {
	authPath := writeAuthFile(t, "acc_test", time.Now().Add(time.Hour))
	p := newTestProvider(t, authPath, "https://chatgpt.com/backend-api")

	p.recordHealth(&provider.UpstreamError{StatusCode: http.StatusBadRequest, Message: "bad request"})
	health, err := p.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Status != provider.HealthReady || health.Reason != "" {
		t.Fatalf("client request error should not degrade provider health: %#v", health)
	}
}

func newTestProvider(t *testing.T, authPath string, baseURL string) *Provider {
	t.Helper()
	p, err := New(Options{
		Registration:  testRegistration(),
		BaseURL:       baseURL,
		AuthPath:      authPath,
		Originator:    "pangaea-test",
		ClientVersion: "9.9.9",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func testRegistration() provider.Registration {
	now := time.Now().UTC()
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "codex-cli",
			ProviderInstanceID: "codex-cli",
			NodeID:             "node-1",
			HostName:           "host-1",
			Service:            provider.ServiceCodex,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityOpenAIResponses,
			provider.CapabilityStreamSSE,
			provider.CapabilityModelsRead,
			provider.CapabilityUsageRead,
		},
		Models:       []provider.Model{{ID: "gpt-5.5", Aliases: []string{"codex-default"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Refreshable: true, ExpiresAt: now.Add(time.Hour)},
		RegisteredAt: now,
	}
}

func testRequest() compat.Request {
	temp := 0.2
	return compat.Request{
		ID:              "req_1",
		Dialect:         compat.APIDialectOpenAI,
		Model:           "gpt-5.5",
		Temperature:     &temp,
		ReasoningEffort: "high",
		Messages: []compat.Message{
			{Role: compat.MessageRoleSystem, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "system prompt"}}},
			{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "Say hello"}}},
		},
	}
}

func writeAuthFile(t *testing.T, accountID string, expiresAt time.Time) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	token := fakeJWT(map[string]any{
		"exp": expiresAt.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	body := map[string]any{
		"tokens": map[string]any{
			"access_token":  token,
			"id_token":      token,
			"refresh_token": "refresh-token",
			"account_id":    "fallback-account",
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeJWT(payload map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	raw, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(raw)
	return header + "." + body + ".sig"
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
