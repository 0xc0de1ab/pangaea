package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

func writeServer(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "server.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadServer_Valid(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	body := `
listen: "0.0.0.0:8443"
pki:
  ca_cert: "./pki/ca.crt"
  server_cert: "./pki/server/server.crt"
  server_key: "./pki/server/server.key"
log:
  level: "info"
  format: "json"
profiles_file: "./profiles.yaml"
self_node:
  enabled: false
`
	c, err := LoadServer(writeServer(t, body))
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if c.Listen != "0.0.0.0:8443" {
		t.Fatalf("listen = %q", c.Listen)
	}
	if !strings.HasSuffix(c.ProfilesFile, "profiles.yaml") {
		t.Fatalf("profiles_file = %q", c.ProfilesFile)
	}
}

func TestLoadServer_MissingListen(t *testing.T) {
	body := `
pki:
  ca_cert: x
  server_cert: y
  server_key: z
profiles_file: "p.yaml"
`
	_, err := LoadServer(writeServer(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadServer_MissingPKI(t *testing.T) {
	body := `
listen: ":8443"
profiles_file: p.yaml
`
	_, err := LoadServer(writeServer(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadServer_SelfNodeEnabledRequiresCerts(t *testing.T) {
	body := `
listen: ":8443"
pki:
  ca_cert: a
  server_cert: b
  server_key: c
profiles_file: p.yaml
self_node:
  enabled: true
`
	_, err := LoadServer(writeServer(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadServer_PathsExpanded(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "/opt/claude")
	body := `
listen: ":8443"
pki:
  ca_cert: "~/.claude/ca.crt"
  server_cert: "/abs/server.crt"
  server_key: "/abs/server.key"
profiles_file: "~/.claude/profiles.yaml"
`
	c, err := LoadServer(writeServer(t, body))
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if c.PKI.CACert != "/opt/claude/ca.crt" {
		t.Fatalf("ca_cert = %q", c.PKI.CACert)
	}
	if c.ProfilesFile != "/opt/claude/profiles.yaml" {
		t.Fatalf("profiles_file = %q", c.ProfilesFile)
	}
}
