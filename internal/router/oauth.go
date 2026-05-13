package router

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultGoogleOAuthAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleOAuthTokenURL     = "https://oauth2.googleapis.com/token"
	defaultGoogleOAuthTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"
	defaultGoogleOAuthScope        = "openid email profile"
	defaultGoogleOAuthCookieName   = "pangaea_router_session"
	defaultGoogleOAuthStateCookie  = "pangaea_router_oauth_state"
	defaultGoogleOAuthSessionTTL   = 12 * time.Hour
	defaultGoogleOAuthStateTTL     = 10 * time.Minute
	routerAdminSessionContextKey   = "router_admin_google_session"
	routerAdminAuthModeAuto        = "auto"
	routerAdminAuthModeOpen        = "open"
	routerAdminAuthModeBearer      = "bearer"
	routerAdminAuthModeGoogle      = "google"
	routerAdminAuthModeBoth        = "both"
)

type AdminAuthOptions struct {
	Mode        string
	GoogleOAuth GoogleOAuthOptions
}

type GoogleOAuthOptions struct {
	Enabled        bool
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	AllowedEmails  []string
	AllowedDomains []string
	SessionSecret  string
	CookieName     string
	CookieSecure   bool
	SessionTTL     time.Duration
	AuthURL        string
	TokenURL       string
	TokenInfoURL   string
	HTTPClient     *http.Client
}

