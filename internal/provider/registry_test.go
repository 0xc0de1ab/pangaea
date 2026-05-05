package provider

import (
	"errors"
	"testing"
	"time"
)

func TestRegistryAddRejectsDuplicateProviderInstanceID(t *testing.T) {
	registry := NewRegistry()
	registration := validRegistration()

	if err := registry.Add(registration); err != nil {
		t.Fatalf("add registration: %v", err)
	}
	if err := registry.Add(registration); !errors.Is(err, ErrProviderDuplicate) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestRegistryFindsByCapabilityAndService(t *testing.T) {
	registry := NewRegistry()
	codex := validRegistration()
	claude := validRegistration()
	claude.Identity.ProviderID = "claude-samtest"
	claude.Identity.ProviderInstanceID = "claude-samtest-a1-01"
	claude.Identity.Service = ServiceClaude
	claude.Capabilities = []Capability{CapabilityAnthropicMessages}

	for _, registration := range []Registration{codex, claude} {
		if err := registry.Upsert(registration); err != nil {
			t.Fatalf("upsert registration: %v", err)
		}
	}

	openaiProviders := registry.FindByCapability(CapabilityOpenAIChat)
	if len(openaiProviders) != 1 || openaiProviders[0].Identity.Service != ServiceCodex {
		t.Fatalf("expected one codex OpenAI provider, got %#v", openaiProviders)
	}

	claudeProviders := registry.FindByService(ServiceClaude)
	if len(claudeProviders) != 1 || claudeProviders[0].Identity.ProviderID != "claude-samtest" {
		t.Fatalf("expected one claude provider, got %#v", claudeProviders)
	}
}

func TestRegistryRemove(t *testing.T) {
	registry := NewRegistry()
	registration := validRegistration()
	if err := registry.Upsert(registration); err != nil {
		t.Fatalf("upsert registration: %v", err)
	}

	if !registry.Remove(registration.Identity.ProviderInstanceID) {
		t.Fatalf("expected remove to succeed")
	}
	if _, ok := registry.Get(registration.Identity.ProviderInstanceID); ok {
		t.Fatalf("expected provider to be removed")
	}
	if registry.Remove(registration.Identity.ProviderInstanceID) {
		t.Fatalf("expected second remove to report false")
	}
}

func validRegistration() Registration {
	return Registration{
		Identity: validIdentity(),
		Capabilities: []Capability{
			CapabilityOpenAIChat,
			CapabilityStreamSSE,
			CapabilityUsageRead,
		},
		Models: []Model{{ID: "gpt-5.3-codex-spark"}},
		Health: Health{
			Status:    HealthReady,
			CheckedAt: time.Now(),
		},
		RegisteredAt: time.Now(),
	}
}
