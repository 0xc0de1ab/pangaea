package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

func writeClient(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadClient_Valid(t *testing.T) {
	body := `
server: "wss://hub.local:8443"
node_id: "host-a"
profiles:
  - name: claude-prod
    format: claude-credentials-json-format
    dir: "/tmp/claude"
    watch_files: [".credentials.json", "~/.claude.json"]
  - name: codex-prod
    format: codex-auth-json-format
    dir: "/tmp/codex"
pki:
  ca_cert: "./pki/ca.crt"
  client_cert: "./pki/host-a.crt"
  client_key:  "./pki/host-a.key"
reconnect:
  initial_delay: "5s"
  jitter: "1s"
  max_delay: "60s"
log:
  level: "info"
  format: "json"
reverse:
  listen: ":9443"
  pki:
    ca_cert: "./pki/ca.crt"
    server_cert: "./pki/reverse.crt"
    server_key: "./pki/reverse.key"
  allowed_peers: ["opi5(server)"]
`
	c, err := LoadClient(writeClient(t, body))
	if err != nil {
		t.Fatalf("LoadClient: %v", err)
	}
	if c.Reconnect.InitialDelay != 5*time.Second {
		t.Fatalf("initial_delay = %v", c.Reconnect.InitialDelay)
	}
	if len(c.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(c.Profiles))
	}
	if c.Profiles[0].Name != "claude-prod" || c.Profiles[1].Name != "codex-prod" {
		t.Fatalf("unexpected profile order: %+v", c.Profiles)
	}
	if c.Reverse.Listen != ":9443" {
		t.Fatalf("reverse.listen = %q", c.Reverse.Listen)
	}
	if len(c.Reverse.AllowedPeers) != 1 || c.Reverse.AllowedPeers[0] != "opi5(server)" {
		t.Fatalf("unexpected reverse allowed peers: %+v", c.Reverse.AllowedPeers)
	}
}

func TestLoadClient_DefaultsApplied(t *testing.T) {
	body := `
server: "wss://x:1"
node_id: n
profiles:
  - name: p
    dir: "/tmp/x"
pki:
  ca_cert: a
  client_cert: b
  client_key: c
`
	c, err := LoadClient(writeClient(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if c.Reconnect.InitialDelay != common.ReconnectInitial {
		t.Fatalf("initial_delay default = %v", c.Reconnect.InitialDelay)
	}
	if c.Reconnect.Jitter != common.ReconnectJitter {
		t.Fatalf("jitter default = %v", c.Reconnect.Jitter)
	}
	if c.Reconnect.MaxDelay != common.ReconnectMax {
		t.Fatalf("max_delay default = %v", c.Reconnect.MaxDelay)
	}
}

func TestLoadClient_BadScheme(t *testing.T) {
	body := `
server: "https://x:1"
node_id: n
profiles:
  - name: p
    dir: "/tmp/x"
pki: { ca_cert: a, client_cert: b, client_key: c }
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadClient_MissingFields(t *testing.T) {
	body := `
server: "wss://x:1"
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadClient_BadDuration(t *testing.T) {
	body := `
server: "wss://x:1"
node_id: n
profiles:
  - name: p
    dir: "/tmp/x"
pki: { ca_cert: a, client_cert: b, client_key: c }
reconnect:
  initial_delay: "five"
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadClient_DuplicateProfile(t *testing.T) {
	body := `
server: "wss://x:1"
node_id: n
profiles:
  - name: p
    dir: "/tmp/x"
  - name: p
    dir: "/tmp/y"
pki: { ca_cert: a, client_cert: b, client_key: c }
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadClient_ProfileMissingDir(t *testing.T) {
	body := `
server: "wss://x:1"
node_id: n
profiles:
  - name: p
    dir: ""
pki: { ca_cert: a, client_cert: b, client_key: c }
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadClient_JWTMode(t *testing.T) {
	body := `
server: "wss://x:1"
auth_mode: jwt
node_id: n
jwt:
  token_file: "~/token.jwt"
profiles:
  - name: p
    dir: "/tmp/x"
pki:
  ca_cert: a
`
	c, err := LoadClient(writeClient(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMode != AuthModeJWT {
		t.Fatalf("auth_mode = %q", c.AuthMode)
	}
	if c.JWT.SendVia != JWTSendViaAuto {
		t.Fatalf("send_via = %q", c.JWT.SendVia)
	}
}

func TestLoadClient_JWTModeRequiresTokenSource(t *testing.T) {
	body := `
server: "wss://x:1"
auth_mode: jwt
node_id: n
profiles:
  - name: p
    dir: "/tmp/x"
pki:
  ca_cert: a
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}
