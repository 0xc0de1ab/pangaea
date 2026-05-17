// Package router contains v2 router-core primitives that are independent of
// the legacy auth-sync server.
package router

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"gopkg.in/yaml.v3"
)

const RoutingPolicyVersion = "routing-policy/v1"

var (
	ErrInvalidPolicy = errors.New("invalid routing policy")
	ErrNoRoute       = errors.New("no route matched")
	ErrNoProvider    = errors.New("no provider matched")
)

type RoutingPolicy struct {
	Version      string                `json:"version" yaml:"version"`
	ModelAliases map[string]ModelAlias `json:"model_aliases,omitempty" yaml:"model_aliases,omitempty"`
	Routes       []Route               `json:"routes" yaml:"routes"`
}

type ModelAlias struct {
	CanonicalModel       string                `json:"canonical_model" yaml:"canonical_model"`
	CanonicalModels      []string              `json:"canonical_models,omitempty" yaml:"canonical_models,omitempty"`
	RequiredCapabilities []provider.Capability `json:"required_capabilities,omitempty" yaml:"required_capabilities,omitempty"`
}

type Route struct {
	ID          string      `json:"id" yaml:"id"`
	Match       RouteMatch  `json:"match,omitempty" yaml:"match,omitempty"`
	Candidates  []Candidate `json:"candidates" yaml:"candidates"`
	Constraints Constraints `json:"constraints,omitempty" yaml:"constraints,omitempty"`
}

type RouteMatch struct {
	Models      []string            `json:"models,omitempty" yaml:"models,omitempty"`
	Tenants     []string            `json:"tenants,omitempty" yaml:"tenants,omitempty"`
	APIDialects []compat.APIDialect `json:"api_dialects,omitempty" yaml:"api_dialects,omitempty"`
}

type Candidate struct {
	ProviderType       string `json:"provider_type" yaml:"provider_type"`
	ProviderInstanceID string `json:"provider_instance_id,omitempty" yaml:"provider_instance_id,omitempty"`
	Account            string `json:"account,omitempty" yaml:"account,omitempty"`
	HostName           string `json:"host_name,omitempty" yaml:"host_name,omitempty"`
	Weight             int    `json:"weight,omitempty" yaml:"weight,omitempty"`
}

type Constraints struct {
	RequiredCapabilities []provider.Capability   `json:"required_capabilities,omitempty" yaml:"required_capabilities,omitempty"`
	AuthStatus           []provider.AuthStatus   `json:"auth_status,omitempty" yaml:"auth_status,omitempty"`
	HealthState          []provider.HealthStatus `json:"health_state,omitempty" yaml:"health_state,omitempty"`
	ProviderKind         []provider.Kind         `json:"provider_kind,omitempty" yaml:"provider_kind,omitempty"`
	NodeID               []string                `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	HostName             []string                `json:"host_name,omitempty" yaml:"host_name,omitempty"`
	Account              []string                `json:"account,omitempty" yaml:"account,omitempty"`
	MaxQueueDepth        int                     `json:"max_queue_depth,omitempty" yaml:"max_queue_depth,omitempty"`
}

type RouteRequest struct {
	TenantID           string            `json:"tenant_id,omitempty"`
	UserID             string            `json:"user_id,omitempty"`
	APIKeyID           string            `json:"api_key_id,omitempty"`
	RoutingRuleID      string            `json:"routing_rule_id,omitempty"`
	RoutingRuleName    string            `json:"routing_rule_name,omitempty"`
	RoutingRuleOwner   string            `json:"routing_rule_owner,omitempty"`
	ProviderInstanceID string            `json:"provider_instance_id,omitempty"`
	ProviderType       string            `json:"provider_type,omitempty"`
	Model              string            `json:"model"`
	APIDialect         compat.APIDialect `json:"api_dialect"`
	Stream             bool              `json:"stream,omitempty"`
	Features           []string          `json:"features,omitempty"`
}

type RouteDecision struct {
	Allowed              bool                   `json:"allowed"`
	RouteID              string                 `json:"route_id,omitempty"`
	RoutingRuleID        string                 `json:"routing_rule_id,omitempty"`
	ModelAlias           string                 `json:"model_alias,omitempty"`
	CanonicalModel       string                 `json:"canonical_model,omitempty"`
	Selected             string                 `json:"selected,omitempty"`
	SelectedProvider     *provider.Registration `json:"selected_provider,omitempty"`
	RequiredCapabilities []provider.Capability  `json:"required_capabilities,omitempty"`
	FallbackChain        []string               `json:"fallback_chain,omitempty"`
	Scores               []RouteCandidateScore  `json:"scores,omitempty"`
	Rejections           []RouteRejection       `json:"rejections,omitempty"`
	Reason               string                 `json:"reason,omitempty"`
}

type RouteCandidateScore struct {
	ProviderInstanceID string `json:"provider_instance_id,omitempty"`
	ProviderType       string `json:"provider_type,omitempty"`
	Score              int    `json:"score"`
	Weight             int    `json:"weight,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type RouteRejection struct {
	ProviderInstanceID string `json:"provider_instance_id,omitempty"`
	ProviderType       string `json:"provider_type,omitempty"`
	Reason             string `json:"reason"`
}

