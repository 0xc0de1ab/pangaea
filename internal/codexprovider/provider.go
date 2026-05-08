// Package codexprovider adapts Codex CLI AppServer to Pangaea's canonical
// provider shim interface.
package codexprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gorilla/websocket"
)

const (
	defaultRequestTimeout = 2 * time.Minute
	usageSource           = "codex-appserver-websocket"
	maxCodexImageBytes    = 20 << 20
)

var ErrCodexProviderConfig = errors.New("invalid codex appserver provider config")

type Options struct {
	Registration   provider.Registration
	AppServerURL   string
	AuthPath       string
	RequestTimeout time.Duration
	Dialer         *websocket.Dialer
}

type Provider struct {
	registration   provider.Registration
	appServerURL   string
	authPath       string
	requestTimeout time.Duration
	dialer         *websocket.Dialer

	usageMu sync.Mutex
	usage   provider.UsageReport
	health  provider.Health
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCodexProviderConfig, err)
	}
	appServerURL := strings.TrimSpace(opts.AppServerURL)
	if appServerURL == "" {
		return nil, fmt.Errorf("%w: app server url is required", ErrCodexProviderConfig)
	}
	parsed, err := url.Parse(appServerURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, fmt.Errorf("%w: app server url must use ws or wss", ErrCodexProviderConfig)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: app server url must include host", ErrCodexProviderConfig)
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	dialer := opts.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	now := time.Now().UTC()
	return &Provider{
		registration:   opts.Registration,
		appServerURL:   appServerURL,
		authPath:       strings.TrimSpace(opts.AuthPath),
		requestTimeout: requestTimeout,
		dialer:         dialer,
		usage: provider.UsageReport{
			ObservedAt: now,
			Source:     usageSource,
		},
		health: provider.Health{Status: provider.HealthReady, CheckedAt: now},
	}, nil
}

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrCodexProviderConfig
	}
	return p.registration, nil
}

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	if p == nil {
		return nil, ErrCodexProviderConfig
	}
	models, err := p.discoverAppServerModels(ctx)
	if err != nil || len(models) == 0 {
		fallback := cloneModels(p.registration.Models)
		if len(fallback) > 0 {
			return fallback, nil
		}
		return nil, err
	}
	return mergeCodexModels(p.registration.Models, models), nil
}

func (p *Provider) Usage() (provider.UsageReport, error) {
	if p == nil {
		return provider.UsageReport{}, ErrCodexProviderConfig
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	usage := p.usage
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	if usage.Source == "" {
		usage.Source = usageSource
	}
	return usage, nil
}

func (p *Provider) Health() (provider.Health, error) {
	if p == nil {
		return provider.Health{}, ErrCodexProviderConfig
	}
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	health := p.health
	if health.Status == "" {
		health.Status = provider.HealthReady
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = time.Now().UTC()
	}
	return health, nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	return p.InvokeStream(ctx, registration, request, nil)
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if p == nil {
		return compat.Response{}, ErrCodexProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrCodexProviderConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	requestCtx := ctx
	if _, ok := requestCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, p.requestTimeout)
		defer cancel()
	}
	response, err := p.invokeAppServer(requestCtx, request, emit)
	p.recordInvocationResult(response.Usage, err)
	return response, err
}

