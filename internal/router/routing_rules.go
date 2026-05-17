package router

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

type RoutingRuleScope string

const (
	RoutingRuleScopePublic RoutingRuleScope = "public"
	RoutingRuleScopeUser   RoutingRuleScope = "user"
)

type RoutingRule struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Scope       RoutingRuleScope  `json:"scope"`
	OwnerEmail  string            `json:"owner_email,omitempty"`
	Description string            `json:"description,omitempty"`
	Filters     []RoutingFilter   `json:"filters"`
	Stats       *RoutingRuleStats `json:"stats,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type RoutingRuleStats struct {
	Requests        int64      `json:"requests,omitempty"`
	Tokens          int64      `json:"tokens,omitempty"`
	ActualTokens    int64      `json:"actual_tokens,omitempty"`
	EstimatedTokens int64      `json:"estimated_tokens,omitempty"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

type RoutingFilter struct {
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type"`
	Label    string                `json:"label,omitempty"`
	Criteria RoutingFilterCriteria `json:"criteria,omitempty"`
}

type RoutingFilterCriteria struct {
	ProviderTypes       []string                `json:"provider_types,omitempty"`
	ProviderInstanceIDs []string                `json:"provider_instance_ids,omitempty"`
	Services            []provider.Service      `json:"services,omitempty"`
	Models              []string                `json:"models,omitempty"`
	Accounts            []string                `json:"accounts,omitempty"`
	HostNames           []string                `json:"host_names,omitempty"`
	NodeIDs             []string                `json:"node_ids,omitempty"`
	APIDialects         []compat.APIDialect     `json:"api_dialects,omitempty"`
	Capabilities        []provider.Capability   `json:"capabilities,omitempty"`
	HealthStatus        []provider.HealthStatus `json:"health_status,omitempty"`
	AuthStatus          []provider.AuthStatus   `json:"auth_status,omitempty"`
}

type RoutingRuleStep struct {
	FilterID   string           `json:"filter_id,omitempty"`
	FilterType string           `json:"filter_type"`
	Label      string           `json:"label,omitempty"`
	Matched    []string         `json:"matched,omitempty"`
	Rejected   []RouteRejection `json:"rejected,omitempty"`
	Selected   string           `json:"selected,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}

type RoutingRuleDryRunRequest struct {
	RuleID     string           `json:"rule_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	Scope      RoutingRuleScope `json:"scope,omitempty"`
	OwnerEmail string           `json:"owner_email,omitempty"`
	Request    RouteRequest     `json:"request"`
	Rule       *RoutingRule     `json:"rule,omitempty"`
}

type RoutingRuleDryRunResponse struct {
	Decision RouteDecision     `json:"decision"`
	Steps    []RoutingRuleStep `json:"steps"`
}

