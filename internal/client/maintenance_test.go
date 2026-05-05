package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/pkg/formats/claudecreds"
	"github.com/0xc0de1ab/pangaea/pkg/formats/codexauth"
	"github.com/0xc0de1ab/pangaea/pkg/formats/geminioauth"
)

func TestCLIUpgradePackagesForAgents(t *testing.T) {
	got := cliUpgradePackagesForAgents([]*agent{
		{format: codexauth.Format{}},
		{format: geminioauth.Format{}},
		{format: codexauth.Format{}},
		{format: claudecreds.Format{}},
	})
	want := []string{
		"@anthropic-ai/claude-code",
		"@google/gemini-cli",
		"@openai/codex",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
}

func TestNewCLIUpgradeMaintainerDisabled(t *testing.T) {
	m := newCLIUpgradeMaintainer(
		config.CLIUpgradeConfig{Enabled: false, InitialDelay: time.Second, Interval: time.Hour},
		[]*agent{{format: codexauth.Format{}}},
		slog.Default(),
	)
	if m != nil {
		t.Fatalf("maintainer = %#v, want nil", m)
	}
}

func TestCLIUpgradeRunOnceUpgradesInstalledPackagesOnly(t *testing.T) {
	var upgraded []string
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := &cliUpgradeMaintainer{
		cfg:      config.CLIUpgradeConfig{Enabled: true, InitialDelay: time.Second, Interval: time.Hour},
		packages: []string{"@google/gemini-cli", "@openai/codex"},
		log:      log,
	}
	m.runCommand = func(_ context.Context, cmd refreshCommand) ([]byte, error) {
		switch cmd.Description {
		case "npm global package check":
			pkg := cmd.Args[len(cmd.Args)-1]
			if pkg == "@openai/codex" {
				return nil, nil
			}
			return nil, errors.New("missing")
		case "npm global CLI upgrade":
			upgraded = append(upgraded, cmd.Args[3:]...)
			return []byte("changed 1 package"), nil
		default:
			t.Fatalf("unexpected command: %+v", cmd)
			return nil, nil
		}
	}

	m.runOnce(context.Background())

	want := []string{"@openai/codex@latest"}
	if !slices.Equal(upgraded, want) {
		t.Fatalf("upgraded = %v, want %v", upgraded, want)
	}
}
