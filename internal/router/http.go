package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	"github.com/0xc0de1ab/pangaea/internal/security"
	"github.com/gin-gonic/gin"
)

type HTTPOptions struct {
	Engine     *Engine
	APIKeys    *security.APIKeyStore
	DataBroker *DataBroker
}

type openAIModelList struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type anthropicModelList struct {
	Data    []anthropicModel `json:"data"`
	HasMore bool             `json:"has_more"`
	FirstID string           `json:"first_id,omitempty"`
	LastID  string           `json:"last_id,omitempty"`
}

type anthropicModel struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type geminiModelList struct {
	Models []geminiModel `json:"models"`
}

type geminiModel struct {
	Name                       string   `json:"name"`
	Version                    string   `json:"version,omitempty"`
	DisplayName                string   `json:"displayName,omitempty"`
	Description                string   `json:"description,omitempty"`
	InputTokenLimit            int      `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit           int      `json:"outputTokenLimit,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

func NewHTTPHandler(opts HTTPOptions) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	if opts.APIKeys == nil {
		opts.APIKeys = security.NewAPIKeyStore(nil)
	}
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	r.GET("/router/ui", serveRouterDashboard)
	r.GET("/router/ui/", serveRouterDashboard)
	r.GET("/v1/models", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		if wantsAnthropicModels(c) {
			c.JSON(http.StatusOK, anthropicModelsFromEngine(engine))
			return
		}
		models := engine.Models()
		now := time.Now().Unix()
		out := openAIModelList{
			Object: "list",
			Data:   make([]openAIModel, 0, len(models)),
		}
		for _, model := range models {
			out.Data = append(out.Data, openAIModel{
				ID:      model.ID,
				Object:  "model",
				Created: now,
				OwnedBy: "pangaea",
			})
		}
		c.JSON(http.StatusOK, out)
	})
	r.GET("/v1beta/models", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, geminiModelsFromEngine(engine))
	})
	r.GET("/v1beta/models/*modelName", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		name := strings.TrimPrefix(c.Param("modelName"), "/")
		model, ok := geminiModelFromEngine(engine, name)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
			return
		}
		c.JSON(http.StatusOK, model)
	})
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var openaiRequest compat.OpenAIChatRequest
		if err := c.ShouldBindJSON(&openaiRequest); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		canonicalRequest, err := compat.OpenAIChatRequestToCanonical(openaiRequest)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		requestID := publicRequestID(c)
		routeRequest := applyPublicPrincipal(principal, RouteRequest{
			TenantID:   c.GetHeader("x-pangaea-tenant-id"),
			UserID:     c.GetHeader("x-pangaea-user-id"),
			APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
			Model:      openaiRequest.Model,
			APIDialect: compat.APIDialectOpenAI,
			Stream:     openaiRequest.Stream,
		})
		response, _, err := engine.Invoke(c.Request.Context(), RouteExecutionRequest{
			RequestID:     requestID,
			RouteRequest:  routeRequest,
			QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
			QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
		}, canonicalRequest)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if openaiRequest.Stream {
			writeOpenAIChatStream(c, response)
			return
		}
		openaiResponse, err := compat.OpenAIChatResponseFromCanonical(response)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, openaiResponse)
	})
	r.POST("/v1/messages", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var anthropicRequest compat.AnthropicMessagesRequest
		if err := c.ShouldBindJSON(&anthropicRequest); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		canonicalRequest, err := compat.AnthropicMessagesRequestToCanonical(anthropicRequest)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		requestID := publicRequestID(c)
		routeRequest := applyPublicPrincipal(principal, RouteRequest{
			TenantID:   c.GetHeader("x-pangaea-tenant-id"),
			UserID:     c.GetHeader("x-pangaea-user-id"),
			APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
			Model:      anthropicRequest.Model,
			APIDialect: compat.APIDialectAnthropic,
			Stream:     anthropicRequest.Stream,
		})
		response, _, err := engine.Invoke(c.Request.Context(), RouteExecutionRequest{
			RequestID:     requestID,
			RouteRequest:  routeRequest,
			QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
			QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
		}, canonicalRequest)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if anthropicRequest.Stream {
			writeAnthropicMessagesStream(c, response)
			return
		}
		anthropicResponse, err := compat.AnthropicMessagesResponseFromCanonical(response)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, anthropicResponse)
	})
	r.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
		handleGeminiGenerateContent(c, opts)
	})
	r.POST("/v1/models/*modelAction", func(c *gin.Context) {
		handleGeminiGenerateContent(c, opts)
	})
	r.GET("/router/v1/providers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"providers": engine.Providers()})
	})
	r.POST("/router/v1/providers/:provider_instance_id/auth/refresh", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		providerInstanceID := c.Param("provider_instance_id")
		var request authRefreshHTTPRequest
		if c.Request.Body != nil && c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&request); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		request.Reason = strings.TrimSpace(request.Reason)
		if !request.Confirm {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confirm must be true"})
			return
		}
		if request.Reason == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "reason is required"})
			return
		}
		timeout := 30 * time.Second
		if request.TimeoutSeconds < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "timeout_seconds must be non-negative"})
			return
		}
		if request.TimeoutSeconds > 0 {
			timeout = time.Duration(request.TimeoutSeconds) * time.Second
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		result, err := engine.RequestAuthRefresh(ctx, control.AuthRefreshRequest{
			RefreshID:          request.RefreshID,
			ProviderInstanceID: providerInstanceID,
			Reason:             request.Reason,
		})
		if err != nil {
			recordHTTPAuditEvent(engine, c, AuditEvent{
				Type:    AuditEventProviderAuthRefresh,
				Target:  auditProviderTarget(engine, providerInstanceID),
				Reason:  request.Reason,
				Outcome: AuditOutcomeFailed,
				Error:   err.Error(),
			})
			writeControlCommandError(c, err)
			return
		}
		outcome := AuditOutcomeSucceeded
		errorMessage := ""
		if !result.OK {
			outcome = AuditOutcomeFailed
			errorMessage = "auth refresh failed"
			if result.Error != nil && result.Error.Message != "" {
				errorMessage = result.Error.Message
			}
		}
		recordHTTPAuditEvent(engine, c, AuditEvent{
			Type:    AuditEventProviderAuthRefresh,
			Target:  auditProviderTarget(engine, providerInstanceID),
			Reason:  request.Reason,
			Outcome: outcome,
			Error:   errorMessage,
			Metadata: map[string]string{
				"refresh_id":  result.RefreshID,
				"auth_status": string(result.Auth.Status),
			},
		})
		c.JSON(http.StatusOK, result)
	})
	r.POST("/router/v1/providers/:provider_instance_id/drain", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		providerInstanceID := c.Param("provider_instance_id")
		var request providerDrainHTTPRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		request.Reason = strings.TrimSpace(request.Reason)
		if !request.Confirm {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confirm must be true"})
			return
		}
		if request.Reason == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "reason is required"})
			return
		}
		timeout := 5 * time.Second
		if request.TimeoutSeconds < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "timeout_seconds must be non-negative"})
			return
		}
		if request.TimeoutSeconds > 0 {
			timeout = time.Duration(request.TimeoutSeconds) * time.Second
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		command := control.ProviderDrain{
			ProviderInstanceID: providerInstanceID,
			Reason:             request.Reason,
			Drain:              request.Drain,
		}
		if err := engine.SendProviderDrain(ctx, command); err != nil {
			recordHTTPAuditEvent(engine, c, AuditEvent{
				Type:    providerDrainAuditType(request.Drain),
				Target:  auditProviderTarget(engine, providerInstanceID),
				Reason:  request.Reason,
				Outcome: AuditOutcomeFailed,
				Error:   err.Error(),
			})
			writeControlCommandError(c, err)
			return
		}
		recordHTTPAuditEvent(engine, c, AuditEvent{
			Type:    providerDrainAuditType(request.Drain),
			Target:  auditProviderTarget(engine, providerInstanceID),
			Reason:  request.Reason,
			Outcome: AuditOutcomeSucceeded,
		})
		c.JSON(http.StatusAccepted, command)
	})
	r.GET("/router/v1/nodes", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"nodes": engine.Nodes()})
	})
	r.GET("/router/v1/containers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"containers": engine.Containers()})
	})
	r.GET("/router/v1/control/sessions", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"sessions": engine.ControlSessions()})
	})
	r.GET("/router/v1/data/sessions", func(c *gin.Context) {
		if opts.DataBroker == nil {
			c.JSON(http.StatusOK, gin.H{"sessions": []DataSessionSnapshot{}})
			return
		}
		sessions := opts.DataBroker.Sessions()
		if opts.Engine != nil {
			sessions = opts.Engine.EnrichDataSessions(sessions)
		}
		c.JSON(http.StatusOK, gin.H{"sessions": sessions})
	})
	r.GET("/router/v1/audit/events", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		limit := 0
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a non-negative integer"})
				return
			}
			limit = parsed
		}
		c.JSON(http.StatusOK, gin.H{"events": engine.AuditEvents(limit)})
	})
	r.GET("/router/v1/usage/providers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"usage": engine.ProviderUsages()})
	})
	r.GET("/router/v1/traces", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		limit := 0
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a non-negative integer"})
				return
			}
			limit = parsed
		}
		c.JSON(http.StatusOK, gin.H{"traces": engine.RequestTraces(limit)})
	})
	r.GET("/router/v1/traces/:request_id", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		trace, found := engine.RequestTrace(c.Param("request_id"))
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "trace not found"})
			return
		}
		c.JSON(http.StatusOK, trace)
	})
	r.GET("/router/v1/quotas", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"quotas": engine.QuotaSnapshots()})
	})
	r.PUT("/router/v1/quotas/limits", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request quotaLimitRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := engine.SetQuotaLimit(request.Scope, request.Limit); err != nil {
			recordHTTPAuditEvent(engine, c, AuditEvent{
				Type:    AuditEventQuotaLimitSet,
				Target:  quotaAuditTarget(request.Scope),
				Outcome: AuditOutcomeFailed,
				Error:   err.Error(),
			})
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		snapshot, err := engine.QuotaSnapshot(request.Scope)
		if err != nil {
			recordHTTPAuditEvent(engine, c, AuditEvent{
				Type:    AuditEventQuotaLimitSet,
				Target:  quotaAuditTarget(request.Scope),
				Outcome: AuditOutcomeFailed,
				Error:   err.Error(),
			})
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		recordHTTPAuditEvent(engine, c, AuditEvent{
			Type:    AuditEventQuotaLimitSet,
			Target:  quotaAuditTarget(request.Scope),
			Outcome: AuditOutcomeSucceeded,
			Metadata: map[string]string{
				"max_tokens":   strconv.FormatInt(request.Limit.MaxTokens, 10),
				"max_requests": strconv.FormatInt(request.Limit.MaxRequests, 10),
			},
		})
		c.JSON(http.StatusOK, snapshot)
	})
	r.POST("/router/v1/quotas/snapshot", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var scope quota.Scope
		if err := c.ShouldBindJSON(&scope); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		snapshot, err := engine.QuotaSnapshot(scope)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, snapshot)
	})
	r.GET("/router/v1/api-keys", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"api_keys": opts.APIKeys.List()})
	})
	r.POST("/router/v1/api-keys", func(c *gin.Context) {
		var request apiKeyCreateRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if request.RawKey == "" {
			raw, principal, err := opts.APIKeys.CreateKeyWithOptions(security.APIKeyOptions{
				TenantID:  request.TenantID,
				UserID:    request.UserID,
				ExpiresAt: request.ExpiresAt,
				Disabled:  request.Disabled,
			})
			if err != nil {
				recordHTTPAuditEvent(opts.Engine, c, AuditEvent{
					Type:    AuditEventAPIKeyCreate,
					Target:  apiKeyAuditTarget(security.APIKeyPrincipal{TenantID: request.TenantID, UserID: request.UserID}),
					Outcome: AuditOutcomeFailed,
					Error:   err.Error(),
				})
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			recordHTTPAuditEvent(opts.Engine, c, AuditEvent{
				Type:     AuditEventAPIKeyCreate,
				Target:   apiKeyAuditTarget(principal),
				Outcome:  AuditOutcomeSucceeded,
				Metadata: apiKeyAuditMetadata(principal),
			})
			c.JSON(http.StatusCreated, apiKeyCreateResponse{APIKey: principal, RawKey: raw})
			return
		}
		if request.ID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id is required when raw_key is provided"})
			return
		}
		principal, err := opts.APIKeys.AddRawKeyWithOptions(security.APIKeyOptions{
			ID:        request.ID,
			Raw:       request.RawKey,
			TenantID:  request.TenantID,
			UserID:    request.UserID,
			ExpiresAt: request.ExpiresAt,
			Disabled:  request.Disabled,
		})
		if err != nil {
			recordHTTPAuditEvent(opts.Engine, c, AuditEvent{
				Type:    AuditEventAPIKeyCreate,
				Target:  apiKeyAuditTarget(security.APIKeyPrincipal{ID: request.ID, TenantID: request.TenantID, UserID: request.UserID}),
				Outcome: AuditOutcomeFailed,
				Error:   err.Error(),
			})
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		recordHTTPAuditEvent(opts.Engine, c, AuditEvent{
			Type:     AuditEventAPIKeyCreate,
			Target:   apiKeyAuditTarget(principal),
			Outcome:  AuditOutcomeSucceeded,
			Metadata: apiKeyAuditMetadata(principal),
		})
		c.JSON(http.StatusCreated, apiKeyCreateResponse{APIKey: principal})
	})
	r.DELETE("/router/v1/api-keys/:id", func(c *gin.Context) {
		id := c.Param("id")
		if !opts.APIKeys.Remove(id) {
			recordHTTPAuditEvent(opts.Engine, c, AuditEvent{
				Type:    AuditEventAPIKeyDelete,
				Target:  AuditTarget{APIKeyID: id},
				Outcome: AuditOutcomeFailed,
				Error:   "api key not found",
			})
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		recordHTTPAuditEvent(opts.Engine, c, AuditEvent{
			Type:    AuditEventAPIKeyDelete,
			Target:  AuditTarget{APIKeyID: id},
			Outcome: AuditOutcomeSucceeded,
		})
		c.Status(http.StatusNoContent)
	})
	r.GET("/router/v1/control/ws", handleControlWS(opts.Engine))
	r.GET("/router/v1/data/ws", func(c *gin.Context) {
		if opts.DataBroker == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrDataBrokerNotReady.Error()})
			return
		}
		opts.DataBroker.HandleDataWS(c)
	})
	r.POST("/router/v1/routes/dry-run", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request RouteRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		decision := engine.DryRun(request)
		status := http.StatusOK
		if !decision.Allowed {
			status = http.StatusConflict
		}
		c.JSON(status, decision)
	})
	return r
}