func (p *Provider) invokeAppServer(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	token, err := readCodexAccessToken(p.authPath)
	if err != nil {
		return compat.Response{}, err
	}
	client, err := newRPCClient(ctx, p.appServerURL, token, p.dialer)
	if err != nil {
		return compat.Response{}, err
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		return compat.Response{}, err
	}

	threadParams := map[string]any{
		"ephemeral": true,
		"model":     request.Model,
	}
	var threadResp threadStartResponse
	if err := client.call(ctx, "thread/start", threadParams, &threadResp); err != nil {
		return compat.Response{}, err
	}
	threadID := strings.TrimSpace(threadResp.Thread.ID)
	if threadID == "" {
		return compat.Response{}, fmt.Errorf("%w: thread/start response missing thread id", ErrCodexProviderConfig)
	}

	turnParams, cleanup, err := turnStartParamsFromCanonical(request, threadID)
	if err != nil {
		return compat.Response{}, err
	}
	defer cleanup()
	var turnResp turnStartResponse
	if err := client.call(ctx, "turn/start", turnParams, &turnResp); err != nil {
		return compat.Response{}, err
	}
	turnID := strings.TrimSpace(turnResp.Turn.ID)
	if turnID == "" {
		return compat.Response{}, fmt.Errorf("%w: turn/start response missing turn id", ErrCodexProviderConfig)
	}

	builder := strings.Builder{}
	var usage compat.Usage
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

	for {
		select {
		case <-ctx.Done():
			return compat.Response{}, ctx.Err()
		case <-client.done:
			return compat.Response{}, fmt.Errorf("%w: codex appserver websocket closed", ErrCodexProviderConfig)
		case notification, ok := <-client.notifications:
			if !ok {
				return compat.Response{}, fmt.Errorf("%w: codex appserver websocket closed", ErrCodexProviderConfig)
			}
			done, err := handleNotification(notification, turnID, request, emit, &builder, &usage)
			if err != nil {
				return compat.Response{}, err
			}
			if done {
				text := builder.String()
				if strings.TrimSpace(text) == "" {
					text = "[empty response]"
				}
				response := compat.Response{
					ID:      request.ID,
					Dialect: request.Dialect,
					Model:   request.Model,
					Message: compat.Message{
						Role:    compat.MessageRoleAssistant,
						Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: text}},
					},
					StopReason: "stop",
					Usage:      usage,
				}
				if emit != nil {
					if err := emit(compat.Event{
						ResponseID: request.ID,
						Dialect:    request.Dialect,
						Model:      request.Model,
						Type:       compat.EventDone,
						DoneReason: response.StopReason,
					}); err != nil {
						return compat.Response{}, err
					}
				}
				return response, nil
			}
		}
	}
}

func (p *Provider) discoverAppServerModels(ctx context.Context) ([]provider.Model, error) {
	token, err := readCodexAccessToken(p.authPath)
	if err != nil {
		return nil, err
	}
	client, err := newRPCClient(ctx, p.appServerURL, token, p.dialer)
	if err != nil {
		return nil, err
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}
	var response codexModelListResponse
	if err := client.call(ctx, "model/list", map[string]any{"forceRefetch": true}, &response); err != nil {
		return nil, err
	}
	cache := readCodexModelsCache(filepath.Dir(p.authPath))
	return codexModelsFromAppServer(response.Data, cache, codexModelCapabilities(p.registration)), nil
}

type codexModelListResponse struct {
	Data []codexAppServerModel `json:"data"`
}

type codexAppServerModel struct {
	ID               string `json:"id"`
	Model            string `json:"model"`
	DisplayName      string `json:"displayName"`
	Hidden           bool   `json:"hidden"`
	ContextWindow    int    `json:"contextWindow"`
	MaxContextWindow int    `json:"maxContextWindow"`
}

type codexModelsCache struct {
	Models []codexCachedModel `json:"models"`
}

type codexCachedModel struct {
	Slug             string `json:"slug"`
	DisplayName      string `json:"display_name"`
	ContextWindow    int    `json:"context_window"`
	MaxContextWindow int    `json:"max_context_window"`
}

func readCodexModelsCache(dir string) map[string]codexCachedModel {
	path := filepath.Join(strings.TrimSpace(dir), "models_cache.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache codexModelsCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil
	}
	out := make(map[string]codexCachedModel, len(cache.Models))
	for _, model := range cache.Models {
		slug := strings.TrimSpace(model.Slug)
		if slug == "" {
			continue
		}
		out[slug] = model
	}
	return out
}