type scoredCandidate struct {
	registration provider.Registration
	modelAlias   string
	canonical    string
	score        int
	weight       int
	reason       string
}

func ParseRoutingPolicyYAML(data []byte) (RoutingPolicy, error) {
	var policy RoutingPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return RoutingPolicy{}, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	if err := policy.Validate(); err != nil {
		return RoutingPolicy{}, err
	}
	return policy, nil
}

func (p RoutingPolicy) Validate() error {
	if strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("%w: missing version", ErrInvalidPolicy)
	}
	if p.Version != RoutingPolicyVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidPolicy, p.Version)
	}
	if len(p.Routes) == 0 {
		return fmt.Errorf("%w: routes are required", ErrInvalidPolicy)
	}
	for aliasName, alias := range p.ModelAliases {
		if strings.TrimSpace(aliasName) == "" || len(modelAliasCanonicalModels(alias)) == 0 {
			return fmt.Errorf("%w: invalid model alias %q", ErrInvalidPolicy, aliasName)
		}
		if err := validateCapabilities(alias.RequiredCapabilities); err != nil {
			return err
		}
	}
	for _, route := range p.Routes {
		if err := route.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r Route) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("%w: route id is required", ErrInvalidPolicy)
	}
	if len(r.Candidates) == 0 {
		return fmt.Errorf("%w: route %q has no candidates", ErrInvalidPolicy, r.ID)
	}
	for _, candidate := range r.Candidates {
		if strings.TrimSpace(candidate.ProviderType) == "" {
			return fmt.Errorf("%w: route %q has candidate without provider_type", ErrInvalidPolicy, r.ID)
		}
		if candidate.Weight < 0 {
			return fmt.Errorf("%w: route %q has negative candidate weight", ErrInvalidPolicy, r.ID)
		}
	}
	if err := validateCapabilities(r.Constraints.RequiredCapabilities); err != nil {
		return err
	}
	return nil
}

