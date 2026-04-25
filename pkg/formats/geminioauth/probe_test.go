package geminioauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbe_HappyPath(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"currentTier":{"id":"standard-tier","name":"Gemini Standard","hasOnboardedPreviously":true},
			"paidTier":   {"id":"standard-tier","name":"Gemini Standard"},
			"cloudaicompanionProject":"my-proj-1234"
		}`))
	}))
	defer srv.Close()
	saved := LoadCodeAssistEndpoint
	LoadCodeAssistEndpoint = srv.URL
	defer func() { LoadCodeAssistEndpoint = saved }()

	expMs := time.Now().Add(time.Hour).UnixMilli()
	raw, _ := json.Marshal(map[string]any{
		"access_token":  "ya29.X",
		"refresh_token": "1//rt",
		"scope":         "https://www.googleapis.com/auth/cloud-platform",
		"token_type":    "Bearer",
		"id_token":      "id-tok",
		"expiry_date":   expMs,
	})
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rep, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if gotAuth != "Bearer ya29.X" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if md, _ := gotBody["metadata"].(map[string]any); md["pluginType"] != "GEMINI" {
		t.Fatalf("body metadata = %+v", gotBody["metadata"])
	}
	if rep.PlanTier != "standard-tier" {
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
	wantNote("my-proj-1234")
	wantNote("Gemini Standard")
}

func TestProbe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"PERMISSION_DENIED"}}`))
	}))
	defer srv.Close()
	saved := LoadCodeAssistEndpoint
	LoadCodeAssistEndpoint = srv.URL
	defer func() { LoadCodeAssistEndpoint = saved }()

	expMs := time.Now().Add(time.Hour).UnixMilli()
	raw, _ := json.Marshal(map[string]any{
		"access_token": "ya29.X", "refresh_token": "1//rt", "expiry_date": expMs,
	})
	snap, _ := (Format{}).Parse(raw)
	_, err := (Format{}).Probe(context.Background(), snap, "", srv.Client())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}
