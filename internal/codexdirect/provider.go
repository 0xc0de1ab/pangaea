// Package codexdirect implements the Codex ChatGPT backend direct-http
// adapter. It uses the same `/backend-api/codex/responses` transport shape
// used by native Codex Responses clients, without starting Codex AppServer.
package codexdirect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const (
	defaultBaseURL             = "https://chatgpt.com/backend-api"
	defaultRequestTimeout      = 2 * time.Minute
	defaultTextVerbosity       = "medium"
	defaultInstructions        = "You are a helpful coding assistant."
	defaultOriginator          = "pangaea"
	defaultUserAgent           = "pangaea-codex-direct/0.1"
	authRefreshThreshold       = 5 * time.Minute
	usageSourceDirectHTTP      = "codex-direct-http"
	maxCodexInlineImageBytes   = 20 << 20
	maxCodexDirectHTTPRetries  = 3
	codexDirectHTTPBaseBackoff = time.Second
	codexVersionProbeTimeout   = 2 * time.Second
)

var ErrConfig = errors.New("invalid codex direct-http provider config")

type Options struct {
	Registration provider.Registration
	BaseURL      string
	AuthPath     string
	HTTPClient   *http.Client
	Originator   string
	UserAgent    string
	// ClientVersion is intended for tests; production detects it with `codex --version`.
	ClientVersion string
}

type Provider struct {
	registration  provider.Registration
	baseURL       *url.URL
	authPath      string
	client        *http.Client
	originator    string
	userAgent     string
	clientVersion string

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
		baseURL = envOrDefault("PANGAEA_CODEX_DIRECT_BASE_URL", defaultBaseURL)
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
		client = &http.Client{Timeout: defaultRequestTimeout}
	}
	originator := strings.TrimSpace(opts.Originator)
	if originator == "" {
		originator = envOrDefault("PANGAEA_CODEX_DIRECT_ORIGINATOR", defaultOriginator)
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = envOrDefault("PANGAEA_CODEX_DIRECT_USER_AGENT", defaultUserAgent)
	}
	clientVersion := strings.TrimSpace(opts.ClientVersion)
	if clientVersion == "" {
		clientVersion, err = detectCodexClientVersion()
		if err != nil {
			return nil, fmt.Errorf("%w: detect codex client version: %v", ErrConfig, err)
		}
	}
	registration := opts.Registration
	if strings.TrimSpace(registration.Identity.TargetVersion) == "" {
		registration.Identity.TargetVersion = clientVersion
	}
	now := time.Now().UTC()
	return &Provider{
		registration:  registration,
		baseURL:       parsed,
		authPath:      strings.TrimSpace(opts.AuthPath),
		client:        client,
		originator:    originator,
		userAgent:     userAgent,
		clientVersion: clientVersion,
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

func (p *Provider) ForceModelDiscovery() bool {
	return true
}

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	if p == nil {
		return nil, ErrConfig
	}
	models, err := p.discoverModels(ctx)
	if err == nil && len(models) > 0 {
		return mergeCodexModels(p.registration.Models, models), nil
	}
	fallback := cloneModels(p.registration.Models)
	if len(fallback) > 0 {
		return fallback, nil
	}
	defaults := defaultModels(p.registration.Capabilities)
	if len(defaults) > 0 {
		return defaults, nil
	}
	return nil, err
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

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	return p.InvokeStream(ctx, registration, request, nil)
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if err := p.validateInvocation(registration, request); err != nil {
		return compat.Response{}, err
	}
	response, err := p.invokeResponsesStream(ctx, request, emit)
	p.recordInvocationResult(response.Usage, err)
	return response, err
}

