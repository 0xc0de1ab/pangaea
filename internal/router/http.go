package router

import (
	"net/http"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/gin-gonic/gin"
)

type HTTPOptions struct {
	Engine *Engine
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
