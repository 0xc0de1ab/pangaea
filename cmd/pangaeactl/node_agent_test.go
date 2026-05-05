package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunNodeAgentRequiresRouterControlURL(t *testing.T) {
	err := runNodeAgent(context.Background(), nodeAgentRunOptions{NodeID: "node-a1", HostName: "host-a1"})
	if err == nil {
		t.Fatalf("expected router control url error")
	}
}

func TestNodeAgentRunCommandExists(t *testing.T) {
	cmd := newNodeAgentRunCmd()
	if cmd.Use != "run" {
		t.Fatalf("expected run command, got %q", cmd.Use)
	}
	for _, name := range []string{"config", "router-control", "node-id", "host-name", "heartbeat-interval", "runtime-kind", "runtime-version", "runtime-rootless"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected %s flag", name)
		}
	}
	if _, err := time.ParseDuration(cmd.Flags().Lookup("heartbeat-interval").DefValue); err != nil {
		t.Fatalf("heartbeat interval default is not a duration: %v", err)
	}
}

func TestRunNodeAgentBootstrapAuthCopiesConfiguredProviderAuth(t *testing.T) {
	dir := t.TempDir()
	hostPath := filepath.Join(dir, "host", "auth.json")
	containerPath := filepath.Join(dir, "container", "auth.json")
	configPath := filepath.Join(dir, "node-agent.yaml")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o700); err != nil {
		t.Fatalf("mkdir host: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"account":"samtest4u@gmail.com"}`), 0o600); err != nil {
		t.Fatalf("write host auth: %v", err)
	}
	config := `version: node-agent/v1
providers:
  - id: codex-samtest
    kind: cli-container
    service: codex
    auth:
      mode: file
      bootstrap: copy
      host_path: ` + hostPath + `
      container_path: ` + containerPath + `
    shim:
      capabilities: [api.openai.chat]
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := runNodeAgentBootstrapAuth(context.Background(), nodeAgentBootstrapAuthOptions{ConfigPath: configPath, ProviderID: "codex-samtest"}); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	data, err := os.ReadFile(containerPath)
	if err != nil {
		t.Fatalf("read container auth: %v", err)
	}
	if string(data) != `{"account":"samtest4u@gmail.com"}` {
		t.Fatalf("unexpected container auth: %s", string(data))
	}
}

func TestNodeAgentBootstrapAuthCommandExists(t *testing.T) {
	cmd := newNodeAgentBootstrapAuthCmd()
	if cmd.Use != "bootstrap-auth" {
		t.Fatalf("expected bootstrap-auth command, got %q", cmd.Use)
	}
	for _, name := range []string{"config", "provider"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected %s flag", name)
		}
	}
}

func TestRunNodeAgentReconcileProviderDryRun(t *testing.T) {
	dir := t.TempDir()
	hostPath := filepath.Join(dir, "host", "auth.json")
	configPath := filepath.Join(dir, "node-agent.yaml")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o700); err != nil {
		t.Fatalf("mkdir host: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"account":"samtest4u@gmail.com"}`), 0o600); err != nil {
		t.Fatalf("write host auth: %v", err)
	}
	config := `version: node-agent/v1
node:
  id: node-a1
  host_name: snowbox
providers:
  - id: codex-samtest
    kind: cli-container
    image: pangaea/provider-codex:test
    service: codex
    auth:
      mode: file
      bootstrap: copy
      host_path: ` + hostPath + `
      container_path: /var/lib/pangaea/auth/codex/auth.json
    shim:
      capabilities: [api.openai.chat]
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := runNodeAgentReconcileProvider(context.Background(), nodeAgentReconcileProviderOptions{
		ConfigPath: configPath,
		ProviderID: "codex-samtest",
		DryRun:     true,
	}); err != nil {
		t.Fatalf("reconcile provider dry-run: %v", err)
	}
}

func TestNodeAgentReconcileProviderCommandExists(t *testing.T) {
	cmd := newNodeAgentReconcileProviderCmd()
	if cmd.Use != "reconcile-provider" {
		t.Fatalf("expected reconcile-provider command, got %q", cmd.Use)
	}
	for _, name := range []string{"config", "provider", "node-id", "host-name", "runtime-kind", "docker-bin", "dry-run"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected %s flag", name)
		}
	}
}
