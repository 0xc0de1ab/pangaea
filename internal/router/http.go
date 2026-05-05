package router

import (
	"net/http"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
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
		requestID := c.GetHeader("x-request-id")
		if requestID == "" {
			requestID = "req_" + time.Now().UTC().Format("20060102150405.000000000")
		}
		routeRequest := RouteRequest{
			TenantID:   c.GetHeader("x-pangaea-tenant-id"),
			UserID:     c.GetHeader("x-pangaea-user-id"),
			APIKeyID:   c.GetHeader("x-pangaea-api-key-id"),
			Model:      openaiRequest.Model,
			APIDialect: compat.APIDialectOpenAI,
			Stream:     openaiRequest.Stream,
		}
		if principal.ID != "" {
			routeRequest.TenantID = principal.TenantID
			routeRequest.UserID = principal.UserID
			routeRequest.APIKeyID = principal.ID
		}
		response, _, err := engine.Invoke(c.Request.Context(), RouteExecutionRequest{
			RequestID:     requestID,
			RouteRequest:  routeRequest,
			QuotaScope:    OpenAIQuotaScope(requestID, routeRequest, canonicalRequest),
			QuotaEstimate: EstimateQuotaUsage(canonicalRequest),
		}, canonicalRequest)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		openaiResponse, err := compat.OpenAIChatResponseFromCanonical(response)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, openaiResponse)
	})
	r.GET("/router/v1/providers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"providers": engine.Providers()})
	})
	r.GET("/router/v1/usage/providers", func(c *gin.Context) {
		engine, ok := requireEngine(c, opts.Engine)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"usage": engine.ProviderUsages()})
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

func requireEngine(c *gin.Context, engine *Engine) (*Engine, bool) {
	if engine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrRouterNotReady.Error()})
		return nil, false
	}
	return engine, true
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
