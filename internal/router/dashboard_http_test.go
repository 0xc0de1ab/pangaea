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

func dashboardUIBlocksFraming(header http.Header) bool {
	xfo := strings.ToLower(strings.TrimSpace(header.Get("x-frame-options")))
	if xfo == "deny" || xfo == "sameorigin" {
		return true
	}
	csp := strings.ToLower(header.Get("content-security-policy"))
	return strings.Contains(csp, "frame-ancestors 'none'")
}
