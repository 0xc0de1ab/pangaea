package antigravity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var ErrRefresh = errors.New("antigravity auth refresh failed")

type RefreshOptions struct {
	BaseURL          string
	APIKey           string
	APIKeyFile       string
	APIKeyMode       string
	APIKeyHeader     string
	APIKeyQueryParam string
	AuthPath         string
	Format           formats.Format
	HTTPClient       *http.Client
	Timeout          time.Duration
	Now              func() time.Time
}

type AuthRefresher struct {
	baseURL          *url.URL
	apiKey           string
	apiKeyFile       string
	apiKeyMode       string
	apiKeyHeader     string
	apiKeyQueryParam string
	authPath         string
	format           formats.Format
	client           *http.Client
	timeout          time.Duration
	now              func() time.Time
}

func NewAuthRefresher(opts RefreshOptions) (*AuthRefresher, error) {
	baseURL, err := url.Parse(strings.TrimSpace(opts.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base url: %v", ErrRefresh, err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("%w: base url must include scheme and host", ErrRefresh)
	}
	mode, header, queryParam, err := normalizeAPIKey(opts.APIKeyMode, opts.APIKeyHeader, opts.APIKeyQueryParam)
	if err != nil {
		return nil, err
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &AuthRefresher{
		baseURL:          baseURL,
		apiKey:           strings.TrimSpace(opts.APIKey),
		apiKeyFile:       strings.TrimSpace(opts.APIKeyFile),
		apiKeyMode:       mode,
		apiKeyHeader:     header,
		apiKeyQueryParam: queryParam,
		authPath:         strings.TrimSpace(opts.AuthPath),
		format:           opts.Format,
		client:           client,
		timeout:          timeout,
		now:              now,
	}, nil
}

func (r *AuthRefresher) RefreshAuth(ctx context.Context, request control.AuthRefreshRequest, registration provider.Registration) (provider.AuthState, error) {
	if r == nil {
		return registration.Auth, fmt.Errorf("%w: refresher is nil", ErrRefresh)
	}
	ctx, cancel := r.refreshContext(ctx, request)
	defer cancel()
	if err := r.triggerLSCoreRefresh(ctx); err != nil {
		auth := registration.Auth
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshAt = r.now().UTC()
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	auth := registration.Auth
	auth.Status = provider.AuthHealthy
	auth.LastRefreshAt = r.now().UTC()
	auth.LastRefreshErr = ""
	auth.Refreshable = true
	if r.authPath == "" || r.format == nil {
		return auth, nil
	}
	return r.authStateFromFile(ctx, auth)
}

func (r *AuthRefresher) refreshContext(ctx context.Context, request control.AuthRefreshRequest) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !request.DeadlineAt.IsZero() {
		if deadline, ok := ctx.Deadline(); !ok || request.DeadlineAt.Before(deadline) {
			return context.WithDeadline(ctx, request.DeadlineAt)
		}
	}
	if r.timeout > 0 {
		return context.WithTimeout(ctx, r.timeout)
	}
	return ctx, func() {}
}

func (r *AuthRefresher) triggerLSCoreRefresh(ctx context.Context) error {
	apiKey, err := r.apiKeyForRequest()
	if err != nil {
		return err
	}
	paths := []string{"/v1/account", "/v1/models/status"}
	var lastErr error
	success := false
	for _, path := range paths {
		if err := r.get(ctx, path, apiKey); err != nil {
			var statusErr *statusError
			if errors.As(err, &statusErr) && (statusErr.status == http.StatusUnauthorized || statusErr.status == http.StatusForbidden) {
				return err
			}
			lastErr = err
			continue
		}
		success = true
	}
	if success {
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("%w: no refresh probes were attempted", ErrRefresh)
}

func (r *AuthRefresher) get(ctx context.Context, path string, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint(path, apiKey), nil)
	if err != nil {
		return fmt.Errorf("%w: build %s request: %v", ErrRefresh, path, err)
	}
	req.Header.Set("accept", "application/json")
	if err := r.applyAPIKeyHeader(req, apiKey); err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: probe %s: %v", ErrRefresh, path, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{path: path, status: resp.StatusCode}
	}
	return nil
}

func (r *AuthRefresher) endpoint(path string, apiKey string) string {
	u := *r.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		u.Path = path
	} else {
		u.Path = basePath + path
	}
	if apiKey != "" && r.apiKeyMode == "query" {
		q := u.Query()
		q.Set(r.apiKeyQueryParam, apiKey)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (r *AuthRefresher) apiKeyForRequest() (string, error) {
	if r.apiKeyFile == "" {
		return r.apiKey, nil
	}
	data, err := os.ReadFile(r.apiKeyFile)
	if err != nil {
		return "", fmt.Errorf("%w: read api key file: %v", ErrRefresh, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (r *AuthRefresher) applyAPIKeyHeader(req *http.Request, apiKey string) error {
	if apiKey == "" || r.apiKeyMode == "query" || r.apiKeyMode == "none" {
		return nil
	}
	switch r.apiKeyMode {
	case "bearer":
		req.Header.Set(r.apiKeyHeader, "Bearer "+apiKey)
	case "header":
		req.Header.Set(r.apiKeyHeader, apiKey)
	default:
		return fmt.Errorf("%w: unsupported api key mode %q", ErrRefresh, r.apiKeyMode)
	}
	return nil
}

func (r *AuthRefresher) authStateFromFile(ctx context.Context, auth provider.AuthState) (provider.AuthState, error) {
	raw, err := os.ReadFile(r.authPath)
	if err != nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, fmt.Errorf("%w: read refreshed auth: %v", ErrRefresh, err)
	}
	snapshot, err := r.format.Parse(raw)
	if err != nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	result, err := r.format.Validate(ctx, snapshot, formats.ValidateOpts{Clock: r.now})
	if err != nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	auth.Status = authStatusFromValidation(result.Status)
	auth.ExpiresAt = snapshot.ExpiresAt()
	auth.SelectedSource = "container"
	auth.BootstrapSource = "copy"
	auth.LastRefreshAt = r.now().UTC()
	auth.LastRefreshErr = ""
	auth.Refreshable = true
	if accountAware, ok := r.format.(formats.AccountAware); ok {
		if id, err := accountAware.Account(ctx, snapshot, r.authPath); err == nil {
			auth.Account.ID = id
		}
	}
	if displayAware, ok := r.format.(formats.AccountDisplayAware); ok {
		if display, err := displayAware.AccountDisplay(ctx, snapshot, r.authPath); err == nil {
			auth.Account.Display = display
		}
	}
	if auth.Status == provider.AuthExpired || auth.Status == provider.AuthRevoked || auth.Status == provider.AuthUnavailable {
		if result.Detail != "" {
			auth.LastRefreshErr = result.Detail
		}
		return auth, fmt.Errorf("%w: refreshed auth status %s", ErrRefresh, result.Status)
	}
	return auth, nil
}

func authStatusFromValidation(status formats.ValidationStatus) provider.AuthStatus {
	switch status {
	case formats.StatusOK:
		return provider.AuthHealthy
	case formats.StatusScopeWarn:
		return provider.AuthConflict
	case formats.StatusExpired:
		return provider.AuthExpired
	case formats.StatusRevoked:
		return provider.AuthRevoked
	case formats.StatusUnreachable:
		return provider.AuthUnavailable
	default:
		return provider.AuthUnavailable
	}
}

func normalizeAPIKey(mode string, header string, queryParam string) (string, string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	header = strings.TrimSpace(header)
	queryParam = strings.TrimSpace(queryParam)
	if mode == "" {
		mode = "bearer"
	}
	switch mode {
	case "bearer":
		if header == "" {
			header = "authorization"
		}
	case "header":
		if header == "" {
			return "", "", "", fmt.Errorf("%w: api key header is required for header mode", ErrRefresh)
		}
	case "query":
		if queryParam == "" {
			return "", "", "", fmt.Errorf("%w: api key query param is required for query mode", ErrRefresh)
		}
	case "none":
	default:
		return "", "", "", fmt.Errorf("%w: unsupported api key mode %q", ErrRefresh, mode)
	}
	return mode, header, queryParam, nil
}

type statusError struct {
	path   string
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%v: probe %s returned status %d", ErrRefresh, e.path, e.status)
}
