package router

import (
	"sort"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

const (
	DefaultDashboardTraceLimit      = 25
	DashboardFreshnessStaleAfter    = 2 * time.Minute
	dashboardQuotaPressureThreshold = 0.8
)

type DashboardOverviewResponse struct {
	Summary DashboardSummary `json:"summary"`
}

type DashboardRoutesResponse struct {
	GeneratedAt time.Time   `json:"generated_at"`
	Routes      []RouteView `json:"routes"`
}

type DashboardProvidersResponse struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Summary     DashboardSummary `json:"summary"`
	Providers   []ProviderView   `json:"providers"`
}

type DashboardTracesResponse struct {
	GeneratedAt  time.Time      `json:"generated_at"`
	RecentErrors int            `json:"recent_errors"`
	Traces       []TraceSummary `json:"traces"`
}

type DashboardSummary struct {
	UpdatedAt             time.Time                     `json:"updated_at"`
	GeneratedAt           time.Time                     `json:"generated_at"`
	Router                DashboardRouterSummary        `json:"router"`
	Providers             DashboardProviderSummary      `json:"providers"`
	Requests              DashboardRequestSummary       `json:"requests"`
	Streams               DashboardStreamSummary        `json:"streams"`
	Sessions              DashboardSessionSummary       `json:"sessions"`
	Nodes                 DashboardStaleSummary         `json:"nodes"`
	Containers            DashboardStaleSummary         `json:"containers"`
	Auth                  DashboardAuthSummary          `json:"auth"`
	RoutesTotal           int                           `json:"routes_total"`
	ProvidersTotal        int                           `json:"providers_total"`
	ProvidersByHealth     map[provider.HealthStatus]int `json:"providers_by_health"`
	ProvidersByAuth       map[provider.AuthStatus]int   `json:"providers_by_auth"`
	NodesTotal            int                           `json:"nodes_total"`
	StaleNodes            int                           `json:"stale_nodes"`
	ContainersTotal       int                           `json:"containers_total"`
	StaleContainers       int                           `json:"stale_containers"`
	ActiveControlSessions int                           `json:"active_control_sessions"`
	ActiveDataSessions    int                           `json:"active_data_sessions"`
	RecentTraces          int                           `json:"recent_traces"`
	RecentErrors          int                           `json:"recent_errors"`
	QuotaPressure         DashboardQuotaPressureSummary `json:"quota_pressure"`
}

type DashboardRouterSummary struct {
	Status string `json:"status"`
}

type DashboardProviderSummary struct {
	Total    int                           `json:"total"`
	Ready    int                           `json:"ready"`
	Degraded int                           `json:"degraded"`
	Draining int                           `json:"draining"`
	Down     int                           `json:"down"`
	Unknown  int                           `json:"unknown"`
	ByHealth map[provider.HealthStatus]int `json:"by_health,omitempty"`
	ByAuth   map[provider.AuthStatus]int   `json:"by_auth,omitempty"`
}

type DashboardRequestSummary struct {
	Active         int `json:"active"`
	Recent         int `json:"recent"`
	RecentFailures int `json:"recent_failures"`
}

type DashboardStreamSummary struct {
	Active int `json:"active"`
}

type DashboardSessionSummary struct {
	ControlActive       int `json:"control_active"`
	DataActive          int `json:"data_active"`
	ControlDisconnected int `json:"control_disconnected"`
	DataDisconnected    int `json:"data_disconnected"`
}

type DashboardStaleSummary struct {
	Total int `json:"total"`
	Stale int `json:"stale"`
}

type DashboardAuthSummary struct {
	Healthy     int `json:"healthy"`
	RefreshSoon int `json:"refresh_soon"`
	Refreshing  int `json:"refreshing"`
	Expiring    int `json:"expiring"`
	Expired     int `json:"expired"`
	Revoked     int `json:"revoked"`
	Conflict    int `json:"conflict"`
	Unavailable int `json:"unavailable"`
	Unknown     int `json:"unknown"`
}

type DashboardQuotaPressureSummary struct {
	LimitedScopes    int                   `json:"limited_scopes"`
	PressuredScopes  int                   `json:"pressured_scopes"`
	MaxRatio         float64               `json:"max_ratio,omitempty"`
	MaxTokensRatio   float64               `json:"max_tokens_ratio,omitempty"`
	MaxRequestsRatio float64               `json:"max_requests_ratio,omitempty"`
	Highest          *DashboardQuotaRecord `json:"highest,omitempty"`
}

type DashboardQuotaRecord struct {
	Scope         quota.Scope `json:"scope"`
	Limit         quota.Limit `json:"limit"`
	Committed     quota.Usage `json:"committed,omitempty"`
	Reserved      quota.Usage `json:"reserved,omitempty"`
	Used          quota.Usage `json:"used,omitempty"`
	Ratio         float64     `json:"ratio,omitempty"`
	TokensRatio   float64     `json:"tokens_ratio,omitempty"`
	RequestsRatio float64     `json:"requests_ratio,omitempty"`
}

