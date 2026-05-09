package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/security"
)

func TestDashboardSummaryRequiresAdminAuthAndReturnsReadModelShape(t *testing.T) {
	handler, adminToken := newDashboardContractHandler(t)

	rec := dashboardContractGET(handler, "/router/v1/dashboard/summary", "")
	dashboardRequireUnauthorized(t, rec)

	rec = dashboardContractGET(handler, "/router/v1/dashboard/summary", "pk_wrong_dashboard_admin")
	dashboardRequireUnauthorized(t, rec)

	rec = dashboardContractGET(handler, "/router/v1/dashboard/summary", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with admin bearer token, got %d body=%s", rec.Code, rec.Body.String())
	}
	summary := dashboardDecodeObject(t, rec)
	dashboardRequireTimestampFieldAny(t, summary, "updated_at", "last_updated_at")

	routerHealth := dashboardRequireObjectField(t, summary, "router")
	dashboardRequireStringField(t, routerHealth, "status")

	providers := dashboardRequireObjectField(t, summary, "providers")
	dashboardAssertNumberField(t, providers, "total", 1)
	dashboardAssertNumberField(t, providers, "ready", 1)
	dashboardRequireNumberField(t, providers, "degraded")
	dashboardRequireNumberField(t, providers, "down")

	requests := dashboardRequireObjectField(t, summary, "requests")
	dashboardRequireNumberField(t, requests, "active")
	dashboardRequireNumberField(t, requests, "recent_failures")

	streams := dashboardRequireObjectField(t, summary, "streams")
	dashboardRequireNumberField(t, streams, "active")

	sessions := dashboardRequireObjectField(t, summary, "sessions")
	dashboardRequireNumberField(t, sessions, "control_disconnected")
	dashboardRequireNumberField(t, sessions, "data_disconnected")

	nodes := dashboardRequireObjectField(t, summary, "nodes")
	dashboardAssertNumberField(t, nodes, "total", 1)
	dashboardRequireNumberField(t, nodes, "stale")

	containers := dashboardRequireObjectField(t, summary, "containers")
	dashboardAssertNumberField(t, containers, "total", 1)
	dashboardRequireNumberField(t, containers, "stale")

	auth := dashboardRequireObjectField(t, summary, "auth")
	dashboardRequireNumberField(t, auth, "expiring")
	dashboardRequireNumberField(t, auth, "expired")
}

func TestDashboardProvidersReadModelIncludesProviderIdentity(t *testing.T) {
	handler, adminToken := newDashboardContractHandler(t)

	rec := dashboardContractGET(handler, "/router/v1/dashboard/providers", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	out := dashboardDecodeObject(t, rec)
	providers := dashboardRequireArrayField(t, out, "providers")
	got := dashboardFindObjectByStringField(t, providers, "provider_instance_id", "codex-primary-a1")

	dashboardAssertStringField(t, got, "provider_instance_id", "codex-primary-a1")
	dashboardAssertStringField(t, got, "provider_id", "codex-cli")
	dashboardAssertStringField(t, got, "service", string(provider.ServiceCodex))
	dashboardAssertStringField(t, got, "provider_kind", string(provider.KindCLIContainer))
	dashboardAssertStringField(t, got, "host_name", "snowbox")
	dashboardAssertStringField(t, got, "node_id", "node-a1")

	account := dashboardRequireObjectField(t, got, "account")
	dashboardAssertStringField(t, account, "display", "primary@example.test")

	health := dashboardRequireObjectField(t, got, "health")
	dashboardAssertStringField(t, health, "status", string(provider.HealthReady))

	auth := dashboardRequireObjectField(t, got, "auth")
	dashboardAssertStringField(t, auth, "status", string(provider.AuthHealthy))

	models := dashboardRequireArrayField(t, got, "models")
	if len(models) == 0 {
		t.Fatalf("expected provider read model to include models")
	}
	capabilities := dashboardRequireArrayField(t, got, "capabilities")
	dashboardRequireStringInArray(t, capabilities, string(provider.CapabilityOpenAIChat))
}

func newDashboardContractHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	engine, _ := testEngine(t)
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	if err := engine.UpdateNodeHello(control.NodeHello{
		NodeID:       "node-a1",
		AgentVersion: "test-agent",
		OS:           "linux",
		Arch:         "arm64",
		Runtime:      control.RuntimeInfo{Kind: "docker"},
	}, now); err != nil {
		t.Fatalf("update node hello: %v", err)
	}
	if err := engine.UpdateNodeHeartbeat(control.NodeHeartbeat{
		NodeID:     "node-a1",
		HostName:   "snowbox",
		Health:     control.HealthReport{Status: "ready"},
		ReportedAt: now,
	}); err != nil {
		t.Fatalf("update node heartbeat: %v", err)
	}
	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		Mode:     "full",
		NodeID:   "node-a1",
		HostName: "snowbox",
		Containers: []control.ContainerReport{{
			ContainerID:        "container-a1",
			ProviderID:         "codex-cli",
			ProviderInstanceID: "codex-primary-a1",
			Image:              "pangaea/codex:contract",
			State:              "running",
			Health:             control.HealthReport{Status: "ready"},
		}},
		ReportedAt: now,
	}); err != nil {
		t.Fatalf("apply provider inventory: %v", err)
	}
	if err := engine.UpdateProviderUsage("codex-primary-a1", provider.UsageReport{
		ObservedAt:   now,
		Source:       "contract-test",
		Requests:     3,
		InputTokens:  20,
		OutputTokens: 10,
		TotalTokens:  30,
	}, now); err != nil {
		t.Fatalf("update provider usage: %v", err)
	}

	store := security.NewAPIKeyStore([]byte("dashboard-contract-pepper"))
	const adminToken = "pk_dashboard_contract_admin"
	if _, err := store.AddRawKey("dashboard_admin", adminToken, "ops", "admin_1"); err != nil {
		t.Fatalf("add admin key: %v", err)
	}
	return NewHTTPHandler(HTTPOptions{Engine: engine, APIKeys: store}), adminToken
}

