// Package geminidirect implements the Gemini CLI Code Assist direct-http
// adapter. It talks to the same cloudcode-pa.googleapis.com v1internal
// endpoints observed from gemini-cli traffic captures, instead of launching
// the CLI for each request.
package geminidirect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const (
	defaultBaseURL            = "https://cloudcode-pa.googleapis.com"
	defaultUserAgent          = "GeminiCLI/0.41.2/gemini-3-pro-preview (linux; x64; terminal) google-api-nodejs-client/9.15.1"
	defaultAPIClient          = "gl-node/24.14.0"
	authRefreshThreshold      = 5 * time.Minute
	usageSourceDirectHTTP     = "gemini-direct-http"
	geminiVersionProbeTimeout = 2 * time.Second
)

var ErrConfig = errors.New("invalid gemini direct-http provider config")

type Options struct {
	Registration   provider.Registration
	BaseURL        string
	AuthPath       string
	HTTPClient     *http.Client
	UserAgent      string
	APIClient      string
	ToolDispatcher ToolDispatcher
	MaxToolRounds  int
}

type Provider struct {
	registration   provider.Registration
	baseURL        *url.URL
	authPath       string
	client         *http.Client
	userAgent      string
	apiClient      string
	toolDispatcher ToolDispatcher
	maxToolRounds  int

	projectMu sync.Mutex
	project   string

	usageMu sync.Mutex
	usage   provider.UsageReport

	healthMu sync.Mutex
	health   provider.Health

	authMu sync.Mutex
	auth   provider.AuthState
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = defaultCodeAssistBaseURL()
	} else {
		baseURL = normalizeCodeAssistBaseURL(baseURL)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: parse base url: %v", ErrConfig, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: base url must include scheme and host", ErrConfig)
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = envOrDefault("PANGAEA_GEMINI_DIRECT_USER_AGENT", defaultUserAgent)
	}
	apiClient := strings.TrimSpace(opts.APIClient)
	if apiClient == "" {
		apiClient = envOrDefault("PANGAEA_GEMINI_DIRECT_API_CLIENT", defaultAPIClient)
	}
	registration := opts.Registration
	if strings.TrimSpace(registration.Identity.TargetVersion) == "" {
		if version, err := detectGeminiClientVersion(); err == nil {
			registration.Identity.TargetVersion = version
		}
	}
	now := time.Now().UTC()
	return &Provider{
		registration:   registration,
		baseURL:        parsed,
		authPath:       opts.AuthPath,
		client:         client,
		userAgent:      userAgent,
		apiClient:      apiClient,
		toolDispatcher: opts.ToolDispatcher,
		maxToolRounds:  maxToolRounds(opts.MaxToolRounds),
		usage: provider.UsageReport{
			ObservedAt: now,
			Source:     usageSourceDirectHTTP,
		},
		health: initialHealth(opts.Registration.Health, now),
		auth:   opts.Registration.Auth,
	}, nil
}

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrConfig
	}
	return p.registration, nil
}

func (p *Provider) Close() error {
	if p == nil || p.toolDispatcher == nil {
		return nil
	}
	closer, ok := p.toolDispatcher.(closeableToolDispatcher)
	if !ok || closer == nil {
		return nil
	}
	return closer.Close()
}

