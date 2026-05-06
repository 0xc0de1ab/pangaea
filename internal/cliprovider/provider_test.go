package cliprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestProviderInvokeUsesDefaultClaudeCommand(t *testing.T) {
	var saw CommandSpec
	p := newTestProvider(t, provider.ServiceClaude, CommandRunnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		saw = spec
		return CommandResult{Stdout: []byte("claude ok\n")}, nil
	}))
	request := testRequest(compat.APIDialectAnthropic, "claude-default")
	response, err := p.Invoke(context.Background(), mustRegistration(t, p), request)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if response.Dialect != compat.APIDialectAnthropic || response.Message.Content[0].Text != "claude ok" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(saw.Command) < 8 || saw.Command[0] != "claude" || saw.Command[1] != "-p" || !strings.Contains(saw.Command[2], "[user]\nhello") {
		t.Fatalf("unexpected command: %#v", saw.Command)
	}
	if saw.Env["PANGAEA_REQUEST_MODEL"] != "claude-default" || saw.Env["PANGAEA_REQUEST_DIALECT"] != "anthropic" {
		t.Fatalf("missing request env: %#v", saw.Env)
	}
	usage, err := p.Usage()
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Requests != 1 || usage.InputTokens == 0 || usage.OutputTokens == 0 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestProviderInvokeExtractsGeminiJSON(t *testing.T) {
	p := newTestProvider(t, provider.ServiceGemini, CommandRunnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		if spec.Command[0] != "gemini" || spec.Command[len(spec.Command)-1] != "gemini-2.5-flash" {
			t.Fatalf("unexpected gemini command: %#v", spec.Command)
		}
		return CommandResult{Stdout: []byte(`{"response":"gemini ok"}`)}, nil
	}))
	response, err := p.Invoke(context.Background(), mustRegistration(t, p), testRequest(compat.APIDialectGemini, "gemini-2.5-flash"))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if response.Message.Content[0].Text != "gemini ok" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestProviderInvokeExpandsCommandTemplate(t *testing.T) {
	var saw []string
	p := newTestProviderWithOptions(t, Options{
		Service: provider.ServiceClaude,
		Command: []string{"echo", "{{model}}", "{{prompt}}"},
		Runner: CommandRunnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
			saw = spec.Command
			return CommandResult{Stdout: []byte("templated")}, nil
		}),
	})
	_, err := p.Invoke(context.Background(), mustRegistration(t, p), testRequest(compat.APIDialectOpenAI, "claude-opus"))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(saw) != 3 || saw[1] != "claude-opus" || !strings.Contains(saw[2], "hello") {
		t.Fatalf("template was not expanded: %#v", saw)
	}
}

func TestPromptFromCanonicalIncludesToolCalls(t *testing.T) {
	prompt, err := PromptFromCanonical(compat.Request{
		Dialect: compat.APIDialectOpenAI,
		Model:   "test",
		Messages: []compat.Message{{
			Role: compat.MessageRoleAssistant,
			ToolCalls: []compat.ToolCall{{
				ID:        "call_1",
				Type:      compat.ToolCallFunction,
				Name:      "lookup",
				Arguments: `{"q":"status"}`,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if !strings.Contains(prompt, "call_1") || !strings.Contains(prompt, `{"q":"status"}`) {
		t.Fatalf("tool call missing from prompt: %s", prompt)
	}
}

func newTestProvider(t *testing.T, service provider.Service, runner CommandRunner) *Provider {
	t.Helper()
	return newTestProviderWithOptions(t, Options{Service: service, Runner: runner})
}

func newTestProviderWithOptions(t *testing.T, opts Options) *Provider {
	t.Helper()
	if opts.Registration.Identity.ProviderID == "" {
		service := opts.Service
		if service == "" {
			service = provider.ServiceClaude
		}
		opts.Registration = provider.Registration{
			Identity: provider.ProviderIdentity{
				ProviderID:         string(service) + "-test",
				ProviderInstanceID: string(service) + "-test-a1",
				NodeID:             "node-a1",
				HostName:           "snowbox",
				Service:            service,
				Kind:               provider.KindCLIContainer,
			},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
			Models:       []provider.Model{{ID: string(service) + "-default"}},
			Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now().UTC()},
			RegisteredAt: time.Now().UTC(),
		}
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return p
}

func mustRegistration(t *testing.T, p *Provider) provider.Registration {
	t.Helper()
	registration, err := p.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	return registration
}

func testRequest(dialect compat.APIDialect, model string) compat.Request {
	return compat.Request{
		ID:      "req-test",
		Dialect: dialect,
		Model:   model,
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
		}},
	}
}
