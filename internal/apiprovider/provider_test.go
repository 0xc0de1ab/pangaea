package apiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
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

func TestProviderDefaultHTTPClientHasNoTotalTimeout(t *testing.T) {
	client, err := New(Options{
		Registration: testRegistration(),
		BaseURL:      "http://127.0.0.1:1",
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if client.client == nil {
		t.Fatalf("default HTTP client is nil")
	}
	if client.client.Timeout != 0 {
		t.Fatalf("default HTTP client timeout = %s, want no total timeout for streaming reads", client.client.Timeout)
	}
}

func TestProviderSuccessfulInvokeKeepsStaleExpiryAsRefreshSoon(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-test",
			Object: "chat.completion",
			Model:  "gpt-upstream",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "world"},
				FinishReason: "stop",
			}},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Auth.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello")); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Status != provider.AuthRefreshSoon {
		t.Fatalf("auth status = %q, want refresh_soon", auth.Status)
	}
}

func TestProviderSuccessfulInvokeTreatsAntigravityStaleExpiryAsHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-test",
			Object: "chat.completion",
			Model:  "antigravity-default",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "world"},
				FinishReason: "stop",
			}},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	registration.Auth.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	registration.Auth.Status = provider.AuthRefreshSoon
	registration.Auth.LastRefreshErr = "antigravity oauth expiry is stale in state.vscdb but may be refreshed in ls-core memory"
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello")); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Status != provider.AuthHealthy {
		t.Fatalf("auth status = %q, want healthy", auth.Status)
	}
	if !auth.ExpiresAt.IsZero() {
		t.Fatalf("expires_at = %s, want zero advisory expiry", auth.ExpiresAt)
	}
	if auth.LastRefreshErr != "" {
		t.Fatalf("last_refresh_error = %q, want empty", auth.LastRefreshErr)
	}
}

func TestProviderDiscoversOpenAICompatibleModels(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
		case "/v1/models/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"gpt-upstream-a": map[string]any{
					"label":        "GPT Upstream A",
					"kind":         "group",
					"groupMembers": []string{"gpt-upstream-b"},
					"maxTokens":    128000,
					"quotaInfo": map[string]any{
						"remainingFraction": 0.75,
						"resetTime":         "2026-05-09T06:20:16Z",
					},
				},
			})
			return
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("authorization") == "Bearer sk_models" {
			sawAuth = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-upstream-a", "object": "model"},
				{"id": "gpt-upstream-b", "object": "model"},
			},
		})
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "sk_models")
	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if len(models) != 2 || models[0].ID != "gpt-upstream-a" || models[1].ID != "gpt-upstream-b" {
		t.Fatalf("unexpected models: %#v", models)
	}
	if !hasProviderCapability(models[0].Capabilities, provider.CapabilityOpenAIChat) || !hasProviderCapability(models[0].Capabilities, provider.CapabilityStreamSSE) {
		t.Fatalf("unexpected model capabilities: %#v", models[0].Capabilities)
	}
	if models[0].Quota == nil || models[0].Quota.RemainingPct != 75 || models[0].Quota.ResetAt.IsZero() {
		t.Fatalf("expected quota enrichment on first model: %#v", models[0])
	}
	if models[0].ContextTokens != 128000 || models[0].MaxContextTokens != 128000 {
		t.Fatalf("expected context enrichment from model status: %#v", models[0])
	}
	if models[0].Kind != "group" || strings.Join(models[0].GroupMembers, ",") != "gpt-upstream-b" {
		t.Fatalf("expected group metadata from model status: %#v", models[0])
	}
}

func TestProviderDiscoversAntigravitySubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer sk_antigravity" {
			t.Fatalf("missing auth header: %q", r.Header.Get("authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":  "Sam Sung",
			"email": "samtest4u@gmail.com",
			"planStatus": map[string]any{
				"planInfo": map[string]any{"planName": "Pro"},
			},
			"userTier": map[string]any{
				"id":                      "g1-ultra-tier",
				"name":                    "Google AI Ultra",
				"upgradeSubscriptionText": "You are subscribed to the best plan.",
			},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	registration.Auth.Subscription = nil
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_antigravity",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Account.ID != "samtest4u@gmail.com" || auth.Account.Display != "samtest4u@gmail.com" {
		t.Fatalf("unexpected account: %#v", auth.Account)
	}
	if auth.Subscription == nil {
		t.Fatalf("missing subscription")
	}
	if auth.Subscription.Name != "Google AI Ultra" || auth.Subscription.Tier != "Google AI Ultra" {
		t.Fatalf("unexpected subscription plan: %#v", auth.Subscription)
	}
	if auth.Subscription.Status != "You are subscribed to the best plan." {
		t.Fatalf("unexpected subscription status: %#v", auth.Subscription)
	}
}

func TestProviderDiscoversAntigravityUsageFromAccountQuota(t *testing.T) {
	var sawAccount bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			sawAccount = true
			if r.Header.Get("authorization") != "Bearer sk_antigravity" {
				t.Fatalf("missing auth header: %q", r.Header.Get("authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":  "Dave Kam",
				"email": "donghee.kam@gmail.com",
				"planStatus": map[string]any{
					"planInfo": map[string]any{"planName": "Pro"},
				},
				"userTier": map[string]any{
					"id":                      "g1-pro-tier",
					"name":                    "Google AI Pro",
					"upgradeSubscriptionText": "You can upgrade to the Google AI Ultra plan to receive the highest rate limits.",
				},
				"cascadeModelConfigData": map[string]any{
					"clientModelConfigs": []map[string]any{
						{
							"label": "Gemini 3.1 Pro (High)",
							"quotaInfo": map[string]any{
								"remainingFraction": 0.5,
								"resetTime":         "2026-05-13T17:10:31Z",
							},
						},
						{
							"label": "Claude Sonnet 4.6 (Thinking)",
							"quotaInfo": map[string]any{
								"remainingFraction": 1.0,
								"resetTime":         "2026-05-13T18:10:31Z",
							},
						},
					},
				},
			})
		case "/v1/models/status":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_antigravity",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	client.usage.Source = "direct-http+antigravity-usage-error"
	client.usage.PlanTier = "antigravity"
	usage, err := client.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !sawAccount {
		t.Fatalf("expected account quota request")
	}
	if !strings.Contains(usage.Source, "antigravity-account-quota") {
		t.Fatalf("unexpected usage source: %q", usage.Source)
	}
	if strings.Contains(usage.Source, "antigravity-usage-error") {
		t.Fatalf("successful quota refresh should clear stale error source: %q", usage.Source)
	}
	if usage.Subscription == nil || usage.Subscription.Name != "Google AI Pro" || usage.PlanTier != "Google AI Pro" {
		t.Fatalf("unexpected subscription or tier: usage=%#v subscription=%#v", usage, usage.Subscription)
	}
	native, ok := usage.NativeSummary.(formats.UsageReport)
	if !ok {
		t.Fatalf("unexpected native summary type: %T", usage.NativeSummary)
	}
	if native.RemainingPct != 0 || native.ResetAt != (time.Time{}) {
		t.Fatalf("native summary should expose quota only as windows to avoid duplicate current window: %#v", native)
	}
	if native.PlanTier != "Google AI Pro" || native.Unit != "quota" || len(native.Windows) != 2 {
		t.Fatalf("unexpected native usage summary: %#v", native)
	}
	if native.Windows[0].Label != "Gemini 3.1 Pro (High)" || native.Windows[0].RemainingPct != 50 || native.Windows[0].Unit != "quota" {
		t.Fatalf("unexpected first window: %#v", native.Windows[0])
	}
	if native.Windows[1].Label != "Claude Sonnet 4.6 (Thinking)" || native.Windows[1].RemainingPct != 100 || native.Windows[1].ResetAt.IsZero() {
		t.Fatalf("unexpected second window: %#v", native.Windows[1])
	}
}

func TestProviderDiscoversMiniMAXTokenPlanAccountAndUsage(t *testing.T) {
	var sawTokenPlan bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/token_plan/remains":
			sawTokenPlan = true
		case "/anthropic/v1/token_plan/remains":
			t.Fatalf("minimax token plan usage must be fetched from base host root, got %s", r.URL.Path)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer sk_minimax" {
			t.Fatalf("missing auth header: %q", r.Header.Get("authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model_remains": []map[string]any{
				{
					"model_name":                   "MiniMax-M*",
					"start_time":                   1778529600000,
					"end_time":                     1778544000000,
					"remains_time":                 900000,
					"current_interval_total_count": 1500,
					"current_interval_usage_count": 25,
					"current_weekly_total_count":   15000,
					"current_weekly_usage_count":   38,
					"weekly_start_time":            1778457600000,
					"weekly_end_time":              1779062400000,
					"weekly_remains_time":          519300000,
				},
			},
			"base_resp": map[string]any{"status_code": 0, "status_msg": "success"},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceMiniMAX
	registration.Identity.ProviderType = "minimax-api"
	registration.Identity.ProviderInstanceID = "minimax-api"
	registration.Identity.Account = provider.Account{Display: "minimax-prod"}
	registration.Auth.Account = provider.Account{Display: "minimax-prod"}
	registration.Auth.Subscription = nil
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL + "/anthropic",
		Dialect:      compat.APIDialectAnthropic,
		APIKey:       "sk_minimax",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	expectedAccount := miniMAXAccountFromAPIKey("sk_minimax")
	if auth.Account != expectedAccount {
		t.Fatalf("unexpected minimax account: %#v, want %#v", auth.Account, expectedAccount)
	}
	if auth.Subscription == nil || auth.Subscription.Name != "MiniMAX Token Plan" || auth.Subscription.Tier != "token-plan" {
		t.Fatalf("unexpected minimax subscription: %#v", auth.Subscription)
	}
	usage, err := client.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !sawTokenPlan {
		t.Fatalf("expected token plan endpoint request")
	}
	if !strings.Contains(usage.Source, "minimax-token-plan-remains") {
		t.Fatalf("unexpected usage source: %q", usage.Source)
	}
	native, ok := usage.NativeSummary.(formats.UsageReport)
	if !ok {
		t.Fatalf("unexpected native summary type: %T", usage.NativeSummary)
	}
	if native.PlanTier != "token-plan" || len(native.Windows) != 2 {
		t.Fatalf("unexpected native usage summary: %#v", native)
	}
	if native.Windows[0].Label != "MiniMax-M* current window" || native.Windows[0].Used != 25 || native.Windows[0].Limit != 1500 {
		t.Fatalf("unexpected current window: %#v", native.Windows[0])
	}
	if native.Windows[1].Label != "MiniMax-M* weekly" || native.Windows[1].Used != 38 || native.Windows[1].Limit != 15000 {
		t.Fatalf("unexpected weekly window: %#v", native.Windows[1])
	}
}

func TestProviderDiscoversGitHubCopilotUsageFromQuota(t *testing.T) {
	var sawQuota bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/quota" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawQuota = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quotaSnapshots": map[string]any{
				"chat": map[string]any{
					"isUnlimitedEntitlement": true,
					"entitlementRequests":    0,
					"usedRequests":           0,
					"remainingPercentage":    100,
					"resetDate":              "2030-05-16T15:47:20.803Z",
				},
				"premium_interactions": map[string]any{
					"isUnlimitedEntitlement": false,
					"entitlementRequests":    300,
					"usedRequests":           17,
					"remainingPercentage":    94.2,
					"resetDate":              "2030-06-01T00:00:00Z",
				},
			},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceGitHubCopilot
	registration.Identity.ProviderType = "github-copilot-sidecar"
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	usage, err := client.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !sawQuota {
		t.Fatalf("expected quota endpoint request")
	}
	if !strings.Contains(usage.Source, "github-copilot-quota") {
		t.Fatalf("unexpected usage source: %q", usage.Source)
	}
	if usage.Subscription == nil || usage.Subscription.Name != "GitHub Copilot" {
		t.Fatalf("unexpected subscription: %#v", usage.Subscription)
	}
	native, ok := usage.NativeSummary.(formats.UsageReport)
	if !ok {
		t.Fatalf("unexpected native summary type: %T", usage.NativeSummary)
	}
	if native.PlanTier != "github-copilot" || len(native.Windows) != 2 {
		t.Fatalf("unexpected native usage summary: %#v", native)
	}
	if native.Windows[0].Label != "Chat" || native.Windows[0].RemainingPct != 100 {
		t.Fatalf("unexpected chat window: %#v", native.Windows[0])
	}
	if native.Windows[1].Label != "Premium Interactions" || native.Windows[1].Used != 17 || native.Windows[1].Limit != 300 || native.Windows[1].RemainingPct != 94.2 {
		t.Fatalf("unexpected premium window: %#v", native.Windows[1])
	}
	if native.Windows[0].ResetAt.IsZero() || native.Windows[1].ResetAt.IsZero() {
		t.Fatalf("expected future reset timestamps: %#v", native.Windows)
	}
}

func TestProviderIgnoresAntigravityMockAccountFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":  "Mock User",
			"email": "mock@example.com",
			"planStatus": map[string]any{
				"planInfo": map[string]any{"planName": "Standard"},
			},
			"userTier": map[string]any{"name": "Free"},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	registration.Auth.Account = provider.Account{ID: "real@example.test", Display: "real@example.test"}
	registration.Auth.Subscription = nil
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_antigravity",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Account.Display != "real@example.test" || auth.Subscription != nil {
		t.Fatalf("mock fallback should not replace auth account/subscription: %#v", auth)
	}
}

func TestProviderDiscoversGeminiModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1beta/models":
		case "/v1/models/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"gemini-2.5-flash": map[string]any{
					"quotaInfo": map[string]any{
						"remainingFraction": 1.0,
						"resetTime":         "2026-05-09T06:20:16Z",
					},
				},
			})
			return
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"name":                       "models/gemini-2.5-flash",
					"displayName":                "Gemini 2.5 Flash",
					"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
					"inputTokenLimit":            1048576,
					"outputTokenLimit":           65536,
				},
			},
		})
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectGemini, "")
	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-2.5-flash" || models[0].ContextTokens != 1048576 || models[0].MaxOutputTokens != 65536 {
		t.Fatalf("unexpected models: %#v", models)
	}
	if !hasProviderCapability(models[0].Capabilities, provider.CapabilityGeminiGenerateContent) || !hasProviderCapability(models[0].Capabilities, provider.CapabilityStreamSSE) {
		t.Fatalf("unexpected model capabilities: %#v", models[0].Capabilities)
	}
	if len(models[0].Aliases) != 1 || models[0].Aliases[0] != "Gemini 2.5 Flash" {
		t.Fatalf("unexpected aliases: %#v", models[0].Aliases)
	}
	if models[0].Quota == nil || models[0].Quota.RemainingPct != 100 || models[0].Quota.ResetAt.IsZero() {
		t.Fatalf("expected quota enrichment: %#v", models[0])
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

func TestProviderInvokeMiniMAXAnthropicUpstreamRaisesSmallMaxTokens(t *testing.T) {
	var sawMaxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request compat.AnthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawMaxTokens = request.MaxTokens
		if request.Stream {
			t.Fatalf("buffered minimax request unexpectedly streamed: %#v", request)
		}
		if request.Model != "MiniMax-M2.7" {
			t.Fatalf("unexpected model %q", request.Model)
		}
		_ = json.NewEncoder(w).Encode(compat.AnthropicMessagesResponse{
			ID:         "msg-minimax",
			Type:       "message",
			Role:       "assistant",
			Model:      "MiniMax-M2.7",
			StopReason: "end_turn",
			Content: []compat.AnthropicContentBlock{
				{Type: "thinking", Text: "hidden reasoning"},
				{Type: "text", Text: "OK"},
			},
			Usage: compat.AnthropicUsage{InputTokens: 11, OutputTokens: 79},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceMiniMAX
	registration.Models = []provider.Model{{ID: "MiniMax-M2.7", MaxOutputTokens: 2048, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages}}}
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectAnthropic,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	response, err := client.Invoke(context.Background(), mustRegistration(t, client), compat.Request{
		Dialect:         compat.APIDialectOpenAI,
		Model:           "MiniMax-M2.7",
		MaxOutputTokens: 32,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "Reply with exactly OK."}},
		}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if sawMaxTokens != miniMAXM2MinimumTextOutputTokens {
		t.Fatalf("upstream max_tokens = %d, want %d", sawMaxTokens, miniMAXM2MinimumTextOutputTokens)
	}
	if response.Dialect != compat.APIDialectOpenAI || response.Message.Content[0].Text != "OK" {
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

func TestProviderInvokeStreamOpenAICompatibleUpstreamSSEToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-tool\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-tool\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_weather\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-tool\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-tool\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"Seoul\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	events := []compat.Event{}
	request := testOpenAIRequest("weather")
	request.Stream = true
	request.Tools = []compat.ToolDefinition{{
		Name:       "get_weather",
		Parameters: map[string]any{"type": "object"},
	}}
	response, err := client.InvokeStream(context.Background(), mustRegistration(t, client), request, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_weather" || call.Name != "get_weather" || call.Arguments != `{"city":"Seoul"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
	var toolDeltas int
	for _, event := range events {
		if event.Type == compat.EventToolCallDelta {
			toolDeltas++
		}
	}
	if toolDeltas != 3 || response.StopReason != "tool_calls" {
		t.Fatalf("unexpected events=%#v response=%#v", events, response)
	}
}

func TestProviderInvokeStreamOpenAIEmptySSEFallsBackToBuffered(t *testing.T) {
	var sawStream bool
	var sawBuffered bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !request.Stream {
			sawBuffered = true
			_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
				ID:     "chatcmpl-buffered",
				Object: "chat.completion",
				Model:  "gpt-upstream",
				Choices: []compat.OpenAIChatChoice{{
					Index:        0,
					Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "buffered fallback"},
					FinishReason: "stop",
				}},
			})
			return
		}
		sawStream = true
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(": stream opened\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-empty\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-empty\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":0,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	var events []compat.Event
	request := testOpenAIRequest("hello")
	request.Stream = true
	response, err := client.InvokeStream(context.Background(), mustRegistration(t, client), request, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if !sawStream || !sawBuffered {
		t.Fatalf("expected stream then buffered fallback, sawStream=%v sawBuffered=%v", sawStream, sawBuffered)
	}
	if response.Message.Content[0].Text != "buffered fallback" {
		t.Fatalf("unexpected fallback response: %#v", response)
	}
	if len(events) != 3 || events[0].Type != compat.EventMessageStart || events[1].ContentDelta.Text != "buffered fallback" || events[2].DoneReason != "stop" {
		t.Fatalf("unexpected fallback events: %#v", events)
	}
}

func TestProviderInvokeStreamOpenAIAntigravityEmptySSEDoesNotUseBufferedFallback(t *testing.T) {
	var sawBuffered bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !request.Stream {
			sawBuffered = true
			_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
				ID:     "chatcmpl-buffered",
				Object: "chat.completion",
				Model:  "gpt-upstream",
				Choices: []compat.OpenAIChatChoice{{
					Index:        0,
					Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "buffered fallback"},
					FinishReason: "stop",
				}},
			})
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(": stream opened\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-empty\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-empty\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	var events []compat.Event
	_, err = client.InvokeStream(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"), func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err == nil {
		t.Fatalf("expected empty stream error")
	}
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) || upstream.Code != "empty_stream" {
		t.Fatalf("expected empty_stream upstream error, got %v", err)
	}
	if sawBuffered {
		t.Fatalf("antigravity empty stream should not call buffered fallback")
	}
	if len(events) != 0 {
		t.Fatalf("empty stream should not emit semantic events: %#v", events)
	}
}