func (p *Provider) validateInvocation(registration provider.Registration, request compat.Request) error {
	if p == nil || p.client == nil {
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

func (p *Provider) invokeResponsesStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	upstream, err := p.codexRequestBody(request)
	if err != nil {
		return compat.Response{}, err
	}
	resp, err := p.doResponsesRequest(ctx, upstream, request.ID)
	if err != nil {
		return compat.Response{}, err
	}
	defer resp.Body.Close()

	if emit != nil {
		if err := emit(compat.Event{
			ResponseID: request.ID,
			Dialect:    request.Dialect,
			Model:      request.Model,
			Type:       compat.EventMessageStart,
			Message:    &compat.Message{Role: compat.MessageRoleAssistant},
		}); err != nil {
			return compat.Response{}, err
		}
	}

	state := &streamState{
		request: request,
		emit:    emit,
	}
	body := bufio.NewReader(resp.Body)
	if !isSSEPayload(resp.Header.Get("content-type"), body) {
		raw, readErr := io.ReadAll(body)
		if readErr != nil {
			return compat.Response{}, readErr
		}
		return responseFromJSON(raw, request)
	}
	if err := processSSEPayloads(body, func(data string) (bool, error) {
		if data == "[DONE]" {
			state.done = true
			return true, nil
		}
		return state.handle(data)
	}); err != nil {
		return compat.Response{}, err
	}
	return state.response()
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

func (p *Provider) doResponsesRequest(ctx context.Context, body map[string]any, requestID string) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	auth, err := p.readAuthFile()
	if err != nil {
		return nil, err
	}
	var last error
	for attempt := 0; attempt <= maxCodexDirectHTTPRetries; attempt++ {
		resp, err := p.doResponsesRequestOnce(ctx, data, auth, requestID)
		if err != nil {
			last = err
			if !retryableError(err) || attempt == maxCodexDirectHTTPRetries {
				return nil, err
			}
			if sleepErr := sleep(ctx, codexDirectHTTPBaseBackoff*time.Duration(1<<attempt)); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		last = upstreamHTTPError(resp, bodyBytes)
		if !retryableStatus(resp.StatusCode, bodyBytes) || attempt == maxCodexDirectHTTPRetries {
			return nil, last
		}
		if sleepErr := sleep(ctx, retryDelay(resp.Header.Get("retry-after"), attempt)); sleepErr != nil {
			return nil, sleepErr
		}
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("%w: request failed", ErrConfig)
}

func (p *Provider) doResponsesRequestOnce(ctx context.Context, body []byte, auth codexAuth, requestID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.responsesEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("chatgpt-account-id", auth.AccountID)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("originator", p.originator)
	req.Header.Set("User-Agent", p.userAgent)
	if requestID != "" {
		req.Header.Set("session_id", requestID)
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

func (p *Provider) responsesEndpoint() string {
	u := *p.baseURL
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(path, "/codex/responses"):
	case strings.HasSuffix(path, "/codex"):
		path += "/responses"
	default:
		path += "/codex/responses"
	}
	if path == "" {
		path = "/codex/responses"
	}
	u.Path = path
	u.RawQuery = ""
	return u.String()
}

func (p *Provider) discoverModels(ctx context.Context) ([]provider.Model, error) {
	auth, err := p.readAuthFile()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.modelsEndpoint(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("chatgpt-account-id", auth.AccountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", p.originator)
	req.Header.Set("User-Agent", p.userAgent)
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &provider.UpstreamError{Message: err.Error()}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamHTTPError(resp, raw)
	}
	var response codexModelListResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("%w: decode codex models: %v", ErrConfig, err)
	}
	return codexModelsFromAPI(response.Models, codexModelCapabilities(p.registration)), nil
}

func (p *Provider) modelsEndpoint() string {
	u := *p.baseURL
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(path, "/codex/responses"):
		path = strings.TrimSuffix(path, "/responses") + "/models"
	case strings.HasSuffix(path, "/codex"):
		path += "/models"
	default:
		path += "/codex/models"
	}
	if path == "" {
		path = "/codex/models"
	}
	q := u.Query()
	q.Set("client_version", p.clientVersion)
	u.Path = path
	u.RawQuery = q.Encode()
	return u.String()
}

type codexModelListResponse struct {
	Models []codexAPIModel `json:"models"`
}

type codexAPIModel struct {
	Slug             string `json:"slug"`
	ID               string `json:"id"`
	Model            string `json:"model"`
	DisplayName      string `json:"display_name"`
	Visibility       string `json:"visibility"`
	Hidden           bool   `json:"hidden"`
	ContextWindow    int    `json:"context_window"`
	MaxContextWindow int    `json:"max_context_window"`
}

func codexModelsFromAPI(items []codexAPIModel, capabilities []provider.Capability) []provider.Model {
	models := make([]provider.Model, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.Hidden || strings.EqualFold(strings.TrimSpace(item.Visibility), "hide") {
			continue
		}
		id := firstNonBlank(item.Slug, item.ID, item.Model)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		maxContextWindow := item.MaxContextWindow
		if maxContextWindow == 0 {
			maxContextWindow = item.ContextWindow
		}
		aliases := []string(nil)
		if displayName := codexDisplayAlias(id, item.DisplayName); displayName != "" && displayName != id {
			aliases = []string{displayName}
		}
		models = append(models, provider.Model{
			ID:               id,
			Aliases:          aliases,
			Capabilities:     append([]provider.Capability(nil), capabilities...),
			ContextTokens:    item.ContextWindow,
			MaxContextTokens: maxContextWindow,
		})
	}
	return models
}

func (p *Provider) codexRequestBody(request compat.Request) (map[string]any, error) {
	input, instructions, err := codexInputFromCanonical(request)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":               request.Model,
		"store":               false,
		"stream":              true,
		"input":               input,
		"text":                map[string]any{"verbosity": defaultTextVerbosity},
		"include":             []string{"reasoning.encrypted_content"},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	body["instructions"] = firstNonEmpty(instructions, defaultInstructions)
	if request.ID != "" {
		body["prompt_cache_key"] = request.ID
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{
			"effort":  clampReasoningEffort(request.Model, request.ReasoningEffort),
			"summary": "auto",
		}
	}
	if len(request.Tools) > 0 {
		body["tools"] = codexTools(request.Tools)
	}
	return body, nil
}

func codexInputFromCanonical(request compat.Request) ([]map[string]any, string, error) {
	input := make([]map[string]any, 0, len(request.Messages))
	var instructions []string
	messageIndex := 0
	for _, message := range request.Messages {
		switch message.Role {
		case compat.MessageRoleSystem, compat.MessageRoleDeveloper:
			text := messageText(message)
			if strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
		case compat.MessageRoleUser:
			parts, err := inputContentParts(message.Content)
			if err != nil {
				return nil, "", err
			}
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": "user", "content": parts})
			}
		case compat.MessageRoleAssistant:
			input = append(input, assistantItems(message, &messageIndex)...)
		case compat.MessageRoleTool:
			text := messageText(message)
			if strings.TrimSpace(text) == "" {
				text = "(empty tool result)"
			}
			callID := strings.TrimSpace(message.ToolCallID)
			if callID == "" {
				return nil, "", fmt.Errorf("%w: tool message missing tool_call_id", ErrConfig)
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  text,
			})
		}
	}
	if len(input) == 0 {
		return nil, "", fmt.Errorf("%w: no Codex input messages", ErrConfig)
	}
	return input, strings.Join(instructions, "\n\n"), nil
}

func inputContentParts(parts []compat.ContentPart) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case compat.ContentPartText:
			if strings.TrimSpace(part.Text) != "" {
				out = append(out, map[string]any{"type": "input_text", "text": part.Text})
			}
		case compat.ContentPartImage:
			imageURL, err := codexImageURL(part)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"type": "input_image", "detail": "auto", "image_url": imageURL})
		}
	}
	return out, nil
}

