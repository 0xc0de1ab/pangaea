package router

import (
	"strings"
	"testing"

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