func TestProviderInvokeStreamOpenAIAntigravityDefaultDoesNotTimeoutBeforeContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(": stream opened\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		timer := time.NewTimer(25 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-late\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-late\",\"model\":\"gpt-upstream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	var events []compat.Event
	response, err := client.InvokeStream(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"), func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream should wait for late content by default: %v", err)
	}
	if response.Message.Content[0].Text != "late" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(events) == 0 {
		t.Fatalf("expected streamed events")
	}
}

func TestProviderInvokeStreamOpenAIAntigravityNoFirstEventTimeout(t *testing.T) {
	t.Setenv("PANGAEA_ANTIGRAVITY_STREAM_FIRST_EVENT_TIMEOUT", "10ms")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(": stream opened\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = client.InvokeStream(ctx, mustRegistration(t, client), testOpenAIRequest("hello"), func(compat.Event) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) || upstream.Code != "empty_stream_timeout" || upstream.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected empty_stream_timeout upstream error, got %v", err)
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

func TestProviderInvokeStreamMiniMAXAnthropicUpstreamSkipsThinkingAndRaisesSmallMaxTokens(t *testing.T) {
	var sawMaxTokens int
	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request compat.AnthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawMaxTokens = request.MaxTokens
		sawStream = request.Stream
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-minimax-stream\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"MiniMax-M2.7\",\"content\":[],\"usage\":{\"input_tokens\":11}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hidden\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"redacted\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":75}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceMiniMAX
	registration.Models = []provider.Model{{ID: "MiniMax-M2.7", MaxOutputTokens: 2048, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityStreamSSE}}}
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectAnthropic,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	events := []compat.Event{}
	response, err := client.InvokeStream(context.Background(), mustRegistration(t, client), compat.Request{
		Dialect:         compat.APIDialectGemini,
		Model:           "MiniMax-M2.7",
		MaxOutputTokens: 32,
		Stream:          true,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "Reply with exactly OK."}},
		}},
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if !sawStream {
		t.Fatalf("expected upstream stream=true")
	}
	if sawMaxTokens != miniMAXM2MinimumTextOutputTokens {
		t.Fatalf("upstream max_tokens = %d, want %d", sawMaxTokens, miniMAXM2MinimumTextOutputTokens)
	}
	if response.Dialect != compat.APIDialectGemini || response.Message.Content[0].Text != "OK" || response.Usage.TotalTokens != 86 {
		t.Fatalf("unexpected response: %#v", response)
	}
	var sawTextDelta bool
	for _, event := range events {
		if event.Type == compat.EventContentDelta && event.ContentDelta != nil && event.ContentDelta.Text == "OK" {
			sawTextDelta = true
		}
	}
	if !sawTextDelta {
		t.Fatalf("stream events did not include text delta: %#v", events)
	}
}

