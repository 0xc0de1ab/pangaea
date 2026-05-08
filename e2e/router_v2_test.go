package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/nodeagent"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	v2router "github.com/0xc0de1ab/pangaea/internal/router"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func TestE2E_V2RouterShimDataPlanePublicDialects(t *testing.T) {
	tokenKey := []byte("test-v2-stream-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2E2EPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)

	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{
		Engine:     engine,
		DataBroker: dataBroker,
	}))
	defer server.Close()

	registration := routerV2E2ERegistration(time.Now())
	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible, Registration: registration})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	shimDone := make(chan error, 1)
	go func() {
		shimDone <- providershim.RunSimulatorShim(ctx, providershim.SimulatorShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Simulator:         sim,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-shimDone:
			if err != nil {
				t.Fatalf("shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, registration.Identity.ProviderInstanceID)
	node := waitForV2Node(t, client, server.URL, registration.Identity.NodeID)
	if node.HostName != "providersim-host" {
		t.Fatalf("unexpected node snapshot: %#v", node)
	}
	geminiModels := waitForV2GeminiModels(t, client, server.URL)
	if len(geminiModels) != 1 || geminiModels[0].Name != "models/gemini-default" {
		t.Fatalf("unexpected Gemini models response: %#v", geminiModels)
	}
	anthropicModels := waitForV2AnthropicModels(t, client, server.URL)
	if len(anthropicModels) != 1 || anthropicModels[0].ID != "claude-default" || anthropicModels[0].Type != "model" {
		t.Fatalf("unexpected Anthropic models response: %#v", anthropicModels)
	}
	sim.SetAuthStatus(provider.AuthRefreshSoon)
	waitForV2ProviderAuth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.AuthRefreshSoon)
	refresh := requestV2AuthRefresh(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if !refresh.OK || refresh.Auth.Status != provider.AuthHealthy {
		t.Fatalf("unexpected auth refresh result: %#v", refresh)
	}
	waitForV2ProviderAuth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.AuthHealthy)
	setV2QuotaLimit(t, client, server.URL, quota.Scope{APIKeyID: "req_e2e_v2_data", Model: "providersim-default"}, quota.Limit{MaxTokens: 1000, MaxRequests: 10})
	response := waitForV2OpenAIChat(t, client, server.URL)
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "providersim: e2e hello" {
		t.Fatalf("unexpected chat response: %#v", response)
	}
	if response.Model != "gpt-5-sim" {
		t.Fatalf("expected canonical model gpt-5-sim, got %q", response.Model)
	}
	if response.Usage == nil || response.Usage.TotalTokens == 0 {
		t.Fatalf("expected usage in response, got %#v", response.Usage)
	}
	trace := waitForV2Trace(t, client, server.URL, "req_e2e_v2_data")
	if trace.Status != "completed" || trace.Provider == nil || trace.Provider.HostName != "providersim-host" {
		t.Fatalf("unexpected request trace: %#v", trace)
	}
	snapshot := getV2QuotaSnapshot(t, client, server.URL, quota.Scope{APIKeyID: "req_e2e_v2_data", Model: "providersim-default"})
	if snapshot.Committed.Tokens == 0 || snapshot.Limit.MaxTokens != 1000 {
		t.Fatalf("unexpected quota snapshot: %#v", snapshot)
	}
	streamBody := waitForV2OpenAIChatStream(t, client, server.URL)
	if !strings.Contains(streamBody, "providersim: e2e stream") || !strings.Contains(streamBody, "data: [DONE]") {
		t.Fatalf("unexpected OpenAI stream body: %s", streamBody)
	}
	anthropic := waitForV2AnthropicMessages(t, client, server.URL)
	if len(anthropic.Content) != 1 || anthropic.Content[0].Text != "providersim: e2e anthropic" {
		t.Fatalf("unexpected Anthropic response: %#v", anthropic)
	}
	anthropicStreamBody := waitForV2AnthropicMessagesStream(t, client, server.URL)
	if !strings.Contains(anthropicStreamBody, "event: content_block_delta") || !strings.Contains(anthropicStreamBody, "providersim: e2e anthropic stream") || !strings.Contains(anthropicStreamBody, "event: message_stop") {
		t.Fatalf("unexpected Anthropic stream body: %s", anthropicStreamBody)
	}
	gemini := waitForV2GeminiGenerateContent(t, client, server.URL)
	if len(gemini.Candidates) != 1 || gemini.Candidates[0].Content.Parts[0].Text != "providersim: e2e gemini" {
		t.Fatalf("unexpected Gemini response: %#v", gemini)
	}
	geminiStreamBody := waitForV2GeminiGenerateContentStream(t, client, server.URL)
	if !strings.Contains(geminiStreamBody, "data:") || !strings.Contains(geminiStreamBody, "providersim: e2e gemini stream") {
		t.Fatalf("unexpected Gemini stream body: %s", geminiStreamBody)
	}
	usage := waitForV2ProviderUsage(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if usage.HostName != "providersim-host" || usage.Account.Display != "providersim@example.test" {
		t.Fatalf("usage lost provider host/account dimensions: %#v", usage)
	}
}

func TestE2E_V2RouterDataPlaneFallbackWhenSelectedSessionMissing(t *testing.T) {
	tokenKey := []byte("test-v2-stream-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2FallbackE2EPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	registry := provider.NewRegistry()
	missing := routerV2E2ERegistration(time.Now())
	missing.Identity.ProviderID = "providersim-missing"
	missing.Identity.ProviderInstanceID = "providersim-missing-0001"
	missing.Identity.HostName = "missing-host"
	missing.Identity.Account = provider.Account{ID: "acct-missing", Display: "missing@example.test"}
	missing.Auth.Account = missing.Identity.Account
	if err := registry.Upsert(missing); err != nil {
		t.Fatalf("upsert missing provider: %v", err)
	}
	engine, err := v2router.NewEngine(policy, registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)

	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{
		Engine:     engine,
		DataBroker: dataBroker,
	}))
	defer server.Close()

	available := routerV2E2ERegistration(time.Now())
	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible, Registration: available})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	shimDone := make(chan error, 1)
	go func() {
		shimDone <- providershim.RunSimulatorShim(ctx, providershim.SimulatorShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Simulator:         sim,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-shimDone:
			if err != nil {
				t.Fatalf("shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, available.Identity.ProviderInstanceID)
	response := waitForV2OpenAIChat(t, client, server.URL)
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "providersim: e2e hello" {
		t.Fatalf("unexpected fallback response: %#v", response)
	}
	trace := waitForV2Trace(t, client, server.URL, "req_e2e_v2_data")
	if trace.Provider == nil || trace.Provider.ProviderInstanceID != available.Identity.ProviderInstanceID {
		t.Fatalf("trace did not record fallback provider: %#v", trace)
	}
	if len(trace.Decision.Rejections) == 0 || !strings.Contains(trace.Decision.Rejections[len(trace.Decision.Rejections)-1].Reason, "data session disconnected") {
		t.Fatalf("trace did not record missing data session rejection: %#v", trace.Decision.Rejections)
	}
}

func TestE2E_V2NodeAgentProviderInventory(t *testing.T) {
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2E2EPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- nodeagent.RunControlClient(ctx, nodeagent.ControlClientOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			NodeID:            "node-a1",
			HostName:          "snowbox",
			HeartbeatInterval: 20 * time.Millisecond,
			Runtime:           control.RuntimeInfo{Kind: "docker", Version: "26.1.0"},
			ProviderSpecs: []nodeagent.ProviderSpec{{
				ID:          "codex-samtest",
				InstanceID:  "codex-samtest-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-codex:test",
				AccountHint: "samtest4u@gmail.com",
				Service:     provider.ServiceCodex,
				Shim:        nodeagent.ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAuthRefreshOneshot}},
			}, {
				ID:          "gemini-nullcode",
				InstanceID:  "gemini-nullcode-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-gemini:test",
				AccountHint: "nullcode@gmail.com",
				Service:     provider.ServiceGemini,
				Shim:        nodeagent.ShimSpec{Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent, provider.CapabilityAuthRefreshOneshot}},
			}},
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("node agent exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("node agent did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, "codex-samtest-0001")
	waitForV2Provider(t, client, server.URL, "gemini-nullcode-0001")
	node := waitForV2Node(t, client, server.URL, "node-a1")
	if node.HostName != "snowbox" || node.Runtime.Kind != "docker" {
		t.Fatalf("unexpected node inventory: %#v", node)
	}
	containers := waitForV2Containers(t, client, server.URL, 2)
	seen := map[string]bool{}
	for _, container := range containers {
		if container.HostName != "snowbox" {
			t.Fatalf("container inventory lost host name: %#v", containers)
		}
		seen[container.ProviderInstanceID] = true
	}
	if !seen["codex-samtest-0001"] || !seen["gemini-nullcode-0001"] {
		t.Fatalf("expected both provider containers, got %#v", containers)
	}
}

func TestE2E_V2APICompatibleProviderShimOpenAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "deepseek-chat", "object": "model"},
				},
			})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if request.Model != "deepseek-chat" || len(request.Messages) != 1 || request.Messages[0].Content != "api provider hello" {
			if request.Model != "deepseek-chat" || len(request.Messages) != 1 || request.Messages[0].Content != "api provider stream" {
				t.Fatalf("unexpected upstream request: %#v", request)
			}
		}
		if request.Stream {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-api-provider-stream\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-api-provider-stream\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"api-compatible: \"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-api-provider-stream\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"stream\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":13,\"completion_tokens\":5,\"total_tokens\":18}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-api-provider",
			Object: "chat.completion",
			Model:  "deepseek-chat",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "api-compatible: ok"},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
		})
	}))
	defer upstream.Close()

	tokenKey := []byte("test-v2-api-compatible-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2APICompatiblePolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, DataBroker: dataBroker}))
	defer server.Close()

	apiProvider, err := apiprovider.New(apiprovider.Options{
		Registration: apiCompatibleE2ERegistration(time.Now()),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_test_e2e",
	})
	if err != nil {
		t.Fatalf("new api provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("api provider registration: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Provider:          apiProvider,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("api-compatible shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("api-compatible shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, registration.Identity.ProviderInstanceID)
	response := waitForV2OpenAIChatModel(t, client, server.URL, "deepseek-default", "api provider hello", "req_e2e_v2_api_provider")
	if response.Choices[0].Message.Content != "api-compatible: ok" || response.Usage == nil || response.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected api-compatible response: %#v", response)
	}
	streamBody := waitForV2OpenAIChatStreamModel(t, client, server.URL, "deepseek-default", "api provider stream", "req_e2e_v2_api_provider_stream")
	if !strings.Contains(streamBody, "api-compatible: ") || !strings.Contains(streamBody, "stream") || !strings.Contains(streamBody, "data: [DONE]") {
		t.Fatalf("unexpected api-compatible stream body: %s", streamBody)
	}
	usage := waitForV2ProviderUsage(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if usage.HostName != "api-host" || usage.Usage.TotalTokens != 36 || usage.Usage.Requests != 2 {
		t.Fatalf("unexpected api-compatible usage: %#v", usage)
	}
	trace := waitForV2Trace(t, client, server.URL, "req_e2e_v2_api_provider")
	if trace.Provider == nil || trace.Provider.ProviderInstanceID != registration.Identity.ProviderInstanceID {
		t.Fatalf("unexpected api-compatible trace: %#v", trace)
	}
}

func TestE2E_V2APICompatibleProviderShimAnthropicGLMAndMiniMAX(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var request compat.AnthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		var text string
		switch request.Model {
		case "glm-4.6":
			text = "glm-compatible: ok"
		case "minimax-m1":
			text = "minimax-compatible: ok"
		default:
			t.Fatalf("unexpected upstream model: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(compat.AnthropicMessagesResponse{
			ID:         "msg-api-compatible-" + request.Model,
			Type:       "message",
			Role:       "assistant",
			Model:      request.Model,
			Content:    []compat.AnthropicContentBlock{{Type: "text", Text: text}},
			StopReason: "end_turn",
			Usage:      compat.AnthropicUsage{InputTokens: 9, OutputTokens: 6},
		})
	}))
	defer upstream.Close()

	tokenKey := []byte("test-v2-api-compatible-anthropic-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2APICompatibleAnthropicPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, DataBroker: dataBroker}))
	defer server.Close()

	glmProvider, err := apiprovider.New(apiprovider.Options{
		Registration: apiCompatibleAnthropicE2ERegistration(time.Now(), provider.ServiceGLM, "glm-api", "glm-api-0001", "api-host", "glm-api@example.test", "glm-4.6", "glm-default"),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectAnthropic,
		APIKey:       "glm_test_e2e",
	})
	if err != nil {
		t.Fatalf("new GLM api provider: %v", err)
	}
	minimaxProvider, err := apiprovider.New(apiprovider.Options{
		Registration: apiCompatibleAnthropicE2ERegistration(time.Now(), provider.ServiceMiniMAX, "minimax-api", "minimax-api-0001", "api-host", "minimax-api@example.test", "minimax-m1", "minimax-default"),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectAnthropic,
		APIKey:       "minimax_test_e2e",
	})
	if err != nil {
		t.Fatalf("new MiniMAX api provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	for _, apiProvider := range []*apiprovider.Provider{glmProvider, minimaxProvider} {
		apiProvider := apiProvider
		go func() {
			done <- providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
				ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
				HeartbeatInterval: 20 * time.Millisecond,
				TokenKey:          tokenKey,
				Provider:          apiProvider,
			})
		}()
	}
	defer func() {
		cancel()
		for i := 0; i < 2; i++ {
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("api-compatible anthropic shim exited with error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("api-compatible anthropic shim did not stop")
			}
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, "glm-api-0001")
	waitForV2Provider(t, client, server.URL, "minimax-api-0001")
	glmResponse := waitForV2AnthropicMessagesModel(t, client, server.URL, "glm-default", "glm provider hello", "req_e2e_v2_glm_api")
	if len(glmResponse.Content) != 1 || glmResponse.Content[0].Text != "glm-compatible: ok" || glmResponse.Usage.InputTokens+glmResponse.Usage.OutputTokens != 15 {
		t.Fatalf("unexpected GLM response: %#v", glmResponse)
	}
	minimaxResponse := waitForV2AnthropicMessagesModel(t, client, server.URL, "minimax-default", "minimax provider hello", "req_e2e_v2_minimax_api")
	if len(minimaxResponse.Content) != 1 || minimaxResponse.Content[0].Text != "minimax-compatible: ok" || minimaxResponse.Usage.InputTokens+minimaxResponse.Usage.OutputTokens != 15 {
		t.Fatalf("unexpected MiniMAX response: %#v", minimaxResponse)
	}
	glmUsage := waitForV2ProviderUsage(t, client, server.URL, "glm-api-0001")
	if glmUsage.HostName != "api-host" || glmUsage.Account.Display != "glm-api@example.test" || glmUsage.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected GLM usage: %#v", glmUsage)
	}
	minimaxUsage := waitForV2ProviderUsage(t, client, server.URL, "minimax-api-0001")
	if minimaxUsage.HostName != "api-host" || minimaxUsage.Account.Display != "minimax-api@example.test" || minimaxUsage.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected MiniMAX usage: %#v", minimaxUsage)
	}
}

