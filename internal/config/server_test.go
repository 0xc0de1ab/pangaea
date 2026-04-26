package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
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
ssh_nodes:
  - node_id: "a2"
    target: "dh.kam@a2"
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
	if len(c.SSHNodes) != 1 || !c.SSHNodes[0].UseSSHConfig {
		t.Fatalf("ssh_nodes = %+v", c.SSHNodes)
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

func TestLoadServer_JWTMode(t *testing.T) {
	body := `
listen: ":8443"
auth_mode: jwt
pki:
  server_cert: "/abs/server.crt"
  server_key: "/abs/server.key"
profiles_file: "p.yaml"
jwt:
  secret_key_file: "./jwt.secret"
  issuer: "issuer"
  audience: "audience"
  auth_timeout: "3s"
`
	c, err := LoadServer(writeServer(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMode != AuthModeJWT {
		t.Fatalf("auth_mode = %q", c.AuthMode)
	}
	if c.JWT.AuthTimeout != 3*time.Second {
		t.Fatalf("auth_timeout = %v", c.JWT.AuthTimeout)
	}
}

func TestLoadServer_SSHNodeValidation(t *testing.T) {
	body := `
listen: ":8443"
pki:
  ca_cert: x
  server_cert: y
  server_key: z
profiles_file: p.yaml
ssh_nodes:
  - node_id: a2
    target: host-a
    port: 2222
    use_ssh_config: false
    reverse_addr: 127.0.0.1:9443
    command: /usr/local/bin/pangaeactl
    config_path: $HOME/pangaea-client.yaml
`
	c, err := LoadServer(writeServer(t, body))
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if got := c.SSHNodes[0]; got.Port != 2222 || got.UseSSHConfig || got.Command == "" || got.ConfigPath == "" {
		t.Fatalf("unexpected ssh node: %+v", got)
	}
}

func TestLoadServer_SSHNodeDuplicate(t *testing.T) {
	body := `
listen: ":8443"
pki:
  ca_cert: x
  server_cert: y
  server_key: z
profiles_file: p.yaml
ssh_nodes:
  - node_id: a2
    target: host-a
  - node_id: a2
    target: host-b
`
	_, err := LoadServer(writeServer(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}
