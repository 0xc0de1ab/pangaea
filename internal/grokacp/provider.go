// Package grokacp adapts Grok Build CLI's Agent Client Protocol
// (`grok agent stdio`) to Pangaea's canonical provider shim contract.
package grokacp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const usageSourceACP = "grok-build-acp"

var ErrConfig = errors.New("invalid grok build acp provider config")

type Options struct {
	Registration   provider.Registration
	GrokPath       string
	WorkingDir     string
	ExtraEnv       []string
	MCPServersJSON string
}

type Provider struct {
	registration provider.Registration
	grokPath     string
	workingDir   string
	extraEnv     []string
	mcpServers   []any

	mu       sync.Mutex
	usage    provider.UsageReport
	healthMu sync.Mutex
	health   provider.Health
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	mcpServers, err := parseMCPServersJSON(opts.MCPServersJSON)
	if err != nil {
		return nil, err
	}
	opts.Registration.Models = annotateGrokModels(opts.Registration.Models, opts.Registration.Capabilities)
	opts.Registration.Auth = localAuthState(opts.Registration.Auth)

	grokPath := strings.TrimSpace(opts.GrokPath)
	if grokPath == "" {
		grokPath = strings.TrimSpace(os.Getenv("PANGAEA_GROK_CLI_EXE"))
	}
	if grokPath == "" {
		grokPath = "grok"
	}
	workDir := strings.TrimSpace(opts.WorkingDir)
	if workDir == "" {
		workDir = "."
	}
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}
	now := time.Now().UTC()
	h := opts.Registration.Health
	if strings.TrimSpace(string(h.Status)) == "" {
		h.Status = provider.HealthReady
	}
	h.CheckedAt = now
	return &Provider{
		registration: opts.Registration,
		grokPath:     grokPath,
		workingDir:   workDir,
		extraEnv:     append([]string(nil), opts.ExtraEnv...),
		mcpServers:   mcpServers,
		usage: provider.UsageReport{
			ObservedAt: now,
			Source:     usageSourceACP,
		},
		health: h,
	}, nil
}

func localAuthState(auth provider.AuthState) provider.AuthState {
	if auth.Status != "" && auth.Status != provider.AuthNoLogin && auth.Status != provider.AuthUnknown {
		return auth
	}
	switch {
	case strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "":
		auth.Status = provider.AuthHealthy
		auth.SelectedSource = "env:XAI_API_KEY"
	case strings.TrimSpace(os.Getenv("GROK_CODE_XAI_API_KEY")) != "":
		auth.Status = provider.AuthHealthy
		auth.SelectedSource = "env:GROK_CODE_XAI_API_KEY"
	case fileExists(defaultGrokAuthPath()):
		auth.Status = provider.AuthHealthy
		auth.SelectedSource = "file:" + defaultGrokAuthPath()
	default:
		if auth.Status == "" || auth.Status == provider.AuthUnknown {
			auth.Status = provider.AuthNoLogin
			auth.SelectedSource = "none"
			auth.LastRefreshErr = "grok login cache or XAI_API_KEY is not configured"
		}
		return auth
	}
	if auth.Account.Display == "" {
		auth.Account.Display = firstNonEmpty(os.Getenv("PANGAEA_ACCOUNT_DISPLAY"), os.Getenv("PANGAEA_ACCOUNT"))
	}
	auth.LastRefreshErr = ""
	return auth
}

func defaultGrokAuthPath() string {
	if path := strings.TrimSpace(os.Getenv("PANGAEA_GROK_AUTH_PATH")); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv("PANGAEA_AUTH_PATH")); path != "" {
		return path
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".grok", "auth.json")
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
	return annotateGrokModels(p.registration.Models, p.registration.Capabilities), nil
}

func annotateGrokModels(in []provider.Model, capabilities []provider.Capability) []provider.Model {
	if len(in) == 0 {
		return []provider.Model{defaultGrokBuildModel(capabilities)}
	}
	out := make([]provider.Model, len(in))
	copy(out, in)
	for i := range out {
		switch strings.ToLower(strings.TrimSpace(out[i].ID)) {
		case "grok-build":
			out[i].Aliases = appendStringUnique(out[i].Aliases, "grok-build-default")
			out[i].Aliases = appendStringUnique(out[i].Aliases, "grok-build-0.1")
			out[i].Aliases = appendStringUnique(out[i].Aliases, "grok-default")
		case "grok-build-default", "grok-default":
			out[i].Kind = "alias"
		}
		out[i].Capabilities = modelCapabilities(out[i].Capabilities, capabilities)
	}
	return out
}

func defaultGrokBuildModel(capabilities []provider.Capability) provider.Model {
	return provider.Model{
		ID:               "grok-build",
		Aliases:          []string{"grok-build-default", "grok-build-0.1", "grok-default"},
		Capabilities:     modelCapabilities(nil, capabilities),
		ContextTokens:    512_000,
		MaxContextTokens: 512_000,
	}
}