func codexModelsFromAppServer(items []codexAppServerModel, cache map[string]codexCachedModel, capabilities []provider.Capability) []provider.Model {
	models := make([]provider.Model, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.Hidden {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.Model)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cached := cache[id]
		contextWindow := item.ContextWindow
		if contextWindow == 0 {
			contextWindow = cached.ContextWindow
		}
		maxContextWindow := item.MaxContextWindow
		if maxContextWindow == 0 {
			maxContextWindow = cached.MaxContextWindow
		}
		if maxContextWindow == 0 {
			maxContextWindow = contextWindow
		}
		aliases := []string(nil)
		displayName := item.DisplayName
		if strings.TrimSpace(displayName) == "" {
			displayName = cached.DisplayName
		}
		if displayName := codexDisplayAlias(id, displayName); displayName != "" && displayName != id {
			aliases = []string{displayName}
		}
		models = append(models, provider.Model{
			ID:               id,
			Aliases:          aliases,
			Capabilities:     append([]provider.Capability(nil), capabilities...),
			ContextTokens:    contextWindow,
			MaxContextTokens: maxContextWindow,
		})
	}
	return models
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
	return base
}

func mergeModelAliases(base []string, discovered []string) []string {
	if len(base) == 0 {
		return append([]string(nil), discovered...)
	}
	seen := make(map[string]struct{}, len(base)+len(discovered))
	out := make([]string, 0, len(base)+len(discovered))
	for _, alias := range base {
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
	for _, alias := range discovered {
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

func mergeCapabilities(base []provider.Capability, discovered []provider.Capability) []provider.Capability {
	if len(base) == 0 {
		return append([]provider.Capability(nil), discovered...)
	}
	seen := make(map[provider.Capability]struct{}, len(base)+len(discovered))
	out := make([]provider.Capability, 0, len(base)+len(discovered))
	for _, capability := range base {
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	for _, capability := range discovered {
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func handleNotification(notification rpcNotification, turnID string, request compat.Request, emit func(compat.Event) error, builder *strings.Builder, usage *compat.Usage) (bool, error) {
	switch notification.Method {
	case "item/agentMessage/delta", "item/reasoning/textDelta", "item/reasoning/summaryTextDelta", "item/reasoning/delta":
		var delta struct {
			Delta  string `json:"delta"`
			TurnID string `json:"turnId"`
		}
		if err := json.Unmarshal(notification.Params, &delta); err != nil {
			return false, nil
		}
		if delta.TurnID != "" && delta.TurnID != turnID {
			return false, nil
		}
		if delta.Delta == "" {
			return false, nil
		}
		builder.WriteString(delta.Delta)
		if emit != nil {
			return false, emit(compat.Event{
				ResponseID:   request.ID,
				Dialect:      request.Dialect,
				Model:        request.Model,
				Type:         compat.EventContentDelta,
				ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: delta.Delta},
			})
		}
	case "thread/tokenUsage/updated":
		var update struct {
			TurnID     string `json:"turnId"`
			TokenUsage struct {
				Last tokenUsageBreakdown `json:"last"`
			} `json:"tokenUsage"`
		}
		if err := json.Unmarshal(notification.Params, &update); err != nil {
			return false, nil
		}
		if update.TurnID != "" && update.TurnID != turnID {
			return false, nil
		}
		converted := compat.Usage{
			InputTokens:  update.TokenUsage.Last.InputTokens,
			OutputTokens: update.TokenUsage.Last.OutputTokens,
			TotalTokens:  update.TokenUsage.Last.TotalTokens,
		}
		if converted.TotalTokens > 0 {
			*usage = converted
			if emit != nil {
				return false, emit(compat.Event{
					ResponseID: request.ID,
					Dialect:    request.Dialect,
					Model:      request.Model,
					Type:       compat.EventUsageDelta,
					UsageDelta: &converted,
				})
			}
		}
	case "turn/completed":
		completed, err := parseTurnCompleted(notification.Params)
		if err != nil {
			return false, nil
		}
		if completed.TurnID != "" && completed.TurnID != turnID {
			return false, nil
		}
		if completed.ErrorMessage != "" {
			return false, &provider.UpstreamError{Code: completed.ErrorCode, Message: completed.ErrorMessage}
		}
		return true, nil
	case "error":
		code, message := parseCodexErrorNotification(notification.Params)
		if code == "" {
			code = "codex_appserver_error"
		}
		return false, &provider.UpstreamError{Code: code, Message: message}
	}
	return false, nil
}

func (p *Provider) recordInvocationResult(usage compat.Usage, invokeErr error) {
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	now := time.Now().UTC()
	p.usage.ObservedAt = now
	p.usage.Source = usageSource
	p.usage.Requests++
	p.usage.InputTokens += usage.InputTokens
	p.usage.OutputTokens += usage.OutputTokens
	p.usage.TotalTokens += usage.TotalTokens
	if invokeErr != nil {
		p.health = provider.Health{Status: provider.HealthDegraded, Reason: invokeErr.Error(), CheckedAt: now}
		return
	}
	p.health = provider.Health{Status: provider.HealthReady, CheckedAt: now}
}

type codexAuthFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}

func readCodexAccessToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: auth path is required", ErrCodexProviderConfig)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read codex auth file: %w", err)
	}
	var auth codexAuthFile
	if err := json.Unmarshal(raw, &auth); err != nil {
		return "", fmt.Errorf("decode codex auth file: %w", err)
	}
	token := strings.TrimSpace(auth.Tokens.AccessToken)
	if token == "" {
		return "", fmt.Errorf("%w: auth file missing tokens.access_token", ErrCodexProviderConfig)
	}
	return token, nil
}

type turnStartParams struct {
	ThreadID       string      `json:"threadId"`
	Input          []userInput `json:"input"`
	Model          *string     `json:"model,omitempty"`
	Effort         *string     `json:"effort,omitempty"`
	ApprovalPolicy string      `json:"approvalPolicy,omitempty"`
}

type userInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
}

