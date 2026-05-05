package router

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
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

func NewHTTPHandler(opts HTTPOptions) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	r.GET("/v1/models", func(c *gin.Context) {
		if _, ok := authenticatePublicRequest(c, opts.APIKeys); !ok {
			return
		}
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		snapshot, err := engine.QuotaSnapshot(request.Scope)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
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
	model, ok := geminiModelFromAction(c.Param("modelAction"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unsupported Gemini model action"})
		return
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
	requestID := publicRequestID(c)
	routeRequest := applyPublicPrincipal(principal, RouteRequest{
		TenantID:   c.GetHeader("x-pangaea-tenant-id"),
		UserID:     c.GetHeader("x-pangaea-user-id"),
		APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
		Model:      model,
		APIDialect: compat.APIDialectGemini,
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

func geminiModelFromAction(action string) (string, bool) {
	action = strings.TrimPrefix(action, "/")
	model, suffix, ok := strings.Cut(action, ":")
	if !ok || suffix != "generateContent" || strings.TrimSpace(model) == "" {
		return "", false
	}
	return model, true
}
