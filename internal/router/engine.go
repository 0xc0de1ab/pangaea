package router

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

var ErrRouterNotReady = errors.New("router not ready")

type Engine struct {
	policy   RoutingPolicy
	registry *provider.Registry
	ledger   *quota.Ledger
	invoker  Invoker
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

type ModelInfo struct {
	ID             string                `json:"id"`
	CanonicalModel string                `json:"canonical_model,omitempty"`
	Capabilities   []provider.Capability `json:"capabilities,omitempty"`
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
	return &Engine{policy: policy, registry: registry, ledger: ledger}, nil
}

func (e *Engine) SetInvoker(invoker Invoker) {
	if e == nil {
		return
	}
	e.invoker = invoker
}

func (e *Engine) DryRun(request RouteRequest) RouteDecision {
	if e == nil || e.registry == nil {
		return RouteDecision{Reason: ErrRouterNotReady.Error()}
	}
	return e.policy.Evaluate(request, e.registry.List())
}

func (e *Engine) Models() []ModelInfo {
	if e == nil {
		return nil
	}
	models := make([]ModelInfo, 0, len(e.policy.ModelAliases))
	for id, alias := range e.policy.ModelAliases {
		models = append(models, ModelInfo{
			ID:             id,
			CanonicalModel: alias.CanonicalModel,
			Capabilities:   append([]provider.Capability(nil), alias.RequiredCapabilities...),
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func (e *Engine) Providers() []provider.Registration {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.List()
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

func (e *Engine) Invoke(ctx context.Context, execution RouteExecutionRequest, request compat.Request) (compat.Response, RouteExecution, error) {
	if e == nil || e.invoker == nil {
		return compat.Response{}, RouteExecution{}, fmt.Errorf("%w: provider invoker is nil", ErrRouterNotReady)
	}
	routeExecution, err := e.ReserveRoute(execution)
	if err != nil {
		return compat.Response{}, routeExecution, err
	}
	if routeExecution.Decision.SelectedProvider == nil {
		_, _ = e.Release(execution.RequestID)
		return compat.Response{}, routeExecution, ErrNoProvider
	}
	if routeExecution.Decision.CanonicalModel != "" {
		request.Model = routeExecution.Decision.CanonicalModel
	}
	response, err := e.invoker.Invoke(ctx, *routeExecution.Decision.SelectedProvider, request)
	if err != nil {
		_, _ = e.Release(execution.RequestID)
		return compat.Response{}, routeExecution, err
	}
	if _, err := e.Commit(execution.RequestID, quota.Usage{
		Tokens:   response.Usage.TotalTokens,
		Requests: 1,
	}); err != nil {
		return compat.Response{}, routeExecution, err
	}
	return response, routeExecution, nil
}
