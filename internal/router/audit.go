package router

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const defaultAuditEventLimit = 1024

type AuditEventType string

const (
	AuditEventAPIKeyCreate         AuditEventType = "api_key.create"
	AuditEventAPIKeyDelete         AuditEventType = "api_key.delete"
	AuditEventQuotaLimitSet        AuditEventType = "quota.limit.set"
	AuditEventProviderAuthRefresh  AuditEventType = "provider.auth_refresh"
	AuditEventProviderDrain        AuditEventType = "provider.drain"
	AuditEventProviderDrainRelease AuditEventType = "provider.drain_release"
	AuditEventRequestTraceDelete   AuditEventType = "request_trace.delete"
)

const (
	AuditOutcomeSucceeded = "succeeded"
	AuditOutcomeFailed    = "failed"
)

type AuditActor struct {
	TenantID   string `json:"tenant_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	APIKeyID   string `json:"api_key_id,omitempty"`
	Source     string `json:"source,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type AuditTarget struct {
	ProviderInstanceID string           `json:"provider_instance_id,omitempty"`
	ProviderType       string           `json:"provider_type,omitempty"`
	NodeID             string           `json:"node_id,omitempty"`
	HostName           string           `json:"host_name,omitempty"`
	ContainerID        string           `json:"container_id,omitempty"`
	Service            provider.Service `json:"service,omitempty"`
	APIKeyID           string           `json:"api_key_id,omitempty"`
	TenantID           string           `json:"tenant_id,omitempty"`
	UserID             string           `json:"user_id,omitempty"`
	Model              string           `json:"model,omitempty"`
	RequestID          string           `json:"request_id,omitempty"`
}

type AuditEvent struct {
	ID        string            `json:"id"`
	Type      AuditEventType    `json:"type"`
	Actor     AuditActor        `json:"actor,omitempty"`
	Target    AuditTarget       `json:"target,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Outcome   string            `json:"outcome"`
	Error     string            `json:"error,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

func (e *Engine) RecordAuditEvent(event AuditEvent) AuditEvent {
	if e == nil || event.Type == "" {
		return AuditEvent{}
	}
	now := time.Now().UTC()
	e.auditMu.Lock()
	defer e.auditMu.Unlock()
	if e.auditEvents == nil {
		e.auditEvents = make(map[string]AuditEvent)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if strings.TrimSpace(event.Outcome) == "" {
		event.Outcome = AuditOutcomeSucceeded
	}
	if strings.TrimSpace(event.ID) == "" {
		e.auditSeq++
		event.ID = fmt.Sprintf("audit_%s_%06d", event.CreatedAt.UTC().Format("20060102150405.000000000"), e.auditSeq)
	}
	if len(event.Metadata) == 0 {
		event.Metadata = nil
	}
	if _, exists := e.auditEvents[event.ID]; !exists {
		e.auditIDs = append(e.auditIDs, event.ID)
	}
	e.auditEvents[event.ID] = event
	for len(e.auditIDs) > defaultAuditEventLimit {
		oldest := e.auditIDs[0]
		e.auditIDs = e.auditIDs[1:]
		delete(e.auditEvents, oldest)
	}
	return event
}

func (e *Engine) AuditEvents(limit int) []AuditEvent {
	if e == nil {
		return nil
	}
	if limit <= 0 || limit > defaultAuditEventLimit {
		limit = defaultAuditEventLimit
	}
	e.auditMu.RLock()
	defer e.auditMu.RUnlock()
	out := make([]AuditEvent, 0, min(limit, len(e.auditEvents)))
	for i := len(e.auditIDs) - 1; i >= 0 && len(out) < limit; i-- {
		if event, ok := e.auditEvents[e.auditIDs[i]]; ok {
			out = append(out, event)
		}
	}
	if len(e.auditIDs) == 0 && len(e.auditEvents) > 0 {
		for _, event := range e.auditEvents {
			out = append(out, event)
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		})
		if len(out) > limit {
			out = out[:limit]
		}
	}
	return out
}

func providerAuditTarget(registration provider.Registration) AuditTarget {
	return AuditTarget{
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		ProviderType:       registration.Identity.ProviderType,
		NodeID:             registration.Identity.NodeID,
		HostName:           registration.Identity.HostName,
		ContainerID:        registration.Identity.ContainerID,
		Service:            registration.Identity.Service,
	}
}