func (p RoutingPolicy) Evaluate(request RouteRequest, registrations []provider.Registration) RouteDecision {
	if err := p.Validate(); err != nil {
		return RouteDecision{Reason: err.Error(), Rejections: []RouteRejection{{Reason: err.Error()}}}
	}
	alias := p.ModelAliases[request.Model]
	canonicalModels := []string{request.Model}
	required := make([]provider.Capability, 0)
	if candidates := modelAliasCanonicalModels(alias); len(candidates) > 0 {
		canonicalModels = candidates
		required = append(required, alias.RequiredCapabilities...)
	}
	canonicalModel := canonicalModels[0]
	if request.Stream {
		required = appendCapability(required, provider.CapabilityStreamSSE)
	}
	if strings.TrimSpace(request.ProviderInstanceID) != "" {
		return p.evaluatePinnedProvider(request, registrations, canonicalModels, canonicalModel, required)
	}

	route, ok := p.matchRoute(request)
	if !ok {
		return RouteDecision{
			Allowed:        false,
			ModelAlias:     request.Model,
			CanonicalModel: canonicalModel,
			Reason:         ErrNoRoute.Error(),
			Rejections:     []RouteRejection{{Reason: ErrNoRoute.Error()}},
		}
	}
	required = append(required, route.Constraints.RequiredCapabilities...)
	required = uniqueCapabilities(required)

	decision := RouteDecision{
		RouteID:              route.ID,
		ModelAlias:           request.Model,
		CanonicalModel:       canonicalModel,
		RequiredCapabilities: required,
	}
	scored := make([]scoredCandidate, 0)
	allRejections := make([]RouteRejection, 0)
	for _, candidateCanonicalModel := range canonicalModels {
		modelScored := make([]scoredCandidate, 0)
		modelRejections := make([]RouteRejection, 0)
		for _, candidate := range route.Candidates {
			candidateMatches := false
			for _, registration := range registrations {
				if !candidateMatchesRegistration(candidate, registration) {
					continue
				}
				candidateMatches = true
				score, weight, scoreReason, rejection := evaluateRegistration(candidate, route.Constraints, required, request.Model, candidateCanonicalModel, registration)
				if rejection != "" {
					modelRejections = append(modelRejections, RouteRejection{
						ProviderInstanceID: registration.Identity.ProviderInstanceID,
						ProviderType:       registration.Identity.ProviderType,
						Reason:             rejection,
					})
					continue
				}
				modelScored = append(modelScored, scoredCandidate{registration: registration, canonical: candidateCanonicalModel, score: score, weight: weight, reason: scoreReason})
			}
			if !candidateMatches {
				modelRejections = append(modelRejections, RouteRejection{
					ProviderType: candidate.ProviderType,
					Reason:       "candidate provider not connected",
				})
			}
		}
		if len(modelScored) > 0 {
			scored = modelScored
			canonicalModel = candidateCanonicalModel
			decision.CanonicalModel = candidateCanonicalModel
			decision.Rejections = modelRejections
			break
		}
		allRejections = append(allRejections, modelRejections...)
	}
	if len(scored) == 0 {
		decision.Rejections = allRejections
	}
	now := time.Now().UTC()
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			if less, ok := compareCandidateQuotaReset(scored[i], scored[j], request.Model, now); ok {
				return less
			}
			return scored[i].registration.Identity.ProviderInstanceID < scored[j].registration.Identity.ProviderInstanceID
		}
		return scored[i].score > scored[j].score
	})
	for _, candidate := range scored {
		decision.FallbackChain = append(decision.FallbackChain, candidate.registration.Identity.ProviderInstanceID)
		decision.Scores = append(decision.Scores, RouteCandidateScore{
			ProviderInstanceID: candidate.registration.Identity.ProviderInstanceID,
			ProviderType:       candidate.registration.Identity.ProviderType,
			Score:              candidate.score,
			Weight:             candidate.weight,
			Reason:             candidate.reason,
		})
	}
	if len(scored) == 0 {
		decision.Reason = ErrNoProvider.Error()
		return decision
	}
	selected := scored[0].registration
	decision.Allowed = true
	decision.Selected = selected.Identity.ProviderInstanceID
	decision.SelectedProvider = &selected
	decision.Reason = "selected highest scoring candidate"
	return decision
}

func (p RoutingPolicy) evaluatePinnedProvider(request RouteRequest, registrations []provider.Registration, canonicalModels []string, canonicalModel string, required []provider.Capability) RouteDecision {
	providerInstanceID := strings.TrimSpace(request.ProviderInstanceID)
	required = append(required, capabilitiesForDialect(request.APIDialect)...)
	required = uniqueCapabilities(required)
	constraints := Constraints{
		AuthStatus:    []provider.AuthStatus{provider.AuthHealthy, provider.AuthRefreshSoon},
		HealthState:   []provider.HealthStatus{provider.HealthReady},
		MaxQueueDepth: 4,
	}
	decision := RouteDecision{
		RouteID:              "provider:" + providerInstanceID,
		ModelAlias:           request.Model,
		CanonicalModel:       canonicalModel,
		RequiredCapabilities: required,
	}
	scored := make([]scoredCandidate, 0, 1)
	allRejections := make([]RouteRejection, 0)
	foundProvider := false
	for _, candidateCanonicalModel := range canonicalModels {
		modelScored := make([]scoredCandidate, 0, 1)
		modelRejections := make([]RouteRejection, 0)
		for _, registration := range registrations {
			if registration.Identity.ProviderInstanceID != providerInstanceID {
				continue
			}
			foundProvider = true
			if request.ProviderType != "" && registration.Identity.ProviderType != request.ProviderType {
				modelRejections = append(modelRejections, RouteRejection{
					ProviderInstanceID: registration.Identity.ProviderInstanceID,
					ProviderType:       registration.Identity.ProviderType,
					Reason:             "provider_type constraint not matched",
				})
				continue
			}
			candidate := Candidate{
				ProviderType:       registration.Identity.ProviderType,
				ProviderInstanceID: providerInstanceID,
				Weight:             100,
			}
			score, weight, scoreReason, rejection := evaluateRegistration(candidate, constraints, required, request.Model, candidateCanonicalModel, registration)
			if rejection != "" {
				modelRejections = append(modelRejections, RouteRejection{
					ProviderInstanceID: registration.Identity.ProviderInstanceID,
					ProviderType:       registration.Identity.ProviderType,
					Reason:             rejection,
				})
				continue
			}
			modelScored = append(modelScored, scoredCandidate{registration: registration, canonical: candidateCanonicalModel, score: score, weight: weight, reason: scoreReason})
		}
		if len(modelScored) > 0 {
			scored = modelScored
			canonicalModel = candidateCanonicalModel
			decision.CanonicalModel = candidateCanonicalModel
			decision.Rejections = modelRejections
			break
		}
		allRejections = append(allRejections, modelRejections...)
	}
	if !foundProvider {
		allRejections = append(allRejections, RouteRejection{
			ProviderInstanceID: providerInstanceID,
			ProviderType:       request.ProviderType,
			Reason:             "pinned provider not connected",
		})
	}
	if len(scored) == 0 {
		decision.Rejections = allRejections
		decision.Reason = ErrNoProvider.Error()
		return decision
	}
	now := time.Now().UTC()
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			if less, ok := compareCandidateQuotaReset(scored[i], scored[j], request.Model, now); ok {
				return less
			}
			return scored[i].registration.Identity.ProviderInstanceID < scored[j].registration.Identity.ProviderInstanceID
		}
		return scored[i].score > scored[j].score
	})
	for _, candidate := range scored {
		decision.FallbackChain = append(decision.FallbackChain, candidate.registration.Identity.ProviderInstanceID)
		decision.Scores = append(decision.Scores, RouteCandidateScore{
			ProviderInstanceID: candidate.registration.Identity.ProviderInstanceID,
			ProviderType:       candidate.registration.Identity.ProviderType,
			Score:              candidate.score,
			Weight:             candidate.weight,
			Reason:             candidate.reason,
		})
	}
	selected := scored[0].registration
	decision.Allowed = true
	decision.Selected = selected.Identity.ProviderInstanceID
	decision.SelectedProvider = &selected
	decision.Reason = "selected pinned provider"
	return decision
}