type DashboardFreshness struct {
	Source            string    `json:"source,omitempty"`
	LastSeenAt        time.Time `json:"last_seen_at,omitempty"`
	AgeSeconds        int64     `json:"age_seconds,omitempty"`
	StaleAfterSeconds int64     `json:"stale_after_seconds,omitempty"`
	Stale             bool      `json:"stale"`
}

type RouteView struct {
	RouteID              string                `json:"route_id"`
	Match                RouteMatch            `json:"match,omitempty"`
	RequiredCapabilities []provider.Capability `json:"required_capabilities,omitempty"`
	Constraints          Constraints           `json:"constraints,omitempty"`
	CandidateCount       int                   `json:"candidate_count"`
	MatchedProviders     int                   `json:"matched_providers"`
	AvailableProviders   int                   `json:"available_providers"`
	RejectedProviders    int                   `json:"rejected_providers"`
	Candidates           []RouteCandidateView  `json:"candidates"`
}

type RouteCandidateView struct {
	Provider           string              `json:"provider"`
	Account            string              `json:"account,omitempty"`
	HostName           string              `json:"host_name,omitempty"`
	Weight             int                 `json:"weight,omitempty"`
	MatchedProviders   int                 `json:"matched_providers"`
	AvailableProviders int                 `json:"available_providers"`
	RejectedProviders  int                 `json:"rejected_providers"`
	Rejection          string              `json:"rejection,omitempty"`
	Providers          []RouteProviderView `json:"providers,omitempty"`
}

type RouteProviderView struct {
	ProviderInstanceID string                `json:"provider_instance_id"`
	ProviderID         string                `json:"provider_id,omitempty"`
	NodeID             string                `json:"node_id,omitempty"`
	HostName           string                `json:"host_name,omitempty"`
	ContainerID        string                `json:"container_id,omitempty"`
	Service            provider.Service      `json:"service,omitempty"`
	ProviderKind       provider.Kind         `json:"provider_kind,omitempty"`
	Account            provider.Account      `json:"account,omitempty"`
	HealthStatus       provider.HealthStatus `json:"health_status,omitempty"`
	AuthStatus         provider.AuthStatus   `json:"auth_status,omitempty"`
	QueueDepth         int                   `json:"queue_depth,omitempty"`
	DataSessionActive  bool                  `json:"data_session_active"`
	Score              int                   `json:"score,omitempty"`
	Weight             int                   `json:"weight,omitempty"`
	Allowed            bool                  `json:"allowed"`
	Rejection          string                `json:"rejection,omitempty"`
}

type ProviderView struct {
	ProviderInstanceID      string                `json:"provider_instance_id"`
	ProviderID              string                `json:"provider_id,omitempty"`
	NodeID                  string                `json:"node_id,omitempty"`
	HostName                string                `json:"host_name,omitempty"`
	ContainerID             string                `json:"container_id,omitempty"`
	Service                 provider.Service      `json:"service,omitempty"`
	ProviderKind            provider.Kind         `json:"provider_kind,omitempty"`
	Account                 provider.Account      `json:"account,omitempty"`
	Capabilities            []provider.Capability `json:"capabilities,omitempty"`
	Models                  []provider.Model      `json:"models,omitempty"`
	Health                  provider.Health       `json:"health"`
	Auth                    provider.AuthState    `json:"auth,omitempty"`
	Limits                  provider.LimitState   `json:"limits,omitempty"`
	RegisteredAt            time.Time             `json:"registered_at,omitempty"`
	ControlSessionActive    bool                  `json:"control_session_active"`
	DataSessionActive       bool                  `json:"data_session_active"`
	PendingRequests         int                   `json:"pending_requests,omitempty"`
	NodeFreshness           DashboardFreshness    `json:"node_freshness,omitempty"`
	ContainerFreshness      DashboardFreshness    `json:"container_freshness,omitempty"`
	ControlSessionFreshness DashboardFreshness    `json:"control_session_freshness,omitempty"`
	DataSessionFreshness    DashboardFreshness    `json:"data_session_freshness,omitempty"`
	AuthFreshness           DashboardFreshness    `json:"auth_freshness,omitempty"`
	UsageFreshness          DashboardFreshness    `json:"usage_freshness,omitempty"`
	Usage                   *provider.UsageReport `json:"usage,omitempty"`
}