func assistantItems(message compat.Message, messageIndex *int) []map[string]any {
	items := make([]map[string]any, 0, 1+len(message.ToolCalls))
	text := messageText(message)
	if strings.TrimSpace(text) != "" {
		id := fmt.Sprintf("msg_%d", *messageIndex)
		items = append(items, map[string]any{
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"id":     id,
			"content": []map[string]any{{
				"type":        "output_text",
				"text":        text,
				"annotations": []any{},
			}},
		})
		*messageIndex = *messageIndex + 1
	}
	for _, call := range message.ToolCalls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = fmt.Sprintf("call_%d", call.Index)
		}
		items = append(items, map[string]any{
			"type":      "function_call",
			"id":        "fc_" + normalizeCodexID(callID),
			"call_id":   callID,
			"name":      call.Name,
			"arguments": call.Arguments,
		})
	}
	return items
}

func messageText(message compat.Message) string {
	parts := make([]string, 0, len(message.Content))
	for _, part := range message.Content {
		if part.Type == compat.ContentPartText && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func codexImageURL(part compat.ContentPart) (string, error) {
	if part.URL != "" {
		parsed, err := url.Parse(part.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "data") {
			return "", fmt.Errorf("%w: codex image url must be http(s) or data URL", ErrConfig)
		}
		return part.URL, nil
	}
	mime := strings.ToLower(strings.TrimSpace(part.MIME))
	if !supportedCodexImageMIME(mime) {
		return "", fmt.Errorf("%w: unsupported codex image mime type %q", ErrConfig, part.MIME)
	}
	data, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		return "", fmt.Errorf("%w: decode image attachment: %v", ErrConfig, err)
	}
	if len(data) == 0 || len(data) > maxCodexInlineImageBytes {
		return "", fmt.Errorf("%w: codex image attachment must be 1..%d bytes", ErrConfig, maxCodexInlineImageBytes)
	}
	return "data:" + mime + ";base64," + part.Data, nil
}

func supportedCodexImageMIME(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif":
		return true
	}
	return false
}

