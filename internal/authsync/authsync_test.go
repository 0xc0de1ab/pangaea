package authsync

import (
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestMissingContainerBootstrapCopyFromSpecifiedHostPath(t *testing.T) {
	now := testNow()
	host := Snapshot{
		Source:  SnapshotSourceHost,
		Path:    "/home/alice/.codex/auth.json",
		Present: true,
		State: provider.AuthState{
			Status:      provider.AuthHealthy,
			ExpiresAt:   now.Add(time.Hour),
			Refreshable: true,
		},
	}
	container := Snapshot{
		Source: SnapshotSourceContainer,
		Path:   "/var/lib/pangaea/codex/auth.json",
	}

	decision := Decide(now, Policy{}, host, container, Snapshot{})
	if decision.Action != ActionBootstrapCopy {
		t.Fatalf("expected %s, got %s", ActionBootstrapCopy, decision.Action)
	}
	step := decision.Actions[0]
	if step.From != SnapshotSourceHost || step.To != SnapshotSourceContainer {
		t.Fatalf("expected host->container bootstrap, got %s->%s", step.From, step.To)
	}
	if step.FromPath != host.Path {
		t.Fatalf("expected bootstrap from specified host path %q, got %q", host.Path, step.FromPath)
	}
	if step.ToPath != container.Path {
		t.Fatalf("expected bootstrap to container path %q, got %q", container.Path, step.ToPath)
	}
}

func TestRefreshSoonTriggersRefreshOneshot(t *testing.T) {
	now := testNow()
	container := Snapshot{
		Source:  SnapshotSourceContainer,
		Present: true,
		State: provider.AuthState{
			Status:      provider.AuthRefreshSoon,
			ExpiresAt:   now.Add(5 * time.Minute),
			Refreshable: true,
		},
	}
	router := Snapshot{
		Source: SnapshotSourceRouter,
		State:  provider.AuthState{SelectedSource: string(SnapshotSourceContainer)},
	}

	decision := Decide(now, Policy{RefreshThreshold: 10 * time.Minute}, Snapshot{}, container, router)
	if decision.Action != ActionRefreshOneshot {
		t.Fatalf("expected %s, got %s", ActionRefreshOneshot, decision.Action)
	}
	if decision.Actions[0].To != SnapshotSourceContainer {
		t.Fatalf("expected refresh target container, got %s", decision.Actions[0].To)
	}
}

func TestExpiredCausesDrainAndRefreshDecision(t *testing.T) {
	now := testNow()
	container := Snapshot{
		Source:  SnapshotSourceContainer,
		Present: true,
		State: provider.AuthState{
			Status:      provider.AuthExpired,
			ExpiresAt:   now.Add(-time.Minute),
			Refreshable: true,
		},
	}
	router := Snapshot{
		Source: SnapshotSourceRouter,
		State:  provider.AuthState{SelectedSource: string(SnapshotSourceContainer)},
	}

	decision := Decide(now, Policy{}, Snapshot{}, container, router)
	if decision.Action != ActionDrainProvider {
		t.Fatalf("expected primary action %s, got %s", ActionDrainProvider, decision.Action)
	}
	if !decision.HasAction(ActionDrainProvider) {
		t.Fatalf("expected %s action", ActionDrainProvider)
	}
	if !decision.HasAction(ActionRefreshOneshot) {
		t.Fatalf("expected %s action", ActionRefreshOneshot)
	}
}

func TestDivergentFreshHostContainerConflictUnlessPolicyAllowsOverwrite(t *testing.T) {
	now := testNow()
	host := Snapshot{
		Source:  SnapshotSourceHost,
		Path:    "/host/auth.json",
		Present: true,
		State: provider.AuthState{
			Status:      provider.AuthHealthy,
			ExpiresAt:   now.Add(2 * time.Hour),
			Refreshable: true,
		},
	}
	container := Snapshot{
		Source:  SnapshotSourceContainer,
		Path:    "/container/auth.json",
		Present: true,
		State: provider.AuthState{
			Status:      provider.AuthHealthy,
			ExpiresAt:   now.Add(3 * time.Hour),
			Refreshable: true,
		},
	}

	decision := Decide(now, Policy{}, host, container, Snapshot{})
	if decision.Action != ActionMarkConflict {
		t.Fatalf("expected %s, got %s", ActionMarkConflict, decision.Action)
	}
	if decision.Status != provider.AuthConflict {
		t.Fatalf("expected conflict status, got %s", decision.Status)
	}

	containerWins := Decide(now, Policy{
		AllowHostOverwrite:          true,
		PreferContainerAfterRefresh: true,
	}, host, container, Snapshot{})
	if containerWins.Action != ActionSyncContainerToHost {
		t.Fatalf("expected %s, got %s", ActionSyncContainerToHost, containerWins.Action)
	}

	hostWins := Decide(now, Policy{AllowContainerOverwrite: true}, host, container, Snapshot{})
	if hostWins.Action != ActionSyncHostToContainer {
		t.Fatalf("expected %s, got %s", ActionSyncHostToContainer, hostWins.Action)
	}
}

func testNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