func serveRouterDashboard(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(routerDashboardHTML))
}

func wantsAnthropicModels(c *gin.Context) bool {
	return c.GetHeader("anthropic-version") != "" || strings.EqualFold(c.GetHeader("x-api-dialect"), string(compat.APIDialectAnthropic))
}

func anthropicModelsFromEngine(engine *Engine) anthropicModelList {
	models := engine.Models()
	out := anthropicModelList{
		Data:    make([]anthropicModel, 0, len(models)),
		HasMore: false,
	}
	for _, model := range models {
		if !hasCapability(model.Capabilities, provider.CapabilityAnthropicMessages) {
			continue
		}
		out.Data = append(out.Data, anthropicModel{
			ID:          model.ID,
			Type:        "model",
			DisplayName: model.ID,
		})
	}
	if len(out.Data) > 0 {
		out.FirstID = out.Data[0].ID
		out.LastID = out.Data[len(out.Data)-1].ID
	}
	return out
}

func geminiModelsFromEngine(engine *Engine) geminiModelList {
	models := engine.Models()
	out := geminiModelList{Models: make([]geminiModel, 0, len(models))}
	for _, model := range models {
		if !hasCapability(model.Capabilities, provider.CapabilityGeminiGenerateContent) {
			continue
		}
		out.Models = append(out.Models, geminiModelFromModelInfo(model))
	}
	return out
}

