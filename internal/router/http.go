package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	PeerToken  string
	AdminAuth  AdminAuthOptions
}

const routeSelectedModelPlaceholder = "__pangaea_route_selected_model__"

type RouterHTTPPrincipal struct {
	Email     string
	Name      string
	Role      RouterUserRole
	APIKey    security.APIKeyPrincipal
	Session   GoogleOAuthSession
	AuthKind  string
	Anonymous bool
}

const (
	traceRequestIDContextKey = "pangaea_trace_request_id"
	maxTraceHTTPBodyBytes    = 256 << 10
)

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
	opts.AdminAuth = normalizeAdminAuthOptions(opts.AdminAuth, opts.APIKeys.Len() > 0)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	r.GET("/router", redirectToEmbeddedRouterDashboard)
	r.GET("/router/", redirectToEmbeddedRouterDashboard)
	routerDashboard := serveEmbeddedRouterDashboardWithAuth(opts.AdminAuth, opts.Engine)
	r.GET("/router/ui", routerDashboard)
	r.GET("/router/ui/*path", routerDashboard)
	registerGoogleOAuthRoutes(r, opts.AdminAuth, opts.Engine)
	registerRouterUserRoutes(r, opts)
	registerNamedRouteCompatRoutes(r, opts)
	r.Use(routerAdminAuthMiddleware(opts.APIKeys, opts.AdminAuth, opts.Engine))
	r.Use(traceHTTPExchangeMiddleware(opts.Engine))
	r.GET("/v1/models", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		handleOpenAIOrAnthropicModels(c, opts)
	})
	r.GET("/router/v1/compat/v1/models", func(c *gin.Context) {
		handleOpenAIOrAnthropicModels(c, opts)
	})
	r.GET("/v1beta/models", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		handleGeminiModelsList(c, opts)
	})
	r.GET("/router/v1/compat/v1beta/models", func(c *gin.Context) {
		handleGeminiModelsList(c, opts)
	})
	r.GET("/v1beta/models/*modelName", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		handleGeminiModelGet(c, opts)
	})
	r.GET("/router/v1/compat/v1beta/models/*modelName", func(c *gin.Context) {
		handleGeminiModelGet(c, opts)
	})
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		handleOpenAIChatCompletions(c, opts, func(openaiRequest compat.OpenAIChatRequest) RouteRequest {
			routeRequest := applyPublicPrincipal(principal, RouteRequest{
				TenantID:   c.GetHeader("x-pangaea-tenant-id"),
				UserID:     c.GetHeader("x-pangaea-user-id"),
				APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
				Model:      openaiRequest.Model,
				APIDialect: compat.APIDialectOpenAI,
				Stream:     openaiRequest.Stream,
			})
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	r.POST("/v1/responses", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		handleOpenAIResponses(c, opts, func(openaiRequest compat.OpenAIResponsesRequest) RouteRequest {
			routeRequest := applyPublicPrincipal(principal, RouteRequest{
				TenantID:   c.GetHeader("x-pangaea-tenant-id"),
				UserID:     c.GetHeader("x-pangaea-user-id"),
				APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
				Model:      openaiRequest.Model,
				APIDialect: compat.APIDialectOpenAI,
				Stream:     openaiRequest.Stream,
			})
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	r.POST("/router/v1/compat/v1/chat/completions", func(c *gin.Context) {
		handleOpenAIChatCompletions(c, opts, func(openaiRequest compat.OpenAIChatRequest) RouteRequest {
			return dashboardCompatRouteRequest(c, openaiRequest.Model, compat.APIDialectOpenAI, openaiRequest.Stream)
		})
	})
	r.POST("/router/v1/compat/v1/responses", func(c *gin.Context) {
		handleOpenAIResponses(c, opts, func(openaiRequest compat.OpenAIResponsesRequest) RouteRequest {
			return dashboardCompatRouteRequest(c, openaiRequest.Model, compat.APIDialectOpenAI, openaiRequest.Stream)
		})
	})
	r.POST("/v1/messages", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		handleAnthropicMessages(c, opts, func(anthropicRequest compat.AnthropicMessagesRequest) RouteRequest {
			routeRequest := applyPublicPrincipal(principal, RouteRequest{
				TenantID:   c.GetHeader("x-pangaea-tenant-id"),
				UserID:     c.GetHeader("x-pangaea-user-id"),
				APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
				Model:      anthropicRequest.Model,
				APIDialect: compat.APIDialectAnthropic,
				Stream:     anthropicRequest.Stream,
			})
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	r.POST("/router/v1/compat/v1/messages", func(c *gin.Context) {
		handleAnthropicMessages(c, opts, func(anthropicRequest compat.AnthropicMessagesRequest) RouteRequest {
			return dashboardCompatRouteRequest(c, anthropicRequest.Model, compat.APIDialectAnthropic, anthropicRequest.Stream)
		})
	})
	r.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		handleGeminiGenerateContent(c, opts, func(model string, stream bool) RouteRequest {
			routeRequest := applyPublicPrincipal(principal, RouteRequest{
				TenantID:   c.GetHeader("x-pangaea-tenant-id"),
				UserID:     c.GetHeader("x-pangaea-user-id"),
				APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
				Model:      model,
				APIDialect: compat.APIDialectGemini,
				Stream:     stream,
			})
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	r.POST("/v1/models/*modelAction", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok {
			return
		}
		handleGeminiGenerateContent(c, opts, func(model string, stream bool) RouteRequest {
			routeRequest := applyPublicPrincipal(principal, RouteRequest{
				TenantID:   c.GetHeader("x-pangaea-tenant-id"),
				UserID:     c.GetHeader("x-pangaea-user-id"),
				APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
				Model:      model,
				APIDialect: compat.APIDialectGemini,
				Stream:     stream,
			})
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	r.POST("/router/v1/compat/v1beta/models/*modelAction", func(c *gin.Context) {
		handleGeminiGenerateContent(c, opts, func(model string, stream bool) RouteRequest {
			return dashboardCompatRouteRequest(c, model, compat.APIDialectGemini, stream)
		})
	})
	r.POST("/router/v1/compat/v1/models/*modelAction", func(c *gin.Context) {
		handleGeminiGenerateContent(c, opts, func(model string, stream bool) RouteRequest {
			return dashboardCompatRouteRequest(c, model, compat.APIDialectGemini, stream)
		})
	})
	r.GET("/router/v1/providers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"providers": engine.Providers()})
	})
	r.DELETE("/router/v1/providers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request providerDeleteHTTPRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		providerInstanceIDs := uniqueNonEmptyStrings(request.ProviderInstanceIDs)
		request.Reason = strings.TrimSpace(request.Reason)
		if !request.Confirm {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confirm must be true"})
			return
		}
		if request.Reason == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "reason is required"})
			return
		}
		if len(providerInstanceIDs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider_instance_ids is required"})
			return
		}
		for _, providerInstanceID := range providerInstanceIDs {
			if _, ok := engine.registry.Get(providerInstanceID); !ok {
				recordHTTPAuditEvent(engine, c, AuditEvent{
					Type:    AuditEventProviderDelete,
					Target:  AuditTarget{ProviderInstanceID: providerInstanceID},
					Reason:  request.Reason,
					Outcome: AuditOutcomeFailed,
					Error:   provider.ErrProviderNotFound.Error(),
				})
				c.JSON(http.StatusNotFound, gin.H{"error": "provider not found", "provider_instance_id": providerInstanceID})
				return
			}
			if engine.ProviderControlConnected(providerInstanceID) {
				recordHTTPAuditEvent(engine, c, AuditEvent{
					Type:    AuditEventProviderDelete,
					Target:  auditProviderTarget(engine, providerInstanceID),
					Reason:  request.Reason,
					Outcome: AuditOutcomeFailed,
					Error:   "provider control session is still connected",
				})
				c.JSON(http.StatusConflict, gin.H{"error": "provider control session is still connected", "provider_instance_id": providerInstanceID})
				return
			}
			if opts.DataBroker != nil && opts.DataBroker.ProviderAvailable(providerInstanceID) {
				recordHTTPAuditEvent(engine, c, AuditEvent{
					Type:    AuditEventProviderDelete,
					Target:  auditProviderTarget(engine, providerInstanceID),
					Reason:  request.Reason,
					Outcome: AuditOutcomeFailed,
					Error:   "provider data session is still connected",
				})
				c.JSON(http.StatusConflict, gin.H{"error": "provider data session is still connected", "provider_instance_id": providerInstanceID})
				return
			}
		}
		results := make([]ProviderDeleteResult, 0, len(providerInstanceIDs))
		authRecordsRemoved := 0
		authReplicasRemoved := 0
		containersRemoved := 0
		usageRemoved := 0
		for _, providerInstanceID := range providerInstanceIDs {
			target := auditProviderTarget(engine, providerInstanceID)
			result, err := engine.DeleteProvider(providerInstanceID)
			if err != nil {
				recordHTTPAuditEvent(engine, c, AuditEvent{
					Type:    AuditEventProviderDelete,
					Target:  target,
					Reason:  request.Reason,
					Outcome: AuditOutcomeFailed,
					Error:   err.Error(),
				})
				writeControlCommandError(c, err)
				return
			}
			results = append(results, result)
			authRecordsRemoved += result.AuthRecordsRemoved
			authReplicasRemoved += result.AuthReplicasRemoved
			containersRemoved += result.ContainersRemoved
			if result.UsageRemoved {
				usageRemoved++
			}
		}
		recordHTTPAuditEvent(engine, c, AuditEvent{
			Type:    AuditEventProviderDelete,
			Target:  AuditTarget{ProviderInstanceID: firstNonEmpty(providerInstanceIDs...)},
			Reason:  request.Reason,
			Outcome: AuditOutcomeSucceeded,
			Metadata: map[string]string{
				"requested":             strconv.Itoa(len(providerInstanceIDs)),
				"deleted":               strconv.Itoa(len(results)),
				"auth_records_removed":  strconv.Itoa(authRecordsRemoved),
				"auth_replicas_removed": strconv.Itoa(authReplicasRemoved),
				"usage_removed":         strconv.Itoa(usageRemoved),
				"containers_removed":    strconv.Itoa(containersRemoved),
			},
		})
		c.JSON(http.StatusOK, gin.H{"deleted": len(results), "results": results})
	})
	r.GET("/router/v1/auth", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"auth": engine.AuthRecords()})
	})
	r.GET("/router/v1/auth/:auth_id/events", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"events": engine.AuthEvents(c.Param("auth_id"))})
	})
	r.GET("/router/v1/auth/:auth_id/download", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		authID := c.Param("auth_id")
		raw, filename, found := engine.AuthDownload(authID)
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "auth file is not available for download"})
			return
		}
		engine.RecordAuthDownload(authID)
		c.Header("Content-Disposition", `attachment; filename="`+downloadFilename(filename)+`"`)
		c.Data(http.StatusOK, "application/octet-stream", raw)
	})
	r.GET("/router/v1/dashboard/summary", func(c *gin.Context) {
		c.JSON(http.StatusOK, BuildDashboardSummary(opts.Engine, opts.DataBroker))
	})
	r.GET("/router/v1/dashboard/overview", func(c *gin.Context) {
		c.JSON(http.StatusOK, BuildDashboardOverview(opts.Engine, opts.DataBroker))
	})
	r.GET("/router/v1/dashboard/routes", func(c *gin.Context) {
		c.JSON(http.StatusOK, BuildDashboardRoutes(opts.Engine, opts.DataBroker))
	})
	r.GET("/router/v1/dashboard/providers", func(c *gin.Context) {
		c.JSON(http.StatusOK, BuildDashboardProviders(opts.Engine, opts.DataBroker))
	})
	r.GET("/router/v1/dashboard/traces", func(c *gin.Context) {
		limit := 0
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a non-negative integer"})
				return
			}
			limit = parsed
		}
		c.JSON(http.StatusOK, BuildDashboardTraces(opts.Engine, limit))
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
	r.GET("/router/v1/notifiers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"notifiers": engine.NotifierStatuses()})
	})
	r.GET("/router/v1/notifiers/history", func(c *gin.Context) {
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
		c.JSON(http.StatusOK, gin.H{"history": engine.NotifierHistory(limit)})
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
		offset := 0
		if raw := c.Query("offset"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be a non-negative integer"})
				return
			}
			offset = parsed
		}
		c.JSON(http.StatusOK, engine.RequestTracesPage(offset, limit))
	})
	r.DELETE("/router/v1/traces", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request requestTraceDeleteRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		requestIDs := uniqueNonEmptyStrings(request.RequestIDs)
		if len(requestIDs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request_ids is required"})
			return
		}
		deleted := engine.DeleteRequestTraces(requestIDs)
		recordHTTPAuditEvent(engine, c, AuditEvent{
			Type:    AuditEventRequestTraceDelete,
			Target:  AuditTarget{RequestID: firstNonEmpty(requestIDs...)},
			Outcome: AuditOutcomeSucceeded,
			Metadata: map[string]string{
				"requested": strconv.Itoa(len(requestIDs)),
				"deleted":   strconv.Itoa(deleted),
			},
		})
		c.JSON(http.StatusOK, gin.H{"deleted": deleted})
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
	r.DELETE("/router/v1/traces/:request_id", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		requestID := strings.TrimSpace(c.Param("request_id"))
		if requestID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request id is required"})
			return
		}
		deleted := engine.DeleteRequestTraces([]string{requestID})
		if deleted == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "trace not found"})
			return
		}
		recordHTTPAuditEvent(engine, c, AuditEvent{
			Type:     AuditEventRequestTraceDelete,
			Target:   AuditTarget{RequestID: requestID},
			Outcome:  AuditOutcomeSucceeded,
			Metadata: map[string]string{"deleted": strconv.Itoa(deleted)},
		})
		c.Status(http.StatusNoContent)
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
	r.GET("/router/v1/control/ws", handleControlWS(opts.Engine, opts.PeerToken))
	r.GET("/router/v1/data/ws", func(c *gin.Context) {
		if !authenticateRouterPeerRequest(c, opts.PeerToken) {
			return
		}
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

func registerRouterUserRoutes(r *gin.Engine, opts HTTPOptions) {
	r.GET("/router/v1/users/me", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": routerUserFromPrincipal(principal)})
	})
	r.GET("/router/v1/users", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		if !principal.Role.IsAdmin() {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin role is required"})
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": engine.ListUsers()})
	})
	r.POST("/router/v1/users", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		if !principal.Role.IsAdmin() {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin role is required"})
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request RouterUserUpsertRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		user, err := engine.UpsertUser(request)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, user)
	})
	r.PUT("/router/v1/users/:email", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		if !principal.Role.IsAdmin() {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin role is required"})
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request RouterUserUpsertRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		request.Email = c.Param("email")
		user, err := engine.UpsertUser(request)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, user)
	})
	r.DELETE("/router/v1/users/:email", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		if !principal.Role.IsAdmin() {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin role is required"})
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		if !engine.DeleteUser(c.Param("email")) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.Status(http.StatusNoContent)
	})

	r.GET("/router/v1/routing-rules", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		rules := attachRoutingRuleStats(routingRulesVisibleTo(engine.ListRoutingRules(), principal), engine.RoutingRuleStats())
		c.JSON(http.StatusOK, gin.H{"rules": rules})
	})
	r.POST("/router/v1/routing-rules", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var rule RoutingRule
		if err := c.ShouldBindJSON(&rule); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		rule, ok = normalizeRuleForPrincipal(c, rule, principal)
		if !ok {
			return
		}
		created, err := engine.UpsertRoutingRule(rule)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, created)
	})
	r.PUT("/router/v1/routing-rules/:id", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		existing, found := engine.GetRoutingRule(c.Param("id"))
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "routing rule not found"})
			return
		}
		if !principalCanEditRoutingRule(principal, existing) {
			c.JSON(http.StatusForbidden, gin.H{"error": "routing rule is not editable by this user"})
			return
		}
		var rule RoutingRule
		if err := c.ShouldBindJSON(&rule); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if strings.TrimSpace(rule.Name) == "" {
			rule.Name = existing.Name
		}
		rule.CreatedAt = existing.CreatedAt
		rule, ok = normalizeRuleForPrincipal(c, rule, principal)
		if !ok {
			return
		}
		if rule.ID != existing.ID {
			engine.DeleteRoutingRule(existing.ID)
		}
		updated, err := engine.UpsertRoutingRule(rule)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, updated)
	})
	r.DELETE("/router/v1/routing-rules/:id", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		rule, found := engine.GetRoutingRule(c.Param("id"))
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "routing rule not found"})
			return
		}
		if !principalCanEditRoutingRule(principal, rule) {
			c.JSON(http.StatusForbidden, gin.H{"error": "routing rule is not editable by this user"})
			return
		}
		engine.DeleteRoutingRule(rule.ID)
		c.Status(http.StatusNoContent)
	})
	r.POST("/router/v1/routing-rules/dry-run", func(c *gin.Context) {
		principal, ok := authenticateRouterUserRequest(c, opts.APIKeys, opts.AdminAuth, opts.Engine)
		if !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		var request RoutingRuleDryRunRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if request.Rule != nil {
			rule, ok := normalizeRuleForPrincipal(c, *request.Rule, principal)
			if !ok {
				return
			}
			request.Rule = &rule
		} else if request.RuleID != "" {
			rule, found := engine.GetRoutingRule(request.RuleID)
			if !found {
				c.JSON(http.StatusNotFound, gin.H{"error": "routing rule not found"})
				return
			}
			if !principalCanReadRoutingRule(principal, rule) {
				c.JSON(http.StatusForbidden, gin.H{"error": "routing rule is not visible to this user"})
				return
			}
		} else if request.Name != "" {
			request.OwnerEmail = firstNonEmpty(request.OwnerEmail, principal.Email)
		}
		response := engine.DryRunRoutingRule(request)
		status := http.StatusOK
		if !response.Decision.Allowed {
			status = http.StatusConflict
		}
		c.JSON(status, response)
	})
}

