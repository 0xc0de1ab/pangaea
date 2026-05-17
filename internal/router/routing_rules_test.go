package router

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func TestRoutingRuleNameMustBeURLSafe(t *testing.T) {
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	invalidNames := []string{
		"has space",
		"has/slash",
		"has%percent",
		"한글",
	}
	for _, name := range invalidNames {
		_, err := engine.UpsertRoutingRule(RoutingRule{
			Name:    name,
			Scope:   RoutingRuleScopePublic,
			Filters: []RoutingFilter{{Type: "any"}},
		})
		if err == nil || !strings.Contains(err.Error(), "URL-safe") {
			t.Fatalf("name %q should be rejected as URL-safe violation, got %v", name, err)
		}
	}
}

func TestRoutingRuleNameIsNotSlugged(t *testing.T) {
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:    "Fast.Route_1~A",
		Scope:   RoutingRuleScopePublic,
		Filters: []RoutingFilter{{Type: "any"}},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}
	if rule.ID != "public:Fast.Route_1~A" {
		t.Fatalf("routing rule ID was slugged: %q", rule.ID)
	}
	if _, ok := engine.FindRoutingRule(RoutingRuleScopePublic, "", "fast-route-1-a"); ok {
		t.Fatalf("slugged name should not resolve")
	}
	if _, ok := engine.FindRoutingRule(RoutingRuleScopePublic, "", "Fast.Route_1~A"); !ok {
		t.Fatalf("exact URL-safe name should resolve")
	}
}

func TestRoutingRuleStatsFromRequestTraces(t *testing.T) {
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:    "fast",
		Scope:   RoutingRuleScopePublic,
		Filters: []RoutingFilter{{Type: "any"}},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}
	now := time.Date(2026, 5, 17, 4, 30, 0, 0, time.UTC)
	engine.recordRequestTrace(RequestTrace{
		RequestID:      "req_1",
		RouteRequest:   RouteRequest{RoutingRuleName: "fast", Model: "gpt-5", APIDialect: compat.APIDialectOpenAI},
		Decision:       RouteDecision{RoutingRuleID: rule.ID},
		ActualUsage:    quota.Usage{Tokens: 12, Requests: 1},
		EstimatedUsage: quota.Usage{Tokens: 8, Requests: 1},
		StartedAt:      now,
		CompletedAt:    now.Add(time.Second),
	})
	engine.recordRequestTrace(RequestTrace{
		RequestID:      "req_2",
		RouteRequest:   RouteRequest{RoutingRuleName: "fast", Model: "gpt-5", APIDialect: compat.APIDialectOpenAI},
		Decision:       RouteDecision{RoutingRuleID: rule.ID},
		EstimatedUsage: quota.Usage{Tokens: 5, Requests: 1},
		StartedAt:      now.Add(time.Minute),
		CompletedAt:    now.Add(time.Minute + time.Second),
	})

	stats := engine.RoutingRuleStats()
	stat := stats[rule.ID]
	if stat.Requests != 2 || stat.Tokens != 17 || stat.ActualTokens != 12 || stat.EstimatedTokens != 5 {
		t.Fatalf("unexpected stats: %#v", stat)
	}
	if stat.LastUsedAt == nil || !stat.LastUsedAt.Equal(now.Add(time.Minute+time.Second)) {
		t.Fatalf("unexpected last used: %#v", stat.LastUsedAt)
	}

	visible := attachRoutingRuleStats([]RoutingRule{rule}, stats)
	if len(visible) != 1 || visible[0].Stats == nil || visible[0].Stats.Tokens != 17 {
		t.Fatalf("stats were not attached: %#v", visible)
	}
}

