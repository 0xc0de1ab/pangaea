package router

import (
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func TestUpdateProviderUsageRecordsQuotaWindowResetAuthEvent(t *testing.T) {
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	reg := registration("codex-samtest", "codex-cli", "codex-user@example.test", 10, 0)
	if err := engine.UpsertProvider(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}

	firstReportedAt := time.Date(2026, 5, 17, 4, 28, 9, 0, time.UTC)
	secondReportedAt := time.Date(2026, 5, 17, 0, 11, 14, 0, time.UTC).Add(9 * time.Hour)
	oldReset := time.Date(2026, 5, 18, 23, 51, 4, 0, time.UTC)
	newReset := time.Date(2026, 5, 24, 0, 10, 54, 0, time.UTC)

	if err := engine.UpdateProviderUsage(reg.Identity.ProviderInstanceID, usageWithWindow("Weekly limit", 44, oldReset), firstReportedAt); err != nil {
		t.Fatalf("first usage: %v", err)
	}
	if err := engine.UpdateProviderUsage(reg.Identity.ProviderInstanceID, usageWithWindow("Weekly limit", 100, newReset), secondReportedAt); err != nil {
		t.Fatalf("second usage: %v", err)
	}
	if err := engine.UpdateProviderUsage(reg.Identity.ProviderInstanceID, usageWithWindow("Weekly limit", 100, newReset), secondReportedAt.Add(time.Minute)); err != nil {
		t.Fatalf("duplicate usage: %v", err)
	}

	events := engine.AuthEvents("")
	resetEvents := []AuthEvent{}
	for _, event := range events {
		if event.Type == authEventUsageQuotaWindowReset {
			resetEvents = append(resetEvents, event)
		}
	}
	if len(resetEvents) != 1 {
		t.Fatalf("reset events = %d, want 1: %#v", len(resetEvents), resetEvents)
	}
	event := resetEvents[0]
	if event.AuthID == "" || event.Account.Display != "codex-user@example.test" || event.Service != provider.ServiceCodex {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if !strings.Contains(event.Message, "Weekly limit") || !strings.Contains(event.Message, "2026-05-24T00:10:54Z") {
		t.Fatalf("unexpected message: %q", event.Message)
	}
	if event.Details["previous_reset_at"] != "2026-05-18T23:51:04Z" || event.Details["new_reset_at"] != "2026-05-24T00:10:54Z" {
		t.Fatalf("unexpected details: %#v", event.Details)
	}
	if event.Details["previous_used_pct"] != "56.0" || event.Details["new_used_pct"] != "0.0" {
		t.Fatalf("unexpected usage details: %#v", event.Details)
	}
}

func TestEngineSnapshotAndRestoreUsageHistoryState(t *testing.T) {
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	reg := registration("codex-samtest", "codex-cli", "codex-user@example.test", 10, 0)
	if err := engine.UpsertProvider(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	oldReset := time.Date(2026, 5, 19, 8, 51, 4, 0, time.FixedZone("KST", 9*60*60))
	newReset := time.Date(2026, 5, 24, 9, 10, 54, 0, time.FixedZone("KST", 9*60*60))
	if err := engine.UpdateProviderUsage(reg.Identity.ProviderInstanceID, usageWithWindow("Weekly limit", 44, oldReset), time.Date(2026, 5, 17, 4, 28, 9, 0, time.UTC)); err != nil {
		t.Fatalf("first usage: %v", err)
	}
	if err := engine.UpdateProviderUsage(reg.Identity.ProviderInstanceID, usageWithWindow("Weekly limit", 100, newReset), time.Date(2026, 5, 17, 0, 11, 14, 0, time.UTC)); err != nil {
		t.Fatalf("second usage: %v", err)
	}
	snapshot := engine.SnapshotState(time.Date(2026, 5, 17, 0, 12, 0, 0, time.UTC))
	if len(snapshot.Usages) != 1 || len(snapshot.AuthEvents) != 1 {
		t.Fatalf("snapshot missing usage history: usages=%d auth_events=%d", len(snapshot.Usages), len(snapshot.AuthEvents))
	}

	restored, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new restored engine: %v", err)
	}
	restored.RestoreState(snapshot)
	if len(restored.ProviderUsages()) != 1 {
		t.Fatalf("provider usages were not restored: %#v", restored.ProviderUsages())
	}
	events := restored.AuthEvents("")
	if len(events) != 1 || events[0].Type != authEventUsageQuotaWindowReset {
		t.Fatalf("auth events were not restored: %#v", events)
	}
}

func TestRecordAuthRefreshResultIncludesRefreshMethodAndQuotaDetails(t *testing.T) {
	engine, err := NewEngine(validPolicy(), provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	reg := registration("codex-samtest", "codex-cli", "codex-user@example.test", 10, 0)
	if err := engine.UpsertProvider(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	reset := time.Date(2026, 5, 24, 0, 10, 54, 0, time.UTC)
	reportedAt := time.Date(2026, 5, 17, 0, 11, 14, 0, time.UTC)
	if err := engine.UpdateProviderUsage(reg.Identity.ProviderInstanceID, usageWithWindow("Weekly limit", 100, reset), reportedAt); err != nil {
		t.Fatalf("usage: %v", err)
	}

	engine.RecordAuthRefreshResult(control.AuthRefreshResult{
		RefreshID:          "refresh_manual",
		ProviderInstanceID: reg.Identity.ProviderInstanceID,
		Auth: provider.AuthState{
			Status:      provider.AuthHealthy,
			Refreshable: true,
			Account:     reg.Identity.Account,
		},
		OK:     true,
		Reason: "operator requested refresh",
		Metadata: control.AuthRefreshMetadata{
			Trigger:         "manual",
			Initiator:       "router.http",
			RequestMethod:   "api",
			ExecutionMethod: "command",
			Command:         "codex exec",
		},
		ReportedAt: reportedAt.Add(time.Minute),
	})

	events := engine.AuthEvents("")
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1: %#v", len(events), events)
	}
	event := events[0]
	if event.Type != "auth.refresh.result" || !strings.Contains(event.Message, "manual command") {
		t.Fatalf("unexpected refresh event: %#v", event)
	}
	for key, want := range map[string]string{
		"trigger":                 "manual",
		"initiator":               "router.http",
		"request_method":          "api",
		"execution_method":        "command",
		"command":                 "codex exec",
		"quota_window_1":          "Weekly limit",
		"quota_window_1_usage":    "0.0% used, 100.0% remaining",
		"quota_window_1_reset_at": "2026-05-24T00:10:54Z",
	} {
		if got := event.Details[key]; got != want {
			t.Fatalf("details[%q] = %q, want %q; details=%#v", key, got, want, event.Details)
		}
	}
}

func usageWithWindow(label string, remainingPct float64, resetAt time.Time) provider.UsageReport {
	return provider.UsageReport{
		ObservedAt: resetAt.Add(-time.Hour),
		Source:     "codex-auth-json-format/usage-probe",
		NativeSummary: formats.UsageReport{
			Windows: []formats.UsageWindow{{
				Label:        label,
				RemainingPct: remainingPct,
				Unit:         "7d window",
				ResetAt:      resetAt.UTC(),
			}},
		},
	}
}
