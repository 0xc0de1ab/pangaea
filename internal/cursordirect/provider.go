// Package cursordirect implements an experimental HTTP JSON adapter for Cursor:
// it POSTs an OpenAI-chat-shaped JSON document to an operator-provided base URL
// and path (typically discovered via HTTP(S) proxy captures). It does not embed
// Cursor-specific protobuf contracts — callers align URL, headers, and body
// shape with their captures.
package cursordirect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const (
	defaultChatPath       = "/v1/chat/completions"
	defaultAuthHeader     = "Authorization"
	defaultAuthPrefix     = "Bearer "
	defaultUserAgent      = "pangaea-cursor-direct/0.1"
	defaultRequestTimeout = 2 * time.Minute
	usageSourceDirectHTTP = "cursor-direct-http"
	envBaseURL            = "PANGAEA_CURSOR_DIRECT_BASE_URL"
	envChatPath           = "PANGAEA_CURSOR_DIRECT_CHAT_PATH"
	envExtraHeadersJSON   = "PANGAEA_CURSOR_DIRECT_EXTRA_HEADERS_JSON"
)

var ErrConfig = errors.New("invalid cursor direct-http provider config")

type Options struct {
	Registration provider.Registration
	BaseURL      string
	ChatPath     string
	AuthHeader   string
	AuthPrefix   string
	APIKey       string
	AuthPath     string
	UserAgent    string
	HTTPClient   *http.Client
	ExtraHeaders map[string]string
}

type Provider struct {
	registration provider.Registration
	baseURL      *url.URL
	chatPath     string
	authHeader   string
	authPrefix   string
	apiKey       string
	authPath     string
	userAgent    string
	client       *http.Client
	extraHdr     map[string]string

	mu       sync.Mutex
	usage    provider.UsageReport
	healthMu sync.Mutex
	health   provider.Health
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = strings.TrimSpace(os.Getenv(envBaseURL))
	}
	if base == "" {
		return nil, fmt.Errorf("%w: base url is required (use --upstream-base-url or %s)", ErrConfig, envBaseURL)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("%w: parse base url: %v", ErrConfig, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: base url must include scheme and host", ErrConfig)
	}
	chatPath := strings.TrimSpace(opts.ChatPath)
	if chatPath == "" {
		chatPath = strings.TrimSpace(os.Getenv(envChatPath))
	}
	if chatPath == "" {
		chatPath = defaultChatPath
	}
	if !strings.HasPrefix(chatPath, "/") {
		chatPath = "/" + chatPath
	}
	authHeader := strings.TrimSpace(opts.AuthHeader)
	if authHeader == "" {
		authHeader = defaultAuthHeader
	}
	authPrefix := opts.AuthPrefix
	if strings.TrimSpace(authPrefix) == "" {
		authPrefix = defaultAuthPrefix
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultRequestTimeout}
	}
	extra := cloneHeaderMap(opts.ExtraHeaders)
	if len(extra) == 0 {
		if hdrPath := strings.TrimSpace(os.Getenv(envExtraHeadersJSON)); hdrPath != "" {
			raw, err := os.ReadFile(hdrPath)
			if err != nil {
				return nil, fmt.Errorf("%w: read %s: %v", ErrConfig, envExtraHeadersJSON, err)
			}
			if err := json.Unmarshal(raw, &extra); err != nil {
				return nil, fmt.Errorf("%w: decode extra headers json: %v", ErrConfig, err)
			}
		}
	}
	now := time.Now().UTC()
	h := opts.Registration.Health
	if strings.TrimSpace(string(h.Status)) == "" {
		h.Status = provider.HealthReady
	}
	h.CheckedAt = now
	return &Provider{
		registration: opts.Registration,
		baseURL:      parsed,
		chatPath:     chatPath,
		authHeader:   authHeader,
		authPrefix:   authPrefix,
		apiKey:       strings.TrimSpace(opts.APIKey),
		authPath:     strings.TrimSpace(opts.AuthPath),
		userAgent:    userAgent,
		client:       client,
		extraHdr:     extra,
		usage: provider.UsageReport{
			ObservedAt: now,
			Source:     usageSourceDirectHTTP,
		},
		health: h,
	}, nil
}

func cloneHeaderMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrConfig
	}
	return p.registration, nil
}

func (p *Provider) ForceModelDiscovery() bool { return false }

func (p *Provider) Models(context.Context) ([]provider.Model, error) {
	if p == nil {
		return nil, ErrConfig
	}
	out := make([]provider.Model, len(p.registration.Models))
	copy(out, p.registration.Models)
	return out, nil
}

func (p *Provider) Usage() (provider.UsageReport, error) {
	if p == nil {
		return provider.UsageReport{}, ErrConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.usage
	if u.ObservedAt.IsZero() {
		u.ObservedAt = time.Now().UTC()
	}
	if u.Source == "" {
		u.Source = usageSourceDirectHTTP
	}
	return u, nil
}

func (p *Provider) Health() (provider.Health, error) {
	if p == nil {
		return provider.Health{}, ErrConfig
	}
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	h := p.health
	if h.CheckedAt.IsZero() {
		h.CheckedAt = time.Now().UTC()
	}
	if strings.TrimSpace(string(h.Status)) == "" {
		h.Status = provider.HealthReady
	}
	return h, nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	return p.InvokeStream(ctx, registration, request, nil)
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if err := p.validateInvocation(registration, request); err != nil {
		return compat.Response{}, err
	}
	token, err := p.resolveToken()
	if err != nil {
		return compat.Response{}, err
	}

	upstream, err := compat.OpenAIChatRequestFromCanonical(request)
	if err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}
	streamUpstream := request.Stream
	upstream.Stream = streamUpstream

	payload, err := json.Marshal(upstream)
	if err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpointURL(), bytes.NewReader(payload))
	if err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", p.userAgent)
	httpReq.Header.Set(p.authHeader, p.authPrefix+token)
	if streamUpstream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	for k, v := range p.extraHdr {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		err := cursorUpstreamHTTPError(resp.StatusCode, raw)
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}

	body := bufio.NewReader(resp.Body)

	if streamUpstream && isSSEPayload(resp.Header.Get("Content-Type"), body) {
		response := compat.Response{
			ID:      request.ID,
			Dialect: request.Dialect,
			Model:   request.Model,
			Message: compat.Message{Role: compat.MessageRoleAssistant},
		}
		started := false
		if err := processSSEPayloads(body, func(data string) (bool, error) {
			return applyOpenAIStreamPayload(&response, &started, data, emit)
		}); err != nil {
			p.recordFailure()
			p.recordHealth(err)
			return compat.Response{}, err
		}
		if emit != nil && !started {
			if err := emit(compat.Event{
				ResponseID: response.ID,
				Dialect:    response.Dialect,
				Model:      response.Model,
				Type:       compat.EventMessageStart,
				Message:    &compat.Message{Role: compat.MessageRoleAssistant},
			}); err != nil {
				p.recordFailure()
				return compat.Response{}, err
			}
		}
		if response.StopReason == "" {
			response.StopReason = "stop"
		}
		if err := response.Validate(); err != nil {
			p.recordFailure()
			p.recordHealth(err)
			return compat.Response{}, err
		}
		p.recordHealth(nil)
		p.recordSuccessCompat(response.Usage)
		return response, nil
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}

	var parsed compat.OpenAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, fmt.Errorf("cursor http-direct: decode response: %w", err)
	}
	if strings.TrimSpace(parsed.Model) == "" {
		parsed.Model = request.Model
	}
	if len(parsed.Choices) == 0 {
		p.recordFailure()
		err := fmt.Errorf("cursor http-direct: empty choices")
		p.recordHealth(err)
		return compat.Response{}, err
	}
	canonical, err := compat.OpenAIChatResponseToCanonical(parsed)
	if err != nil {
		p.recordFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}
	canonical.ID = request.ID
	canonical.Dialect = request.Dialect
	canonical.Model = request.Model

	if emit != nil {
		events, err := compat.EventsFromResponse(canonical)
		if err != nil {
			p.recordFailure()
			p.recordHealth(err)
			return compat.Response{}, err
		}
		for _, ev := range events {
			if err := emit(ev); err != nil {
				p.recordFailure()
				return compat.Response{}, err
			}
		}
	}

	p.recordHealth(nil)
	p.recordSuccessCompat(compatUsageFromOpenAI(parsed.Usage))
	return canonical, nil
}