func registerNamedRouteCompatRoutes(r *gin.Engine, opts HTTPOptions) {
	group := r.Group("/route/:user/:rule")
	group.Use(traceHTTPExchangeMiddleware(opts.Engine))
	group.GET("/v1/models", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleOpenAIOrAnthropicModels(c, opts)
	})
	group.GET("/v1beta/models", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleGeminiModelsList(c, opts)
	})
	group.GET("/v1beta/models/*modelName", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleGeminiModelGet(c, opts)
	})
	group.POST("/v1/chat/completions", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleOpenAIChatCompletions(c, opts, func(openaiRequest compat.OpenAIChatRequest) RouteRequest {
			routeRequest := namedRouteRequest(c, principal, openaiRequest.Model, compat.APIDialectOpenAI, openaiRequest.Stream)
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	group.POST("/v1/responses", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleOpenAIResponses(c, opts, func(openaiRequest compat.OpenAIResponsesRequest) RouteRequest {
			routeRequest := namedRouteRequest(c, principal, openaiRequest.Model, compat.APIDialectOpenAI, openaiRequest.Stream)
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	group.POST("/v1/messages", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleAnthropicMessages(c, opts, func(anthropicRequest compat.AnthropicMessagesRequest) RouteRequest {
			routeRequest := namedRouteRequest(c, principal, anthropicRequest.Model, compat.APIDialectAnthropic, anthropicRequest.Stream)
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	group.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleGeminiGenerateContent(c, opts, func(model string, stream bool) RouteRequest {
			routeRequest := namedRouteRequest(c, principal, model, compat.APIDialectGemini, stream)
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
	group.POST("/v1/models/*modelAction", func(c *gin.Context) {
		principal, ok := authenticatePublicRequest(c, opts.APIKeys)
		if !ok || !authorizeNamedRoutePrincipal(c, principal) {
			return
		}
		handleGeminiGenerateContent(c, opts, func(model string, stream bool) RouteRequest {
			routeRequest := namedRouteRequest(c, principal, model, compat.APIDialectGemini, stream)
			applyRouteRequestProviderHeaders(c, &routeRequest)
			return routeRequest
		})
	})
}

func authorizeNamedRoutePrincipal(c *gin.Context, principal security.APIKeyPrincipal) bool {
	if principal.ID == "" {
		return true
	}
	routeUser := normalizeUserEmail(c.Param("user"))
	if routeUser == "" || routeUser == "public" || strings.EqualFold(routeUser, principal.UserID) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "api key user does not match route user"})
	return false
}

func namedRouteRequest(c *gin.Context, principal security.APIKeyPrincipal, model string, dialect compat.APIDialect, stream bool) RouteRequest {
	routeUser := normalizeUserEmail(c.Param("user"))
	routeRule := strings.TrimSpace(c.Param("rule"))
	routeRequest := applyPublicPrincipal(principal, RouteRequest{
		TenantID:         c.GetHeader("x-pangaea-tenant-id"),
		UserID:           firstNonEmpty(routeUser, c.GetHeader("x-pangaea-user-id")),
		APIKeyID:         c.GetHeader("x-pangaea-api-key-id"),
		RoutingRuleName:  routeRule,
		RoutingRuleOwner: routeUser,
		Model:            model,
		APIDialect:       dialect,
		Stream:           stream,
	})
	return routeRequest
}

func routerUserFromPrincipal(principal RouterHTTPPrincipal) RouterUser {
	return RouterUser{
		ID:      principal.Email,
		Email:   principal.Email,
		Name:    principal.Name,
		Role:    principal.Role,
		Enabled: true,
	}
}

func routingRulesVisibleTo(rules []RoutingRule, principal RouterHTTPPrincipal) []RoutingRule {
	out := make([]RoutingRule, 0, len(rules))
	for _, rule := range rules {
		if principalCanReadRoutingRule(principal, rule) {
			out = append(out, rule)
		}
	}
	return out
}

func attachRoutingRuleStats(rules []RoutingRule, stats map[string]RoutingRuleStats) []RoutingRule {
	if len(rules) == 0 || len(stats) == 0 {
		return rules
	}
	out := make([]RoutingRule, len(rules))
	copy(out, rules)
	for index := range out {
		if stat, ok := stats[out[index].ID]; ok {
			next := stat
			out[index].Stats = &next
		}
	}
	return out
}

func normalizeRuleForPrincipal(c *gin.Context, rule RoutingRule, principal RouterHTTPPrincipal) (RoutingRule, bool) {
	rule.Scope = normalizeRoutingRuleScope(rule.Scope)
	if rule.Scope == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid routing rule scope"})
		return RoutingRule{}, false
	}
	if principal.Role.IsAdmin() {
		if rule.Scope == RoutingRuleScopeUser {
			rule.OwnerEmail = normalizeUserEmail(rule.OwnerEmail)
			if rule.OwnerEmail == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "owner_email is required for user routing rules"})
				return RoutingRule{}, false
			}
		}
		return rule, true
	}
	if rule.Scope == RoutingRuleScopePublic {
		c.JSON(http.StatusForbidden, gin.H{"error": "only admins can edit public routing rules"})
		return RoutingRule{}, false
	}
	if principal.Email == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "authenticated user email is required"})
		return RoutingRule{}, false
	}
	rule.Scope = RoutingRuleScopeUser
	rule.OwnerEmail = principal.Email
	return rule, true
}

func principalCanReadRoutingRule(principal RouterHTTPPrincipal, rule RoutingRule) bool {
	if rule.Scope == RoutingRuleScopePublic || principal.Role.IsAdmin() {
		return true
	}
	return normalizeUserEmail(rule.OwnerEmail) == principal.Email
}

func principalCanEditRoutingRule(principal RouterHTTPPrincipal, rule RoutingRule) bool {
	if principal.Role.IsAdmin() {
		return true
	}
	return rule.Scope == RoutingRuleScopeUser && normalizeUserEmail(rule.OwnerEmail) == principal.Email
}

func handleOpenAIChatCompletions(c *gin.Context, opts HTTPOptions, buildRouteRequest func(compat.OpenAIChatRequest) RouteRequest) {
	engine, ok := requireEngine(c, opts.Engine)
	if !ok {
		return
	}
	var openaiRequest compat.OpenAIChatRequest
	if err := c.ShouldBindJSON(&openaiRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	canonicalSource := openaiRequest
	modelOmitted := strings.TrimSpace(canonicalSource.Model) == ""
	if modelOmitted {
		canonicalSource.Model = routeSelectedModelPlaceholder
	}
	canonicalRequest, err := compat.OpenAIChatRequestToCanonical(canonicalSource)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if modelOmitted {
		canonicalRequest.Model = ""
	}
	requestID := publicRequestID(c)
	routeRequest := buildRouteRequest(openaiRequest)
	execution := RouteExecutionRequest{
		RequestID:     requestID,
		RouteRequest:  routeRequest,
		QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
		QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
	}
	if openaiRequest.Stream {
		writeOpenAIChatEventStream(c, engine, execution, canonicalRequest)
		return
	}
	response, _, err := engine.Invoke(c.Request.Context(), execution, canonicalRequest)
	if err != nil {
		writeRouteError(c, err)
		return
	}
	openaiResponse, err := compat.OpenAIChatResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, openaiResponse)
}

func handleOpenAIResponses(c *gin.Context, opts HTTPOptions, buildRouteRequest func(compat.OpenAIResponsesRequest) RouteRequest) {
	engine, ok := requireEngine(c, opts.Engine)
	if !ok {
		return
	}
	var openaiRequest compat.OpenAIResponsesRequest
	if err := c.ShouldBindJSON(&openaiRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	canonicalSource := openaiRequest
	modelOmitted := strings.TrimSpace(canonicalSource.Model) == ""
	if modelOmitted {
		canonicalSource.Model = routeSelectedModelPlaceholder
	}
	canonicalRequest, err := compat.OpenAIResponsesRequestToCanonical(canonicalSource)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if modelOmitted {
		canonicalRequest.Model = ""
	}
	requestID := publicRequestID(c)
	routeRequest := buildRouteRequest(openaiRequest)
	execution := RouteExecutionRequest{
		RequestID:     requestID,
		RouteRequest:  routeRequest,
		QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
		QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
	}
	if openaiRequest.Stream {
		writeOpenAIResponsesEventStream(c, engine, execution, canonicalRequest)
		return
	}
	response, _, err := engine.Invoke(c.Request.Context(), execution, canonicalRequest)
	if err != nil {
		writeRouteError(c, err)
		return
	}
	openaiResponse, err := compat.OpenAIResponsesResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, openaiResponse)
}

func handleAnthropicMessages(c *gin.Context, opts HTTPOptions, buildRouteRequest func(compat.AnthropicMessagesRequest) RouteRequest) {
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
	routeRequest := buildRouteRequest(anthropicRequest)
	execution := RouteExecutionRequest{
		RequestID:     requestID,
		RouteRequest:  routeRequest,
		QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
		QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
	}
	if anthropicRequest.Stream {
		writeAnthropicMessagesEventStream(c, engine, execution, canonicalRequest)
		return
	}
	response, _, err := engine.Invoke(c.Request.Context(), execution, canonicalRequest)
	if err != nil {
		writeRouteError(c, err)
		return
	}
	anthropicResponse, err := compat.AnthropicMessagesResponseFromCanonical(response)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, anthropicResponse)
}

