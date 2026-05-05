package router

import (
	"errors"
	"fmt"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

var ErrRouterNotReady = errors.New("router not ready")

type Engine struct {
	policy   RoutingPolicy
	registry *provider.Registry
	ledger   *quota.Ledger
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

func (e *Engine) DryRun(request RouteRequest) RouteDecision {
	if e == nil || e.registry == nil {
		return RouteDecision{Reason: ErrRouterNotReady.Error()}
	}
	return e.policy.Evaluate(request, e.registry.List())
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