func modelCapabilities(existing []provider.Capability, registration []provider.Capability) []provider.Capability {
	if len(existing) > 0 {
		return dedupeCapabilities(existing)
	}
	out := make([]provider.Capability, 0, len(registration))
	for _, capability := range registration {
		switch capability {
		case provider.CapabilityOpenAIChat,
			provider.CapabilityOpenAIResponses,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
			provider.CapabilityAgentToolUse,
			provider.CapabilityAgentWorkspaceRead,
			provider.CapabilityAgentWorkspaceWrite,
			provider.CapabilityAgentTerminal:
			out = append(out, capability)
		}
	}
	return dedupeCapabilities(out)
}

func appendStringUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func dedupeCapabilities(in []provider.Capability) []provider.Capability {
	out := make([]provider.Capability, 0, len(in))
	seen := map[provider.Capability]struct{}{}
	for _, capability := range in {
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
		u.Source = usageSourceACP
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

func (p *Provider) Auth() (provider.AuthState, error) {
	if p == nil {
		return provider.AuthState{}, ErrConfig
	}
	return p.registration.Auth, nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	return p.InvokeStream(ctx, registration, request, nil)
}

func (p *Provider) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if p == nil {
		return compat.Response{}, ErrConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}

	userText, err := flattenCompatMessages(request)
	if err != nil {
		p.recordUsageFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}

	cl, err := newACPClient(ctx, p.grokPath, p.extraEnv, p.workingDir)
	if err != nil {
		p.recordUsageFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}
	defer func() { _ = cl.Close() }()

	sid, err := handshake(ctx, cl, p.workingDir, p.mcpServers)
	if err != nil {
		p.recordUsageFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}

	stopReason, reply, err := sessionPrompt(ctx, cl, sid, userText, request, request.Stream, emit)
	if err != nil {
		p.recordUsageFailure()
		p.recordHealth(err)
		return compat.Response{}, err
	}

	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "[empty response]"
	}
	if strings.TrimSpace(stopReason) == "" {
		stopReason = "stop"
	}

	p.recordUsageSuccess()
	p.recordHealth(nil)

	return compat.Response{
		ID:         request.ID,
		Dialect:    request.Dialect,
		Model:      request.Model,
		StopReason: stopReason,
		Message: compat.Message{
			Role: compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: reply,
			}},
		},
	}, nil
}

func (p *Provider) recordUsageFailure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage.ObservedAt = time.Now().UTC()
	p.usage.Source = usageSourceACP
}

func (p *Provider) recordUsageSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage.ObservedAt = time.Now().UTC()
	p.usage.Source = usageSourceACP
	p.usage.Requests++
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
		health.Status = provider.HealthDegraded
		health.Reason = "provider invoke failed"
	}
	p.healthMu.Lock()
	p.health = health
	p.healthMu.Unlock()
}

func flattenCompatMessages(request compat.Request) (string, error) {
	var b strings.Builder
	if len(request.Tools) > 0 {
		fmt.Fprintf(&b, "[tools-declared]\n")
		for _, t := range request.Tools {
			name := strings.TrimSpace(t.Name)
			if name == "" {
				continue
			}
			if desc := strings.TrimSpace(t.Description); desc != "" {
				fmt.Fprintf(&b, "- %s: %s\n", name, desc)
			} else {
				fmt.Fprintf(&b, "- %s\n", name)
			}
		}
		b.WriteString("\n")
	}

	for _, m := range request.Messages {
		hasPayload := len(m.ToolCalls) > 0 || m.ToolCallID != ""
		for _, part := range m.Content {
			switch part.Type {
			case compat.ContentPartText:
				if strings.TrimSpace(part.Text) != "" {
					hasPayload = true
				}
			case compat.ContentPartImage:
				hasPayload = true
			default:
				return "", fmt.Errorf("grok build acp: content type %q is not supported", part.Type)
			}
		}
		if !hasPayload {
			continue
		}

		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if len(m.ToolCalls) > 0 {
			fmt.Fprintf(&b, "[%s tool_calls]\n", m.Role)
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "- id=%s name=%s args=%s\n", tc.ID, tc.Name, tc.Arguments)
			}
		}
		if m.ToolCallID != "" {
			fmt.Fprintf(&b, "[%s tool_result tool_call_id=%s]\n", m.Role, m.ToolCallID)
		}
		rolePrinted := len(m.ToolCalls) > 0 || m.ToolCallID != ""

		for _, part := range m.Content {
			switch part.Type {
			case compat.ContentPartText:
				text := strings.TrimSpace(part.Text)
				if text == "" {
					continue
				}
				if !rolePrinted {
					fmt.Fprintf(&b, "[%s]\n", m.Role)
					rolePrinted = true
				}
				b.WriteString(text)
				b.WriteString("\n")
			case compat.ContentPartImage:
				if !rolePrinted {
					fmt.Fprintf(&b, "[%s]\n", m.Role)
					rolePrinted = true
				}
				fmt.Fprintf(&b, "[image mime=%s bytes=%d omitted]\n", part.MIME, len(part.Data))
			}
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("grok build acp: empty conversational prompt")
	}
	return b.String(), nil
}
