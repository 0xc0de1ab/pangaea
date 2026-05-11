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

type Engine struct {
	policy             RoutingPolicy
	registry           *provider.Registry
	ledger             *quota.Ledger
	invoker            Invoker
	availability       ProviderAvailability
	usageMu            sync.RWMutex
	usages             map[string]ProviderUsageSnapshot
	traceMu            sync.RWMutex
	traces             map[string]RequestTrace
	traceIDs           []string
	auditMu            sync.RWMutex
	auditEvents        map[string]AuditEvent
	auditIDs           []string
	auditSeq           uint64
	authMu             sync.RWMutex
	authRecords        map[string]AuthRecord
	authRaw            map[string][]byte
	authEvents         []AuthEvent
	notifierMu         sync.RWMutex
	notifierStatuses   map[string]NotifierStatus
	notifierHistory    []NotifierDelivery
	notifierSeq        uint64
	nodeMu             sync.RWMutex
	nodes              map[string]NodeSnapshot
	containers         map[string]ContainerSnapshot
	controlMu          sync.RWMutex
	controlSessions    map[string]*controlSession
	pendingAuthRefresh map[string]chan control.AuthRefreshResult
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
		policy:             policy,
		registry:           registry,
		ledger:             ledger,
		usages:             make(map[string]ProviderUsageSnapshot),
		traces:             make(map[string]RequestTrace),
		auditEvents:        make(map[string]AuditEvent),
		authRecords:        make(map[string]AuthRecord),
		authRaw:            make(map[string][]byte),
		notifierStatuses:   make(map[string]NotifierStatus),
		nodes:              make(map[string]NodeSnapshot),
		containers:         make(map[string]ContainerSnapshot),
		controlSessions:    make(map[string]*controlSession),
		pendingAuthRefresh: make(map[string]chan control.AuthRefreshResult),
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
	if e == nil || e.registry == nil {
		return RouteDecision{Reason: ErrRouterNotReady.Error()}
	}
	return e.policy.Evaluate(request, e.routingRegistrations())
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
	return e.registry.List()
}

func (e *Engine) routingRegistrations() []provider.Registration {
	registrations := e.registry.List()
	availability := e.availability
	load, _ := e.invoker.(ProviderLoad)
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
			registration.Health.CheckedAt = time.Now().UTC()
		}
		out[i] = registration
	}
	return out
}

func (e *Engine) UpsertProvider(registration provider.Registration) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
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
		registration.Identity.Account = accountWithFallback(registration.Identity.Account, auth.Account)
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
	registration.Identity.Account = accountWithFallback(registration.Identity.Account, auth.Account)
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
	identity.Account = accountWithFallback(identity.Account, registration.Auth.Account)
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
	defer e.usageMu.Unlock()
	if e.usages == nil {
		e.usages = make(map[string]ProviderUsageSnapshot)
	}
	e.usages[providerInstanceID] = snapshot
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

func (e *Engine) ReserveRoute(request RouteExecutionRequest) (RouteExecution, error) {
	if e == nil || e.registry == nil || e.ledger == nil {
		return RouteExecution{}, ErrRouterNotReady
	}
	decision := e.DryRun(request.RouteRequest)
	if !decision.Allowed {
		return RouteExecution{Decision: decision}, ErrNoProvider
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
	routeExecution, err := e.ReserveRoute(execution)
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
	if routeExecution.Decision.CanonicalModel != "" {
		request.Model = routeExecution.Decision.CanonicalModel
	}
	if request.ID == "" {
		request.ID = execution.RequestID
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
		response, invokeErr = e.invoker.Invoke(ctx, candidate, request)
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
	routeExecution, err := e.ReserveRoute(execution)
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
	if routeExecution.Decision.CanonicalModel != "" {
		request.Model = routeExecution.Decision.CanonicalModel
	}
	if request.ID == "" {
		request.ID = execution.RequestID
	}
	request.Stream = true
	candidates := e.executionCandidates(routeExecution.Decision)
	var response compat.Response
	var invokeErr error
	var invokeRejections []RouteRejection
	finalExecution := routeExecution
	emitted := false
	wrappedEmit := func(event compat.Event) error {
		emitted = true
		return emit(event)
	}
	for _, candidate := range candidates {
		candidateExecution := routeExecution
		candidateExecution.Decision.Selected = candidate.Identity.ProviderInstanceID
		candidateExecution.Decision.SelectedProvider = &candidate
		candidateExecution.Decision.Reason = "selected provider"
		response, invokeErr = streamInvoker.InvokeStream(ctx, candidate, request, wrappedEmit)
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
	now := time.Now().UTC()
	switch upstream.StatusCode {
	case 401, 403:
		registration.Auth.Status = provider.AuthUnavailable
		registration.Auth.LastRefreshErr = upstream.Error()
		registration.Health.Status = provider.HealthDown
		registration.Health.Reason = "upstream auth failed"
	case 429:
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
