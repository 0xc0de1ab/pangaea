// Package cliprovider adapts provider CLIs that only expose a one-shot prompt
// interface to Pangaea's canonical provider shim contract.
package cliprovider

import (
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
	usageSource           = "cli-oneshot"
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

type CommandRunner interface {
	RunCommand(context.Context, CommandSpec) (CommandResult, error)
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
	text, err := ExtractResponseText(p.service, result.Stdout)
	if err != nil {
		return compat.Response{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "[empty response]"
	}
	usage := compat.Usage{
		InputTokens:  estimateTokens(prompt),
		OutputTokens: estimateTokens(text),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
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
	return env
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
	raw := strings.TrimSpace(string(stdout))
	if raw == "" {
		return "", nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		if response, ok := decodeKnownCanonicalResponse(service, value); ok {
			return response, nil
		}
		if text := extractTextValue(value); strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return raw, nil
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
	stderr := strings.TrimSpace(string(result.Stderr))
	if stderr == "" {
		stderr = strings.TrimSpace(string(result.Stdout))
	}
	if stderr == "" {
		return fmt.Errorf("%w: command %s failed: %v", ErrCLIProviderConfig, commandName(command), err)
	}
	return fmt.Errorf("%w: command %s failed: %v: %s", ErrCLIProviderConfig, commandName(command), err, stderr)
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
	cmd.Env = os.Environ()
	for key, value := range spec.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
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
