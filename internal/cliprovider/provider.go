// Package cliprovider adapts provider CLIs that expose a prompt-oriented CLI
// interface to Pangaea's canonical provider shim contract.
package cliprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	defaultRequestTimeout = 5 * time.Minute
	usageSource           = "cli-adapter"
)

var ErrCLIProviderConfig = errors.New("invalid cli provider config")

type Options struct {
	Registration   provider.Registration
	Service        provider.Service
	Command        []string
	Env            map[string]string
	WorkingDir     string
	RequestTimeout time.Duration
	Runner         CommandRunner
	Now            func() time.Time
}

type Provider struct {
	registration   provider.Registration
	service        provider.Service
	command        []string
	env            map[string]string
	workingDir     string
	requestTimeout time.Duration
	runner         CommandRunner
	now            func() time.Time

	mu     sync.Mutex
	usage  provider.UsageReport
	health provider.Health
}

type CommandSpec struct {
	Command    []string
	Env        map[string]string
	WorkingDir string
}

type CommandResult struct {
	Stdout []byte
	Stderr []byte
}

type CommandLineHandler func([]byte) error

type CommandRunner interface {
	RunCommand(context.Context, CommandSpec) (CommandResult, error)
}

type StreamingCommandRunner interface {
	CommandRunner
	StreamCommand(context.Context, CommandSpec, CommandLineHandler) (CommandResult, error)
}

type CommandRunnerFunc func(context.Context, CommandSpec) (CommandResult, error)

func (f CommandRunnerFunc) RunCommand(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	return f(ctx, spec)
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCLIProviderConfig, err)
	}
	service := opts.Service
	if service == "" {
		service = opts.Registration.Identity.Service
	}
	if !service.Valid() {
		return nil, fmt.Errorf("%w: invalid service %q", ErrCLIProviderConfig, service)
	}
	if len(opts.Command) == 0 && !hasDefaultCommand(service) {
		return nil, fmt.Errorf("%w: no default command for service %q", ErrCLIProviderConfig, service)
	}
	timeout := opts.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = defaultCommandRunner{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	observedAt := now().UTC()
	return &Provider{
		registration:   opts.Registration,
		service:        service,
		command:        append([]string(nil), opts.Command...),
		env:            cloneStringMap(opts.Env),
		workingDir:     strings.TrimSpace(opts.WorkingDir),
		requestTimeout: timeout,
		runner:         runner,
		now:            now,
		usage: provider.UsageReport{
			ObservedAt: observedAt,
			Source:     usageSource,
		},
		health: provider.Health{Status: provider.HealthReady, CheckedAt: observedAt},
	}, nil
}

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrCLIProviderConfig
	}
	return p.registration, nil
}

func (p *Provider) Models(context.Context) ([]provider.Model, error) {
	if p == nil {
		return nil, ErrCLIProviderConfig
	}
	return cloneModels(p.registration.Models), nil
}

func (p *Provider) Usage() (provider.UsageReport, error) {
	if p == nil {
		return provider.UsageReport{}, ErrCLIProviderConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	usage := p.usage
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = p.now().UTC()
	}
	if usage.Source == "" {
		usage.Source = usageSource
	}
	return usage, nil
}

func (p *Provider) Health() (provider.Health, error) {
	if p == nil {
		return provider.Health{}, ErrCLIProviderConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	health := p.health
	if health.Status == "" {
		health.Status = provider.HealthReady
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = p.now().UTC()
	}
	return health, nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if p == nil {
		return compat.Response{}, ErrCLIProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrCLIProviderConfig)
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
	response, err := p.invoke(requestCtx, request)
	p.recordInvocationResult(response.Usage, err)
	return response, err
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if p == nil {
		return compat.Response{}, ErrCLIProviderConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrCLIProviderConfig)
	}
	if emit == nil {
		return compat.Response{}, fmt.Errorf("%w: stream emit callback is required", ErrCLIProviderConfig)
	}
	request.Stream = true
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	requestCtx := ctx
	if _, ok := requestCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, p.requestTimeout)
		defer cancel()
	}
	response, err := p.invokeStream(requestCtx, request, emit)
	p.recordInvocationResult(response.Usage, err)
	return response, err
}