func handleOpenAIOrAnthropicModels(c *gin.Context, opts HTTPOptions) {
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
}

func handleGeminiModelsList(c *gin.Context, opts HTTPOptions) {
	engine, ok := requireEngine(c, opts.Engine)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, geminiModelsFromEngine(engine))
}

func handleGeminiModelGet(c *gin.Context, opts HTTPOptions) {
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
}

func dashboardCompatRouteRequest(c *gin.Context, model string, dialect compat.APIDialect, stream bool) RouteRequest {
	routeRequest := RouteRequest{
		TenantID:   "dashboard",
		UserID:     "dashboard",
		Model:      model,
		APIDialect: dialect,
		Stream:     stream,
	}
	applyRouteRequestProviderHeaders(c, &routeRequest)
	if value, ok := c.Get("router_admin_principal"); ok {
		if principal, ok := value.(security.APIKeyPrincipal); ok && principal.ID != "" {
			routeRequest.TenantID = principal.TenantID
			routeRequest.UserID = principal.UserID
			routeRequest.APIKeyID = principal.ID
			return routeRequest
		}
	}
	if value, ok := c.Get(routerAdminSessionContextKey); ok {
		if session, ok := value.(GoogleOAuthSession); ok && session.Email != "" {
			routeRequest.TenantID = "google"
			routeRequest.UserID = session.Email
			return routeRequest
		}
	}
	if tenantID := strings.TrimSpace(c.GetHeader("x-pangaea-tenant-id")); tenantID != "" {
		routeRequest.TenantID = tenantID
	}
	if userID := strings.TrimSpace(c.GetHeader("x-pangaea-user-id")); userID != "" {
		routeRequest.UserID = userID
	}
	if apiKeyID := strings.TrimSpace(c.GetHeader("x-pangaea-api-key-id")); apiKeyID != "" {
		routeRequest.APIKeyID = apiKeyID
	}
	return routeRequest
}

