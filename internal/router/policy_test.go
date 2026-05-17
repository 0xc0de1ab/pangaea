package router

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestParseRoutingPolicyYAML(t *testing.T) {
	policy, err := ParseRoutingPolicyYAML([]byte(`
version: routing-policy/v1
model_aliases:
  gpt-5-codex:
    canonical_model: gpt-5.3-codex-spark
    required_capabilities: [api.openai.chat]
routes:
  - id: codex-primary
    match:
      models: [gpt-5-codex]
      api_dialects: [openai]
    candidates:
      - provider_type: codex-cli
        account: primary@example.test
        host_name: snowbox
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	if got := policy.ModelAliases["gpt-5-codex"].CanonicalModel; got != "gpt-5.3-codex-spark" {
		t.Fatalf("expected alias canonical model, got %q", got)
	}
}

func TestRoutingPolicyEvaluateSelectsHighestWeightHealthyCandidate(t *testing.T) {
	policy := validPolicy()
	registrations := []provider.Registration{
		registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0),
		registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0),
	}

	decision := policy.Evaluate(RouteRequest{
		TenantID:   "team-a",
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	}, registrations)
	if !decision.Allowed {
		t.Fatalf("expected route allowed: %#v", decision)
	}
	if decision.Selected != "codex-secondary-a1" {
		t.Fatalf("expected higher weighted provider, got %q", decision.Selected)
	}
	if decision.CanonicalModel != "gpt-5.3-codex-spark" {
		t.Fatalf("expected canonical model, got %q", decision.CanonicalModel)
	}
	if len(decision.Scores) != 2 {
		t.Fatalf("expected two candidate scores, got %#v", decision.Scores)
	}
	if decision.Scores[0].ProviderInstanceID != "codex-secondary-a1" || decision.Scores[0].Score != 50 || decision.Scores[0].Weight != 50 {
		t.Fatalf("unexpected top candidate score: %#v", decision.Scores)
	}
	if decision.Scores[0].Reason == "" {
		t.Fatalf("expected score reason, got %#v", decision.Scores)
	}
}

func TestRoutingPolicyEvaluateRejectsMissingCapability(t *testing.T) {
	policy := validPolicy()
	reg := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
	reg.Capabilities = []provider.Capability{provider.CapabilityOpenAIChat}

	decision := policy.Evaluate(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	}, []provider.Registration{reg})
	if decision.Allowed {
		t.Fatalf("expected route denied")
	}
	if len(decision.Rejections) == 0 || decision.Rejections[0].Reason == "" {
		t.Fatalf("expected rejection reason, got %#v", decision.Rejections)
	}
}

func TestRoutingPolicyEvaluateRoutesSameProviderAcrossDialects(t *testing.T) {
	policy := RoutingPolicy{
		Version: RoutingPolicyVersion,
		ModelAliases: map[string]ModelAlias{
			"codex-default": {CanonicalModel: "gpt-5.5"},
		},
		Routes: []Route{
			{
				ID:          "codex-openai",
				Match:       RouteMatch{Models: []string{"codex-default", "gpt-5.5"}, APIDialects: []compat.APIDialect{compat.APIDialectOpenAI}},
				Candidates:  []Candidate{{ProviderType: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityOpenAIChat}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
			{
				ID:          "codex-anthropic",
				Match:       RouteMatch{Models: []string{"codex-default", "gpt-5.5"}, APIDialects: []compat.APIDialect{compat.APIDialectAnthropic}},
				Candidates:  []Candidate{{ProviderType: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityAnthropicMessages}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
			{
				ID:          "codex-gemini",
				Match:       RouteMatch{Models: []string{"codex-default", "gpt-5.5"}, APIDialects: []compat.APIDialect{compat.APIDialectGemini}},
				Candidates:  []Candidate{{ProviderType: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
		},
	}
	reg := registration("codex-cli", "codex-cli", "primary@example.test", 100, 0)
	reg.Capabilities = []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityStreamSSE,
	}

	for _, tc := range []struct {
		dialect compat.APIDialect
		routeID string
	}{
		{dialect: compat.APIDialectOpenAI, routeID: "codex-openai"},
		{dialect: compat.APIDialectAnthropic, routeID: "codex-anthropic"},
		{dialect: compat.APIDialectGemini, routeID: "codex-gemini"},
	} {
		decision := policy.Evaluate(RouteRequest{Model: "codex-default", APIDialect: tc.dialect, Stream: true}, []provider.Registration{reg})
		if !decision.Allowed || decision.RouteID != tc.routeID || decision.Selected != "codex-cli" {
			t.Fatalf("dialect %s decision = %#v, want route %s selected codex-cli", tc.dialect, decision, tc.routeID)
		}
	}
}

func TestRoutingPolicyEvaluateFiltersQueueDepth(t *testing.T) {
	policy := validPolicy()
	registrations := []provider.Registration{
		registration("codex-busy-a1", "codex-cli", "primary@example.test", 100, 9),
		registration("codex-ready-a1", "codex-cli", "secondary@example.test", 10, 1),
	}

	decision := policy.Evaluate(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	}, registrations)
	if !decision.Allowed {
		t.Fatalf("expected fallback provider to be allowed: %#v", decision)
	}
	if decision.Selected != "codex-ready-a1" {
		t.Fatalf("expected ready fallback, got %q", decision.Selected)
	}
}

func TestRoutingPolicyEvaluateRejectsUnsupportedReportedModel(t *testing.T) {
	policy := RoutingPolicy{
		Version: RoutingPolicyVersion,
		Routes: []Route{
			{
				ID:          "antigravity-openai",
				Match:       RouteMatch{APIDialects: []compat.APIDialect{compat.APIDialectOpenAI}},
				Candidates:  []Candidate{{ProviderType: "antigravity-sidecar", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityOpenAIChat}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
		},
	}
	reg := registration("antigravity-sidecar", "antigravity-sidecar", "primary@example.test", 100, 0)
	reg.Identity.Service = provider.ServiceAntigravity
	reg.Identity.Kind = provider.KindSidecar
	reg.Capabilities = []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}
	reg.Models = []provider.Model{
		{ID: "antigravity-default", Aliases: []string{"antigravity-default"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
		{ID: "gemini-2.5-flash", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
	}

	decision := policy.Evaluate(RouteRequest{
		Model:      "gpt-4o",
		APIDialect: compat.APIDialectOpenAI,
	}, []provider.Registration{reg})
	if decision.Allowed {
		t.Fatalf("expected unsupported model to be denied: %#v", decision)
	}
	if len(decision.Rejections) == 0 || !strings.Contains(decision.Rejections[0].Reason, "model not reported by provider: gpt-4o") {
		t.Fatalf("expected model rejection, got %#v", decision.Rejections)
	}
}

func TestRoutingPolicyEvaluateAcceptsReportedModelAlias(t *testing.T) {
	policy := RoutingPolicy{
		Version: RoutingPolicyVersion,
		ModelAliases: map[string]ModelAlias{
			"codex-default": {CanonicalModel: "gpt-5.5"},
		},
		Routes: []Route{
			{
				ID:          "codex-openai",
				Match:       RouteMatch{Models: []string{"codex-default", "gpt-5.5"}, APIDialects: []compat.APIDialect{compat.APIDialectOpenAI}},
				Candidates:  []Candidate{{ProviderType: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityOpenAIChat}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
		},
	}
	reg := registration("codex-cli", "codex-cli", "primary@example.test", 100, 0)
	reg.Models = []provider.Model{
		{ID: "gpt-5.5", Aliases: []string{"codex-default"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
	}

	decision := policy.Evaluate(RouteRequest{
		Model:      "codex-default",
		APIDialect: compat.APIDialectOpenAI,
	}, []provider.Registration{reg})
	if !decision.Allowed || decision.Selected != "codex-cli" {
		t.Fatalf("expected reported model alias to route: %#v", decision)
	}
}

func TestRoutingPolicyEvaluateHonorsPinnedProviderInstance(t *testing.T) {
	policy := RoutingPolicy{
		Version: RoutingPolicyVersion,
		ModelAliases: map[string]ModelAlias{
			"github-copilot-default": {CanonicalModel: "auto"},
		},
		Routes: []Route{
			{
				ID:         "codex-openai",
				Match:      RouteMatch{Models: []string{"codex-default"}, APIDialects: []compat.APIDialect{compat.APIDialectOpenAI}},
				Candidates: []Candidate{{ProviderType: "codex-cli", Weight: 100}},
			},
		},
	}
	copilot := registration("github-copilot-130258", "github-copilot-sidecar", "copilot@example.test", 100, 0)
	copilot.Identity.Service = provider.ServiceGitHubCopilot
	copilot.Capabilities = []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}
	copilot.Models = []provider.Model{{ID: "auto", Kind: "group", GroupMembers: []string{"gpt-5.2"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}}}

	decision := policy.Evaluate(RouteRequest{
		ProviderInstanceID: "github-copilot-130258",
		Model:              "github-copilot-default",
		APIDialect:         compat.APIDialectOpenAI,
		Stream:             true,
	}, []provider.Registration{copilot})
	if !decision.Allowed || decision.Selected != "github-copilot-130258" || decision.CanonicalModel != "auto" {
		t.Fatalf("expected pinned provider to route through auto model: %#v", decision)
	}
	if decision.RouteID != "provider:github-copilot-130258" {
		t.Fatalf("expected provider-pinned route id, got %q", decision.RouteID)
	}
}

func TestRoutingPolicyEvaluateUsesCanonicalModelPriorityList(t *testing.T) {
	policy := RoutingPolicy{
		Version: RoutingPolicyVersion,
		ModelAliases: map[string]ModelAlias{
			"gemini-auto": {CanonicalModels: []string{"gemini-3.1-pro-preview", "gemini-3-flash-preview", "gemini-2.5-pro"}},
		},
		Routes: []Route{
			{
				ID:          "gemini-openai",
				Match:       RouteMatch{Models: []string{"gemini-auto"}, APIDialects: []compat.APIDialect{compat.APIDialectOpenAI}},
				Candidates:  []Candidate{{ProviderType: "gemini-cli", Weight: 10}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityOpenAIChat}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
		},
	}
	low := registration("gemini-low", "gemini-cli", "low@example.test", 100, 0)
	low.Identity.Service = provider.ServiceGemini
	low.Models = []provider.Model{{ID: "gemini-2.5-pro", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}}}
	high := registration("gemini-high", "gemini-cli", "high@example.test", 1, 0)
	high.Identity.Service = provider.ServiceGemini
	high.Models = []provider.Model{{ID: "gemini-3-flash-preview", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}}}

	decision := policy.Evaluate(RouteRequest{
		Model:      "gemini-auto",
		APIDialect: compat.APIDialectOpenAI,
	}, []provider.Registration{low, high})
	if !decision.Allowed {
		t.Fatalf("expected group model to route: %#v", decision)
	}
	if decision.Selected != "gemini-high" || decision.CanonicalModel != "gemini-3-flash-preview" {
		t.Fatalf("expected highest priority supported canonical model, got selected=%q canonical=%q decision=%#v", decision.Selected, decision.CanonicalModel, decision)
	}
}

func TestRoutingPolicyEvaluatePrefersSoonestQuotaResetOnTie(t *testing.T) {
	policy := RoutingPolicy{
		Version: RoutingPolicyVersion,
		Routes: []Route{
			{
				ID: "ag-openai",
				Match: RouteMatch{
					Models:      []string{"claude-sonnet-4-6"},
					APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
				},
				Candidates: []Candidate{
					{ProviderType: "antigravity-sidecar", Weight: 10},
				},
				Constraints: Constraints{
					RequiredCapabilities: []provider.Capability{provider.CapabilityOpenAIChat},
					AuthStatus:           []provider.AuthStatus{provider.AuthHealthy},
					HealthState:          []provider.HealthStatus{provider.HealthReady},
				},
			},
		},
	}
	now := time.Now().UTC()
	later := registration("ag-a", "antigravity-sidecar", "a@example.test", 10, 0)
	later.Identity.Service = provider.ServiceAntigravity
	later.Models = []provider.Model{{
		ID:           "claude-sonnet-4-6",
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Quota:        &provider.ModelQuota{RemainingPct: 30, ResetAt: now.Add(4 * time.Hour)},
	}}
	sooner := registration("ag-z", "antigravity-sidecar", "z@example.test", 10, 0)
	sooner.Identity.Service = provider.ServiceAntigravity
	sooner.Models = []provider.Model{{
		ID:           "claude-sonnet-4-6",
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Quota:        &provider.ModelQuota{RemainingPct: 30, ResetAt: now.Add(30 * time.Minute)},
	}}

	decision := policy.Evaluate(RouteRequest{
		Model:      "claude-sonnet-4-6",
		APIDialect: compat.APIDialectOpenAI,
	}, []provider.Registration{later, sooner})
	if !decision.Allowed {
		t.Fatalf("expected policy to route: %#v", decision)
	}
	if decision.Selected != "ag-z" {
		t.Fatalf("expected soonest quota reset provider, got %q decision=%#v", decision.Selected, decision)
	}
	if len(decision.FallbackChain) < 2 || decision.FallbackChain[0] != "ag-z" {
		t.Fatalf("expected fallback chain to prefer soonest reset, got %#v", decision.FallbackChain)
	}
}

func TestRoutingPolicyValidateRejectsInvalidCapability(t *testing.T) {
	policy := validPolicy()
	policy.ModelAliases["gpt-5-codex"] = ModelAlias{
		CanonicalModel:       "gpt-5.3-codex-spark",
		RequiredCapabilities: []provider.Capability{"api.invalid"},
	}

	if err := policy.Validate(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected ErrInvalidPolicy, got %v", err)
	}
}

func validPolicy() RoutingPolicy {
	return RoutingPolicy{
		Version: RoutingPolicyVersion,
		ModelAliases: map[string]ModelAlias{
			"gpt-5-codex": {
				CanonicalModel: "gpt-5.3-codex-spark",
				RequiredCapabilities: []provider.Capability{
					provider.CapabilityOpenAIChat,
				},
			},
		},
		Routes: []Route{
			{
				ID: "codex-primary",
				Match: RouteMatch{
					Models:      []string{"gpt-5-codex"},
					APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
				},
				Candidates: []Candidate{
					{ProviderType: "codex-cli", Account: "primary@example.test", Weight: 10},
					{ProviderType: "codex-cli", Account: "secondary@example.test", Weight: 50},
				},
				Constraints: Constraints{
					AuthStatus:    []provider.AuthStatus{provider.AuthHealthy, provider.AuthRefreshSoon},
					HealthState:   []provider.HealthStatus{provider.HealthReady},
					MaxQueueDepth: 4,
				},
			},
		},
	}
}

func registration(instanceID, providerType, account string, weight, queueDepth int) provider.Registration {
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       providerType,
			ProviderInstanceID: instanceID,
			NodeID:             "node-a1",
			HostName:           "snowbox",
			Service:            provider.ServiceCodex,
			Kind:               provider.KindCLIContainer,
			Account:            provider.Account{ID: account, Display: account},
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
		},
		Health: provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		Auth: provider.AuthState{
			Status:      provider.AuthHealthy,
			Refreshable: true,
		},
		Limits:       provider.LimitState{QueueDepth: queueDepth},
		RegisteredAt: time.Now(),
	}
}
