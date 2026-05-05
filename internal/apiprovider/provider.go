// Package apiprovider implements generic API-compatible provider invokers.
package apiprovider

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

var ErrAPIProviderConfig = errors.New("invalid api-compatible provider config")

type Options struct {
	Registration     provider.Registration
	BaseURL          string
	Dialect          compat.APIDialect
	APIKey           string
	APIKeyFile       string
	APIKeyMode       string
	APIKeyHeader     string
	APIKeyQueryParam string
	Headers          map[string]string
	HTTPClient       *http.Client
}

type Provider struct {
	registration     provider.Registration
	baseURL          *url.URL
	dialect          compat.APIDialect
	apiKey           string
	apiKeyFile       string
	apiKeyMode       string
	apiKeyHeader     string
	apiKeyQueryParam string
	headers          map[string]string
	client           *http.Client
	usageMu          sync.Mutex
	usage            provider.UsageReport
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
	apiKeyMode, apiKeyHeader, apiKeyQueryParam, err := normalizeAPIKeyAuth(opts.APIKeyMode, opts.APIKeyHeader, opts.APIKeyQueryParam)
	if err != nil {
		return nil, err
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Provider{
		registration:     opts.Registration,
		baseURL:          baseURL,
		dialect:          opts.Dialect,
		apiKey:           strings.TrimSpace(opts.APIKey),
		apiKeyFile:       strings.TrimSpace(opts.APIKeyFile),
		apiKeyMode:       apiKeyMode,
		apiKeyHeader:     apiKeyHeader,
		apiKeyQueryParam: apiKeyQueryParam,
		headers:          cloneHeaders(opts.Headers),
		client:           client,
		usage: provider.UsageReport{
			ObservedAt: time.Now().UTC(),
			Source:     "api-compatible",
		},
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
		response, err := p.invokeOpenAI(ctx, request)
		p.recordUsage(response.Usage, err)
		return response, err
	case compat.APIDialectAnthropic:
		response, err := p.invokeAnthropic(ctx, request)
		p.recordUsage(response.Usage, err)
		return response, err
	case compat.APIDialectGemini:
		response, err := p.invokeGemini(ctx, request)
		p.recordUsage(response.Usage, err)
		return response, err
	default:
		return compat.Response{}, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, p.dialect)
	}
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if p == nil || p.client == nil {
		return compat.Response{}, ErrAPIProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrAPIProviderConfig)
	}
	if emit == nil {
		return compat.Response{}, fmt.Errorf("%w: stream emit callback is required", ErrAPIProviderConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	switch p.dialect {
	case compat.APIDialectOpenAI:
		response, err := p.invokeOpenAIStream(ctx, request, emit)
		p.recordUsage(response.Usage, err)
		return response, err
	case compat.APIDialectAnthropic:
		response, err := p.invokeAnthropic(ctx, request)
		if err == nil {
			err = emitEventsFromResponse(response, emit)
		}
		p.recordUsage(response.Usage, err)
		return response, err
	case compat.APIDialectGemini:
		response, err := p.invokeGemini(ctx, request)
		if err == nil {
			err = emitEventsFromResponse(response, emit)
		}
		p.recordUsage(response.Usage, err)
		return response, err
	default:
		return compat.Response{}, fmt.Errorf("%w: unsupported dialect %q", ErrAPIProviderConfig, p.dialect)
	}
}

func (p *Provider) Usage() (provider.UsageReport, error) {
	if p == nil {
		return provider.UsageReport{}, ErrAPIProviderConfig
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	usage := p.usage
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	if usage.Source == "" {
		usage.Source = "api-compatible"
	}
	return usage, nil
}

func (p *Provider) invokeOpenAI(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.OpenAIChatRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = false
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

type openAIChatStreamChunk struct {
	ID      string                     `json:"id,omitempty"`
	Model   string                     `json:"model,omitempty"`
	Choices []openAIChatStreamChoice   `json:"choices,omitempty"`
	Usage   *compat.OpenAIUsage        `json:"usage,omitempty"`
	Error   *openAIChatStreamErrorBody `json:"error,omitempty"`
}

type openAIChatStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        openAIChatStreamDelta `json:"delta"`
	FinishReason string                `json:"finish_reason,omitempty"`
}

type openAIChatStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIChatStreamErrorBody struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func (p *Provider) invokeOpenAIStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	upstreamRequest, err := compat.OpenAIChatRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = true
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1/chat/completions", upstreamRequest, "text/event-stream")
	if err != nil {
		return compat.Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return compat.Response{}, readErr
		}
		return compat.Response{}, fmt.Errorf("%w: upstream status=%d body=%s", ErrAPIProviderConfig, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	response := compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant},
	}
	started := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if len(dataLines) > 0 {
				if done, err := applyOpenAIStreamPayload(&response, &started, strings.Join(dataLines, "\n"), emit); err != nil {
					return compat.Response{}, err
				} else if done {
					break
				}
				dataLines = dataLines[:0]
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return compat.Response{}, err
	}
	if len(dataLines) > 0 {
		if _, err := applyOpenAIStreamPayload(&response, &started, strings.Join(dataLines, "\n"), emit); err != nil {
			return compat.Response{}, err
		}
	}
	if !started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return compat.Response{}, err
		}
	}
	if response.StopReason == "" {
		response.StopReason = "stop"
	}
	if err := response.Validate(); err != nil {
		return compat.Response{}, err
	}
	return response, nil
}