func dashboardContractGET(handler http.Handler, path string, bearerToken string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearerToken != "" {
		req.Header.Set("authorization", "Bearer "+bearerToken)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func dashboardRequireUnauthorized(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	out := dashboardDecodeObject(t, rec)
	if got := strings.TrimSpace(dashboardStringField(out, "error")); got == "" {
		t.Fatalf("expected JSON error response, got %#v", out)
	}
}

func dashboardDecodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	decoder := json.NewDecoder(rec.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("decode JSON object: %v body=%s", err, rec.Body.String())
	}
	return out
}

func dashboardRequireObjectField(t *testing.T, obj map[string]any, field string) map[string]any {
	t.Helper()
	raw, ok := obj[field]
	if !ok {
		t.Fatalf("missing object field %q in %#v", field, obj)
	}
	child, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("field %q has type %T, want object", field, raw)
	}
	return child
}

func dashboardRequireArrayField(t *testing.T, obj map[string]any, field string) []any {
	t.Helper()
	raw, ok := obj[field]
	if !ok {
		t.Fatalf("missing array field %q in %#v", field, obj)
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("field %q has type %T, want array", field, raw)
	}
	return items
}

func dashboardRequireStringField(t *testing.T, obj map[string]any, field string) string {
	t.Helper()
	got := dashboardStringField(obj, field)
	if got == "" {
		t.Fatalf("missing non-empty string field %q in %#v", field, obj)
	}
	return got
}

func dashboardAssertStringField(t *testing.T, obj map[string]any, field string, want string) {
	t.Helper()
	if got := dashboardRequireStringField(t, obj, field); got != want {
		t.Fatalf("field %q = %q, want %q", field, got, want)
	}
}

func dashboardStringField(obj map[string]any, field string) string {
	raw, ok := obj[field]
	if !ok {
		return ""
	}
	value, _ := raw.(string)
	return value
}

func dashboardRequireNumberField(t *testing.T, obj map[string]any, field string) int64 {
	t.Helper()
	raw, ok := obj[field]
	if !ok {
		t.Fatalf("missing numeric field %q in %#v", field, obj)
	}
	return dashboardNumber(t, field, raw)
}

func dashboardAssertNumberField(t *testing.T, obj map[string]any, field string, want int64) {
	t.Helper()
	if got := dashboardRequireNumberField(t, obj, field); got != want {
		t.Fatalf("field %q = %d, want %d", field, got, want)
	}
}

func dashboardNumber(t *testing.T, field string, raw any) int64 {
	t.Helper()
	switch value := raw.(type) {
	case json.Number:
		if got, err := value.Int64(); err == nil {
			return got
		}
		got, err := value.Float64()
		if err != nil {
			t.Fatalf("field %q has invalid number %q: %v", field, value, err)
		}
		return int64(got)
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	default:
		t.Fatalf("field %q has type %T, want number", field, raw)
		return 0
	}
}

func dashboardRequireTimestampFieldAny(t *testing.T, obj map[string]any, fields ...string) {
	t.Helper()
	for _, field := range fields {
		raw, ok := obj[field]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			t.Fatalf("field %q has type %T, want timestamp string", field, raw)
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			t.Fatalf("field %q has invalid timestamp %q: %v", field, value, err)
		}
		return
	}
	t.Fatalf("missing one of timestamp fields %v in %#v", fields, obj)
}

func dashboardFindObjectByStringField(t *testing.T, items []any, field string, value string) map[string]any {
	t.Helper()
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("providers item has type %T, want object", item)
		}
		if dashboardStringField(obj, field) == value {
			return obj
		}
	}
	t.Fatalf("missing object with %s=%q in %#v", field, value, items)
	return nil
}

func dashboardRequireStringInArray(t *testing.T, items []any, want string) {
	t.Helper()
	for _, item := range items {
		got, ok := item.(string)
		if !ok {
			t.Fatalf("array item has type %T, want string", item)
		}
		if got == want {
			return
		}
	}
	t.Fatalf("array missing %q: %#v", want, items)
}