func applyRouteRequestProviderHeaders(c *gin.Context, routeRequest *RouteRequest) {
	if c == nil || routeRequest == nil {
		return
	}
	if providerInstanceID := firstNonEmpty(
		c.GetHeader("x-pangaea-provider-instance-id"),
		c.Query("provider_instance_id"),
	); providerInstanceID != "" {
		routeRequest.ProviderInstanceID = providerInstanceID
	}
	if providerType := firstNonEmpty(
		c.GetHeader("x-pangaea-provider-type"),
		c.Query("provider_type"),
	); providerType != "" {
		routeRequest.ProviderType = providerType
	}
}

func routerAdminAuthMiddleware(store *security.APIKeyStore, auth AdminAuthOptions, engine *Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/router/v1/") || path == "/router/v1/control/ws" || path == "/router/v1/data/ws" {
			c.Next()
			return
		}
		principal, session, ok := authenticateRouterAdminRequest(c, store, auth, engine)
		if !ok {
			c.Abort()
			return
		}
		if principal.ID != "" {
			c.Set("router_admin_principal", principal)
		}
		if session.Email != "" {
			c.Set(routerAdminSessionContextKey, session)
		}
		c.Next()
	}
}

func traceHTTPExchangeMiddleware(engine *Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		if engine == nil || !traceCapturablePath(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		requestID := publicRequestID(c)
		rawRequestBody, _ := io.ReadAll(c.Request.Body)
		_ = c.Request.Body.Close()
		c.Request.Body = io.NopCloser(bytes.NewReader(rawRequestBody))
		responseWriter := &traceCaptureResponseWriter{ResponseWriter: c.Writer}
		c.Writer = responseWriter
		c.Next()
		engine.AttachRequestTraceHTTP(requestID, RequestTraceHTTP{
			Request: RequestTraceHTTPRequest{
				Method:  c.Request.Method,
				Path:    c.Request.URL.Path,
				Query:   c.Request.URL.RawQuery,
				Headers: redactedHeaders(c.Request.Header),
				Body:    traceHTTPBody(rawRequestBody, c.GetHeader("content-type"), false),
			},
			Response: RequestTraceHTTPResponse{
				Status:  responseWriter.Status(),
				Headers: redactedHeaders(c.Writer.Header()),
				Body:    traceHTTPBody(responseWriter.body.Bytes(), c.Writer.Header().Get("content-type"), responseWriter.truncated),
			},
		})
	}
}

func traceCapturablePath(method string, path string) bool {
	if method != http.MethodPost {
		return false
	}
	return path == "/v1/chat/completions" ||
		path == "/v1/responses" ||
		path == "/v1/messages" ||
		path == "/router/v1/compat/v1/chat/completions" ||
		path == "/router/v1/compat/v1/responses" ||
		path == "/router/v1/compat/v1/messages" ||
		(strings.HasPrefix(path, "/route/") && strings.HasSuffix(path, "/v1/responses")) ||
		strings.HasPrefix(path, "/v1beta/models/") ||
		strings.HasPrefix(path, "/v1/models/") ||
		strings.HasPrefix(path, "/router/v1/compat/v1beta/models/") ||
		strings.HasPrefix(path, "/router/v1/compat/v1/models/")
}

type traceCaptureResponseWriter struct {
	gin.ResponseWriter
	body      bytes.Buffer
	truncated bool
}

func (w *traceCaptureResponseWriter) Write(data []byte) (int, error) {
	w.capture(data)
	return w.ResponseWriter.Write(data)
}

func (w *traceCaptureResponseWriter) WriteString(data string) (int, error) {
	w.capture([]byte(data))
	return w.ResponseWriter.WriteString(data)
}

func (w *traceCaptureResponseWriter) capture(data []byte) {
	if len(data) == 0 || w.body.Len() >= maxTraceHTTPBodyBytes {
		if len(data) > 0 {
			w.truncated = true
		}
		return
	}
	remaining := maxTraceHTTPBodyBytes - w.body.Len()
	if len(data) > remaining {
		_, _ = w.body.Write(data[:remaining])
		w.truncated = true
		return
	}
	_, _ = w.body.Write(data)
}

func redactedHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		copied := append([]string(nil), values...)
		if shouldRedactHeader(canonical) {
			for i, value := range copied {
				copied[i] = redactHeaderValue(value)
			}
		}
		out[canonical] = copied
	}
	return out
}

func shouldRedactHeader(key string) bool {
	key = strings.ToLower(key)
	return key == "authorization" ||
		key == "proxy-authorization" ||
		key == "cookie" ||
		key == "set-cookie" ||
		strings.Contains(key, "api-key") ||
		strings.Contains(key, "token")
}

func redactHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return "Bearer <redacted>"
	}
	if value == "" {
		return "<redacted>"
	}
	return "<redacted>"
}