func TestProviderInvokeStreamGeminiCompatibleUpstreamSSE(t *testing.T) {
	var sawSSE bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-upstream:streamGenerateContent" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") == "sse" && r.URL.Query().Get("key") == "gemini-key" && strings.Contains(r.Header.Get("accept"), "text/event-stream") {
			sawSSE = true
		}
		var request compat.GeminiGenerateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(request.Contents) != 1 || request.Contents[0].Role != "user" || len(request.Contents[0].Parts) != 1 || request.Contents[0].Parts[0].Text != "hello" {
			t.Fatalf("unexpected upstream request: %#v", request)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"modelVersion\":\"gemini-upstream\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"gemini \"}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"modelVersion\":\"gemini-upstream\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"stream\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":4,\"totalTokenCount\":7}}\n\n"))
	}))
	defer server.Close()

	client, err := New(Options{
		Registration:     testRegistration(),
		BaseURL:          server.URL,
		Dialect:          compat.APIDialectGemini,
		APIKey:           "gemini-key",
		APIKeyMode:       "query",
		APIKeyQueryParam: "key",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	events := []compat.Event{}
	request := compat.Request{
		Dialect: compat.APIDialectGemini,
		Model:   "gemini-upstream",
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
	if !sawSSE {
		t.Fatalf("expected Gemini upstream SSE request")
	}
	if response.Model != "gemini-upstream" || response.Message.Content[0].Text != "gemini stream" || response.Usage.TotalTokens != 7 || response.StopReason != "stop" {
		t.Fatalf("unexpected stream response: %#v", response)
	}
	if len(events) != 5 || events[0].Type != compat.EventMessageStart || events[1].ContentDelta.Text != "gemini " || events[2].ContentDelta.Text != "stream" || events[3].UsageDelta.TotalTokens != 7 || events[4].DoneReason != "stop" {
		t.Fatalf("unexpected stream events: %#v", events)
	}
	usage, err := client.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Requests != 1 || usage.TotalTokens != 7 {
		t.Fatalf("unexpected accumulated usage: %#v", usage)
	}
}

func TestProviderInvokeStreamGeminiCompatibleUpstreamSSEError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}` + "\n\n"))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectGemini, "")
	events := []compat.Event{}
	request := compat.Request{
		Dialect: compat.APIDialectGemini,
		Model:   "gemini-upstream",
		Stream:  true,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}
	_, err := client.InvokeStream(context.Background(), mustRegistration(t, client), request, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("expected upstream error, got %T %v", err, err)
	}
	if upstream.Message != "quota exceeded" {
		t.Fatalf("unexpected upstream error: %#v", upstream)
	}
	if upstream.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected stream upstream error status 429, got %#v", upstream)
	}
	if len(events) != 1 || events[0].Type != compat.EventError || events[0].Error.Message != "quota exceeded" {
		t.Fatalf("unexpected stream error events: %#v", events)
	}
}

func TestProviderInvokeReturnsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("retry-after", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit_exceeded"}}`))
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
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("expected provider.UpstreamError, got %T %v", err, err)
	}
	if upstream.StatusCode != http.StatusTooManyRequests || upstream.Code != "rate_limit_exceeded" || upstream.Message != "rate limited" || upstream.RetryAfter != "12" {
		t.Fatalf("unexpected upstream error details: %#v", upstream)
	}
}

