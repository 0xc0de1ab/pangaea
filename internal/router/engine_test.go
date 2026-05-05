package router

import (
	"context"
	"errors"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func TestEngineDryRunUsesRegistry(t *testing.T) {
	engine, _ := testEngine(t)

	decision := engine.DryRun(RouteRequest{
		TenantID:   "team-a",
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	})
	if !decision.Allowed {
		t.Fatalf("expected allowed decision: %#v", decision)
	}
	if decision.Selected == "" {
		t.Fatalf("expected selected provider")
	}
}

func TestEngineReserveRouteReservesQuota(t *testing.T) {
	engine, ledger := testEngine(t)

	execution, err := engine.ReserveRoute(RouteExecutionRequest{
		RequestID: "req_1",
		RouteRequest: RouteRequest{
			TenantID:   "team-a",
			Model:      "gpt-5-codex",
			APIDialect: compat.APIDialectOpenAI,
			Stream:     true,
		},
		QuotaScope: quota.Scope{
			TenantID: "team-a",
			UserID:   "usr_1",
			APIKeyID: "key_1",
		},
		QuotaEstimate: quota.Usage{Tokens: 20, Requests: 1},
	})
	if err != nil {
		t.Fatalf("reserve route: %v", err)
	}
	if execution.Reservation.Status != quota.ReservationReserved {
		t.Fatalf("expected reserved quota, got %#v", execution.Reservation)
	}
	_, _, reserved, err := ledger.Snapshot(quota.Scope{
		TenantID: "team-a",
		UserID:   "usr_1",
		APIKeyID: "key_1",
		Model:    "gpt-5.3-codex-spark",
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if reserved.Tokens != 20 {
		t.Fatalf("expected 20 reserved tokens, got %d", reserved.Tokens)
	}
}

func TestEngineReserveRouteRejectsQuotaExceeded(t *testing.T) {
	engine, _ := testEngine(t)

	_, err := engine.ReserveRoute(RouteExecutionRequest{
		RequestID: "req_1",
		RouteRequest: RouteRequest{
			TenantID:   "team-a",
			Model:      "gpt-5-codex",
			APIDialect: compat.APIDialectOpenAI,
			Stream:     true,
		},
		QuotaScope: quota.Scope{
			TenantID: "team-a",
			UserID:   "usr_1",
			APIKeyID: "key_1",
		},
		QuotaEstimate: quota.Usage{Tokens: 200, Requests: 1},
	})
	if !errors.Is(err, quota.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestEngineCommitAndRelease(t *testing.T) {
	engine, _ := testEngine(t)
	request := RouteExecutionRequest{
		RequestID: "req_1",
		RouteRequest: RouteRequest{
			Model:      "gpt-5-codex",
			APIDialect: compat.APIDialectOpenAI,
			Stream:     true,
		},
		QuotaScope: quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{
			Tokens:   10,
			Requests: 1,
		},
	}
	if _, err := engine.ReserveRoute(request); err != nil {
		t.Fatalf("reserve route: %v", err)
	}
	reservation, err := engine.Commit("req_1", quota.Usage{Tokens: 8, Requests: 1})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if reservation.Status != quota.ReservationCommitted {
		t.Fatalf("expected committed reservation, got %#v", reservation)
	}
}

func TestEngineInvokeReservesAndCommits(t *testing.T) {
	engine, ledger := testEngine(t)
	engine.SetInvoker(fakeInvoker{})

	response, execution, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_1",
		RouteRequest: RouteRequest{
			Model:      "gpt-5-codex",
			APIDialect: compat.APIDialectOpenAI,
			Stream:     true,
		},
		QuotaScope:    quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{Tokens: 10, Requests: 1},
	}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-5.3-codex-spark",
		Messages: []compat.Message{
			{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if execution.Reservation.Status != quota.ReservationReserved {
		t.Fatalf("expected reserved execution, got %#v", execution.Reservation)
	}
	if response.Message.Content[0].Text != "ok" {
		t.Fatalf("expected fake response, got %#v", response)
	}
	_, committed, reserved, err := ledger.Snapshot(quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1", Model: "gpt-5.3-codex-spark"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if committed.Tokens != 3 || reserved.Tokens != 0 {
		t.Fatalf("expected committed usage and no reserved usage, committed=%#v reserved=%#v", committed, reserved)
	}
}

func testEngine(t *testing.T) (*Engine, *quota.Ledger) {
	t.Helper()
	registry := provider.NewRegistry()
	if err := registry.Upsert(registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0)); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	ledger := quota.NewLedger()
	if err := ledger.SetLimit(quota.Scope{
		TenantID: "team-a",
		UserID:   "usr_1",
		APIKeyID: "key_1",
		Model:    "gpt-5.3-codex-spark",
	}, quota.Limit{MaxTokens: 100, MaxRequests: 10}); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, ledger)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return engine, ledger
}

type fakeInvoker struct{}

func (fakeInvoker) Invoke(_ context.Context, _ provider.Registration, request compat.Request) (compat.Response, error) {
	return compat.Response{
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "ok"}},
		},
		Usage: compat.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}, nil
}
