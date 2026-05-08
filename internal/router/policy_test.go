package router

import (
	"errors"
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
      - provider: codex-cli
        account: samtest4u@gmail.com
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
		registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0),
		registration("codex-nullcode-a1", "codex-cli", "nullcode@gmail.com", 50, 0),
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
	if decision.Selected != "codex-nullcode-a1" {
		t.Fatalf("expected higher weighted provider, got %q", decision.Selected)
	}
	if decision.CanonicalModel != "gpt-5.3-codex-spark" {
		t.Fatalf("expected canonical model, got %q", decision.CanonicalModel)
	}
	if len(decision.Scores) != 2 {
		t.Fatalf("expected two candidate scores, got %#v", decision.Scores)
	}
	if decision.Scores[0].ProviderInstanceID != "codex-nullcode-a1" || decision.Scores[0].Score != 50 || decision.Scores[0].Weight != 50 {
		t.Fatalf("unexpected top candidate score: %#v", decision.Scores)
	}
	if decision.Scores[0].Reason == "" {
		t.Fatalf("expected score reason, got %#v", decision.Scores)
	}
}

func TestRoutingPolicyEvaluateRejectsMissingCapability(t *testing.T) {
	policy := validPolicy()
	reg := registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0)
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
				Candidates:  []Candidate{{Provider: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityOpenAIChat}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
			{
				ID:          "codex-anthropic",
				Match:       RouteMatch{Models: []string{"codex-default", "gpt-5.5"}, APIDialects: []compat.APIDialect{compat.APIDialectAnthropic}},
				Candidates:  []Candidate{{Provider: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityAnthropicMessages}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
			{
				ID:          "codex-gemini",
				Match:       RouteMatch{Models: []string{"codex-default", "gpt-5.5"}, APIDialects: []compat.APIDialect{compat.APIDialectGemini}},
				Candidates:  []Candidate{{Provider: "codex-cli", Weight: 100}},
				Constraints: Constraints{RequiredCapabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent}, AuthStatus: []provider.AuthStatus{provider.AuthHealthy}, HealthState: []provider.HealthStatus{provider.HealthReady}},
			},
		},
	}
	reg := registration("codex-cli", "codex-cli", "samtest4u@gmail.com", 100, 0)
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
		registration("codex-busy-a1", "codex-cli", "samtest4u@gmail.com", 100, 9),
		registration("codex-ready-a1", "codex-cli", "nullcode@gmail.com", 10, 1),
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
					{Provider: "codex-cli", Account: "samtest4u@gmail.com", Weight: 10},
					{Provider: "codex-cli", Account: "nullcode@gmail.com", Weight: 50},
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

func registration(instanceID, providerID, account string, weight, queueDepth int) provider.Registration {
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         providerID,
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
