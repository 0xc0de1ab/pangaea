package tunnel

import (
	"errors"
	"testing"
	"time"
)

func TestPoolAcquireReleaseReusesMatchingIdleDescriptor(t *testing.T) {
	pool, err := NewPool(1, 1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	first, err := pool.Acquire("provider-a", "gpt-5")
	if err != nil {
		t.Fatalf("Acquire first: %v", err)
	}
	if first.State != StateActive {
		t.Fatalf("first state got %q want %q", first.State, StateActive)
	}

	if err := pool.Release(first.StreamID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if stats := pool.Stats(); stats.Idle != 1 || stats.Active != 0 {
		t.Fatalf("after release stats got %+v", stats)
	}

	second, err := pool.Acquire("provider-a", "gpt-5")
	if err != nil {
		t.Fatalf("Acquire second: %v", err)
	}
	if second.StreamID != first.StreamID {
		t.Fatalf("expected idle descriptor reuse, got %q want %q", second.StreamID, first.StreamID)
	}
	if second.State != StateActive {
		t.Fatalf("second state got %q want %q", second.State, StateActive)
	}
}

func TestPoolExhaustion(t *testing.T) {
	pool, err := NewPool(1, 1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if _, err := pool.Acquire("provider-a", "gpt-5"); err != nil {
		t.Fatalf("Acquire first: %v", err)
	}

	_, err = pool.Acquire("provider-a", "gpt-5")
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
}

func TestPoolReleaseHonorsMaxIdle(t *testing.T) {
	pool, err := NewPool(1, 2)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	first, err := pool.Acquire("provider-a", "gpt-5")
	if err != nil {
		t.Fatalf("Acquire first: %v", err)
	}
	second, err := pool.Acquire("provider-a", "gpt-5")
	if err != nil {
		t.Fatalf("Acquire second: %v", err)
	}

	if err := pool.Release(first.StreamID); err != nil {
		t.Fatalf("Release first: %v", err)
	}
	if err := pool.Release(second.StreamID); err != nil {
		t.Fatalf("Release second: %v", err)
	}

	if stats := pool.Stats(); stats.Idle != 1 || stats.Active != 0 {
		t.Fatalf("expected max idle cap to retain one descriptor, got %+v", stats)
	}
}

func TestPoolClose(t *testing.T) {
	pool, err := NewPool(1, 1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	desc, err := pool.Acquire("provider-a", "gpt-5")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if stats := pool.Stats(); !stats.Closed || stats.Idle != 0 || stats.Active != 0 {
		t.Fatalf("closed stats got %+v", stats)
	}
	if _, err := pool.Acquire("provider-a", "gpt-5"); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("expected ErrPoolClosed on acquire after close, got %v", err)
	}
	if err := pool.Release(desc.StreamID); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("expected ErrPoolClosed on release after close, got %v", err)
	}
}

func TestStreamTokenClaimsValidateExpired(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	claims := validClaims(now)
	claims.Deadline = now.Add(-time.Nanosecond)

	if err := claims.Validate(now); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestStreamTokenClaimsValidateRequiresFields(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	claims := validClaims(now)
	claims.RequestID = " "

	if err := claims.Validate(now); !errors.Is(err, ErrInvalidTokenClaims) {
		t.Fatalf("expected ErrInvalidTokenClaims, got %v", err)
	}
}

func TestStreamTokenClaimsValidateForDescriptorRejectsWrongProvider(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	desc := StreamDescriptor{
		StreamID:           "stream-1",
		ProviderInstanceID: "provider-a",
		Model:              "gpt-5",
		State:              StateActive,
	}
	claims := validClaims(now)
	claims.ProviderInstanceID = "provider-b"

	if err := claims.ValidateForDescriptor(desc, now); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("expected ErrProviderMismatch, got %v", err)
	}
}

func validClaims(now time.Time) StreamTokenClaims {
	return StreamTokenClaims{
		RequestID:          "req-1",
		StreamID:           "stream-1",
		ProviderInstanceID: "provider-a",
		Model:              "gpt-5",
		Deadline:           now.Add(time.Minute),
	}
}
