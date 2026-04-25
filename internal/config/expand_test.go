package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath_NoTilde(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	got, err := ExpandPath("/etc/foo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/etc/foo" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPath_TildeOnly(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this system")
	}
	got, err := ExpandPath("~")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(home) {
		t.Fatalf("got %q want %q", got, home)
	}
}

func TestExpandPath_TildeSlash(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got, err := ExpandPath("~/foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(home+"/foo/bar") {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPath_ClaudeConfigDirPrefixOnly(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "/opt/claude")
	got, err := ExpandPath("~/.claude")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/claude" {
		t.Fatalf("got %q want /opt/claude", got)
	}
}

func TestExpandPath_ClaudeConfigDirPrefix(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "/opt/claude")
	got, err := ExpandPath("~/.claude/.credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/claude/.credentials.json" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPath_ClaudeConfigDirNotPrefix(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "/opt/claude")
	// "~/.claude-extra" should NOT match the override; it falls through to
	// regular tilde expansion.
	home, _ := os.UserHomeDir()
	got, err := ExpandPath("~/.claude-extra/x")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(home+"/.claude-extra/x") {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPath_ExpandsEnvVars(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	t.Setenv("HOME", "/tmp/home")
	got, err := ExpandPath("$HOME/foo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/home/foo" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPath_RejectsMissingEnvVars(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	_ = os.Unsetenv("DOES_NOT_EXIST")
	if _, err := ExpandPath("$DOES_NOT_EXIST/foo"); err == nil {
		t.Fatalf("expected error for missing environment variable")
	}
}

func TestExpandPathFromDir_ResolvesRelativePath(t *testing.T) {
	got, err := ExpandPathFromDir("/opt/claude", "../.claude.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/.claude.json" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPathFromDir_ExpandsHomeTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this system")
	}
	got, err := ExpandPathFromDir("/opt/claude", "~/.claude.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(filepath.Join(home, ".claude.json")) {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPath_Empty(t *testing.T) {
	got, err := ExpandPath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("got %q", got)
	}
}
