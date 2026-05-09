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
	existing.Auth = provider.AuthState{Status: provider.AuthUnavailable, Account: provider.Account{ID: "acct-control", Display: "control@example.test"}, LastRefreshErr: "invalid api key"}
	existing.Limits = provider.LimitState{QueueDepth: 3}
	if err := engine.UpsertProvider(existing); err != nil {
		t.Fatalf("upsert existing provider: %v", err)
	}

	incoming := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	incoming.Identity.Account = provider.Account{}
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
	if got.Identity.Account.Display != "control@example.test" || got.Auth.Account.Display != "control@example.test" {
		t.Fatalf("account identity was not preserved: identity=%#v auth=%#v", got.Identity.Account, got.Auth.Account)
	}
	if got.Limits.QueueDepth != 3 || got.Identity.ContainerID != "container-existing" {
		t.Fatalf("dynamic limits/container id were not preserved: limits=%#v identity=%#v", got.Limits, got.Identity)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "gpt-5-codex-updated" {
		t.Fatalf("inventory metadata was not applied: %#v", got.Models)
	}
}

func TestApplyProviderInventoryMergesDynamicDiscoveredModels(t *testing.T) {
	engine, _ := testEngine(t)
	existing := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	existing.Models = []provider.Model{
		{ID: "gpt-5.5", Aliases: []string{"codex-default"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
		{ID: "gpt-5.4", Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE}},
	}
	if err := engine.UpsertProvider(existing); err != nil {
		t.Fatalf("upsert existing provider: %v", err)
	}

	incoming := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	incoming.Models = []provider.Model{{
		ID:           "gpt-5.5",
		Aliases:      []string{"codex-default"},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
	}}
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
	if len(got.Models) != 2 {
		t.Fatalf("models were not merged: %#v", got.Models)
	}
	if got.Models[0].ID != "gpt-5.5" || len(got.Models[0].Capabilities) != 2 || got.Models[1].ID != "gpt-5.4" {
		t.Fatalf("unexpected merged models: %#v", got.Models)
	}
}

func TestApplyProviderInventoryPreservesDiscoveredModelGroupMetadata(t *testing.T) {
	engine, _ := testEngine(t)
	existing := registration("gemini-control-a1", "gemini-cli", "control@example.test", 10, 0)
	existing.Models = []provider.Model{{
		ID:           "auto-gemini-3",
		Kind:         "group",
		GroupMembers: []string{"gemini-3.1-pro-preview", "gemini-3-flash-preview"},
		Aliases:      []string{"Auto (Gemini 3)"},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
	}}
	if err := engine.UpsertProvider(existing); err != nil {
		t.Fatalf("upsert existing provider: %v", err)
	}

	incoming := registration("gemini-control-a1", "gemini-cli", "control@example.test", 10, 0)
	incoming.Models = []provider.Model{{
		ID:           "auto-gemini-3",
		Aliases:      []string{"Auto Gemini 3"},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
	}}
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
	if got.Models[0].Kind != "group" || len(got.Models[0].GroupMembers) != 2 {
		t.Fatalf("group metadata was not preserved: %#v", got.Models[0])
	}
	if len(got.Models[0].Aliases) != 2 {
		t.Fatalf("aliases were not merged: %#v", got.Models[0])
	}
}

func TestUpdateProviderAuthBackfillsIdentityAccount(t *testing.T) {
	engine, _ := testEngine(t)
	reg := registration("codex-control-a1", "codex-cli", "", 10, 0)
	reg.Identity.Account = provider.Account{}
	if err := engine.UpsertProvider(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}

	account := provider.Account{ID: "acct-control", Display: "control@example.test"}
	if err := engine.UpdateProviderAuth(reg.Identity.ProviderInstanceID, provider.AuthState{Status: provider.AuthHealthy, Account: account}); err != nil {
		t.Fatalf("update provider auth: %v", err)
	}
	got, ok := engine.registry.Get(reg.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("provider not found after auth update")
	}
	if got.Identity.Account != account {
		t.Fatalf("identity account was not backfilled: %#v", got.Identity.Account)
	}
}
