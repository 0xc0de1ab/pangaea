package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
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
profile: "claude-prod"
node_id: "host-a"
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
`
	c, err := LoadClient(writeClient(t, body))
	if err != nil {
		t.Fatalf("LoadClient: %v", err)
	}
	if c.Reconnect.InitialDelay != 5*time.Second {
		t.Fatalf("initial_delay = %v", c.Reconnect.InitialDelay)
	}
}

func TestLoadClient_DefaultsApplied(t *testing.T) {
	body := `
server: "wss://x:1"
profile: p
node_id: n
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
profile: p
node_id: n
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
profile: p
node_id: n
pki: { ca_cert: a, client_cert: b, client_key: c }
reconnect:
  initial_delay: "five"
`
	_, err := LoadClient(writeClient(t, body))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}