type TraceSummary struct {
	RequestID          string            `json:"request_id"`
	RouteID            string            `json:"route_id,omitempty"`
	TenantID           string            `json:"tenant_id,omitempty"`
	UserID             string            `json:"user_id,omitempty"`
	APIKeyID           string            `json:"api_key_id,omitempty"`
	Model              string            `json:"model,omitempty"`
	CanonicalModel     string            `json:"canonical_model,omitempty"`
	APIDialect         compat.APIDialect `json:"api_dialect,omitempty"`
	Stream             bool              `json:"stream,omitempty"`
	Status             string            `json:"status"`
	Error              string            `json:"error,omitempty"`
	ErrorCode          string            `json:"error_code,omitempty"`
	ErrorStatus        int               `json:"error_status,omitempty"`
	RetryAfter         string            `json:"retry_after,omitempty"`
	ProviderInstanceID string            `json:"provider_instance_id,omitempty"`
	ProviderID         string            `json:"provider_id,omitempty"`
	NodeID             string            `json:"node_id,omitempty"`
	HostName           string            `json:"host_name,omitempty"`
	ContainerID        string            `json:"container_id,omitempty"`
	Service            provider.Service  `json:"service,omitempty"`
	ProviderKind       provider.Kind     `json:"provider_kind,omitempty"`
	Account            provider.Account  `json:"account,omitempty"`
	EstimatedUsage     quota.Usage       `json:"estimated_usage,omitempty"`
	ActualUsage        quota.Usage       `json:"actual_usage,omitempty"`
	StartedAt          time.Time         `json:"started_at"`
	CompletedAt        time.Time         `json:"completed_at"`
	DurationMS         int64             `json:"duration_ms"`
}

func BuildDashboardSummary(engine *Engine, dataBroker *DataBroker) DashboardSummary {
	return buildDashboardSummary(time.Now().UTC(), engine, dataBroker, DefaultDashboardTraceLimit)
}

func BuildDashboardRouteViews(engine *Engine, dataBroker *DataBroker) []RouteView {
	return buildRouteViews(engine, dataBroker)
}

func BuildDashboardProviderViews(engine *Engine, dataBroker *DataBroker) []ProviderView {
	return buildProviderViews(time.Now().UTC(), engine, dataBroker)
}

func BuildDashboardTraceSummaries(engine *Engine, limit int) []TraceSummary {
	return buildTraceSummaries(engine, limit)
}

func BuildDashboardOverview(engine *Engine, dataBroker *DataBroker) DashboardOverviewResponse {
	now := time.Now().UTC()
	return DashboardOverviewResponse{
		Summary: buildDashboardSummary(now, engine, dataBroker, DefaultDashboardTraceLimit),
	}
}

func BuildDashboardRoutes(engine *Engine, dataBroker *DataBroker) DashboardRoutesResponse {
	now := time.Now().UTC()
	return DashboardRoutesResponse{
		GeneratedAt: now,
		Routes:      buildRouteViews(engine, dataBroker),
	}
}

func BuildDashboardProviders(engine *Engine, dataBroker *DataBroker) DashboardProvidersResponse {
	now := time.Now().UTC()
	return DashboardProvidersResponse{
		GeneratedAt: now,
		Summary:     buildDashboardSummary(now, engine, dataBroker, DefaultDashboardTraceLimit),
		Providers:   buildProviderViews(now, engine, dataBroker),
	}
}

func BuildDashboardTraces(engine *Engine, limit int) DashboardTracesResponse {
	now := time.Now().UTC()
	traces := buildTraceSummaries(engine, limit)
	return DashboardTracesResponse{
		GeneratedAt:  now,
		RecentErrors: countTraceSummaryErrors(traces),
		Traces:       traces,
	}
}

func buildDashboardSummary(now time.Time, engine *Engine, dataBroker *DataBroker, traceLimit int) DashboardSummary {
	providers := providerRegistrations(engine)
	nodes := nodeSnapshots(engine)
	containers := containerSnapshots(engine)
	controlSessions := controlSessionSnapshots(engine)
	dataSessions := dataSessionSnapshots(engine, dataBroker)
	traces := requestTraceSnapshots(engine, traceLimit)
	quotas := quotaSnapshots(engine)

	summary := DashboardSummary{
		UpdatedAt:             now,
		GeneratedAt:           now,
		Router:                DashboardRouterSummary{Status: dashboardRouterStatus(engine)},
		RoutesTotal:           len(policyRoutes(engine)),
		ProvidersTotal:        len(providers),
		ProvidersByHealth:     make(map[provider.HealthStatus]int),
		ProvidersByAuth:       make(map[provider.AuthStatus]int),
		NodesTotal:            len(nodes),
		ContainersTotal:       len(containers),
		ActiveControlSessions: len(controlSessions),
		ActiveDataSessions:    len(dataSessions),
		RecentTraces:          len(traces),
		QuotaPressure:         quotaPressureSummary(quotas),
	}
	summary.Providers.Total = len(providers)
	summary.Providers.ByHealth = summary.ProvidersByHealth
	summary.Providers.ByAuth = summary.ProvidersByAuth
	summary.Requests.Active = activeDataRequests(dataSessions)
	summary.Requests.Recent = len(traces)
	summary.Streams.Active = activeStreams(providers)
	summary.Sessions.ControlActive = len(controlSessions)
	summary.Sessions.DataActive = len(dataSessions)
	summary.Sessions.ControlDisconnected = disconnectedCount(len(providers), len(controlSessions), engine != nil)
	summary.Sessions.DataDisconnected = disconnectedCount(len(providers), len(dataSessions), dataBroker != nil)
	summary.Nodes.Total = len(nodes)
	summary.Containers.Total = len(containers)
	for _, registration := range providers {
		health := registration.Health.Status
		if health == "" {
			health = provider.HealthUnknown
		}
		auth := registration.Auth.Status
		if auth == "" {
			auth = provider.AuthUnknown
		}
		summary.ProvidersByHealth[health]++
		summary.ProvidersByAuth[auth]++
		switch health {
		case provider.HealthReady:
			summary.Providers.Ready++
		case provider.HealthDegraded:
			summary.Providers.Degraded++
		case provider.HealthDraining, provider.HealthAuthUpdating:
			summary.Providers.Draining++
		case provider.HealthDown:
			summary.Providers.Down++
		default:
			summary.Providers.Unknown++
		}
		switch auth {
		case provider.AuthHealthy:
			summary.Auth.Healthy++
		case provider.AuthRefreshSoon:
			summary.Auth.RefreshSoon++
			summary.Auth.Expiring++
		case provider.AuthRefreshing:
			summary.Auth.Refreshing++
		case provider.AuthExpired:
			summary.Auth.Expired++
		case provider.AuthRevoked:
			summary.Auth.Revoked++
			summary.Auth.Expired++
		case provider.AuthConflict:
			summary.Auth.Conflict++
		case provider.AuthUnavailable:
			summary.Auth.Unavailable++
		default:
			summary.Auth.Unknown++
		}
	}
	for _, node := range nodes {
		if nodeFreshness(now, node).Stale {
			summary.StaleNodes++
		}
	}
	summary.Nodes.Stale = summary.StaleNodes
	for _, container := range containers {
		if containerFreshness(now, container).Stale {
			summary.StaleContainers++
		}
	}
	summary.Containers.Stale = summary.StaleContainers
	for _, trace := range traces {
		if traceHasError(trace) {
			summary.RecentErrors++
		}
	}
	summary.Requests.RecentFailures = summary.RecentErrors
	return summary
}