func geminiModelFromEngine(engine *Engine, name string) (geminiModel, bool) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "models/")
	if name == "" {
		return geminiModel{}, false
	}
	for _, model := range engine.Models() {
		if !hasCapability(model.Capabilities, provider.CapabilityGeminiGenerateContent) {
			continue
		}
		if model.ID == name || model.CanonicalModel == name {
			return geminiModelFromModelInfo(model), true
		}
	}
	return geminiModel{}, false
}

func geminiModelFromModelInfo(model ModelInfo) geminiModel {
	methods := []string{"generateContent"}
	if hasCapability(model.Capabilities, provider.CapabilityStreamSSE) {
		methods = append(methods, "streamGenerateContent")
	}
	return geminiModel{
		Name:                       "models/" + model.ID,
		Version:                    model.CanonicalModel,
		DisplayName:                model.ID,
		SupportedGenerationMethods: methods,
	}
}

type openAIChatStreamChunk struct {
	ID      string                   `json:"id,omitempty"`
	Object  string                   `json:"object"`
	Created int64                    `json:"created,omitempty"`
	Model   string                   `json:"model"`
	Choices []openAIChatStreamChoice `json:"choices"`
	Usage   *compat.OpenAIUsage      `json:"usage,omitempty"`
}

type quotaLimitRequest struct {
	Scope quota.Scope `json:"scope"`
	Limit quota.Limit `json:"limit"`
}

