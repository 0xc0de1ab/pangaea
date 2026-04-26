package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
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
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
    allowed_clients: ["host-a", "host-b"]
    reverse_targets:
      - node_id: "host-b"
        transport: "direct"
        url: "wss://host-b.example:9443"
    validate:
      strategy: "expires_at_max"
      live_check: true
      live_check_timeout: "5s"
    propagate:
      mode: "to_all"
      cooldown: "3s"
  - name: "claude-dev"
    format: "claude-credentials-json-format"
    dir: "/tmp/claude-dev"
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
	if len(prod.ReverseTargets) != 1 {
		t.Fatalf("reverse_targets len = %d", len(prod.ReverseTargets))
	}
	if prod.ReverseTargets[0].NodeID != "host-b" || prod.ReverseTargets[0].URL != "wss://host-b.example:9443" || prod.ReverseTargets[0].Transport != ReverseTransportDirect {
		t.Fatalf("unexpected reverse target: %+v", prod.ReverseTargets[0])
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
    dir: /x
    allowed_clients: [c]
  - name: a
    format: f
    dir: /y
    allowed_clients: [c]
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_EmptyDir(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    dir: ""
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
    dir: /x
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
    dir: /x
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
    dir: /x
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

func TestLoadProfiles_DuplicateReverseTarget(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    dir: /x
    allowed_clients: [c]
    reverse_targets:
      - node_id: c
        transport: direct
        url: wss://c1
      - node_id: c
        transport: direct
        url: wss://c2
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProfiles_SSHReverseTarget(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    dir: /x
    allowed_clients: [c]
    reverse_targets:
      - node_id: c
        transport: ssh
`
	pf, err := LoadProfiles(writeYAML(t, body))
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	got := pf.Profiles[0].ReverseTargets[0]
	if got.Transport != ReverseTransportSSH || got.URL != "" {
		t.Fatalf("unexpected reverse target: %+v", got)
	}
}

func TestLoadProfiles_SSHReverseTargetRejectsURL(t *testing.T) {
	body := `
profiles:
  - name: a
    format: f
    dir: /x
    allowed_clients: [c]
    reverse_targets:
      - node_id: c
        transport: ssh
        url: wss://c1
`
	_, err := LoadProfiles(writeYAML(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}