func buildRouteViews(engine *Engine, dataBroker *DataBroker) []RouteView {
	if engine == nil {
		return []RouteView{}
	}
	registrations := providerRegistrations(engine)
	dataSessions := dataSessionMap(dataSessionSnapshots(engine, dataBroker))
	dataBrokerPresent := dataBroker != nil
	routes := policyRoutes(engine)
	out := make([]RouteView, 0, len(routes))
	for _, route := range routes {
		required := routeRequiredCapabilities(engine.policy, route)
		view := RouteView{
			RouteID:              route.ID,
			Match:                route.Match,
			RequiredCapabilities: required,
			Constraints:          route.Constraints,
			CandidateCount:       len(route.Candidates),
			Candidates:           make([]RouteCandidateView, 0, len(route.Candidates)),
		}
		for _, candidate := range route.Candidates {
			candidateView := RouteCandidateView{
				Provider: candidate.Provider,
				Account:  candidate.Account,
				HostName: candidate.HostName,
				Weight:   candidate.Weight,
			}
			for _, registration := range registrations {
				if !candidateMatchesRegistration(candidate, registration) {
					continue
				}
				candidateView.MatchedProviders++
				view.MatchedProviders++
				routingRegistration := dashboardRoutingRegistration(registration, dataSessions, dataBrokerPresent)
				score, weight, _, rejection := evaluateRegistration(candidate, route.Constraints, required, routingRegistration)
				providerView := routeProviderView(routingRegistration, dataSessions[routingRegistration.Identity.ProviderInstanceID])
				providerView.Score = score
				providerView.Weight = weight
				if rejection != "" {
					providerView.Rejection = rejection
					candidateView.RejectedProviders++
					view.RejectedProviders++
				} else {
					providerView.Allowed = true
					candidateView.AvailableProviders++
					view.AvailableProviders++
				}
				candidateView.Providers = append(candidateView.Providers, providerView)
			}
			if candidateView.MatchedProviders == 0 {
				candidateView.Rejection = "candidate provider not connected"
			}
			sort.SliceStable(candidateView.Providers, func(i, j int) bool {
				a := candidateView.Providers[i]
				b := candidateView.Providers[j]
				if a.Allowed != b.Allowed {
					return a.Allowed
				}
				if a.Score != b.Score {
					return a.Score > b.Score
				}
				return a.ProviderInstanceID < b.ProviderInstanceID
			})
			view.Candidates = append(view.Candidates, candidateView)
		}
		out = append(out, view)
	}
	return out
}

