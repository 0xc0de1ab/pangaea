package router

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

var ErrRouterNotReady = errors.New("router not ready")

const staleTransientDegradedAfter = 30 * time.Second
const maxRoutingRuleModelFallbackAttempts = 16

const authEventUsageQuotaWindowReset = "usage.quota.window.reset"

type Engine struct {
	policy              RoutingPolicy
	registry            *provider.Registry
	ledger              *quota.Ledger
	invoker             Invoker
	availability        ProviderAvailability
	usageMu             sync.RWMutex
	usages              map[string]ProviderUsageSnapshot
	traceMu             sync.RWMutex
	traces              map[string]RequestTrace
	traceIDs            []string
	routeStatsMu        sync.RWMutex
	routeStats          map[string]RoutingRuleStats
	auditMu             sync.RWMutex
	auditEvents         map[string]AuditEvent
	auditIDs            []string
	auditSeq            uint64
	authMu              sync.RWMutex
	authRecords         map[string]AuthRecord
	authRaw             map[string][]byte
	authRawMeta         map[string]authRawMetadata
	authEvents          []AuthEvent
	notifierMu          sync.RWMutex
	notifierStatuses    map[string]NotifierStatus
	notifierHistory     []NotifierDelivery
	notifierSeq         uint64
	usersMu             sync.RWMutex
	users               map[string]RouterUser
	rulesMu             sync.RWMutex
	routingRules        map[string]RoutingRule
	nodeMu              sync.RWMutex
	nodes               map[string]NodeSnapshot
	containers          map[string]ContainerSnapshot
	controlMu           sync.RWMutex
	controlSessions     map[string]*controlSession
	pendingAuthRefresh  map[string]chan control.AuthRefreshResult
	pendingAuthRequests map[string]control.AuthRefreshRequest
}

type RouteExecutionRequest struct {
	RequestID     string       `json:"request_id"`
	RouteRequest  RouteRequest `json:"route_request"`
	QuotaScope    quota.Scope  `json:"quota_scope"`
	QuotaEstimate quota.Usage  `json:"quota_estimate"`
}

type RouteExecution struct {
	Decision    RouteDecision     `json:"decision"`
	Reservation quota.Reservation `json:"reservation,omitempty"`
}

type Invoker interface {
	Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error)
}

type StreamInvoker interface {
	InvokeStream(context.Context, provider.Registration, compat.Request, func(compat.Event) error) (compat.Response, error)
}

type ProviderAvailability interface {
	ProviderAvailable(providerInstanceID string) bool
}

type ProviderLoad interface {
	ProviderQueueDepth(providerInstanceID string) int
}

type ModelInfo struct {
	ID               string                `json:"id"`
	CanonicalModel   string                `json:"canonical_model,omitempty"`
	Capabilities     []provider.Capability `json:"capabilities,omitempty"`
	ContextTokens    int                   `json:"context_tokens,omitempty"`
	MaxContextTokens int                   `json:"max_context_tokens,omitempty"`
	MaxOutputTokens  int                   `json:"max_output_tokens,omitempty"`
}

type routingRuleModelFallbackCause struct {
	EventType       string
	Message         string
	RejectionPrefix string
}

func NewEngine(policy RoutingPolicy, registry *provider.Registry, ledger *quota.Ledger) (*Engine, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("%w: provider registry is nil", ErrRouterNotReady)
	}
	if ledger == nil {
		ledger = quota.NewLedger()
	}
	return &Engine{
		policy:              policy,
		registry:            registry,
		ledger:              ledger,
		usages:              make(map[string]ProviderUsageSnapshot),
		traces:              make(map[string]RequestTrace),
		routeStats:          make(map[string]RoutingRuleStats),
		auditEvents:         make(map[string]AuditEvent),
		authRecords:         make(map[string]AuthRecord),
		authRaw:             make(map[string][]byte),
		authRawMeta:         make(map[string]authRawMetadata),
		notifierStatuses:    make(map[string]NotifierStatus),
		users:               make(map[string]RouterUser),
		routingRules:        make(map[string]RoutingRule),
		nodes:               make(map[string]NodeSnapshot),
		containers:          make(map[string]ContainerSnapshot),
		controlSessions:     make(map[string]*controlSession),
		pendingAuthRefresh:  make(map[string]chan control.AuthRefreshResult),
		pendingAuthRequests: make(map[string]control.AuthRefreshRequest),
	}, nil
}

func (e *Engine) SetInvoker(invoker Invoker) {
	if e == nil {
		return
	}
	e.invoker = invoker
	if availability, ok := invoker.(ProviderAvailability); ok {
		e.availability = availability
		return
	}
	e.availability = nil
}

func (e *Engine) DryRun(request RouteRequest) RouteDecision {
	return e.dryRunWithSkippedModels(request, nil)
}

func (e *Engine) dryRunWithSkippedModels(request RouteRequest, skippedModels []string) RouteDecision {
	if e == nil || e.registry == nil {
		return RouteDecision{Reason: ErrRouterNotReady.Error()}
	}
	if strings.TrimSpace(request.RoutingRuleID) != "" || strings.TrimSpace(request.RoutingRuleName) != "" {
		if rule, ok := e.resolveRoutingRuleForRequest(request); ok {
			decision, _ := e.evaluateRoutingRuleWithSkippedModels(rule, request, skippedModels)
			return decision
		}
		return RouteDecision{
			Allowed:    false,
			RouteID:    request.RoutingRuleID,
			Reason:     ErrNoRoute.Error(),
			ModelAlias: request.Model,
			Rejections: []RouteRejection{{Reason: "routing rule not found"}},
		}
	}
	return e.policy.Evaluate(request, e.routingRegistrations())
}

