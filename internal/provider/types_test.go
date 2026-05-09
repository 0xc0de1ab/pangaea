package provider

import (
	"encoding/json"
	"errors"
	"strings"
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
	first.ProviderID = "codex-primary"
	first.ProviderInstanceID = "codex-primary-a1-01"
	first.Account = Account{ID: "acct-primary", Display: "primary@example.test"}

	second := validIdentity()
	second.ProviderID = "codex-secondary"
	second.ProviderInstanceID = "codex-secondary-a1-01"
	second.Account = Account{ID: "acct-secondary", Display: "secondary@example.test"}

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
			CapabilityModelsRead,
			CapabilityAuthRefreshProtocol,
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

func TestModelQuotaMarshalOmitsZeroResetAt(t *testing.T) {
	data, err := json.Marshal(ModelQuota{RemainingPct: 100, Source: "antigravity-model-quota"})
	if err != nil {
		t.Fatalf("marshal quota: %v", err)
	}
	if strings.Contains(string(data), "0001-01-01") || strings.Contains(string(data), "reset_at") {
		t.Fatalf("zero reset_at leaked into JSON: %s", data)
	}

	resetAt := time.Date(2026, 5, 9, 6, 20, 16, 0, time.UTC)
	data, err = json.Marshal(ModelQuota{RemainingPct: 100, ResetAt: resetAt})
	if err != nil {
		t.Fatalf("marshal quota with reset: %v", err)
	}
	if !strings.Contains(string(data), "2026-05-09T06:20:16Z") {
		t.Fatalf("reset_at was not included: %s", data)
	}
}

func TestAuthStateMarshalOmitsZeroTimes(t *testing.T) {
	data, err := json.Marshal(AuthState{Status: AuthHealthy, Refreshable: false})
	if err != nil {
		t.Fatalf("marshal auth state: %v", err)
	}
	if strings.Contains(string(data), "0001-01-01") || strings.Contains(string(data), "expires_at") || strings.Contains(string(data), "last_refresh_at") {
		t.Fatalf("zero auth timestamps leaked into JSON: %s", data)
	}

	expiresAt := time.Date(2026, 5, 9, 6, 20, 16, 0, time.UTC)
	data, err = json.Marshal(AuthState{Status: AuthHealthy, ExpiresAt: expiresAt})
	if err != nil {
		t.Fatalf("marshal auth state with expiry: %v", err)
	}
	if !strings.Contains(string(data), "2026-05-09T06:20:16Z") {
		t.Fatalf("expires_at was not included: %s", data)
	}
}

func validIdentity() ProviderIdentity {
	return ProviderIdentity{
		ProviderID:         "codex-primary",
		ProviderInstanceID: "codex-primary-a1-01",
		NodeID:             "node-a1",
		HostName:           "a1",
		ContainerID:        "ctr-123",
		Service:            ServiceCodex,
		Kind:               KindCLIContainer,
		Account:            Account{ID: "acct-primary", Display: "primary@example.test"},
	}
}
