package router

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

type NodeSnapshot struct {
	NodeID          string                `json:"node_id"`
	HostName        string                `json:"host_name,omitempty"`
	AgentVersion    string                `json:"agent_version,omitempty"`
	OS              string                `json:"os,omitempty"`
	Arch            string                `json:"arch,omitempty"`
	Runtime         control.RuntimeInfo   `json:"runtime,omitempty"`
	Capabilities    []string              `json:"capabilities,omitempty"`
	Health          control.HealthReport  `json:"health,omitempty"`
	Resources       control.ResourceUsage `json:"resources,omitempty"`
	LastHelloAt     time.Time             `json:"last_hello_at,omitempty"`
	LastHeartbeatAt time.Time             `json:"last_heartbeat_at,omitempty"`
	LastInventoryAt time.Time             `json:"last_inventory_at,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type ContainerSnapshot struct {
	NodeID             string                `json:"node_id,omitempty"`
	HostName           string                `json:"host_name,omitempty"`
	ContainerID        string                `json:"container_id"`
	ProviderID         string                `json:"provider_id,omitempty"`
	ProviderInstanceID string                `json:"provider_instance_id,omitempty"`
	Image              string                `json:"image,omitempty"`
	State              string                `json:"state,omitempty"`
	Health             control.HealthReport  `json:"health,omitempty"`
	Resources          control.ResourceUsage `json:"resources,omitempty"`
	Labels             map[string]string     `json:"labels,omitempty"`
	StartedAt          time.Time             `json:"started_at,omitempty"`
	Extensions         map[string]any        `json:"extensions,omitempty"`
	ReportedAt         time.Time             `json:"reported_at,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

func (e *Engine) UpdateNodeHello(hello control.NodeHello, receivedAt time.Time) error {
	if e == nil {
		return ErrRouterNotReady
	}
	if strings.TrimSpace(hello.NodeID) == "" {
		return control.ErrInvalidPayload
	}
	now := time.Now().UTC()
	if receivedAt.IsZero() {
		receivedAt = now
	}
	e.nodeMu.Lock()
	defer e.nodeMu.Unlock()
	node := e.nodes[hello.NodeID]
	node.NodeID = hello.NodeID
	node.AgentVersion = hello.AgentVersion
	node.OS = hello.OS
	node.Arch = hello.Arch
	node.Runtime = hello.Runtime
	node.Capabilities = append([]string(nil), hello.Capabilities...)
	node.LastHelloAt = receivedAt
	node.UpdatedAt = now
	if e.nodes == nil {
		e.nodes = make(map[string]NodeSnapshot)
	}
	e.nodes[hello.NodeID] = node
	return nil
}

func (e *Engine) UpdateNodeHeartbeat(heartbeat control.NodeHeartbeat) error {
	if e == nil {
		return ErrRouterNotReady
	}
	if strings.TrimSpace(heartbeat.NodeID) == "" {
		return control.ErrInvalidPayload
	}
	now := time.Now().UTC()
	reportedAt := heartbeat.ReportedAt
	if reportedAt.IsZero() {
		reportedAt = now
	}
	e.nodeMu.Lock()
	defer e.nodeMu.Unlock()
	node := e.nodes[heartbeat.NodeID]
	node.NodeID = heartbeat.NodeID
	if heartbeat.HostName != "" {
		node.HostName = heartbeat.HostName
	}
	node.Health = heartbeat.Health
	node.Resources = heartbeat.Resources
	node.LastHeartbeatAt = reportedAt
	node.UpdatedAt = now
	if e.nodes == nil {
		e.nodes = make(map[string]NodeSnapshot)
	}
	e.nodes[heartbeat.NodeID] = node
	return nil
}

func (e *Engine) ApplyProviderInventoryReport(report control.ProviderInventoryReport) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
	for _, registration := range report.Providers {
		if existing, ok := e.registry.Get(registration.Identity.ProviderInstanceID); ok {
			registration = mergeInventoryRegistration(existing, registration)
		}
		if err := e.UpsertProvider(registration); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	reportedAt := report.ReportedAt
	if reportedAt.IsZero() {
		reportedAt = now
	}
	if report.NodeID != "" {
		e.nodeMu.Lock()
		if e.nodes == nil {
			e.nodes = make(map[string]NodeSnapshot)
		}
		node := e.nodes[report.NodeID]
		node.NodeID = report.NodeID
		if report.HostName != "" {
			node.HostName = report.HostName
		}
		node.Resources = report.Resources
		node.LastInventoryAt = reportedAt
		node.UpdatedAt = now
		e.nodes[report.NodeID] = node
		e.nodeMu.Unlock()
	}
	containerKeys := make(map[string]struct{}, len(report.Containers))
	for _, container := range report.Containers {
		if strings.TrimSpace(container.ContainerID) == "" {
			return control.ErrInvalidPayload
		}
		containerKeys[containerKey(report.NodeID, container.ContainerID)] = struct{}{}
		e.upsertContainer(report.NodeID, report.HostName, container, reportedAt, now)
	}
	if strings.EqualFold(report.Mode, "full") && report.NodeID != "" && report.Containers != nil {
		e.removeContainersMissingFromFullInventory(report.NodeID, containerKeys)
	}
	return nil
}

func mergeInventoryRegistration(existing provider.Registration, incoming provider.Registration) provider.Registration {
	if incoming.Identity.ContainerID == "" {
		incoming.Identity.ContainerID = existing.Identity.ContainerID
	}
	if incoming.Health.Status == "" || incoming.Health.Status == provider.HealthUnknown {
		incoming.Health = existing.Health
	}
	if incoming.Auth.Status == "" || incoming.Auth.Status == provider.AuthUnknown {
		incoming.Auth = existing.Auth
	}
	if incoming.Limits == (provider.LimitState{}) {
		incoming.Limits = existing.Limits
	}
	if incoming.RegisteredAt.IsZero() {
		incoming.RegisteredAt = existing.RegisteredAt
	}
	return incoming
}

func (e *Engine) Nodes() []NodeSnapshot {
	if e == nil {
		return nil
	}
	e.nodeMu.RLock()
	defer e.nodeMu.RUnlock()
	out := make([]NodeSnapshot, 0, len(e.nodes))
	for _, node := range e.nodes {
		node.Capabilities = append([]string(nil), node.Capabilities...)
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostName == out[j].HostName {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].HostName < out[j].HostName
	})
	return out
}

