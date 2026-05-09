package router

import (
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func TestDashboardReadModelsAreNilSafe(t *testing.T) {
	if got := BuildDashboardOverview(nil, nil); got.Summary.ProvidersTotal != 0 || got.Summary.ActiveDataSessions != 0 {
		t.Fatalf("unexpected nil overview: %#v", got)
	}
	if got := BuildDashboardRoutes(nil, nil); len(got.Routes) != 0 {
		t.Fatalf("unexpected nil routes: %#v", got)
	}
	if got := BuildDashboardProviders(nil, nil); len(got.Providers) != 0 || got.Summary.ProvidersTotal != 0 {
		t.Fatalf("unexpected nil providers: %#v", got)
	}
	if got := BuildDashboardTraces(nil, 10); len(got.Traces) != 0 || got.RecentErrors != 0 {
		t.Fatalf("unexpected nil traces: %#v", got)
	}
}

func TestDashboardReadModelsIncludeIdentityFreshnessAndCounts(t *testing.T) {
	engine, _ := testEngine(t)
	base := time.Now().UTC().Add(-10 * time.Minute)
	readAt := time.Now().UTC().Add(10 * time.Minute)

	registration, ok := engine.registry.Get("codex-primary-a1")
	if !ok {
		t.Fatal("missing test provider")
	}
	registration.Identity.ContainerID = "container-a1"
	registration.Auth.LastRefreshAt = base
	registration.Limits.ActiveStreams = 1
	if err := engine.UpsertProvider(registration); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}

	if err := engine.UpdateNodeHello(control.NodeHello{
		NodeID:       "node-a1",
		AgentVersion: "test-agent",
		OS:           "linux",
		Arch:         "arm64",
		Runtime:      control.RuntimeInfo{Kind: "docker"},
	}, base); err != nil {
		t.Fatalf("update node hello: %v", err)
	}
	if err := engine.UpdateNodeHeartbeat(control.NodeHeartbeat{
		NodeID:     "node-a1",
		HostName:   "snowbox",
		Health:     control.HealthReport{Status: "ready"},
		ReportedAt: base,
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
			State:              "running",
			Health:             control.HealthReport{Status: "ready"},
		}},
		ReportedAt: base,
	}); err != nil {
		t.Fatalf("apply provider inventory: %v", err)
	}
	if err := engine.UpdateProviderUsage("codex-primary-a1", provider.UsageReport{
		ObservedAt:   base,
		Source:       "dashboard-test",
		Requests:     3,
		InputTokens:  20,
		OutputTokens: 10,
		TotalTokens:  30,
	}, base); err != nil {
		t.Fatalf("update usage: %v", err)
	}
	engine.bindProviderControlSession("codex-primary-a1", &controlSession{connectedAt: readAt.Add(-time.Minute)})

	broker := &DataBroker{sessions: map[string]*dataSession{
		"codex-primary-a1": {
			providerInstanceID: "codex-primary-a1",
			connectedAt:        readAt.Add(-time.Minute),
			pending: map[string]*pendingResponse{
				"req-a": {},
				"req-b": {},
			},
		},
	}}

	identity := registration.Identity
	engine.recordRequestTrace(RequestTrace{
		RequestID: "req-dashboard-failed",
		RouteRequest: RouteRequest{
			TenantID:   "team-a",
			UserID:     "usr_1",
			APIKeyID:   "key_1",
			Model:      "gpt-5-codex",
			APIDialect: compat.APIDialectOpenAI,
		},
		Decision: RouteDecision{
			RouteID:        "codex-primary",
			CanonicalModel: "gpt-5.3-codex-spark",
		},
		Provider:       &identity,
		Status:         "provider_error",
		Error:          "upstream failed",
		ErrorStatus:    500,
		EstimatedUsage: quota.Usage{Tokens: 10, Requests: 1},
		StartedAt:      readAt.Add(-2 * time.Second),
		CompletedAt:    readAt,
		DurationMS:     2000,
	})

	providers := buildProviderViews(readAt, engine, broker)
	if len(providers) != 1 {
		t.Fatalf("expected one provider view, got %#v", providers)
	}
	providerView := providers[0]
	if providerView.ProviderInstanceID != "codex-primary-a1" || providerView.HostName != "snowbox" || providerView.Service != provider.ServiceCodex || providerView.ProviderKind != provider.KindCLIContainer {
		t.Fatalf("provider identity not flattened: %#v", providerView)
	}
	if providerView.Account.Display != "primary@example.test" {
		t.Fatalf("provider account not set: %#v", providerView.Account)
	}
	if len(providerView.Models) != 1 || providerView.Models[0].ID != "gpt-5.3-codex-spark" {
		t.Fatalf("provider models not derived from policy: %#v", providerView.Models)
	}
	if !providerView.ControlSessionActive || !providerView.DataSessionActive || providerView.PendingRequests != 2 {
		t.Fatalf("provider sessions not summarized: %#v", providerView)
	}
	if !providerView.NodeFreshness.Stale || !providerView.ContainerFreshness.Stale {
		t.Fatalf("expected stale node/container freshness: node=%#v container=%#v", providerView.NodeFreshness, providerView.ContainerFreshness)
	}
	if providerView.Usage == nil || providerView.Usage.TotalTokens != 30 || providerView.UsageFreshness.Source == "" {
		t.Fatalf("usage freshness missing: %#v", providerView)
	}

	summary := buildDashboardSummary(readAt, engine, broker, DefaultDashboardTraceLimit)
	if summary.ProvidersTotal != 1 || summary.Providers.Ready != 1 || summary.ProvidersByHealth[provider.HealthReady] != 1 {
		t.Fatalf("provider counts wrong: %#v", summary)
	}
	if summary.ActiveControlSessions != 1 || summary.ActiveDataSessions != 1 || summary.Requests.Active != 2 || summary.Streams.Active != 1 {
		t.Fatalf("session/request counts wrong: %#v", summary)
	}
	if summary.StaleNodes != 1 || summary.Nodes.Stale != 1 || summary.StaleContainers != 1 || summary.Containers.Stale != 1 {
		t.Fatalf("stale counts wrong: %#v", summary)
	}
	if summary.RecentErrors != 1 || summary.Requests.RecentFailures != 1 {
		t.Fatalf("trace error counts wrong: %#v", summary)
	}
	if summary.QuotaPressure.LimitedScopes != 1 || summary.QuotaPressure.Highest == nil {
		t.Fatalf("quota pressure not derived: %#v", summary.QuotaPressure)
	}

	routes := buildRouteViews(engine, broker)
	if len(routes) != 1 || routes[0].AvailableProviders != 1 {
		t.Fatalf("route views missing available provider: %#v", routes)
	}
	routeProvider := routes[0].Candidates[0].Providers[0]
	if !routeProvider.Allowed || !routeProvider.DataSessionActive || routeProvider.ProviderKind != provider.KindCLIContainer || routeProvider.ProviderInstanceID != "codex-primary-a1" {
		t.Fatalf("route provider details missing: %#v", routeProvider)
	}

	traces := BuildDashboardTraces(engine, 10)
	if traces.RecentErrors != 1 || len(traces.Traces) != 1 {
		t.Fatalf("trace summary count wrong: %#v", traces)
	}
	trace := traces.Traces[0]
	if trace.ProviderInstanceID != "codex-primary-a1" || trace.HostName != "snowbox" || trace.ProviderKind != provider.KindCLIContainer || trace.ErrorStatus != 500 {
		t.Fatalf("trace identity not flattened: %#v", trace)
	}
}