func (p *Provider) ForceModelDiscovery() bool {
	return true
}

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	if p == nil {
		return nil, ErrConfig
	}
	configured := cloneModels(p.registration.Models)
	quota, err := p.retrieveQuota(ctx)
	if err != nil {
		p.recordHealth(err)
		p.recordAuth(err)
		return configured, nil
	}
	discovered := modelsFromQuota(quota, p.registration.Capabilities)
	if len(discovered) == 0 {
		return configured, nil
	}
	return mergeGeminiModels(configured, discovered), nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if err := p.validateInvocation(registration, request); err != nil {
		return compat.Response{}, err
	}
	current := request
	totalUsage := compat.Usage{}
	for round := 0; ; round++ {
		response, err := p.invokeOnce(ctx, current)
		if err != nil {
			p.recordInvocationResult(totalUsage, err)
			return compat.Response{}, err
		}
		totalUsage = addUsage(totalUsage, response.Usage)
		if response.ID == "" {
			response.ID = request.ID
		}
		if len(response.Message.ToolCalls) == 0 || p.toolDispatcher == nil {
			response.Usage = totalUsage
			p.recordInvocationResult(response.Usage, nil)
			return response, nil
		}
		if round >= p.maxToolRounds {
			err := fmt.Errorf("%w: maximum tool continuation rounds exceeded", ErrConfig)
			p.recordInvocationResult(totalUsage, err)
			return compat.Response{}, err
		}
		toolResults, err := p.dispatchToolCalls(ctx, response.Message.ToolCalls)
		if err != nil {
			p.recordInvocationResult(totalUsage, err)
			return compat.Response{}, err
		}
		current = appendToolContinuation(current, response.Message, toolResults)
	}
}

func (p *Provider) invokeOnce(ctx context.Context, request compat.Request) (compat.Response, error) {
	upstreamRequest, err := p.codeAssistRequest(ctx, request)
	if err != nil {
		return compat.Response{}, err
	}
	var upstream codeAssistGenerateResponse
	if err := p.doJSON(ctx, http.MethodPost, "/v1internal:generateContent", upstreamRequest, "application/json", &upstream); err != nil {
		return compat.Response{}, err
	}
	response, err := codeAssistResponseToCanonical(upstream.Response, request.Dialect, request.Model)
	if err != nil {
		return compat.Response{}, err
	}
	if response.ID == "" {
		response.ID = request.ID
	}
	return response, nil
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if emit == nil {
		return compat.Response{}, fmt.Errorf("%w: stream emit callback is required", ErrConfig)
	}
	if err := p.validateInvocation(registration, request); err != nil {
		return compat.Response{}, err
	}
	current := request
	totalUsage := compat.Usage{}
	for round := 0; ; round++ {
		roundEmit := emit
		var toolEmitter *toolStreamRoundEmitter
		if p.toolDispatcher != nil {
			toolEmitter = &toolStreamRoundEmitter{emit: emit}
			roundEmit = toolEmitter.Emit
		}
		response, err := p.invokeStreamOnce(ctx, current, roundEmit)
		if err != nil {
			p.recordInvocationResult(totalUsage, err)
			return compat.Response{}, err
		}
		totalUsage = addUsage(totalUsage, response.Usage)
		canDispatchTools := p.toolDispatcher != nil &&
			len(response.Message.ToolCalls) > 0 &&
			toolEmitter != nil &&
			toolEmitter.internalToolRound &&
			!toolEmitter.streamingToClient
		if len(response.Message.ToolCalls) == 0 || !canDispatchTools {
			response.Usage = totalUsage
			p.recordInvocationResult(response.Usage, nil)
			return response, nil
		}
		if round >= p.maxToolRounds {
			err := fmt.Errorf("%w: maximum tool continuation rounds exceeded", ErrConfig)
			p.recordInvocationResult(totalUsage, err)
			return compat.Response{}, err
		}
		toolResults, err := p.dispatchToolCalls(ctx, response.Message.ToolCalls)
		if err != nil {
			p.recordInvocationResult(totalUsage, err)
			return compat.Response{}, err
		}
		current = appendToolContinuation(current, response.Message, toolResults)
	}
}