func applyOpenAIStreamPayload(response *compat.Response, started *bool, payload string, emit func(compat.Event) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		return true, nil
	}
	var chunk openAIChatStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, err
	}
	if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
		errEvent := compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventError,
			Error:      &compat.EventErrorPayload{Message: chunk.Error.Message, Code: stringFromAny(chunk.Error.Code)},
		}
		if err := emit(errEvent); err != nil {
			return false, err
		}
		return false, fmt.Errorf("%w: upstream stream error: %s", ErrAPIProviderConfig, chunk.Error.Message)
	}
	if chunk.ID != "" {
		response.ID = chunk.ID
	}
	if chunk.Model != "" {
		response.Model = chunk.Model
	}
	if !*started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return false, err
		}
		*started = true
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			part := compat.ContentPart{Type: compat.ContentPartText, Text: choice.Delta.Content}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
		}
		if choice.FinishReason != "" {
			response.StopReason = choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		usage := compat.Usage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens:  chunk.Usage.TotalTokens,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
			return false, err
		}
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
			return false, err
		}
	}
	if response.StopReason != "" {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventDone, DoneReason: response.StopReason}); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (p *Provider) invokeAnthropic(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := compat.AnthropicMessagesRequestFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	upstreamRequest.Stream = false
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
	resp, err := p.doRequest(ctx, method, path, body, "application/json")
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

func (p *Provider) doRequest(ctx context.Context, method string, path string, body any, accept string) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	apiKey, err := p.apiKeyForRequest()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path, apiKey), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if accept != "" {
		req.Header.Set("accept", accept)
	}
	if err := p.applyAPIKeyHeader(req, apiKey); err != nil {
		return nil, err
	}
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	return p.client.Do(req)
}

func (p *Provider) apiKeyForRequest() (string, error) {
	if p.apiKeyFile == "" {
		return p.apiKey, nil
	}
	data, err := os.ReadFile(p.apiKeyFile)
	if err != nil {
		return "", fmt.Errorf("%w: read api key file: %v", ErrAPIProviderConfig, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (p *Provider) endpoint(path string, apiKey string) string {
	u := *p.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		u.Path = path
	} else {
		u.Path = basePath + path
	}
	if apiKey != "" && p.apiKeyMode == "query" {
		q := u.Query()
		q.Set(p.apiKeyQueryParam, apiKey)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (p *Provider) applyAPIKeyHeader(req *http.Request, apiKey string) error {
	if apiKey == "" || p.apiKeyMode == "query" || p.apiKeyMode == "none" {
		return nil
	}
	switch p.apiKeyMode {
	case "bearer":
		req.Header.Set(p.apiKeyHeader, "Bearer "+apiKey)
	case "header":
		req.Header.Set(p.apiKeyHeader, apiKey)
	default:
		return fmt.Errorf("%w: unsupported api key mode %q", ErrAPIProviderConfig, p.apiKeyMode)
	}
	return nil
}

func normalizeAPIKeyAuth(mode string, header string, queryParam string) (string, string, string, error) {
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
			return "", "", "", fmt.Errorf("%w: api key header is required for header mode", ErrAPIProviderConfig)
		}
	case "query":
		if queryParam == "" {
			return "", "", "", fmt.Errorf("%w: api key query param is required for query mode", ErrAPIProviderConfig)
		}
	case "none":
	default:
		return "", "", "", fmt.Errorf("%w: unsupported api key mode %q", ErrAPIProviderConfig, mode)
	}
	return mode, header, queryParam, nil
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

func (p *Provider) recordUsage(usage compat.Usage, invokeErr error) {
	if p == nil || invokeErr != nil {
		return
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	if p.usage.Source == "" {
		p.usage.Source = "api-compatible"
	}
	p.usage.Requests++
	p.usage.InputTokens += usage.InputTokens
	p.usage.OutputTokens += usage.OutputTokens
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	p.usage.TotalTokens += total
	p.usage.ObservedAt = time.Now().UTC()
}

func emitEventsFromResponse(response compat.Response, emit func(compat.Event) error) error {
	events, err := compat.EventsFromResponse(response)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}