func buildProviderViews(now time.Time, engine *Engine, dataBroker *DataBroker) []ProviderView {
	views := make(map[string]ProviderView)
	nodes := nodeMap(nodeSnapshots(engine))
	containersByID, containersByProvider := containerMaps(containerSnapshots(engine))
	controlSessions := controlSessionMap(controlSessionSnapshots(engine))
	dataSessions := dataSessionMap(dataSessionSnapshots(engine, dataBroker))
	usages := providerUsageMap(providerUsageSnapshots(engine))

	for _, registration := range providerRegistrations(engine) {
		identity := registration.Identity
		view := ProviderView{
			ProviderInstanceID: identity.ProviderInstanceID,
			ProviderID:         identity.ProviderID,
			NodeID:             identity.NodeID,
			HostName:           identity.HostName,
			ContainerID:        identity.ContainerID,
			Service:            identity.Service,
			ProviderKind:       identity.Kind,
			Account:            accountWithFallback(identity.Account, registration.Auth.Account),
			Capabilities:       append([]provider.Capability(nil), registration.Capabilities...),
			Models:             providerModels(engine, registration),
			Health:             registration.Health,
			Auth:               registration.Auth,
			Limits:             registration.Limits,
			RegisteredAt:       registration.RegisteredAt,
			AuthFreshness:      authFreshness(now, registration.Auth),
		}
		if node, ok := nodes[identity.NodeID]; ok {
			view.NodeFreshness = nodeFreshness(now, node)
		}
		if container, ok := providerContainer(identity, containersByID, containersByProvider); ok {
			if view.ContainerID == "" {
				view.ContainerID = container.ContainerID
			}
			view.ContainerFreshness = containerFreshness(now, container)
		}
		if session, ok := controlSessions[identity.ProviderInstanceID]; ok {
			view.ControlSessionActive = true
			view.ControlSessionFreshness = sessionFreshness(now, "control.connected_at", session.ConnectedAt)
		}
		if session, ok := dataSessions[identity.ProviderInstanceID]; ok {
			view.DataSessionActive = true
			view.PendingRequests = session.PendingRequests
			view.DataSessionFreshness = sessionFreshness(now, "data.connected_at", session.ConnectedAt)
			if view.ProviderID == "" {
				view.ProviderID = session.ProviderID
			}
			if view.NodeID == "" {
				view.NodeID = session.NodeID
			}
			if view.HostName == "" {
				view.HostName = session.HostName
			}
			if view.Service == "" {
				view.Service = session.Service
			}
			view.Account = accountWithFallback(view.Account, session.Account)
		}
		if usage, ok := usages[identity.ProviderInstanceID]; ok {
			report := usage.Usage
			view.Usage = &report
			view.UsageFreshness = usageFreshness(now, usage)
		}
		views[identity.ProviderInstanceID] = view
	}

	for _, session := range controlSessions {
		if _, ok := views[session.ProviderInstanceID]; ok {
			continue
		}
		views[session.ProviderInstanceID] = ProviderView{
			ProviderInstanceID:      session.ProviderInstanceID,
			ProviderID:              session.ProviderID,
			NodeID:                  session.NodeID,
			HostName:                session.HostName,
			Service:                 session.Service,
			Account:                 session.Account,
			ControlSessionActive:    true,
			ControlSessionFreshness: sessionFreshness(now, "control.connected_at", session.ConnectedAt),
		}
	}
	for _, session := range dataSessions {
		view := views[session.ProviderInstanceID]
		view.ProviderInstanceID = session.ProviderInstanceID
		if view.ProviderID == "" {
			view.ProviderID = session.ProviderID
		}
		if view.NodeID == "" {
			view.NodeID = session.NodeID
		}
		if view.HostName == "" {
			view.HostName = session.HostName
		}
		if view.Service == "" {
			view.Service = session.Service
		}
		view.Account = accountWithFallback(view.Account, session.Account)
		view.DataSessionActive = true
		view.PendingRequests = session.PendingRequests
		view.DataSessionFreshness = sessionFreshness(now, "data.connected_at", session.ConnectedAt)
		views[session.ProviderInstanceID] = view
	}

	out := make([]ProviderView, 0, len(views))
	for _, view := range views {
		out = append(out, view)
	}
	sort.SliceStable(out, func(i, j int) bool { return providerViewLess(out[i], out[j]) })
	return out
}

func buildTraceSummaries(engine *Engine, limit int) []TraceSummary {
	traces := requestTraceSnapshots(engine, limit)
	out := make([]TraceSummary, 0, len(traces))
	for _, trace := range traces {
		out = append(out, traceSummary(trace))
	}
	return out
}

func providerRegistrations(engine *Engine) []provider.Registration {
	if engine == nil {
		return nil
	}
	return engine.Providers()
}

func policyRoutes(engine *Engine) []Route {
	if engine == nil {
		return nil
	}
	return append([]Route(nil), engine.policy.Routes...)
}

func nodeSnapshots(engine *Engine) []NodeSnapshot {
	if engine == nil {
		return nil
	}
	return engine.Nodes()
}

func containerSnapshots(engine *Engine) []ContainerSnapshot {
	if engine == nil {
		return nil
	}
	return engine.Containers()
}

func controlSessionSnapshots(engine *Engine) []ControlSessionSnapshot {
	if engine == nil {
		return nil
	}
	return engine.ControlSessions()
}

func dataSessionSnapshots(engine *Engine, dataBroker *DataBroker) []DataSessionSnapshot {
	if dataBroker == nil {
		return nil
	}
	sessions := dataBroker.Sessions()
	if engine != nil {
		sessions = engine.EnrichDataSessions(sessions)
	}
	return sessions
}

func providerUsageSnapshots(engine *Engine) []ProviderUsageSnapshot {
	if engine == nil {
		return nil
	}
	return engine.ProviderUsages()
}