func (p *Provider) invokeStreamOnce(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	upstreamRequest, err := p.codeAssistRequest(ctx, request)
	if err != nil {
		return compat.Response{}, err
	}
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1internal:streamGenerateContent?alt=sse", upstreamRequest, "text/event-stream")
	if err != nil {
		return compat.Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return compat.Response{}, readErr
		}
		err := upstreamHTTPError(resp, body)
		return compat.Response{}, err
	}
	response := compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant},
	}
	started := false
	if err := processSSEPayloads(resp.Body, func(payload string) (bool, error) {
		return applyCodeAssistStreamPayload(&response, &started, payload, emit)
	}); err != nil {
		return compat.Response{}, err
	}
	if !started {
		event := compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}
		if err := emit(event); err != nil {
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

func (p *Provider) Usage() (provider.UsageReport, error) {
	if p == nil {
		return provider.UsageReport{}, ErrConfig
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	usage := p.usage
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	if usage.Source == "" {
		usage.Source = usageSourceDirectHTTP
	}
	return usage, nil
}

func (p *Provider) Health() (provider.Health, error) {
	if p == nil {
		return provider.Health{}, ErrConfig
	}
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	return p.health, nil
}

func (p *Provider) Auth() (provider.AuthState, error) {
	if p == nil {
		return provider.AuthState{}, ErrConfig
	}
	if strings.TrimSpace(p.authPath) == "" {
		p.authMu.Lock()
		defer p.authMu.Unlock()
		return p.auth, nil
	}
	auth, err := p.authFromFile()
	if err != nil {
		p.authMu.Lock()
		defer p.authMu.Unlock()
		current := p.auth
		current.Status = provider.AuthUnavailable
		current.LastRefreshErr = err.Error()
		p.auth = current
		return current, nil
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	current := p.auth
	current.Status = auth.Status
	current.ExpiresAt = auth.ExpiresAt
	current.Refreshable = auth.Refreshable
	current.LastRefreshErr = auth.LastRefreshErr
	p.auth = current
	return current, nil
}

func (p *Provider) validateInvocation(registration provider.Registration, request compat.Request) error {
	if p == nil || p.client == nil {
		return ErrConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return fmt.Errorf("%w: provider instance mismatch", ErrConfig)
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: validate compat request: %v", ErrConfig, err)
	}
	return nil
}

func (p *Provider) codeAssistRequest(ctx context.Context, request compat.Request) (codeAssistGenerateRequest, error) {
	tools, err := p.toolDefinitions(ctx, request.Tools)
	if err != nil {
		return codeAssistGenerateRequest{}, fmt.Errorf("%w: build tool definitions: %v", ErrConfig, err)
	}
	request.Tools = tools
	upstream, err := compat.GeminiGenerateContentRequestFromCanonical(request)
	if err != nil {
		return codeAssistGenerateRequest{}, fmt.Errorf("%w: convert canonical request to Gemini request: %v", ErrConfig, err)
	}
	model := resolveModel(request.Model)
	body, err := codeAssistRequestBody(upstream, model, request.ReasoningEffort)
	if err != nil {
		return codeAssistGenerateRequest{}, fmt.Errorf("%w: build Code Assist request body: %v", ErrConfig, err)
	}
	project, err := p.projectForRequest(ctx)
	if err != nil {
		return codeAssistGenerateRequest{}, fmt.Errorf("%w: resolve Code Assist project: %v", ErrConfig, err)
	}
	return codeAssistGenerateRequest{
		Model:        model,
		Project:      project,
		UserPromptID: newPromptID(),
		Request:      body,
	}, nil
}

func codeAssistRequestBody(request compat.GeminiGenerateContentRequest, model string, reasoningEffort string) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	applyCodeAssistDefaults(body, model, reasoningEffort)
	return body, nil
}

func applyCodeAssistDefaults(body map[string]any, model string, reasoningEffort string) {
	delete(body, "reasoning_effort")
	generationConfig, _ := body["generationConfig"].(map[string]any)
	if generationConfig == nil {
		generationConfig = map[string]any{}
		body["generationConfig"] = generationConfig
	}
	delete(generationConfig, "reasoningEffort")
	if _, ok := generationConfig["temperature"]; !ok {
		generationConfig["temperature"] = float64(1)
	}
	if _, ok := generationConfig["topP"]; !ok {
		generationConfig["topP"] = 0.95
	}
	if _, ok := generationConfig["topK"]; !ok {
		generationConfig["topK"] = float64(64)
	}
	if _, ok := generationConfig["thinkingConfig"]; !ok {
		if thinkingConfig := defaultThinkingConfig(model, reasoningEffort); len(thinkingConfig) > 0 {
			generationConfig["thinkingConfig"] = thinkingConfig
		}
	} else if thinkingConfig, ok := generationConfig["thinkingConfig"].(map[string]any); ok {
		if strings.TrimSpace(reasoningEffort) != "" {
			if _, exists := thinkingConfig["includeThoughts"]; !exists {
				thinkingConfig["includeThoughts"] = true
			}
		}
		if strings.TrimSpace(reasoningEffort) != "" {
			if _, exists := thinkingConfig["thinkingLevel"]; !exists && !strings.Contains(strings.ToLower(model), "2.5") {
				thinkingConfig["thinkingLevel"] = strings.ToUpper(strings.TrimSpace(reasoningEffort))
			}
		}
	}
	if _, ok := body["session_id"]; !ok {
		body["session_id"] = newPromptID()
	}
	if systemInstruction, ok := body["systemInstruction"].(map[string]any); ok {
		if _, exists := systemInstruction["role"]; !exists {
			systemInstruction["role"] = "user"
		}
	}
}

func defaultThinkingConfig(model string, reasoningEffort string) map[string]any {
	model = strings.ToLower(strings.TrimSpace(model))
	reasoningEffort = strings.ToLower(strings.TrimSpace(reasoningEffort))
	if reasoningEffort == "" {
		return nil
	}
	out := map[string]any{"includeThoughts": true}
	switch {
	case strings.Contains(model, "2.5"):
		out["thinkingBudget"] = float64(8192)
	default:
		out["thinkingLevel"] = strings.ToUpper(reasoningEffort)
	}
	return out
}

func (p *Provider) projectForRequest(ctx context.Context) (string, error) {
	p.projectMu.Lock()
	project := p.project
	p.projectMu.Unlock()
	if project != "" {
		return project, nil
	}
	loaded, err := p.loadCodeAssist(ctx)
	if err != nil {
		return "", err
	}
	project = strings.TrimSpace(loaded.CloudaiCompanionProject)
	if project == "" {
		return "", fmt.Errorf("%w: loadCodeAssist did not return cloudaicompanionProject", ErrConfig)
	}
	p.projectMu.Lock()
	p.project = project
	p.projectMu.Unlock()
	return project, nil
}

func (p *Provider) loadCodeAssist(ctx context.Context) (loadCodeAssistResponse, error) {
	request := loadCodeAssistRequest{
		Metadata: loadCodeAssistMetadata{
			IDEType:    "IDE_UNSPECIFIED",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
	}
	var response loadCodeAssistResponse
	if err := p.doJSON(ctx, http.MethodPost, "/v1internal:loadCodeAssist", request, "application/json", &response); err != nil {
		return loadCodeAssistResponse{}, err
	}
	p.recordHealth(nil)
	p.recordAuth(nil)
	return response, nil
}

func (p *Provider) retrieveQuota(ctx context.Context) (retrieveUserQuotaResponse, error) {
	project, err := p.projectForRequest(ctx)
	if err != nil {
		return retrieveUserQuotaResponse{}, err
	}
	var response retrieveUserQuotaResponse
	err = p.doJSON(ctx, http.MethodPost, "/v1internal:retrieveUserQuota", retrieveUserQuotaRequest{Project: project}, "application/json", &response)
	return response, err
}

func (p *Provider) doJSON(ctx context.Context, method string, path string, body any, accept string, out any) error {
	resp, err := p.doRequest(ctx, method, path, body, accept)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamHTTPError(resp, responseBody)
	}
	if len(responseBody) == 0 {
		return fmt.Errorf("%w: empty upstream response", ErrConfig)
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
	auth, err := p.readAuthFile()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("x-goog-api-client", p.apiClient)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &provider.UpstreamError{Message: err.Error()}
	}
	return resp, nil
}

func (p *Provider) endpoint(path string) string {
	u := *p.baseURL
	pathOnly, rawQuery, hasQuery := strings.Cut(path, "?")
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		u.Path = pathOnly
	} else {
		u.Path = basePath + pathOnly
	}
	if hasQuery {
		u.RawQuery = rawQuery
	}
	return u.String()
}

func (p *Provider) readAuthFile() (oauthCredentials, error) {
	if strings.TrimSpace(p.authPath) == "" {
		return oauthCredentials{}, fmt.Errorf("%w: auth path is not configured", ErrConfig)
	}
	raw, err := os.ReadFile(p.authPath)
	if err != nil {
		return oauthCredentials{}, fmt.Errorf("%w: read auth file: %v", ErrConfig, err)
	}
	var creds oauthCredentials
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}), &creds); err != nil {
		return oauthCredentials{}, fmt.Errorf("%w: decode auth file: %v", ErrConfig, err)
	}
	if strings.TrimSpace(creds.AccessToken) == "" {
		return oauthCredentials{}, fmt.Errorf("%w: auth file missing access_token", ErrConfig)
	}
	return creds, nil
}