func (e *Engine) Containers() []ContainerSnapshot {
	if e == nil {
		return nil
	}
	e.nodeMu.RLock()
	defer e.nodeMu.RUnlock()
	out := make([]ContainerSnapshot, 0, len(e.containers))
	for _, container := range e.containers {
		container.Labels = cloneStringMap(container.Labels)
		container.Extensions = cloneAnyMap(container.Extensions)
		out = append(out, container)
	}
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].HostName != out[j].HostName:
			return out[i].HostName < out[j].HostName
		case out[i].NodeID != out[j].NodeID:
			return out[i].NodeID < out[j].NodeID
		default:
			return out[i].ContainerID < out[j].ContainerID
		}
	})
	return out
}

func (e *Engine) upsertContainer(nodeID string, hostName string, report control.ContainerReport, reportedAt time.Time, now time.Time) {
	e.nodeMu.Lock()
	defer e.nodeMu.Unlock()
	if e.containers == nil {
		e.containers = make(map[string]ContainerSnapshot)
	}
	key := containerKey(nodeID, report.ContainerID)
	e.containers[key] = ContainerSnapshot{
		NodeID:             nodeID,
		HostName:           hostName,
		ContainerID:        report.ContainerID,
		ProviderID:         report.ProviderID,
		ProviderInstanceID: report.ProviderInstanceID,
		Image:              report.Image,
		State:              report.State,
		Health:             report.Health,
		Resources:          report.Resources,
		Labels:             cloneStringMap(report.Labels),
		StartedAt:          report.StartedAt,
		Extensions:         cloneAnyMap(report.Extensions),
		ReportedAt:         reportedAt,
		UpdatedAt:          now,
	}
}

func (e *Engine) removeContainersMissingFromFullInventory(nodeID string, seen map[string]struct{}) {
	e.nodeMu.Lock()
	defer e.nodeMu.Unlock()
	for key, container := range e.containers {
		if container.NodeID != nodeID {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		delete(e.containers, key)
	}
}

func containerKey(nodeID string, containerID string) string {
	if nodeID == "" {
		return containerID
	}
	return fmt.Sprintf("%s/%s", nodeID, containerID)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
