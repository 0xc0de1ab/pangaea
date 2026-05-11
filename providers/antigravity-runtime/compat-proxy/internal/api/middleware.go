package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/antigravity-compat-proxy/internal/models"
)

type APIKeys struct {
	OpenAI    string
	Anthropic string
	Gemini    string
}

func AuthMiddleware(keys APIKeys) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		authHeader := c.GetHeader("Authorization")
		apiKey := ""

		// Extract API key from header or query
		if strings.HasPrefix(authHeader, "Bearer ") {
			apiKey = strings.TrimPrefix(authHeader, "Bearer ")
		} else if xApiKey := c.GetHeader("x-api-key"); xApiKey != "" {
			apiKey = xApiKey
		} else if xGoogAPIKey := c.GetHeader("x-goog-api-key"); xGoogAPIKey != "" {
			apiKey = xGoogAPIKey
		} else if keyQuery := c.Query("key"); keyQuery != "" {
			apiKey = keyQuery
		}

		// Validate based on provider path
		if strings.HasPrefix(path, "/v1/chat") {
			// OpenAI
			if apiKey != keys.OpenAI {
				resp := models.OpenAIErrorResponse{}
				resp.Error.Message = "Incorrect API key provided: " + apiKey + ". You can find your API key in the proxy logs."
				resp.Error.Type = "invalid_request_error"
				resp.Error.Code = "invalid_api_key"
				c.AbortWithStatusJSON(http.StatusUnauthorized, resp)
				return
			}
		} else if strings.HasPrefix(path, "/v1/messages") {
			// Anthropic
			if apiKey != keys.Anthropic {
				resp := models.AnthropicErrorResponse{Type: "error"}
				resp.Error.Type = "authentication_error"
				resp.Error.Message = "invalid x-api-key"
				c.AbortWithStatusJSON(http.StatusUnauthorized, resp)
				return
			}
		} else if strings.HasPrefix(path, "/v1beta/models") {
			// Gemini
			if apiKey != keys.Gemini {
				resp := models.GeminiErrorResponse{}
				resp.Error.Code = 401
				resp.Error.Message = "API key not valid. Please pass a valid API key."
				resp.Error.Status = "UNAUTHENTICATED"
				c.AbortWithStatusJSON(http.StatusUnauthorized, resp)
				return
			}
		}

		c.Next()
	}
}
