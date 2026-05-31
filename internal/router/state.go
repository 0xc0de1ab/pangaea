package router

import (
	"sort"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/quota"
)

const RouterStateSnapshotVersion = "router-state/v1"

type StateSnapshot struct {
	Version             string                   `json:"version"`
	SavedAt             time.Time                `json:"saved_at"`
	Traces              []RequestTrace           `json:"traces,omitempty"`
	RoutingRuleStats    []RoutingRuleStatsRecord `json:"routing_rule_stats,omitempty"`
	Quotas              []quota.SnapshotRecord   `json:"quotas,omitempty"`
	Usages              []ProviderUsageSnapshot  `json:"usages,omitempty"`
	AuthEvents          []AuthEvent              `json:"auth_events,omitempty"`
	Users               []RouterUser             `json:"users,omitempty"`
	RoutingRules        []RoutingRule            `json:"routing_rules,omitempty"`
	Notifiers           []NotifierStatus         `json:"notifiers,omitempty"`
	NotificationHistory []NotifierDelivery       `json:"notification_history,omitempty"`
}

type RoutingRuleStatsRecord struct {
	RuleID string `json:"rule_id"`
	RoutingRuleStats
}

func (e *Engine) SnapshotState(now time.Time) StateSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return StateSnapshot{
		Version:             RouterStateSnapshotVersion,
		SavedAt:             now.UTC(),
		Traces:              e.requestTracesInRecordOrder(defaultRequestTraceLimit),
		RoutingRuleStats:    e.routingRuleStatsInRecordOrder(),
		Quotas:              e.ledgerSnapshots(),
		Usages:              e.providerUsagesInRecordOrder(),
		AuthEvents:          e.authEventsInRecordOrder(maxAuthEvents),
		Users:               e.ListUsers(),
		RoutingRules:        e.ListRoutingRules(),
		Notifiers:           e.NotifierStatuses(),
		NotificationHistory: e.notifierDeliveriesInRecordOrder(maxNotifierHistory),
	}
}

func (e *Engine) RestoreState(snapshot StateSnapshot) {
	if e == nil {
		return
	}
	if len(snapshot.Quotas) > 0 && e.ledger != nil {
		_ = e.ledger.RestoreSnapshots(snapshot.Quotas)
	}
	if len(snapshot.Usages) > 0 {
		e.restoreProviderUsages(snapshot.Usages)
	}
	if len(snapshot.AuthEvents) > 0 {
		e.restoreAuthEvents(snapshot.AuthEvents)
	}
	if len(snapshot.Traces) > 0 {
		e.traceMu.Lock()
		e.traces = make(map[string]RequestTrace, len(snapshot.Traces))
		e.traceIDs = e.traceIDs[:0]
		for _, trace := range snapshot.Traces {
			if trace.RequestID == "" {
				continue
			}
			if _, exists := e.traces[trace.RequestID]; !exists {
				e.traceIDs = append(e.traceIDs, trace.RequestID)
			}
			e.traces[trace.RequestID] = trace
		}
		e.traceMu.Unlock()
	}
	if len(snapshot.RoutingRuleStats) > 0 {
		e.restoreRoutingRuleStats(snapshot.RoutingRuleStats)
	} else if len(snapshot.Traces) > 0 {
		e.rebuildRoutingRuleStatsFromTraces()
	}
	if len(snapshot.Users) > 0 {
		e.usersMu.Lock()
		e.users = make(map[string]RouterUser, len(snapshot.Users))
		for _, user := range snapshot.Users {
			email := normalizeUserEmail(user.Email)
			if email == "" {
				continue
			}
			user.Email = email
			if user.ID == "" {
				user.ID = email
			}
			e.users[email] = user
		}
		e.usersMu.Unlock()
	}
	if len(snapshot.RoutingRules) > 0 {
		e.rulesMu.Lock()
		e.routingRules = make(map[string]RoutingRule, len(snapshot.RoutingRules))
		for _, rule := range snapshot.RoutingRules {
			normalized, err := normalizeRoutingRule(rule)
			if err != nil {
				continue
			}
			e.routingRules[normalized.ID] = normalized
		}
		e.rulesMu.Unlock()
	}
	if len(snapshot.Notifiers) > 0 || len(snapshot.NotificationHistory) > 0 {
		e.notifierMu.Lock()
		e.notifierStatuses = make(map[string]NotifierStatus, len(snapshot.Notifiers))
		for _, status := range snapshot.Notifiers {
			if status.ID == "" {
				continue
			}
			status = sanitizeNotifierStatus(status)
			e.notifierStatuses[status.ID] = status
		}
		e.notifierHistory = e.notifierHistory[:0]
		if len(snapshot.NotificationHistory) > maxNotifierHistory {
			snapshot.NotificationHistory = snapshot.NotificationHistory[len(snapshot.NotificationHistory)-maxNotifierHistory:]
		}
		for _, delivery := range snapshot.NotificationHistory {
			if delivery.NotifierID == "" {
				continue
			}
			delivery = sanitizeNotifierDelivery(delivery)
			e.notifierHistory = append(e.notifierHistory, delivery)
		}
		e.notifierMu.Unlock()
	}
}

func (e *Engine) routingRuleStatsInRecordOrder() []RoutingRuleStatsRecord {
	if e == nil {
		return nil
	}
	e.routeStatsMu.RLock()
	defer e.routeStatsMu.RUnlock()
	ids := make([]string, 0, len(e.routeStats))
	for id := range e.routeStats {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]RoutingRuleStatsRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, RoutingRuleStatsRecord{
			RuleID:           id,
			RoutingRuleStats: cloneRoutingRuleStats(e.routeStats[id]),
		})
	}
	return out
}

func (e *Engine) restoreRoutingRuleStats(records []RoutingRuleStatsRecord) {
	if e == nil {
		return
	}
	e.routeStatsMu.Lock()
	defer e.routeStatsMu.Unlock()
	e.routeStats = make(map[string]RoutingRuleStats, len(records))
	for _, record := range records {
		if record.RuleID == "" {
			continue
		}
		e.routeStats[record.RuleID] = cloneRoutingRuleStats(record.RoutingRuleStats)
	}
}

func (e *Engine) rebuildRoutingRuleStatsFromTraces() {
	if e == nil {
		return
	}
	e.traceMu.RLock()
	traces := make([]RequestTrace, 0, len(e.traces))
	for _, trace := range e.traces {
		traces = append(traces, trace)
	}
	e.traceMu.RUnlock()
	e.routeStatsMu.Lock()
	e.routeStats = make(map[string]RoutingRuleStats)
	e.routeStatsMu.Unlock()
	for _, trace := range traces {
		e.recordRoutingRuleTraceStats(trace)
	}
}

func (e *Engine) requestTracesInRecordOrder(limit int) []RequestTrace {
	if e == nil {
		return nil
	}
	if limit <= 0 || limit > defaultRequestTraceLimit {
		limit = defaultRequestTraceLimit
	}
	e.traceMu.RLock()
	defer e.traceMu.RUnlock()
	start := 0
	if len(e.traceIDs) > limit {
		start = len(e.traceIDs) - limit
	}
	out := make([]RequestTrace, 0, min(limit, len(e.traceIDs)))
	for _, id := range e.traceIDs[start:] {
		if trace, ok := e.traces[id]; ok {
			out = append(out, trace)
		}
	}
	return out
}

func (e *Engine) ledgerSnapshots() []quota.SnapshotRecord {
	if e == nil || e.ledger == nil {
		return nil
	}
	return e.ledger.Snapshots()
}
