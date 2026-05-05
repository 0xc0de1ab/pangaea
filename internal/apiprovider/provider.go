// Package apiprovider implements generic API-compatible provider invokers.
package apiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

var ErrAPIProviderConfig = errors.New("invalid api-compatible provider config")

type Options struct {
	Registration provider.Registration
	BaseURL      string
	Dialect      compat.APIDialect
	APIKey       string
	Headers      map[string]string
	HTTPClient   *http.Client
}

type Provider struct {
	registration provider.Registration
	baseURL      *url.URL
	dialect      compat.APIDialect
	apiKey       string
	headers      map[string]string
	client       *http.Client
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAPIProviderConfig, err)
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("%w: base url is required", ErrAPIProviderConfig)
	}
	baseURL, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("%w: base url must include scheme and host", ErrAPIProviderConfig)
	}
	if !opts.Dialect.Valid() {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, opts.Dialect)
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Provider{
		registration: opts.Registration,
		baseURL:      baseURL,
		dialect:      opts.Dialect,
		apiKey:       opts.APIKey,
		headers:      cloneHeaders(opts.Headers),
		client:       client,
	}, nil
}

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrAPIProviderConfig
	}
	return p.registration, nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if p == nil || p.client == nil {
		return compat.Response{}, ErrAPIProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrAPIProviderConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	switch p.dialect {
	case compat.APIDialectOpenAI:
		return p.invokeOpenAI(ctx, request)
	case compat.APIDialectAnthropic:
		return p.invokeAnthropic(ctx, request)
	case compat.APIDialectGemini:
		return p.invokeGemini(ctx, request)
	default:
		return compat.Response{}, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, p.dialect)
	}
}

func (p *Provider) invokeOpenAI(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.OpenAIChatRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	var upstreamResponse compat.OpenAIChatResponse
	if err := p.doJSON(ctx, http.MethodPost, "/v1/chat/completions", upstreamRequest, &upstreamResponse); err != nil {
		return compat.Response{}, err
	}
	if upstreamResponse.Model == "" {
		upstreamResponse.Model = request.Model
	}
	response, err := compat.OpenAIChatResponseToCanonical(upstreamResponse)
	if err != nil {
		return compat.Response{}, err
	}
	response.Dialect = request.Dialect
	return response, nil
}

func (p *Provider) invokeAnthropic(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.AnthropicMessagesRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	var upstreamResponse compat.AnthropicMessagesResponse
	if err := p.doJSON(ctx, http.MethodPost, "/v1/messages", upstreamRequest, &upstreamResponse); err != nil {
		return compat.Response{}, err
	}
	if upstreamResponse.Model == "" {
		upstreamResponse.Model = request.Model
	}
	response, err := compat.AnthropicMessagesResponseToCanonical(upstreamResponse)
	if err != nil {
		return compat.Response{}, err
	}
	response.Dialect = request.Dialect
	return response, nil
}

func (p *Provider) invokeGemini(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.GeminiGenerateContentRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	path := "/v1beta/models/" + url.PathEscape(request.Model) + ":generateContent"
	var upstreamResponse compat.GeminiGenerateContentResponse
	if err := p.doJSON(ctx, http.MethodPost, path, upstreamRequest, &upstreamResponse); err != nil {
		return compat.Response{}, err
	}
	if upstreamResponse.ModelVersion == "" {
		upstreamResponse.ModelVersion = request.Model
	}
	response, err := compat.GeminiGenerateContentResponseToCanonical(upstreamResponse)
	if err != nil {
		return compat.Response{}, err
	}
	response.Dialect = request.Dialect
	return response, nil
}

func (p *Provider) doJSON(ctx context.Context, method string, path string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("authorization", "Bearer "+p.apiKey)
	}
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: upstream status=%d body=%s", ErrAPIProviderConfig, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if len(responseBody) == 0 {
		return fmt.Errorf("%w: empty upstream response", ErrAPIProviderConfig)
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return err
	}
	return nil
}

func (p *Provider) endpoint(path string) string {
	u := *p.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		u.Path = path
	} else {
		u.Path = basePath + path
	}
	return u.String()
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