type apiKeyCreateRequest struct {
	ID        string    `json:"id,omitempty"`
	RawKey    string    `json:"raw_key,omitempty"`
	TenantID  string    `json:"tenant_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Disabled  bool      `json:"disabled,omitempty"`
}

type apiKeyCreateResponse struct {
	APIKey security.APIKeyPrincipal `json:"api_key"`
	RawKey string                   `json:"raw_key,omitempty"`
}

type authRefreshHTTPRequest struct {
	RefreshID      string `json:"refresh_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Confirm        bool   `json:"confirm,omitempty"`
}

type providerDrainHTTPRequest struct {
	Drain          bool   `json:"drain"`
	Reason         string `json:"reason,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Confirm        bool   `json:"confirm,omitempty"`
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

func writeOpenAIChatStream(c *gin.Context, response compat.Response) {
	openaiResponse, err := compat.OpenAIChatResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)
	created := time.Now().Unix()
	content := ""
	finishReason := ""
	if len(openaiResponse.Choices) > 0 {
		content = openaiResponse.Choices[0].Message.Content
		finishReason = openaiResponse.Choices[0].FinishReason
	}
	writeSSEData(c, openAIChatStreamChunk{
		ID:      openaiResponse.ID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   openaiResponse.Model,
		Choices: []openAIChatStreamChoice{{
			Index: 0,
			Delta: openAIChatStreamDelta{
				Role:    "assistant",
				Content: content,
			},
		}},
	})
	writeSSEData(c, openAIChatStreamChunk{
		ID:      openaiResponse.ID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   openaiResponse.Model,
		Choices: []openAIChatStreamChoice{{
			Index:        0,
			Delta:        openAIChatStreamDelta{},
			FinishReason: finishReason,
		}},
		Usage: openaiResponse.Usage,
	})
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	flushSSE(c)
}

func writeAnthropicMessagesStream(c *gin.Context, response compat.Response) {
	anthropicResponse, err := compat.AnthropicMessagesResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)

	startMessage := anthropicResponse
	startMessage.Content = []compat.AnthropicContentBlock{}
	startMessage.StopReason = ""
	startMessage.StopSequence = nil
	startMessage.Usage.OutputTokens = 0
	writeSSEEvent(c, "message_start", gin.H{
		"type":    "message_start",
		"message": startMessage,
	})
	for index, block := range anthropicResponse.Content {
		startBlock := block
		if startBlock.Type == "text" {
			startBlock.Text = ""
		}
		writeSSEEvent(c, "content_block_start", gin.H{
			"type":          "content_block_start",
			"index":         index,
			"content_block": startBlock,
		})
		if block.Type == "text" && block.Text != "" {
			writeSSEEvent(c, "content_block_delta", gin.H{
				"type":  "content_block_delta",
				"index": index,
				"delta": gin.H{
					"type": "text_delta",
					"text": block.Text,
				},
			})
		}
		writeSSEEvent(c, "content_block_stop", gin.H{
			"type":  "content_block_stop",
			"index": index,
		})
	}
	writeSSEEvent(c, "message_delta", gin.H{
		"type": "message_delta",
		"delta": gin.H{
			"stop_reason":   anthropicResponse.StopReason,
			"stop_sequence": anthropicResponse.StopSequence,
		},
		"usage": gin.H{
			"output_tokens": anthropicResponse.Usage.OutputTokens,
		},
	})
	writeSSEEvent(c, "message_stop", gin.H{"type": "message_stop"})
}

func writeGeminiGenerateContentStream(c *gin.Context, response compat.Response) {
	geminiResponse, err := compat.GeminiGenerateContentResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)
	writeSSEData(c, geminiResponse)
}

func writeControlCommandError(c *gin.Context, err error) {
	status := http.StatusConflict
	switch {
	case errors.Is(err, provider.ErrProviderNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrProviderControlSessionNotFound):
		status = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status = http.StatusGatewayTimeout
	case errors.Is(err, control.ErrInvalidPayload):
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func writeSSEEvent(c *gin.Context, event string, payload any) {
	_, _ = c.Writer.Write([]byte("event: "))
	_, _ = c.Writer.Write([]byte(event))
	_, _ = c.Writer.Write([]byte("\n"))
	writeSSEData(c, payload)
}

func writeSSEData(c *gin.Context, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = c.Writer.Write([]byte("data: "))
	_, _ = c.Writer.Write(data)
	_, _ = c.Writer.Write([]byte("\n\n"))
	flushSSE(c)
}

func flushSSE(c *gin.Context) {
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

func handleGeminiGenerateContent(c *gin.Context, opts HTTPOptions) {
	principal, ok := authenticatePublicRequest(c, opts.APIKeys)
	if !ok {
		return
	}
	engine, ok := requireEngine(c, opts.Engine)
	if !ok {
		return
	}
	model, stream, ok := geminiModelFromAction(c.Param("modelAction"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unsupported Gemini model action"})
		return
	}
	if c.Query("alt") == "sse" {
		stream = true
	}
	var geminiRequest compat.GeminiGenerateContentRequest
	if err := c.ShouldBindJSON(&geminiRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	canonicalRequest, err := compat.GeminiGenerateContentRequestToCanonical(geminiRequest, model)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	canonicalRequest.Stream = stream
	requestID := publicRequestID(c)
	routeRequest := applyPublicPrincipal(principal, RouteRequest{
		TenantID:   c.GetHeader("x-pangaea-tenant-id"),
		UserID:     c.GetHeader("x-pangaea-user-id"),
		APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
		Model:      model,
		APIDialect: compat.APIDialectGemini,
		Stream:     stream,
	})
	response, _, err := engine.Invoke(c.Request.Context(), RouteExecutionRequest{
		RequestID:     requestID,
		RouteRequest:  routeRequest,
		QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
		QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
	}, canonicalRequest)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if stream {
		writeGeminiGenerateContentStream(c, response)
		return
	}
	geminiResponse, err := compat.GeminiGenerateContentResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, geminiResponse)
}

func requireEngine(c *gin.Context, engine *Engine) (*Engine, bool) {
	if engine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrRouterNotReady.Error()})
		return nil, false
	}
	return engine, true
}

func publicRequestID(c *gin.Context) string {
	requestID := c.GetHeader("x-request-id")
	if requestID == "" {
		requestID = "req_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return requestID
}

func applyPublicPrincipal(principal security.APIKeyPrincipal, routeRequest RouteRequest) RouteRequest {
	if principal.ID == "" {
		return routeRequest
	}
	routeRequest.TenantID = principal.TenantID
	routeRequest.UserID = principal.UserID
	routeRequest.APIKeyID = principal.ID
	return routeRequest
}

func recordHTTPAuditEvent(engine *Engine, c *gin.Context, event AuditEvent) AuditEvent {
	if engine == nil {
		return AuditEvent{}
	}
	event.Actor = httpAuditActor(c)
	return engine.RecordAuditEvent(event)
}

func httpAuditActor(c *gin.Context) AuditActor {
	actor := AuditActor{
		TenantID:   c.GetHeader("x-pangaea-tenant-id"),
		UserID:     c.GetHeader("x-pangaea-user-id"),
		APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
		Source:     c.GetHeader("x-pangaea-actor-source"),
		RemoteAddr: c.ClientIP(),
		RequestID:  c.GetHeader("x-request-id"),
	}
	if actor.Source == "" {
		actor.Source = "admin-api"
	}
	return actor
}

func auditProviderTarget(engine *Engine, providerInstanceID string) AuditTarget {
	if engine != nil && engine.registry != nil {
		if registration, ok := engine.registry.Get(providerInstanceID); ok {
			return providerAuditTarget(registration)
		}
	}
	return AuditTarget{ProviderInstanceID: providerInstanceID}
}

func providerDrainAuditType(drain bool) AuditEventType {
	if drain {
		return AuditEventProviderDrain
	}
	return AuditEventProviderDrainRelease
}

func quotaAuditTarget(scope quota.Scope) AuditTarget {
	return AuditTarget{
		TenantID: scope.TenantID,
		UserID:   scope.UserID,
		APIKeyID: scope.APIKeyID,
		Model:    scope.Model,
	}
}

func apiKeyAuditTarget(principal security.APIKeyPrincipal) AuditTarget {
	return AuditTarget{
		APIKeyID: principal.ID,
		TenantID: principal.TenantID,
		UserID:   principal.UserID,
	}
}

func apiKeyAuditMetadata(principal security.APIKeyPrincipal) map[string]string {
	metadata := map[string]string{}
	if !principal.ExpiresAt.IsZero() {
		metadata["expires_at"] = principal.ExpiresAt.Format(time.RFC3339)
	}
	if principal.Disabled {
		metadata["disabled"] = "true"
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func authenticatePublicRequest(c *gin.Context, store *security.APIKeyStore) (security.APIKeyPrincipal, bool) {
	if store == nil || store.Len() == 0 {
		return security.APIKeyPrincipal{}, true
	}
	raw := bearerToken(c.GetHeader("authorization"))
	if raw == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return security.APIKeyPrincipal{}, false
	}
	principal, ok := store.Authenticate(raw)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid bearer token"})
		return security.APIKeyPrincipal{}, false
	}
	return principal, true
}

func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) < len(prefix) || strings.ToLower(header[:len(prefix)]) != prefix {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func geminiModelFromAction(action string) (string, bool, bool) {
	action = strings.TrimPrefix(action, "/")
	model, suffix, ok := strings.Cut(action, ":")
	if !ok || strings.TrimSpace(model) == "" {
		return "", false, false
	}
	switch suffix {
	case "generateContent":
		return model, false, true
	case "streamGenerateContent":
		return model, true, true
	default:
		return "", false, false
	}
}
