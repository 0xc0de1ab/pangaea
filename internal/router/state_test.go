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
		Status:         "completed",
		StartedAt:      time.Unix(1, 0),
		CompletedAt:    time.Unix(2, 0),
		EstimatedUsage: quota.Usage{Tokens: 5, Requests: 1},
	})
	snapshot := engine.SnapshotState(time.Unix(3, 0))
	if snapshot.Version != RouterStateSnapshotVersion || len(snapshot.Traces) != 1 || len(snapshot.Quotas) != 1 {
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
}