func (e *Engine) resolveRoutingRuleForRequest(request RouteRequest) (RoutingRule, bool) {
	if e == nil {
		return RoutingRule{}, false
	}
	if id := strings.TrimSpace(request.RoutingRuleID); id != "" {
		if rule, ok := e.GetRoutingRule(id); ok {
			return rule, true
		}
	}
	name := strings.TrimSpace(request.RoutingRuleName)
	if name == "" {
		return RoutingRule{}, false
	}
	owner := firstNonEmpty(request.RoutingRuleOwner, request.UserID)
	if owner != "" {
		if rule, ok := e.FindRoutingRule(RoutingRuleScopeUser, owner, name); ok {
			return rule, true
		}
	}
	return e.FindRoutingRule(RoutingRuleScopePublic, "", name)
}

func (e *Engine) Models() []ModelInfo {
	if e == nil {
		return nil
	}
	models := make([]ModelInfo, 0, len(e.policy.ModelAliases))
	for id, alias := range e.policy.ModelAliases {
		contextTokens, maxContextTokens, maxOutputTokens := e.modelTokenMetadata(id, alias)
		models = append(models, ModelInfo{
			ID:               id,
			CanonicalModel:   alias.CanonicalModel,
			Capabilities:     append([]provider.Capability(nil), alias.RequiredCapabilities...),
			ContextTokens:    contextTokens,
			MaxContextTokens: maxContextTokens,
			MaxOutputTokens:  maxOutputTokens,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func (e *Engine) modelTokenMetadata(id string, alias ModelAlias) (int, int, int) {
	if e == nil || e.registry == nil {
		return 0, 0, 0
	}
	names := map[string]struct{}{strings.ToLower(strings.TrimSpace(id)): {}}
	for _, value := range append([]string{alias.CanonicalModel}, alias.CanonicalModels...) {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			names[value] = struct{}{}
		}
	}
	for _, registration := range e.registry.List() {
		for _, model := range registration.Models {
			if _, ok := names[strings.ToLower(strings.TrimSpace(model.ID))]; !ok {
				for _, modelAlias := range model.Aliases {
					if _, ok = names[strings.ToLower(strings.TrimSpace(modelAlias))]; ok {
						break
					}
				}
				if !ok {
					continue
				}
			}
			return model.ContextTokens, model.MaxContextTokens, model.MaxOutputTokens
		}
	}
	return 0, 0, 0
}

func (e *Engine) Providers() []provider.Registration {
	if e == nil || e.registry == nil {
		return nil
	}
	return recoverStaleTransientDegradations(e.registry.List(), time.Now().UTC())
}

func (e *Engine) routingRegistrations() []provider.Registration {
	availability := e.availability
	load, _ := e.invoker.(ProviderLoad)
	now := time.Now().UTC()
	registrations := recoverStaleTransientDegradations(e.registry.List(), now)
	if availability == nil && load == nil {
		return registrations
	}
	out := make([]provider.Registration, len(registrations))
	for i, registration := range registrations {
		providerInstanceID := registration.Identity.ProviderInstanceID
		if load != nil {
			queueDepth := load.ProviderQueueDepth(providerInstanceID)
			if queueDepth > registration.Limits.QueueDepth {
				registration.Limits.QueueDepth = queueDepth
			}
			if queueDepth > registration.Limits.ActiveStreams {
				registration.Limits.ActiveStreams = queueDepth
			}
		}
		if availability != nil && !availability.ProviderAvailable(providerInstanceID) {
			registration.Health.Status = provider.HealthDown
			registration.Health.Reason = "data session disconnected"
			registration.Health.CheckedAt = now
		}
		out[i] = registration
	}
	return out
}

func recoverStaleTransientDegradations(registrations []provider.Registration, now time.Time) []provider.Registration {
	out := make([]provider.Registration, len(registrations))
	for i, registration := range registrations {
		out[i] = recoverStaleTransientDegradation(registration, now)
	}
	return out
}

func recoverStaleTransientDegradation(registration provider.Registration, now time.Time) provider.Registration {
	if registration.Health.Status != provider.HealthDegraded {
		return registration
	}
	if !isTransientDegradedHealthReason(registration.Health.Reason) {
		return registration
	}
	if registration.Health.CheckedAt.IsZero() || now.Sub(registration.Health.CheckedAt) < staleTransientDegradedAfter {
		return registration
	}
	registration.Health.Status = provider.HealthReady
	registration.Health.Reason = ""
	registration.Health.CheckedAt = now
	return registration
}

func isTransientDegradedHealthReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(reason, "upstream rate limited"):
		return true
	case strings.Contains(reason, "upstream request failed"):
		return true
	case strings.Contains(reason, "provider invoke failed"):
		return true
	default:
		return false
	}
}

func (e *Engine) UpsertProvider(registration provider.Registration) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
	registration.Identity.Account = registration.Identity.Account.MergeMissingFrom(registration.Auth.Account)
	return e.registry.Upsert(registration)
}

func (e *Engine) UpdateProviderHeartbeat(providerInstanceID string, health provider.Health, auth provider.AuthState, limits provider.LimitState) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return provider.ErrProviderNotFound
	}
	if health.Status != "" {
		registration.Health = health
	}
	if auth.Status != "" {
		registration.Auth = auth
		registration.Identity.Account = registration.Identity.Account.MergeMissingFrom(auth.Account)
	}
	if limits != (provider.LimitState{}) {
		registration.Limits = limits
	}
	return e.registry.Upsert(registration)
}

func (e *Engine) UpdateProviderAuth(providerInstanceID string, auth provider.AuthState) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return provider.ErrProviderNotFound
	}
	switch auth.Status {
	case provider.AuthRefreshing:
		registration.Health.Status = provider.HealthAuthUpdating
		registration.Health.Reason = "auth updating"
		registration.Health.CheckedAt = time.Now().UTC()
	default:
		if registration.Health.Status == provider.HealthAuthUpdating {
			registration.Health.Status = provider.HealthReady
			registration.Health.Reason = ""
			registration.Health.CheckedAt = time.Now().UTC()
		}
	}
	registration.Auth = auth
	registration.Identity.Account = registration.Identity.Account.MergeMissingFrom(auth.Account)
	return e.registry.Upsert(registration)
}