func TestE2E_V2SidecarProviderShimAntigravityAndCopilot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "antigravity-default", "object": "model"},
					{"id": "github-copilot-default", "object": "model"},
				},
			})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		var text string
		switch request.Model {
		case "antigravity-default":
			text = "antigravity-sidecar: ok"
		case "github-copilot-default":
			text = "copilot-sidecar: ok"
		default:
			t.Fatalf("unexpected sidecar upstream request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-sidecar-" + request.Model,
			Object: "chat.completion",
			Model:  request.Model,
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: text},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: 13},
		})
	}))
	defer upstream.Close()

	tokenKey := []byte("test-v2-sidecar-compatible-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2SidecarPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, DataBroker: dataBroker}))
	defer server.Close()

	antigravityProvider, err := apiprovider.New(apiprovider.Options{
		Registration: sidecarE2ERegistration(time.Now(), provider.ServiceAntigravity, "antigravity-sidecar", "antigravity-sidecar-0001", "sidecar-host", "antigravity@example.test", "antigravity-default", "antigravity-default"),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new Antigravity sidecar provider: %v", err)
	}
	copilotProvider, err := apiprovider.New(apiprovider.Options{
		Registration: sidecarE2ERegistration(time.Now(), provider.ServiceGitHubCopilot, "github-copilot-sidecar", "github-copilot-sidecar-0001", "sidecar-host", "copilot@example.test", "github-copilot-default", "copilot-default"),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new Copilot sidecar provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	for _, apiProvider := range []*apiprovider.Provider{antigravityProvider, copilotProvider} {
		apiProvider := apiProvider
		go func() {
			done <- providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
				ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
				HeartbeatInterval: 20 * time.Millisecond,
				TokenKey:          tokenKey,
				Provider:          apiProvider,
			})
		}()
	}
	defer func() {
		cancel()
		for i := 0; i < 2; i++ {
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("sidecar shim exited with error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("sidecar shim did not stop")
			}
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, "antigravity-sidecar-0001")
	waitForV2Provider(t, client, server.URL, "github-copilot-sidecar-0001")
	antigravityResponse := waitForV2OpenAIChatModel(t, client, server.URL, "antigravity-default", "sidecar provider hello", "req_e2e_v2_antigravity_sidecar")
	if antigravityResponse.Choices[0].Message.Content != "antigravity-sidecar: ok" || antigravityResponse.Usage == nil || antigravityResponse.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected Antigravity sidecar response: %#v", antigravityResponse)
	}
	copilotResponse := waitForV2OpenAIChatModel(t, client, server.URL, "copilot-default", "sidecar provider hello", "req_e2e_v2_copilot_sidecar")
	if copilotResponse.Choices[0].Message.Content != "copilot-sidecar: ok" || copilotResponse.Usage == nil || copilotResponse.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected Copilot sidecar response: %#v", copilotResponse)
	}
	antigravityUsage := waitForV2ProviderUsage(t, client, server.URL, "antigravity-sidecar-0001")
	if antigravityUsage.HostName != "sidecar-host" || antigravityUsage.Account.Display != "antigravity@example.test" || antigravityUsage.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected Antigravity usage: %#v", antigravityUsage)
	}
	copilotUsage := waitForV2ProviderUsage(t, client, server.URL, "github-copilot-sidecar-0001")
	if copilotUsage.HostName != "sidecar-host" || copilotUsage.Account.Display != "copilot@example.test" || copilotUsage.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected Copilot usage: %#v", copilotUsage)
	}
}