func codexTools(tools []compat.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		item := map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"strict":      nil,
		}
		out = append(out, item)
	}
	return out
}

func clampReasoningEffort(model string, effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	id := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(id, "/") {
		parts := strings.Split(id, "/")
		id = parts[len(parts)-1]
	}
	if (strings.HasPrefix(id, "gpt-5.2") || strings.HasPrefix(id, "gpt-5.3") || strings.HasPrefix(id, "gpt-5.4") || strings.HasPrefix(id, "gpt-5.5")) && effort == "minimal" {
		return "low"
	}
	if id == "gpt-5.1" && effort == "xhigh" {
		return "high"
	}
	if id == "gpt-5.1-codex-mini" && (effort == "high" || effort == "xhigh") {
		return "high"
	}
	if id == "gpt-5.1-codex-mini" {
		return "medium"
	}
	return effort
}

type streamState struct {
	request compat.Request
	emit    func(compat.Event) error

	responseID string
	text       strings.Builder
	usage      compat.Usage
	stopReason string
	done       bool

	currentTool *compat.ToolCall
	toolArgs    strings.Builder
	toolCalls   []compat.ToolCall
}

func (s *streamState) handle(data string) (bool, error) {
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return false, nil
	}
	eventType := stringFromAny(event["type"])
	switch eventType {
	case "error":
		return false, codexEventError(event)
	case "response.failed":
		return false, responseFailedError(event)
	case "response.created":
		if response, ok := event["response"].(map[string]any); ok {
			s.responseID = stringFromAny(response["id"])
		}
	case "response.output_item.added":
		s.handleOutputItemAdded(event)
	case "response.output_text.delta", "response.refusal.delta":
		delta := stringFromAny(event["delta"])
		if delta != "" {
			s.text.WriteString(delta)
			if s.emit != nil {
				return false, s.emit(compat.Event{
					ResponseID:   s.responseIDOrRequest(),
					Dialect:      s.request.Dialect,
					Model:        s.request.Model,
					Type:         compat.EventContentDelta,
					ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: delta},
				})
			}
		}
	case "response.function_call_arguments.delta":
		delta := stringFromAny(event["delta"])
		if delta != "" {
			s.toolArgs.WriteString(delta)
			if s.emit != nil {
				tool := compat.ToolCall{Index: len(s.toolCalls), Type: compat.ToolCallFunction, Arguments: delta}
				if s.currentTool != nil {
					tool.ID = s.currentTool.ID
					tool.Name = s.currentTool.Name
				}
				return false, s.emit(compat.Event{
					ResponseID:    s.responseIDOrRequest(),
					Dialect:       s.request.Dialect,
					Model:         s.request.Model,
					Type:          compat.EventToolCallDelta,
					ToolCallDelta: &tool,
				})
			}
		}
	case "response.function_call_arguments.done":
		if args := stringFromAny(event["arguments"]); args != "" {
			s.toolArgs.Reset()
			s.toolArgs.WriteString(args)
		}
	case "response.output_item.done":
		s.handleOutputItemDone(event)
	case "response.done", "response.completed", "response.incomplete":
		s.handleCompleted(event)
		return true, nil
	}
	return false, nil
}