func TestProviderInvokeNormalizesWrappedAntigravityModelErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "resource exhausted",
			body:       `{"error":"ls_core returned status 500: {\"code\":\"unknown\",\"message\":\"RESOURCE_EXHAUSTED (code 429): You have exhausted your capacity on this model.\"}"}`,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "rate_limit_exceeded",
		},
		{
			name:       "not found",
			body:       `{"error":"ls_core returned status 500: {\"code\":\"unknown\",\"message\":\"NOT_FOUND (code 404): Requested entity was not found.\"}"}`,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
			_, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"))
			if err == nil {
				t.Fatalf("expected upstream error")
			}
			var upstream *provider.UpstreamError
			if !errors.As(err, &upstream) {
				t.Fatalf("expected provider.UpstreamError, got %T %v", err, err)
			}
			if upstream.StatusCode != tc.wantStatus || upstream.Code != tc.wantCode {
				t.Fatalf("unexpected upstream error details: %#v", upstream)
			}
		})
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

func TestProviderTracksUpstreamRateLimitHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exhausted","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	_, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"))
	if err == nil {
		t.Fatalf("expected upstream rate limit error")
	}
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusTooManyRequests || upstream.RetryAfter != "12" {
		t.Fatalf("unexpected upstream error: %v", err)
	}
	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != provider.HealthDegraded || health.Reason != "upstream rate limited" {
		t.Fatalf("unexpected health: %#v", health)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Status != provider.AuthHealthy {
		t.Fatalf("rate limit should not mark auth unavailable: %#v", auth)
	}
}