func turnStartParamsFromCanonical(request compat.Request, threadID string) (turnStartParams, func(), error) {
	inputs := make([]userInput, 0, len(request.Messages))
	cleanup := func() {}
	for _, message := range request.Messages {
		messageInputs, messageCleanup, err := codexInputsFromCanonicalMessage(message)
		if err != nil {
			cleanup()
			return turnStartParams{}, func() {}, err
		}
		inputs = append(inputs, messageInputs...)
		previousCleanup := cleanup
		cleanup = func() {
			messageCleanup()
			previousCleanup()
		}
	}
	if len(inputs) == 0 {
		return turnStartParams{}, cleanup, fmt.Errorf("%w: no input for codex turn", ErrCodexProviderConfig)
	}
	model := request.Model
	var effort *string
	if request.ReasoningEffort != "" {
		normalized := strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
		effort = &normalized
	}
	return turnStartParams{
		ThreadID:       threadID,
		Input:          inputs,
		Model:          &model,
		Effort:         effort,
		ApprovalPolicy: "never",
	}, cleanup, nil
}

func codexInputsFromCanonicalMessage(message compat.Message) ([]userInput, func(), error) {
	inputs := make([]userInput, 0, len(message.Content)+1)
	cleanup := func() {}
	text := canonicalMessageText(message)
	if strings.TrimSpace(text) != "" {
		inputs = append(inputs, userInput{Type: "text", Text: text})
	}
	for _, part := range message.Content {
		if part.Type != compat.ContentPartImage {
			continue
		}
		imageInput, imageCleanup, err := codexImageInput(part)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		inputs = append(inputs, imageInput)
		previousCleanup := cleanup
		cleanup = func() {
			imageCleanup()
			previousCleanup()
		}
	}
	if len(inputs) == 0 {
		return nil, cleanup, nil
	}
	return inputs, cleanup, nil
}

