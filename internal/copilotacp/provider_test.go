package copilotacp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestFlattenCompatMessages(t *testing.T) {
	got, err := flattenCompatMessages([]compat.Message{{
		Role: compat.MessageRoleUser,
		Content: []compat.ContentPart{{
			Type: compat.ContentPartText,
			Text: "hello",
		}},
	}})
	if err != nil {
		t.Fatalf("flattenCompatMessages: %v", err)
	}
	if !strings.Contains(got, "[user]\nhello") {
		t.Fatalf("flattened prompt = %q", got)
	}
}

func TestModelsAnnotateCopilotAliasesAndAutoGroup(t *testing.T) {
	registration := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "github-copilot",
			ProviderInstanceID: "github-copilot-a1",
			NodeID:             "node-a1",
			HostName:           "snowbox",
			Service:            provider.ServiceGitHubCopilot,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		Models: []provider.Model{
			{ID: "github-copilot-default"},
			{ID: "auto"},
			{ID: "gpt-5.2"},
		},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now().UTC()},
		RegisteredAt: time.Now().UTC(),
	}
	p, err := New(Options{Registration: registration})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !p.ForceModelDiscovery() {
		t.Fatalf("copilot ACP should force model metadata refresh")
	}
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	byID := map[string]provider.Model{}
	for _, model := range models {
		byID[model.ID] = model
	}
	if byID["github-copilot-default"].Kind != "alias" || !containsString(byID["github-copilot-default"].Aliases, "copilot-default") {
		t.Fatalf("default metadata = %#v", byID["github-copilot-default"])
	}
	if byID["auto"].Kind != "group" || !containsString(byID["auto"].GroupMembers, "gpt-5.2") {
		t.Fatalf("auto metadata = %#v", byID["auto"])
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