func capabilitiesForDialect(dialect compat.APIDialect) []provider.Capability {
	switch dialect {
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

func (p RoutingPolicy) matchRoute(request RouteRequest) (Route, bool) {
	for _, route := range p.Routes {
		if !matchString(route.Match.Models, request.Model) {
			continue
		}
		if !matchString(route.Match.Tenants, request.TenantID) {
			continue
		}
		if !matchDialect(route.Match.APIDialects, request.APIDialect) {
			continue
		}
		return route, true
	}
	return Route{}, false
}

func evaluateRegistration(candidate Candidate, constraints Constraints, required []provider.Capability, requestedModel string, canonicalModel string, registration provider.Registration) (int, int, string, string) {
	for _, capability := range required {
		if !hasCapability(registration.Capabilities, capability) {
			return 0, 0, "", "required capability missing: " + string(capability)
		}
	}
	if rejection := evaluateModelSupport(requestedModel, canonicalModel, required, registration.Models); rejection != "" {
		return 0, 0, "", rejection
	}
	if len(constraints.AuthStatus) > 0 && !hasAuthStatus(constraints.AuthStatus, registration.Auth.Status) {
		return 0, 0, "", "auth status not allowed: " + string(registration.Auth.Status)
	}
	if len(constraints.HealthState) > 0 && !hasHealthStatus(constraints.HealthState, registration.Health.Status) {
		reason := "health state not allowed: " + string(registration.Health.Status)
		if strings.TrimSpace(registration.Health.Reason) != "" {
			reason += " (" + strings.TrimSpace(registration.Health.Reason) + ")"
		}
		return 0, 0, "", reason
	}
	if len(constraints.ProviderKind) > 0 && !hasKind(constraints.ProviderKind, registration.Identity.Kind) {
		return 0, 0, "", "provider kind not allowed: " + string(registration.Identity.Kind)
	}
	if !matchString(constraints.NodeID, registration.Identity.NodeID) {
		return 0, 0, "", "node_id constraint not matched"
	}
	if !matchString(constraints.HostName, registration.Identity.HostName) {
		return 0, 0, "", "host_name constraint not matched"
	}
	if !matchAccount(constraints.Account, registration.Identity.Account) {
		return 0, 0, "", "account constraint not matched"
	}
	if constraints.MaxQueueDepth > 0 && registration.Limits.QueueDepth > constraints.MaxQueueDepth {
		return 0, 0, "", fmt.Sprintf("queue_depth %d > max_queue_depth %d", registration.Limits.QueueDepth, constraints.MaxQueueDepth)
	}
	weight := candidate.Weight
	if weight == 0 {
		weight = 1
	}
	return weight, weight, fmt.Sprintf("candidate weight %d", weight), ""
}

func evaluateModelSupport(requestedModel string, canonicalModel string, required []provider.Capability, models []provider.Model) string {
	if len(models) == 0 {
		return ""
	}
	names := uniqueStrings([]string{requestedModel, canonicalModel})
	if len(names) == 0 {
		return ""
	}
	for _, model := range models {
		if !modelMatchesAny(model, names) {
			continue
		}
		if len(model.Capabilities) == 0 {
			return ""
		}
		for _, capability := range required {
			if capability == provider.CapabilityStreamSSE {
				continue
			}
			if !hasCapability(model.Capabilities, capability) {
				return fmt.Sprintf("model %q missing capability: %s", model.ID, capability)
			}
		}
		return ""
	}
	return fmt.Sprintf("model not reported by provider: %s", names[0])
}

func compareCandidateQuotaReset(left scoredCandidate, right scoredCandidate, requestedModel string, now time.Time) (bool, bool) {
	leftReset, leftOK := candidateQuotaResetAt(left, requestedModel, now)
	rightReset, rightOK := candidateQuotaResetAt(right, requestedModel, now)
	if leftOK != rightOK {
		return leftOK, true
	}
	if !leftOK {
		return false, false
	}
	if leftReset.Equal(rightReset) {
		return false, false
	}
	return leftReset.Before(rightReset), true
}

func candidateQuotaResetAt(candidate scoredCandidate, requestedModel string, now time.Time) (time.Time, bool) {
	names := uniqueStrings([]string{requestedModel, candidate.canonical})
	if len(names) == 0 {
		return time.Time{}, false
	}
	var best time.Time
	for _, model := range candidate.registration.Models {
		if model.Quota == nil || !modelMatchesAny(model, names) {
			continue
		}
		resetAt := model.Quota.ResetAt
		if resetAt.IsZero() || !resetAt.After(now) {
			continue
		}
		if best.IsZero() || resetAt.Before(best) {
			best = resetAt
		}
	}
	if best.IsZero() {
		return time.Time{}, false
	}
	return best, true
}

func modelMatchesAny(model provider.Model, names []string) bool {
	for _, name := range names {
		if model.ID == name {
			return true
		}
		for _, alias := range model.Aliases {
			if alias == name {
				return true
			}
		}
		for _, member := range model.GroupMembers {
			if member == name {
				return true
			}
		}
	}
	return false
}

func candidateMatchesRegistration(candidate Candidate, registration provider.Registration) bool {
	if candidate.ProviderType != registration.Identity.ProviderType {
		return false
	}
	if candidate.ProviderInstanceID != "" && candidate.ProviderInstanceID != registration.Identity.ProviderInstanceID {
		return false
	}
	if candidate.HostName != "" && candidate.HostName != registration.Identity.HostName {
		return false
	}
	if candidate.Account != "" && !accountMatches(candidate.Account, registration.Identity.Account) {
		return false
	}
	return true
}

func matchString(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}

func matchDialect(allowed []compat.APIDialect, value compat.APIDialect) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}

func matchAccount(allowed []string, account provider.Account) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if accountMatches(item, account) {
			return true
		}
	}
	return false
}

func accountMatches(want string, account provider.Account) bool {
	return want == account.ID || want == account.Display
}

func hasCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func hasAuthStatus(statuses []provider.AuthStatus, want provider.AuthStatus) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

func hasHealthStatus(statuses []provider.HealthStatus, want provider.HealthStatus) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

func hasKind(kinds []provider.Kind, want provider.Kind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func validateCapabilities(capabilities []provider.Capability) error {
	for _, capability := range capabilities {
		if !capability.Valid() {
			return fmt.Errorf("%w: invalid capability %q", ErrInvalidPolicy, capability)
		}
	}
	return nil
}

func modelAliasCanonicalModels(alias ModelAlias) []string {
	models := uniqueStrings(alias.CanonicalModels)
	if len(models) > 0 {
		return models
	}
	return uniqueStrings([]string{alias.CanonicalModel})
}

func appendCapability(capabilities []provider.Capability, capability provider.Capability) []provider.Capability {
	if hasCapability(capabilities, capability) {
		return capabilities
	}
	return append(capabilities, capability)
}

func uniqueCapabilities(capabilities []provider.Capability) []provider.Capability {
	out := make([]provider.Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		out = appendCapability(out, capability)
	}
	return out
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