type ProviderUsageSnapshot struct {
	ProviderInstanceID string               `json:"provider_instance_id"`
	ProviderType       string               `json:"provider_type"`
	NodeID             string               `json:"node_id"`
	HostName           string               `json:"host_name"`
	ContainerID        string               `json:"container_id,omitempty"`
	ContainerKind      string               `json:"container_kind,omitempty"`
	ContainerName      string               `json:"container_name,omitempty"`
	Service            provider.Service     `json:"service"`
	Kind               provider.Kind        `json:"kind"`
	Account            provider.Account     `json:"account,omitempty"`
	Usage              provider.UsageReport `json:"usage"`
	ReportedAt         time.Time            `json:"reported_at,omitempty"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

func (e *Engine) UpdateProviderUsage(providerInstanceID string, usage provider.UsageReport, reportedAt time.Time) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return provider.ErrProviderNotFound
	}
	now := time.Now().UTC()
	if reportedAt.IsZero() {
		reportedAt = usage.ObservedAt
	}
	if reportedAt.IsZero() {
		reportedAt = now
	}
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = reportedAt
	}
	identity := registration.Identity
	identity.Account = identity.Account.MergeMissingFrom(registration.Auth.Account)
	snapshot := ProviderUsageSnapshot{
		ProviderInstanceID: identity.ProviderInstanceID,
		ProviderType:       identity.ProviderType,
		NodeID:             identity.NodeID,
		HostName:           identity.HostName,
		ContainerID:        identity.ContainerID,
		ContainerKind:      identity.ContainerKind,
		ContainerName:      identity.ContainerName,
		Service:            identity.Service,
		Kind:               identity.Kind,
		Account:            identity.Account,
		Usage:              usage,
		ReportedAt:         reportedAt,
		UpdatedAt:          now,
	}
	e.usageMu.Lock()
	if e.usages == nil {
		e.usages = make(map[string]ProviderUsageSnapshot)
	}
	previous, hadPrevious := e.usages[providerInstanceID]
	e.usages[providerInstanceID] = snapshot
	e.usageMu.Unlock()
	if hadPrevious {
		e.recordUsageWindowChanges(registration, previous, snapshot)
	}
	return nil
}

func (e *Engine) ProviderUsages() []ProviderUsageSnapshot {
	if e == nil {
		return nil
	}
	e.usageMu.RLock()
	defer e.usageMu.RUnlock()
	out := make([]ProviderUsageSnapshot, 0, len(e.usages))
	for _, usage := range e.usages {
		out = append(out, usage)
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i]
		b := out[j]
		switch {
		case a.HostName != b.HostName:
			return a.HostName < b.HostName
		case a.Service != b.Service:
			return a.Service < b.Service
		case a.ProviderType != b.ProviderType:
			return a.ProviderType < b.ProviderType
		case a.Account.Display != b.Account.Display:
			return a.Account.Display < b.Account.Display
		default:
			return a.ProviderInstanceID < b.ProviderInstanceID
		}
	})
	return out
}

func (e *Engine) providerUsagesInRecordOrder() []ProviderUsageSnapshot {
	if e == nil {
		return nil
	}
	e.usageMu.RLock()
	defer e.usageMu.RUnlock()
	out := make([]ProviderUsageSnapshot, 0, len(e.usages))
	for _, usage := range e.usages {
		out = append(out, usage)
	}
	return out
}

func (e *Engine) restoreProviderUsages(usages []ProviderUsageSnapshot) {
	if e == nil || len(usages) == 0 {
		return
	}
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	if e.usages == nil {
		e.usages = make(map[string]ProviderUsageSnapshot, len(usages))
	}
	for _, usage := range usages {
		if strings.TrimSpace(usage.ProviderInstanceID) == "" {
			continue
		}
		e.usages[usage.ProviderInstanceID] = usage
	}
}

func (e *Engine) restoreAuthEvents(events []AuthEvent) {
	if e == nil || len(events) == 0 {
		return
	}
	e.authMu.Lock()
	defer e.authMu.Unlock()
	if len(events) > maxAuthEvents {
		events = events[len(events)-maxAuthEvents:]
	}
	e.authEvents = make([]AuthEvent, 0, len(events))
	for _, event := range events {
		if event.AuthID == "" || event.Type == "" {
			continue
		}
		if len(event.Details) > 0 {
			event.Details = cloneStringMap(event.Details)
		}
		e.authEvents = append(e.authEvents, event)
	}
}

func (e *Engine) recordUsageWindowChanges(registration provider.Registration, previous ProviderUsageSnapshot, next ProviderUsageSnapshot) {
	if e == nil {
		return
	}
	previousWindows := quotaWindowMap(routerProviderQuotaWindows(registration, previous))
	nextWindows := quotaWindowMap(routerProviderQuotaWindows(registration, next))
	if len(previousWindows) == 0 || len(nextWindows) == 0 {
		return
	}
	for key, nextWindow := range nextWindows {
		previousWindow, ok := previousWindows[key]
		if !ok || !isQuotaWindowReset(previousWindow, nextWindow, next.ReportedAt) {
			continue
		}
		e.recordUsageWindowResetAuthEvent(registration, previous, next, previousWindow, nextWindow)
	}
}

func quotaWindowMap(windows []routerQuotaWindow) map[string]routerQuotaWindow {
	out := make(map[string]routerQuotaWindow, len(windows))
	for _, window := range windows {
		key := quotaWindowKey(window)
		if key == "" {
			continue
		}
		out[key] = window
	}
	return out
}

func quotaWindowKey(window routerQuotaWindow) string {
	label := strings.ToLower(strings.TrimSpace(window.Label))
	if label == "" {
		return ""
	}
	source := strings.ToLower(strings.TrimSpace(window.Source))
	unit := strings.ToLower(strings.TrimSpace(window.Unit))
	return label + "|" + source + "|" + unit
}

func isQuotaWindowReset(previous routerQuotaWindow, next routerQuotaWindow, reportedAt time.Time) bool {
	if previous.ResetAt.IsZero() || next.ResetAt.IsZero() {
		return false
	}
	if !next.ResetAt.After(previous.ResetAt.Add(time.Minute)) {
		return false
	}
	if previous.Used > 0 && next.Used < previous.Used {
		return true
	}
	if next.Limit > 0 && previous.Limit > 0 && next.Used < previous.Used {
		return true
	}
	if next.RemainingPct > previous.RemainingPct+0.01 {
		return true
	}
	if !reportedAt.IsZero() && !previous.ResetAt.After(reportedAt.Add(time.Minute)) {
		return true
	}
	return false
}

func (e *Engine) recordUsageWindowResetAuthEvent(registration provider.Registration, previous ProviderUsageSnapshot, next ProviderUsageSnapshot, previousWindow routerQuotaWindow, nextWindow routerQuotaWindow) {
	identity := registration.Identity
	account := identity.Account.MergeMissingFrom(registration.Auth.Account)
	authID := authRecordID(identity.Service, account, identity.ProviderInstanceID)
	at := next.ReportedAt
	if at.IsZero() {
		at = next.UpdatedAt
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	label := firstNonEmpty(strings.TrimSpace(nextWindow.Label), strings.TrimSpace(previousWindow.Label), "quota window")
	message := fmt.Sprintf(
		"Quota window %q reset: reset_at moved from %s to %s; usage moved from %s to %s.",
		label,
		formatAuthEventTime(previousWindow.ResetAt),
		formatAuthEventTime(nextWindow.ResetAt),
		describeQuotaWindowUsage(previousWindow),
		describeQuotaWindowUsage(nextWindow),
	)
	details := map[string]string{
		"window":               label,
		"source":               firstNonEmpty(nextWindow.Source, previousWindow.Source),
		"previous_reset_at":    previousWindow.ResetAt.UTC().Format(time.RFC3339),
		"new_reset_at":         nextWindow.ResetAt.UTC().Format(time.RFC3339),
		"previous_usage":       describeQuotaWindowUsage(previousWindow),
		"new_usage":            describeQuotaWindowUsage(nextWindow),
		"previous_reported_at": formatOptionalAuthEventTime(previous.ReportedAt),
		"new_reported_at":      formatOptionalAuthEventTime(next.ReportedAt),
	}
	if previousWindow.Unit != "" || nextWindow.Unit != "" {
		details["unit"] = firstNonEmpty(nextWindow.Unit, previousWindow.Unit)
	}
	if previousWindow.RemainingPct != 0 || nextWindow.RemainingPct != 0 {
		details["previous_remaining_pct"] = fmt.Sprintf("%.1f", previousWindow.RemainingPct)
		details["new_remaining_pct"] = fmt.Sprintf("%.1f", nextWindow.RemainingPct)
		details["previous_used_pct"] = fmt.Sprintf("%.1f", 100-previousWindow.RemainingPct)
		details["new_used_pct"] = fmt.Sprintf("%.1f", 100-nextWindow.RemainingPct)
	}
	if previousWindow.Limit > 0 || nextWindow.Limit > 0 || previousWindow.Used > 0 || nextWindow.Used > 0 {
		details["previous_used"] = fmt.Sprintf("%d", previousWindow.Used)
		details["previous_limit"] = fmt.Sprintf("%d", previousWindow.Limit)
		details["new_used"] = fmt.Sprintf("%d", nextWindow.Used)
		details["new_limit"] = fmt.Sprintf("%d", nextWindow.Limit)
	}
	e.authMu.Lock()
	defer e.authMu.Unlock()
	e.appendAuthEventLocked(AuthEvent{
		AuthID:             authID,
		Type:               authEventUsageQuotaWindowReset,
		Service:            identity.Service,
		Account:            account,
		ProviderType:       identity.ProviderType,
		ProviderInstanceID: identity.ProviderInstanceID,
		NodeID:             identity.NodeID,
		HostName:           identity.HostName,
		Status:             registration.Auth.Status,
		Source:             firstNonEmpty(next.Usage.Source, previous.Usage.Source),
		Message:            message,
		Details:            details,
		At:                 at,
	})
}

func describeQuotaWindowUsage(window routerQuotaWindow) string {
	if window.Limit > 0 {
		unit := strings.TrimSpace(window.Unit)
		if unit != "" {
			return fmt.Sprintf("%d/%d %s used, %.1f%% remaining", window.Used, window.Limit, unit, window.RemainingPct)
		}
		return fmt.Sprintf("%d/%d used, %.1f%% remaining", window.Used, window.Limit, window.RemainingPct)
	}
	if window.RemainingPct != 0 {
		return fmt.Sprintf("%.1f%% used, %.1f%% remaining", 100-window.RemainingPct, window.RemainingPct)
	}
	if window.Used > 0 {
		return fmt.Sprintf("%d used", window.Used)
	}
	return "0.0% used, 100.0% remaining"
}

func formatAuthEventTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}

func formatOptionalAuthEventTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (e *Engine) ReserveRoute(request RouteExecutionRequest) (RouteExecution, error) {
	return e.reserveRouteWithSkippedModels(request, nil)
}

func (e *Engine) reserveRouteWithSkippedModels(request RouteExecutionRequest, skippedModels []string) (RouteExecution, error) {
	if e == nil || e.registry == nil || e.ledger == nil {
		return RouteExecution{}, ErrRouterNotReady
	}
	decision := e.dryRunWithSkippedModels(request.RouteRequest, skippedModels)
	if !decision.Allowed {
		err := ErrNoProvider
		if decision.Reason == ErrNoRoute.Error() {
			err = ErrNoRoute
		}
		return RouteExecution{Decision: decision}, err
	}
	scope := request.QuotaScope
	if scope.Model == "" {
		scope.Model = decision.CanonicalModel
	}
	reservation, err := e.ledger.Reserve(quota.ReservationRequest{
		RequestID: request.RequestID,
		Scope:     scope,
		Estimate:  request.QuotaEstimate,
	})
	if err != nil {
		decision.Allowed = false
		decision.Reason = err.Error()
		decision.Rejections = append(decision.Rejections, RouteRejection{Reason: err.Error()})
		return RouteExecution{Decision: decision}, err
	}
	return RouteExecution{Decision: decision, Reservation: reservation}, nil
}

func (e *Engine) Commit(requestID string, usage quota.Usage) (quota.Reservation, error) {
	if e == nil || e.ledger == nil {
		return quota.Reservation{}, ErrRouterNotReady
	}
	return e.ledger.Commit(requestID, usage)
}

func (e *Engine) Release(requestID string) (quota.Reservation, error) {
	if e == nil || e.ledger == nil {
		return quota.Reservation{}, ErrRouterNotReady
	}
	return e.ledger.Release(requestID)
}

func (e *Engine) SetQuotaLimit(scope quota.Scope, limit quota.Limit) error {
	if e == nil || e.ledger == nil {
		return ErrRouterNotReady
	}
	return e.ledger.SetLimit(scope, limit)
}

func (e *Engine) QuotaSnapshot(scope quota.Scope) (quota.SnapshotRecord, error) {
	if e == nil || e.ledger == nil {
		return quota.SnapshotRecord{}, ErrRouterNotReady
	}
	limit, committed, reserved, err := e.ledger.Snapshot(scope)
	if err != nil {
		return quota.SnapshotRecord{}, err
	}
	return quota.SnapshotRecord{Scope: scope, Limit: limit, Committed: committed, Reserved: reserved}, nil
}

func (e *Engine) QuotaSnapshots() []quota.SnapshotRecord {
	if e == nil || e.ledger == nil {
		return nil
	}
	return e.ledger.Snapshots()
}

func (e *Engine) Invoke(ctx context.Context, execution RouteExecutionRequest, request compat.Request) (compat.Response, RouteExecution, error) {
	if e == nil || e.invoker == nil {
		err := fmt.Errorf("%w: provider invoker is nil", ErrRouterNotReady)
		if e != nil {
			e.recordRequestTrace(newRequestTrace(execution, RouteExecution{}, quota.Usage{}, "failed", err, time.Now().UTC(), time.Now().UTC()))
		}
		return compat.Response{}, RouteExecution{}, err
	}
	startedAt := time.Now().UTC()
	skippedModels := make([]string, 0)
	accumulatedRejections := make([]RouteRejection, 0)
	var previousFallback *RouteDecisionEvent
	for attempt := 0; attempt < maxRoutingRuleModelFallbackAttempts; attempt++ {
		routeExecution, err := e.reserveRouteWithSkippedModels(execution, skippedModels)
		if previousFallback != nil {
			previousFallback.ModelAlias = routeExecution.Decision.ModelAlias
			previousFallback.CanonicalModel = routeExecution.Decision.CanonicalModel
			if previousFallback.RoutingRuleID == "" {
				previousFallback.RoutingRuleID = routeExecution.Decision.RoutingRuleID
			}
			routeExecution.Decision.Events = append(routeExecution.Decision.Events, *previousFallback)
			previousFallback = nil
		}
		attemptDecisionRejections := append([]RouteRejection(nil), routeExecution.Decision.Rejections...)
		if len(accumulatedRejections) > 0 {
			routeExecution.Decision.Rejections = append(accumulatedRejections, routeExecution.Decision.Rejections...)
		}
		if err != nil {
			e.recordRequestTrace(newRequestTrace(execution, routeExecution, quota.Usage{}, "rejected", err, startedAt, time.Now().UTC()))
			return compat.Response{}, routeExecution, err
		}
		if routeExecution.Decision.SelectedProvider == nil {
			if released, releaseErr := e.Release(execution.RequestID); releaseErr == nil {
				routeExecution.Reservation = released
			}
			e.recordRequestTrace(newRequestTrace(execution, routeExecution, quota.Usage{}, "rejected", ErrNoProvider, startedAt, time.Now().UTC()))
			return compat.Response{}, routeExecution, ErrNoProvider
		}
		attemptRequest := request
		if routeExecution.Decision.CanonicalModel != "" {
			attemptRequest.Model = routeExecution.Decision.CanonicalModel
		}
		if attemptRequest.ID == "" {
			attemptRequest.ID = execution.RequestID
		}
		candidates := e.executionCandidates(routeExecution.Decision)
		var response compat.Response
		var invokeErr error
		var invokeRejections []RouteRejection
		finalExecution := routeExecution
		for _, candidate := range candidates {
			candidateExecution := routeExecution
			candidateExecution.Decision.Selected = candidate.Identity.ProviderInstanceID
			candidateExecution.Decision.SelectedProvider = &candidate
			candidateExecution.Decision.Reason = "selected provider"
			response, invokeErr = e.invoker.Invoke(ctx, candidate, attemptRequest)
			if invokeErr == nil {
				finalExecution = candidateExecution
				break
			}
			e.markProviderUnavailableFromInvokeError(candidate.Identity.ProviderInstanceID, invokeErr)
			invokeRejections = append(invokeRejections, RouteRejection{
				ProviderInstanceID: candidate.Identity.ProviderInstanceID,
				ProviderType:       candidate.Identity.ProviderType,
				Reason:             "invoke failed: " + invokeErr.Error(),
			})
			if ctx.Err() != nil {
				break
			}
		}
		if invokeErr != nil {
			if released, releaseErr := e.Release(execution.RequestID); releaseErr == nil {
				finalExecution.Reservation = released
			}
			if cause, ok := e.routingRuleModelFallbackCause(execution.RouteRequest, finalExecution.Decision, skippedModels, invokeErr, false); ok {
				failedAlias, failedCanonical := finalExecution.Decision.ModelAlias, finalExecution.Decision.CanonicalModel
				attemptRejections := append([]RouteRejection(nil), attemptDecisionRejections...)
				attemptRejections = append(attemptRejections, invokeRejections...)
				finalExecution.Decision.Rejections = append(accumulatedRejections, attemptRejections...)
				accumulatedRejections = append(accumulatedRejections, attemptRejections...)
				accumulatedRejections = append(accumulatedRejections, RouteRejection{
					ProviderInstanceID: finalExecution.Decision.Selected,
					Reason:             fmt.Sprintf("%s for %q; trying next route model", cause.RejectionPrefix, firstNonEmpty(failedCanonical, failedAlias)),
				})
				skippedModels = appendRouteDecisionModelSkips(skippedModels, finalExecution.Decision)
				previousFallback = &RouteDecisionEvent{
					Type:                   cause.EventType,
					Message:                cause.Message,
					RoutingRuleID:          finalExecution.Decision.RoutingRuleID,
					PreviousModelAlias:     failedAlias,
					PreviousCanonicalModel: failedCanonical,
				}
				continue
			}
			finalExecution.Decision.Rejections = append(finalExecution.Decision.Rejections, invokeRejections...)
			e.recordRequestTrace(newRequestTrace(execution, finalExecution, quota.Usage{}, "provider_error", invokeErr, startedAt, time.Now().UTC()))
			return compat.Response{}, finalExecution, invokeErr
		}
		if len(invokeRejections) > 0 {
			finalExecution.Decision.Rejections = append(finalExecution.Decision.Rejections, invokeRejections...)
			finalExecution.Decision.Reason = "fallback selected after provider invoke failure"
		}
		actualUsage := quota.Usage{
			Tokens:   response.Usage.TotalTokens,
			Requests: 1,
		}
		committed, err := e.Commit(execution.RequestID, actualUsage)
		if err != nil {
			e.recordRequestTrace(newRequestTrace(execution, finalExecution, actualUsage, "failed", err, startedAt, time.Now().UTC()))
			return compat.Response{}, finalExecution, err
		}
		traceExecution := finalExecution
		traceExecution.Reservation = committed
		e.recordRequestTrace(newRequestTrace(execution, traceExecution, actualUsage, "completed", nil, startedAt, time.Now().UTC()))
		return response, finalExecution, nil
	}
	err := fmt.Errorf("%w: routing rule model fallback limit reached", ErrNoProvider)
	finalExecution := RouteExecution{Decision: RouteDecision{Reason: err.Error(), Rejections: accumulatedRejections}}
	e.recordRequestTrace(newRequestTrace(execution, finalExecution, quota.Usage{}, "provider_error", err, startedAt, time.Now().UTC()))
	return compat.Response{}, finalExecution, err
}

func (e *Engine) InvokeStream(ctx context.Context, execution RouteExecutionRequest, request compat.Request, emit func(compat.Event) error) (compat.Response, RouteExecution, error) {
	if e == nil || e.invoker == nil {
		err := fmt.Errorf("%w: provider invoker is nil", ErrRouterNotReady)
		if e != nil {
			e.recordRequestTrace(newRequestTrace(execution, RouteExecution{}, quota.Usage{}, "failed", err, time.Now().UTC(), time.Now().UTC()))
		}
		return compat.Response{}, RouteExecution{}, err
	}
	if emit == nil {
		err := fmt.Errorf("%w: stream emit callback is nil", ErrRouterNotReady)
		if e != nil {
			e.recordRequestTrace(newRequestTrace(execution, RouteExecution{}, quota.Usage{}, "failed", err, time.Now().UTC(), time.Now().UTC()))
		}
		return compat.Response{}, RouteExecution{}, err
	}
	streamInvoker, ok := e.invoker.(StreamInvoker)
	if !ok {
		response, routeExecution, err := e.Invoke(ctx, execution, request)
		if err != nil {
			return compat.Response{}, routeExecution, err
		}
		events, err := compat.EventsFromResponse(response)
		if err != nil {
			return compat.Response{}, routeExecution, err
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return compat.Response{}, routeExecution, err
			}
		}
		return response, routeExecution, nil
	}
	startedAt := time.Now().UTC()
	skippedModels := make([]string, 0)
	accumulatedRejections := make([]RouteRejection, 0)
	var previousFallback *RouteDecisionEvent
	for attempt := 0; attempt < maxRoutingRuleModelFallbackAttempts; attempt++ {
		routeExecution, err := e.reserveRouteWithSkippedModels(execution, skippedModels)
		if previousFallback != nil {
			previousFallback.ModelAlias = routeExecution.Decision.ModelAlias
			previousFallback.CanonicalModel = routeExecution.Decision.CanonicalModel
			if previousFallback.RoutingRuleID == "" {
				previousFallback.RoutingRuleID = routeExecution.Decision.RoutingRuleID
			}
			routeExecution.Decision.Events = append(routeExecution.Decision.Events, *previousFallback)
			previousFallback = nil
		}
		attemptDecisionRejections := append([]RouteRejection(nil), routeExecution.Decision.Rejections...)
		if len(accumulatedRejections) > 0 {
			routeExecution.Decision.Rejections = append(accumulatedRejections, routeExecution.Decision.Rejections...)
		}
		if err != nil {
			e.recordRequestTrace(newRequestTrace(execution, routeExecution, quota.Usage{}, "rejected", err, startedAt, time.Now().UTC()))
			return compat.Response{}, routeExecution, err
		}
		if routeExecution.Decision.SelectedProvider == nil {
			if released, releaseErr := e.Release(execution.RequestID); releaseErr == nil {
				routeExecution.Reservation = released
			}
			e.recordRequestTrace(newRequestTrace(execution, routeExecution, quota.Usage{}, "rejected", ErrNoProvider, startedAt, time.Now().UTC()))
			return compat.Response{}, routeExecution, ErrNoProvider
		}
		attemptRequest := request
		if routeExecution.Decision.CanonicalModel != "" {
			attemptRequest.Model = routeExecution.Decision.CanonicalModel
		}
		if attemptRequest.ID == "" {
			attemptRequest.ID = execution.RequestID
		}
		attemptRequest.Stream = true
		candidates := e.executionCandidates(routeExecution.Decision)
		var response compat.Response
		var invokeErr error
		var invokeRejections []RouteRejection
		finalExecution := routeExecution
		emitted := false
		wrappedEmit := func(event compat.Event) error {
			if event.Type == compat.EventError && !emitted {
				return nil
			}
			emitted = true
			return emit(event)
		}
		for _, candidate := range candidates {
			candidateExecution := routeExecution
			candidateExecution.Decision.Selected = candidate.Identity.ProviderInstanceID
			candidateExecution.Decision.SelectedProvider = &candidate
			candidateExecution.Decision.Reason = "selected provider"
			response, invokeErr = streamInvoker.InvokeStream(ctx, candidate, attemptRequest, wrappedEmit)
			if invokeErr == nil {
				finalExecution = candidateExecution
				break
			}
			e.markProviderUnavailableFromInvokeError(candidate.Identity.ProviderInstanceID, invokeErr)
			invokeRejections = append(invokeRejections, RouteRejection{
				ProviderInstanceID: candidate.Identity.ProviderInstanceID,
				ProviderType:       candidate.Identity.ProviderType,
				Reason:             "invoke failed: " + invokeErr.Error(),
			})
			if ctx.Err() != nil || emitted {
				break
			}
		}
		if invokeErr != nil {
			if released, releaseErr := e.Release(execution.RequestID); releaseErr == nil {
				finalExecution.Reservation = released
			}
			if cause, ok := e.routingRuleModelFallbackCause(execution.RouteRequest, finalExecution.Decision, skippedModels, invokeErr, emitted); ok {
				failedAlias, failedCanonical := finalExecution.Decision.ModelAlias, finalExecution.Decision.CanonicalModel
				attemptRejections := append([]RouteRejection(nil), attemptDecisionRejections...)
				attemptRejections = append(attemptRejections, invokeRejections...)
				finalExecution.Decision.Rejections = append(accumulatedRejections, attemptRejections...)
				accumulatedRejections = append(accumulatedRejections, attemptRejections...)
				accumulatedRejections = append(accumulatedRejections, RouteRejection{
					ProviderInstanceID: finalExecution.Decision.Selected,
					Reason:             fmt.Sprintf("%s for %q; trying next route model", cause.RejectionPrefix, firstNonEmpty(failedCanonical, failedAlias)),
				})
				skippedModels = appendRouteDecisionModelSkips(skippedModels, finalExecution.Decision)
				previousFallback = &RouteDecisionEvent{
					Type:                   cause.EventType,
					Message:                cause.Message,
					RoutingRuleID:          finalExecution.Decision.RoutingRuleID,
					PreviousModelAlias:     failedAlias,
					PreviousCanonicalModel: failedCanonical,
				}
				continue
			}
			finalExecution.Decision.Rejections = append(finalExecution.Decision.Rejections, invokeRejections...)
			e.recordRequestTrace(newRequestTrace(execution, finalExecution, quota.Usage{}, "provider_error", invokeErr, startedAt, time.Now().UTC()))
			return compat.Response{}, finalExecution, invokeErr
		}
		if len(invokeRejections) > 0 {
			finalExecution.Decision.Rejections = append(finalExecution.Decision.Rejections, invokeRejections...)
			finalExecution.Decision.Reason = "fallback selected after provider invoke failure"
		}
		actualUsage := quota.Usage{
			Tokens:   response.Usage.TotalTokens,
			Requests: 1,
		}
		committed, err := e.Commit(execution.RequestID, actualUsage)
		if err != nil {
			e.recordRequestTrace(newRequestTrace(execution, finalExecution, actualUsage, "failed", err, startedAt, time.Now().UTC()))
			return compat.Response{}, finalExecution, err
		}
		traceExecution := finalExecution
		traceExecution.Reservation = committed
		e.recordRequestTrace(newRequestTrace(execution, traceExecution, actualUsage, "completed", nil, startedAt, time.Now().UTC()))
		return response, finalExecution, nil
	}
	err := fmt.Errorf("%w: routing rule model fallback limit reached", ErrNoProvider)
	finalExecution := RouteExecution{Decision: RouteDecision{Reason: err.Error(), Rejections: accumulatedRejections}}
	e.recordRequestTrace(newRequestTrace(execution, finalExecution, quota.Usage{}, "provider_error", err, startedAt, time.Now().UTC()))
	return compat.Response{}, finalExecution, err
}

func (e *Engine) executionCandidates(decision RouteDecision) []provider.Registration {
	if e == nil || e.registry == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]provider.Registration, 0, len(decision.FallbackChain))
	if decision.SelectedProvider != nil {
		selected := *decision.SelectedProvider
		out = append(out, selected)
		seen[selected.Identity.ProviderInstanceID] = struct{}{}
	}
	for _, providerInstanceID := range decision.FallbackChain {
		if _, ok := seen[providerInstanceID]; ok {
			continue
		}
		registration, ok := e.registry.Get(providerInstanceID)
		if !ok {
			continue
		}
		out = append(out, registration)
		seen[providerInstanceID] = struct{}{}
	}
	return out
}

func (e *Engine) markProviderUnavailableFromInvokeError(providerInstanceID string, err error) {
	if e == nil || e.registry == nil || err == nil {
		return
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return
	}
	if errors.Is(err, ErrNoDataSession) {
		registration.Health.Status = provider.HealthDown
		registration.Health.Reason = "data session disconnected"
		registration.Health.CheckedAt = time.Now().UTC()
		_ = e.registry.Upsert(registration)
		return
	}
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) {
		return
	}
	if isEmptyStreamUpstreamError(upstream) {
		return
	}
	if isMalformedToolCallUpstreamError(upstream) {
		return
	}
	now := time.Now().UTC()
	switch upstream.StatusCode {
	case 401, 403:
		registration.Auth.Status = provider.AuthUnavailable
		registration.Auth.LastRefreshErr = upstream.Error()
		registration.Health.Status = provider.HealthDown
		registration.Health.Reason = "upstream auth failed"
	case 429:
		if isModelScopedCapacityError(upstream) {
			return
		}
		registration.Health.Status = provider.HealthDegraded
		registration.Health.Reason = "upstream rate limited"
	case 400, 404:
		return
	default:
		registration.Health.Status = provider.HealthDegraded
		registration.Health.Reason = "upstream request failed"
	}
	registration.Health.CheckedAt = now
	_ = e.registry.Upsert(registration)
}

func isModelScopedCapacityError(upstream *provider.UpstreamError) bool {
	if upstream == nil {
		return false
	}
	combined := strings.ToLower(strings.TrimSpace(upstream.Code + " " + upstream.Message + " " + upstream.Body))
	if upstream.StatusCode != 0 && upstream.StatusCode != 429 {
		return false
	}
	return strings.Contains(combined, "no capacity available for model") ||
		strings.Contains(combined, "capacity on this model") ||
		(strings.Contains(combined, "resource_exhausted") && strings.Contains(combined, "capacity"))
}

func isModelScopedCapacityInvokeError(err error) bool {
	var upstream *provider.UpstreamError
	return errors.As(err, &upstream) && isModelScopedCapacityError(upstream)
}

func isEmptyStreamInvokeError(err error) bool {
	var upstream *provider.UpstreamError
	return errors.As(err, &upstream) && isEmptyStreamUpstreamError(upstream)
}

func isEmptyStreamUpstreamError(upstream *provider.UpstreamError) bool {
	if upstream == nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(upstream.Code))
	return code == "empty_stream_timeout" || code == "empty_stream"
}

func isMalformedToolCallInvokeError(err error) bool {
	var upstream *provider.UpstreamError
	return errors.As(err, &upstream) && isMalformedToolCallUpstreamError(upstream)
}

func isMalformedToolCallUpstreamError(upstream *provider.UpstreamError) bool {
	if upstream == nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(upstream.Code))
	return code == "malformed_tool_call"
}

func routingRuleModelFallbackCauseForError(err error) (routingRuleModelFallbackCause, bool) {
	switch {
	case isModelScopedCapacityInvokeError(err):
		return routingRuleModelFallbackCause{
			EventType:       routeDecisionEventModelFallback,
			Message:         "upstream reported model capacity exhausted; routing rule selected the next effective model",
			RejectionPrefix: "model capacity exhausted",
		}, true
	case isEmptyStreamInvokeError(err):
		return routingRuleModelFallbackCause{
			EventType:       routeDecisionEventModelEmptyStreamFallback,
			Message:         "upstream stream did not produce assistant content; routing rule selected the next effective model",
			RejectionPrefix: "model produced no assistant content",
		}, true
	case isMalformedToolCallInvokeError(err):
		return routingRuleModelFallbackCause{
			EventType:       routeDecisionEventModelToolCallFallback,
			Message:         "upstream emitted a malformed tool call; routing rule selected the next effective model",
			RejectionPrefix: "model produced malformed tool call",
		}, true
	default:
		return routingRuleModelFallbackCause{}, false
	}
}

func (e *Engine) routingRuleModelFallbackCause(request RouteRequest, decision RouteDecision, skippedModels []string, err error, streamEmitted bool) (routingRuleModelFallbackCause, bool) {
	if streamEmitted {
		return routingRuleModelFallbackCause{}, false
	}
	cause, ok := routingRuleModelFallbackCauseForError(err)
	if !ok {
		return routingRuleModelFallbackCause{}, false
	}
	if strings.TrimSpace(request.Model) != "" {
		return routingRuleModelFallbackCause{}, false
	}
	if strings.TrimSpace(request.RoutingRuleID) == "" && strings.TrimSpace(request.RoutingRuleName) == "" && strings.TrimSpace(decision.RoutingRuleID) == "" {
		return routingRuleModelFallbackCause{}, false
	}
	for _, model := range routeDecisionModelSkipNames(decision) {
		if !stringInSet(model, skippedModels) {
			return cause, true
		}
	}
	return routingRuleModelFallbackCause{}, false
}

func appendRouteDecisionModelSkips(skippedModels []string, decision RouteDecision) []string {
	for _, model := range routeDecisionModelSkipNames(decision) {
		if stringInSet(model, skippedModels) {
			continue
		}
		skippedModels = append(skippedModels, model)
	}
	return skippedModels
}

func routeDecisionModelSkipNames(decision RouteDecision) []string {
	return uniqueStrings([]string{decision.CanonicalModel, decision.ModelAlias})
}
