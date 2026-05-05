package tunnel

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokenSignerRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.Sign(validClaims(now))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := signer.Verify(token, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.RequestID != "req-1" || claims.StreamID != "stream-1" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestTokenSignerRejectsTampering(t *testing.T) {
	now := time.Now().UTC()
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.Sign(validClaims(now))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	token = strings.Replace(token, ".", "x.", 1)

	if _, err := signer.Verify(token, now); !errors.Is(err, ErrTokenMismatch) && !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected token validation error, got %v", err)
	}
}

func TestTokenSignerRejectsExpiredToken(t *testing.T) {
	now := time.Now().UTC()
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	claims := validClaims(now)
	claims.Deadline = now.Add(time.Minute)
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := signer.Verify(token, now.Add(2*time.Minute)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestTokenSignerVerifyForDescriptor(t *testing.T) {
	now := time.Now().UTC()
	signer, err := NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	desc := StreamDescriptor{
		StreamID:           "stream-1",
		ProviderInstanceID: "provider-a",
		Model:              "gpt-5",
		State:              StateActive,
	}
	token, err := signer.Sign(validClaims(now))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := signer.VerifyForDescriptor(token, desc, now); err != nil {
		t.Fatalf("VerifyForDescriptor: %v", err)
	}
	desc.ProviderInstanceID = "provider-b"
	if _, err := signer.VerifyForDescriptor(token, desc, now); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("expected ErrProviderMismatch, got %v", err)
	}
}

func TestNewTokenSignerRequiresKey(t *testing.T) {
	if _, err := NewTokenSigner(nil); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}