func requestTraceSnapshots(engine *Engine, limit int) []RequestTrace {
	if engine == nil {
		return nil
	}
	if limit <= 0 {
		limit = DefaultDashboardTraceLimit
	}
	return engine.RequestTraces(limit)
}

func quotaSnapshots(engine *Engine) []quota.SnapshotRecord {
	if engine == nil {
		return nil
	}
	return engine.QuotaSnapshots()
}

func dashboardRouterStatus(engine *Engine) string {
	if engine == nil {
		return "not_ready"
	}
	return "ready"
}

func activeDataRequests(sessions []DataSessionSnapshot) int {
	total := 0
	for _, session := range sessions {
		total += session.PendingRequests
	}
	return total
}

func activeStreams(registrations []provider.Registration) int {
	total := 0
	for _, registration := range registrations {
		total += registration.Limits.ActiveStreams
	}
	return total
}

func disconnectedCount(total int, active int, known bool) int {
	if !known {
		return 0
	}
	if active >= total {
		return 0
	}
	return total - active
}

func routeRequiredCapabilities(policy RoutingPolicy, route Route) []provider.Capability {
	required := append([]provider.Capability(nil), route.Constraints.RequiredCapabilities...)
	for _, model := range route.Match.Models {
		alias := policy.ModelAliases[model]
		required = append(required, alias.RequiredCapabilities...)
	}
	return uniqueCapabilities(required)
}

func dashboardRoutingRegistration(registration provider.Registration, dataSessions map[string]DataSessionSnapshot, dataBrokerPresent bool) provider.Registration {
	providerInstanceID := registration.Identity.ProviderInstanceID
	if session, ok := dataSessions[providerInstanceID]; ok {
		if session.PendingRequests > registration.Limits.QueueDepth {
			registration.Limits.QueueDepth = session.PendingRequests
		}
		if session.PendingRequests > registration.Limits.ActiveStreams {
			registration.Limits.ActiveStreams = session.PendingRequests
		}
		return registration
	}
	if dataBrokerPresent {
		registration.Health.Status = provider.HealthDown
		registration.Health.Reason = "data session disconnected"
	}
	return registration
}

func routeProviderView(registration provider.Registration, dataSession DataSessionSnapshot) RouteProviderView {
	identity := registration.Identity
	view := RouteProviderView{
		ProviderInstanceID: identity.ProviderInstanceID,
		ProviderID:         identity.ProviderID,
		NodeID:             identity.NodeID,
		HostName:           identity.HostName,
		ContainerID:        identity.ContainerID,
		Service:            identity.Service,
		ProviderKind:       identity.Kind,
		Account:            accountWithFallback(identity.Account, registration.Auth.Account),
		HealthStatus:       registration.Health.Status,
		AuthStatus:         registration.Auth.Status,
		QueueDepth:         registration.Limits.QueueDepth,
	}
	if dataSession.ProviderInstanceID != "" {
		view.DataSessionActive = true
		view.QueueDepth = max(view.QueueDepth, dataSession.PendingRequests)
	}
	return view
}

func traceSummary(trace RequestTrace) TraceSummary {
	summary := TraceSummary{
		RequestID:      trace.RequestID,
		RouteID:        trace.Decision.RouteID,
		TenantID:       trace.RouteRequest.TenantID,
		UserID:         trace.RouteRequest.UserID,
		APIKeyID:       trace.RouteRequest.APIKeyID,
		Model:          trace.RouteRequest.Model,
		CanonicalModel: trace.Decision.CanonicalModel,
		APIDialect:     trace.RouteRequest.APIDialect,
		Stream:         trace.RouteRequest.Stream,
		Status:         trace.Status,
		Error:          trace.Error,
		ErrorCode:      trace.ErrorCode,
		ErrorStatus:    trace.ErrorStatus,
		RetryAfter:     trace.RetryAfter,
		EstimatedUsage: trace.EstimatedUsage,
		ActualUsage:    trace.ActualUsage,
		StartedAt:      trace.StartedAt,
		CompletedAt:    trace.CompletedAt,
		DurationMS:     trace.DurationMS,
	}
	if trace.Provider != nil {
		summary.ProviderInstanceID = trace.Provider.ProviderInstanceID
		summary.ProviderID = trace.Provider.ProviderID
		summary.NodeID = trace.Provider.NodeID
		summary.HostName = trace.Provider.HostName
		summary.ContainerID = trace.Provider.ContainerID
		summary.Service = trace.Provider.Service
		summary.ProviderKind = trace.Provider.Kind
		summary.Account = trace.Provider.Account
	}
	return summary
}

func quotaPressureSummary(records []quota.SnapshotRecord) DashboardQuotaPressureSummary {
	var summary DashboardQuotaPressureSummary
	for _, record := range records {
		pressure, ok := quotaPressureRecord(record)
		if !ok {
			continue
		}
		summary.LimitedScopes++
		if pressure.Ratio >= dashboardQuotaPressureThreshold {
			summary.PressuredScopes++
		}
		if summary.Highest == nil || pressure.Ratio > summary.MaxRatio {
			copy := pressure
			summary.Highest = &copy
			summary.MaxRatio = pressure.Ratio
		}
		if pressure.TokensRatio > summary.MaxTokensRatio {
			summary.MaxTokensRatio = pressure.TokensRatio
		}
		if pressure.RequestsRatio > summary.MaxRequestsRatio {
			summary.MaxRequestsRatio = pressure.RequestsRatio
		}
	}
	return summary
}

