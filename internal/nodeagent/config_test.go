package nodeagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestParseConfigYAMLSupportsMultipleProvidersForSameService(t *testing.T) {
	cfg, err := ParseConfigYAML([]byte(`
version: node-agent/v1
node:
  id: node-a1
  host_name: snowbox
runtime:
  kind: docker
  version: 26.1.0
providers:
  - id: codex-samtest
    kind: cli-container
    image: pangaea/provider-codex:2026.05.1
    account_hint: samtest4u@gmail.com
    service: codex
    auth:
      mode: file
      bootstrap: copy
      host_path: /srv/pangaea/auth/codex/samtest/auth.json
      container_path: /var/lib/pangaea/auth/codex/auth.json
      file_mode: "0600"
    refresh:
      threshold: 5m
      command: [codex, exec, "Reply with OK only."]
      cooldown: 2h
      timeout: 90s
    shim:
      protocols: [openai]
      capabilities: [api.openai.chat, auth.refresh.oneshot]
  - id: codex-nullcode
    kind: cli-container
    account_hint: nullcode@gmail.com
    service: codex
    auth:
      mode: file
      host_path: /srv/pangaea/auth/codex/nullcode/auth.json
      container_path: /var/lib/pangaea/auth/codex/auth.json
    shim:
      capabilities: [api.openai.chat]
`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected two providers, got %#v", cfg.Providers)
	}
	if cfg.Providers[0].Auth.HostPath == cfg.Providers[1].Auth.HostPath {
		t.Fatalf("provider auth host paths must remain distinct: %#v", cfg.Providers)
	}
	registration := cfg.Providers[0].Registration(cfg.Node.ID, cfg.Node.HostName, nowForTest())
	if registration.Identity.HostName != "snowbox" || registration.Identity.Service != provider.ServiceCodex {
		t.Fatalf("unexpected registration: %#v", registration)
	}
	if !registration.Auth.Refreshable {
		t.Fatalf("expected refreshable registration")
	}
}

func TestParseConfigYAMLRejectsDuplicateProviderID(t *testing.T) {
	_, err := ParseConfigYAML([]byte(`
version: node-agent/v1
providers:
  - id: codex-samtest
    kind: cli-container
    service: codex
    auth:
      mode: file
      host_path: /a
      container_path: /b
  - id: codex-samtest
    kind: cli-container
    service: codex
    auth:
      mode: file
      host_path: /c
      container_path: /d
`))
	if err == nil {
		t.Fatalf("expected duplicate provider id error")
	}
}

func TestBootstrapAuthCopyCopiesFileAtomically(t *testing.T) {
	dir := t.TempDir()
	hostPath := filepath.Join(dir, "host", "auth.json")
	containerPath := filepath.Join(dir, "container", "auth.json")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o700); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"account":"samtest4u@gmail.com"}`), 0o600); err != nil {
		t.Fatalf("write host auth: %v", err)
	}

	result, err := BootstrapAuthCopy(context.Background(), AuthSpec{
		Mode:          "file",
		Bootstrap:     "copy",
		HostPath:      hostPath,
		ContainerPath: containerPath,
		FileMode:      "0600",
	})
	if err != nil {
		t.Fatalf("bootstrap auth copy: %v", err)
	}
	if result.Bytes == 0 || result.ContainerPath != containerPath {
		t.Fatalf("unexpected bootstrap result: %#v", result)
	}
	data, err := os.ReadFile(containerPath)
	if err != nil {
		t.Fatalf("read copied auth: %v", err)
	}
	if string(data) != `{"account":"samtest4u@gmail.com"}` {
		t.Fatalf("unexpected copied auth: %s", string(data))
	}
	info, err := os.Stat(containerPath)
	if err != nil {
		t.Fatalf("stat copied auth: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("copied auth mode = %o", info.Mode().Perm())
	}
}

func nowForTest() time.Time {
	return time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
}
