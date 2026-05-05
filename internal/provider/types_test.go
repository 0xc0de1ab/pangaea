package provider

import (
	"errors"
	"testing"
	"time"
)

func TestProviderIdentityValidateRequiresOperatorHostName(t *testing.T) {
	identity := validIdentity()
	identity.HostName = ""

	if err := identity.Validate(); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("expected ErrInvalidIdentity, got %v", err)
	}
}

func TestProviderIdentityAllowsMultipleAccountsOnSameHost(t *testing.T) {
	first := validIdentity()
	first.ProviderID = "codex-samtest"
	first.ProviderInstanceID = "codex-samtest-a1-01"
	first.Account = Account{ID: "acct-samtest", Display: "samtest4u@gmail.com"}

	second := validIdentity()
	second.ProviderID = "codex-nullcode"
	second.ProviderInstanceID = "codex-nullcode-a1-01"
	second.Account = Account{ID: "acct-nullcode", Display: "nullcode@gmail.com"}

	for _, identity := range []ProviderIdentity{first, second} {
		if err := identity.Validate(); err != nil {
			t.Fatalf("expected valid identity for %s: %v", identity.ProviderID, err)
		}
	}
}

func TestRegistrationValidate(t *testing.T) {
	registration := Registration{
		Identity: validIdentity(),
		Capabilities: []Capability{
			CapabilityOpenAIChat,
			CapabilityStreamSSE,
			CapabilityUsageRead,
		},
		Models: []Model{
			{ID: "gpt-5.3-codex-spark", Aliases: []string{"codex-spark"}},
		},
		Health:       Health{Status: HealthReady, CheckedAt: time.Now()},
		RegisteredAt: time.Now(),
	}

	if err := registration.Validate(); err != nil {
		t.Fatalf("expected valid registration: %v", err)
	}
}

func TestRegistrationValidateRejectsUnknownCapability(t *testing.T) {
	registration := Registration{
		Identity:     validIdentity(),
		Capabilities: []Capability{"api.unknown"},
		Health:       Health{Status: HealthReady},
	}

	if err := registration.Validate(); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("expected ErrInvalidRegistration, got %v", err)
	}
}

func validIdentity() ProviderIdentity {
	return ProviderIdentity{
		ProviderID:         "codex-samtest",
		ProviderInstanceID: "codex-samtest-a1-01",
		NodeID:             "node-a1",
		HostName:           "a1",
		ContainerID:        "ctr-123",
		Service:            ServiceCodex,
		Kind:               KindCLIContainer,
		Account:            Account{ID: "acct-samtest", Display: "samtest4u@gmail.com"},
	}
}