func TestE2E_V2APICompatibleProviderShimPropagatesUpstreamRateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("retry-after", "11")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream rate limit hit","code":"rate_limit_exceeded"}}`))
	}))
	defer upstream.Close()

	tokenKey := []byte("test-v2-api-compatible-rate-limit-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2APICompatiblePolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, DataBroker: dataBroker}))
	defer server.Close()

	apiProvider, err := apiprovider.New(apiprovider.Options{
		Registration: apiCompatibleE2ERegistration(time.Now()),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_test_e2e",
	})
	if err != nil {
		t.Fatalf("new api provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("api provider registration: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Provider:          apiProvider,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("api-compatible shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("api-compatible shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, registration.Identity.ProviderInstanceID)
	body, err := json.Marshal(compat.OpenAIChatRequest{
		Model:    "deepseek-default",
		Messages: []compat.OpenAIChatMessage{{Role: "user", Content: "rate limit please"}},
	})
	if err != nil {
		t.Fatalf("marshal OpenAI chat request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-request-id", "req_e2e_v2_api_provider_rate_limit")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("rate-limit chat request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read rate-limit response: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", resp.StatusCode, string(data))
	}
	if resp.Header.Get("retry-after") != "11" {
		t.Fatalf("retry-after header = %q, want 11", resp.Header.Get("retry-after"))
	}
	var out struct {
		Code           string `json:"code"`
		UpstreamCode   string `json:"upstream_code"`
		UpstreamStatus int    `json:"upstream_status"`
		RetryAfter     string `json:"retry_after"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode rate-limit response: %v body=%s", err, string(data))
	}
	if out.Code != "upstream_error" || out.UpstreamCode != "rate_limit_exceeded" || out.UpstreamStatus != http.StatusTooManyRequests || out.RetryAfter != "11" {
		t.Fatalf("unexpected rate-limit payload: %#v body=%s", out, string(data))
	}
	time.Sleep(80 * time.Millisecond)
	degraded := waitForV2ProviderHealth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.HealthDegraded)
	if degraded.Health.Reason != "upstream rate limited" {
		t.Fatalf("unexpected degraded provider health: %#v", degraded.Health)
	}
}

func TestE2E_V2CLIContainerProviderShimAuthRefreshAndRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if request.Model != "gpt-5-codex" || len(request.Messages) != 1 || request.Messages[0].Content != "cli provider hello" {
			t.Fatalf("unexpected upstream request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-cli-provider",
			Object: "chat.completion",
			Model:  "gpt-5-codex",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "cli-container: ok"},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 13, CompletionTokens: 5, TotalTokens: 18},
		})
	}))
	defer upstream.Close()

	tokenKey := []byte("test-v2-cli-container-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2CLIContainerPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, DataBroker: dataBroker}))
	defer server.Close()

	authPath := t.TempDir() + "/auth.txt"
	if err := os.WriteFile(authPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale auth: %v", err)
	}
	authRefresher, err := providershim.NewCommandAuthRefresher(providershim.CommandAuthRefresherOptions{
		Command:  []string{"sh", "-c", `printf fresh > "$AUTH_PATH"`},
		Env:      map[string]string{"AUTH_PATH": authPath},
		Timeout:  time.Second,
		AuthPath: authPath,
		Format:   cliContainerE2EFormat{},
	})
	if err != nil {
		t.Fatalf("new auth refresher: %v", err)
	}
	apiProvider, err := apiprovider.New(apiprovider.Options{
		Registration: cliContainerE2ERegistration(time.Now()),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new cli-container provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("cli-container registration: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Provider:          apiProvider,
			AuthRefresher:     authRefresher,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("cli-container shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("cli-container shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2ProviderAuth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.AuthRefreshSoon)
	refresh := requestV2AuthRefresh(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if !refresh.OK || refresh.Auth.Status != provider.AuthHealthy || refresh.Auth.Account.Display != "codex-cli@example.test" {
		t.Fatalf("unexpected cli-container refresh result: %#v", refresh)
	}
	waitForV2ProviderAuth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.AuthHealthy)
	response := waitForV2OpenAIChatModel(t, client, server.URL, "codex-default", "cli provider hello", "req_e2e_v2_cli_provider")
	if response.Choices[0].Message.Content != "cli-container: ok" || response.Usage == nil || response.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected cli-container response: %#v", response)
	}
	usage := waitForV2ProviderUsage(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if usage.HostName != "cli-host" || usage.Account.Display != "codex-cli@example.test" {
		t.Fatalf("unexpected cli-container usage: %#v", usage)
	}
}

func waitForV2Provider(t *testing.T, client *http.Client, baseURL string, providerInstanceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/providers")
		if err == nil {
			var out struct {
				Providers []provider.Registration `json:"providers"`
			}
			if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr == nil {
				_ = resp.Body.Close()
				for _, registration := range out.Providers {
					if registration.Identity.ProviderInstanceID == providerInstanceID {
						return
					}
				}
			} else {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider %q did not register", providerInstanceID)
}

func waitForV2Containers(t *testing.T, client *http.Client, baseURL string, minCount int) []v2router.ContainerSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/containers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Containers []v2router.ContainerSnapshot `json:"containers"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode containers response: %v body=%s", err, string(data))
		}
		if len(out.Containers) >= minCount {
			return out.Containers
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("container inventory did not reach %d entries: %v", minCount, lastErr)
	return nil
}

func waitForV2ProviderAuth(t *testing.T, client *http.Client, baseURL string, providerInstanceID string, status provider.AuthStatus) provider.Registration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/providers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Providers []provider.Registration `json:"providers"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode providers response: %v body=%s", err, string(data))
		}
		for _, registration := range out.Providers {
			if registration.Identity.ProviderInstanceID == providerInstanceID && registration.Auth.Status == status {
				return registration
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider %q auth status did not become %q: %v", providerInstanceID, status, lastErr)
	return provider.Registration{}
}

func waitForV2ProviderHealth(t *testing.T, client *http.Client, baseURL string, providerInstanceID string, status provider.HealthStatus) provider.Registration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/providers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Providers []provider.Registration `json:"providers"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode providers response: %v body=%s", err, string(data))
		}
		for _, registration := range out.Providers {
			if registration.Identity.ProviderInstanceID == providerInstanceID && registration.Health.Status == status {
				return registration
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider %q health status did not become %q: %v", providerInstanceID, status, lastErr)
	return provider.Registration{}
}

func requestV2AuthRefresh(t *testing.T, client *http.Client, baseURL string, providerInstanceID string) control.AuthRefreshResult {
	t.Helper()
	body := bytes.NewBufferString(`{"refresh_id":"refresh_e2e","reason":"e2e","timeout_seconds":2,"confirm":true}`)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/router/v1/providers/"+providerInstanceID+"/auth/refresh", body)
	if err != nil {
		t.Fatalf("new auth refresh request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("auth refresh request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read auth refresh response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth refresh status=%d body=%s", resp.StatusCode, string(data))
	}
	var result control.AuthRefreshResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode auth refresh response: %v body=%s", err, string(data))
	}
	return result
}

func waitForV2Node(t *testing.T, client *http.Client, baseURL string, nodeID string) v2router.NodeSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/nodes")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Nodes []v2router.NodeSnapshot `json:"nodes"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode nodes response: %v body=%s", err, string(data))
		}
		for _, node := range out.Nodes {
			if node.NodeID == nodeID && !node.LastInventoryAt.IsZero() {
				return node
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("node %q did not report inventory: %v", nodeID, lastErr)
	return v2router.NodeSnapshot{}
}

func setV2QuotaLimit(t *testing.T, client *http.Client, baseURL string, scope quota.Scope, limit quota.Limit) {
	t.Helper()
	body, err := json.Marshal(struct {
		Scope quota.Scope `json:"scope"`
		Limit quota.Limit `json:"limit"`
	}{Scope: scope, Limit: limit})
	if err != nil {
		t.Fatalf("marshal quota limit: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, baseURL+"/router/v1/quotas/limits", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new quota request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("set quota limit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("set quota limit status=%d body=%s", resp.StatusCode, string(data))
	}
}

func getV2QuotaSnapshot(t *testing.T, client *http.Client, baseURL string, scope quota.Scope) quota.SnapshotRecord {
	t.Helper()
	body, err := json.Marshal(scope)
	if err != nil {
		t.Fatalf("marshal quota scope: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/router/v1/quotas/snapshot", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new quota snapshot request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get quota snapshot: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read quota snapshot: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quota snapshot status=%d body=%s", resp.StatusCode, string(data))
	}
	var snapshot quota.SnapshotRecord
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("decode quota snapshot: %v body=%s", err, string(data))
	}
	return snapshot
}

func waitForV2OpenAIChat(t *testing.T, client *http.Client, baseURL string) compat.OpenAIChatResponse {
	t.Helper()
	return waitForV2OpenAIChatModel(t, client, baseURL, "providersim-default", "e2e hello", "req_e2e_v2_data")
}

func waitForV2OpenAIChatModel(t *testing.T, client *http.Client, baseURL string, model string, content string, requestID string) compat.OpenAIChatResponse {
	t.Helper()
	body, err := json.Marshal(compat.OpenAIChatRequest{
		Model:    model,
		Messages: []compat.OpenAIChatMessage{{Role: "user", Content: content}},
	})
	if err != nil {
		t.Fatalf("marshal OpenAI chat request: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", requestID)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var response compat.OpenAIChatResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode chat response: %v body=%s", err, string(data))
			}
			return response
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("chat completion did not succeed: %v", lastErr)
	return compat.OpenAIChatResponse{}
}

func waitForV2OpenAIChatStream(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	return waitForV2OpenAIChatStreamModel(t, client, baseURL, "providersim-default", "e2e stream", "req_e2e_v2_stream")
}

func waitForV2OpenAIChatStreamModel(t *testing.T, client *http.Client, baseURL string, model string, content string, requestID string) string {
	t.Helper()
	body, err := json.Marshal(compat.OpenAIChatRequest{
		Model:    model,
		Stream:   true,
		Messages: []compat.OpenAIChatMessage{{Role: "user", Content: content}},
	})
	if err != nil {
		t.Fatalf("marshal OpenAI chat stream request: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", requestID)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return string(data)
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("OpenAI stream did not succeed: %v", lastErr)
	return ""
}

func waitForV2AnthropicMessages(t *testing.T, client *http.Client, baseURL string) compat.AnthropicMessagesResponse {
	t.Helper()
	return waitForV2AnthropicMessagesModel(t, client, baseURL, "claude-default", "e2e anthropic", "req_e2e_v2_anthropic")
}

func waitForV2AnthropicMessagesModel(t *testing.T, client *http.Client, baseURL string, model string, content string, requestID string) compat.AnthropicMessagesResponse {
	t.Helper()
	rawContent, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal Anthropic content: %v", err)
	}
	body, err := json.Marshal(compat.AnthropicMessagesRequest{
		Model:     model,
		MaxTokens: 64,
		Messages: []compat.AnthropicMessage{{
			Role:    "user",
			Content: rawContent,
		}},
	})
	if err != nil {
		t.Fatalf("marshal Anthropic message request: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", requestID)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var response compat.AnthropicMessagesResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode Anthropic response: %v body=%s", err, string(data))
			}
			return response
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Anthropic message did not succeed: %v", lastErr)
	return compat.AnthropicMessagesResponse{}
}

func waitForV2AnthropicMessagesStream(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	body := []byte(`{"model":"claude-default","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"e2e anthropic stream"}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_anthropic_stream")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK && strings.Contains(resp.Header.Get("content-type"), "text/event-stream") {
			return string(data)
		}
		lastErr = fmt.Errorf("status=%d content-type=%s body=%s", resp.StatusCode, resp.Header.Get("content-type"), string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Anthropic message stream did not succeed: %v", lastErr)
	return ""
}

func waitForV2GeminiGenerateContent(t *testing.T, client *http.Client, baseURL string) compat.GeminiGenerateContentResponse {
	t.Helper()
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"e2e gemini"}]}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1beta/models/gemini-default:generateContent", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_gemini")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var response compat.GeminiGenerateContentResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode Gemini response: %v body=%s", err, string(data))
			}
			return response
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Gemini generateContent did not succeed: %v", lastErr)
	return compat.GeminiGenerateContentResponse{}
}

func waitForV2GeminiGenerateContentStream(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"e2e gemini stream"}]}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1beta/models/gemini-default:streamGenerateContent?alt=sse", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_gemini_stream")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK && strings.Contains(resp.Header.Get("content-type"), "text/event-stream") {
			return string(data)
		}
		lastErr = fmt.Errorf("status=%d content-type=%s body=%s", resp.StatusCode, resp.Header.Get("content-type"), string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Gemini streamGenerateContent did not succeed: %v", lastErr)
	return ""
}

type e2eGeminiModel struct {
	Name                       string   `json:"name"`
	Version                    string   `json:"version,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

func waitForV2GeminiModels(t *testing.T, client *http.Client, baseURL string) []e2eGeminiModel {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/v1beta/models")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var out struct {
				Models []e2eGeminiModel `json:"models"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatalf("decode Gemini models response: %v body=%s", err, string(data))
			}
			return out.Models
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Gemini models did not succeed: %v", lastErr)
	return nil
}

type e2eAnthropicModel struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func waitForV2AnthropicModels(t *testing.T, client *http.Client, baseURL string) []e2eAnthropicModel {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/models", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var out struct {
				Data []e2eAnthropicModel `json:"data"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatalf("decode Anthropic models response: %v body=%s", err, string(data))
			}
			return out.Data
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Anthropic models did not succeed: %v", lastErr)
	return nil
}

func waitForV2ProviderUsage(t *testing.T, client *http.Client, baseURL string, providerInstanceID string) v2router.ProviderUsageSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/usage/providers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Usage []v2router.ProviderUsageSnapshot `json:"usage"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode usage response: %v body=%s", err, string(data))
		}
		for _, usage := range out.Usage {
			if usage.ProviderInstanceID == providerInstanceID && usage.Usage.TotalTokens > 0 {
				return usage
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider usage did not update: %v", lastErr)
	return v2router.ProviderUsageSnapshot{}
}

func waitForV2Trace(t *testing.T, client *http.Client, baseURL string, requestID string) v2router.RequestTrace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/traces/" + requestID)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var trace v2router.RequestTrace
			if err := json.Unmarshal(data, &trace); err != nil {
				t.Fatalf("decode trace response: %v body=%s", err, string(data))
			}
			return trace
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("request trace did not update: %v", lastErr)
	return v2router.RequestTrace{}
}

func routerV2E2ERegistration(now time.Time) provider.Registration {
	account := provider.Account{ID: "acct-providersim", Display: "providersim@example.test"}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "providersim-multi",
			ProviderInstanceID: "providersim-multi-0001",
			NodeID:             "providersim-node",
			HostName:           "providersim-host",
			Service:            provider.ServiceOpenAI,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
			provider.CapabilityAuthRefreshOneshot,
		},
		Models: []provider.Model{
			{ID: "gpt-5-sim", Aliases: []string{"providersim-default"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
			{ID: "claude-native-sim", Aliases: []string{"claude-default"}, Capabilities: []provider.Capability{provider.CapabilityAnthropicMessages}},
			{ID: "gemini-native-sim", Aliases: []string{"gemini-default"}, Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent}},
		},
		Health: provider.Health{
			Status:    provider.HealthReady,
			CheckedAt: now,
		},
		Auth: provider.AuthState{
			Status:        provider.AuthHealthy,
			Account:       account,
			ExpiresAt:     now.Add(time.Hour),
			Refreshable:   true,
			LastRefreshAt: now,
		},
		Limits: provider.LimitState{
			MaxConcurrency: 8,
			QueueDepth:     0,
		},
		RegisteredAt: now,
	}
}

func apiCompatibleE2ERegistration(now time.Time) provider.Registration {
	account := provider.Account{ID: "acct-deepseek", Display: "deepseek-api@example.test"}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "deepseek-api",
			ProviderInstanceID: "deepseek-api-0001",
			NodeID:             "api-node",
			HostName:           "api-host",
			Service:            provider.ServiceDeepSeek,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
		},
		Models: []provider.Model{{
			ID:           "deepseek-chat",
			Aliases:      []string{"deepseek-default"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE},
		}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account},
		RegisteredAt: now,
	}
}

func apiCompatibleAnthropicE2ERegistration(now time.Time, service provider.Service, providerID string, instanceID string, hostName string, accountDisplay string, modelID string, alias string) provider.Registration {
	account := provider.Account{ID: "acct-" + providerID, Display: accountDisplay}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         providerID,
			ProviderInstanceID: instanceID,
			NodeID:             "api-node",
			HostName:           hostName,
			Service:            service,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityAnthropicMessages,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
			provider.CapabilityAuthAPIKey,
		},
		Models: []provider.Model{{
			ID:           modelID,
			Aliases:      []string{alias},
			Capabilities: []provider.Capability{provider.CapabilityAnthropicMessages, provider.CapabilityStreamSSE},
		}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account},
		RegisteredAt: now,
	}
}

func sidecarE2ERegistration(now time.Time, service provider.Service, providerID string, instanceID string, hostName string, accountDisplay string, modelID string, alias string) provider.Registration {
	account := provider.Account{ID: "acct-" + providerID, Display: accountDisplay}
	capabilities := []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityStreamSSE,
		provider.CapabilityUsageRead,
		provider.CapabilityModelsRead,
	}
	switch service {
	case provider.ServiceAntigravity:
		capabilities = append(capabilities,
			provider.CapabilityAntigravitySidecar,
			provider.CapabilityAgentToolUse,
			provider.CapabilityAgentWorkspaceRead,
			provider.CapabilityAgentWorkspaceWrite,
		)
	case provider.ServiceGitHubCopilot:
		capabilities = append(capabilities,
			provider.CapabilityCodeCompletion,
			provider.CapabilityAgentWorkspaceRead,
		)
	}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         providerID,
			ProviderInstanceID: instanceID,
			NodeID:             "sidecar-node",
			HostName:           hostName,
			Service:            service,
			Kind:               provider.KindSidecar,
			Account:            account,
		},
		Capabilities: capabilities,
		Models: []provider.Model{{
			ID:           modelID,
			Aliases:      []string{alias},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE},
		}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account, SelectedSource: "sidecar"},
		RegisteredAt: now,
	}
}

func cliContainerE2ERegistration(now time.Time) provider.Registration {
	account := provider.Account{ID: "acct-codex-cli", Display: "codex-cli@example.test"}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "codex-cli",
			ProviderInstanceID: "codex-cli-0001",
			NodeID:             "cli-node",
			HostName:           "cli-host",
			Service:            provider.ServiceCodex,
			Kind:               provider.KindCLIContainer,
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
			provider.CapabilityAuthFile,
			provider.CapabilityAuthRefreshOneshot,
		},
		Models: []provider.Model{{
			ID:           "gpt-5-codex",
			Aliases:      []string{"codex-default"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		}},
		Health: provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth: provider.AuthState{
			Status:        provider.AuthRefreshSoon,
			Account:       account,
			ExpiresAt:     now.Add(2 * time.Minute),
			Refreshable:   true,
			LastRefreshAt: now.Add(-time.Hour),
		},
		RegisteredAt: now,
	}
}

type cliContainerE2EFormat struct{}

func (cliContainerE2EFormat) Name() string         { return "cli-container-e2e-format" }
func (cliContainerE2EFormat) Strategies() []string { return []string{"default"} }
func (cliContainerE2EFormat) Parse(raw []byte) (formats.Snapshot, error) {
	status := formats.StatusExpired
	if strings.TrimSpace(string(raw)) == "fresh" {
		status = formats.StatusOK
	}
	return cliContainerE2ESnapshot{
		raw:       append([]byte(nil), raw...),
		expiresAt: time.Now().UTC().Add(time.Hour),
		status:    status,
	}, nil
}
func (cliContainerE2EFormat) Validate(_ context.Context, snapshot formats.Snapshot, _ formats.ValidateOpts) (formats.ValidationResult, error) {
	status := formats.StatusExpired
	if e2eSnapshot, ok := snapshot.(cliContainerE2ESnapshot); ok {
		status = e2eSnapshot.status
	}
	return formats.ValidationResult{Status: status, CheckedAt: time.Now().UTC()}, nil
}
func (cliContainerE2EFormat) Compare(_ string, _ formats.Snapshot, _ formats.Snapshot) int {
	return 0
}
func (cliContainerE2EFormat) Redact(_ formats.Snapshot) formats.Summary {
	return formats.Summary{}
}
func (cliContainerE2EFormat) Account(context.Context, formats.Snapshot, string) (string, error) {
	return "acct-codex-cli", nil
}
func (cliContainerE2EFormat) AccountDisplay(context.Context, formats.Snapshot, string) (string, error) {
	return "codex-cli@example.test", nil
}

type cliContainerE2ESnapshot struct {
	raw       []byte
	expiresAt time.Time
	status    formats.ValidationStatus
}

func (s cliContainerE2ESnapshot) Identity() string     { return "cli-container-e2e-snapshot" }
func (s cliContainerE2ESnapshot) ExpiresAt() time.Time { return s.expiresAt }
func (s cliContainerE2ESnapshot) Raw() []byte          { return append([]byte(nil), s.raw...) }
func (s cliContainerE2ESnapshot) Fingerprint() string  { return "cli-container-e2e-fingerprint" }

func httpURLToWS(raw string) string {
	if strings.HasPrefix(raw, "https://") {
		return "wss://" + strings.TrimPrefix(raw, "https://")
	}
	return "ws://" + strings.TrimPrefix(raw, "http://")
}

const routerV2E2EPolicy = `
version: routing-policy/v1
model_aliases:
  providersim-default:
    canonical_model: gpt-5-sim
    required_capabilities:
      - api.openai.chat
  claude-default:
    canonical_model: claude-native-sim
    required_capabilities:
      - api.anthropic.messages
  gemini-default:
    canonical_model: gemini-native-sim
    required_capabilities:
      - api.gemini.generateContent
routes:
  - id: providersim-openai
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
  - id: providersim-anthropic
    match:
      models: [claude-default]
      api_dialects: [anthropic]
    candidates:
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
  - id: providersim-gemini
    match:
      models: [gemini-default]
      api_dialects: [gemini]
    candidates:
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`

const routerV2FallbackE2EPolicy = `
version: routing-policy/v1
model_aliases:
  providersim-default:
    canonical_model: gpt-5-sim
    required_capabilities:
      - api.openai.chat
routes:
  - id: providersim-openai-fallback
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-missing
        account: missing@example.test
        host_name: missing-host
        weight: 100
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 10
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`

const routerV2APICompatiblePolicy = `
version: routing-policy/v1
model_aliases:
  deepseek-default:
    canonical_model: deepseek-chat
    required_capabilities:
      - api.openai.chat
routes:
  - id: deepseek-openai
    match:
      models: [deepseek-default]
      api_dialects: [openai]
    candidates:
      - provider: deepseek-api
        account: deepseek-api@example.test
        host_name: api-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
`

const routerV2APICompatibleAnthropicPolicy = `
version: routing-policy/v1
model_aliases:
  glm-default:
    canonical_model: glm-4.6
    required_capabilities:
      - api.anthropic.messages
  minimax-default:
    canonical_model: minimax-m1
    required_capabilities:
      - api.anthropic.messages
routes:
  - id: glm-anthropic
    match:
      models: [glm-default]
      api_dialects: [anthropic]
    candidates:
      - provider: glm-api
        account: glm-api@example.test
        host_name: api-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
  - id: minimax-anthropic
    match:
      models: [minimax-default]
      api_dialects: [anthropic]
    candidates:
      - provider: minimax-api
        account: minimax-api@example.test
        host_name: api-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
`

const routerV2SidecarPolicy = `
version: routing-policy/v1
model_aliases:
  antigravity-default:
    canonical_model: antigravity-default
    required_capabilities:
      - api.openai.chat
  copilot-default:
    canonical_model: github-copilot-default
    required_capabilities:
      - api.openai.chat
routes:
  - id: antigravity-openai
    match:
      models: [antigravity-default]
      api_dialects: [openai]
    candidates:
      - provider: antigravity-sidecar
        account: antigravity@example.test
        host_name: sidecar-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
  - id: copilot-openai
    match:
      models: [copilot-default]
      api_dialects: [openai]
    candidates:
      - provider: github-copilot-sidecar
        account: copilot@example.test
        host_name: sidecar-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
`

const routerV2CLIContainerPolicy = `
version: routing-policy/v1
model_aliases:
  codex-default:
    canonical_model: gpt-5-codex
    required_capabilities:
      - api.openai.chat
routes:
  - id: codex-cli-openai
    match:
      models: [codex-default]
      api_dialects: [openai]
    candidates:
      - provider: codex-cli
        account: codex-cli@example.test
        host_name: cli-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
`
