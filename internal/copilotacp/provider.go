// Package copilotacp adapts GitHub Copilot CLI's Agent Client Protocol server
// to Pangaea's canonical provider shim contract.
package copilotacp

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

const usageSourceACP = "github-copilot-acp"

var ErrConfig = errors.New("invalid github copilot acp provider config")

type Options struct {
	Registration provider.Registration
	CopilotPath  string
	WorkingDir   string
	ExtraEnv     []string
}

type Provider struct {
	registration provider.Registration
	copilotPath  string
	workingDir   string
	extraEnv     []string

	mu    sync.Mutex
	usage provider.UsageReport
}

func New(opts Options) (*Provider, error) {
	if err := opts.Registration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	copilotPath := strings.TrimSpace(opts.CopilotPath)
	if copilotPath == "" {
		copilotPath = strings.TrimSpace(os.Getenv("PANGAEA_COPILOT_CLI_EXE"))
	}
	if copilotPath == "" {
		copilotPath = "copilot"
	}
	workDir := strings.TrimSpace(opts.WorkingDir)
	if workDir == "" {
		workDir = "."
	}
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}
	now := time.Now().UTC()
	return &Provider{
		registration: opts.Registration,
		copilotPath:  copilotPath,
		workingDir:   workDir,
		extraEnv:     append([]string(nil), opts.ExtraEnv...),
		usage: provider.UsageReport{
			ObservedAt: now,
			Source:     usageSourceACP,
		},
	}, nil
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
		u.Source = usageSourceACP
	}
	return u, nil
}

func (p *Provider) Health() (provider.Health, error) {
	if p == nil {
		return provider.Health{}, ErrConfig
	}
	return provider.Health{Status: provider.HealthReady, CheckedAt: time.Now().UTC()}, nil
}

func (p *Provider) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if p == nil {
		return compat.Response{}, ErrConfig
	}
	if registration.Identity.ProviderInstanceID != p.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("%w: provider instance mismatch", ErrConfig)
	}
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	if len(request.Tools) > 0 {
		return compat.Response{}, fmt.Errorf("%w: tools are not supported for github copilot acp yet", ErrConfig)
	}
	if request.Stream {
		return compat.Response{}, fmt.Errorf("%w: set stream=false for github copilot acp (non-streaming shim)", ErrConfig)
	}
	userText, err := flattenCompatMessages(request.Messages)
	if err != nil {
		return compat.Response{}, err
	}

	cl, err := newACPClient(ctx, p.copilotPath, p.extraEnv, p.workingDir)
	if err != nil {
		return compat.Response{}, err
	}
	defer func() { _ = cl.Close() }()

	sid, err := handshake(ctx, cl, p.workingDir)
	if err != nil {
		return compat.Response{}, err
	}
	stopReason, reply, err := sessionPrompt(ctx, cl, sid, userText)
	if err != nil {
		p.recordUsageFailure()
		return compat.Response{}, err
	}
	p.recordUsageSuccess()

	return compat.Response{
		ID:         request.ID,
		Dialect:    request.Dialect,
		Model:      request.Model,
		StopReason: stopReason,
		Message: compat.Message{
			Role: compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: strings.TrimSpace(reply),
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

func flattenCompatMessages(messages []compat.Message) (string, error) {
	var b strings.Builder
	for _, m := range messages {
		if len(m.ToolCalls) > 0 {
			return "", fmt.Errorf("github copilot acp: assistant tool_calls in history are not supported")
		}
		if m.ToolCallID != "" {
			return "", fmt.Errorf("github copilot acp: tool role messages are not supported")
		}
		for _, part := range m.Content {
			switch part.Type {
			case compat.ContentPartText:
				text := strings.TrimSpace(part.Text)
				if text == "" {
					continue
				}
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				fmt.Fprintf(&b, "[%s]\n%s", m.Role, text)
			default:
				return "", fmt.Errorf("github copilot acp: content type %q is not supported", part.Type)
			}
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("github copilot acp: empty conversational prompt")
	}
	return b.String(), nil
}