type GoogleOAuthSession struct {
	Provider      string `json:"provider"`
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name,omitempty"`
	Picture       string `json:"picture,omitempty"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
}

type googleOAuthState struct {
	State       string `json:"state"`
	RedirectURL string `json:"redirect_url"`
	Next        string `json:"next,omitempty"`
	ExpiresAt   int64  `json:"exp"`
}

type googleOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

type googleOAuthTokenInfo struct {
	Audience      string `json:"aud"`
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	ExpiresIn     string `json:"expires_in"`
	Error         string `json:"error"`
	Description   string `json:"error_description"`
}

func normalizeAdminAuthOptions(opts AdminAuthOptions, _ bool) AdminAuthOptions {
	opts.Mode = strings.ToLower(strings.TrimSpace(opts.Mode))
	if opts.Mode == "" {
		opts.Mode = routerAdminAuthModeAuto
	}
	opts.GoogleOAuth = normalizeGoogleOAuthOptions(opts.GoogleOAuth)
	if opts.GoogleOAuth.Enabled && opts.Mode == routerAdminAuthModeBoth {
		opts.Mode = routerAdminAuthModeGoogle
	}
	if opts.Mode != routerAdminAuthModeAuto {
		return opts
	}
	switch {
	case opts.GoogleOAuth.Enabled:
		opts.Mode = routerAdminAuthModeGoogle
	default:
		opts.Mode = routerAdminAuthModeBearer
	}
	return opts
}

func normalizeGoogleOAuthOptions(opts GoogleOAuthOptions) GoogleOAuthOptions {
	opts.ClientID = strings.TrimSpace(opts.ClientID)
	opts.ClientSecret = strings.TrimSpace(opts.ClientSecret)
	opts.RedirectURL = strings.TrimSpace(opts.RedirectURL)
	opts.SessionSecret = strings.TrimSpace(opts.SessionSecret)
	opts.CookieName = strings.TrimSpace(opts.CookieName)
	if opts.CookieName == "" {
		opts.CookieName = defaultGoogleOAuthCookieName
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = defaultGoogleOAuthSessionTTL
	}
	if opts.AuthURL == "" {
		opts.AuthURL = defaultGoogleOAuthAuthURL
	}
	if opts.TokenURL == "" {
		opts.TokenURL = defaultGoogleOAuthTokenURL
	}
	if opts.TokenInfoURL == "" {
		opts.TokenInfoURL = defaultGoogleOAuthTokenInfoURL
	}
	opts.AllowedEmails = normalizeEmailList(opts.AllowedEmails)
	opts.AllowedDomains = normalizeDomainList(opts.AllowedDomains)
	return opts
}

func validateAdminAuthOptions(opts AdminAuthOptions) error {
	switch opts.Mode {
	case routerAdminAuthModeAuto, routerAdminAuthModeOpen, routerAdminAuthModeBearer, routerAdminAuthModeGoogle, routerAdminAuthModeBoth:
	default:
		return fmt.Errorf("invalid admin auth mode %q", opts.Mode)
	}
	if !opts.GoogleOAuth.Enabled {
		return nil
	}
	if opts.GoogleOAuth.ClientID == "" {
		return fmt.Errorf("google oauth client id is required")
	}
	if opts.GoogleOAuth.ClientSecret == "" {
		return fmt.Errorf("google oauth client secret is required")
	}
	if opts.GoogleOAuth.SessionSecret == "" {
		return fmt.Errorf("google oauth session secret is required")
	}
	if len(opts.GoogleOAuth.AllowedEmails) == 0 && len(opts.GoogleOAuth.AllowedDomains) == 0 {
		return fmt.Errorf("at least one google oauth allowed email or domain is required")
	}
	return nil
}

func ValidateAdminAuthOptions(opts AdminAuthOptions) error {
	opts.Mode = strings.ToLower(strings.TrimSpace(opts.Mode))
	opts.GoogleOAuth = normalizeGoogleOAuthOptions(opts.GoogleOAuth)
	return validateAdminAuthOptions(opts)
}

func registerGoogleOAuthRoutes(r *gin.Engine, opts AdminAuthOptions) {
	oauth := normalizeGoogleOAuthOptions(opts.GoogleOAuth)
	r.GET("/router/v1/session", func(c *gin.Context) {
		session, ok := authenticateGoogleOAuthSession(c, oauth)
		if ok {
			c.JSON(http.StatusOK, gin.H{
				"authenticated": true,
				"mode":          opts.Mode,
				"user":          session,
				"google_oauth":  googleOAuthPublicConfig(oauth),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"authenticated": false,
			"mode":          opts.Mode,
			"google_oauth":  googleOAuthPublicConfig(oauth),
		})
	})
	r.DELETE("/router/v1/session", func(c *gin.Context) {
		clearGoogleOAuthCookie(c, oauth.CookieName, "/", oauth.CookieSecure)
		clearGoogleOAuthCookie(c, defaultGoogleOAuthStateCookie, "/router/v1/auth/google/callback", oauth.CookieSecure)
		c.Status(http.StatusNoContent)
	})
	r.GET("/router/v1/auth/google/login", func(c *gin.Context) {
		if !oauth.Enabled {
			c.JSON(http.StatusNotFound, gin.H{"error": "google oauth is not enabled"})
			return
		}
		redirectURL := oauth.RedirectURL
		if redirectURL == "" {
			redirectURL = inferGoogleOAuthRedirectURL(c)
		}
		state, err := randomOAuthToken(24)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "generate oauth state"})
			return
		}
		next := sanitizeOAuthNext(c.Query("next"))
		statePayload := googleOAuthState{
			State:       state,
			RedirectURL: redirectURL,
			Next:        next,
			ExpiresAt:   time.Now().Add(defaultGoogleOAuthStateTTL).Unix(),
		}
		signed, err := signJSONPayload([]byte(oauth.SessionSecret), statePayload)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "sign oauth state"})
			return
		}
		setGoogleOAuthCookie(c, defaultGoogleOAuthStateCookie, signed, "/router/v1/auth/google/callback", oauth.CookieSecure, defaultGoogleOAuthStateTTL)
		authURL, err := googleOAuthAuthorizationURL(oauth, redirectURL, state)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, authURL)
	})
	r.GET("/router/v1/auth/google/callback", func(c *gin.Context) {
		if !oauth.Enabled {
			c.JSON(http.StatusNotFound, gin.H{"error": "google oauth is not enabled"})
			return
		}
		statePayload, ok := googleOAuthStateFromCookie(c, oauth)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid oauth state"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(statePayload.State), []byte(c.Query("state"))) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "oauth state mismatch"})
			return
		}
		if errMsg := strings.TrimSpace(c.Query("error")); errMsg != "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": errMsg})
			return
		}
		code := strings.TrimSpace(c.Query("code"))
		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing oauth code"})
			return
		}
		token, err := exchangeGoogleOAuthCode(c.Request.Context(), oauth, code, statePayload.RedirectURL)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		info, err := fetchGoogleOAuthTokenInfo(c.Request.Context(), oauth, token.IDToken)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		session, err := sessionFromGoogleTokenInfo(oauth, info)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		signed, err := signJSONPayload([]byte(oauth.SessionSecret), session)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "sign session"})
			return
		}
		setGoogleOAuthCookie(c, oauth.CookieName, signed, "/", oauth.CookieSecure, oauth.SessionTTL)
		clearGoogleOAuthCookie(c, defaultGoogleOAuthStateCookie, "/router/v1/auth/google/callback", oauth.CookieSecure)
		c.Redirect(http.StatusFound, statePayload.Next)
	})
}

func googleOAuthPublicConfig(opts GoogleOAuthOptions) gin.H {
	return gin.H{
		"enabled":   opts.Enabled,
		"client_id": opts.ClientID,
	}
}

func googleOAuthAuthorizationURL(opts GoogleOAuthOptions, redirectURL string, state string) (string, error) {
	baseURL, err := url.Parse(opts.AuthURL)
	if err != nil {
		return "", err
	}
	query := baseURL.Query()
	query.Set("client_id", opts.ClientID)
	query.Set("redirect_uri", redirectURL)
	query.Set("response_type", "code")
	query.Set("scope", defaultGoogleOAuthScope)
	query.Set("state", state)
	query.Set("prompt", "select_account")
	baseURL.RawQuery = query.Encode()
	return baseURL.String(), nil
}

func exchangeGoogleOAuthCode(ctx context.Context, opts GoogleOAuthOptions, code string, redirectURL string) (googleOAuthTokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", opts.ClientID)
	form.Set("client_secret", opts.ClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("accept", "application/json")
	resp, err := httpClient(opts).Do(req)
	if err != nil {
		return googleOAuthTokenResponse{}, err
	}
	var token googleOAuthTokenResponse
	if err := decodeJSONResponse(resp, &token); err != nil {
		return googleOAuthTokenResponse{}, err
	}
	if token.Error != "" {
		return googleOAuthTokenResponse{}, fmt.Errorf("google oauth token exchange failed: %s", token.Error)
	}
	if token.IDToken == "" {
		return googleOAuthTokenResponse{}, fmt.Errorf("google oauth token exchange did not return id_token")
	}
	return token, nil
}

func fetchGoogleOAuthTokenInfo(ctx context.Context, opts GoogleOAuthOptions, idToken string) (googleOAuthTokenInfo, error) {
	if strings.TrimSpace(idToken) == "" {
		return googleOAuthTokenInfo{}, fmt.Errorf("google id_token is required")
	}
	endpoint, err := url.Parse(opts.TokenInfoURL)
	if err != nil {
		return googleOAuthTokenInfo{}, err
	}
	query := endpoint.Query()
	query.Set("id_token", idToken)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return googleOAuthTokenInfo{}, err
	}
	req.Header.Set("accept", "application/json")
	resp, err := httpClient(opts).Do(req)
	if err != nil {
		return googleOAuthTokenInfo{}, err
	}
	var info googleOAuthTokenInfo
	if err := decodeJSONResponse(resp, &info); err != nil {
		return googleOAuthTokenInfo{}, err
	}
	if info.Error != "" {
		return googleOAuthTokenInfo{}, fmt.Errorf("google tokeninfo rejected id token: %s", info.Error)
	}
	return info, nil
}

func sessionFromGoogleTokenInfo(opts GoogleOAuthOptions, info googleOAuthTokenInfo) (GoogleOAuthSession, error) {
	if info.Error != "" {
		return GoogleOAuthSession{}, fmt.Errorf("google tokeninfo rejected id token: %s", info.Error)
	}
	if info.Audience != opts.ClientID {
		return GoogleOAuthSession{}, fmt.Errorf("google token audience mismatch")
	}
	email := strings.ToLower(strings.TrimSpace(info.Email))
	if email == "" {
		return GoogleOAuthSession{}, fmt.Errorf("google account email is required")
	}
	verified := googleOAuthEmailVerified(info.EmailVerified)
	if !verified {
		return GoogleOAuthSession{}, fmt.Errorf("google account email is not verified")
	}
	if !googleOAuthEmailAllowed(email, opts.AllowedEmails, opts.AllowedDomains) {
		return GoogleOAuthSession{}, fmt.Errorf("google account %s is not allowed", email)
	}
	now := time.Now().UTC()
	return GoogleOAuthSession{
		Provider:      "google",
		Subject:       strings.TrimSpace(info.Subject),
		Email:         email,
		EmailVerified: verified,
		Name:          strings.TrimSpace(info.Name),
		Picture:       strings.TrimSpace(info.Picture),
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(opts.SessionTTL).Unix(),
	}, nil
}

func googleOAuthEmailVerified(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
	default:
		return false
	}
}

func googleOAuthEmailAllowed(email string, allowedEmails []string, allowedDomains []string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if slices.Contains(allowedEmails, email) {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	return slices.Contains(allowedDomains, strings.ToLower(strings.TrimSpace(domain)))
}

func authenticateGoogleOAuthSession(c *gin.Context, opts GoogleOAuthOptions) (GoogleOAuthSession, bool) {
	if !opts.Enabled {
		return GoogleOAuthSession{}, false
	}
	raw, err := c.Cookie(opts.CookieName)
	if err != nil || strings.TrimSpace(raw) == "" {
		return GoogleOAuthSession{}, false
	}
	var session GoogleOAuthSession
	if err := verifySignedJSONPayload([]byte(opts.SessionSecret), raw, &session); err != nil {
		return GoogleOAuthSession{}, false
	}
	if session.Provider != "google" || session.Email == "" || session.ExpiresAt <= time.Now().Unix() {
		return GoogleOAuthSession{}, false
	}
	if !googleOAuthEmailAllowed(session.Email, opts.AllowedEmails, opts.AllowedDomains) {
		return GoogleOAuthSession{}, false
	}
	return session, true
}

func googleOAuthStateFromCookie(c *gin.Context, opts GoogleOAuthOptions) (googleOAuthState, bool) {
	raw, err := c.Cookie(defaultGoogleOAuthStateCookie)
	if err != nil || strings.TrimSpace(raw) == "" {
		return googleOAuthState{}, false
	}
	var state googleOAuthState
	if err := verifySignedJSONPayload([]byte(opts.SessionSecret), raw, &state); err != nil {
		return googleOAuthState{}, false
	}
	if state.State == "" || state.RedirectURL == "" || state.ExpiresAt <= time.Now().Unix() {
		return googleOAuthState{}, false
	}
	if state.Next == "" {
		state.Next = "/router/ui"
	}
	return state, true
}

func signJSONPayload(secret []byte, payload any) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("signing secret is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payloadPart))
	signature := mac.Sum(nil)
	return payloadPart + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func verifySignedJSONPayload(secret []byte, signed string, out any) error {
	parts := strings.Split(signed, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid signed payload")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	if !hmac.Equal(signature, expected) {
		return fmt.Errorf("invalid signed payload signature")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func setGoogleOAuthCookie(c *gin.Context, name string, value string, path string, secure bool, ttl time.Duration) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(ttl.Seconds()),
		Expires:  time.Now().Add(ttl),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearGoogleOAuthCookie(c *gin.Context, name string, path string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomOAuthToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func normalizeEmailList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || slices.Contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func normalizeDomainList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "@")
		if value == "" || slices.Contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func sanitizeOAuthNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return "/router/ui"
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.IsAbs() || strings.HasPrefix(next, "//") {
		return "/router/ui"
	}
	if !strings.HasPrefix(next, "/") {
		return "/router/ui"
	}
	return next
}

func inferGoogleOAuthRedirectURL(c *gin.Context) string {
	scheme := strings.TrimSpace(c.GetHeader("x-forwarded-proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(c.GetHeader("x-forwarded-host"))
	if host == "" {
		host = c.Request.Host
	}
	return scheme + "://" + host + "/router/v1/auth/google/callback"
}

func httpClient(opts GoogleOAuthOptions) *http.Client {
	if opts.HTTPClient != nil {
		return opts.HTTPClient
	}
	return http.DefaultClient
}

func decodeJSONResponse(resp *http.Response, out any) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("google oauth request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return err
	}
	return nil
}