func canonicalMessageText(message compat.Message) string {
	var parts []string
	for _, part := range message.Content {
		if part.Type == compat.ContentPartText && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	for _, toolCall := range message.ToolCalls {
		name := strings.TrimSpace(toolCall.Name)
		if name == "" {
			name = "function"
		}
		parts = append(parts, fmt.Sprintf("[Tool call %s] %s", name, toolCall.Arguments))
	}
	text := strings.Join(parts, "\n")
	switch message.Role {
	case compat.MessageRoleSystem:
		return "[System] " + text
	case compat.MessageRoleDeveloper:
		return "[Developer] " + text
	case compat.MessageRoleAssistant:
		return "[Assistant] " + text
	case compat.MessageRoleTool:
		if message.ToolCallID != "" {
			return "[Tool result " + message.ToolCallID + "] " + text
		}
		return "[Tool result] " + text
	default:
		return text
	}
}

func codexImageInput(part compat.ContentPart) (userInput, func(), error) {
	if part.URL != "" {
		parsed, err := url.Parse(part.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return userInput{}, func() {}, fmt.Errorf("%w: codex image url must be http(s)", ErrCodexProviderConfig)
		}
		return userInput{Type: "image", URL: part.URL}, func() {}, nil
	}
	mime := strings.ToLower(strings.TrimSpace(part.MIME))
	if !supportedCodexImageMIME(mime) {
		return userInput{}, func() {}, fmt.Errorf("%w: unsupported codex image mime type %q", ErrCodexProviderConfig, part.MIME)
	}
	data, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		return userInput{}, func() {}, fmt.Errorf("%w: decode image attachment: %v", ErrCodexProviderConfig, err)
	}
	if len(data) == 0 || len(data) > maxCodexImageBytes {
		return userInput{}, func() {}, fmt.Errorf("%w: codex image attachment must be 1..%d bytes", ErrCodexProviderConfig, maxCodexImageBytes)
	}
	dir := filepath.Join(os.TempDir(), "pangaea-codex-attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return userInput{}, func() {}, err
	}
	file, err := os.CreateTemp(dir, "image-*"+codexImageExtension(mime))
	if err != nil {
		return userInput{}, func() {}, err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return userInput{}, func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return userInput{}, func() {}, err
	}
	return userInput{Type: "localImage", Path: path}, func() { _ = os.Remove(path) }, nil
}

func supportedCodexImageMIME(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif":
		return true
	}
	return false
}

func codexImageExtension(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

type threadStartResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type tokenUsageBreakdown struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
}

type turnCompleted struct {
	TurnID       string
	ErrorCode    string
	ErrorMessage string
}