func (p *Provider) validateInvocation(registration provider.Registration, request compat.Request) error {
	if p == nil {
		return ErrConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return fmt.Errorf("%w: provider instance mismatch", ErrConfig)
	}
	if err := request.Validate(); err != nil {
		return err
	}
	return nil
}

func (p *Provider) chatEndpointURL() string {
	endpoint := *p.baseURL
	basePath := strings.TrimSuffix(endpoint.Path, "/")
	chat := strings.TrimPrefix(p.chatPath, "/")
	if basePath == "" {
		endpoint.Path = "/" + chat
	} else {
		endpoint.Path = basePath + "/" + chat
	}
	return endpoint.String()
}

func isSSEPayload(contentType string, body *bufio.Reader) bool {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return true
	}
	peek, err := body.Peek(32)
	if err != nil && len(peek) == 0 {
		return false
	}
	trimmed := strings.TrimLeft(string(peek), "\ufeff \t\r\n")
	return strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "event:")
}

func cursorUpstreamHTTPError(status int, body []byte) error {
	return &provider.UpstreamError{
		StatusCode: status,
		Message:    fmt.Sprintf("cursor http-direct: upstream HTTP %d", status),
		Body:       truncate(string(body), 512),
	}
}

func compatUsageFromOpenAI(u *compat.OpenAIUsage) compat.Usage {
	if u == nil {
		return compat.Usage{}
	}
	out := compat.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func (p *Provider) resolveToken() (string, error) {
	if p.apiKey != "" {
		return p.apiKey, nil
	}
	if v := strings.TrimSpace(os.Getenv("CURSOR_API_KEY")); v != "" {
		return v, nil
	}
	if p.authPath != "" {
		raw, err := os.ReadFile(p.authPath)
		if err != nil {
			return "", fmt.Errorf("cursor http-direct: read auth path: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if idx := strings.IndexAny(token, "\r\n"); idx >= 0 {
			token = strings.TrimSpace(token[:idx])
		}
		if token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("%w: missing API token (CURSOR_API_KEY, --upstream-api-key, or auth file)", ErrConfig)
}

func (p *Provider) recordFailure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage.ObservedAt = time.Now().UTC()
	p.usage.Source = usageSourceDirectHTTP
}

func (p *Provider) recordSuccessCompat(u compat.Usage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage.ObservedAt = time.Now().UTC()
	p.usage.Source = usageSourceDirectHTTP
	p.usage.Requests++
	p.usage.InputTokens += u.InputTokens
	p.usage.OutputTokens += u.OutputTokens
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	p.usage.TotalTokens += total
}

func (p *Provider) recordHealth(invokeErr error) {
	if p == nil {
		return
	}
	now := time.Now().UTC()
	health := provider.Health{Status: provider.HealthReady, CheckedAt: now}
	if invokeErr != nil {
		if errors.Is(invokeErr, context.Canceled) || errors.Is(invokeErr, context.DeadlineExceeded) {
			return
		}
		var upstream *provider.UpstreamError
		if errors.As(invokeErr, &upstream) {
			switch upstream.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				health.Status = provider.HealthDown
				health.Reason = "upstream auth failed"
			case http.StatusTooManyRequests:
				health.Status = provider.HealthDegraded
				health.Reason = "upstream rate limited"
			case http.StatusBadRequest, http.StatusNotFound:
				return
			default:
				health.Status = provider.HealthDegraded
				health.Reason = "upstream request failed"
			}
		} else {
			health.Status = provider.HealthDegraded
			health.Reason = "provider invoke failed"
		}
	}
	p.healthMu.Lock()
	p.health = health
	p.healthMu.Unlock()
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
