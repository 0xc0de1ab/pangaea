package providersim

import (
	"errors"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestHealthyRegistrationAndReports(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 30, 0, 0, time.UTC)
	sim, err := New(Options{
		Mode:  ModeCLIStdio,
		Clock: ClockFunc(func() time.Time { return now }),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	registration, err := sim.Registration()
	if err != nil {
		t.Fatalf("Registration() error = %v", err)
	}
	if err := registration.Validate(); err != nil {
		t.Fatalf("registration Validate() error = %v", err)
	}
	if registration.Identity.Kind != provider.KindCLIContainer {
		t.Fatalf("registration kind = %q, want %q", registration.Identity.Kind, provider.KindCLIContainer)
	}
	if !registration.RegisteredAt.Equal(now) {
		t.Fatalf("registered_at = %v, want %v", registration.RegisteredAt, now)
	}
	if registration.Health.Status != provider.HealthReady {
		t.Fatalf("health status = %q, want %q", registration.Health.Status, provider.HealthReady)
	}
	if registration.Auth.Status != provider.AuthHealthy {
		t.Fatalf("auth status = %q, want %q", registration.Auth.Status, provider.AuthHealthy)
	}

	heartbeat := sim.Heartbeat()
	if heartbeat.Identity.ProviderType != registration.Identity.ProviderType {
		t.Fatalf("heartbeat provider_type = %q, want %q", heartbeat.Identity.ProviderType, registration.Identity.ProviderType)
	}
	if !heartbeat.ReportedAt.Equal(now) {
		t.Fatalf("heartbeat reported_at = %v, want %v", heartbeat.ReportedAt, now)
	}

	inventory := sim.Inventory()
	if len(inventory.Capabilities) == 0 {
		t.Fatal("inventory capabilities are empty")
	}
	if len(inventory.Models) == 0 {
		t.Fatal("inventory models are empty")
	}

	auth := sim.Auth()
	if auth.Auth.Status != provider.AuthHealthy {
		t.Fatalf("auth report status = %q, want %q", auth.Auth.Status, provider.AuthHealthy)
	}

	usage, err := sim.Usage()
	if err != nil {
		t.Fatalf("Usage() error = %v", err)
	}
	if usage.Usage.Source != "providersim" {
		t.Fatalf("usage source = %q, want providersim", usage.Usage.Source)
	}
	if !usage.Usage.ObservedAt.Equal(now) {
		t.Fatalf("usage observed_at = %v, want %v", usage.Usage.ObservedAt, now)
	}
}

func TestRegistrationFailureInjection(t *testing.T) {
	sim, err := New(Options{
		Failures: NewFailureOptions(FailureRegistrationRejected),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := sim.Registration(); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatalf("Registration() error = %v, want ErrRegistrationRejected", err)
	}
}

func TestUsageMissing(t *testing.T) {
	sim, err := New(Options{
		Failures: NewFailureOptions(FailureUsageMissing),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := sim.Usage(); !errors.Is(err, ErrUsageMissing) {
		t.Fatalf("Usage() error = %v, want ErrUsageMissing", err)
	}
}

func TestAuthExpiredMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 30, 0, 0, time.UTC)
	clock := NewManualClock(now)
	sim, err := New(Options{Clock: clock})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	expiredAt := now.Add(15 * time.Minute)
	clock.Set(expiredAt)
	sim.ExpireAuth("refresh token expired")

	auth := sim.Auth()
	if auth.Auth.Status != provider.AuthExpired {
		t.Fatalf("auth status = %q, want %q", auth.Auth.Status, provider.AuthExpired)
	}
	if !auth.Auth.ExpiresAt.Equal(expiredAt) {
		t.Fatalf("auth expires_at = %v, want %v", auth.Auth.ExpiresAt, expiredAt)
	}
	if auth.Auth.LastRefreshErr != "refresh token expired" {
		t.Fatalf("last refresh error = %q", auth.Auth.LastRefreshErr)
	}

	registration, err := sim.Registration()
	if err != nil {
		t.Fatalf("Registration() error = %v", err)
	}
	if registration.Auth.Status != provider.AuthExpired {
		t.Fatalf("registration auth status = %q, want %q", registration.Auth.Status, provider.AuthExpired)
	}
}
