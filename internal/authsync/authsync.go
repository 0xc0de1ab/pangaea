// Package authsync contains provider-neutral auth state-machine primitives.
package authsync

import (
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

type SnapshotSource string

const (
	SnapshotSourceHost      SnapshotSource = "host"
	SnapshotSourceContainer SnapshotSource = "container"
	SnapshotSourceRouter    SnapshotSource = "router"

	SourceHost      = SnapshotSourceHost
	SourceContainer = SnapshotSourceContainer
	SourceRouter    = SnapshotSourceRouter
)

type Action string

const (
	ActionNoop                Action = "noop"
	ActionBootstrapCopy       Action = "bootstrap_copy"
	ActionSyncHostToContainer Action = "sync_host_to_container"
	ActionSyncContainerToHost Action = "sync_container_to_host"
	ActionRefreshOneshot      Action = "refresh_oneshot"
	ActionMarkConflict        Action = "mark_conflict"
	ActionDrainProvider       Action = "drain_provider"
)

type Policy struct {
	RefreshThreshold            time.Duration `json:"refresh_threshold"`
	PreferContainerAfterRefresh bool          `json:"prefer_container_after_refresh"`
	AllowHostOverwrite          bool          `json:"allow_host_overwrite"`
	AllowContainerOverwrite     bool          `json:"allow_container_overwrite"`
}

type Snapshot struct {
	Source  SnapshotSource     `json:"source"`
	Path    string             `json:"path,omitempty"`
	Present bool               `json:"present"`
	State   provider.AuthState `json:"state"`
}

type ActionStep struct {
	Action   Action         `json:"action"`
	From     SnapshotSource `json:"from,omitempty"`
	To       SnapshotSource `json:"to,omitempty"`
	FromPath string         `json:"from_path,omitempty"`
	ToPath   string         `json:"to_path,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}

type Decision struct {
	Status         provider.AuthStatus `json:"status"`
	SelectedSource SnapshotSource      `json:"selected_source,omitempty"`
	Action         Action              `json:"action"`
	Actions        []ActionStep        `json:"actions"`
	Reason         string              `json:"reason,omitempty"`
}

func Decide(now time.Time, policy Policy, host, container, router Snapshot) Decision {
	host = withSource(host, SnapshotSourceHost)
	container = withSource(container, SnapshotSourceContainer)
	router = withSource(router, SnapshotSourceRouter)

	selected := selectedSource(now, policy, host, container, router)
	active := snapshotFor(selected, host, container, router)

	if missing(container) && usable(now, host) && host.Path != "" {
		return newDecision(hostStatus(now, host), SnapshotSourceHost, "container auth missing", ActionStep{
			Action:   ActionBootstrapCopy,
			From:     SnapshotSourceHost,
			To:       SnapshotSourceContainer,
			FromPath: host.Path,
			ToPath:   container.Path,
			Reason:   "copy host auth to bootstrap missing container auth",
		})
	}

	if shouldDrain(now, active) {
		steps := []ActionStep{{
			Action: ActionDrainProvider,
			From:   selected,
			To:     SnapshotSourceRouter,
			Reason: "selected auth is not routable",
		}}
		if refreshTarget, ok := refreshTarget(selected, host, container); ok {
			steps = append(steps, ActionStep{
				Action: ActionRefreshOneshot,
				From:   selected,
				To:     refreshTarget.Source,
				Reason: "selected auth can be refreshed",
			})
		}
		return newDecision(hostStatus(now, active), selected, "selected auth requires drain", steps...)
	}

	if refreshSource, target, ok := refreshDueTarget(now, policy, selected, host, container); ok {
		return newDecision(hostStatus(now, refreshSource), selected, "auth refresh threshold reached", ActionStep{
			Action: ActionRefreshOneshot,
			From:   refreshSource.Source,
			To:     target.Source,
			Reason: "auth expires within refresh threshold",
		})
	}

	if fresh(now, host) && fresh(now, container) && !sameFreshAuth(host, container) {
		if step, ok := overwriteStep(policy, host, container); ok {
			return newDecision(hostStatus(now, snapshotFor(step.From, host, container, router)), step.From, "fresh auth divergence resolved by policy", step)
		}
		return newDecision(provider.AuthConflict, selected, "fresh host and container auth diverged", ActionStep{
			Action: ActionMarkConflict,
			From:   SnapshotSourceHost,
			To:     SnapshotSourceRouter,
			Reason: "host and container auth are both fresh but do not match",
		})
	}

	if missing(host) && usable(now, container) && policy.AllowHostOverwrite {
		return newDecision(hostStatus(now, container), SnapshotSourceContainer, "host auth missing", ActionStep{
			Action:   ActionSyncContainerToHost,
			From:     SnapshotSourceContainer,
			To:       SnapshotSourceHost,
			FromPath: container.Path,
			ToPath:   host.Path,
			Reason:   "container auth may initialize host auth by policy",
		})
	}

	return newDecision(hostStatus(now, active), selected, "no auth sync action required")
}

func (d Decision) HasAction(action Action) bool {
	for _, step := range d.Actions {
		if step.Action == action {
			return true
		}
	}
	return false
}

func withSource(snapshot Snapshot, source SnapshotSource) Snapshot {
	if snapshot.Source == "" {
		snapshot.Source = source
	}
	return snapshot
}

func newDecision(status provider.AuthStatus, selected SnapshotSource, reason string, steps ...ActionStep) Decision {
	if status == "" {
		status = provider.AuthUnknown
	}
	if len(steps) == 0 {
		steps = []ActionStep{{
			Action: ActionNoop,
			Reason: reason,
		}}
	}
	return Decision{
		Status:         status,
		SelectedSource: selected,
		Action:         steps[0].Action,
		Actions:        steps,
		Reason:         reason,
	}
}

func selectedSource(now time.Time, policy Policy, host, container, router Snapshot) SnapshotSource {
	if selected := parseSource(router.State.SelectedSource); selected != "" {
		return selected
	}
	if policy.PreferContainerAfterRefresh && usable(now, container) {
		return SnapshotSourceContainer
	}
	if usable(now, container) {
		return SnapshotSourceContainer
	}
	if usable(now, host) {
		return SnapshotSourceHost
	}
	if exists(container) {
		return SnapshotSourceContainer
	}
	if exists(host) {
		return SnapshotSourceHost
	}
	return SnapshotSourceRouter
}

func parseSource(source string) SnapshotSource {
	switch SnapshotSource(source) {
	case SnapshotSourceHost:
		return SnapshotSourceHost
	case SnapshotSourceContainer:
		return SnapshotSourceContainer
	case SnapshotSourceRouter:
		return SnapshotSourceRouter
	default:
		return ""
	}
}

func snapshotFor(source SnapshotSource, host, container, router Snapshot) Snapshot {
	switch source {
	case SnapshotSourceHost:
		return host
	case SnapshotSourceContainer:
		return container
	case SnapshotSourceRouter:
		return router
	default:
		return Snapshot{}
	}
}

func exists(snapshot Snapshot) bool {
	if snapshot.Present {
		return true
	}
	switch snapshot.State.Status {
	case "", provider.AuthUnknown, provider.AuthUnavailable:
		return false
	default:
		return true
	}
}

func missing(snapshot Snapshot) bool {
	return !exists(snapshot)
}

func hostStatus(now time.Time, snapshot Snapshot) provider.AuthStatus {
	status := snapshot.State.Status
	if status == "" {
		status = provider.AuthUnknown
	}
	if !snapshot.State.ExpiresAt.IsZero() && !snapshot.State.ExpiresAt.After(now) {
		return provider.AuthExpired
	}
	return status
}

func usable(now time.Time, snapshot Snapshot) bool {
	if !exists(snapshot) {
		return false
	}
	switch hostStatus(now, snapshot) {
	case provider.AuthHealthy, provider.AuthRefreshSoon, provider.AuthRefreshing:
		return true
	default:
		return false
	}
}

func fresh(now time.Time, snapshot Snapshot) bool {
	return exists(snapshot) && hostStatus(now, snapshot) == provider.AuthHealthy
}

func shouldDrain(now time.Time, snapshot Snapshot) bool {
	if !exists(snapshot) {
		return false
	}
	switch hostStatus(now, snapshot) {
	case provider.AuthExpired, provider.AuthRevoked, provider.AuthUnavailable:
		return true
	default:
		return false
	}
}

func refreshDueTarget(now time.Time, policy Policy, selected SnapshotSource, host, container Snapshot) (Snapshot, Snapshot, bool) {
	candidates := []Snapshot{snapshotFor(selected, host, container, Snapshot{}), container, host}
	seen := map[SnapshotSource]bool{}
	for _, candidate := range candidates {
		if seen[candidate.Source] {
			continue
		}
		seen[candidate.Source] = true
		if !refreshDue(now, policy, candidate) {
			continue
		}
		if target, ok := refreshTarget(candidate.Source, host, container); ok {
			return candidate, target, true
		}
	}
	return Snapshot{}, Snapshot{}, false
}

func refreshDue(now time.Time, policy Policy, snapshot Snapshot) bool {
	if !exists(snapshot) || !snapshot.State.Refreshable {
		return false
	}
	if hostStatus(now, snapshot) == provider.AuthRefreshSoon {
		return true
	}
	if policy.RefreshThreshold <= 0 || snapshot.State.ExpiresAt.IsZero() {
		return false
	}
	return snapshot.State.ExpiresAt.After(now) && !snapshot.State.ExpiresAt.After(now.Add(policy.RefreshThreshold))
}

func refreshTarget(selected SnapshotSource, host, container Snapshot) (Snapshot, bool) {
	if selected == SnapshotSourceContainer && exists(container) && container.State.Refreshable {
		return container, true
	}
	if exists(container) && container.State.Refreshable {
		return container, true
	}
	if selected == SnapshotSourceHost && exists(host) && host.State.Refreshable {
		return host, true
	}
	return Snapshot{}, false
}

func sameFreshAuth(host, container Snapshot) bool {
	return host.State.Account == container.State.Account &&
		host.State.ExpiresAt.Equal(container.State.ExpiresAt) &&
		host.State.Refreshable == container.State.Refreshable
}

func overwriteStep(policy Policy, host, container Snapshot) (ActionStep, bool) {
	if policy.AllowHostOverwrite && (policy.PreferContainerAfterRefresh || !policy.AllowContainerOverwrite || newer(container, host)) {
		return ActionStep{
			Action:   ActionSyncContainerToHost,
			From:     SnapshotSourceContainer,
			To:       SnapshotSourceHost,
			FromPath: container.Path,
			ToPath:   host.Path,
			Reason:   "container auth is allowed to overwrite host auth",
		}, true
	}
	if policy.AllowContainerOverwrite {
		return ActionStep{
			Action:   ActionSyncHostToContainer,
			From:     SnapshotSourceHost,
			To:       SnapshotSourceContainer,
			FromPath: host.Path,
			ToPath:   container.Path,
			Reason:   "host auth is allowed to overwrite container auth",
		}, true
	}
	if policy.AllowHostOverwrite {
		return ActionStep{
			Action:   ActionSyncContainerToHost,
			From:     SnapshotSourceContainer,
			To:       SnapshotSourceHost,
			FromPath: container.Path,
			ToPath:   host.Path,
			Reason:   "container auth is allowed to overwrite host auth",
		}, true
	}
	return ActionStep{}, false
}

func newer(candidate, current Snapshot) bool {
	if candidate.State.ExpiresAt.IsZero() {
		return !current.State.ExpiresAt.IsZero()
	}
	if current.State.ExpiresAt.IsZero() {
		return false
	}
	return candidate.State.ExpiresAt.After(current.State.ExpiresAt)
}
