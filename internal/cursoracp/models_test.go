package cursoracp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestParseCursorModelJSON(t *testing.T) {
	models := parseCursorModels([]byte(`{"models":[{"id":"composer-2","displayName":"Composer 2"},{"modelId":"gpt-5"},{"id":"claude-sonnet-4-6[thinking=true,context=200k,effort=medium]"}]}`), []provider.Capability{
		provider.CapabilityOpenAIChat,
		provider.CapabilityAnthropicMessages,
		provider.CapabilityGeminiGenerateContent,
		provider.CapabilityUsageRead,
	})
	if got := cursorModelIDs(models); strings.Join(got, ",") != "composer-2,gpt-5,claude-sonnet-4-6[thinking=true,context=200k,effort=medium]" {
		t.Fatalf("model ids = %v", got)
	}
	if len(models[0].Aliases) != 1 || models[0].Aliases[0] != "Composer 2" {
		t.Fatalf("composer alias = %#v", models[0].Aliases)
	}
	if hasCursorModelCapability(models[0].Capabilities, provider.CapabilityUsageRead) {
		t.Fatalf("model capabilities should not include provider-only capability: %v", models[0].Capabilities)
	}
	if !hasCursorModelCapability(models[0].Capabilities, provider.CapabilityGeminiGenerateContent) {
		t.Fatalf("model capabilities missing API caps: %v", models[0].Capabilities)
	}
}

func TestParseCursorModelText(t *testing.T) {
	models := parseCursorModels([]byte("\x1b[32mAvailable models\x1b[0m\n✓ auto - Auto\n✓ composer-2 Composer 2\n- `gpt-5`\n* claude-sonnet-4-6[thinking=true,context=200k,effort=medium]\n"), nil)
	if got := cursorModelIDs(models); strings.Join(got, ",") != "auto,composer-2,gpt-5,claude-sonnet-4-6[thinking=true,context=200k,effort=medium]" {
		t.Fatalf("model ids = %v", got)
	}
	if len(models[0].Aliases) != 1 || models[0].Aliases[0] != "Auto" {
		t.Fatalf("auto alias = %#v", models[0].Aliases)
	}
}

func TestProviderModelsDiscoversCursorAgentModels(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	if err := os.WriteFile(agent, []byte(`#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "models" ]; then
	printf '{"models":[{"id":"composer-2"},{"id":"gpt-5"}]}'
	exit 0
fi
exit 2
`), 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := New(Options{
		Registration: provider.Registration{
			Identity: provider.ProviderIdentity{
				ProviderType:       "cursor-cli",
				ProviderInstanceID: "cursor-test",
				NodeID:             "node01",
				HostName:           "host",
				Service:            provider.ServiceCursor,
				Kind:               provider.KindCLIContainer,
			},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAnthropicMessages},
			Models:       []provider.Model{{ID: "composer-2"}},
			Health:       provider.Health{Status: provider.HealthReady, CheckedAt: time.Now()},
			Auth:         provider.AuthState{Status: provider.AuthHealthy},
			RegisteredAt: time.Now(),
		},
		AgentPath:  agent,
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !p.ForceModelDiscovery() {
		t.Fatal("cursor ACP should force model discovery even with configured fallback models")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := p.Models(ctx)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if got := cursorModelIDs(models); strings.Join(got, ",") != "composer-2,gpt-5" {
		t.Fatalf("model ids = %v", got)
	}
}

func cursorModelIDs(models []provider.Model) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, model.ID)
	}
	return out
}

func hasCursorModelCapability(capabilities []provider.Capability, want provider.Capability) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