func quotaPressureRecord(record quota.SnapshotRecord) (DashboardQuotaRecord, bool) {
	used := quota.Usage{
		Tokens:   record.Committed.Tokens + record.Reserved.Tokens,
		Requests: record.Committed.Requests + record.Reserved.Requests,
	}
	limited := record.Limit.MaxTokens > 0 || record.Limit.MaxRequests > 0
	if !limited {
		return DashboardQuotaRecord{}, false
	}
	var tokensRatio float64
	var requestsRatio float64
	if record.Limit.MaxTokens > 0 {
		tokensRatio = float64(used.Tokens) / float64(record.Limit.MaxTokens)
	}
	if record.Limit.MaxRequests > 0 {
		requestsRatio = float64(used.Requests) / float64(record.Limit.MaxRequests)
	}
	ratio := max(tokensRatio, requestsRatio)
	return DashboardQuotaRecord{
		Scope:         record.Scope,
		Limit:         record.Limit,
		Committed:     record.Committed,
		Reserved:      record.Reserved,
		Used:          used,
		Ratio:         ratio,
		TokensRatio:   tokensRatio,
		RequestsRatio: requestsRatio,
	}, true
}

func nodeFreshness(now time.Time, node NodeSnapshot) DashboardFreshness {
	return newestFreshness(now, DashboardFreshnessStaleAfter,
		freshnessCandidate{source: "node.heartbeat", at: node.LastHeartbeatAt},
		freshnessCandidate{source: "node.inventory", at: node.LastInventoryAt},
		freshnessCandidate{source: "node.hello", at: node.LastHelloAt},
		freshnessCandidate{source: "node.updated_at", at: node.UpdatedAt},
	)
}

func containerFreshness(now time.Time, container ContainerSnapshot) DashboardFreshness {
	reported := freshnessAt(now, "container.reported_at", container.ReportedAt, DashboardFreshnessStaleAfter)
	if !reported.LastSeenAt.IsZero() {
		return reported
	}
	return freshnessAt(now, "container.updated_at", container.UpdatedAt, DashboardFreshnessStaleAfter)
}

func authFreshness(now time.Time, auth provider.AuthState) DashboardFreshness {
	return freshnessAt(now, "auth.last_refresh_at", auth.LastRefreshAt, 0)
}

func usageFreshness(now time.Time, usage ProviderUsageSnapshot) DashboardFreshness {
	reported := freshnessAt(now, "usage.reported_at", usage.ReportedAt, 0)
	if !reported.LastSeenAt.IsZero() {
		return reported
	}
	return freshnessAt(now, "usage.updated_at", usage.UpdatedAt, 0)
}

func sessionFreshness(now time.Time, source string, at time.Time) DashboardFreshness {
	return freshnessAt(now, source, at, 0)
}

type freshnessCandidate struct {
	source string
	at     time.Time
}

func newestFreshness(now time.Time, staleAfter time.Duration, candidates ...freshnessCandidate) DashboardFreshness {
	var selected freshnessCandidate
	for _, candidate := range candidates {
		if candidate.at.IsZero() {
			continue
		}
		if selected.at.IsZero() || candidate.at.After(selected.at) {
			selected = candidate
		}
	}
	return freshnessAt(now, selected.source, selected.at, staleAfter)
}

