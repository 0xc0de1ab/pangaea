package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const goldenProfiles = `
profiles:
  - name: "claude-prod"
    format: "claude-credentials-json-format"
    paths:
      - "~/.claude/.credentials.json"
      - "~/.config/claude/.credentials.json"
    allowed_clients: ["host-a", "host-b"]
    validate:
      strategy: "expires_at_max"
      live_check: true
      live_check_timeout: "5s"
    propagate:
      mode: "to_all"
      cooldown: "3s"
  - name: "claude-dev"
    format: "claude-credentials-json-format"
    paths: ["/tmp/c.json"]
    allowed_clients: ["host-c"]
`

func TestLoadProfiles_Golden(t *testing.T) {
	pf, err := LoadProfiles(writeYAML(t, goldenProfiles))
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(pf.Profiles) != 2 {
		t.Fatalf("len = %d", len(pf.Profiles))
	}
	prod := pf.Profiles[0]
	if prod.Name != "claude-prod" {
		t.Fatalf("name = %q", prod.Name)
	}
	if prod.Validate.LiveCheckTimeout != 5*time.Second {
		t.Fatalf("live_check_timeout = %v", prod.Validate.LiveCheckTimeout)
	}
	if prod.Propagate.Mode != PropagateModeAll {
		t.Fatalf("mode = %q", prod.Propagate.Mode)
	}
	if prod.Propagate.Cooldown != 3*time.Second {
		t.Fatalf("cooldown = %v", prod.Propagate.Cooldown)
	}
	// Defaults applied to second profile (no propagate at all).
	dev := pf.Profiles[1]
	if dev.Propagate.Mode != PropagateModeStaleOnly {
		t.Fatalf("default mode = %q", dev.Propagate.Mode)
	}
	if dev.Propagate.Cooldown != 2*time.Second {
		t.Fatalf("default cooldown = %v", dev.Propagate.Cooldown)
	}
}

func TestLoadProfiles_DuplicateName(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: [c]
  - name: a
    format: f
    paths: [/y]
    allowed_clients: [c]
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_EmptyPaths(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    paths: []
    allowed_clients: [c]
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_EmptyAllowedClient(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: ["host-a", ""]
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_BadDuration(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: [c]
    validate:
      live_check_timeout: "five seconds"
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_BadPropagateMode(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    paths: [/x]
    allowed_clients: [c]
    propagate:
      mode: "broadcast"
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_FileMissing(t *testing.T) {
	_, err := LoadProfiles(filepath.Join(t.TempDir(), "no.yaml"))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}