func traceHTTPBody(raw []byte, contentType string, truncated bool) *RequestTraceHTTPBody {
	if len(raw) == 0 && !truncated {
		return nil
	}
	body := &RequestTraceHTTPBody{
		ContentType: contentType,
		Truncated:   truncated,
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return body
	}
	if json.Valid(trimmed) {
		body.JSON = append([]byte(nil), trimmed...)
		return body
	}
	if jsonl := parseTraceJSONL(trimmed, contentType); len(jsonl) > 0 {
		body.JSONL = jsonl
		return body
	}
	body.Text = string(trimmed)
	return body
}

func parseTraceJSONL(raw []byte, contentType string) []json.RawMessage {
	lines := strings.Split(string(raw), "\n")
	out := make([]json.RawMessage, 0, len(lines))
	sawPayloadLine := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			sawPayloadLine = true
		} else if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
			continue
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		if !json.Valid([]byte(line)) {
			return nil
		}
		out = append(out, json.RawMessage(append([]byte(nil), line...)))
	}
	if len(out) == 0 || (!sawPayloadLine && len(out) == 1) {
		return nil
	}
	return out
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
		InputTokenLimit:            firstPositive(model.ContextTokens, model.MaxContextTokens),
		OutputTokenLimit:           model.MaxOutputTokens,
		SupportedGenerationMethods: methods,
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
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

type requestTraceDeleteRequest struct {
	RequestIDs []string `json:"request_ids"`
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

type providerDeleteHTTPRequest struct {
	ProviderInstanceIDs []string `json:"provider_instance_ids"`
	Reason              string   `json:"reason,omitempty"`
	Confirm             bool     `json:"confirm,omitempty"`
}

type openAIChatStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        openAIChatStreamDelta `json:"delta"`
	FinishReason string                `json:"finish_reason,omitempty"`
}

type openAIChatStreamDelta struct {
	Role      string                     `json:"role,omitempty"`
	Content   string                     `json:"content,omitempty"`
	ToolCalls []openAIChatStreamToolCall `json:"tool_calls,omitempty"`
}

type openAIChatStreamToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function openAIStreamFunction `json:"function"`
}

type openAIStreamFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

const (
	streamDeltaFlushInterval = 120 * time.Millisecond
	streamDeltaFlushBytes    = 512
)

type openAIChatEventStreamWriter struct {
	id               string
	model            string
	created          int64
	wrote            bool
	done             bool
	usage            compat.Usage
	pendingContent   strings.Builder
	lastContentFlush time.Time
}

func writeOpenAIChatEventStream(c *gin.Context, engine *Engine, execution RouteExecutionRequest, request compat.Request) {
	writer := &openAIChatEventStreamWriter{
		id:      execution.RequestID,
		model:   request.Model,
		created: time.Now().Unix(),
	}
	_, _, err := engine.InvokeStream(c.Request.Context(), execution, request, func(event compat.Event) error {
		return writer.write(c, event)
	})
	if err != nil {
		if !writer.wrote {
			writeRouteError(c, err)
			return
		}
		writer.writeError(c, err)
		return
	}
	if !writer.done {
		writer.writeDone(c, "stop")
	}
}

func (w *openAIChatEventStreamWriter) ensure(c *gin.Context) {
	if w.wrote {
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)
	w.wrote = true
}

func (w *openAIChatEventStreamWriter) write(c *gin.Context, event compat.Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate upstream stream event %s: %w", event.Type, err)
	}
	w.applyEventMeta(event)
	w.ensure(c)
	switch event.Type {
	case compat.EventMessageStart:
		w.flushContent(c)
		writeSSEData(c, openAIChatStreamChunk{
			ID:      w.id,
			Object:  "chat.completion.chunk",
			Created: w.created,
			Model:   w.model,
			Choices: []openAIChatStreamChoice{{
				Index: 0,
				Delta: openAIChatStreamDelta{Role: "assistant"},
			}},
		})
	case compat.EventContentDelta:
		w.writeContentDelta(c, event.ContentDelta.Text)
	case compat.EventToolCallDelta:
		w.writeToolCallDelta(c, *event.ToolCallDelta)
	case compat.EventUsageDelta:
		w.flushContent(c)
		w.usage.InputTokens += event.UsageDelta.InputTokens
		w.usage.OutputTokens += event.UsageDelta.OutputTokens
		w.usage.TotalTokens += event.UsageDelta.TotalTokens
	case compat.EventDone:
		w.writeDone(c, event.DoneReason)
	case compat.EventError:
		w.writeErrorMessage(c, event.Error.Message)
	}
	return nil
}

func (w *openAIChatEventStreamWriter) writeContentDelta(c *gin.Context, text string) {
	if text == "" {
		return
	}
	w.pendingContent.WriteString(text)
	now := time.Now()
	if w.lastContentFlush.IsZero() || w.pendingContent.Len() >= streamDeltaFlushBytes || now.Sub(w.lastContentFlush) >= streamDeltaFlushInterval {
		w.flushContent(c)
	}
}

func (w *openAIChatEventStreamWriter) flushContent(c *gin.Context) {
	if w.pendingContent.Len() == 0 {
		return
	}
	text := w.pendingContent.String()
	w.pendingContent.Reset()
	w.lastContentFlush = time.Now()
	writeSSEData(c, openAIChatStreamChunk{
		ID:      w.id,
		Object:  "chat.completion.chunk",
		Created: w.created,
		Model:   w.model,
		Choices: []openAIChatStreamChoice{{
			Index: 0,
			Delta: openAIChatStreamDelta{Content: text},
		}},
	})
}

func (w *openAIChatEventStreamWriter) writeToolCallDelta(c *gin.Context, tool compat.ToolCall) {
	w.flushContent(c)
	callType := string(tool.Type)
	if callType == "" && (tool.ID != "" || tool.Name != "" || tool.Arguments != "") {
		callType = string(compat.ToolCallFunction)
	}
	writeSSEData(c, openAIChatStreamChunk{
		ID:      w.id,
		Object:  "chat.completion.chunk",
		Created: w.created,
		Model:   w.model,
		Choices: []openAIChatStreamChoice{{
			Index: 0,
			Delta: openAIChatStreamDelta{
				ToolCalls: []openAIChatStreamToolCall{{
					Index: tool.Index,
					ID:    tool.ID,
					Type:  callType,
					Function: openAIStreamFunction{
						Name:      tool.Name,
						Arguments: tool.Arguments,
					},
				}},
			},
		}},
	})
}

func (w *openAIChatEventStreamWriter) applyEventMeta(event compat.Event) {
	if event.ResponseID != "" {
		w.id = event.ResponseID
	}
	if event.Model != "" {
		w.model = event.Model
	}
}

func (w *openAIChatEventStreamWriter) writeDone(c *gin.Context, finishReason string) {
	if w.done {
		return
	}
	w.ensure(c)
	w.flushContent(c)
	usage := (*compat.OpenAIUsage)(nil)
	if w.usage != (compat.Usage{}) {
		total := w.usage.TotalTokens
		if total == 0 {
			total = w.usage.InputTokens + w.usage.OutputTokens
		}
		usage = &compat.OpenAIUsage{
			PromptTokens:     w.usage.InputTokens,
			CompletionTokens: w.usage.OutputTokens,
			TotalTokens:      total,
		}
	}
	writeSSEData(c, openAIChatStreamChunk{
		ID:      w.id,
		Object:  "chat.completion.chunk",
		Created: w.created,
		Model:   w.model,
		Choices: []openAIChatStreamChoice{{
			Index:        0,
			Delta:        openAIChatStreamDelta{},
			FinishReason: finishReason,
		}},
		Usage: usage,
	})
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	flushSSE(c)
	w.done = true
}

func (w *openAIChatEventStreamWriter) writeError(c *gin.Context, err error) {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	w.writeErrorMessage(c, message)
}

