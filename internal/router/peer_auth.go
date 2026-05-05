package router

import (
	"crypto/hmac"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const routerPeerTokenHeader = "x-pangaea-peer-token"

func authenticateRouterPeerRequest(c *gin.Context, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	token := routerPeerToken(c)
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing peer token"})
		return false
	}
	if !hmac.Equal([]byte(token), []byte(expected)) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid peer token"})
		return false
	}
	return true
}

func routerPeerToken(c *gin.Context) string {
	if token := bearerToken(c.GetHeader("authorization")); token != "" {
		return token
	}
	if token := strings.TrimSpace(c.GetHeader(routerPeerTokenHeader)); token != "" {
		return token
	}
	if token := strings.TrimSpace(c.Query("peer_token")); token != "" {
		return token
	}
	return ""
}
