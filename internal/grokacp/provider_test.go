package grokacp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestFlattenCompatMessages(t *testing.T) {
	got, err := flattenCompatMessages(compat.Request{Messages: []compat.Message{{
		Role: compat.MessageRoleUser,
		Content: []compat.ContentPart{{
			Type: compat.ContentPartText,
			Text: "hello",
		}},
	}}})
	if err != nil {
		t.Fatalf("flattenCompatMessages: %v", err)
	}
	if !strings.Contains(got, "[user]\nhello") {
		t.Fatalf("flattened prompt = %q", got)
	}
}

func TestDefaultGrokModel(t *testing.T) {
	p, err := New(Options{Registration: testRegistration(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].ID != "grok-build" {
		t.Fatalf("models = %#v", models)
	}
	if !containsString(models[0].Aliases, "grok-build-default") || !containsString(models[0].Aliases, "grok-build-0.1") {
		t.Fatalf("aliases = %#v", models[0].Aliases)
	}
}

func TestSelectAuthMethodPrefersCachedToken(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	raw := json.RawMessage(`{"authMethods":[{"id":"cached_token"},{"id":"grok.com"}]}`)
	got, err := selectAuthMethod(raw)
	if err != nil {
		t.Fatalf("selectAuthMethod: %v", err)
	}
	if got != "cached_token" {
		t.Fatalf("method = %q", got)
	}
}

func TestSelectAuthMethodUsesAPIKeyWhenAvailable(t *testing.T) {
	t.Setenv("XAI_API_KEY", "xai-test")
	raw := json.RawMessage(`{"authMethods":[{"id":"cached_token"},{"id":"xai.api_key"}]}`)
	got, err := selectAuthMethod(raw)
	if err != nil {
		t.Fatalf("selectAuthMethod: %v", err)
	}
	if got != "xai.api_key" {
		t.Fatalf("method = %q", got)
	}
}

func TestExtractAgentChunk(t *testing.T) {
	raw := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`)
	if got := extractAgentChunk(raw); got != "hello" {
		t.Fatalf("chunk = %q", got)
	}
}

func TestParseMCPServersJSONMap(t *testing.T) {
	got, err := parseMCPServersJSON(`{"mcpServers":{"demo":{"command":"npx","args":["-y","mcp"]}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %#v", got)
	}
	m, ok := got[0].(map[string]any)
	if !ok || m["name"] != "demo" {
		t.Fatalf("got %#v", got[0])
	}
}

func testRegistration(models []provider.Model) provider.Registration {
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "grok-build-cli",
			ProviderInstanceID: "grok-build-test",
			NodeID:             "node01",
			HostName:           "host",
			Service:            provider.ServiceGrokBuild,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
		},
		Models:       models,
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
		Auth:         provider.AuthState{Status: provider.AuthHealthy},
		RegisteredAt: time.Now(),
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
