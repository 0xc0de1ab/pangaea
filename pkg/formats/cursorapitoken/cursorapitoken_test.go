package cursorapitoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestParsePlainToken(t *testing.T) {
	var f Format
	snap, err := f.Parse([]byte("  secret-token-line  "))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Identity() == "" {
		t.Fatal("identity")
	}
	raw := string(snap.Raw())
	if raw != "secret-token-line" {
		t.Fatalf("raw %q", raw)
	}
}

func TestParseKeyValueLine(t *testing.T) {
	var f Format
	snap, err := f.Parse([]byte("CURSOR_API_KEY=abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.Raw()) != "abc123" {
		t.Fatalf("raw %q", snap.Raw())
	}
}

func TestParseRejectsEmpty(t *testing.T) {
	var f Format
	if _, err := f.Parse([]byte(" \n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestAccountUsesEmailFromMe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "tok" || pass != "" {
			t.Fatalf("expected basic auth username=tok empty password, got ok=%v user=%q pass=%q", ok, user, pass)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"apiKeyName": "fixture-key",
			"createdAt":  "2026-01-01T00:00:00Z",
			"userEmail":  "dev@example.com",
		})
	}))
	defer srv.Close()
	t.Setenv("CURSOR_API_BASE_URL", srv.URL)

	var f Format
	snap, err := f.Parse([]byte("tok"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.Account(context.Background(), snap, filepath.Join(t.TempDir(), "x"))
	if err != nil || id != "dev@example.com" {
		t.Fatalf("account id %q err %v", id, err)
	}
	display, err := f.AccountDisplay(context.Background(), snap, "")
	if err != nil || display != "dev@example.com" {
		t.Fatalf("display %q err %v", display, err)
	}
}

func TestAccountFallsBackToIdentityWhenMeFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("CURSOR_API_BASE_URL", srv.URL)

	var f Format
	snap, err := f.Parse([]byte("tok"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.Account(context.Background(), snap, filepath.Join(t.TempDir(), "x"))
	if err != nil || id != snap.Identity() {
		t.Fatalf("account %q err %v want identity fallback", id, err)
	}
}

func TestProbeSurfacesPlanWhenPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me" {
			t.Fatalf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"apiKeyName": "fixture-key",
			"createdAt":  "2026-01-01T00:00:00Z",
			"userEmail":  "dev@example.com",
			"plan":       "Pro",
		})
	}))
	defer srv.Close()
	t.Setenv("CURSOR_API_BASE_URL", srv.URL)

	var f Format
	snap, err := f.Parse([]byte("sekrit"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := f.Probe(context.Background(), snap, "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if rep.PlanTier != "Pro" {
		t.Fatalf("plan tier %q", rep.PlanTier)
	}
}