func (p *Provider) invoke(ctx context.Context, request compat.Request) (compat.Response, error) {
	prompt, err := PromptFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	command, err := p.commandForRequest(request, prompt)
	if err != nil {
		return compat.Response{}, err
	}
	result, err := p.runner.RunCommand(ctx, CommandSpec{
		Command:    command,
		Env:        requestEnv(p.env, request, prompt),
		WorkingDir: p.workingDir,
	})
	if err != nil {
		return compat.Response{}, commandError(command, result, err)
	}
	extracted, err := ExtractResponse(p.service, result.Stdout)
	if err != nil {
		return compat.Response{}, err
	}
	text := strings.TrimSpace(extracted.Text)
	if text == "" {
		text = "[empty response]"
	}
	usage := extracted.Usage
	if usage.InputTokens == 0 {
		usage.InputTokens = estimateTokens(prompt)
	}
	if usage.OutputTokens == 0 {
		usage.OutputTokens = estimateTokens(text)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: text}},
		},
		StopReason: "stop",
		Usage:      usage,
	}, nil
}

func (p *Provider) invokeStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if p.service != provider.ServiceGemini {
		return p.invokeBufferedStream(ctx, request, emit)
	}
	streamRunner, ok := p.runner.(StreamingCommandRunner)
	if !ok {
		return p.invokeBufferedStream(ctx, request, emit)
	}
	prompt, err := PromptFromCanonical(request)
	if err != nil {
		return compat.Response{}, err
	}
	command, err := p.commandForRequest(request, prompt)
	if err != nil {
		return compat.Response{}, err
	}
	command = geminiStreamCommand(command)
	response := compat.Response{
		ID:      request.ID,
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{Role: compat.MessageRoleAssistant},
	}
	emittedStart := false
	emitStart := func() error {
		if emittedStart {
			return nil
		}
		emittedStart = true
		return emit(compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventMessageStart,
			Message:    &compat.Message{Role: compat.MessageRoleAssistant},
		})
	}
	var streamErr error
	var sawResult bool
	result, runErr := streamRunner.StreamCommand(ctx, CommandSpec{
		Command:    command,
		Env:        requestEnv(p.env, request, prompt),
		WorkingDir: p.workingDir,
	}, func(line []byte) error {
		event, ok, err := parseGeminiStreamJSONLine(line)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		switch event.Type {
		case "init":
			if event.SessionID != "" && response.ID == "" {
				response.ID = event.SessionID
			}
			if event.Model != "" {
				response.Model = event.Model
			}
		case "message":
			if event.Role != "assistant" || strings.TrimSpace(event.Content) == "" {
				return nil
			}
			if err := emitStart(); err != nil {
				return err
			}
			response.Message.Content = appendTextPart(response.Message.Content, event.Content)
			return emit(compat.Event{
				ResponseID: response.ID,
				Dialect:    response.Dialect,
				Model:      response.Model,
				Type:       compat.EventContentDelta,
				ContentDelta: &compat.ContentPart{
					Type: compat.ContentPartText,
					Text: event.Content,
				},
			})
		case "result":
			sawResult = true
			if event.Status == "error" {
				streamErr = geminiStreamError(event)
				return nil
			}
			usage := geminiUsageFromStats(event.Stats)
			if usage != (compat.Usage{}) {
				response.Usage = usage
				if err := emitStart(); err != nil {
					return err
				}
				return emit(compat.Event{
					ResponseID: response.ID,
					Dialect:    response.Dialect,
					Model:      response.Model,
					Type:       compat.EventUsageDelta,
					UsageDelta: &usage,
				})
			}
		}
		return nil
	})
	if streamErr != nil {
		return response, streamErr
	}
	if runErr != nil {
		return response, commandError(command, result, runErr)
	}
	if !sawResult && len(response.Message.Content) == 0 {
		return response, fmt.Errorf("%w: command %s produced no Gemini stream result", ErrCLIProviderConfig, commandName(command))
	}
	if len(response.Message.Content) == 0 {
		response.Message.Content = []compat.ContentPart{{Type: compat.ContentPartText, Text: "[empty response]"}}
		if err := emitStart(); err != nil {
			return response, err
		}
		if err := emit(compat.Event{
			ResponseID:   response.ID,
			Dialect:      response.Dialect,
			Model:        response.Model,
			Type:         compat.EventContentDelta,
			ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: "[empty response]"},
		}); err != nil {
			return response, err
		}
	}
	if response.Usage.InputTokens == 0 {
		response.Usage.InputTokens = estimateTokens(prompt)
	}
	if response.Usage.OutputTokens == 0 {
		response.Usage.OutputTokens = estimateTokens(contentText(response.Message.Content))
	}
	if response.Usage.TotalTokens == 0 {
		response.Usage.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens
	}
	response.StopReason = "stop"
	if err := emitStart(); err != nil {
		return response, err
	}
	if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventDone, DoneReason: response.StopReason}); err != nil {
		return response, err
	}
	return response, nil
}

