package router

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/routerui"
	"github.com/gin-gonic/gin"
)

const (
	embeddedRouterDashboardRoot  = "/router/ui"
	embeddedRouterDashboardIndex = "index.html"
)

var embeddedRouterDashboardFS = routerui.Dist()

func redirectToEmbeddedRouterDashboard(c *gin.Context) {
	c.Redirect(http.StatusMovedPermanently, embeddedRouterDashboardRoot)
}

func serveEmbeddedRouterDashboardWithAuth(auth AdminAuthOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		if shouldRedirectEmbeddedRouterDashboardToGoogleLogin(c, auth) {
			setEmbeddedRouterDashboardHeaders(c)
			c.Header("Cache-Control", "no-store")
			c.Redirect(http.StatusFound, embeddedRouterDashboardGoogleLoginURL(c))
			return
		}
		serveEmbeddedRouterDashboard(c)
	}
}

func serveEmbeddedRouterDashboard(c *gin.Context) {
	setEmbeddedRouterDashboardHeaders(c)

	assetPath, ok := embeddedRouterDashboardPath(c.Request.URL.Path)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	if assetPath == "" {
		serveEmbeddedRouterDashboardFile(c, embeddedRouterDashboardIndex, false)
		return
	}
	if assetPath == embeddedRouterDashboardIndex {
		serveEmbeddedRouterDashboardFile(c, embeddedRouterDashboardIndex, false)
		return
	}

	info, err := fs.Stat(embeddedRouterDashboardFS, assetPath)
	if err == nil && !info.IsDir() {
		serveEmbeddedRouterDashboardFile(c, assetPath, true)
		return
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		c.String(http.StatusInternalServerError, "router dashboard asset unavailable")
		return
	}
	if embeddedRouterDashboardLooksLikeAsset(assetPath) {
		c.Status(http.StatusNotFound)
		return
	}

	serveEmbeddedRouterDashboardFile(c, embeddedRouterDashboardIndex, false)
}

func shouldRedirectEmbeddedRouterDashboardToGoogleLogin(c *gin.Context, auth AdminAuthOptions) bool {
	if !embeddedRouterDashboardRequiresGoogleLogin(auth) {
		return false
	}
	assetPath, ok := embeddedRouterDashboardPath(c.Request.URL.Path)
	if !ok {
		return false
	}
	if assetPath != "" && assetPath != embeddedRouterDashboardIndex && embeddedRouterDashboardLooksLikeAsset(assetPath) {
		return false
	}
	_, ok = authenticateGoogleOAuthSession(c, auth.GoogleOAuth)
	return !ok
}

func embeddedRouterDashboardRequiresGoogleLogin(auth AdminAuthOptions) bool {
	if !auth.GoogleOAuth.Enabled {
		return false
	}
	switch auth.Mode {
	case routerAdminAuthModeGoogle, routerAdminAuthModeBoth:
		return true
	default:
		return false
	}
}

func embeddedRouterDashboardGoogleLoginURL(c *gin.Context) string {
	next := c.Request.URL.RequestURI()
	if next == "" || !strings.HasPrefix(next, "/") {
		next = embeddedRouterDashboardRoot
	}
	return "/router/v1/auth/google/login?next=" + url.QueryEscape(next)
}

func embeddedRouterDashboardPath(requestPath string) (string, bool) {
	if requestPath != embeddedRouterDashboardRoot && !strings.HasPrefix(requestPath, embeddedRouterDashboardRoot+"/") {
		return "", false
	}

	assetPath := strings.TrimPrefix(requestPath, embeddedRouterDashboardRoot)
	assetPath = strings.TrimLeft(assetPath, "/")
	if assetPath == "" {
		return "", true
	}
	for _, part := range strings.Split(assetPath, "/") {
		if part == ".." {
			return "", false
		}
	}
	assetPath = path.Clean(assetPath)
	if assetPath == "." || assetPath == "/" {
		return "", true
	}
	if !fs.ValidPath(assetPath) {
		return "", false
	}
	return assetPath, true
}

func serveEmbeddedRouterDashboardFile(c *gin.Context, name string, cacheable bool) {
	body, err := fs.ReadFile(embeddedRouterDashboardFS, name)
	if err != nil {
		c.String(http.StatusInternalServerError, "router dashboard asset unavailable")
		return
	}

	if cacheable {
		c.Header("Cache-Control", embeddedRouterDashboardCacheControl(name))
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	c.Data(http.StatusOK, embeddedRouterDashboardContentType(name, body), body)
}

func setEmbeddedRouterDashboardHeaders(c *gin.Context) {
	c.Header("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"connect-src 'self' ws: wss:",
	}, "; "))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Frame-Options", "DENY")
}

func embeddedRouterDashboardCacheControl(name string) string {
	if strings.HasPrefix(name, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

func embeddedRouterDashboardContentType(name string, body []byte) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	}

	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		return contentType
	}
	return http.DetectContentType(body)
}

func embeddedRouterDashboardLooksLikeAsset(name string) bool {
	return strings.HasPrefix(name, "assets/") || path.Ext(name) != ""
}
