package router

import (
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestApplyFullProviderInventoryReportRemovesStaleContainers(t *testing.T) {
	engine, _ := testEngine(t)
	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		Mode:     "full",
		NodeID:   "node-a1",
		HostName: "snowbox",
		Containers: []control.ContainerReport{
			{ContainerID: "container-1", ProviderInstanceID: "codex-a1", State: "running"},
			{ContainerID: "container-2", ProviderInstanceID: "gemini-a1", State: "running"},
		},
	}); err != nil {
		t.Fatalf("apply first inventory: %v", err)
	}
	if got := engine.Containers(); len(got) != 2 {
		t.Fatalf("expected two containers, got %#v", got)
	}

	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		Mode:     "full",
		NodeID:   "node-a1",
		HostName: "snowbox",
		Containers: []control.ContainerReport{
			{ContainerID: "container-2", ProviderInstanceID: "gemini-a1", State: "running"},
		},
	}); err != nil {
		t.Fatalf("apply second inventory: %v", err)
	}
	containers := engine.Containers()
	if len(containers) != 1 || containers[0].ContainerID != "container-2" {
		t.Fatalf("stale container was not removed: %#v", containers)
	}
}

func TestApplyProviderOnlyFullInventoryDoesNotClearNodeContainers(t *testing.T) {
	engine, _ := testEngine(t)
	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		Mode:     "full",
		NodeID:   "node-a1",
		HostName: "snowbox",
		Containers: []control.ContainerReport{
			{ContainerID: "container-1", ProviderInstanceID: "codex-a1", State: "running"},
		},
	}); err != nil {
		t.Fatalf("apply container inventory: %v", err)
	}
	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		Mode:      "full",
		NodeID:    "node-a1",
		HostName:  "snowbox",
		Providers: []control.ProviderRegisterPayload{registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)},
	}); err != nil {
		t.Fatalf("apply provider-only inventory: %v", err)
	}
	containers := engine.Containers()
	if len(containers) != 1 || containers[0].ContainerID != "container-1" {
		t.Fatalf("provider-only inventory should not clear containers: %#v", containers)
	}
}

func TestApplyProviderInventoryPreservesDynamicProviderState(t *testing.T) {
	engine, _ := testEngine(t)
	existing := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	existing.Identity.ContainerID = "container-existing"
	existing.Health = provider.Health{Status: provider.HealthDegraded, Reason: "upstream rate limited", CheckedAt: time.Now().UTC()}
	existing.Auth = provider.AuthState{Status: provider.AuthUnavailable, LastRefreshErr: "invalid api key"}
	existing.Limits = provider.LimitState{QueueDepth: 3}
	if err := engine.UpsertProvider(existing); err != nil {
		t.Fatalf("upsert existing provider: %v", err)
	}

	incoming := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	incoming.Health = provider.Health{Status: provider.HealthUnknown, CheckedAt: time.Now().UTC()}
	incoming.Auth = provider.AuthState{Status: provider.AuthUnknown}
	incoming.Limits = provider.LimitState{}
	incoming.Models = []provider.Model{{ID: "gpt-5-codex-updated"}}
	if err := engine.ApplyProviderInventoryReport(control.ProviderInventoryReport{
		Mode:      "full",
		NodeID:    "node-a1",
		HostName:  "snowbox",
		Providers: []control.ProviderRegisterPayload{incoming},
	}); err != nil {
		t.Fatalf("apply provider inventory: %v", err)
	}

	got, ok := engine.registry.Get(existing.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("provider not found after inventory")
	}
	if got.Health.Status != provider.HealthDegraded || got.Health.Reason != "upstream rate limited" {
		t.Fatalf("dynamic health was not preserved: %#v", got.Health)
	}
	if got.Auth.Status != provider.AuthUnavailable || got.Auth.LastRefreshErr != "invalid api key" {
		t.Fatalf("dynamic auth was not preserved: %#v", got.Auth)
	}
	if got.Limits.QueueDepth != 3 || got.Identity.ContainerID != "container-existing" {
		t.Fatalf("dynamic limits/container id were not preserved: limits=%#v identity=%#v", got.Limits, got.Identity)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "gpt-5-codex-updated" {
		t.Fatalf("inventory metadata was not applied: %#v", got.Models)
	}
}
