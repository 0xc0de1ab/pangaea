package router

import (
	"sort"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

const defaultRequestTraceLimit = 512

type RequestTrace struct {
	RequestID      string                     `json:"request_id"`
	RouteRequest   RouteRequest               `json:"route_request"`
	Decision       RouteDecision              `json:"decision"`
	Reservation    quota.Reservation          `json:"reservation,omitempty"`
	Provider       *provider.ProviderIdentity `json:"provider,omitempty"`
	Status         string                     `json:"status"`
	Error          string                     `json:"error,omitempty"`
	EstimatedUsage quota.Usage                `json:"estimated_usage,omitempty"`
	ActualUsage    quota.Usage                `json:"actual_usage,omitempty"`
	StartedAt      time.Time                  `json:"started_at"`
	CompletedAt    time.Time                  `json:"completed_at"`
	DurationMS     int64                      `json:"duration_ms"`
}

func newRequestTrace(execution RouteExecutionRequest, routeExecution RouteExecution, actual quota.Usage, status string, err error, startedAt time.Time, completedAt time.Time) RequestTrace {
	if startedAt.IsZero() {
		startedAt = completedAt
	}
	if completedAt.IsZero() {
		completedAt = startedAt
	}
	var identity *provider.ProviderIdentity
	if routeExecution.Decision.SelectedProvider != nil {
		selected := routeExecution.Decision.SelectedProvider.Identity
		identity = &selected
	}
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	return RequestTrace{
		RequestID:      execution.RequestID,
		RouteRequest:   execution.RouteRequest,
		Decision:       routeExecution.Decision,
		Reservation:    routeExecution.Reservation,
		Provider:       identity,
		Status:         status,
		Error:          errorMessage,
		EstimatedUsage: execution.QuotaEstimate,
		ActualUsage:    actual,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		DurationMS:     completedAt.Sub(startedAt).Milliseconds(),
	}
}

func (e *Engine) recordRequestTrace(trace RequestTrace) {
	if e == nil || trace.RequestID == "" {
		return
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	if e.traces == nil {
		e.traces = make(map[string]RequestTrace)
	}
	if _, exists := e.traces[trace.RequestID]; !exists {
		e.traceIDs = append(e.traceIDs, trace.RequestID)
	}
	e.traces[trace.RequestID] = trace
	limit := defaultRequestTraceLimit
	for len(e.traceIDs) > limit {
		oldest := e.traceIDs[0]
		e.traceIDs = e.traceIDs[1:]
		delete(e.traces, oldest)
	}
}

func (e *Engine) RequestTrace(requestID string) (RequestTrace, bool) {
	if e == nil || requestID == "" {
		return RequestTrace{}, false
	}
	e.traceMu.RLock()
	defer e.traceMu.RUnlock()
	trace, ok := e.traces[requestID]
	return trace, ok
}

func (e *Engine) RequestTraces(limit int) []RequestTrace {
	if e == nil {
		return nil
	}
	if limit <= 0 || limit > defaultRequestTraceLimit {
		limit = defaultRequestTraceLimit
	}
	e.traceMu.RLock()
	defer e.traceMu.RUnlock()
	out := make([]RequestTrace, 0, min(limit, len(e.traces)))
	for i := len(e.traceIDs) - 1; i >= 0 && len(out) < limit; i-- {
		if trace, ok := e.traces[e.traceIDs[i]]; ok {
			out = append(out, trace)
		}
	}
	if len(e.traceIDs) == 0 && len(e.traces) > 0 {
		for _, trace := range e.traces {
			out = append(out, trace)
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].StartedAt.After(out[j].StartedAt)
		})
		if len(out) > limit {
			out = out[:limit]
		}
	}
	return out
}
