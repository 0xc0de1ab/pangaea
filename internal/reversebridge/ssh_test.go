package reversebridge

import (
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/config"
)

func TestSplitSSHTarget(t *testing.T) {
	tests := []struct {
		in       string
		wantUser string
		wantHost string
	}{
		{in: "operator@a2.oci.example.com", wantUser: "operator", wantHost: "a2.oci.example.com"},
		{in: "a2", wantUser: "", wantHost: "a2"},
	}
	for _, tt := range tests {
		gotUser, gotHost := splitSSHTarget(tt.in)
		if gotUser != tt.wantUser || gotHost != tt.wantHost {
			t.Fatalf("splitSSHTarget(%q) = (%q,%q), want (%q,%q)", tt.in, gotUser, gotHost, tt.wantUser, tt.wantHost)
		}
	}
}

func TestMaterializeSSHNodeDefaults(t *testing.T) {
	node, err := materializeSSHNode(config.SSHNodeConfig{
		NodeID:       "a2",
		Target:       "operator@a2",
		UseSSHConfig: false,
	})
	if err != nil {
		t.Fatalf("materializeSSHNode: %v", err)
	}
	if node.Port != 22 {
		t.Fatalf("port = %d", node.Port)
	}
	if node.Command != defaultManagedCommand {
		t.Fatalf("command = %q", node.Command)
	}
	if node.ConfigPath != defaultManagedConfigPath {
		t.Fatalf("config_path = %q", node.ConfigPath)
	}
	if len(node.KnownHosts) == 0 {
		t.Fatal("known_hosts defaults missing")
	}
	if len(node.IdentityFile) == 0 {
		t.Fatal("identity defaults missing")
	}
}

func TestBuildManagedRemoteCommand(t *testing.T) {
	cmd := buildManagedRemoteCommand(sshResolvedNode{
		Command:    "/usr/local/bin/pangaeactl",
		ConfigPath: "$HOME/pangaea-client.yaml",
	}, "claude")
	for _, needle := range []string{
		"reverse-client",
		"--profile",
		"claude",
		"--listen",
		"127.0.0.1:0",
		"--print-listen-addr",
	} {
		if !strings.Contains(cmd, needle) {
			t.Fatalf("managed command missing %q: %s", needle, cmd)
		}
	}
}
