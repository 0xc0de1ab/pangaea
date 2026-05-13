package router

import (
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

type ProviderDeleteResult struct {
	ProviderInstanceID  string           `json:"provider_instance_id"`
	ProviderType        string           `json:"provider_type,omitempty"`
	NodeID              string           `json:"node_id,omitempty"`
	HostName            string           `json:"host_name,omitempty"`
	Service             provider.Service `json:"service,omitempty"`
	Removed             bool             `json:"removed"`
	AuthRecordsRemoved  int              `json:"auth_records_removed,omitempty"`
	AuthReplicasRemoved int              `json:"auth_replicas_removed,omitempty"`
	UsageRemoved        bool             `json:"usage_removed,omitempty"`
	ContainersRemoved   int              `json:"containers_removed,omitempty"`
}

func (e *Engine) DeleteProvider(providerInstanceID string) (ProviderDeleteResult, error) {
	providerInstanceID = strings.TrimSpace(providerInstanceID)
	if e == nil || e.registry == nil {
		return ProviderDeleteResult{}, ErrRouterNotReady
	}
	if providerInstanceID == "" {
		return ProviderDeleteResult{}, provider.ErrProviderNotFound
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return ProviderDeleteResult{}, provider.ErrProviderNotFound
	}
	result := ProviderDeleteResult{
		ProviderInstanceID: providerInstanceID,
		ProviderType:       registration.Identity.ProviderType,
		NodeID:             registration.Identity.NodeID,
		HostName:           registration.Identity.HostName,
		Service:            registration.Identity.Service,
		Removed:            e.registry.Remove(providerInstanceID),
	}
	result.AuthRecordsRemoved, result.AuthReplicasRemoved = e.removeProviderAuth(providerInstanceID)
	result.UsageRemoved = e.removeProviderUsage(providerInstanceID)
	result.ContainersRemoved = e.removeProviderContainers(providerInstanceID)
	return result, nil
}

func (e *Engine) removeProviderUsage(providerInstanceID string) bool {
	if e == nil || strings.TrimSpace(providerInstanceID) == "" {
		return false
	}
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	if _, ok := e.usages[providerInstanceID]; !ok {
		return false
	}
	delete(e.usages, providerInstanceID)
	return true
}

func (e *Engine) removeProviderContainers(providerInstanceID string) int {
	if e == nil || strings.TrimSpace(providerInstanceID) == "" {
		return 0
	}
	e.nodeMu.Lock()
	defer e.nodeMu.Unlock()
	removed := 0
	for key, container := range e.containers {
		if container.ProviderInstanceID != providerInstanceID {
			continue
		}
		delete(e.containers, key)
		removed++
	}
	return removed
}

func (e *Engine) removeProviderAuth(providerInstanceID string) (int, int) {
	if e == nil || strings.TrimSpace(providerInstanceID) == "" {
		return 0, 0
	}
	now := time.Now().UTC()
	recordsRemoved := 0
	replicasRemoved := 0
	e.authMu.Lock()
	defer e.authMu.Unlock()
	for authID, record := range e.authRecords {
		original := record
		kept := make([]AuthReplica, 0, len(record.Replicas))
		removedReplica := false
		for _, replica := range record.Replicas {
			if replica.ProviderInstanceID == providerInstanceID {
				removedReplica = true
				replicasRemoved++
				continue
			}
			kept = append(kept, replica)
		}
		latestRemoved := record.ProviderInstanceID == providerInstanceID
		if !removedReplica && !latestRemoved {
			continue
		}
		if len(kept) == 0 {
			delete(e.authRecords, authID)
			delete(e.authRaw, authID)
			recordsRemoved++
			e.appendAuthEventLocked(AuthEvent{
				AuthID:             authID,
				Type:               "auth.provider.deleted",
				Service:            original.Service,
				Account:            original.Account,
				ProviderType:       original.LatestProviderType,
				ProviderInstanceID: providerInstanceID,
				NodeID:             original.NodeID,
				HostName:           original.HostName,
				Status:             original.Status,
				Fingerprint:        original.Fingerprint,
				Source:             original.Source,
				Message:            "provider deleted; auth sync record removed",
				At:                 now,
			})
			continue
		}
		record.Replicas = kept
		record.UpdatedAt = now
		if latestRemoved {
			promoted := latestAuthReplica(kept)
			record.LatestProviderType = promoted.ProviderType
			record.ProviderInstanceID = promoted.ProviderInstanceID
			record.NodeID = promoted.NodeID
			record.HostName = promoted.HostName
			record.Account = accountWithFallback(record.Account, promoted.Account)
			record.Status = promoted.Status
			record.Fingerprint = promoted.Fingerprint
			record.Source = promoted.Source
			record.ObservedAt = promoted.ObservedAt
			record.ReportedAt = promoted.ObservedAt
			record.HasDownload = false
			record.DownloadURL = ""
			delete(e.authRaw, authID)
		}
		e.authRecords[authID] = record
		e.appendAuthEventLocked(AuthEvent{
			AuthID:             authID,
			Type:               "auth.provider.deleted",
			Service:            original.Service,
			Account:            original.Account,
			ProviderType:       original.LatestProviderType,
			ProviderInstanceID: providerInstanceID,
			NodeID:             original.NodeID,
			HostName:           original.HostName,
			Status:             original.Status,
			Fingerprint:        original.Fingerprint,
			Source:             original.Source,
			Message:            "provider deleted; auth replica removed",
			At:                 now,
		})
	}
	return recordsRemoved, replicasRemoved
}

func latestAuthReplica(replicas []AuthReplica) AuthReplica {
	if len(replicas) == 0 {
		return AuthReplica{}
	}
	latest := replicas[0]
	for _, replica := range replicas[1:] {
		if replica.UpdatedAt.After(latest.UpdatedAt) {
			latest = replica
			continue
		}
		if replica.UpdatedAt.Equal(latest.UpdatedAt) && replica.ObservedAt.After(latest.ObservedAt) {
			latest = replica
		}
	}
	return latest
}