func freshnessAt(now time.Time, source string, at time.Time, staleAfter time.Duration) DashboardFreshness {
	if at.IsZero() {
		return DashboardFreshness{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	at = at.UTC()
	age := now.Sub(at)
	if age < 0 {
		age = 0
	}
	freshness := DashboardFreshness{
		Source:     source,
		LastSeenAt: at,
		AgeSeconds: int64(age / time.Second),
	}
	if staleAfter > 0 {
		freshness.StaleAfterSeconds = int64(staleAfter / time.Second)
		freshness.Stale = age > staleAfter
	}
	return freshness
}

func accountWithFallback(primary provider.Account, fallback provider.Account) provider.Account {
	if primary.ID != "" || primary.Display != "" {
		return primary
	}
	return fallback
}

func providerModels(engine *Engine, registration provider.Registration) []provider.Model {
	if len(registration.Models) > 0 {
		return cloneProviderModels(registration.Models)
	}
	if engine == nil {
		return nil
	}
	out := make([]provider.Model, 0)
	indexByID := make(map[string]int)
	for _, route := range engine.policy.Routes {
		if !routeHasCandidateRegistration(route, registration) {
			continue
		}
		for _, modelName := range route.Match.Models {
			if modelName == "" {
				continue
			}
			alias := engine.policy.ModelAliases[modelName]
			modelID := alias.CanonicalModel
			if modelID == "" {
				modelID = modelName
			}
			capabilities := uniqueCapabilities(alias.RequiredCapabilities)
			if !providerHasCapabilities(registration, capabilities) {
				continue
			}
			if idx, ok := indexByID[modelID]; ok {
				out[idx].Aliases = appendStringUnique(out[idx].Aliases, modelName)
				out[idx].Capabilities = mergeCapabilities(out[idx].Capabilities, capabilities)
				continue
			}
			indexByID[modelID] = len(out)
			out = append(out, provider.Model{
				ID:           modelID,
				Aliases:      []string{modelName},
				Capabilities: capabilities,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cloneProviderModels(models []provider.Model) []provider.Model {
	out := make([]provider.Model, 0, len(models))
	for _, model := range models {
		model.Aliases = append([]string(nil), model.Aliases...)
		model.Capabilities = append([]provider.Capability(nil), model.Capabilities...)
		out = append(out, model)
	}
	return out
}

func routeHasCandidateRegistration(route Route, registration provider.Registration) bool {
	for _, candidate := range route.Candidates {
		if candidateMatchesRegistration(candidate, registration) {
			return true
		}
	}
	return false
}

func providerHasCapabilities(registration provider.Registration, capabilities []provider.Capability) bool {
	for _, capability := range capabilities {
		if !hasCapability(registration.Capabilities, capability) {
			return false
		}
	}
	return true
}

func appendStringUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeCapabilities(existing []provider.Capability, incoming []provider.Capability) []provider.Capability {
	out := append([]provider.Capability(nil), existing...)
	for _, capability := range incoming {
		out = appendCapability(out, capability)
	}
	return out
}

func traceHasError(trace RequestTrace) bool {
	return trace.Error != "" || trace.ErrorCode != "" || trace.ErrorStatus >= 400 || trace.Status == "failed" || trace.Status == "rejected" || trace.Status == "provider_error"
}

func countTraceSummaryErrors(traces []TraceSummary) int {
	count := 0
	for _, trace := range traces {
		if trace.Error != "" || trace.ErrorCode != "" || trace.ErrorStatus >= 400 || trace.Status == "failed" || trace.Status == "rejected" || trace.Status == "provider_error" {
			count++
		}
	}
	return count
}

func nodeMap(nodes []NodeSnapshot) map[string]NodeSnapshot {
	out := make(map[string]NodeSnapshot, len(nodes))
	for _, node := range nodes {
		if node.NodeID != "" {
			out[node.NodeID] = node
		}
	}
	return out
}

func containerMaps(containers []ContainerSnapshot) (map[string]ContainerSnapshot, map[string]ContainerSnapshot) {
	byID := make(map[string]ContainerSnapshot, len(containers))
	byProvider := make(map[string]ContainerSnapshot, len(containers))
	for _, container := range containers {
		if container.NodeID != "" && container.ContainerID != "" {
			byID[containerKey(container.NodeID, container.ContainerID)] = container
		}
		if container.ContainerID != "" {
			byID[container.ContainerID] = container
		}
		if container.ProviderInstanceID != "" {
			byProvider[container.ProviderInstanceID] = container
		}
	}
	return byID, byProvider
}

func providerContainer(identity provider.ProviderIdentity, byID map[string]ContainerSnapshot, byProvider map[string]ContainerSnapshot) (ContainerSnapshot, bool) {
	if identity.ContainerID != "" {
		if container, ok := byID[containerKey(identity.NodeID, identity.ContainerID)]; ok {
			return container, true
		}
		if container, ok := byID[identity.ContainerID]; ok {
			return container, true
		}
	}
	if container, ok := byProvider[identity.ProviderInstanceID]; ok {
		return container, true
	}
	return ContainerSnapshot{}, false
}

func controlSessionMap(sessions []ControlSessionSnapshot) map[string]ControlSessionSnapshot {
	out := make(map[string]ControlSessionSnapshot, len(sessions))
	for _, session := range sessions {
		if session.ProviderInstanceID != "" {
			out[session.ProviderInstanceID] = session
		}
	}
	return out
}

func dataSessionMap(sessions []DataSessionSnapshot) map[string]DataSessionSnapshot {
	out := make(map[string]DataSessionSnapshot, len(sessions))
	for _, session := range sessions {
		if session.ProviderInstanceID != "" {
			out[session.ProviderInstanceID] = session
		}
	}
	return out
}

func providerUsageMap(usages []ProviderUsageSnapshot) map[string]ProviderUsageSnapshot {
	out := make(map[string]ProviderUsageSnapshot, len(usages))
	for _, usage := range usages {
		if usage.ProviderInstanceID != "" {
			out[usage.ProviderInstanceID] = usage
		}
	}
	return out
}

func providerViewLess(a ProviderView, b ProviderView) bool {
	switch {
	case a.HostName != b.HostName:
		return a.HostName < b.HostName
	case a.Service != b.Service:
		return a.Service < b.Service
	case a.ProviderID != b.ProviderID:
		return a.ProviderID < b.ProviderID
	case a.Account.Display != b.Account.Display:
		return a.Account.Display < b.Account.Display
	default:
		return a.ProviderInstanceID < b.ProviderInstanceID
	}
}
