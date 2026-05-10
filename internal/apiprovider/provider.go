// Package apiprovider implements generic API-compatible provider invokers.
package apiprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

var ErrAPIProviderConfig = errors.New("invalid direct-http provider config")

const authExpiryRefreshThreshold = 5 * time.Minute

type Options struct {
	Registration     provider.Registration
	BaseURL          string
	Dialect          compat.APIDialect
	APIKey           string
	APIKeyFile       string
	APIKeyMode       string
	APIKeyHeader     string
	APIKeyQueryParam string
	Headers          map[string]string
	HTTPClient       *http.Client
}

type Provider struct {
	registration     provider.Registration
	baseURL          *url.URL
	dialect          compat.APIDialect
	apiKey           string
	apiKeyFile       string
	apiKeyMode       string
	apiKeyHeader     string
	apiKeyQueryParam string
	headers          map[string]string
	client           *http.Client
	usageMu          sync.Mutex
	usage            provider.UsageReport
	healthMu         sync.Mutex
	health           provider.Health
	authMu           sync.Mutex
	auth             provider.AuthState
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAPIProviderConfig, err)
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("%w: base url is required", ErrAPIProviderConfig)
	}
	baseURL, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("%w: base url must include scheme and host", ErrAPIProviderConfig)
	}
	if !opts.Dialect.Valid() {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, opts.Dialect)
	}
	apiKeyMode, apiKeyHeader, apiKeyQueryParam, err := normalizeAPIKeyAuth(opts.APIKeyMode, opts.APIKeyHeader, opts.APIKeyQueryParam)
	if err != nil {
		return nil, err
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Provider{
		registration:     opts.Registration,
		baseURL:          baseURL,
		dialect:          opts.Dialect,
		apiKey:           strings.TrimSpace(opts.APIKey),
		apiKeyFile:       strings.TrimSpace(opts.APIKeyFile),
		apiKeyMode:       apiKeyMode,
		apiKeyHeader:     apiKeyHeader,
		apiKeyQueryParam: apiKeyQueryParam,
		headers:          cloneHeaders(opts.Headers),
		client:           client,
		usage: provider.UsageReport{
			ObservedAt: time.Now().UTC(),
			Source:     "direct-http",
		},
		health: initialHealth(opts.Registration.Health),
		auth:   opts.Registration.Auth,
	}, nil
}

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrAPIProviderConfig
	}
	return p.registration, nil
}

func (p *Provider) TargetVersion(ctx context.Context) (string, error) {
	if p == nil || p.client == nil {
		return "", ErrAPIProviderConfig
	}
	current := strings.TrimSpace(p.registration.Identity.TargetVersion)
	if p.registration.Identity.Service != provider.ServiceAntigravity {
		return current, nil
	}
	var response struct {
		TargetVersion string `json:"target_version"`
		ServerVersion string `json:"server_version"`
		Version       string `json:"version"`
	}
	if err := p.doGETJSON(ctx, "/v1/health", &response); err != nil {
		return current, err
	}
	if version := strings.TrimSpace(firstNonEmpty(response.TargetVersion, response.ServerVersion, response.Version)); version != "" {
		return version, nil
	}
	return current, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	if p == nil || p.client == nil {
		return nil, ErrAPIProviderConfig
	}
	switch p.dialect {
	case compat.APIDialectOpenAI, compat.APIDialectAnthropic:
		var response compatibleModelsResponse
		if err := p.doGETJSON(ctx, "/v1/models", &response); err != nil {
			return nil, err
		}
		models := compatibleModels(response.Data, p.compatibleModelCapabilities())
		p.enrichModelsFromStatus(ctx, models)
		return models, nil
	case compat.APIDialectGemini:
		var response geminiModelsResponse
		if err := p.doGETJSON(ctx, "/v1beta/models", &response); err != nil {
			return nil, err
		}
		models := geminiModels(response.Models)
		p.enrichModelsFromStatus(ctx, models)
		return models, nil
	default:
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, p.dialect)
	}
}

type compatibleModelsResponse struct {
	Data []compatibleModel `json:"data"`
}

type compatibleModel struct {
	ID string `json:"id"`
}

type geminiModelsResponse struct {
	Models []geminiModel `json:"models"`
}

type geminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
	InputTokenLimit            int      `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit           int      `json:"outputTokenLimit,omitempty"`
}

type compatibleModelStatus struct {
	Model          string                      `json:"model"`
	Label          string                      `json:"label,omitempty"`
	MaxTokens      int                         `json:"maxTokens,omitempty"`
	QuotaInfo      *compatibleModelStatusQuota `json:"quotaInfo,omitempty"`
	SupportsImages bool                        `json:"supportsImages,omitempty"`
}

type compatibleModelStatusQuota struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
}

type antigravityAccountResponse struct {
	Name       string                 `json:"name"`
	Email      string                 `json:"email"`
	PlanStatus *antigravityPlanStatus `json:"planStatus"`
	UserTier   *antigravityUserTier   `json:"userTier"`
}

type antigravityPlanStatus struct {
	PlanInfo *antigravityPlanInfo `json:"planInfo"`
}

type antigravityPlanInfo struct {
	PlanName string `json:"planName"`
}

type antigravityUserTier struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	UpgradeSubscriptionText string `json:"upgradeSubscriptionText"`
}

func compatibleModels(items []compatibleModel, capabilities []provider.Capability) []provider.Model {
	models := make([]provider.Model, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		models = append(models, provider.Model{
			ID:           id,
			Capabilities: capabilities,
		})
	}
	return models
}

func (p *Provider) enrichModelsFromStatus(ctx context.Context, models []provider.Model) {
	if len(models) == 0 {
		return
	}
	var status map[string]compatibleModelStatus
	if err := p.doGETJSON(ctx, "/v1/models/status", &status); err != nil || len(status) == 0 {
		return
	}
	for i := range models {
		detail, ok := status[models[i].ID]
		if !ok {
			continue
		}
		if detail.Label != "" && detail.Label != models[i].ID {
			models[i].Aliases = mergeStringSet(models[i].Aliases, []string{detail.Label})
		}
		if detail.MaxTokens > 0 {
			if models[i].ContextTokens == 0 {
				models[i].ContextTokens = detail.MaxTokens
			}
			if models[i].MaxContextTokens == 0 {
				models[i].MaxContextTokens = detail.MaxTokens
			}
		}
		if detail.SupportsImages {
			models[i].Capabilities = appendProviderCapability(models[i].Capabilities, provider.CapabilityGeminiGenerateContent)
		}
		if detail.QuotaInfo != nil {
			quota := &provider.ModelQuota{
				RemainingPct: detail.QuotaInfo.RemainingFraction * 100,
				Source:       "antigravity-model-quota",
			}
			if resetAt := parseModelQuotaReset(detail.QuotaInfo.ResetTime); !resetAt.IsZero() {
				quota.ResetAt = resetAt
			}
			models[i].Quota = quota
		}
	}
}

func mergeStringSet(left []string, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	out := make([]string, 0, len(left)+len(right))
	for _, value := range append(left, right...) {
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

func parseModelQuotaReset(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return time.Time{}
}

func (p *Provider) compatibleModelCapabilities() []provider.Capability {
	capabilities := []provider.Capability(nil)
	for _, capability := range p.registration.Capabilities {
		switch capability {
		case provider.CapabilityOpenAIChat,
			provider.CapabilityOpenAIResponses,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE:
			capabilities = appendProviderCapability(capabilities, capability)
		}
	}
	if len(capabilities) == 0 {
		capabilities = appendProviderCapability(capabilities, capabilityForAPIDialect(p.dialect))
		capabilities = appendProviderCapability(capabilities, provider.CapabilityStreamSSE)
	}
	return capabilities
}

func geminiModels(items []geminiModel) []provider.Model {
	models := make([]provider.Model, 0, len(items))
	for _, item := range items {
		id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
		if id == "" {
			continue
		}
		capabilities := geminiModelCapabilities(item.SupportedGenerationMethods)
		aliases := []string(nil)
		if display := strings.TrimSpace(item.DisplayName); display != "" && display != id {
			aliases = []string{display}
		}
		models = append(models, provider.Model{
			ID:               id,
			Aliases:          aliases,
			Capabilities:     capabilities,
			ContextTokens:    item.InputTokenLimit,
			MaxContextTokens: item.InputTokenLimit,
			MaxOutputTokens:  item.OutputTokenLimit,
		})
	}
	return models
}

func geminiModelCapabilities(methods []string) []provider.Capability {
	capabilities := []provider.Capability{}
	for _, method := range methods {
		switch strings.TrimSpace(method) {
		case "generateContent":
			capabilities = appendProviderCapability(capabilities, provider.CapabilityGeminiGenerateContent)
		case "streamGenerateContent":
			capabilities = appendProviderCapability(capabilities, provider.CapabilityStreamSSE)
		}
	}
	if len(capabilities) == 0 {
		capabilities = append(capabilities, provider.CapabilityGeminiGenerateContent)
	}
	return capabilities
}

func capabilityForAPIDialect(dialect compat.APIDialect) provider.Capability {
	switch dialect {
	case compat.APIDialectAnthropic:
		return provider.CapabilityAnthropicMessages
	case compat.APIDialectGemini:
		return provider.CapabilityGeminiGenerateContent
	default:
		return provider.CapabilityOpenAIChat
	}
}

func appendProviderCapability(capabilities []provider.Capability, capability provider.Capability) []provider.Capability {
	for _, existing := range capabilities {
		if existing == capability {
			return capabilities
		}
	}
	return append(capabilities, capability)
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if p == nil || p.client == nil {
		return compat.Response{}, ErrAPIProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrAPIProviderConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	switch p.dialect {
	case compat.APIDialectOpenAI:
		response, err := p.invokeOpenAI(ctx, request)
		p.recordInvocationResult(response.Usage, err)
		return response, err
	case compat.APIDialectAnthropic:
		response, err := p.invokeAnthropic(ctx, request)
		p.recordInvocationResult(response.Usage, err)
		return response, err
	case compat.APIDialectGemini:
		response, err := p.invokeGemini(ctx, request)
		p.recordInvocationResult(response.Usage, err)
		return response, err
	default:
		return compat.Response{}, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, p.dialect)
	}
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if p == nil || p.client == nil {
		return compat.Response{}, ErrAPIProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrAPIProviderConfig)
	}
	if emit == nil {
		return compat.Response{}, fmt.Errorf("%w: stream emit callback is required", ErrAPIProviderConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	switch p.dialect {
	case compat.APIDialectOpenAI:
		response, err := p.invokeOpenAIStream(ctx, request, emit)
		p.recordInvocationResult(response.Usage, err)
		return response, err
	case compat.APIDialectAnthropic:
		response, err := p.invokeAnthropicStream(ctx, request, emit)
		p.recordInvocationResult(response.Usage, err)
		return response, err
	case compat.APIDialectGemini:
		response, err := p.invokeGeminiStream(ctx, request, emit)
		p.recordInvocationResult(response.Usage, err)
		return response, err
	default:
		return compat.Response{}, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, p.dialect)
	}
}

func (p *Provider) Usage() (provider.UsageReport, error) {
	if p == nil {
		return provider.UsageReport{}, ErrAPIProviderConfig
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	usage := p.usage
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	if usage.Source == "" {
		usage.Source = "direct-http"
	}
	return usage, nil
}

func (p *Provider) Health() (provider.Health, error) {
	if p == nil {
		return provider.Health{}, ErrAPIProviderConfig
	}
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	health := p.health
	if health.Status == "" {
		health.Status = provider.HealthReady
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = time.Now().UTC()
	}
	return health, nil
}

func (p *Provider) Auth() (provider.AuthState, error) {
	if p == nil {
		return provider.AuthState{}, ErrAPIProviderConfig
	}
	if p.registration.Identity.Service == provider.ServiceAntigravity {
		p.refreshAntigravityAccount()
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	auth := p.auth
	if auth.Status == "" {
		auth.Status = provider.AuthHealthy
	}
	return auth, nil
}

func (p *Provider) refreshAntigravityAccount() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var account antigravityAccountResponse
	if err := p.doGETJSON(ctx, "/v1/account", &account); err != nil {
		return
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	if account.Email != "" {
		p.auth.Account.ID = strings.TrimSpace(account.Email)
		if p.auth.Account.Display == "" || strings.Contains(p.auth.Account.Display, "@") {
			p.auth.Account.Display = strings.TrimSpace(account.Email)
		}
	}
	if p.auth.Account.Display == "" {
		p.auth.Account.Display = strings.TrimSpace(account.Name)
	}
	if subscription := subscriptionFromAntigravityAccount(account); subscription != nil {
		p.auth.Subscription = mergeProviderSubscription(p.auth.Subscription, subscription)
	}
}

func subscriptionFromAntigravityAccount(account antigravityAccountResponse) *provider.SubscriptionInfo {
	var tierName, tierID, status, planName string
	if account.UserTier != nil {
		tierName = strings.TrimSpace(account.UserTier.Name)
		tierID = strings.TrimSpace(account.UserTier.ID)
		status = strings.TrimSpace(account.UserTier.UpgradeSubscriptionText)
	}
	if account.PlanStatus != nil && account.PlanStatus.PlanInfo != nil {
		planName = strings.TrimSpace(account.PlanStatus.PlanInfo.PlanName)
	}
	displayName := firstNonEmpty(tierName, planName)
	info := provider.SubscriptionInfo{
		Tier:   firstNonEmpty(tierName, tierID, planName),
		Name:   displayName,
		Status: status,
		Source: "antigravity-account",
	}
	if info.Tier == "" && info.Name == "" && info.Status == "" && info.PaidTier == "" {
		return nil
	}
	return &info
}

func mergeProviderSubscription(base, update *provider.SubscriptionInfo) *provider.SubscriptionInfo {
	if base == nil || *base == (provider.SubscriptionInfo{}) {
		return update
	}
	if update == nil || *update == (provider.SubscriptionInfo{}) {
		return base
	}
	merged := *base
	if update.Tier != "" {
		merged.Tier = update.Tier
	}
	if update.Name != "" {
		merged.Name = update.Name
	}
	if update.Status != "" {
		merged.Status = update.Status
	}
	if update.PaidTier != "" {
		merged.PaidTier = update.PaidTier
	}
	if update.RateLimitTier != "" {
		merged.RateLimitTier = update.RateLimitTier
	}
	if merged.Source == "" {
		merged.Source = update.Source
	} else if update.Source != "" && !strings.Contains(merged.Source, update.Source) {
		merged.Source = merged.Source + "+" + update.Source
	}
	return &merged
}

func (p *Provider) invokeOpenAI(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.OpenAIChatRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = false
	var upstreamResponse compat.OpenAIChatResponse
	if err := p.doJSON(ctx, http.MethodPost, "/v1/chat/completions", upstreamRequest, &upstreamResponse); err != nil {
		return compat.Response{}, err
	}
	if upstreamResponse.Model == "" {
		upstreamResponse.Model = request.Model
	}
	response, err := compat.OpenAIChatResponseToCanonical(upstreamResponse)
	if err != nil {
		return compat.Response{}, err
	}
	response.Dialect = request.Dialect
	return response, nil
}

type openAIChatStreamChunk struct {
	ID      string                     `json:"id,omitempty"`
	Model   string                     `json:"model,omitempty"`
	Choices []openAIChatStreamChoice   `json:"choices,omitempty"`
	Usage   *compat.OpenAIUsage        `json:"usage,omitempty"`
	Error   *openAIChatStreamErrorBody `json:"error,omitempty"`
}

type openAIChatStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        openAIChatStreamDelta `json:"delta"`
	FinishReason string                `json:"finish_reason,omitempty"`
}

type openAIChatStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIChatStreamErrorBody struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func (p *Provider) invokeOpenAIStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	upstreamRequest, err := compat.OpenAIChatRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = true
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1/chat/completions", upstreamRequest, "text/event-stream")
	if err != nil {
		return compat.Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return compat.Response{}, readErr
		}
		return compat.Response{}, upstreamHTTPError(resp, responseBody)
	}
	response := compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant},
	}
	started := false
	if err := processSSEPayloads(resp.Body, func(payload string) (bool, error) {
		return applyOpenAIStreamPayload(&response, &started, payload, emit)
	}); err != nil {
		return compat.Response{}, err
	}
	if !started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return compat.Response{}, err
		}
	}
	if response.StopReason == "" {
		response.StopReason = "stop"
	}
	if err := response.Validate(); err != nil {
		return compat.Response{}, err
	}
	return response, nil
}

func applyOpenAIStreamPayload(response *compat.Response, started *bool, payload string, emit func(compat.Event) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		return true, nil
	}
	var chunk openAIChatStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, err
	}
	if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
		errEvent := compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventError,
			Error:      &compat.EventErrorPayload{Message: chunk.Error.Message, Code: stringFromAny(chunk.Error.Code)},
		}
		if err := emit(errEvent); err != nil {
			return false, err
		}
		return false, &provider.UpstreamError{Code: stringFromAny(chunk.Error.Code), Message: chunk.Error.Message}
	}
	if chunk.ID != "" {
		response.ID = chunk.ID
	}
	if chunk.Model != "" {
		response.Model = chunk.Model
	}
	if !*started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return false, err
		}
		*started = true
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			part := compat.ContentPart{Type: compat.ContentPartText, Text: choice.Delta.Content}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
		}
		if choice.FinishReason != "" {
			response.StopReason = choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		usage := compat.Usage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens:  chunk.Usage.TotalTokens,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
			return false, err
		}
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
			return false, err
		}
	}
	if response.StopReason != "" {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventDone, DoneReason: response.StopReason}); err != nil {
			return false, err
		}
	}
	return false, nil
}

type anthropicStreamDeltaPayload struct {
	Type    string                           `json:"type"`
	Index   int                              `json:"index,omitempty"`
	Message compat.AnthropicMessagesResponse `json:"message,omitempty"`
	Delta   anthropicStreamDeltaBody         `json:"delta,omitempty"`
	Usage   compat.AnthropicUsage            `json:"usage,omitempty"`
	Error   *anthropicStreamErrorBody        `json:"error,omitempty"`
}

type anthropicStreamDeltaBody struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

type anthropicStreamErrorBody struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

func (p *Provider) invokeAnthropicStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	upstreamRequest, err := compat.AnthropicMessagesRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = true
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1/messages", upstreamRequest, "text/event-stream")
	if err != nil {
		return compat.Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return compat.Response{}, readErr
		}
		return compat.Response{}, upstreamHTTPError(resp, responseBody)
	}
	response := compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant},
	}
	started := false
	if err := processSSEPayloads(resp.Body, func(payload string) (bool, error) {
		return applyAnthropicStreamPayload(&response, &started, payload, emit)
	}); err != nil {
		return compat.Response{}, err
	}
	if !started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return compat.Response{}, err
		}
	}
	if response.StopReason == "" {
		response.StopReason = "end_turn"
	}
	if err := response.Validate(); err != nil {
		return compat.Response{}, err
	}
	return response, nil
}

func applyAnthropicStreamPayload(response *compat.Response, started *bool, payload string, emit func(compat.Event) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return payload == "[DONE]", nil
	}
	var event anthropicStreamDeltaPayload
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return false, err
	}
	if event.Type == "error" && event.Error != nil {
		errEvent := compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventError,
			Error:      &compat.EventErrorPayload{Message: event.Error.Message, Code: event.Error.Type},
		}
		if err := emit(errEvent); err != nil {
			return false, err
		}
		return false, &provider.UpstreamError{Code: event.Error.Type, Message: event.Error.Message}
	}
	switch event.Type {
	case "message_start":
		if event.Message.ID != "" {
			response.ID = event.Message.ID
		}
		if event.Message.Model != "" {
			response.Model = event.Message.Model
		}
		startEvent := compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}
		if err := emit(startEvent); err != nil {
			return false, err
		}
		*started = true
		if event.Message.Usage.InputTokens > 0 {
			usage := compat.Usage{InputTokens: event.Message.Usage.InputTokens}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
				return false, err
			}
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
				return false, err
			}
		}
	case "content_block_delta":
		if !*started {
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
				return false, err
			}
			*started = true
		}
		if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
			part := compat.ContentPart{Type: compat.ContentPartText, Text: event.Delta.Text}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
		}
	case "message_delta":
		if event.Delta.StopReason != "" {
			response.StopReason = event.Delta.StopReason
		}
		if event.Usage.OutputTokens > 0 {
			usage := compat.Usage{OutputTokens: event.Usage.OutputTokens}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
				return false, err
			}
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
				return false, err
			}
		}
	case "message_stop":
		if response.Usage.TotalTokens == 0 {
			response.Usage.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens
		}
		if response.StopReason == "" {
			response.StopReason = "end_turn"
		}
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventDone, DoneReason: response.StopReason}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (p *Provider) invokeAnthropic(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.AnthropicMessagesRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = false
	var upstreamResponse compat.AnthropicMessagesResponse
	if err := p.doJSON(ctx, http.MethodPost, "/v1/messages", upstreamRequest, &upstreamResponse); err != nil {
		return compat.Response{}, err
	}
	if upstreamResponse.Model == "" {
		upstreamResponse.Model = request.Model
	}
	response, err := compat.AnthropicMessagesResponseToCanonical(upstreamResponse)
	if err != nil {
		return compat.Response{}, err
	}
	response.Dialect = request.Dialect
	return response, nil
}

func (p *Provider) invokeGemini(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.GeminiGenerateContentRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	path := "/v1beta/models/" + url.PathEscape(request.Model) + ":generateContent"
	var upstreamResponse compat.GeminiGenerateContentResponse
	if err := p.doJSON(ctx, http.MethodPost, path, upstreamRequest, &upstreamResponse); err != nil {
		return compat.Response{}, err
	}
	if upstreamResponse.ModelVersion == "" {
		upstreamResponse.ModelVersion = request.Model
	}
	response, err := compat.GeminiGenerateContentResponseToCanonical(upstreamResponse)
	if err != nil {
		return compat.Response{}, err
	}
	response.Dialect = request.Dialect
	return response, nil
}

func (p *Provider) invokeGeminiStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	upstreamRequest, err := compat.GeminiGenerateContentRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	path := "/v1beta/models/" + url.PathEscape(request.Model) + ":streamGenerateContent?alt=sse"
	resp, err := p.doRequest(ctx, http.MethodPost, path, upstreamRequest, "text/event-stream")
	if err != nil {
		return compat.Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return compat.Response{}, readErr
		}
		return compat.Response{}, upstreamHTTPError(resp, responseBody)
	}
	response := compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant},
	}
	started := false
	if err := processSSEPayloads(resp.Body, func(payload string) (bool, error) {
		return applyGeminiStreamPayload(&response, &started, payload, emit)
	}); err != nil {
		return compat.Response{}, err
	}
	if !started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return compat.Response{}, err
		}
	}
	if response.StopReason == "" {
		response.StopReason = "stop"
	}
	if err := response.Validate(); err != nil {
		return compat.Response{}, err
	}
	return response, nil
}

func applyGeminiStreamPayload(response *compat.Response, started *bool, payload string, emit func(compat.Event) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return payload == "[DONE]", nil
	}
	if message, code := upstreamErrorDetails([]byte(payload)); message != "" {
		errEvent := compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventError,
			Error:      &compat.EventErrorPayload{Message: message, Code: code},
		}
		if err := emit(errEvent); err != nil {
			return false, err
		}
		return false, &provider.UpstreamError{Code: code, Message: message}
	}
	var chunk compat.GeminiGenerateContentResponse
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, err
	}
	if chunk.ModelVersion != "" {
		response.Model = chunk.ModelVersion
	}
	if !*started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return false, err
		}
		*started = true
	}
	for _, candidate := range chunk.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text == "" {
				continue
			}
			content := compat.ContentPart{Type: compat.ContentPartText, Text: part.Text}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventContentDelta, ContentDelta: &content}); err != nil {
				return false, err
			}
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventContentDelta, ContentDelta: &content}); err != nil {
				return false, err
			}
		}
		if candidate.FinishReason != "" {
			response.StopReason = geminiStopToCanonical(candidate.FinishReason)
		}
	}
	if chunk.UsageMetadata != nil {
		usage := compat.Usage{
			InputTokens:  chunk.UsageMetadata.PromptTokenCount,
			OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  chunk.UsageMetadata.TotalTokenCount,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		delta := usageDifference(usage, response.Usage)
		response.Usage = usage
		if delta != (compat.Usage{}) {
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventUsageDelta, UsageDelta: &delta}); err != nil {
				return false, err
			}
		}
	}
	if response.StopReason != "" {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventDone, DoneReason: response.StopReason}); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (p *Provider) doJSON(ctx context.Context, method string, path string, body any, out any) error {
	resp, err := p.doRequest(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamHTTPError(resp, responseBody)
	}
	if len(responseBody) == 0 {
		return fmt.Errorf("%w: empty upstream response", ErrAPIProviderConfig)
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return err
	}
	return nil
}

func (p *Provider) doGETJSON(ctx context.Context, path string, out any) error {
	resp, err := p.doGET(ctx, path, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamHTTPError(resp, responseBody)
	}
	if len(responseBody) == 0 {
		return fmt.Errorf("%w: empty upstream response", ErrAPIProviderConfig)
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return err
	}
	return nil
}

func (p *Provider) doGET(ctx context.Context, path string, accept string) (*http.Response, error) {
	apiKey, err := p.apiKeyForRequest()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint(path, apiKey), nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("accept", accept)
	}
	if err := p.applyAPIKeyHeader(req, apiKey); err != nil {
		return nil, err
	}
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &provider.UpstreamError{Message: err.Error()}
	}
	return resp, nil
}

func (p *Provider) doRequest(ctx context.Context, method string, path string, body any, accept string) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	apiKey, err := p.apiKeyForRequest()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path, apiKey), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if accept != "" {
		req.Header.Set("accept", accept)
	}
	if err := p.applyAPIKeyHeader(req, apiKey); err != nil {
		return nil, err
	}
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &provider.UpstreamError{Message: err.Error()}
	}
	return resp, nil
}

func upstreamHTTPError(resp *http.Response, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	message, code := upstreamErrorDetails(body)
	if message == "" {
		message = bodyText
	}
	if message == "" && resp != nil {
		message = http.StatusText(resp.StatusCode)
	}
	statusCode := 0
	retryAfter := ""
	if resp != nil {
		statusCode = resp.StatusCode
		retryAfter = resp.Header.Get("retry-after")
	}
	statusCode, code = normalizeUpstreamStatus(statusCode, code, message)
	return &provider.UpstreamError{
		StatusCode: statusCode,
		Code:       code,
		Message:    message,
		Body:       bodyText,
		RetryAfter: retryAfter,
	}
}

func upstreamErrorDetails(body []byte) (string, string) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	if errPayload, ok := payload["error"]; ok {
		switch errValue := errPayload.(type) {
		case string:
			return strings.TrimSpace(errValue), firstString(payload, "code", "type", "status")
		case map[string]any:
			message := firstString(errValue, "message", "error", "detail")
			code := firstString(errValue, "code", "type", "status")
			return message, code
		}
	}
	message := firstString(payload, "message", "error", "detail")
	code := firstString(payload, "code", "type", "status")
	return message, code
}

func normalizeUpstreamStatus(statusCode int, code string, message string) (int, string) {
	combined := strings.ToUpper(strings.TrimSpace(code + " " + message))
	switch {
	case statusCode >= http.StatusInternalServerError && strings.Contains(combined, "RESOURCE_EXHAUSTED"):
		if strings.TrimSpace(code) == "" || strings.EqualFold(code, "unknown") {
			code = "rate_limit_exceeded"
		}
		return http.StatusTooManyRequests, code
	case statusCode >= http.StatusInternalServerError && (strings.Contains(combined, "NOT_FOUND") || strings.Contains(combined, "CODE 404")):
		if strings.TrimSpace(code) == "" || strings.EqualFold(code, "unknown") {
			code = "not_found"
		}
		return http.StatusNotFound, code
	default:
		return statusCode, code
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64, bool:
			return fmt.Sprint(v)
		default:
			text := strings.TrimSpace(fmt.Sprint(v))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func processSSEPayloads(body io.Reader, handle func(string) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if len(dataLines) > 0 {
				done, err := handle(strings.Join(dataLines, "\n"))
				if err != nil {
					return err
				}
				if done {
					return nil
				}
				dataLines = dataLines[:0]
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(dataLines) > 0 {
		_, err := handle(strings.Join(dataLines, "\n"))
		return err
	}
	return nil
}

func (p *Provider) apiKeyForRequest() (string, error) {
	if p.apiKeyFile == "" {
		return p.apiKey, nil
	}
	data, err := os.ReadFile(p.apiKeyFile)
	if err != nil {
		return "", fmt.Errorf("%w: read api key file: %v", ErrAPIProviderConfig, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (p *Provider) endpoint(path string, apiKey string) string {
	u := *p.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	pathOnly, rawQuery, hasQuery := strings.Cut(path, "?")
	if basePath == "" {
		u.Path = pathOnly
	} else {
		u.Path = basePath + pathOnly
	}
	if hasQuery {
		u.RawQuery = rawQuery
	}
	if apiKey != "" && p.apiKeyMode == "query" {
		q := u.Query()
		q.Set(p.apiKeyQueryParam, apiKey)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (p *Provider) applyAPIKeyHeader(req *http.Request, apiKey string) error {
	if apiKey == "" || p.apiKeyMode == "query" || p.apiKeyMode == "none" {
		return nil
	}
	switch p.apiKeyMode {
	case "bearer":
		req.Header.Set(p.apiKeyHeader, "Bearer "+apiKey)
	case "header":
		req.Header.Set(p.apiKeyHeader, apiKey)
	default:
		return fmt.Errorf("%w: unsupported api key mode %q", ErrAPIProviderConfig, p.apiKeyMode)
	}
	return nil
}

func normalizeAPIKeyAuth(mode string, header string, queryParam string) (string, string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	header = strings.TrimSpace(header)
	queryParam = strings.TrimSpace(queryParam)
	if mode == "" {
		mode = "bearer"
	}
	switch mode {
	case "bearer":
		if header == "" {
			header = "authorization"
		}
	case "header":
		if header == "" {
			return "", "", "", fmt.Errorf("%w: api key header is required for header mode", ErrAPIProviderConfig)
		}
	case "query":
		if queryParam == "" {
			return "", "", "", fmt.Errorf("%w: api key query param is required for query mode", ErrAPIProviderConfig)
		}
	case "none":
	default:
		return "", "", "", fmt.Errorf("%w: unsupported api key mode %q", ErrAPIProviderConfig, mode)
	}
	return mode, header, queryParam, nil
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func initialHealth(health provider.Health) provider.Health {
	if health.Status == "" {
		health.Status = provider.HealthReady
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = time.Now().UTC()
	}
	return health
}

func (p *Provider) recordInvocationResult(usage compat.Usage, invokeErr error) {
	p.recordUsage(usage, invokeErr)
	p.recordHealth(invokeErr)
	p.recordAuth(invokeErr)
}

func (p *Provider) recordUsage(usage compat.Usage, invokeErr error) {
	if p == nil || invokeErr != nil {
		return
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	if p.usage.Source == "" {
		p.usage.Source = "direct-http"
	}
	p.usage.Requests++
	p.usage.InputTokens += usage.InputTokens
	p.usage.OutputTokens += usage.OutputTokens
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	p.usage.TotalTokens += total
	p.usage.ObservedAt = time.Now().UTC()
}

func (p *Provider) recordHealth(invokeErr error) {
	if p == nil {
		return
	}
	now := time.Now().UTC()
	health := provider.Health{Status: provider.HealthReady, CheckedAt: now}
	if invokeErr != nil {
		if errors.Is(invokeErr, context.Canceled) || errors.Is(invokeErr, context.DeadlineExceeded) {
			return
		}
		var upstream *provider.UpstreamError
		if errors.As(invokeErr, &upstream) {
			switch upstream.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				health.Status = provider.HealthDown
				health.Reason = "upstream auth failed"
			case http.StatusTooManyRequests:
				health.Status = provider.HealthDegraded
				health.Reason = "upstream rate limited"
			case http.StatusBadRequest, http.StatusNotFound:
				return
			default:
				health.Status = provider.HealthDegraded
				health.Reason = "upstream request failed"
			}
		} else {
			health.Status = provider.HealthDegraded
			health.Reason = "provider invoke failed"
		}
	}
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	p.health = health
}

func (p *Provider) recordAuth(invokeErr error) {
	if p == nil {
		return
	}
	if invokeErr == nil {
		p.authMu.Lock()
		defer p.authMu.Unlock()
		auth := p.auth
		now := time.Now().UTC()
		if p.authExpiryIsAdvisory() && !auth.ExpiresAt.IsZero() && !now.Add(authExpiryRefreshThreshold).Before(auth.ExpiresAt) {
			auth.ExpiresAt = time.Time{}
		}
		if auth.ExpiresAt.IsZero() || now.Add(authExpiryRefreshThreshold).Before(auth.ExpiresAt) {
			auth.Status = provider.AuthHealthy
			auth.LastRefreshErr = ""
		} else {
			auth.Status = provider.AuthRefreshSoon
			if auth.LastRefreshErr == "" {
				auth.LastRefreshErr = "auth expiry is stale or inside refresh window"
			}
		}
		p.auth = auth
		return
	}
	if errors.Is(invokeErr, context.Canceled) || errors.Is(invokeErr, context.DeadlineExceeded) {
		return
	}
	var upstream *provider.UpstreamError
	if !errors.As(invokeErr, &upstream) {
		return
	}
	if upstream.StatusCode != http.StatusUnauthorized && upstream.StatusCode != http.StatusForbidden {
		return
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	auth := p.auth
	auth.Status = provider.AuthUnavailable
	auth.LastRefreshErr = upstream.Error()
	p.auth = auth
}

func (p *Provider) authExpiryIsAdvisory() bool {
	return p != nil && p.registration.Identity.Service == provider.ServiceAntigravity
}

func emitEventsFromResponse(response compat.Response, emit func(compat.Event) error) error {
	events, err := compat.EventsFromResponse(response)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func geminiStopToCanonical(stop string) string {
	switch strings.ToUpper(stop) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "safety"
	default:
		return "stop"
	}
}

func usageDifference(current compat.Usage, previous compat.Usage) compat.Usage {
	return compat.Usage{
		InputTokens:  nonNegativeDifference(current.InputTokens, previous.InputTokens),
		OutputTokens: nonNegativeDifference(current.OutputTokens, previous.OutputTokens),
		TotalTokens:  nonNegativeDifference(current.TotalTokens, previous.TotalTokens),
	}
}

func nonNegativeDifference(current int64, previous int64) int64 {
	if current <= previous {
		return 0
	}
	return current - previous
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}
