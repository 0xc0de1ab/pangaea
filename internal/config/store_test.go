package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

func writeProfilesFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProfileStore_Initial(t *testing.T) {
	pf := &ProfilesFile{Profiles: []Profile{{Name: "x", Format: "f", Paths: []string{"/x"}, AllowedClients: []string{"c"}, Propagate: PropagateSpec{Mode: PropagateModeStaleOnly}}}}
	s := NewProfileStore(pf)
	got, ok := s.Get("x")
	if !ok || got.Name != "x" {
		t.Fatalf("Get x: %v %v", got, ok)
	}
	if len(s.List()) != 1 {
		t.Fatalf("List len = %d", len(s.List()))
	}
}

func TestProfileStore_ReloadHappy(t *testing.T) {
	s := NewProfileStore(nil)
	body := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: [c]
`
	p := writeProfilesFile(t, body)
	if err := s.Reload(p); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatalf("missing a after reload")
	}
}

func TestProfileStore_ReloadBadPreservesPrevious(t *testing.T) {
	s := NewProfileStore(&ProfilesFile{Profiles: []Profile{{Name: "old", Format: "f", Paths: []string{"/x"}, AllowedClients: []string{"c"}}}})

	// Reload with broken yaml.
	bad := writeProfilesFile(t, `:::: not yaml ::::`)
	err := s.Reload(bad)
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := s.Get("old"); !ok {
		t.Fatalf("old profile should still be present after failed reload")
	}
	if len(s.List()) != 1 {
		t.Fatalf("List len = %d", len(s.List()))
	}
}

func TestProfileStore_SubscribeReceivesUpdate(t *testing.T) {
	s := NewProfileStore(nil)
	ch := s.Subscribe()
	body := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: [c]
`
	if err := s.Reload(writeProfilesFile(t, body)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		if len(got) != 1 || got[0].Name != "a" {
			t.Fatalf("got %v", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("subscriber did not receive update")
	}
}

func TestProfileStore_SubscribeNonBlocking(t *testing.T) {
	// Buffer is 1 and this subscriber never drains. Multiple Reloads must
	// not block.
	s := NewProfileStore(nil)
	_ = s.Subscribe()
	body1 := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: [c]
`
	body2 := `
profiles:
  - name: b
    format: f
    paths: [/x]
    allowed_clients: [c]
`
	for i := 0; i < 5; i++ {
		path := body1
		if i%2 == 0 {
			path = body2
		}
		done := make(chan error, 1)
		go func(b string) { done <- s.Reload(writeProfilesFile(t, b)) }(path)
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("reload %d: %v", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Reload %d blocked on subscriber", i)
		}
	}
}
