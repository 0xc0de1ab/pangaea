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

func TestExpandPath_NoEnvExpansionOfDollar(t *testing.T) {
	t.Setenv(EnvClaudeConfigDir, "")
	got, err := ExpandPath("$HOME/foo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "$HOME/foo" {
		t.Fatalf("got %q (must NOT expand env vars)", got)
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