func (p *Provider) invokeBufferedStream(ctx context.Context, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	response, err := p.invoke(ctx, request)
	if err != nil {
		return compat.Response{}, err
	}
	events, err := compat.EventsFromResponse(response)
	if err != nil {
		return compat.Response{}, err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return compat.Response{}, err
		}
	}
	return response, nil
}

func (p *Provider) commandForRequest(request compat.Request, prompt string) ([]string, error) {
	if len(p.command) > 0 {
		return expandCommandTemplate(p.command, request, prompt), nil
	}
	switch p.service {
	case provider.ServiceClaude:
		return claudeCommand(request, prompt), nil
	case provider.ServiceGemini:
		return geminiCommand(request, prompt), nil
	default:
		return nil, fmt.Errorf("%w: no default command for service %q", ErrCLIProviderConfig, p.service)
	}
}

func hasDefaultCommand(service provider.Service) bool {
	switch service {
	case provider.ServiceClaude, provider.ServiceGemini:
		return true
	default:
		return false
	}
}

func claudeCommand(request compat.Request, prompt string) []string {
	command := []string{"claude", "-p", prompt, "--permission-mode", "plan", "--tools", "", "--output-format", "text"}
	if shouldForwardModel(request.Model, "claude-default") {
		command = append(command, "--model", request.Model)
	}
	return command
}

func geminiCommand(request compat.Request, prompt string) []string {
	model := strings.TrimSpace(request.Model)
	if model == "" || model == "gemini-default" {
		model = "gemini-2.5-flash"
	}
	return []string{"gemini", "-p", prompt, "--skip-trust", "--approval-mode", "plan", "--output-format", "json", "--model", model}
}

func geminiStreamCommand(command []string) []string {
	out := append([]string(nil), command...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "--output-format" {
			out[i+1] = "stream-json"
			return out
		}
	}
	return append(out, "--output-format", "stream-json")
}

func shouldForwardModel(model string, aliases ...string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, alias := range aliases {
		if model == alias {
			return false
		}
	}
	return true
}

func requestEnv(base map[string]string, request compat.Request, prompt string) map[string]string {
	env := cloneStringMap(base)
	if env == nil {
		env = map[string]string{}
	}
	env["PANGAEA_REQUEST_ID"] = request.ID
	env["PANGAEA_REQUEST_DIALECT"] = string(request.Dialect)
	env["PANGAEA_REQUEST_MODEL"] = request.Model
	env["PANGAEA_REQUEST_PROMPT"] = prompt
	if request.MaxOutputTokens > 0 {
		env["PANGAEA_REQUEST_MAX_OUTPUT_TOKENS"] = strconv.Itoa(request.MaxOutputTokens)
	}
	if request.Temperature != nil {
		env["PANGAEA_REQUEST_TEMPERATURE"] = strconv.FormatFloat(*request.Temperature, 'f', -1, 64)
	}
	ensureCLIEnvironment(env)
	return env
}

func ensureCLIEnvironment(env map[string]string) {
	term := strings.TrimSpace(env["TERM"])
	if term == "" || term == "dumb" {
		env["TERM"] = "xterm-256color"
	}
	if strings.TrimSpace(env["COLORTERM"]) == "" {
		env["COLORTERM"] = "truecolor"
	}
	if strings.TrimSpace(env["FORCE_COLOR"]) == "" {
		env["FORCE_COLOR"] = "1"
	}
}

func expandCommandTemplate(command []string, request compat.Request, prompt string) []string {
	out := make([]string, 0, len(command))
	for _, arg := range command {
		arg = strings.ReplaceAll(arg, "{{prompt}}", prompt)
		arg = strings.ReplaceAll(arg, "{{model}}", request.Model)
		arg = strings.ReplaceAll(arg, "{{dialect}}", string(request.Dialect))
		arg = strings.ReplaceAll(arg, "{{request_id}}", request.ID)
		if request.MaxOutputTokens > 0 {
			arg = strings.ReplaceAll(arg, "{{max_output_tokens}}", strconv.Itoa(request.MaxOutputTokens))
		}
		out = append(out, arg)
	}
	return out
}

