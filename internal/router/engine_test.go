package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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
	missing := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	available := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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

func TestEngineDryRunUsesLiveProviderQueueDepth(t *testing.T) {
	registry := provider.NewRegistry()
	busy := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	ready := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
	if err := registry.Upsert(busy); err != nil {
		t.Fatalf("upsert busy: %v", err)
	}
	if err := registry.Upsert(ready); err != nil {
		t.Fatalf("upsert ready: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.SetInvoker(availabilityInvoker{
		available: map[string]bool{
			busy.Identity.ProviderInstanceID:  true,
			ready.Identity.ProviderInstanceID: true,
		},
		queueDepth: map[string]int{busy.Identity.ProviderInstanceID: 9},
	})

	decision := engine.DryRun(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	})
	if !decision.Allowed || decision.Selected != ready.Identity.ProviderInstanceID {
		t.Fatalf("expected lower-weight ready provider selected, got %#v", decision)
	}
	foundBusyRejection := false
	for _, rejection := range decision.Rejections {
		if rejection.ProviderInstanceID == busy.Identity.ProviderInstanceID && strings.Contains(rejection.Reason, "queue_depth 9") {
			foundBusyRejection = true
		}
	}
	if !foundBusyRejection {
		t.Fatalf("expected busy provider queue-depth rejection, got %#v", decision.Rejections)
	}
}

func TestEngineDryRunAllowsStaleRateLimitedProvider(t *testing.T) {
	registry := provider.NewRegistry()
	reg := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
	reg.Health = provider.Health{
		Status:    provider.HealthDegraded,
		Reason:    "upstream rate limited",
		CheckedAt: time.Now().Add(-2 * time.Minute),
	}
	if err := registry.Upsert(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	decision := engine.DryRun(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
	})
	if !decision.Allowed || decision.Selected != reg.Identity.ProviderInstanceID {
		t.Fatalf("expected stale rate-limited provider to be routable, got %#v", decision)
	}
}