func (p *Provider) authFromFile() (provider.AuthState, error) {
	creds, err := p.readAuthFile()
	if err != nil {
		return provider.AuthState{}, err
	}
	auth := p.registration.Auth
	auth.Refreshable = strings.TrimSpace(creds.RefreshToken) != ""
	if creds.ExpiryDate > 0 {
		auth.ExpiresAt = time.UnixMilli(creds.ExpiryDate).UTC()
	}
	now := time.Now().UTC()
	switch {
	case !auth.ExpiresAt.IsZero() && !auth.ExpiresAt.After(now):
		auth.Status = provider.AuthExpired
		auth.LastRefreshErr = "gemini oauth access token is expired"
	case !auth.ExpiresAt.IsZero() && !now.Add(authRefreshThreshold).Before(auth.ExpiresAt):
		auth.Status = provider.AuthRefreshSoon
		auth.LastRefreshErr = "gemini oauth access token is inside refresh window"
	default:
		auth.Status = provider.AuthHealthy
		auth.LastRefreshErr = ""
	}
	return auth, nil
}

func (p *Provider) recordInvocationResult(usage compat.Usage, invokeErr error) {
	p.recordUsage(usage, invokeErr)
	p.recordHealth(invokeErr)
	p.recordAuth(invokeErr)
}