func PromptFromCanonical(request compat.Request) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, message := range request.Messages {
		role := strings.TrimSpace(string(message.Role))
		if role == "" {
			role = "message"
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[")
		b.WriteString(role)
		if message.Name != "" {
			b.WriteString(" name=")
			b.WriteString(message.Name)
		}
		if message.ToolCallID != "" {
			b.WriteString(" tool_call_id=")
			b.WriteString(message.ToolCallID)
		}
		b.WriteString("]\n")
		for _, part := range message.Content {
			if part.Type != compat.ContentPartText {
				return "", compat.ErrInvalidRequest
			}
			b.WriteString(part.Text)
		}
		for _, call := range message.ToolCalls {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("[tool_call")
			if call.ID != "" {
				b.WriteString(" id=")
				b.WriteString(call.ID)
			}
			if call.Name != "" {
				b.WriteString(" name=")
				b.WriteString(call.Name)
			}
			b.WriteString("]\n")
			b.WriteString(call.Arguments)
		}
	}
	prompt := strings.TrimSpace(b.String())
	if prompt == "" {
		return "", compat.ErrInvalidRequest
	}
	return prompt, nil
}

func ExtractResponseText(service provider.Service, stdout []byte) (string, error) {
	extracted, err := ExtractResponse(service, stdout)
	if err != nil {
		return "", err
	}
	return extracted.Text, nil
}

type ExtractedResponse struct {
	Text  string
	Usage compat.Usage
}

func ExtractResponse(service provider.Service, stdout []byte) (ExtractedResponse, error) {
	raw := strings.TrimSpace(string(stdout))
	if raw == "" {
		return ExtractedResponse{}, nil
	}
	if service == provider.ServiceGemini && looksLikeGeminiStreamJSON(raw) {
		if extracted, ok, err := extractGeminiStreamJSON(raw); ok || err != nil {
			return extracted, err
		}
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		if response, ok := decodeKnownCanonicalResponse(service, value); ok {
			return ExtractedResponse{Text: response, Usage: usageFromJSONValue(service, value)}, nil
		}
		if text := extractTextValue(value); strings.TrimSpace(text) != "" {
			return ExtractedResponse{Text: text, Usage: usageFromJSONValue(service, value)}, nil
		}
	}
	return ExtractedResponse{Text: raw}, nil
}

func decodeKnownCanonicalResponse(service provider.Service, value any) (string, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	switch service {
	case provider.ServiceGemini:
		var gemini compat.GeminiGenerateContentResponse
		if err := json.Unmarshal(raw, &gemini); err == nil && len(gemini.Candidates) > 0 {
			if gemini.ModelVersion == "" {
				gemini.ModelVersion = "gemini-cli"
			}
			if response, err := compat.GeminiGenerateContentResponseToCanonical(gemini); err == nil {
				return contentText(response.Message.Content), true
			}
		}
	case provider.ServiceClaude:
		var anthropic compat.AnthropicMessagesResponse
		if err := json.Unmarshal(raw, &anthropic); err == nil && len(anthropic.Content) > 0 {
			if anthropic.Model == "" {
				anthropic.Model = "claude-cli"
			}
			if response, err := compat.AnthropicMessagesResponseToCanonical(anthropic); err == nil {
				return contentText(response.Message.Content), true
			}
		}
	}
	return "", false
}

func extractTextValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractTextValue(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"response", "text", "output", "content", "message", "answer"} {
			if item, ok := typed[key]; ok {
				if text := extractTextValue(item); strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
		parts := []string{}
		for key, item := range typed {
			if key == "usage" || key == "usageMetadata" || key == "metadata" {
				continue
			}
			if text := extractTextValue(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func looksLikeGeminiStreamJSON(raw string) bool {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, `{"type":`) || strings.Contains(line, `"type":"`)
	}
	return false
}

type geminiStreamJSONEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Model     string          `json:"model,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Delta     bool            `json:"delta,omitempty"`
	Status    string          `json:"status,omitempty"`
	Error     *geminiCLIError `json:"error,omitempty"`
	Stats     map[string]any  `json:"stats,omitempty"`
}

type geminiCLIError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

func parseGeminiStreamJSONLine(line []byte) (geminiStreamJSONEvent, bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return geminiStreamJSONEvent{}, false, nil
	}
	var event geminiStreamJSONEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return geminiStreamJSONEvent{}, false, fmt.Errorf("%w: invalid Gemini stream-json line: %v", ErrCLIProviderConfig, err)
	}
	if event.Type == "" {
		return geminiStreamJSONEvent{}, false, nil
	}
	return event, true, nil
}

func extractGeminiStreamJSON(raw string) (ExtractedResponse, bool, error) {
	var text strings.Builder
	var usage compat.Usage
	saw := false
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		event, ok, err := parseGeminiStreamJSONLine(scanner.Bytes())
		if err != nil {
			return ExtractedResponse{}, saw, err
		}
		if !ok {
			continue
		}
		saw = true
		switch event.Type {
		case "message":
			if event.Role == "assistant" {
				text.WriteString(event.Content)
			}
		case "result":
			if event.Status == "error" {
				return ExtractedResponse{}, true, geminiStreamError(event)
			}
			usage = geminiUsageFromStats(event.Stats)
		}
	}
	if err := scanner.Err(); err != nil {
		return ExtractedResponse{}, saw, err
	}
	return ExtractedResponse{Text: text.String(), Usage: usage}, saw, nil
}

func geminiStreamError(event geminiStreamJSONEvent) error {
	message := "Gemini CLI stream failed"
	code := "gemini_cli_error"
	if event.Error != nil {
		if strings.TrimSpace(event.Error.Message) != "" {
			message = strings.TrimSpace(event.Error.Message)
		}
		if strings.TrimSpace(event.Error.Code) != "" {
			code = strings.TrimSpace(event.Error.Code)
		} else if strings.TrimSpace(event.Error.Type) != "" {
			code = strings.TrimSpace(event.Error.Type)
		}
	}
	return &provider.UpstreamError{
		StatusCode: upstreamStatusFromGeminiError(message),
		Code:       code,
		Message:    message,
	}
}

func upstreamStatusFromGeminiError(message string) int {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "quota"):
		return 429
	case strings.Contains(lower, "not found") || strings.Contains(lower, "requested entity"):
		return 404
	case strings.Contains(lower, "unauth") || strings.Contains(lower, "permission") || strings.Contains(lower, "forbidden"):
		return 401
	default:
		return 502
	}
}

func usageFromJSONValue(service provider.Service, value any) compat.Usage {
	if service != provider.ServiceGemini {
		return compat.Usage{}
	}
	root, ok := value.(map[string]any)
	if !ok {
		return compat.Usage{}
	}
	return geminiUsageFromStats(asMap(root["stats"]))
}

func geminiUsageFromStats(stats map[string]any) compat.Usage {
	if len(stats) == 0 {
		return compat.Usage{}
	}
	usage := compat.Usage{
		InputTokens:  int64Number(firstPresent(stats, "input_tokens", "input", "prompt_tokens", "prompt")),
		OutputTokens: int64Number(firstPresent(stats, "output_tokens", "output", "candidates_tokens", "candidates")),
		TotalTokens:  int64Number(firstPresent(stats, "total_tokens", "total")),
	}
	if usage.InputTokens == 0 || usage.OutputTokens == 0 || usage.TotalTokens == 0 {
		for _, modelValue := range asMap(stats["models"]) {
			modelStats := asMap(modelValue)
			if tokens := asMap(modelStats["tokens"]); len(tokens) > 0 {
				usage.InputTokens += int64Number(firstPresent(tokens, "input", "prompt", "input_tokens", "prompt_tokens"))
				usage.OutputTokens += int64Number(firstPresent(tokens, "candidates", "output", "output_tokens", "candidates_tokens"))
				usage.TotalTokens += int64Number(firstPresent(tokens, "total", "total_tokens"))
				continue
			}
			usage.InputTokens += int64Number(firstPresent(modelStats, "input_tokens", "input", "prompt_tokens", "prompt"))
			usage.OutputTokens += int64Number(firstPresent(modelStats, "output_tokens", "output", "candidates_tokens", "candidates"))
			usage.TotalTokens += int64Number(firstPresent(modelStats, "total_tokens", "total"))
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func int64Number(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		out, _ := typed.Int64()
		return out
	case string:
		out, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return out
	default:
		return 0
	}
}

func appendTextPart(parts []compat.ContentPart, text string) []compat.ContentPart {
	if len(parts) > 0 {
		last := &parts[len(parts)-1]
		if last.Type == compat.ContentPartText {
			last.Text += text
			return parts
		}
	}
	return append(parts, compat.ContentPart{Type: compat.ContentPartText, Text: text})
}

func contentText(parts []compat.ContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Type == compat.ContentPartText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func estimateTokens(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	count := int64(math.Ceil(float64(len([]rune(text))) / 4.0))
	if count < 1 {
		return 1
	}
	return count
}

func commandError(command []string, result CommandResult, err error) error {
	stderr := strings.TrimSpace(filterCLIAdvisories(string(result.Stderr)))
	if stderr == "" {
		stderr = strings.TrimSpace(string(result.Stdout))
	}
	if stderr == "" {
		return fmt.Errorf("%w: command %s failed: %v", ErrCLIProviderConfig, commandName(command), err)
	}
	return fmt.Errorf("%w: command %s failed: %v: %s", ErrCLIProviderConfig, commandName(command), err, stderr)
}

func filterCLIAdvisories(stderr string) string {
	lines := strings.Split(stderr, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "Warning: Basic terminal detected"):
			continue
		case strings.HasPrefix(trimmed, "Warning: 256-color support not detected"):
			continue
		case strings.HasPrefix(trimmed, "Ripgrep is not available. Falling back to GrepTool."):
			continue
		default:
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func commandName(command []string) string {
	if len(command) == 0 {
		return "<empty>"
	}
	return command[0]
}

func (p *Provider) recordInvocationResult(usage compat.Usage, invokeErr error) {
	p.recordUsage(usage, invokeErr)
	p.recordHealth(invokeErr)
}

func (p *Provider) recordUsage(usage compat.Usage, invokeErr error) {
	if p == nil || invokeErr != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.usage.Source == "" {
		p.usage.Source = usageSource
	}
	p.usage.Requests++
	p.usage.InputTokens += usage.InputTokens
	p.usage.OutputTokens += usage.OutputTokens
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	p.usage.TotalTokens += total
	p.usage.ObservedAt = p.now().UTC()
}

func (p *Provider) recordHealth(invokeErr error) {
	if p == nil {
		return
	}
	now := p.now().UTC()
	health := provider.Health{Status: provider.HealthReady, CheckedAt: now}
	if invokeErr != nil {
		if errors.Is(invokeErr, context.Canceled) || errors.Is(invokeErr, context.DeadlineExceeded) {
			return
		}
		health.Status = provider.HealthDegraded
		health.Reason = invokeErr.Error()
	}
	p.mu.Lock()
	p.health = health
	p.mu.Unlock()
}

type defaultCommandRunner struct{}

func (defaultCommandRunner) RunCommand(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return CommandResult{}, fmt.Errorf("%w: command is required", ErrCLIProviderConfig)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	cmd.Dir = strings.TrimSpace(spec.WorkingDir)
	cmd.Env = commandEnv(spec.Env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

func (defaultCommandRunner) StreamCommand(ctx context.Context, spec CommandSpec, onStdoutLine CommandLineHandler) (CommandResult, error) {
	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return CommandResult{}, fmt.Errorf("%w: command is required", ErrCLIProviderConfig)
	}
	if onStdoutLine == nil {
		return CommandResult{}, fmt.Errorf("%w: stdout line handler is required", ErrCLIProviderConfig)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	cmd.Dir = strings.TrimSpace(spec.WorkingDir)
	cmd.Env = commandEnv(spec.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CommandResult{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return CommandResult{Stderr: stderr.Bytes()}, err
	}
	var stdoutCopy bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var handlerErr error
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		stdoutCopy.Write(line)
		stdoutCopy.WriteByte('\n')
		if err := onStdoutLine(line); err != nil {
			handlerErr = err
			_ = cmd.Process.Kill()
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && handlerErr == nil {
		handlerErr = scanErr
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	result := CommandResult{Stdout: stdoutCopy.Bytes(), Stderr: stderr.Bytes()}
	if handlerErr != nil {
		return result, handlerErr
	}
	return result, waitErr
}

func commandEnv(extra map[string]string) []string {
	envMap := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			envMap[key] = value
		}
	}
	for key, value := range extra {
		envMap[key] = value
	}
	ensureCLIEnvironment(envMap)
	out := make([]string, 0, len(envMap))
	for key, value := range envMap {
		out = append(out, key+"="+value)
	}
	return out
}

func cloneModels(in []provider.Model) []provider.Model {
	if len(in) == 0 {
		return nil
	}
	out := make([]provider.Model, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
