package router

import (
	"context"
	"errors"
	"strings"
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

func TestEngineDryRunFiltersUnavailableDataSessions(t *testing.T) {
	registry := provider.NewRegistry()
	missing := registration("codex-nullcode-a1", "codex-cli", "nullcode@gmail.com", 50, 0)
	available := registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0)
	if err := registry.Upsert(missing); err != nil {
		t.Fatalf("upsert missing: %v", err)
	}
	if err := registry.Upsert(available); err != nil {
		t.Fatalf("upsert available: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.SetInvoker(availabilityInvoker{
		available: map[string]bool{available.Identity.ProviderInstanceID: true},
	})

	decision := engine.DryRun(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	})
	if !decision.Allowed || decision.Selected != available.Identity.ProviderInstanceID {
		t.Fatalf("expected available data session selected, got %#v", decision)
	}
	foundMissingRejection := false
	for _, rejection := range decision.Rejections {
		if rejection.ProviderInstanceID == missing.Identity.ProviderInstanceID && strings.Contains(rejection.Reason, "data session disconnected") {
			foundMissingRejection = true
		}
	}
	if !foundMissingRejection {
		t.Fatalf("expected missing data session rejection, got %#v", decision.Rejections)
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

func TestEngineInvokeRecordsRequestTrace(t *testing.T) {
	engine, _ := testEngine(t)
	engine.SetInvoker(fakeInvoker{})

	_, _, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_trace_1",
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
	trace, ok := engine.RequestTrace("req_trace_1")
	if !ok {
		t.Fatalf("expected trace")
	}
	if trace.Status != "completed" || trace.Provider == nil || trace.Provider.ProviderInstanceID != "codex-samtest-a1" {
		t.Fatalf("unexpected trace: %#v", trace)
	}
	if trace.Reservation.Status != quota.ReservationCommitted {
		t.Fatalf("expected committed trace reservation, got %#v", trace.Reservation)
	}
	if trace.ActualUsage.Tokens != 3 || trace.EstimatedUsage.Tokens != 10 {
		t.Fatalf("unexpected trace usage: %#v", trace)
	}
	traces := engine.RequestTraces(1)
	if len(traces) != 1 || traces[0].RequestID != "req_trace_1" {
		t.Fatalf("unexpected traces list: %#v", traces)
	}
}

func TestEngineInvokeStreamUsesStreamInvokerAndCommits(t *testing.T) {
	engine, ledger := testEngine(t)
	engine.SetInvoker(fakeStreamInvoker{})
	events := []compat.Event{}

	response, execution, err := engine.InvokeStream(context.Background(), RouteExecutionRequest{
		RequestID: "req_stream_engine_1",
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
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if len(events) == 0 || events[0].Type != compat.EventMessageStart {
		t.Fatalf("expected stream events, got %#v", events)
	}
	if response.Message.Content[0].Text != "stream ok" || execution.Decision.Selected != "codex-samtest-a1" {
		t.Fatalf("unexpected stream response/execution: response=%#v execution=%#v", response, execution)
	}
	_, committed, reserved, err := ledger.Snapshot(quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1", Model: "gpt-5.3-codex-spark"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if committed.Tokens != 5 || reserved.Tokens != 0 {
		t.Fatalf("expected committed stream usage and no reserved usage, committed=%#v reserved=%#v", committed, reserved)
	}
}

func TestEngineInvokeFallsBackAfterProviderFailure(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-nullcode-a1", "codex-cli", "nullcode@gmail.com", 50, 0)
	second := registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0)
	if err := registry.Upsert(first); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	if err := registry.Upsert(second); err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	ledger := quota.NewLedger()
	engine, err := NewEngine(validPolicy(), registry, ledger)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.SetInvoker(fallbackInvoker{failProviderInstanceID: first.Identity.ProviderInstanceID})

	response, execution, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_fallback_1",
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
	if execution.Decision.Selected != second.Identity.ProviderInstanceID {
		t.Fatalf("expected fallback provider selected, got %#v", execution.Decision)
	}
	if len(execution.Decision.Rejections) == 0 {
		t.Fatalf("expected invoke rejection recorded")
	}
	if response.Message.Content[0].Text != "ok from "+second.Identity.ProviderInstanceID {
		t.Fatalf("unexpected response: %#v", response)
	}
	trace, ok := engine.RequestTrace("req_fallback_1")
	if !ok {
		t.Fatalf("expected trace")
	}
	if trace.Provider == nil || trace.Provider.ProviderInstanceID != second.Identity.ProviderInstanceID {
		t.Fatalf("trace did not record fallback provider: %#v", trace)
	}
	if len(trace.Decision.Rejections) == 0 {
		t.Fatalf("trace did not include failed provider rejection: %#v", trace)
	}
}

func TestEngineInvokeMarksMissingDataSessionProviderDown(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-nullcode-a1", "codex-cli", "nullcode@gmail.com", 50, 0)
	second := registration("codex-samtest-a1", "codex-cli", "samtest4u@gmail.com", 10, 0)
	if err := registry.Upsert(first); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	if err := registry.Upsert(second); err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.SetInvoker(missingDataSessionFallbackInvoker{failProviderInstanceID: first.Identity.ProviderInstanceID})

	_, execution, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_missing_data_session_1",
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
	if execution.Decision.Selected != second.Identity.ProviderInstanceID {
		t.Fatalf("expected fallback provider selected, got %#v", execution.Decision)
	}
	updated, ok := registry.Get(first.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("missing first provider")
	}
	if updated.Health.Status != provider.HealthDown || updated.Health.Reason != "data session disconnected" {
		t.Fatalf("missing data session provider was not marked down: %#v", updated.Health)
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

type fakeStreamInvoker struct{}

func (fakeStreamInvoker) Invoke(_ context.Context, _ provider.Registration, request compat.Request) (compat.Response, error) {
	return fakeInvoker{}.Invoke(context.Background(), provider.Registration{}, request)
}

func (fakeStreamInvoker) InvokeStream(_ context.Context, _ provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	response := compat.Response{
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "stream ok"}},
		},
		StopReason: "stop",
		Usage:      compat.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
	}
	events, err := compat.EventsFromResponse(response)
	if err != nil {
		return compat.Response{}, err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return compat.Response{}, err
		}
	}
	return response, nil
}

type fallbackInvoker struct {
	failProviderInstanceID string
}

func (f fallbackInvoker) Invoke(_ context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if registration.Identity.ProviderInstanceID == f.failProviderInstanceID {
		return compat.Response{}, errors.New("simulated provider failure")
	}
	return compat.Response{
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "ok from " + registration.Identity.ProviderInstanceID}},
		},
		Usage: compat.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}, nil
}

type missingDataSessionFallbackInvoker struct {
	failProviderInstanceID string
}

func (f missingDataSessionFallbackInvoker) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if registration.Identity.ProviderInstanceID == f.failProviderInstanceID {
		return compat.Response{}, ErrNoDataSession
	}
	return fallbackInvoker{}.Invoke(ctx, registration, request)
}

type availabilityInvoker struct {
	available map[string]bool
}

func (i availabilityInvoker) ProviderAvailable(providerInstanceID string) bool {
	return i.available[providerInstanceID]
}

func (i availabilityInvoker) Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error) {
	return compat.Response{}, errors.New("invoke should not be called")
}
