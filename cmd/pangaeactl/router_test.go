package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	v2router "github.com/0xc0de1ab/pangaea/internal/router"
)

func TestLoadRouterPolicyRequiresPolicyWithoutSimulator(t *testing.T) {
	if _, err := loadRouterPolicy("", false); err == nil {
		t.Fatalf("expected error without policy or simulator")
	}
}

func TestBuildRouterEngineWithSimulator(t *testing.T) {
	engine, err := buildRouterEngine(routerServeOptions{Simulator: true})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	decision := engine.DryRun(v2router.RouteRequest{
		Model:      "providersim-default",
		APIDialect: "openai",
		Stream:     true,
	})
	if !decision.Allowed {
		t.Fatalf("expected simulator route allowed: %#v", decision)
	}
}

func TestBuildRouterAPIKeyStore(t *testing.T) {
	store := buildRouterAPIKeyStore(routerServeOptions{APIKey: "pk_test_router", TenantID: "team-a", UserID: "usr_1"})
	if store == nil {
		t.Fatalf("expected API key store")
	}
	principal, ok := store.Authenticate("pk_test_router")
	if !ok {
		t.Fatalf("expected auth success")
	}
	if principal.TenantID != "team-a" || principal.UserID != "usr_1" {
		t.Fatalf("unexpected principal: %#v", principal)
	}
}

func TestRouterServeCommandExposesModelsWithSimulatorEngine(t *testing.T) {
	engine, err := buildRouterEngine(routerServeOptions{Simulator: true})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRouterServeCommandExposesStreamTokenKeyFlag(t *testing.T) {
	cmd := newRouterServeCmd()
	if cmd.Flags().Lookup("stream-token-key") == nil {
		t.Fatalf("expected stream-token-key flag")
	}
}
