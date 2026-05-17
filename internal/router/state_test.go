package router

import (
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func TestEngineSnapshotAndRestoreState(t *testing.T) {
	ledger := quota.NewLedger()
	scope := quota.Scope{TenantID: "team-a", Model: "gpt-5"}
	if err := ledger.SetLimit(scope, quota.Limit{MaxTokens: 100, MaxRequests: 10}); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), ledger)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.recordRequestTrace(RequestTrace{
		RequestID:      "req_state_1",
		RouteRequest:   RouteRequest{RoutingRuleName: "mine", RoutingRuleOwner: "user@example.test", UserID: "user@example.test"},
		Decision:       RouteDecision{RoutingRuleID: "user:user@example.test:mine"},
		Status:         "completed",
		StartedAt:      time.Unix(1, 0),
		CompletedAt:    time.Unix(2, 0),
		EstimatedUsage: quota.Usage{Tokens: 5, Requests: 1},
	})
	if _, err := engine.UpsertUser(RouterUserUpsertRequest{Email: "user@example.test", Role: RouterUserRoleUser}); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if _, err := engine.UpsertRoutingRule(RoutingRule{
		Name:       "mine",
		Scope:      RoutingRuleScopeUser,
		OwnerEmail: "user@example.test",
		Filters:    []RoutingFilter{{Type: "any", Label: "Any"}},
	}); err != nil {
		t.Fatalf("upsert routing rule: %v", err)
	}
	engine.UpsertNotifierStatus(NotifierStatus{
		ID:          "telegram",
		Type:        "telegram",
		Destination: "123...789",
		Enabled:     true,
		State:       "ready",
		UpdatedAt:   time.Unix(2, 0),
	})
	engine.RecordNotifierDelivery(NotifierDelivery{
		NotifierID:  "telegram",
		Type:        "startup",
		Destination: "123...789",
		Status:      "sent",
		CreatedAt:   time.Unix(2, 0),
		CompletedAt: time.Unix(2, 1),
	})
	snapshot := engine.SnapshotState(time.Unix(3, 0))
	if snapshot.Version != RouterStateSnapshotVersion || len(snapshot.Traces) != 1 || len(snapshot.RoutingRuleStats) != 1 || len(snapshot.Quotas) != 1 || len(snapshot.Users) != 1 || len(snapshot.RoutingRules) != 1 || len(snapshot.Notifiers) != 1 || len(snapshot.NotificationHistory) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	restored, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new restored engine: %v", err)
	}
	restored.RestoreState(snapshot)
	trace, ok := restored.RequestTrace("req_state_1")
	if !ok || trace.Status != "completed" {
		t.Fatalf("trace was not restored: %#v ok=%v", trace, ok)
	}
	limit, _, _, err := restored.ledger.Snapshot(scope)
	if err != nil {
		t.Fatalf("snapshot restored quota: %v", err)
	}
	if limit.MaxTokens != 100 {
		t.Fatalf("quota limit was not restored: %#v", limit)
	}
	if user, ok := restored.GetUserByEmail("user@example.test"); !ok || user.Role != RouterUserRoleUser {
		t.Fatalf("user was not restored: %#v ok=%v", user, ok)
	}
	if rule, ok := restored.FindRoutingRule(RoutingRuleScopeUser, "user@example.test", "mine"); !ok || rule.Name != "mine" {
		t.Fatalf("routing rule was not restored: %#v ok=%v", rule, ok)
	}
	if stats := restored.RoutingRuleStats()["user:user@example.test:mine"]; stats.Requests != 1 || stats.Tokens != 5 {
		t.Fatalf("routing rule stats were not restored: %#v", stats)
	}
	notifierStatuses := restored.NotifierStatuses()
	if len(notifierStatuses) != 1 || notifierStatuses[0].ID != "telegram" {
		t.Fatalf("notifier status was not restored: %#v", notifierStatuses)
	}
	notifierHistory := restored.NotifierHistory(10)
	if len(notifierHistory) != 1 || notifierHistory[0].NotifierID != "telegram" || notifierHistory[0].Status != "sent" {
		t.Fatalf("notifier history was not restored: %#v", notifierHistory)
	}
}