func (s *streamState) handleOutputItemAdded(event map[string]any) {
	item, ok := event["item"].(map[string]any)
	if !ok {
		return
	}
	switch stringFromAny(item["type"]) {
	case "function_call":
		id := stringFromAny(item["call_id"])
		if id == "" {
			id = stringFromAny(item["id"])
		}
		s.currentTool = &compat.ToolCall{
			Index: len(s.toolCalls),
			ID:    id,
			Type:  compat.ToolCallFunction,
			Name:  stringFromAny(item["name"]),
		}
		s.toolArgs.Reset()
	}
}

func (s *streamState) handleOutputItemDone(event map[string]any) {
	item, ok := event["item"].(map[string]any)
	if !ok {
		return
	}
	switch stringFromAny(item["type"]) {
	case "message":
		if s.text.Len() == 0 {
			text := outputTextFromMessageItem(item)
			if text != "" {
				s.text.WriteString(text)
			}
		}
	case "function_call":
		tool := compat.ToolCall{
			Index: len(s.toolCalls),
			Type:  compat.ToolCallFunction,
			ID:    stringFromAny(item["call_id"]),
			Name:  stringFromAny(item["name"]),
		}
		if tool.ID == "" {
			tool.ID = stringFromAny(item["id"])
		}
		args := s.toolArgs.String()
		if args == "" {
			args = stringFromAny(item["arguments"])
		}
		tool.Arguments = args
		s.toolCalls = append(s.toolCalls, tool)
		s.currentTool = nil
		s.toolArgs.Reset()
	}
}

func (s *streamState) handleCompleted(event map[string]any) {
	response, _ := event["response"].(map[string]any)
	if id := stringFromAny(response["id"]); id != "" {
		s.responseID = id
	}
	if s.text.Len() == 0 {
		if text := outputTextFromResponse(response); text != "" {
			s.text.WriteString(text)
		}
	}
	if usage := usageFromAny(response["usage"]); usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 {
		s.usage = usage
	}
	status := stringFromAny(response["status"])
	switch status {
	case "incomplete":
		s.stopReason = "length"
	case "failed", "cancelled":
		s.stopReason = "error"
	default:
		s.stopReason = "stop"
	}
	if len(s.toolCalls) > 0 && s.stopReason == "stop" {
		s.stopReason = "tool_use"
	}
	s.done = true
}

func (s *streamState) responseIDOrRequest() string {
	if s.responseID != "" {
		return s.responseID
	}
	return s.request.ID
}

func (s *streamState) response() (compat.Response, error) {
	text := s.text.String()
	content := []compat.ContentPart(nil)
	if strings.TrimSpace(text) != "" {
		content = []compat.ContentPart{{Type: compat.ContentPartText, Text: text}}
	} else if len(s.toolCalls) == 0 {
		content = []compat.ContentPart{{Type: compat.ContentPartText, Text: "[empty response]"}}
	}
	stopReason := s.stopReason
	if stopReason == "" {
		stopReason = "stop"
	}
	response := compat.Response{
		ID:      s.responseIDOrRequest(),
		Dialect: s.request.Dialect,
		Model:   s.request.Model,
		Message: compat.Message{
			Role:      compat.MessageRoleAssistant,
			Content:   content,
			ToolCalls: s.toolCalls,
		},
		StopReason: stopReason,
		Usage:      s.usage,
	}
	if s.emit != nil {
		if s.usage.TotalTokens > 0 || s.usage.InputTokens > 0 || s.usage.OutputTokens > 0 {
			if err := s.emit(compat.Event{
				ResponseID: response.ID,
				Dialect:    response.Dialect,
				Model:      response.Model,
				Type:       compat.EventUsageDelta,
				UsageDelta: &s.usage,
			}); err != nil {
				return compat.Response{}, err
			}
		}
		if err := s.emit(compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventDone,
			DoneReason: response.StopReason,
		}); err != nil {
			return compat.Response{}, err
		}
	}
	return response, nil
}