func TestAntigravityHealthProbeRecoversDegradedHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary upstream failure","code":"internal"}}`))
		case "/v1/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_antigravity",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	_, err = client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"))
	if err == nil {
		t.Fatalf("expected upstream error")
	}

	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != provider.HealthReady || health.Reason != "" {
		t.Fatalf("antigravity health probe should recover degraded health: %#v", health)
	}
}

func TestAntigravityHealthProbeMarksHealthDegraded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"not ready"}`))
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceAntigravity
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_antigravity",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != provider.HealthDegraded || health.Reason != "upstream health probe failed" {
		t.Fatalf("unexpected antigravity health: %#v", health)
	}
}

func TestProviderDoesNotDegradeHealthForUpstreamClientModelError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found","code":"not_found"}}`))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	_, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"))
	if err == nil {
		t.Fatalf("expected upstream not found error")
	}
	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != provider.HealthReady || health.Reason != "" {
		t.Fatalf("client model error should not degrade health: %#v", health)
	}
}

func TestProviderTracksUpstreamAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","code":"invalid_api_key"}}`))
	}))
	defer server.Close()

	client := newTestProvider(t, server.URL, compat.APIDialectOpenAI, "")
	_, err := client.Invoke(context.Background(), mustRegistration(t, client), testOpenAIRequest("hello"))
	if err == nil {
		t.Fatalf("expected upstream auth error")
	}
	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != provider.HealthDown || health.Reason != "upstream auth failed" {
		t.Fatalf("unexpected health: %#v", health)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Status != provider.AuthUnavailable || !strings.Contains(auth.LastRefreshErr, "invalid_api_key") {
		t.Fatalf("unexpected auth: %#v", auth)
	}
}

