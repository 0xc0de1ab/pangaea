// Package cursoracp adapts the Cursor CLI Agent Client Protocol (`agent acp`) to
// Pangaea's canonical provider shim contract. Each shim Invoke starts a fresh
// agent subprocess: one ACP session per request (stateless from the router's
// perspective).
package cursoracp

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

const usageSourceACP = "cursor-acp"

var ErrConfig = errors.New("invalid cursor acp provider config")

type Options struct {
	Registration   provider.Registration
	AgentPath      string
	WorkingDir     string
	ExtraEnv       []string
	MCPServersJSON string
}

type Provider struct {
	registration provider.Registration
	agentPath    string
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
	agentPath := strings.TrimSpace(opts.AgentPath)
	if agentPath == "" {
		agentPath = strings.TrimSpace(os.Getenv("PANGAEA_CURSOR_AGENT_EXE"))
	}
	if agentPath == "" {
		agentPath = "agent"
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
		agentPath:    agentPath,
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

func (p *Provider) Registration() (provider.Registration, error) {
	if p == nil {
		return provider.Registration{}, ErrConfig
	}
	return p.registration, nil
}

func (p *Provider) ForceModelDiscovery() bool { return true }

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	if p == nil {
		return nil, ErrConfig
	}
	models, err := discoverCursorModels(ctx, p.agentPath, p.extraEnv, p.workingDir, p.registration.Capabilities)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("%w: cursor model discovery returned no models", ErrConfig)
	}
	return models, nil
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

	cl, err := newACPClient(ctx, p.agentPath, p.extraEnv, p.workingDir)
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
				return "", fmt.Errorf("cursor acp: content type %q is not supported", part.Type)
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
		return "", fmt.Errorf("cursor acp: empty conversational prompt")
	}
	return b.String(), nil
}