func parseTurnCompleted(raw json.RawMessage) (turnCompleted, error) {
	var payload struct {
		TurnID string `json:"turnId"`
		Turn   struct {
			ID    string          `json:"id"`
			Error json.RawMessage `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return turnCompleted{}, err
	}
	turnID := payload.TurnID
	if turnID == "" {
		turnID = payload.Turn.ID
	}
	completed := turnCompleted{TurnID: turnID}
	if len(payload.Turn.Error) > 0 && string(payload.Turn.Error) != "null" {
		completed.ErrorCode, completed.ErrorMessage = parseCodexError(payload.Turn.Error)
	}
	return completed, nil
}

func parseCodexError(raw json.RawMessage) (string, string) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && asString != "" {
		return "", asString
	}
	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err != nil {
		return "", string(raw)
	}
	code := stringFromAny(asObject["code"])
	if code == "" {
		code = stringFromAny(asObject["type"])
	}
	message := stringFromAny(asObject["message"])
	if message == "" {
		message = string(raw)
	}
	return code, message
}

func parseCodexErrorNotification(raw json.RawMessage) (string, string) {
	var payload struct {
		Message string          `json:"message"`
		Code    string          `json:"code"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "codex_appserver_error", string(raw)
	}
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		code, message := parseCodexError(payload.Error)
		if message != "" {
			return code, message
		}
	}
	if payload.Message != "" {
		return payload.Code, payload.Message
	}
	return payload.Code, "codex appserver error"
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

type rpcClient struct {
	conn          *websocket.Conn
	writeMu       sync.Mutex
	requestsMu    sync.Mutex
	requests      map[string]chan rpcResponse
	notifications chan rpcNotification
	nextID        atomic.Uint64
	done          chan struct{}
	closeOnce     sync.Once
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (e rpcError) Error() string {
	if e.Message == "" {
		return "codex appserver JSON-RPC error"
	}
	if e.Code == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (code: %d)", e.Message, e.Code)
}

type rpcNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func newRPCClient(ctx context.Context, rawURL string, token string, dialer *websocket.Dialer) (*rpcClient, error) {
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := dialer.DialContext(ctx, rawURL, header)
	if err != nil {
		return nil, err
	}
	client := &rpcClient{
		conn:          conn,
		requests:      make(map[string]chan rpcResponse),
		notifications: make(chan rpcNotification, 1024),
		done:          make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

func (c *rpcClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"capabilities": map[string]any{"experimentalApi": true},
		"clientInfo":   map[string]any{"name": "pangaea-codex-provider", "version": "0.1.0"},
	}
	var response map[string]any
	return c.call(ctx, "initialize", params, &response)
}

func (c *rpcClient) call(ctx context.Context, method string, params any, result any) error {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	responseCh := make(chan rpcResponse, 1)
	c.requestsMu.Lock()
	c.requests[id] = responseCh
	c.requestsMu.Unlock()
	defer func() {
		c.requestsMu.Lock()
		delete(c.requests, id)
		c.requestsMu.Unlock()
	}()

	c.writeMu.Lock()
	err := c.conn.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	c.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("%w: codex appserver websocket closed", ErrCodexProviderConfig)
	case response, ok := <-responseCh:
		if !ok {
			return fmt.Errorf("%w: codex appserver websocket closed", ErrCodexProviderConfig)
		}
		if response.Error != nil {
			return response.Error
		}
		if result != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *rpcClient) readLoop() {
	defer c.close()
	for {
		var envelope struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
			Result json.RawMessage `json:"result,omitempty"`
			Error  *rpcError       `json:"error,omitempty"`
		}
		if err := c.conn.ReadJSON(&envelope); err != nil {
			return
		}
		if len(envelope.ID) > 0 && envelope.Method == "" {
			id := rpcIDKey(envelope.ID)
			if id == "" {
				continue
			}
			c.requestsMu.Lock()
			responseCh := c.requests[id]
			c.requestsMu.Unlock()
			if responseCh != nil {
				responseCh <- rpcResponse{ID: envelope.ID, Result: envelope.Result, Error: envelope.Error}
			}
			continue
		}
		if envelope.Method != "" && len(envelope.ID) > 0 {
			c.respondToServerRequest(envelope.ID, envelope.Method)
			continue
		}
		if envelope.Method != "" {
			select {
			case c.notifications <- rpcNotification{Method: envelope.Method, Params: envelope.Params}:
			case <-c.done:
				return
			}
		}
	}
}

func (c *rpcClient) respondToServerRequest(id json.RawMessage, method string) {
	var result any
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		result = map[string]any{"decision": "accept"}
	default:
		c.writeRawResponse(rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32601, Message: "unsupported Codex AppServer request: " + method},
		})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	c.writeRawResponse(rpcResponse{JSONRPC: "2.0", ID: id, Result: raw})
}

func (c *rpcClient) writeRawResponse(response rpcResponse) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.WriteJSON(response)
}

func (c *rpcClient) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.done)
		c.requestsMu.Lock()
		for _, ch := range c.requests {
			close(ch)
		}
		c.requests = make(map[string]chan rpcResponse)
		c.requestsMu.Unlock()
		_ = c.conn.Close()
	})
}

func rpcIDKey(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asNumber json.Number
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber.String()
	}
	return strings.Trim(string(raw), `"`)
}

func cloneModels(models []provider.Model) []provider.Model {
	if len(models) == 0 {
		return nil
	}
	out := make([]provider.Model, len(models))
	for i, model := range models {
		out[i] = model
		out[i].Aliases = append([]string(nil), model.Aliases...)
		out[i].Capabilities = append([]provider.Capability(nil), model.Capabilities...)
	}
	return out
}
