package router

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

const defaultRequestTraceLimit = 512

type RequestTracePage struct {
	Traces  []RequestTrace `json:"traces"`
	Total   int            `json:"total"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
	HasMore bool           `json:"has_more"`
}

type RequestTrace struct {
	RequestID      string                     `json:"request_id"`
	RouteRequest   RouteRequest               `json:"route_request"`
	Decision       RouteDecision              `json:"decision"`
	Reservation    quota.Reservation          `json:"reservation,omitempty"`
	Provider       *provider.ProviderIdentity `json:"provider,omitempty"`
	HTTP           *RequestTraceHTTP          `json:"http,omitempty"`
	Status         string                     `json:"status"`
	Error          string                     `json:"error,omitempty"`
	ErrorCode      string                     `json:"error_code,omitempty"`
	ErrorStatus    int                        `json:"error_status,omitempty"`
	RetryAfter     string                     `json:"retry_after,omitempty"`
	EstimatedUsage quota.Usage                `json:"estimated_usage,omitempty"`
	ActualUsage    quota.Usage                `json:"actual_usage,omitempty"`
	StartedAt      time.Time                  `json:"started_at"`
	CompletedAt    time.Time                  `json:"completed_at"`
	DurationMS     int64                      `json:"duration_ms"`
}

type RequestTraceHTTP struct {
	Request  RequestTraceHTTPRequest  `json:"request"`
	Response RequestTraceHTTPResponse `json:"response"`
}

type RequestTraceHTTPRequest struct {
	Method  string                `json:"method"`
	Path    string                `json:"path"`
	Query   string                `json:"query,omitempty"`
	Headers map[string][]string   `json:"headers,omitempty"`
	Body    *RequestTraceHTTPBody `json:"body,omitempty"`
}

type RequestTraceHTTPResponse struct {
	Status  int                   `json:"status"`
	Headers map[string][]string   `json:"headers,omitempty"`
	Body    *RequestTraceHTTPBody `json:"body,omitempty"`
}

type RequestTraceHTTPBody struct {
	ContentType string            `json:"content_type,omitempty"`
	JSON        json.RawMessage   `json:"json,omitempty"`
	JSONL       []json.RawMessage `json:"jsonl,omitempty"`
	Text        string            `json:"text,omitempty"`
	Truncated   bool              `json:"truncated,omitempty"`
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
	errorCode := ""
	errorStatus := 0
	retryAfter := ""
	var upstream *provider.UpstreamError
	if errors.As(err, &upstream) && upstream != nil {
		errorCode = upstream.Code
		errorStatus = upstream.StatusCode
		retryAfter = upstream.RetryAfter
	}
	return RequestTrace{
		RequestID:      execution.RequestID,
		RouteRequest:   execution.RouteRequest,
		Decision:       routeExecution.Decision,
		Reservation:    routeExecution.Reservation,
		Provider:       identity,
		Status:         status,
		Error:          errorMessage,
		ErrorCode:      errorCode,
		ErrorStatus:    errorStatus,
		RetryAfter:     retryAfter,
		EstimatedUsage: execution.QuotaEstimate,
		ActualUsage:    actual,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		DurationMS:     completedAt.Sub(startedAt).Milliseconds(),
	}
}

func (e *Engine) AttachRequestTraceHTTP(requestID string, httpTrace RequestTraceHTTP) bool {
	if e == nil || requestID == "" {
		return false
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	trace, ok := e.traces[requestID]
	if !ok {
		return false
	}
	trace.HTTP = &httpTrace
	e.traces[requestID] = trace
	return true
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
	page := e.RequestTracesPage(0, limit)
	return page.Traces
}

func (e *Engine) RequestTracesPage(offset int, limit int) RequestTracePage {
	if e == nil {
		return RequestTracePage{}
	}
	if limit <= 0 || limit > defaultRequestTraceLimit {
		limit = defaultRequestTraceLimit
	}
	if offset < 0 {
		offset = 0
	}
	e.traceMu.RLock()
	defer e.traceMu.RUnlock()
	total := len(e.traces)
	out := make([]RequestTrace, 0, min(limit, max(total-offset, 0)))
	skipped := 0
	for i := len(e.traceIDs) - 1; i >= 0 && len(out) < limit; i-- {
		if trace, ok := e.traces[e.traceIDs[i]]; ok {
			if skipped < offset {
				skipped++
				continue
			}
			out = append(out, trace)
		}
	}
	if len(e.traceIDs) == 0 && len(e.traces) > 0 {
		all := make([]RequestTrace, 0, len(e.traces))
		for _, trace := range e.traces {
			all = append(all, trace)
		}
		sort.Slice(all, func(i, j int) bool {
			return all[i].StartedAt.After(all[j].StartedAt)
		})
		if offset < len(all) {
			end := min(offset+limit, len(all))
			out = all[offset:end]
		} else {
			out = nil
		}
	}
	if out == nil {
		out = []RequestTrace{}
	}
	return RequestTracePage{
		Traces:  out,
		Total:   total,
		Limit:   limit,
		Offset:  offset,
		HasMore: offset+len(out) < total,
	}
}

func (e *Engine) DeleteRequestTraces(requestIDs []string) int {
	if e == nil || len(requestIDs) == 0 {
		return 0
	}
	deleteSet := make(map[string]struct{}, len(requestIDs))
	for _, id := range requestIDs {
		if id != "" {
			deleteSet[id] = struct{}{}
		}
	}
	if len(deleteSet) == 0 {
		return 0
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	deleted := 0
	for id := range deleteSet {
		if _, ok := e.traces[id]; ok {
			delete(e.traces, id)
			deleted++
		}
	}
	if deleted == 0 {
		return 0
	}
	kept := e.traceIDs[:0]
	for _, id := range e.traceIDs {
		if _, remove := deleteSet[id]; !remove {
			kept = append(kept, id)
		}
	}
	e.traceIDs = kept
	return deleted
}