func (p *Provider) recordUsage(usage compat.Usage, invokeErr error) {
	if p == nil || invokeErr != nil {
		return
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	if p.usage.Source == "" {
		p.usage.Source = usageSourceDirectHTTP
	}
	p.usage.Requests++
	p.usage.InputTokens += usage.InputTokens
	p.usage.OutputTokens += usage.OutputTokens
	p.usage.TotalTokens += total
	p.usage.ObservedAt = time.Now().UTC()
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
	defer p.healthMu.Unlock()
	p.health = health
}

func (p *Provider) recordAuth(invokeErr error) {
	if p == nil {
		return
	}
	if invokeErr == nil {
		_, _ = p.Auth()
		return
	}
	if errors.Is(invokeErr, context.Canceled) || errors.Is(invokeErr, context.DeadlineExceeded) {
		return
	}
	var upstream *provider.UpstreamError
	if !errors.As(invokeErr, &upstream) {
		return
	}
	if upstream.StatusCode != http.StatusUnauthorized && upstream.StatusCode != http.StatusForbidden {
		return
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	auth := p.auth
	auth.Status = provider.AuthUnavailable
	auth.LastRefreshErr = upstream.Error()
	p.auth = auth
}

func processSSEPayloads(body io.Reader, handle func(string) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if len(dataLines) > 0 {
				done, err := handle(strings.Join(dataLines, "\n"))
				if err != nil {
					return err
				}
				if done {
					return nil
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
		return err
	}
	if len(dataLines) > 0 {
		_, err := handle(strings.Join(dataLines, "\n"))
		return err
	}
	return nil
}

func upstreamHTTPError(resp *http.Response, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	message, code := upstreamErrorDetails(body)
	if message == "" {
		message = bodyText
	}
	if message == "" && resp != nil {
		message = http.StatusText(resp.StatusCode)
	}
	statusCode := 0
	retryAfter := ""
	if resp != nil {
		statusCode = resp.StatusCode
		retryAfter = resp.Header.Get("retry-after")
	}
	statusCode, code = normalizeUpstreamStatus(statusCode, code, message)
	return &provider.UpstreamError{
		StatusCode: statusCode,
		Code:       code,
		Message:    message,
		Body:       bodyText,
		RetryAfter: retryAfter,
	}
}

func upstreamErrorDetails(body []byte) (string, string) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	if errPayload, ok := payload["error"]; ok {
		switch errValue := errPayload.(type) {
		case string:
			return strings.TrimSpace(errValue), firstString(payload, "code", "type", "status")
		case map[string]any:
			return firstString(errValue, "message", "error", "detail"), firstString(errValue, "code", "type", "status")
		}
	}
	return firstString(payload, "message", "error", "detail"), firstString(payload, "code", "type", "status")
}

func normalizeUpstreamStatus(statusCode int, code string, message string) (int, string) {
	combined := strings.ToUpper(strings.TrimSpace(code + " " + message))
	switch {
	case statusCode >= http.StatusInternalServerError && strings.Contains(combined, "RESOURCE_EXHAUSTED"):
		if strings.TrimSpace(code) == "" || strings.EqualFold(code, "unknown") {
			code = "rate_limit_exceeded"
		}
		return http.StatusTooManyRequests, code
	case statusCode >= http.StatusInternalServerError && (strings.Contains(combined, "NOT_FOUND") || strings.Contains(combined, "CODE 404")):
		if strings.TrimSpace(code) == "" || strings.EqualFold(code, "unknown") {
			code = "not_found"
		}
		return http.StatusNotFound, code
	default:
		return statusCode, code
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64, bool:
			return fmt.Sprint(v)
		default:
			text := strings.TrimSpace(fmt.Sprint(v))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func defaultCodeAssistBaseURL() string {
	if raw := strings.TrimSpace(os.Getenv("GEMINI_LOADCODEASSIST_URL")); raw != "" {
		return normalizeCodeAssistBaseURL(raw)
	}
	return defaultBaseURL
}

func normalizeCodeAssistBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	u.Path = strings.TrimSuffix(u.Path, "/v1internal:loadCodeAssist")
	u.RawQuery = ""
	return u.String()
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func detectGeminiClientVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), geminiVersionProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "gemini", "--version").Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	for _, field := range strings.Fields(string(output)) {
		field = strings.Trim(field, " \t\r\n,;()[]{}")
		field = strings.TrimPrefix(field, "v")
		if looksLikeGeminiVersion(field) {
			return field, nil
		}
	}
	return strings.TrimSpace(string(output)), nil
}

func looksLikeGeminiVersion(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if (r < '0' || r > '9') && r != '-' && r != '+' {
				return false
			}
		}
	}
	return true
}

func initialHealth(health provider.Health, now time.Time) provider.Health {
	if health.Status == "" {
		health.Status = provider.HealthReady
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = now
	}
	return health
}

func cloneModels(in []provider.Model) []provider.Model {
	out := make([]provider.Model, len(in))
	copy(out, in)
	for i := range out {
		out[i].Aliases = append([]string(nil), out[i].Aliases...)
		out[i].Capabilities = append([]provider.Capability(nil), out[i].Capabilities...)
		out[i].GroupMembers = append([]string(nil), out[i].GroupMembers...)
		if out[i].Quota != nil {
			quota := *out[i].Quota
			out[i].Quota = &quota
		}
	}
	return out
}

func resolveModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "gemini-default", "gemini-auto", "gemini-auto-3", "auto-gemini-3":
		return "gemini-3-flash-preview"
	case "gemini-auto-2.5", "auto-gemini-2.5":
		return "gemini-2.5-flash"
	default:
		return strings.TrimSpace(model)
	}
}

func newPromptID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