func (w *openAIChatEventStreamWriter) writeErrorMessage(c *gin.Context, message string) {
	w.ensure(c)
	w.flushContent(c)
	writeSSEData(c, gin.H{"error": gin.H{"message": message}})
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	flushSSE(c)
	w.done = true
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
		if text, ok := openaiResponse.Choices[0].Message.Content.(string); ok {
			content = text
		}
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

type openAIResponsesEventStreamWriter struct {
	id                 string
	model              string
	itemID             string
	created            int64
	wrote              bool
	done               bool
	outputStarted      bool
	messageOutputIndex int
	usage              compat.Usage
	pendingContent     strings.Builder
	outputText         strings.Builder
	toolItems          map[int]*openAIResponsesToolState
	toolOrder          []int
	lastContentFlush   time.Time
}

type openAIResponsesToolState struct {
	index       int
	outputIndex int
	itemID      string
	callID      string
	name        string
	arguments   strings.Builder
	added       bool
}

func writeOpenAIResponsesEventStream(c *gin.Context, engine *Engine, execution RouteExecutionRequest, request compat.Request) {
	writer := &openAIResponsesEventStreamWriter{
		id:      execution.RequestID,
		model:   request.Model,
		itemID:  "msg_" + execution.RequestID,
		created: time.Now().Unix(),
	}
	_, _, err := engine.InvokeStream(c.Request.Context(), execution, request, func(event compat.Event) error {
		return writer.write(c, event)
	})
	if err != nil {
		if !writer.wrote {
			writeRouteError(c, err)
			return
		}
		writer.writeError(c, err)
		return
	}
	if !writer.done {
		writer.writeDone(c)
	}
}

func (w *openAIResponsesEventStreamWriter) ensure(c *gin.Context) {
	if w.wrote {
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)
	w.wrote = true
	writeSSEEvent(c, "response.created", gin.H{
		"type": "response.created",
		"response": compat.OpenAIResponsesResponse{
			ID:        w.id,
			Object:    "response",
			CreatedAt: w.created,
			Status:    "in_progress",
			Model:     w.model,
			Output:    []compat.OpenAIResponsesOutputItem{},
		},
	})
}

func (w *openAIResponsesEventStreamWriter) write(c *gin.Context, event compat.Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate upstream stream event %s: %w", event.Type, err)
	}
	w.applyEventMeta(event)
	switch event.Type {
	case compat.EventMessageStart:
		return nil
	case compat.EventContentDelta:
		w.writeContentDelta(c, event.ContentDelta.Text)
	case compat.EventToolCallDelta:
		w.writeToolCallDelta(c, *event.ToolCallDelta)
	case compat.EventUsageDelta:
		w.flushContent(c)
		w.usage.InputTokens += event.UsageDelta.InputTokens
		w.usage.OutputTokens += event.UsageDelta.OutputTokens
		w.usage.TotalTokens += event.UsageDelta.TotalTokens
	case compat.EventDone:
		w.writeDone(c)
	case compat.EventError:
		w.writeErrorMessage(c, event.Error.Message)
	}
	return nil
}

func (w *openAIResponsesEventStreamWriter) writeContentDelta(c *gin.Context, text string) {
	if text == "" {
		return
	}
	w.pendingContent.WriteString(text)
	now := time.Now()
	if w.lastContentFlush.IsZero() || w.pendingContent.Len() >= streamDeltaFlushBytes || now.Sub(w.lastContentFlush) >= streamDeltaFlushInterval {
		w.flushContent(c)
	}
}

func (w *openAIResponsesEventStreamWriter) flushContent(c *gin.Context) {
	if w.pendingContent.Len() == 0 {
		return
	}
	w.ensure(c)
	w.ensureOutputStarted(c)
	text := w.pendingContent.String()
	w.pendingContent.Reset()
	w.lastContentFlush = time.Now()
	w.outputText.WriteString(text)
	writeSSEEvent(c, "response.output_text.delta", gin.H{
		"type":          "response.output_text.delta",
		"response_id":   w.id,
		"item_id":       w.itemID,
		"output_index":  w.messageOutputIndex,
		"content_index": 0,
		"delta":         text,
	})
}

func (w *openAIResponsesEventStreamWriter) ensureOutputStarted(c *gin.Context) {
	if w.outputStarted {
		return
	}
	w.messageOutputIndex = len(w.toolOrder)
	writeSSEEvent(c, "response.output_item.added", gin.H{
		"type":         "response.output_item.added",
		"response_id":  w.id,
		"output_index": w.messageOutputIndex,
		"item": compat.OpenAIResponsesOutputItem{
			ID:      w.itemID,
			Type:    "message",
			Status:  "in_progress",
			Role:    string(compat.MessageRoleAssistant),
			Content: []compat.OpenAIResponsesOutputContent{},
		},
	})
	writeSSEEvent(c, "response.content_part.added", gin.H{
		"type":          "response.content_part.added",
		"response_id":   w.id,
		"item_id":       w.itemID,
		"output_index":  w.messageOutputIndex,
		"content_index": 0,
		"part": compat.OpenAIResponsesOutputContent{
			Type: "output_text",
			Text: "",
		},
	})
	w.outputStarted = true
}

func (w *openAIResponsesEventStreamWriter) writeToolCallDelta(c *gin.Context, tool compat.ToolCall) {
	w.flushContent(c)
	state := w.ensureToolItem(c, tool)
	if tool.Arguments == "" {
		return
	}
	state.arguments.WriteString(tool.Arguments)
	writeSSEEvent(c, "response.function_call_arguments.delta", gin.H{
		"type":         "response.function_call_arguments.delta",
		"response_id":  w.id,
		"item_id":      state.itemID,
		"output_index": state.outputIndex,
		"delta":        tool.Arguments,
	})
}

func (w *openAIResponsesEventStreamWriter) ensureToolItem(c *gin.Context, tool compat.ToolCall) *openAIResponsesToolState {
	if w.toolItems == nil {
		w.toolItems = map[int]*openAIResponsesToolState{}
	}
	index := tool.Index
	state, ok := w.toolItems[index]
	if !ok {
		state = &openAIResponsesToolState{
			index:       index,
			outputIndex: len(w.toolOrder),
		}
		if w.outputStarted {
			state.outputIndex++
		}
		w.toolItems[index] = state
		w.toolOrder = append(w.toolOrder, index)
	}
	if tool.ID != "" {
		state.callID = tool.ID
	}
	if tool.Name != "" {
		state.name = tool.Name
	}
	if state.callID == "" {
		state.callID = fmt.Sprintf("call_%s_%d", w.id, index)
	}
	if state.itemID == "" {
		state.itemID = state.callID
	}
	if !state.added {
		w.ensure(c)
		writeSSEEvent(c, "response.output_item.added", gin.H{
			"type":         "response.output_item.added",
			"response_id":  w.id,
			"output_index": state.outputIndex,
			"item": compat.OpenAIResponsesOutputItem{
				ID:        state.itemID,
				Type:      "function_call",
				Status:    "in_progress",
				CallID:    state.callID,
				Name:      state.name,
				Arguments: "",
			},
		})
		state.added = true
	}
	return state
}

func (w *openAIResponsesEventStreamWriter) applyEventMeta(event compat.Event) {
	if event.ResponseID != "" {
		w.id = event.ResponseID
		if w.itemID == "" || strings.HasPrefix(w.itemID, "msg_") {
			w.itemID = "msg_" + event.ResponseID
		}
	}
	if event.Model != "" {
		w.model = event.Model
	}
}

func (w *openAIResponsesEventStreamWriter) writeDone(c *gin.Context) {
	if w.done {
		return
	}
	w.ensure(c)
	w.flushContent(c)
	outputText := w.outputText.String()
	if w.outputStarted {
		writeSSEEvent(c, "response.output_text.done", gin.H{
			"type":          "response.output_text.done",
			"response_id":   w.id,
			"item_id":       w.itemID,
			"output_index":  w.messageOutputIndex,
			"content_index": 0,
			"text":          outputText,
		})
		writeSSEEvent(c, "response.content_part.done", gin.H{
			"type":          "response.content_part.done",
			"response_id":   w.id,
			"item_id":       w.itemID,
			"output_index":  w.messageOutputIndex,
			"content_index": 0,
			"part": compat.OpenAIResponsesOutputContent{
				Type: "output_text",
				Text: outputText,
			},
		})
		writeSSEEvent(c, "response.output_item.done", gin.H{
			"type":         "response.output_item.done",
			"response_id":  w.id,
			"output_index": w.messageOutputIndex,
			"item": compat.OpenAIResponsesOutputItem{
				ID:     w.itemID,
				Type:   "message",
				Status: "completed",
				Role:   string(compat.MessageRoleAssistant),
				Content: []compat.OpenAIResponsesOutputContent{{
					Type: "output_text",
					Text: outputText,
				}},
			},
		})
	}
	w.writeToolItemsDone(c)
	writeSSEEvent(c, "response.completed", gin.H{
		"type":     "response.completed",
		"response": w.completedResponse(outputText),
	})
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	flushSSE(c)
	w.done = true
}

func (w *openAIResponsesEventStreamWriter) writeToolItemsDone(c *gin.Context) {
	for _, index := range w.toolOrder {
		state := w.toolItems[index]
		if state == nil || !state.added {
			continue
		}
		arguments := state.arguments.String()
		writeSSEEvent(c, "response.function_call_arguments.done", gin.H{
			"type":         "response.function_call_arguments.done",
			"response_id":  w.id,
			"item_id":      state.itemID,
			"output_index": state.outputIndex,
			"arguments":    arguments,
		})
		writeSSEEvent(c, "response.output_item.done", gin.H{
			"type":         "response.output_item.done",
			"response_id":  w.id,
			"output_index": state.outputIndex,
			"item": compat.OpenAIResponsesOutputItem{
				ID:        state.itemID,
				Type:      "function_call",
				Status:    "completed",
				CallID:    state.callID,
				Name:      state.name,
				Arguments: arguments,
			},
		})
	}
}