func TestProviderRefreshesGitHubCopilotAccountFromRelayAuthStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"isAuthenticated": true,
			"authType":        "user",
			"host":            "https://github.com",
			"login":           "octocat",
			"statusMessage":   "octocat",
			"subscription": map[string]any{
				"tier":   "copilot_for_business_seat",
				"name":   "Copilot Business",
				"status": "active",
			},
		})
	}))
	defer server.Close()

	registration := testRegistration()
	registration.Identity.Service = provider.ServiceGitHubCopilot
	registration.Identity.ProviderType = "github-copilot-sidecar"
	registration.Identity.ProviderInstanceID = "github-copilot-sidecar"
	registration.Identity.Kind = provider.KindSidecar
	registration.Identity.Account = provider.Account{}
	registration.Auth = provider.AuthState{Status: provider.AuthHealthy}
	client, err := New(Options{
		Registration: registration,
		BaseURL:      server.URL,
		Dialect:      compat.APIDialectOpenAI,
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	auth, err := client.Auth()
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth.Status != provider.AuthHealthy || auth.Account.Display != "octocat" || auth.SelectedSource != "copilot-auth-status" {
		t.Fatalf("unexpected copilot auth: %#v", auth)
	}
	if auth.Subscription == nil || auth.Subscription.Tier != "copilot_for_business_seat" || auth.Subscription.Name != "Copilot Business" || auth.Subscription.Status != "active" {
		t.Fatalf("unexpected copilot subscription: %#v", auth.Subscription)
	}
	updated, err := client.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if updated.Identity.Account.Display != "octocat" {
		t.Fatalf("registration account was not updated: %#v", updated.Identity.Account)
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
			ProviderType:       "api-compatible-test",
			ProviderInstanceID: "api-compatible-test-0001",
			NodeID:             "node-api",
			HostName:           "api-host",
			Service:            provider.ServiceDeepSeek,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages, provider.CapabilityStreamSSE, provider.CapabilityUsageRead},
		Models:       []provider.Model{{ID: "gpt-upstream", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account},
		RegisteredAt: now,
	}
}

func hasProviderCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