func TestEngineProvidersRecoverStaleRateLimitedProvider(t *testing.T) {
	registry := provider.NewRegistry()
	reg := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
	reg.Health = provider.Health{
		Status:    provider.HealthDegraded,
		Reason:    "upstream rate limited",
		CheckedAt: time.Now().Add(-2 * time.Minute),
	}
	if err := registry.Upsert(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	providers := engine.Providers()
	if len(providers) != 1 {
		t.Fatalf("expected one provider, got %#v", providers)
	}
	if providers[0].Health.Status != provider.HealthReady || providers[0].Health.Reason != "" {
		t.Fatalf("expected provider snapshot to recover stale rate-limit health, got %#v", providers[0].Health)
	}
	raw, ok := registry.Get(reg.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("missing raw provider")
	}
	if raw.Health.Status != provider.HealthDegraded || raw.Health.Reason != "upstream rate limited" {
		t.Fatalf("provider snapshot recovery should not mutate registry, got %#v", raw.Health)
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
	if trace.Status != "completed" || trace.Provider == nil || trace.Provider.ProviderInstanceID != "codex-primary-a1" {
		t.Fatalf("unexpected trace: %#v", trace)
	}
	if trace.Reservation.Status != quota.ReservationCommitted {
		t.Fatalf("expected committed trace reservation, got %#v", trace.Reservation)
	}
	if trace.ActualUsage.Tokens != 3 || trace.EstimatedUsage.Tokens != 10 {
		t.Fatalf("unexpected trace usage: %#v", trace)
	}
	if len(trace.Decision.Scores) == 0 || trace.Decision.Scores[0].ProviderInstanceID == "" || trace.Decision.Scores[0].Reason == "" {
		t.Fatalf("trace missing routing score explanation: %#v", trace.Decision.Scores)
	}
	traces := engine.RequestTraces(1)
	if len(traces) != 1 || traces[0].RequestID != "req_trace_1" {
		t.Fatalf("unexpected traces list: %#v", traces)
	}
}

func TestEngineRequestTracesPageAndDelete(t *testing.T) {
	engine, _ := testEngine(t)
	base := time.Now().UTC()
	for i := 1; i <= 3; i++ {
		engine.recordRequestTrace(RequestTrace{
			RequestID:   fmt.Sprintf("req_page_%d", i),
			Status:      "completed",
			StartedAt:   base.Add(time.Duration(i) * time.Second),
			CompletedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	page := engine.RequestTracesPage(1, 1)
	if page.Total != 3 || page.Offset != 1 || page.Limit != 1 || !page.HasMore {
		t.Fatalf("unexpected page metadata: %#v", page)
	}
	if len(page.Traces) != 1 || page.Traces[0].RequestID != "req_page_2" {
		t.Fatalf("unexpected page traces: %#v", page.Traces)
	}

	deleted := engine.DeleteRequestTraces([]string{"req_page_2", "missing", "req_page_2"})
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, ok := engine.RequestTrace("req_page_2"); ok {
		t.Fatal("deleted trace still present")
	}
	page = engine.RequestTracesPage(0, 10)
	if page.Total != 2 || len(page.Traces) != 2 || page.Traces[0].RequestID != "req_page_3" || page.Traces[1].RequestID != "req_page_1" {
		t.Fatalf("unexpected traces after delete: %#v", page)
	}
}

func TestEngineInvokeTraceRecordsUpstreamErrorMetadata(t *testing.T) {
	engine, _ := testEngine(t)
	engine.SetInvoker(upstreamErrorFallbackInvoker{
		failProviderInstanceID: "codex-primary-a1",
		err: &provider.UpstreamError{
			StatusCode: http.StatusTooManyRequests,
			Code:       "rate_limit_exceeded",
			Message:    "upstream rate limited",
			RetryAfter: "13",
		},
	})

	_, _, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_trace_upstream_1",
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
	if err == nil {
		t.Fatalf("expected upstream error")
	}
	trace, ok := engine.RequestTrace("req_trace_upstream_1")
	if !ok {
		t.Fatalf("expected trace")
	}
	if trace.Status != "provider_error" || trace.ErrorCode != "rate_limit_exceeded" || trace.ErrorStatus != http.StatusTooManyRequests || trace.RetryAfter != "13" {
		t.Fatalf("unexpected upstream trace metadata: %#v", trace)
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
	if response.Message.Content[0].Text != "stream ok" || execution.Decision.Selected != "codex-primary-a1" {
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

func TestEngineDryRunRecoversStaleTransientDegradedProvider(t *testing.T) {
	registry := provider.NewRegistry()
	stale := registration("minimax-api", "codex-cli", "primary@example.test", 10, 0)
	stale.Health = provider.Health{
		Status:    provider.HealthDegraded,
		Reason:    "provider invoke failed",
		CheckedAt: time.Now().Add(-2 * staleTransientDegradedAfter),
	}
	if err := registry.Upsert(stale); err != nil {
		t.Fatalf("upsert stale: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.SetInvoker(availabilityInvoker{
		available: map[string]bool{stale.Identity.ProviderInstanceID: true},
	})

	decision := engine.DryRun(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	})
	if !decision.Allowed || decision.Selected != stale.Identity.ProviderInstanceID {
		t.Fatalf("expected stale transient degradation to recover for routing, got %#v", decision)
	}
	providers := engine.Providers()
	if len(providers) != 1 || providers[0].Health.Status != provider.HealthReady || providers[0].Health.Reason != "" {
		t.Fatalf("expected provider list to show recovered health, got %#v", providers)
	}
}

func TestEngineDryRunDoesNotRecoverFreshTransientDegradedProvider(t *testing.T) {
	registry := provider.NewRegistry()
	fresh := registration("minimax-api", "codex-cli", "primary@example.test", 10, 0)
	fresh.Health = provider.Health{
		Status:    provider.HealthDegraded,
		Reason:    "provider invoke failed",
		CheckedAt: time.Now(),
	}
	if err := registry.Upsert(fresh); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	engine.SetInvoker(availabilityInvoker{
		available: map[string]bool{fresh.Identity.ProviderInstanceID: true},
	})

	decision := engine.DryRun(RouteRequest{
		Model:      "gpt-5-codex",
		APIDialect: compat.APIDialectOpenAI,
		Stream:     true,
	})
	if decision.Allowed {
		t.Fatalf("expected fresh transient degradation to stay excluded, got %#v", decision)
	}
}

func TestEngineInvokeFallsBackAfterProviderFailure(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	second := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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
	first := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	second := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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

func TestEngineInvokeMarksUpstreamAuthFailureUnavailable(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	second := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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
	engine.SetInvoker(upstreamErrorFallbackInvoker{
		failProviderInstanceID: first.Identity.ProviderInstanceID,
		err: &provider.UpstreamError{
			StatusCode: 401,
			Code:       "invalid_api_key",
			Message:    "upstream rejected provider auth",
		},
	})

	_, execution, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_upstream_auth_1",
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
	if updated.Auth.Status != provider.AuthUnavailable || !strings.Contains(updated.Auth.LastRefreshErr, "invalid_api_key") {
		t.Fatalf("upstream auth failure did not update provider auth: %#v", updated.Auth)
	}
	if updated.Health.Status != provider.HealthDown || updated.Health.Reason != "upstream auth failed" {
		t.Fatalf("upstream auth failure did not update provider health: %#v", updated.Health)
	}
}

func TestEngineInvokeMarksUpstreamRateLimitDegraded(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	second := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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
	engine.SetInvoker(upstreamErrorFallbackInvoker{
		failProviderInstanceID: first.Identity.ProviderInstanceID,
		err: &provider.UpstreamError{
			StatusCode: 429,
			Code:       "rate_limit_exceeded",
			Message:    "upstream rate limited",
		},
	})

	_, _, err = engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_upstream_rate_1",
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
	updated, ok := registry.Get(first.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("missing first provider")
	}
	if updated.Health.Status != provider.HealthDegraded || updated.Health.Reason != "upstream rate limited" {
		t.Fatalf("upstream rate limit did not degrade provider health: %#v", updated.Health)
	}
}

func TestEngineInvokeDoesNotDegradeProviderForModelScopedCapacityError(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	second := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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
	engine.SetInvoker(upstreamErrorFallbackInvoker{
		failProviderInstanceID: first.Identity.ProviderInstanceID,
		err: &provider.UpstreamError{
			StatusCode: 429,
			Code:       "429",
			Message:    "No capacity available for model gemini-2.5-pro on the server",
		},
	})

	_, execution, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_model_capacity_1",
		RouteRequest: RouteRequest{
			Model:      "gpt-5-codex",
			APIDialect: compat.APIDialectOpenAI,
			Stream:     true,
		},
		QuotaScope:    quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{Tokens: 10, Requests: 1},
	}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "gemini-2.5-pro",
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
	if updated.Health.Status != provider.HealthReady {
		t.Fatalf("model-scoped capacity error degraded provider health: %#v", updated.Health)
	}
}

func TestIsModelScopedCapacityErrorRecognizesGeminiQuotaMessage(t *testing.T) {
	err := &provider.UpstreamError{
		StatusCode: 429,
		Code:       "429",
		Message:    "You have exhausted your capacity on this model. Your quota will reset after 0s.",
	}
	if !isModelScopedCapacityError(err) {
		t.Fatalf("expected Gemini per-model quota message to be model scoped")
	}
}

func TestIsModelScopedCapacityErrorRecognizesStatuslessResourceExhaustedMessage(t *testing.T) {
	err := &provider.UpstreamError{
		Code:    "rate_limit_exceeded",
		Message: "RESOURCE_EXHAUSTED (code 429): You have exhausted your capacity on this model. Your quota will reset after 47h19m38s.",
	}
	if !isModelScopedCapacityError(err) {
		t.Fatalf("expected statusless AG per-model quota message to be model scoped")
	}
}

func TestIsEmptyStreamInvokeError(t *testing.T) {
	err := &provider.UpstreamError{
		StatusCode: http.StatusGatewayTimeout,
		Code:       "empty_stream_timeout",
		Message:    "upstream stream did not produce assistant content within 1m30s",
	}
	if !isEmptyStreamInvokeError(err) {
		t.Fatalf("expected empty stream timeout to be retryable at route-model level")
	}
}

func TestEngineInvokeFallsBackToNextRoutingRuleModelOnCapacityError(t *testing.T) {
	registry := provider.NewRegistry()
	ag := registration("ag-a2", "antigravity-sidecar", "sam@example.test", 1, 0)
	ag.Identity.Service = provider.ServiceAntigravity
	ag.Models = []provider.Model{
		{
			ID:           "claude-sonnet-4-6",
			Aliases:      []string{"Claude Sonnet 4.6 (Thinking)"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
			Quota:        &provider.ModelQuota{RemainingPct: 30, ResetAt: time.Now().UTC().Add(2 * time.Hour)},
		},
		{
			ID:           "gemini-3-flash-agent",
			Aliases:      []string{"Gemini 3 Flash"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
			Quota:        &provider.ModelQuota{RemainingPct: 90, ResetAt: time.Now().UTC().Add(30 * time.Minute)},
		},
	}
	if err := registry.Upsert(ag); err != nil {
		t.Fatalf("register antigravity: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	invoker := &modelCapacityFallbackInvoker{failModels: map[string]bool{"claude-sonnet-4-6": true}}
	engine.SetInvoker(invoker)
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:  "antigravity-sonnet-gemini",
		Scope: RoutingRuleScopePublic,
		Filters: []RoutingFilter{
			{
				Type:  "criteria",
				Label: "Claude",
				Criteria: RoutingFilterCriteria{
					Services:    []provider.Service{provider.ServiceAntigravity},
					Models:      []string{"Claude Sonnet 4.6 (Thinking)"},
					APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
				},
			},
			{
				Type:  "criteria",
				Label: "Gemini",
				Criteria: RoutingFilterCriteria{
					Services:    []provider.Service{provider.ServiceAntigravity},
					Models:      []string{"Gemini 3 Flash"},
					APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}

	response, execution, err := engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_ag_model_fallback_1",
		RouteRequest: RouteRequest{
			RoutingRuleName: rule.Name,
			APIDialect:      compat.APIDialectOpenAI,
		},
		QuotaScope:    quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{Tokens: 10, Requests: 1},
	}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Messages: []compat.Message{
			{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("invoke should switch to Gemini after Sonnet capacity error: %v", err)
	}
	if response.Model != "gemini-3-flash-agent" || execution.Decision.CanonicalModel != "gemini-3-flash-agent" {
		t.Fatalf("expected Gemini model selected, response=%#v decision=%#v", response, execution.Decision)
	}
	if got := strings.Join(invoker.calls, ","); got != "claude-sonnet-4-6,gemini-3-flash-agent" {
		t.Fatalf("expected Sonnet then Gemini calls, got %q", got)
	}
	if !routeDecisionHasEvent(execution.Decision, routeDecisionEventModelFallback) {
		t.Fatalf("expected model fallback event, got %#v", execution.Decision.Events)
	}
	trace, ok := engine.RequestTrace("req_ag_model_fallback_1")
	if !ok || trace.Decision.CanonicalModel != "gemini-3-flash-agent" || !routeDecisionHasEvent(trace.Decision, routeDecisionEventModelFallback) {
		t.Fatalf("expected completed trace with Gemini fallback, trace=%#v ok=%v", trace, ok)
	}
}

func TestEngineInvokeStreamFallsBackToNextRoutingRuleModelBeforeEmitting(t *testing.T) {
	registry := provider.NewRegistry()
	ag := registration("ag-a2", "antigravity-sidecar", "sam@example.test", 1, 0)
	ag.Identity.Service = provider.ServiceAntigravity
	ag.Models = []provider.Model{
		{ID: "claude-sonnet-4-6", Aliases: []string{"Claude Sonnet 4.6 (Thinking)"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
		{ID: "gemini-3-flash-agent", Aliases: []string{"Gemini 3 Flash"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
	}
	if err := registry.Upsert(ag); err != nil {
		t.Fatalf("register antigravity: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	invoker := &modelCapacityFallbackInvoker{failModels: map[string]bool{"claude-sonnet-4-6": true}}
	engine.SetInvoker(invoker)
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:  "antigravity-sonnet-gemini",
		Scope: RoutingRuleScopePublic,
		Filters: []RoutingFilter{{
			Type: "criteria",
			Criteria: RoutingFilterCriteria{
				Services:    []provider.Service{provider.ServiceAntigravity},
				Models:      []string{"Claude Sonnet 4.6 (Thinking)", "Gemini 3 Flash"},
				APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
			},
		}},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}

	var events []compat.Event
	response, execution, err := engine.InvokeStream(context.Background(), RouteExecutionRequest{
		RequestID: "req_ag_model_fallback_stream_1",
		RouteRequest: RouteRequest{
			RoutingRuleName: rule.Name,
			APIDialect:      compat.APIDialectOpenAI,
			Stream:          true,
		},
		QuotaScope:    quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{Tokens: 10, Requests: 1},
	}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Messages: []compat.Message{
			{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}}},
		},
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream invoke should switch to Gemini after Sonnet capacity error: %v", err)
	}
	if response.Model != "gemini-3-flash-agent" || execution.Decision.CanonicalModel != "gemini-3-flash-agent" {
		t.Fatalf("expected Gemini model selected, response=%#v decision=%#v", response, execution.Decision)
	}
	if got := strings.Join(invoker.calls, ","); got != "claude-sonnet-4-6,gemini-3-flash-agent" {
		t.Fatalf("expected Sonnet then Gemini calls, got %q", got)
	}
	if len(events) == 0 {
		t.Fatalf("expected Gemini stream events")
	}
	if !routeDecisionHasEvent(execution.Decision, routeDecisionEventModelFallback) {
		t.Fatalf("expected model fallback event, got %#v", execution.Decision.Events)
	}
}

func TestEngineInvokeStreamFallsBackToNextRoutingRuleModelOnEmptyStreamTimeout(t *testing.T) {
	registry := provider.NewRegistry()
	ag := registration("ag-a2", "antigravity-sidecar", "sam@example.test", 1, 0)
	ag.Identity.Service = provider.ServiceAntigravity
	ag.Models = []provider.Model{
		{ID: "gemini-3-flash-agent", Aliases: []string{"Gemini 3 Flash"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
		{ID: "gemini-3.1-pro-low", Aliases: []string{"Gemini 3.1 Pro (Low)"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
	}
	if err := registry.Upsert(ag); err != nil {
		t.Fatalf("register antigravity: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	invoker := &modelCapacityFallbackInvoker{emptyStreamModels: map[string]bool{"gemini-3-flash-agent": true}}
	engine.SetInvoker(invoker)
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:  "antigravity-gemini",
		Scope: RoutingRuleScopePublic,
		Filters: []RoutingFilter{{
			Type: "criteria",
			Criteria: RoutingFilterCriteria{
				Services:    []provider.Service{provider.ServiceAntigravity},
				Models:      []string{"Gemini 3 Flash", "Gemini 3.1 Pro (Low)"},
				APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
			},
		}},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}

	var events []compat.Event
	response, execution, err := engine.InvokeStream(context.Background(), RouteExecutionRequest{
		RequestID: "req_ag_empty_stream_model_fallback_1",
		RouteRequest: RouteRequest{
			RoutingRuleName: rule.Name,
			APIDialect:      compat.APIDialectOpenAI,
			Stream:          true,
		},
		QuotaScope:    quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{Tokens: 10, Requests: 1},
	}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Messages: []compat.Message{
			{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}}},
		},
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream invoke should switch to next Gemini model after empty stream timeout: %v", err)
	}
	if response.Model != "gemini-3.1-pro-low" || execution.Decision.CanonicalModel != "gemini-3.1-pro-low" {
		t.Fatalf("expected next Gemini model selected, response=%#v decision=%#v", response, execution.Decision)
	}
	if got := strings.Join(invoker.calls, ","); got != "gemini-3-flash-agent,gemini-3.1-pro-low" {
		t.Fatalf("expected Flash then Pro Low calls, got %q", got)
	}
	if len(events) == 0 {
		t.Fatalf("expected fallback stream events")
	}
	if !routeDecisionHasEvent(execution.Decision, routeDecisionEventModelEmptyStreamFallback) {
		t.Fatalf("expected empty-stream model fallback event, got %#v", execution.Decision.Events)
	}
}

func TestEngineInvokeStreamFallsBackToNextRoutingRuleModelOnMalformedToolCall(t *testing.T) {
	registry := provider.NewRegistry()
	ag := registration("ag-a2", "antigravity-sidecar", "sam@example.test", 1, 0)
	ag.Identity.Service = provider.ServiceAntigravity
	ag.Models = []provider.Model{
		{ID: "claude-sonnet-4-6", Aliases: []string{"Claude Sonnet 4.6 (Thinking)"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
		{ID: "gemini-3-flash-agent", Aliases: []string{"Gemini 3 Flash"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
	}
	if err := registry.Upsert(ag); err != nil {
		t.Fatalf("register antigravity: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	invoker := &modelCapacityFallbackInvoker{malformedToolCallModels: map[string]bool{"claude-sonnet-4-6": true}}
	engine.SetInvoker(invoker)
	rule, err := engine.UpsertRoutingRule(RoutingRule{
		Name:  "antigravity-sonnet-gemini",
		Scope: RoutingRuleScopePublic,
		Filters: []RoutingFilter{
			{
				Type: "criteria",
				Criteria: RoutingFilterCriteria{
					Services:    []provider.Service{provider.ServiceAntigravity},
					Models:      []string{"Claude Sonnet 4.6 (Thinking)"},
					APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
				},
			},
			{
				Type: "criteria",
				Criteria: RoutingFilterCriteria{
					Services:    []provider.Service{provider.ServiceAntigravity},
					Models:      []string{"Gemini 3 Flash"},
					APIDialects: []compat.APIDialect{compat.APIDialectOpenAI},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("upsert rule: %v", err)
	}

	var events []compat.Event
	response, execution, err := engine.InvokeStream(context.Background(), RouteExecutionRequest{
		RequestID: "req_ag_malformed_tool_call_fallback_1",
		RouteRequest: RouteRequest{
			RoutingRuleName: rule.Name,
			APIDialect:      compat.APIDialectOpenAI,
			Stream:          true,
		},
		QuotaScope:    quota.Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1"},
		QuotaEstimate: quota.Usage{Tokens: 10, Requests: 1},
	}, compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Messages: []compat.Message{
			{Role: compat.MessageRoleUser, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}}},
		},
	}, func(event compat.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream invoke should switch to Gemini after malformed tool call: %v", err)
	}
	if response.Model != "gemini-3-flash-agent" || execution.Decision.CanonicalModel != "gemini-3-flash-agent" {
		t.Fatalf("expected Gemini model selected, response=%#v decision=%#v", response, execution.Decision)
	}
	if got := strings.Join(invoker.calls, ","); got != "claude-sonnet-4-6,gemini-3-flash-agent" {
		t.Fatalf("expected Sonnet then Gemini calls, got %q", got)
	}
	if len(events) == 0 {
		t.Fatalf("expected fallback stream events")
	}
	if !routeDecisionHasEvent(execution.Decision, routeDecisionEventModelToolCallFallback) {
		t.Fatalf("expected tool-call model fallback event, got %#v", execution.Decision.Events)
	}
}

func TestEngineInvokeDoesNotDegradeProviderForUpstreamClientModelError(t *testing.T) {
	registry := provider.NewRegistry()
	first := registration("codex-secondary-a1", "codex-cli", "secondary@example.test", 50, 0)
	second := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
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
	engine.SetInvoker(upstreamErrorFallbackInvoker{
		failProviderInstanceID: first.Identity.ProviderInstanceID,
		err: &provider.UpstreamError{
			StatusCode: http.StatusNotFound,
			Code:       "not_found",
			Message:    "model not found",
		},
	})

	_, _, err = engine.Invoke(context.Background(), RouteExecutionRequest{
		RequestID: "req_upstream_model_404",
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
		t.Fatalf("invoke should fall back to second provider: %v", err)
	}
	updated, ok := registry.Get(first.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("missing first provider")
	}
	if updated.Health.Status != provider.HealthReady || updated.Health.Reason != "" {
		t.Fatalf("client model error should not degrade provider health: %#v", updated.Health)
	}
}

func TestEngineRoutingRuleSkipsProviderWithExhaustedModelQuota(t *testing.T) {
	registry := provider.NewRegistry()
	exhausted := registration("codex-exhausted", "codex-cli", "exhausted@example.test", 10, 0)
	exhausted.Models = []provider.Model{{
		ID:           "gpt-5-codex",
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Quota:        &provider.ModelQuota{RemainingPct: 0},
	}}
	available := registration("codex-available", "codex-cli", "available@example.test", 10, 0)
	available.Models = []provider.Model{{
		ID:           "gpt-5-codex",
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Quota:        &provider.ModelQuota{RemainingPct: 25},
	}}
	if err := registry.Upsert(exhausted); err != nil {
		t.Fatalf("upsert exhausted: %v", err)
	}
	if err := registry.Upsert(available); err != nil {
		t.Fatalf("upsert available: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := engine.UpsertRoutingRule(RoutingRule{
		Name:       "quota-aware",
		Scope:      RoutingRuleScopeUser,
		OwnerEmail: "user@example.test",
		Filters:    []RoutingFilter{{Type: "criteria", Label: "Codex", Criteria: RoutingFilterCriteria{Services: []provider.Service{provider.ServiceCodex}}}},
	}); err != nil {
		t.Fatalf("upsert rule: %v", err)
	}

	decision := engine.DryRun(RouteRequest{
		UserID:          "user@example.test",
		RoutingRuleName: "quota-aware",
		Model:           "gpt-5-codex",
		APIDialect:      compat.APIDialectOpenAI,
	})
	if !decision.Allowed || decision.Selected != "codex-available" {
		t.Fatalf("expected available provider selected, got %#v", decision)
	}
	if len(decision.Rejections) == 0 || !strings.Contains(decision.Rejections[0].Reason, "quota exhausted") {
		t.Fatalf("expected exhausted provider rejection, got %#v", decision.Rejections)
	}
}

func testEngine(t *testing.T) (*Engine, *quota.Ledger) {
	t.Helper()
	registry := provider.NewRegistry()
	if err := registry.Upsert(registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)); err != nil {
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

type upstreamErrorFallbackInvoker struct {
	failProviderInstanceID string
	err                    error
}

func (f upstreamErrorFallbackInvoker) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if registration.Identity.ProviderInstanceID == f.failProviderInstanceID {
		return compat.Response{}, f.err
	}
	return fallbackInvoker{}.Invoke(ctx, registration, request)
}

type modelCapacityFallbackInvoker struct {
	failModels              map[string]bool
	emptyStreamModels       map[string]bool
	malformedToolCallModels map[string]bool
	calls                   []string
}

func (f *modelCapacityFallbackInvoker) Invoke(_ context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	f.calls = append(f.calls, request.Model)
	if f.malformedToolCallModels[request.Model] {
		return compat.Response{}, &provider.UpstreamError{
			StatusCode: http.StatusBadGateway,
			Code:       "malformed_tool_call",
			Message:    "upstream emitted an incomplete tool call; tool protocol text was not returned as assistant content",
		}
	}
	if f.failModels[request.Model] {
		return compat.Response{}, &provider.UpstreamError{
			StatusCode: 429,
			Code:       "RESOURCE_EXHAUSTED",
			Message:    "You have exhausted your capacity on this model. Your quota will reset after 2h10m1s.",
		}
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

func (f *modelCapacityFallbackInvoker) InvokeStream(_ context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	f.calls = append(f.calls, request.Model)
	if f.malformedToolCallModels[request.Model] {
		_ = emit(compat.Event{
			Dialect: request.Dialect,
			Model:   request.Model,
			Type:    compat.EventError,
			Error: &compat.EventErrorPayload{
				Code:    "malformed_tool_call",
				Message: "upstream emitted an incomplete tool call; tool protocol text was not returned as assistant content",
			},
		})
		return compat.Response{}, &provider.UpstreamError{
			StatusCode: http.StatusBadGateway,
			Code:       "malformed_tool_call",
			Message:    "upstream emitted an incomplete tool call; tool protocol text was not returned as assistant content",
		}
	}
	if f.emptyStreamModels[request.Model] {
		return compat.Response{}, &provider.UpstreamError{
			StatusCode: http.StatusGatewayTimeout,
			Code:       "empty_stream_timeout",
			Message:    "upstream stream did not produce assistant content within 1m30s",
		}
	}
	if f.failModels[request.Model] {
		_ = emit(compat.Event{
			Dialect: request.Dialect,
			Model:   request.Model,
			Type:    compat.EventError,
			Error: &compat.EventErrorPayload{
				Code:    "RESOURCE_EXHAUSTED",
				Message: "You have exhausted your capacity on this model. Your quota will reset after 2h10m1s.",
			},
		})
		return compat.Response{}, &provider.UpstreamError{
			StatusCode: 429,
			Code:       "RESOURCE_EXHAUSTED",
			Message:    "You have exhausted your capacity on this model. Your quota will reset after 2h10m1s.",
		}
	}
	response := compat.Response{
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "stream ok from " + registration.Identity.ProviderInstanceID}},
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

func routeDecisionHasEvent(decision RouteDecision, eventType string) bool {
	for _, event := range decision.Events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

type availabilityInvoker struct {
	available  map[string]bool
	queueDepth map[string]int
}

func (i availabilityInvoker) ProviderAvailable(providerInstanceID string) bool {
	return i.available[providerInstanceID]
}

func (i availabilityInvoker) ProviderQueueDepth(providerInstanceID string) int {
	return i.queueDepth[providerInstanceID]
}

func (i availabilityInvoker) Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error) {
	return compat.Response{}, errors.New("invoke should not be called")
}