func (w *openAIResponsesEventStreamWriter) completedResponse(outputText string) compat.OpenAIResponsesResponse {
	response := compat.OpenAIResponsesResponse{
		ID:        w.id,
		Object:    "response",
		CreatedAt: w.created,
		Status:    "completed",
		Model:     w.model,
		Output:    []compat.OpenAIResponsesOutputItem{},
	}
	if outputText != "" || w.outputStarted {
		response.Output = append(response.Output, compat.OpenAIResponsesOutputItem{
			ID:     w.itemID,
			Type:   "message",
			Status: "completed",
			Role:   string(compat.MessageRoleAssistant),
			Content: []compat.OpenAIResponsesOutputContent{{
				Type: "output_text",
				Text: outputText,
			}},
		})
		response.OutputText = outputText
	}
	for _, index := range w.toolOrder {
		state := w.toolItems[index]
		if state == nil {
			continue
		}
		response.Output = append(response.Output, compat.OpenAIResponsesOutputItem{
			ID:        state.itemID,
			Type:      "function_call",
			Status:    "completed",
			CallID:    state.callID,
			Name:      state.name,
			Arguments: state.arguments.String(),
		})
	}
	if w.usage != (compat.Usage{}) {
		total := w.usage.TotalTokens
		if total == 0 {
			total = w.usage.InputTokens + w.usage.OutputTokens
		}
		response.Usage = &compat.OpenAIResponsesUsage{
			InputTokens:  w.usage.InputTokens,
			OutputTokens: w.usage.OutputTokens,
			TotalTokens:  total,
		}
	}
	return response
}

func (w *openAIResponsesEventStreamWriter) writeError(c *gin.Context, err error) {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	w.writeErrorMessage(c, message)
}

func (w *openAIResponsesEventStreamWriter) writeErrorMessage(c *gin.Context, message string) {
	w.ensure(c)
	w.flushContent(c)
	writeSSEEvent(c, "error", gin.H{
		"type":  "error",
		"error": gin.H{"message": message},
	})
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	flushSSE(c)
	w.done = true
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

type anthropicMessagesEventStreamWriter struct {
	id           string
	model        string
	wrote        bool
	done         bool
	blockStarted bool
	usage        compat.Usage
}

func writeAnthropicMessagesEventStream(c *gin.Context, engine *Engine, execution RouteExecutionRequest, request compat.Request) {
	writer := &anthropicMessagesEventStreamWriter{
		id:    execution.RequestID,
		model: request.Model,
	}
	_, _, err := engine.InvokeStream(c.Request.Context(), execution, request, func(event compat.Event) error {
		return writer.write(c, event)
	})
	if err != nil {
		if !writer.wrote {
			writeRouteError(c, err)
			return
		}
		writer.writeError(c, err)
		return
	}
	if !writer.done {
		writer.writeDone(c, "end_turn")
	}
}

func (w *anthropicMessagesEventStreamWriter) ensure(c *gin.Context) {
	if w.wrote {
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)
	w.wrote = true
}

func (w *anthropicMessagesEventStreamWriter) write(c *gin.Context, event compat.Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate upstream stream event %s: %w", event.Type, err)
	}
	w.applyEventMeta(event)
	w.ensure(c)
	switch event.Type {
	case compat.EventMessageStart:
		writeSSEEvent(c, "message_start", gin.H{
			"type": "message_start",
			"message": compat.AnthropicMessagesResponse{
				ID:      w.id,
				Type:    "message",
				Role:    string(compat.MessageRoleAssistant),
				Model:   w.model,
				Content: []compat.AnthropicContentBlock{},
				Usage:   compat.AnthropicUsage{},
			},
		})
	case compat.EventContentDelta:
		if !w.blockStarted {
			writeSSEEvent(c, "content_block_start", gin.H{
				"type":          "content_block_start",
				"index":         0,
				"content_block": compat.AnthropicContentBlock{Type: "text", Text: ""},
			})
			w.blockStarted = true
		}
		writeSSEEvent(c, "content_block_delta", gin.H{
			"type":  "content_block_delta",
			"index": 0,
			"delta": gin.H{
				"type": "text_delta",
				"text": event.ContentDelta.Text,
			},
		})
	case compat.EventUsageDelta:
		w.usage.InputTokens += event.UsageDelta.InputTokens
		w.usage.OutputTokens += event.UsageDelta.OutputTokens
		w.usage.TotalTokens += event.UsageDelta.TotalTokens
	case compat.EventDone:
		w.writeDone(c, canonicalStopToAnthropicEvent(event.DoneReason))
	case compat.EventError:
		w.writeErrorMessage(c, event.Error.Message)
	}
	return nil
}

func (w *anthropicMessagesEventStreamWriter) applyEventMeta(event compat.Event) {
	if event.ResponseID != "" {
		w.id = event.ResponseID
	}
	if event.Model != "" {
		w.model = event.Model
	}
}

func (w *anthropicMessagesEventStreamWriter) writeDone(c *gin.Context, stopReason string) {
	if w.done {
		return
	}
	w.ensure(c)
	if w.blockStarted {
		writeSSEEvent(c, "content_block_stop", gin.H{
			"type":  "content_block_stop",
			"index": 0,
		})
	}
	writeSSEEvent(c, "message_delta", gin.H{
		"type": "message_delta",
		"delta": gin.H{
			"stop_reason": stopReason,
		},
		"usage": gin.H{
			"output_tokens": w.usage.OutputTokens,
		},
	})
	writeSSEEvent(c, "message_stop", gin.H{"type": "message_stop"})
	w.done = true
}

func (w *anthropicMessagesEventStreamWriter) writeError(c *gin.Context, err error) {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	w.writeErrorMessage(c, message)
}

func (w *anthropicMessagesEventStreamWriter) writeErrorMessage(c *gin.Context, message string) {
	w.ensure(c)
	writeSSEEvent(c, "error", gin.H{
		"type":  "error",
		"error": gin.H{"message": message},
	})
	w.done = true
}

func canonicalStopToAnthropicEvent(stop string) string {
	switch stop {
	case "", "stop":
		return "end_turn"
	default:
		return stop
	}
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

type geminiGenerateContentEventStreamWriter struct {
	model            string
	wrote            bool
	done             bool
	usage            compat.Usage
	pendingContent   strings.Builder
	lastContentFlush time.Time
}

func writeGeminiGenerateContentEventStream(c *gin.Context, engine *Engine, execution RouteExecutionRequest, request compat.Request) {
	writer := &geminiGenerateContentEventStreamWriter{model: request.Model}
	_, _, err := engine.InvokeStream(c.Request.Context(), execution, request, func(event compat.Event) error {
		return writer.write(c, event)
	})
	if err != nil {
		if !writer.wrote {
			writeRouteError(c, err)
			return
		}
		writer.writeError(c, err)
		return
	}
	if !writer.done {
		writer.writeDone(c)
	}
}

func (w *geminiGenerateContentEventStreamWriter) ensure(c *gin.Context) {
	if w.wrote {
		return
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)
	w.wrote = true
}

func (w *geminiGenerateContentEventStreamWriter) write(c *gin.Context, event compat.Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate upstream stream event %s: %w", event.Type, err)
	}
	if event.Model != "" {
		w.model = event.Model
	}
	w.ensure(c)
	switch event.Type {
	case compat.EventContentDelta:
		w.writeContentDelta(c, event.ContentDelta.Text)
	case compat.EventUsageDelta:
		w.flushContent(c)
		w.usage.InputTokens += event.UsageDelta.InputTokens
		w.usage.OutputTokens += event.UsageDelta.OutputTokens
		w.usage.TotalTokens += event.UsageDelta.TotalTokens
	case compat.EventDone:
		w.writeDone(c)
	case compat.EventError:
		w.writeErrorMessage(c, event.Error.Message)
	}
	return nil
}

func (w *geminiGenerateContentEventStreamWriter) writeContentDelta(c *gin.Context, text string) {
	if text == "" {
		return
	}
	w.pendingContent.WriteString(text)
	now := time.Now()
	if w.lastContentFlush.IsZero() || w.pendingContent.Len() >= streamDeltaFlushBytes || now.Sub(w.lastContentFlush) >= streamDeltaFlushInterval {
		w.flushContent(c)
	}
}

