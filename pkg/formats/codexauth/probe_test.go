package codexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeIDTokenWithAccount(t *testing.T, accountID string) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := map[string]any{
		"sub": "u-1",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_user_id":    "u-1",
			"chatgpt_account_id": accountID,
		},
	}
	payload, _ := json.Marshal(claims)
	return hdr + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func TestProbe_HappyPath(t *testing.T) {
	var gotAccountHdr, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccountHdr = r.Header.Get("ChatGPT-Account-ID")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"pro",
			"rate_limit":{
				"allowed":true, "limit_reached":false,
				"primary_window":   {"used_percent":12.5, "limit_window_seconds":18000, "reset_after_seconds":3600, "reset_at":"2026-04-25T20:00:00Z"},
				"secondary_window": {"used_percent":3.2,  "limit_window_seconds":604800,"reset_after_seconds":345600,"reset_at":"2026-04-29T20:00:00Z"}
			}
		}`))
	}))
	defer srv.Close()
	saved := ChatGPTUsageEndpoint
	ChatGPTUsageEndpoint = srv.URL
	defer func() { ChatGPTUsageEndpoint = saved }()

	idTok := makeIDTokenWithAccount(t, "acct-xyz")
	raw, _ := json.Marshal(map[string]any{
		"auth_mode": "Chatgpt",
		"tokens": map[string]any{
			"id_token":      idTok,
			"access_token":  idTok + "X",
			"refresh_token": "rt",
		},
	})
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rep, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if gotAccountHdr != "acct-xyz" {
		t.Fatalf("ChatGPT-Account-ID = %q", gotAccountHdr)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if rep.PlanTier != "pro" {
		t.Fatalf("PlanTier = %q", rep.PlanTier)
	}
	if rep.RemainingPct < 87.4 || rep.RemainingPct > 87.6 {
		t.Fatalf("RemainingPct = %f, want ~87.5", rep.RemainingPct)
	}
	if rep.Unit != "5h window" {
		t.Fatalf("Unit = %q", rep.Unit)
	}
	wantReset, _ := time.Parse(time.RFC3339, "2026-04-25T20:00:00Z")
	if !rep.ResetAt.Equal(wantReset) {
		t.Fatalf("ResetAt = %v", rep.ResetAt)
	}
	foundSecondary := false
	for _, n := range rep.Notes {
		if strings.Contains(n, "7d window") && strings.Contains(n, "3.2") {
			foundSecondary = true
		}
	}
	if !foundSecondary {
		t.Fatalf("Notes %v missing secondary window", rep.Notes)
	}
}

func TestProbe_MissingAccountID(t *testing.T) {
	// id_token without chatgpt_account_id and no top-level account_id either.
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u-1"}`))
	jwt := hdr + "." + payload + "."
	raw, _ := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"id_token":      jwt,
			"access_token":  jwt + "X",
			"refresh_token": "rt",
		},
	})
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = (Format{}).Probe(context.Background(), snap, "", nil)
	if err == nil {
		t.Fatal("expected error when chatgpt_account_id missing")
	}
}

func TestProbe_FallsBackToTopLevelAccountID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "top-acct" {
			t.Errorf("ChatGPT-Account-ID = %q, want top-acct", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plan_type":"team","rate_limit":{"allowed":true}}`))
	}))
	defer srv.Close()
	saved := ChatGPTUsageEndpoint
	ChatGPTUsageEndpoint = srv.URL
	defer func() { ChatGPTUsageEndpoint = saved }()

	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u-1"}`)) // no chatgpt_account_id
	jwt := hdr + "." + payload + "."
	raw, _ := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"id_token":      jwt,
			"access_token":  jwt + "Y",
			"refresh_token": "rt",
			"account_id":    "top-acct",
		},
	})
	snap, _ := (Format{}).Parse(raw)
	rep, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if rep.PlanTier != "team" {
		t.Fatalf("PlanTier = %q", rep.PlanTier)
	}
}

func TestProbe_ParsesUnixResetAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"pro",
			"rate_limit":{
				"allowed":true, "limit_reached":false,
				"primary_window":   {"used_percent":15, "limit_window_seconds":18000, "reset_after_seconds":1200, "reset_at":1777178051},
				"secondary_window": {"used_percent":48, "limit_window_seconds":604800,"reset_after_seconds":289627,"reset_at":1777466452}
			},
			"additional_rate_limits":[
				{
					"limit_name":"GPT-5.3-Codex-Spark",
					"rate_limit":{
						"allowed":true, "limit_reached":false,
						"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":18000,"reset_at":1777194825}
					}
				}
			]
		}`))
	}))
	defer srv.Close()
	saved := ChatGPTUsageEndpoint
	ChatGPTUsageEndpoint = srv.URL
	defer func() { ChatGPTUsageEndpoint = saved }()

	idTok := makeIDTokenWithAccount(t, "acct-xyz")
	raw, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      idTok,
			"access_token":  idTok + "X",
			"refresh_token": "rt",
		},
	})
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rep, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(rep.Windows) != 3 {
		t.Fatalf("windows = %d, want 3", len(rep.Windows))
	}
	if got := rep.Windows[0].ResetAt.UTC().Format(time.RFC3339); got != "2026-04-26T04:34:11Z" {
		t.Fatalf("primary reset_at = %s", got)
	}
	if got := rep.Windows[2].ResetAt.UTC().Format(time.RFC3339); got != "2026-04-26T09:13:45Z" {
		t.Fatalf("extra reset_at = %s", got)
	}
}
