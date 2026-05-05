package router

import (
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/control"
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