func (w *geminiGenerateContentEventStreamWriter) flushContent(c *gin.Context) {
	if w.pendingContent.Len() == 0 {
		return
	}
	text := w.pendingContent.String()
	w.pendingContent.Reset()
	w.lastContentFlush = time.Now()
	writeSSEData(c, compat.GeminiGenerateContentResponse{
		ModelVersion: w.model,
		Candidates: []compat.GeminiCandidate{{
			Index:        0,
			Content:      compat.GeminiContent{Role: "model", Parts: []compat.GeminiPart{{Text: text}}},
			FinishReason: "",
		}},
	})
}

func (w *geminiGenerateContentEventStreamWriter) writeDone(c *gin.Context) {
	if w.done {
		return
	}
	w.ensure(c)
	w.flushContent(c)
	if w.usage != (compat.Usage{}) {
		total := w.usage.TotalTokens
		if total == 0 {
			total = w.usage.InputTokens + w.usage.OutputTokens
		}
		writeSSEData(c, compat.GeminiGenerateContentResponse{
			ModelVersion: w.model,
			Candidates: []compat.GeminiCandidate{{
				Index:        0,
				Content:      compat.GeminiContent{Role: "model", Parts: []compat.GeminiPart{{Text: ""}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &compat.GeminiUsage{
				PromptTokenCount:     w.usage.InputTokens,
				CandidatesTokenCount: w.usage.OutputTokens,
				TotalTokenCount:      total,
			},
		})
	}
	w.done = true
}

func (w *geminiGenerateContentEventStreamWriter) writeError(c *gin.Context, err error) {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	w.writeErrorMessage(c, message)
}

func (w *geminiGenerateContentEventStreamWriter) writeErrorMessage(c *gin.Context, message string) {
	w.ensure(c)
	w.flushContent(c)
	writeSSEData(c, gin.H{"error": gin.H{"message": message}})
	w.done = true
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

func writeRouteError(c *gin.Context, err error) {
	var upstream *provider.UpstreamError
	if errors.As(err, &upstream) && upstream != nil && upstream.RetryAfter != "" {
		c.Header("retry-after", upstream.RetryAfter)
	}
	c.JSON(routeErrorStatus(err), routeErrorPayload(err))
}

func routeErrorStatus(err error) int {
	var upstream *provider.UpstreamError
	switch {
	case errors.As(err, &upstream):
		return upstream.RouterStatusCode()
	case errors.Is(err, quota.ErrQuotaExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrRouterNotReady):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusGatewayTimeout
	default:
		return http.StatusConflict
	}
}

func routeErrorPayload(err error) gin.H {
	payload := gin.H{"error": err.Error()}
	var upstream *provider.UpstreamError
	if errors.As(err, &upstream) && upstream != nil {
		payload["code"] = "upstream_error"
		if upstream.Code != "" {
			payload["upstream_code"] = upstream.Code
		}
		if upstream.StatusCode > 0 {
			payload["upstream_status"] = upstream.StatusCode
		}
		if upstream.RetryAfter != "" {
			payload["retry_after"] = upstream.RetryAfter
		}
	}
	return payload
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

func handleGeminiGenerateContent(c *gin.Context, opts HTTPOptions, buildRouteRequest func(model string, stream bool) RouteRequest) {
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
	routeRequest := buildRouteRequest(model, stream)
	execution := RouteExecutionRequest{
		RequestID:     requestID,
		RouteRequest:  routeRequest,
		QuotaScope:    CanonicalQuotaScope(requestID, routeRequest, canonicalRequest),
		QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
	}
	if stream {
		writeGeminiGenerateContentEventStream(c, engine, execution, canonicalRequest)
		return
	}
	response, _, err := engine.Invoke(c.Request.Context(), execution, canonicalRequest)
	if err != nil {
		writeRouteError(c, err)
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
	if value, ok := c.Get(traceRequestIDContextKey); ok {
		if requestID, ok := value.(string); ok && requestID != "" {
			return requestID
		}
	}
	requestID := c.GetHeader("x-request-id")
	if requestID == "" {
		requestID = "req_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	c.Set(traceRequestIDContextKey, requestID)
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
	if value, ok := c.Get("router_admin_principal"); ok {
		if principal, ok := value.(security.APIKeyPrincipal); ok && principal.ID != "" {
			return AuditActor{
				TenantID:   principal.TenantID,
				UserID:     principal.UserID,
				APIKeyID:   principal.ID,
				Source:     "admin-api",
				RemoteAddr: c.ClientIP(),
				RequestID:  c.GetHeader("x-request-id"),
			}
		}
	}
	if value, ok := c.Get(routerAdminSessionContextKey); ok {
		if session, ok := value.(GoogleOAuthSession); ok && session.Email != "" {
			return AuditActor{
				TenantID:   "google",
				UserID:     session.Email,
				Source:     "google-oauth",
				RemoteAddr: c.ClientIP(),
				RequestID:  c.GetHeader("x-request-id"),
			}
		}
	}
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

func authenticateRouterAdminRequest(c *gin.Context, store *security.APIKeyStore, auth AdminAuthOptions, engine *Engine) (security.APIKeyPrincipal, GoogleOAuthSession, bool) {
	principal, ok := authenticateRouterUserRequest(c, store, auth, engine)
	if !ok {
		return security.APIKeyPrincipal{}, GoogleOAuthSession{}, false
	}
	if !principal.Role.IsAdmin() {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin role is required"})
		return security.APIKeyPrincipal{}, GoogleOAuthSession{}, false
	}
	return principal.APIKey, principal.Session, true
}

func authenticateRouterUserRequest(c *gin.Context, store *security.APIKeyStore, auth AdminAuthOptions, engine *Engine) (RouterHTTPPrincipal, bool) {
	switch auth.Mode {
	case routerAdminAuthModeOpen:
		return RouterHTTPPrincipal{Role: RouterUserRoleAdmin, AuthKind: routerAdminAuthModeOpen, Anonymous: true}, true
	case routerAdminAuthModeGoogle:
		principal, ok := authenticateRouterGoogle(c, auth.GoogleOAuth, engine)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid google oauth session"})
		}
		return principal, ok
	case routerAdminAuthModeBoth:
		if principal, ok := authenticateRouterGoogle(c, auth.GoogleOAuth, engine); ok {
			return principal, true
		}
		return authenticateRouterBearer(c, store, false)
	case routerAdminAuthModeBearer:
		fallthrough
	default:
		return authenticateRouterBearer(c, store, true)
	}
}

func authenticateRouterGoogle(c *gin.Context, oauth GoogleOAuthOptions, engine *Engine) (RouterHTTPPrincipal, bool) {
	session, ok := authenticateGoogleOAuthSession(c, oauth, engine)
	if !ok {
		return RouterHTTPPrincipal{}, false
	}
	role := normalizeRouterUserRole(RouterUserRole(session.Role))
	if role == "" {
		role = RouterUserRoleUser
	}
	return RouterHTTPPrincipal{
		Email:    normalizeUserEmail(session.Email),
		Name:     strings.TrimSpace(session.Name),
		Role:     role,
		Session:  session,
		AuthKind: routerAdminAuthModeGoogle,
	}, true
}

func authenticateRouterBearer(c *gin.Context, store *security.APIKeyStore, allowOpen bool) (RouterHTTPPrincipal, bool) {
	if store == nil || store.Len() == 0 {
		if allowOpen {
			return RouterHTTPPrincipal{Role: RouterUserRoleAdmin, AuthKind: routerAdminAuthModeOpen, Anonymous: true}, true
		}
		if bearerToken(c.GetHeader("authorization")) == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing admin session or bearer token"})
			return RouterHTTPPrincipal{}, false
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "admin bearer auth is not configured"})
		return RouterHTTPPrincipal{}, false
	}
	raw := bearerToken(c.GetHeader("authorization"))
	if raw == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return RouterHTTPPrincipal{}, false
	}
	principal, ok := store.Authenticate(raw)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid bearer token"})
		return RouterHTTPPrincipal{}, false
	}
	return RouterHTTPPrincipal{
		Email:    normalizeUserEmail(principal.UserID),
		Role:     RouterUserRoleAdmin,
		APIKey:   principal,
		AuthKind: routerAdminAuthModeBearer,
	}, true
}

func downloadFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "auth.json"
	}
	filename = strings.ReplaceAll(filename, `"`, "")
	filename = strings.ReplaceAll(filename, "\\", "")
	filename = strings.ReplaceAll(filename, "/", "")
	if filename == "" || filename == "." || filename == ".." {
		return "auth.json"
	}
	return filename
}

func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) < len(prefix) || strings.ToLower(header[:len(prefix)]) != prefix {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
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
