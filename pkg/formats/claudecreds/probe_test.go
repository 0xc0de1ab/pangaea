package claudecreds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbe_HappyPath(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"account":      {"uuid":"u-1","email_address":"alice@example.com"},
			"organization": {"uuid":"o-1","name":"Acme Inc","organization_type":"claude_max","rate_limit_tier":"default_claude_max_20x"}
		}`))
	}))
	defer srv.Close()
	saved := AnthropicProfileEndpoint
	AnthropicProfileEndpoint = srv.URL
	defer func() { AnthropicProfileEndpoint = saved }()

	raw := []byte(`{
		"claudeAiOauth":{"accessToken":"sk-ant-abc","refreshToken":"sk-ant-rt","expiresAt":9999999999999,"scopes":["user:inference"],"subscriptionType":"max"}
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
	if rep.PlanTier != "claude_max" {
		t.Fatalf("PlanTier = %q", rep.PlanTier)
	}
	wantNote := func(needle string) {
		for _, n := range rep.Notes {
			if strings.Contains(n, needle) {
				return
			}
		}
		t.Fatalf("Notes %v missing %q", rep.Notes, needle)
	}
	wantNote("Acme Inc")
	wantNote("default_claude_max_20x")
	wantNote("alice@example.com")
}

func TestProbe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()
	saved := AnthropicProfileEndpoint
	AnthropicProfileEndpoint = srv.URL
	defer func() { AnthropicProfileEndpoint = saved }()

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
