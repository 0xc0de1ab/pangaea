package security

import (
	"errors"
	"strings"
	"testing"
)

func TestAPIKeyStoreAuthenticatesRawKey(t *testing.T) {
	store := NewAPIKeyStore([]byte("pepper"))
	principal, err := store.AddRawKey("key_1", "pk_test_123456789", "team-a", "usr_1")
	if err != nil {
		t.Fatalf("add raw key: %v", err)
	}
	if principal.Prefix != "pk_test_1234" {
		t.Fatalf("expected prefix, got %q", principal.Prefix)
	}

	got, ok := store.Authenticate("pk_test_123456789")
	if !ok {
		t.Fatalf("expected auth success")
	}
	if got.ID != "key_1" || got.TenantID != "team-a" || got.UserID != "usr_1" {
		t.Fatalf("unexpected principal: %#v", got)
	}
}

func TestAPIKeyStoreRejectsWrongKey(t *testing.T) {
	store := NewAPIKeyStore([]byte("pepper"))
	if _, err := store.AddRawKey("key_1", "pk_test_123456789", "team-a", "usr_1"); err != nil {
		t.Fatalf("add raw key: %v", err)
	}

	if _, ok := store.Authenticate("pk_test_wrong"); ok {
		t.Fatalf("expected auth failure")
	}
}

func TestAPIKeyStoreDoesNotExposeRawKeyInDebugDigests(t *testing.T) {
	store := NewAPIKeyStore([]byte("pepper"))
	raw := "pk_test_123456789"
	if _, err := store.AddRawKey("key_1", raw, "team-a", "usr_1"); err != nil {
		t.Fatalf("add raw key: %v", err)
	}

	for _, digest := range store.DebugDigests() {
		if strings.Contains(digest, raw) {
			t.Fatalf("digest leaked raw key: %q", digest)
		}
	}
}

func TestAPIKeyStoreRejectsInvalidAdd(t *testing.T) {
	store := NewAPIKeyStore(nil)
	if _, err := store.AddRawKey("", "", "", ""); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("expected ErrInvalidAPIKey, got %v", err)
	}
}

func TestAPIKeyStoreCreateListAndRemove(t *testing.T) {
	store := NewAPIKeyStore([]byte("pepper"))
	raw, principal, err := store.CreateKey("team-a", "usr_1")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(raw, "pk_") {
		t.Fatalf("expected generated key prefix, got %q", raw)
	}
	if principal.ID == "" || principal.Prefix == "" {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if _, ok := store.Authenticate(raw); !ok {
		t.Fatalf("generated key did not authenticate")
	}
	keys := store.List()
	if len(keys) != 1 || keys[0].ID != principal.ID {
		t.Fatalf("unexpected key list: %#v", keys)
	}
	if strings.Contains(keys[0].Prefix, raw) {
		t.Fatalf("list leaked raw key: %#v", keys)
	}
	if !store.Remove(principal.ID) {
		t.Fatalf("expected remove success")
	}
	if _, ok := store.Authenticate(raw); ok {
		t.Fatalf("removed key still authenticates")
	}
}