func responseFromJSON(body []byte, request compat.Request) (compat.Response, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return compat.Response{}, err
	}
	text := outputTextFromResponse(payload)
	if strings.TrimSpace(text) == "" {
		text = "[empty response]"
	}
	return compat.Response{
		ID:      firstNonEmpty(stringFromAny(payload["id"]), request.ID),
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant, Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: text}}},
		Usage:   usageFromAny(payload["usage"]),
	}, nil
}

func outputTextFromResponse(response map[string]any) string {
	if response == nil {
		return ""
	}
	if text := stringFromAny(response["output_text"]); text != "" {
		return text
	}
	output, _ := response["output"].([]any)
	parts := make([]string, 0, len(output))
	for _, item := range output {
		itemMap, _ := item.(map[string]any)
		switch stringFromAny(itemMap["type"]) {
		case "message":
			if text := outputTextFromMessageItem(itemMap); text != "" {
				parts = append(parts, text)
			}
		case "output_text":
			if text := stringFromAny(itemMap["text"]); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "")
}

func outputTextFromMessageItem(item map[string]any) string {
	content, _ := item["content"].([]any)
	parts := make([]string, 0, len(content))
	for _, part := range content {
		partMap, _ := part.(map[string]any)
		switch stringFromAny(partMap["type"]) {
		case "output_text":
			parts = append(parts, stringFromAny(partMap["text"]))
		case "refusal":
			parts = append(parts, stringFromAny(partMap["refusal"]))
		}
	}
	return strings.Join(parts, "")
}

func usageFromAny(value any) compat.Usage {
	payload, _ := value.(map[string]any)
	if payload == nil {
		return compat.Usage{}
	}
	input := int64FromAny(payload["input_tokens"])
	output := int64FromAny(payload["output_tokens"])
	total := int64FromAny(payload["total_tokens"])
	if input == 0 {
		input = int64FromAny(payload["prompt_tokens"])
	}
	if output == 0 {
		output = int64FromAny(payload["completion_tokens"])
	}
	if total == 0 {
		total = input + output
	}
	return compat.Usage{InputTokens: input, OutputTokens: output, TotalTokens: total}
}

func codexEventError(event map[string]any) error {
	message := stringFromAny(event["message"])
	code := stringFromAny(event["code"])
	if message == "" {
		message = stringFromAny(event["error"])
	}
	if message == "" {
		message = "codex response stream error"
	}
	return &provider.UpstreamError{Code: code, Message: message}
}

func responseFailedError(event map[string]any) error {
	response, _ := event["response"].(map[string]any)
	errorPayload, _ := response["error"].(map[string]any)
	message := stringFromAny(errorPayload["message"])
	code := stringFromAny(errorPayload["code"])
	if message == "" {
		message = "codex response failed"
	}
	return &provider.UpstreamError{Code: code, Message: message}
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

type codexAuth struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
	ExpiresAt    time.Time
}

type codexAuthFile struct {
	Tokens struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

func (p *Provider) readAuthFile() (codexAuth, error) {
	if strings.TrimSpace(p.authPath) == "" {
		return codexAuth{}, fmt.Errorf("%w: auth path is not configured", ErrConfig)
	}
	raw, err := os.ReadFile(p.authPath)
	if err != nil {
		return codexAuth{}, fmt.Errorf("%w: read auth file: %v", ErrConfig, err)
	}
	var file codexAuthFile
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}), &file); err != nil {
		return codexAuth{}, fmt.Errorf("%w: decode auth file: %v", ErrConfig, err)
	}
	accessToken := strings.TrimSpace(file.Tokens.AccessToken)
	if accessToken == "" {
		return codexAuth{}, fmt.Errorf("%w: auth file missing tokens.access_token", ErrConfig)
	}
	accountID := jwtChatGPTAccountID(accessToken)
	if accountID == "" {
		accountID = jwtChatGPTAccountID(file.Tokens.IDToken)
	}
	if accountID == "" {
		accountID = strings.TrimSpace(file.Tokens.AccountID)
	}
	if accountID == "" {
		return codexAuth{}, fmt.Errorf("%w: auth file missing chatgpt account id", ErrConfig)
	}
	return codexAuth{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(file.Tokens.RefreshToken),
		AccountID:    accountID,
		ExpiresAt:    jwtExp(accessToken),
	}, nil
}

