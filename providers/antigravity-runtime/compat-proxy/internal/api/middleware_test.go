package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthMiddlewareAcceptsGeminiGoogAPIKeyHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(AuthMiddleware(APIKeys{Gemini: "gemini-key"}))
	router.POST("/v1beta/models/gemini-3-flash:generateContent", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-flash:generateContent", nil)
	req.Header.Set("x-goog-api-key", "gemini-key")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
	}
}
