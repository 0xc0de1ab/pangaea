package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/spf13/cobra"
)

func TestSetupServerMTLSInteractive(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "server")
	cmd := &cobra.Command{}
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetIn(strings.NewReader(strings.Join([]string{
		"",              // auth mode mtls
		"",              // listen
		"hub.local",     // tls host
		"",              // additional SANs
		"",              // ca cn
		"",              // ca years
		"",              // leaf years
		"host-a,host-b", // initial nodes
		"",              // add profile yes
		"claude-prod",   // profile name
		"",              // provider claude
		"",              // dir default
		"",              // allowed client ids default from initial nodes
		"n",             // add another profile
		"",              // issue client certs yes
		"n",             // telegram
		"n",             // systemd
	}, "\n") + "\n"))
	if err := runSetupServer(cmd, outDir); err != nil {
		t.Fatalf("setup server: %v", err)
	}

	cfg, err := config.LoadServer(filepath.Join(outDir, "pangaea-server.yaml"))
	if err != nil {
		t.Fatalf("load server config: %v", err)
	}
	if cfg.AuthMode != config.AuthModeMTLS {
		t.Fatalf("auth_mode = %q", cfg.AuthMode)
	}
	if _, err := os.Stat(filepath.Join(outDir, "issued-clients", "host-a", "client.crt")); err != nil {
		t.Fatalf("host-a client cert missing: %v", err)
	}
	profiles, err := config.LoadProfiles(filepath.Join(outDir, "profiles.yaml"))
	if err != nil {
		t.Fatalf("load profiles: %v", err)
	}
	if got := profiles.Profiles[0].AllowedClients; len(got) != 2 || got[0] != "host-a" || got[1] != "host-b" {
		t.Fatalf("allowed_clients = %#v", got)
	}
}

func TestSetupClientJWTInteractive(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "client")
	cmd := &cobra.Command{}
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetIn(strings.NewReader(strings.Join([]string{
		"jwt", // auth mode
		"wss://hub.local:8443",
		"workstation",
		"", // add profile yes
		"codex-prod",
		"codex",
		"",     // dir default
		"n",    // add another profile
		"file", // token source
		"",     // token file default
		"",     // send_via auto
		"n",    // systemd
	}, "\n") + "\n"))
	if err := runSetupClient(cmd, outDir); err != nil {
		t.Fatalf("setup client: %v", err)
	}

	cfg, err := config.LoadClient(filepath.Join(outDir, "pangaea-client.yaml"))
	if err != nil {
		t.Fatalf("load client config: %v", err)
	}
	if cfg.AuthMode != config.AuthModeJWT {
		t.Fatalf("auth_mode = %q", cfg.AuthMode)
	}
	if cfg.JWT.TokenFile == "" {
		t.Fatalf("token_file not set")
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Format != "codex-auth-json-format" {
		t.Fatalf("profiles = %#v", cfg.Profiles)
	}
}

func TestRenderSystemdUnitIncludesEnvFile(t *testing.T) {
	unit := renderSystemdUnit(systemdUnitSpec{
		Description: "demo",
		User:        "alice",
		WorkingDir:  "/srv/pangaea",
		ExecStart:   "/usr/local/bin/pangaeactl serve -c /srv/pangaea/pangaeactl-server.yaml",
		EnvFile:     "/srv/pangaea/pangaea-server.env",
	})
	if !strings.Contains(unit, "EnvironmentFile=-/srv/pangaea/pangaea-server.env") {
		t.Fatalf("unit missing env file:\n%s", unit)
	}
}