func (p *Provider) authFromFile() (provider.AuthState, error) {
	codexAuth, err := p.readAuthFile()
	if err != nil {
		return provider.AuthState{}, err
	}
	auth := p.registration.Auth
	auth.Refreshable = codexAuth.RefreshToken != ""
	auth.ExpiresAt = codexAuth.ExpiresAt
	now := time.Now().UTC()
	switch {
	case !auth.ExpiresAt.IsZero() && !auth.ExpiresAt.After(now):
		auth.Status = provider.AuthExpired
		auth.LastRefreshErr = "codex oauth access token is expired"
	case !auth.ExpiresAt.IsZero() && !now.Add(authRefreshThreshold).Before(auth.ExpiresAt):
		auth.Status = provider.AuthRefreshSoon
		auth.LastRefreshErr = "codex oauth access token is inside refresh window"
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
	p.usage.Source = usageSourceDirectHTTP
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

func retryableStatus(status int, body []byte) bool {
	if status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout || status == http.StatusInternalServerError {
		return true
	}
	combined := strings.ToLower(string(body))
	return strings.Contains(combined, "rate limit") || strings.Contains(combined, "overloaded") || strings.Contains(combined, "service unavailable")
}

func retryableError(err error) bool {
	var upstream *provider.UpstreamError
	if errors.As(err, &upstream) {
		return retryableStatus(upstream.StatusCode, []byte(upstream.Body))
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func retryDelay(raw string, attempt int) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		if ts, err := http.ParseTime(raw); err == nil {
			if delay := time.Until(ts); delay > 0 {
				return delay
			}
		}
	}
	return codexDirectHTTPBaseBackoff * time.Duration(1<<attempt)
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func jwtChatGPTAccountID(token string) string {
	claims := jwtClaims(token)
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	if auth == nil {
		return ""
	}
	return stringFromAny(auth["chatgpt_account_id"])
}

func jwtExp(token string) time.Time {
	claims := jwtClaims(token)
	exp := int64FromAny(claims["exp"])
	if exp <= 0 {
		return time.Time{}
	}
	return time.Unix(exp, 0).UTC()
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload := parts[1]
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(payload)
	}
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}
	return claims
}

func defaultModels(capabilities []provider.Capability) []provider.Model {
	caps := dedupeCapabilities(capabilities)
	return []provider.Model{
		{ID: "gpt-5.5", Aliases: []string{"codex-default", "GPT-5.5"}, Capabilities: caps, ContextTokens: 272000, MaxContextTokens: 128000},
		{ID: "gpt-5.4", Aliases: []string{"GPT-5.4"}, Capabilities: caps, ContextTokens: 272000, MaxContextTokens: 128000},
		{ID: "gpt-5.4-mini", Aliases: []string{"GPT-5.4 Mini"}, Capabilities: caps, ContextTokens: 272000, MaxContextTokens: 128000},
		{ID: "gpt-5.3-codex", Aliases: []string{"GPT-5.3 Codex"}, Capabilities: caps, ContextTokens: 272000, MaxContextTokens: 128000},
		{ID: "gpt-5.3-codex-spark", Aliases: []string{"GPT-5.3 Codex Spark"}, Capabilities: caps, ContextTokens: 128000, MaxContextTokens: 128000},
	}
}

func codexModelCapabilities(registration provider.Registration) []provider.Capability {
	for _, model := range registration.Models {
		if len(model.Capabilities) > 0 {
			return append([]provider.Capability(nil), model.Capabilities...)
		}
	}
	allowed := map[provider.Capability]bool{
		provider.CapabilityOpenAIChat:            true,
		provider.CapabilityOpenAIResponses:       true,
		provider.CapabilityAnthropicMessages:     true,
		provider.CapabilityGeminiGenerateContent: true,
		provider.CapabilityStreamSSE:             true,
	}
	capabilities := make([]provider.Capability, 0, len(registration.Capabilities))
	for _, capability := range registration.Capabilities {
		if allowed[capability] {
			capabilities = append(capabilities, capability)
		}
	}
	return capabilities
}

func mergeCodexModels(configured []provider.Model, discovered []provider.Model) []provider.Model {
	out := cloneModels(configured)
	index := make(map[string]int, len(out))
	for i, model := range out {
		index[model.ID] = i
	}
	for _, model := range discovered {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		model.ID = id
		if i, ok := index[id]; ok {
			out[i] = mergeCodexModel(out[i], model)
			continue
		}
		index[id] = len(out)
		out = append(out, cloneModels([]provider.Model{model})[0])
	}
	return out
}

func mergeCodexModel(base provider.Model, discovered provider.Model) provider.Model {
	base.Aliases = mergeModelAliases(base.Aliases, discovered.Aliases)
	base.Capabilities = mergeCapabilities(base.Capabilities, discovered.Capabilities)
	if base.ContextTokens == 0 {
		base.ContextTokens = discovered.ContextTokens
	}
	if base.MaxContextTokens == 0 {
		base.MaxContextTokens = discovered.MaxContextTokens
	}
	if base.Kind == "" {
		base.Kind = discovered.Kind
	}
	if len(base.GroupMembers) == 0 {
		base.GroupMembers = append([]string(nil), discovered.GroupMembers...)
	}
	if base.Quota == nil && discovered.Quota != nil {
		quota := *discovered.Quota
		base.Quota = &quota
	}
	return base
}

func mergeModelAliases(left []string, right []string) []string {
	out := append([]string(nil), left...)
	seen := make(map[string]struct{}, len(out)+len(right))
	for _, alias := range out {
		seen[alias] = struct{}{}
	}
	for _, alias := range right {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func mergeCapabilities(left []provider.Capability, right []provider.Capability) []provider.Capability {
	return dedupeCapabilities(append(append([]provider.Capability(nil), left...), right...))
}

func codexDisplayAlias(id string, displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = strings.TrimSpace(id)
	}
	if displayName == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(displayName), "gpt-") {
		displayName = "GPT-" + strings.TrimPrefix(strings.TrimPrefix(displayName, "gpt-"), "GPT-")
	}
	replacements := []struct {
		old string
		new string
	}{
		{"-Mini", " Mini"},
		{"-mini", " Mini"},
		{"-Codex", " Codex"},
		{"-codex", " Codex"},
		{"-Spark", " Spark"},
		{"-spark", " Spark"},
	}
	for _, replacement := range replacements {
		displayName = strings.ReplaceAll(displayName, replacement.old, replacement.new)
	}
	return displayName
}

func detectCodexClientVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexVersionProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "codex", "--version").Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	for _, field := range strings.Fields(string(output)) {
		field = strings.TrimSpace(field)
		if looksLikeSemver(field) {
			return field, nil
		}
	}
	return "", fmt.Errorf("could not parse semver from %q", strings.TrimSpace(string(output)))
}

func looksLikeSemver(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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

func dedupeCapabilities(capabilities []provider.Capability) []provider.Capability {
	seen := map[provider.Capability]struct{}{}
	out := make([]provider.Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
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

func normalizeCodexID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return newID()
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.TrimRight(b.String(), "_")
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func int64FromAny(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(stringFromAny(value))
		if text != "" {
			return text
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
