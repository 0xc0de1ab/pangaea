package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardUIServesEmbeddedIndexWithSecurityHeaders(t *testing.T) {
	handler := NewHTTPHandler(HTTPOptions{})
	req := httptest.NewRequest(http.MethodGet, "/router/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("content-type"); !strings.Contains(strings.ToLower(got), "text/html") {
		t.Fatalf("expected html content type, got %q", got)
	}
	if got := rec.Header().Get("x-content-type-options"); !strings.EqualFold(got, "nosniff") {
		t.Fatalf("expected X-Content-Type-Options nosniff, got %q", got)
	}
	if got := rec.Header().Get("referrer-policy"); got == "" {
		t.Fatalf("expected Referrer-Policy header")
	}
	csp := rec.Header().Get("content-security-policy")
	if csp == "" {
		t.Fatalf("expected Content-Security-Policy header")
	}
	lowerCSP := strings.ToLower(csp)
	if !strings.Contains(lowerCSP, "default-src") || !strings.Contains(lowerCSP, "'self'") {
		t.Fatalf("expected CSP to restrict default sources to self, got %q", csp)
	}
	if !strings.Contains(lowerCSP, "img-src") || !strings.Contains(lowerCSP, "https://*.googleusercontent.com") {
		t.Fatalf("expected CSP to allow Google OAuth profile images, got %q", csp)
	}
	if !dashboardUIBlocksFraming(rec.Header()) {
		t.Fatalf("expected dashboard UI response to block framing, got X-Frame-Options=%q CSP=%q", rec.Header().Get("x-frame-options"), csp)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"<title>Pangaea Router</title>",
		`id="root"`,
		"/router/ui/assets/",
	} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Fatalf("dashboard index missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "404 page not found") {
		t.Fatalf("dashboard index returned a not-found body: %s", body)
	}
}

func TestDashboardUIRequiresGoogleSessionWhenOAuthEnabled(t *testing.T) {
	oauth := GoogleOAuthOptions{
		Enabled:       true,
		ClientID:      "client-test",
		ClientSecret:  "secret-test",
		SessionSecret: "session-secret-test",
		AllowedEmails: []string{"operator@example.test"},
	}
	handler := NewHTTPHandler(HTTPOptions{
		AdminAuth: AdminAuthOptions{
			Mode:        routerAdminAuthModeGoogle,
			GoogleOAuth: oauth,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/router/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect to Google login, got %d body=%s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("location")
	if !strings.HasPrefix(location, "/router/v1/auth/google/login?") || !strings.Contains(location, "next=%2Frouter%2Fui") {
		t.Fatalf("unexpected login redirect location: %q", location)
	}
	if strings.Contains(rec.Body.String(), "Pangaea Router") {
		t.Fatalf("dashboard index should not be served before login: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/router/ui", nil)
	req.AddCookie(testGoogleOAuthSessionCookie(t, oauth, "operator@example.test"))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected dashboard with valid Google session, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Pangaea Router") {
		t.Fatalf("dashboard index missing title after login: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/router/ui/assets/missing.js", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusFound {
		t.Fatalf("dashboard assets should not be redirected to login")
	}
}

func dashboardUIBlocksFraming(header http.Header) bool {
	xfo := strings.ToLower(strings.TrimSpace(header.Get("x-frame-options")))
	if xfo == "deny" || xfo == "sameorigin" {
		return true
	}
	csp := strings.ToLower(header.Get("content-security-policy"))
	return strings.Contains(csp, "frame-ancestors 'none'")
}
