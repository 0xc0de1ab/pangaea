package claudecreds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbe_MaxPlanMatchesUsageTab(t *testing.T) {
	var gotAuth string
	var gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 7, "resets_at": "2026-04-26T03:30:00Z"},
			"seven_day": {"utilization": 31, "resets_at": "2026-04-29T07:00:00Z"},
			"seven_day_sonnet": {"utilization": 0, "resets_at": "2026-04-29T07:00:00Z"},
			"extra_usage": {"is_enabled": true, "monthly_limit": 5000, "used_credits": 258, "utilization": 5.16}
		}`))
	}))
	defer srv.Close()
	saved := AnthropicUsageEndpoint
	AnthropicUsageEndpoint = srv.URL
	defer func() { AnthropicUsageEndpoint = saved }()

	raw := []byte(`{
		"claudeAiOauth":{
			"accessToken":"sk-ant-abc",
			"refreshToken":"sk-ant-rt",
			"expiresAt":9999999999999,
			"scopes":["user:profile","user:inference"],
			"subscriptionType":"max",
			"rateLimitTier":"default_claude_max_20x"
		}
	}`)
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rep, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if gotAuth != "Bearer sk-ant-abc" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Fatalf("anthropic-beta = %q", gotBeta)
	}
	if rep.PlanTier != "max" {
		t.Fatalf("PlanTier = %q", rep.PlanTier)
	}
	if len(rep.Windows) != 4 {
		t.Fatalf("expected 4 windows, got %d: %+v", len(rep.Windows), rep.Windows)
	}
	for i, want := range []string{
		"Current session",
		"Current week (all models)",
		"Current week (Sonnet only)",
		"Extra usage",
	} {
		if rep.Windows[i].Label != want {
			t.Fatalf("window[%d].Label = %q, want %q", i, rep.Windows[i].Label, want)
		}
	}
	if rep.Windows[0].RemainingPct != 93 {
		t.Fatalf("session remaining = %.2f", rep.Windows[0].RemainingPct)
	}
	wantNote := func(needle string) {
		t.Helper()
		for _, n := range rep.Notes {
			if strings.Contains(n, needle) {
				return
			}
		}
		t.Fatalf("Notes %v missing %q", rep.Notes, needle)
	}
	wantNote("default_claude_max_20x")
	wantNote("$2.58 / $50.00")
}

func TestProbe_ProPlanHidesSonnetAndShowsExtraUsageState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 12, "resets_at": "2026-04-26T03:30:00Z"},
			"seven_day": {"utilization": 44, "resets_at": "2026-04-29T07:00:00Z"},
			"seven_day_sonnet": {"utilization": 44, "resets_at": "2026-04-29T07:00:00Z"},
			"extra_usage": {"is_enabled": false}
		}`))
	}))
	defer srv.Close()
	saved := AnthropicUsageEndpoint
	AnthropicUsageEndpoint = srv.URL
	defer func() { AnthropicUsageEndpoint = saved }()

	raw := []byte(`{"claudeAiOauth":{"accessToken":"x","refreshToken":"y","expiresAt":9999999999999,"subscriptionType":"pro"}}`)
	snap, _ := (Format{}).Parse(raw)
	rep, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(rep.Windows) != 2 {
		t.Fatalf("expected 2 windows, got %d: %+v", len(rep.Windows), rep.Windows)
	}
	for _, w := range rep.Windows {
		if strings.Contains(w.Label, "Sonnet") {
			t.Fatalf("unexpected sonnet window for pro plan: %+v", rep.Windows)
		}
	}
	foundExtraNote := false
	for _, n := range rep.Notes {
		if n == "extra usage: not enabled" {
			foundExtraNote = true
		}
	}
	if !foundExtraNote {
		t.Fatalf("expected extra usage disabled note, got %v", rep.Notes)
	}
}

func TestProbe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()
	saved := AnthropicUsageEndpoint
	AnthropicUsageEndpoint = srv.URL
	defer func() { AnthropicUsageEndpoint = saved }()

	raw := []byte(`{"claudeAiOauth":{"accessToken":"x","refreshToken":"y","expiresAt":9999999999999}}`)
	snap, _ := (Format{}).Parse(raw)
	_, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}
