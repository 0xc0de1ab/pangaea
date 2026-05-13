package copilotacp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestLiveCopilotACP(t *testing.T) {
	if os.Getenv("PANGAEA_COPILOT_ACP_LIVE") != "1" {
		t.Skip("set PANGAEA_COPILOT_ACP_LIVE=1 to run live Copilot ACP test")
	}
	now := time.Now().UTC()
	registration := provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "github-copilot-acp-live",
			ProviderInstanceID: "github-copilot-acp-live",
			NodeID:             "node-live",
			HostName:           "host-live",
			Service:            provider.ServiceGitHubCopilot,
			Kind:               provider.KindCLIContainer,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityCodeCompletion,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
		},
		Models: []provider.Model{{
			ID:           "gpt-4.1",
			Aliases:      []string{"copilot-default"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy},
		RegisteredAt: now,
	}
	p, err := New(Options{Registration: registration, WorkingDir: "."})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := p.Invoke(ctx, registration, compat.Request{
		ID:      "live",
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-4.1",
		Messages: []compat.Message{{
			Role: compat.MessageRoleUser,
			Content: []compat.ContentPart{{
				Type: compat.ContentPartText,
				Text: "Reply with exactly OK.",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got := strings.TrimSpace(resp.Message.Content[0].Text); got != "OK" {
		t.Fatalf("response = %q, want OK", got)
	}
}