func (e *Engine) ListRoutingRules() []RoutingRule {
	if e == nil {
		return nil
	}
	e.rulesMu.RLock()
	defer e.rulesMu.RUnlock()
	out := make([]RoutingRule, 0, len(e.routingRules))
	for _, rule := range e.routingRules {
		out = append(out, rule)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].OwnerEmail != out[j].OwnerEmail {
			return out[i].OwnerEmail < out[j].OwnerEmail
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (e *Engine) RoutingRuleStats() map[string]RoutingRuleStats {
	out := map[string]RoutingRuleStats{}
	if e == nil {
		return out
	}
	e.routeStatsMu.RLock()
	defer e.routeStatsMu.RUnlock()
	for ruleID, stat := range e.routeStats {
		out[ruleID] = cloneRoutingRuleStats(stat)
	}
	return out
}

func (e *Engine) recordRoutingRuleTraceStats(trace RequestTrace) {
	if e == nil {
		return
	}
	ruleID := routingRuleIDForTrace(trace)
	if ruleID == "" {
		return
	}
	stat := RoutingRuleStats{Requests: 1}
	if trace.ActualUsage.Tokens > 0 {
		stat.ActualTokens = trace.ActualUsage.Tokens
		stat.Tokens = trace.ActualUsage.Tokens
	} else if trace.EstimatedUsage.Tokens > 0 {
		stat.EstimatedTokens = trace.EstimatedUsage.Tokens
		stat.Tokens = trace.EstimatedUsage.Tokens
	}
	usedAt := trace.CompletedAt
	if usedAt.IsZero() {
		usedAt = trace.StartedAt
	}
	if !usedAt.IsZero() {
		next := usedAt
		stat.LastUsedAt = &next
	}
	e.addRoutingRuleStats(ruleID, stat)
}

func (e *Engine) addRoutingRuleStats(ruleID string, delta RoutingRuleStats) {
	if e == nil || strings.TrimSpace(ruleID) == "" {
		return
	}
	e.routeStatsMu.Lock()
	defer e.routeStatsMu.Unlock()
	if e.routeStats == nil {
		e.routeStats = make(map[string]RoutingRuleStats)
	}
	stat := e.routeStats[ruleID]
	stat.Requests += delta.Requests
	stat.Tokens += delta.Tokens
	stat.ActualTokens += delta.ActualTokens
	stat.EstimatedTokens += delta.EstimatedTokens
	if delta.LastUsedAt != nil && (stat.LastUsedAt == nil || delta.LastUsedAt.After(*stat.LastUsedAt)) {
		next := *delta.LastUsedAt
		stat.LastUsedAt = &next
	}
	e.routeStats[ruleID] = stat
}

func cloneRoutingRuleStats(stat RoutingRuleStats) RoutingRuleStats {
	if stat.LastUsedAt != nil {
		next := *stat.LastUsedAt
		stat.LastUsedAt = &next
	}
	return stat
}

func routingRuleIDForTrace(trace RequestTrace) string {
	if id := strings.TrimSpace(trace.Decision.RoutingRuleID); id != "" {
		return id
	}
	if id := strings.TrimSpace(trace.Decision.RouteID); strings.HasPrefix(id, "public:") || strings.HasPrefix(id, "user:") {
		return id
	}
	if id := strings.TrimSpace(trace.RouteRequest.RoutingRuleID); id != "" {
		return id
	}
	name := strings.TrimSpace(trace.RouteRequest.RoutingRuleName)
	if name == "" || !validRoutingRuleName(name) {
		return ""
	}
	owner := firstNonEmpty(trace.RouteRequest.RoutingRuleOwner, trace.RouteRequest.UserID)
	if owner != "" && !strings.EqualFold(owner, string(RoutingRuleScopePublic)) {
		return routingRuleID(RoutingRuleScopeUser, owner, name)
	}
	return routingRuleID(RoutingRuleScopePublic, "", name)
}

func (e *Engine) GetRoutingRule(id string) (RoutingRule, bool) {
	if e == nil {
		return RoutingRule{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return RoutingRule{}, false
	}
	e.rulesMu.RLock()
	defer e.rulesMu.RUnlock()
	rule, ok := e.routingRules[id]
	return rule, ok
}

func (e *Engine) FindRoutingRule(scope RoutingRuleScope, ownerEmail string, name string) (RoutingRule, bool) {
	if e == nil {
		return RoutingRule{}, false
	}
	name = strings.TrimSpace(name)
	if !validRoutingRuleName(name) {
		return RoutingRule{}, false
	}
	id := routingRuleID(scope, ownerEmail, name)
	return e.GetRoutingRule(id)
}

func (e *Engine) UpsertRoutingRule(rule RoutingRule) (RoutingRule, error) {
	if e == nil {
		return RoutingRule{}, ErrRouterNotReady
	}
	rule, err := normalizeRoutingRule(rule)
	if err != nil {
		return RoutingRule{}, err
	}
	now := time.Now().UTC()
	e.rulesMu.Lock()
	defer e.rulesMu.Unlock()
	if e.routingRules == nil {
		e.routingRules = make(map[string]RoutingRule)
	}
	if conflict := routingRuleNameConflictLocked(e.routingRules, rule); conflict != "" {
		return RoutingRule{}, fmt.Errorf("routing rule name conflicts with %s", conflict)
	}
	if existing, ok := e.routingRules[rule.ID]; ok && !existing.CreatedAt.IsZero() {
		rule.CreatedAt = existing.CreatedAt
	} else {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	e.routingRules[rule.ID] = rule
	return rule, nil
}

func routingRuleNameConflictLocked(rules map[string]RoutingRule, rule RoutingRule) string {
	for id, existing := range rules {
		if id == rule.ID || !strings.EqualFold(existing.Name, rule.Name) {
			continue
		}
		if existing.Scope == RoutingRuleScopePublic || rule.Scope == RoutingRuleScopePublic {
			return existing.ID
		}
	}
	return ""
}

func (e *Engine) DeleteRoutingRule(id string) bool {
	if e == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	e.rulesMu.Lock()
	defer e.rulesMu.Unlock()
	if _, ok := e.routingRules[id]; !ok {
		return false
	}
	delete(e.routingRules, id)
	e.routeStatsMu.Lock()
	delete(e.routeStats, id)
	e.routeStatsMu.Unlock()
	return true
}

func (e *Engine) DryRunRoutingRule(request RoutingRuleDryRunRequest) RoutingRuleDryRunResponse {
	if e == nil || e.registry == nil {
		return RoutingRuleDryRunResponse{Decision: RouteDecision{Reason: ErrRouterNotReady.Error()}}
	}
	rule, ok := RoutingRule{}, false
	if request.Rule != nil {
		rule = *request.Rule
		normalized, err := normalizeRoutingRule(rule)
		if err != nil {
			return RoutingRuleDryRunResponse{Decision: RouteDecision{Allowed: false, Reason: err.Error(), Rejections: []RouteRejection{{Reason: err.Error()}}}}
		}
		rule = normalized
		ok = true
	}
	if !ok && strings.TrimSpace(request.RuleID) != "" {
		rule, ok = e.GetRoutingRule(request.RuleID)
	}
	if !ok && strings.TrimSpace(request.Name) != "" {
		scope := request.Scope
		if scope == "" {
			scope = RoutingRuleScopePublic
		}
		rule, ok = e.FindRoutingRule(scope, request.OwnerEmail, request.Name)
	}
	if !ok {
		return RoutingRuleDryRunResponse{Decision: RouteDecision{Allowed: false, Reason: ErrNoRoute.Error(), Rejections: []RouteRejection{{Reason: "routing rule not found"}}}}
	}
	decision, steps := e.evaluateRoutingRule(rule, request.Request)
	return RoutingRuleDryRunResponse{Decision: decision, Steps: steps}
}

func (e *Engine) evaluateRoutingRule(rule RoutingRule, request RouteRequest) (RouteDecision, []RoutingRuleStep) {
	required := append([]provider.Capability(nil), request.RequiredCapabilities()...)
	if request.Stream {
		required = appendCapability(required, provider.CapabilityStreamSSE)
	}
	required = uniqueCapabilities(required)
	registrations := e.routingRegistrations()
	providerHistory := e.routingRuleProviderTraceStats(rule.ID)
	now := time.Now().UTC()
	decision := RouteDecision{
		Allowed:              false,
		RouteID:              rule.ID,
		RoutingRuleID:        rule.ID,
		ModelAlias:           request.Model,
		CanonicalModel:       request.Model,
		RequiredCapabilities: required,
		Reason:               ErrNoProvider.Error(),
	}
	steps := make([]RoutingRuleStep, 0, len(rule.Filters))
	for _, filter := range normalizedRuleFilters(rule.Filters) {
		step := RoutingRuleStep{FilterID: filter.ID, FilterType: filter.Type, Label: filter.Label}
		scored := make([]scoredCandidate, 0)
		for _, registration := range registrations {
			reason := routingRuleRegistrationRejection(filter, request, required, registration)
			if reason != "" {
				step.Rejected = append(step.Rejected, RouteRejection{
					ProviderInstanceID: registration.Identity.ProviderInstanceID,
					ProviderType:       registration.Identity.ProviderType,
					Reason:             reason,
				})
				continue
			}
			step.Matched = append(step.Matched, registration.Identity.ProviderInstanceID)
			modelAlias, canonicalModel := routingRuleEffectiveModel(filter, request, required, registration.Models)
			scoreModel := firstNonEmpty(canonicalModel, modelAlias, request.Model)
			score := 100 + routingRuleModelScore(registration.Models, scoreModel)
			scored = append(scored, scoredCandidate{
				registration: registration,
				modelAlias:   modelAlias,
				canonical:    canonicalModel,
				score:        score,
				weight:       1,
				reason:       "matched filter " + firstNonEmpty(filter.Label, filter.Type),
			})
		}
		sort.SliceStable(scored, func(i, j int) bool {
			if scored[i].score == scored[j].score {
				if less, ok := compareCandidateQuotaReset(scored[i], scored[j], request.Model, now); ok {
					return less
				}
				leftID := scored[i].registration.Identity.ProviderInstanceID
				rightID := scored[j].registration.Identity.ProviderInstanceID
				leftHistory := providerHistory[leftID]
				rightHistory := providerHistory[rightID]
				if leftHistory.Requests != rightHistory.Requests {
					return leftHistory.Requests < rightHistory.Requests
				}
				if !leftHistory.LastUsedAt.Equal(rightHistory.LastUsedAt) {
					if leftHistory.LastUsedAt.IsZero() {
						return true
					}
					if rightHistory.LastUsedAt.IsZero() {
						return false
					}
					return leftHistory.LastUsedAt.Before(rightHistory.LastUsedAt)
				}
				return leftID < rightID
			}
			return scored[i].score > scored[j].score
		})
		for _, candidate := range scored {
			decision.Scores = append(decision.Scores, RouteCandidateScore{
				ProviderInstanceID: candidate.registration.Identity.ProviderInstanceID,
				ProviderType:       candidate.registration.Identity.ProviderType,
				Score:              candidate.score,
				Weight:             candidate.weight,
				Reason:             candidate.reason,
			})
			decision.FallbackChain = append(decision.FallbackChain, candidate.registration.Identity.ProviderInstanceID)
		}
		if len(scored) > 0 {
			selected := scored[0].registration
			step.Selected = selected.Identity.ProviderInstanceID
			step.Reason = "selected"
			decision.Allowed = true
			decision.Selected = selected.Identity.ProviderInstanceID
			decision.SelectedProvider = &selected
			decision.ModelAlias = firstNonEmpty(scored[0].modelAlias, request.Model)
			decision.CanonicalModel = firstNonEmpty(scored[0].canonical, request.Model)
			decision.Reason = "selected by routing rule filter"
			decision.Rejections = step.Rejected
			steps = append(steps, step)
			return decision, steps
		}
		step.Reason = "no provider matched this filter"
		decision.Rejections = append(decision.Rejections, step.Rejected...)
		steps = append(steps, step)
	}
	if len(decision.Rejections) == 0 {
		decision.Rejections = []RouteRejection{{Reason: ErrNoProvider.Error()}}
	}
	return decision, steps
}

type routingRuleProviderTraceStat struct {
	Requests   int
	LastUsedAt time.Time
}

func (e *Engine) routingRuleProviderTraceStats(ruleID string) map[string]routingRuleProviderTraceStat {
	out := map[string]routingRuleProviderTraceStat{}
	if e == nil || strings.TrimSpace(ruleID) == "" {
		return out
	}
	e.traceMu.RLock()
	defer e.traceMu.RUnlock()
	for _, trace := range e.traces {
		if routingRuleIDForTrace(trace) != ruleID {
			continue
		}
		providerID := routingRuleTraceProviderID(trace)
		if providerID == "" {
			continue
		}
		stat := out[providerID]
		stat.Requests++
		usedAt := trace.CompletedAt
		if usedAt.IsZero() {
			usedAt = trace.StartedAt
		}
		if !usedAt.IsZero() && (stat.LastUsedAt.IsZero() || usedAt.After(stat.LastUsedAt)) {
			stat.LastUsedAt = usedAt
		}
		out[providerID] = stat
	}
	return out
}

func routingRuleTraceProviderID(trace RequestTrace) string {
	if trace.Provider != nil {
		if id := strings.TrimSpace(trace.Provider.ProviderInstanceID); id != "" {
			return id
		}
	}
	if id := strings.TrimSpace(trace.Decision.Selected); id != "" {
		return id
	}
	if trace.Decision.SelectedProvider != nil {
		return strings.TrimSpace(trace.Decision.SelectedProvider.Identity.ProviderInstanceID)
	}
	return ""
}

func routingRuleRegistrationRejection(filter RoutingFilter, request RouteRequest, required []provider.Capability, registration provider.Registration) string {
	identity := registration.Identity
	criteria := filter.Criteria
	if !stringInSet(identity.ProviderType, criteria.ProviderTypes) && len(criteria.ProviderTypes) > 0 {
		return "provider_type mismatch"
	}
	if !stringInSet(identity.ProviderInstanceID, criteria.ProviderInstanceIDs) && len(criteria.ProviderInstanceIDs) > 0 {
		return "provider_instance_id mismatch"
	}
	if !serviceInSet(identity.Service, criteria.Services) && len(criteria.Services) > 0 {
		return "service mismatch"
	}
	if !stringInSet(identity.HostName, criteria.HostNames) && len(criteria.HostNames) > 0 {
		return "host_name mismatch"
	}
	if !stringInSet(identity.NodeID, criteria.NodeIDs) && len(criteria.NodeIDs) > 0 {
		return "node_id mismatch"
	}
	account := firstNonEmpty(identity.Account.Display, identity.Account.ID, registration.Auth.Account.Display, registration.Auth.Account.ID)
	if !stringInSet(account, criteria.Accounts) && len(criteria.Accounts) > 0 {
		return "account mismatch"
	}
	if !dialectInSet(request.APIDialect, criteria.APIDialects) && len(criteria.APIDialects) > 0 {
		return "api_dialect mismatch"
	}
	if !healthInSet(registration.Health.Status, criteria.HealthStatus) && len(criteria.HealthStatus) > 0 {
		return "health mismatch"
	}
	if !authInSet(registration.Auth.Status, criteria.AuthStatus) && len(criteria.AuthStatus) > 0 {
		return "auth mismatch"
	}
	criteriaRequired := uniqueCapabilities(append(append([]provider.Capability(nil), required...), criteria.Capabilities...))
	modelAlias, canonicalModel := routingRuleEffectiveModel(filter, request, required, registration.Models)
	if len(criteria.Models) > 0 {
		if requested := strings.TrimSpace(request.Model); requested != "" {
			if !requestedModelAllowedByCriteria(registration.Models, requested, criteria.Models) {
				return fmt.Sprintf("requested model is not allowed by route filter: requested=%q allowed_models=%s provider_models=%s", requested, formatQuotedStrings(criteria.Models, 8), formatReportedModelNames(registration.Models, 12))
			}
		} else if canonicalModel == "" {
			return fmt.Sprintf("no route filter model available on provider: filter_models=%s provider_models=%s", formatQuotedStrings(criteria.Models, 8), formatReportedModelNames(registration.Models, 12))
		}
	}
	for _, capability := range criteriaRequired {
		if capability != "" && !hasCapability(registration.Capabilities, capability) {
			return "missing capability " + string(capability)
		}
	}
	if rejection := evaluateModelSupport(modelAlias, canonicalModel, criteriaRequired, registration.Models); rejection != "" {
		return rejection
	}
	if providerModelQuotaExhausted(registration.Models, firstNonEmpty(canonicalModel, modelAlias, request.Model)) {
		return "provider model quota exhausted"
	}
	if registration.Health.Status != "" && registration.Health.Status != provider.HealthReady {
		reason := "provider health is " + string(registration.Health.Status)
		if strings.TrimSpace(registration.Health.Reason) != "" {
			reason += " (" + strings.TrimSpace(registration.Health.Reason) + ")"
		}
		return reason
	}
	if registration.Auth.Status != "" && registration.Auth.Status != provider.AuthHealthy && registration.Auth.Status != provider.AuthRefreshSoon {
		return "provider auth is " + string(registration.Auth.Status)
	}
	return ""
}

func routingRuleEffectiveModel(filter RoutingFilter, request RouteRequest, required []provider.Capability, models []provider.Model) (string, string) {
	requested := strings.TrimSpace(request.Model)
	if requested != "" {
		return requested, requested
	}
	criteria := filter.Criteria
	if len(criteria.Models) == 0 {
		return "", ""
	}
	criteriaRequired := uniqueCapabilities(append(append([]provider.Capability(nil), required...), criteria.Capabilities...))
	for _, name := range criteria.Models {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, model := range models {
			if !modelMatchesAny(model, []string{name}) {
				continue
			}
			if modelQuotaExhausted(model) || !routingRuleModelSupports(model, criteriaRequired) {
				continue
			}
			return name, firstNonEmpty(model.ID, name)
		}
	}
	return "", ""
}

func routingRuleModelSupports(model provider.Model, required []provider.Capability) bool {
	if len(model.Capabilities) == 0 {
		return true
	}
	for _, capability := range required {
		if capability == "" || capability == provider.CapabilityStreamSSE {
			continue
		}
		if !hasCapability(model.Capabilities, capability) {
			return false
		}
	}
	return true
}

func modelQuotaExhausted(model provider.Model) bool {
	return model.Quota != nil && model.Quota.RemainingPct <= 0
}

func normalizeRoutingRule(rule RoutingRule) (RoutingRule, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return RoutingRule{}, fmt.Errorf("rule name is required")
	}
	if !validRoutingRuleName(rule.Name) {
		return RoutingRule{}, fmt.Errorf("rule name must be URL-safe and use only A-Z, a-z, 0-9, '.', '_', '~', or '-'")
	}
	rule.Scope = normalizeRoutingRuleScope(rule.Scope)
	if rule.Scope == "" {
		return RoutingRule{}, fmt.Errorf("invalid rule scope")
	}
	rule.OwnerEmail = normalizeUserEmail(rule.OwnerEmail)
	if rule.Scope == RoutingRuleScopeUser && rule.OwnerEmail == "" {
		return RoutingRule{}, fmt.Errorf("owner_email is required for user routing rules")
	}
	if rule.Scope == RoutingRuleScopePublic {
		rule.OwnerEmail = ""
	}
	rule.Stats = nil
	rule.ID = routingRuleID(rule.Scope, rule.OwnerEmail, rule.Name)
	rule.Filters = normalizedRuleFilters(rule.Filters)
	return rule, nil
}

func normalizedRuleFilters(filters []RoutingFilter) []RoutingFilter {
	if len(filters) == 0 {
		return []RoutingFilter{{ID: "any", Type: "any", Label: "Any available provider"}}
	}
	out := make([]RoutingFilter, 0, len(filters))
	for index, filter := range filters {
		filter.Type = strings.ToLower(strings.TrimSpace(filter.Type))
		if filter.Type == "" {
			filter.Type = "criteria"
		}
		filter.ID = strings.TrimSpace(filter.ID)
		if filter.ID == "" {
			filter.ID = fmt.Sprintf("filter-%02d", index+1)
		}
		filter.Label = strings.TrimSpace(filter.Label)
		if filter.Label == "" {
			filter.Label = filter.Type
		}
		out = append(out, filter)
	}
	return out
}

func routingRuleID(scope RoutingRuleScope, ownerEmail string, name string) string {
	scope = normalizeRoutingRuleScope(scope)
	name = strings.TrimSpace(name)
	if scope == RoutingRuleScopeUser {
		return "user:" + normalizeUserEmail(ownerEmail) + ":" + name
	}
	return "public:" + name
}

func validRoutingRuleName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || url.PathEscape(name) != name {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '.', r == '_', r == '~':
		default:
			return false
		}
	}
	return true
}

func normalizeRoutingRuleScope(scope RoutingRuleScope) RoutingRuleScope {
	switch RoutingRuleScope(strings.ToLower(strings.TrimSpace(string(scope)))) {
	case RoutingRuleScopePublic, "":
		return RoutingRuleScopePublic
	case RoutingRuleScopeUser:
		return RoutingRuleScopeUser
	default:
		return ""
	}
}

func stringInSet(value string, set []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range set {
		if value == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func serviceInSet(value provider.Service, set []provider.Service) bool {
	for _, candidate := range set {
		if value == candidate {
			return true
		}
	}
	return false
}

func dialectInSet(value compat.APIDialect, set []compat.APIDialect) bool {
	for _, candidate := range set {
		if value == candidate {
			return true
		}
	}
	return false
}

func healthInSet(value provider.HealthStatus, set []provider.HealthStatus) bool {
	for _, candidate := range set {
		if value == candidate {
			return true
		}
	}
	return false
}

func authInSet(value provider.AuthStatus, set []provider.AuthStatus) bool {
	for _, candidate := range set {
		if value == candidate {
			return true
		}
	}
	return false
}

func modelInSet(models []provider.Model, criteria []string, requested string) bool {
	if stringInSet(requested, criteria) {
		return true
	}
	for _, model := range models {
		if stringInSet(model.ID, criteria) {
			return true
		}
		for _, alias := range model.Aliases {
			if stringInSet(alias, criteria) {
				return true
			}
		}
	}
	return false
}

func requestedModelAllowedByCriteria(models []provider.Model, requested string, criteria []string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return true
	}
	if stringInSet(requested, criteria) {
		return true
	}
	for _, model := range models {
		if modelMatchesAny(model, []string{requested}) && modelMatchesAny(model, criteria) {
			return true
		}
	}
	return false
}

func providerModelQuotaExhausted(models []provider.Model, requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	foundQuota := false
	hasAvailableQuota := false
	for _, model := range models {
		if !modelMatchesAny(model, []string{requested}) || model.Quota == nil {
			continue
		}
		foundQuota = true
		if model.Quota.RemainingPct > 0 {
			hasAvailableQuota = true
		}
	}
	return foundQuota && !hasAvailableQuota
}

func routingRuleModelScore(models []provider.Model, requested string) int {
	requested = strings.TrimSpace(requested)
	if requested == "" || len(models) == 0 {
		return 0
	}
	for _, model := range models {
		if model.ID == requested {
			return 40
		}
		for _, alias := range model.Aliases {
			if alias == requested {
				return 30
			}
		}
		for _, member := range model.GroupMembers {
			if member == requested {
				return 20
			}
		}
	}
	return 0
}

func (request RouteRequest) RequiredCapabilities() []provider.Capability {
	switch request.APIDialect {
	case compat.APIDialectOpenAI:
		return []provider.Capability{provider.CapabilityOpenAIChat}
	case compat.APIDialectAnthropic:
		return []provider.Capability{provider.CapabilityAnthropicMessages}
	case compat.APIDialectGemini:
		return []provider.Capability{provider.CapabilityGeminiGenerateContent}
	default:
		return nil
	}
}