func TestRoutingRuleTieBreakPrefersLessUsedProvider(t *testing.T) {
	registry := provider.NewRegistry()
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	left := registration("aa-codex", "codex-cli", "left@example.test", 1, 0)
	right := registration("zz-codex", "codex-cli", "right@example.test", 1, 0)
	left.Models = []provider.Model{{ID: "gpt-5"}}
	right.Models = []provider.Model{{ID: "gpt-5"}}
	if err := registry.Upsert(left); err != nil {
		t.Fatalf("register left: %v", err)
	}
	if err := registry.Upsert(right); err != nil {
		t.Fatalf("register right: %v", err)
	}
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:  "balanced",
		Scope: RoutingRuleScopePublic,
		Filters: []RoutingFilter{{
			Type: "criteria",
			Criteria: RoutingFilterCriteria{
				Services:    []provider.Service{provider.ServiceCodex},
				Models:      []string{"gpt-5"},
				APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
			},
		}},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}
	request := RouteRequest{RoutingRuleName: rule.Name, Model: "gpt-5", APIDialect: compat.APIDialectOpenAI, Stream: true}
	first, _ := engine.evaluateRoutingRule(rule, request)
	if first.Selected != left.Identity.ProviderInstanceID {
		t.Fatalf("expected lexical initial selection, got %#v", first.Selected)
	}
	now := time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC)
	engine.recordRequestTrace(RequestTrace{
		RequestID:    "req_left",
		RouteRequest: request,
		Decision: RouteDecision{
			RoutingRuleID: rule.ID,
			Selected:      left.Identity.ProviderInstanceID,
		},
		Provider:    &left.Identity,
		Status:      "completed",
		StartedAt:   now,
		CompletedAt: now.Add(time.Second),
	})
	second, _ := engine.evaluateRoutingRule(rule, request)
	if second.Selected != right.Identity.ProviderInstanceID {
		t.Fatalf("expected less-used provider after trace history, got %#v", second.Selected)
	}
	if len(second.FallbackChain) < 2 || second.FallbackChain[0] != right.Identity.ProviderInstanceID {
		t.Fatalf("expected fallback chain to be history-balanced, got %#v", second.FallbackChain)
	}
}

func TestRoutingRuleTieBreakPrefersSoonestQuotaResetBeforeHistory(t *testing.T) {
	registry := provider.NewRegistry()
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	now := time.Now().UTC()
	sooner := registration("aa-codex", "codex-cli", "soon@example.test", 1, 0)
	later := registration("zz-codex", "codex-cli", "later@example.test", 1, 0)
	sooner.Models = []provider.Model{{
		ID:           "gpt-5",
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Quota:        &provider.ModelQuota{RemainingPct: 50, ResetAt: now.Add(15 * time.Minute)},
	}}
	later.Models = []provider.Model{{
		ID:           "gpt-5",
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Quota:        &provider.ModelQuota{RemainingPct: 50, ResetAt: now.Add(3 * time.Hour)},
	}}
	if err := registry.Upsert(sooner); err != nil {
		t.Fatalf("register sooner: %v", err)
	}
	if err := registry.Upsert(later); err != nil {
		t.Fatalf("register later: %v", err)
	}
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:  "quota-reset",
		Scope: RoutingRuleScopePublic,
		Filters: []RoutingFilter{{
			Type: "criteria",
			Criteria: RoutingFilterCriteria{
				Services:    []provider.Service{provider.ServiceCodex},
				Models:      []string{"gpt-5"},
				APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
			},
		}},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}
	request := RouteRequest{RoutingRuleName: rule.Name, Model: "gpt-5", APIDialect: compat.APIDialectOpenAI, Stream: true}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		engine.recordRequestTrace(RequestTrace{
			RequestID:    "req_soon_" + strconv.Itoa(i),
			RouteRequest: request,
			Decision: RouteDecision{
				RoutingRuleID: rule.ID,
				Selected:      sooner.Identity.ProviderInstanceID,
			},
			Provider:    &sooner.Identity,
			Status:      "completed",
			StartedAt:   at,
			CompletedAt: at.Add(time.Second),
		})
	}

	decision, _ := engine.evaluateRoutingRule(rule, request)
	if decision.Selected != sooner.Identity.ProviderInstanceID {
		t.Fatalf("expected soonest quota reset to outrank less-used history, got %#v", decision.Selected)
	}
}
