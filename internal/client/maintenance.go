package client

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
)

const cliUpgradeCommandTimeout = 10 * time.Minute

var cliUpgradePackagesByFormat = map[string]string{
	claudeFormatName: "@anthropic-ai/claude-code",
	codexFormatName:  "@openai/codex",
	geminiFormatName: "@google/gemini-cli",
}

type cliUpgradeMaintainer struct {
	cfg        config.CLIUpgradeConfig
	packages   []string
	log        *slog.Logger
	runCommand func(context.Context, refreshCommand) ([]byte, error)
}

func newCLIUpgradeMaintainer(cfg config.CLIUpgradeConfig, agents []*agent, log *slog.Logger) *cliUpgradeMaintainer {
	if !cfg.Enabled {
		return nil
	}
	packages := cliUpgradePackagesForAgents(agents)
	if len(packages) == 0 {
		return nil
	}
	return &cliUpgradeMaintainer{
		cfg:      cfg,
		packages: packages,
		log: log.With(
			slog.String(logging.FieldComponent, logging.ComponentClient),
			slog.String("maintenance", "cli_upgrade"),
		),
		runCommand: defaultRunCommand,
	}
}

func cliUpgradePackagesForAgents(agents []*agent) []string {
	seen := make(map[string]struct{})
	for _, ag := range agents {
		if ag == nil || ag.format == nil {
			continue
		}
		pkg := cliUpgradePackagesByFormat[ag.format.Name()]
		if pkg == "" {
			continue
		}
		seen[pkg] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for pkg := range seen {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}

func (m *cliUpgradeMaintainer) run(ctx context.Context) error {
	if m == nil {
		return nil
	}

	timer := time.NewTimer(m.cfg.InitialDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil
	case <-timer.C:
		m.runOnce(ctx)
	}

	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.runOnce(ctx)
		}
	}
}

func (m *cliUpgradeMaintainer) runOnce(ctx context.Context) {
	if _, err := lookPath("bash"); err != nil {
		m.log.Debug("CLI upgrade skipped; bash not found in PATH",
			slog.String(logging.FieldEvent, "maintenance.cli_upgrade.skipped"),
			slog.String(logging.FieldReason, err.Error()),
		)
		return
	}

	installed := make([]string, 0, len(m.packages))
	for _, pkg := range m.packages {
		ok, err := m.npmGlobalPackageInstalled(ctx, pkg)
		if err != nil {
			m.log.Warn("CLI upgrade package check failed",
				slog.String(logging.FieldEvent, "maintenance.cli_upgrade.check_failed"),
				slog.String("package", pkg),
				slog.String(logging.FieldReason, err.Error()),
			)
			continue
		}
		if ok {
			installed = append(installed, pkg)
		}
	}
	if len(installed) == 0 {
		m.log.Debug("CLI upgrade skipped; no matching npm global packages",
			slog.String(logging.FieldEvent, "maintenance.cli_upgrade.skipped"),
			slog.String("packages", strings.Join(m.packages, ",")),
		)
		return
	}

	upgradeArgs := make([]string, 0, len(installed))
	for _, pkg := range installed {
		upgradeArgs = append(upgradeArgs, pkg+"@latest")
	}
	cmd := refreshCommand{
		Name: "bash",
		Args: append([]string{
			"-lic",
			`exec npm install -g "$@"`,
			"pangaea-cli-upgrade",
		}, upgradeArgs...),
		Dir:         os.TempDir(),
		Description: "npm global CLI upgrade",
	}

	attemptCtx, cancel := context.WithTimeout(ctx, cliUpgradeCommandTimeout)
	out, err := m.runCommand(attemptCtx, cmd)
	cancel()
	if err != nil {
		m.log.Warn("CLI upgrade failed",
			slog.String(logging.FieldEvent, "maintenance.cli_upgrade.failed"),
			slog.String("packages", strings.Join(installed, ",")),
			slog.String(logging.FieldReason, refreshErrText(err, out)),
		)
		return
	}
	m.log.Info("CLI upgrade completed",
		slog.String(logging.FieldEvent, "maintenance.cli_upgrade.completed"),
		slog.String("packages", strings.Join(installed, ",")),
		slog.String("output", maintenanceOutputText(out)),
	)
}

func (m *cliUpgradeMaintainer) npmGlobalPackageInstalled(ctx context.Context, pkg string) (bool, error) {
	cmd := refreshCommand{
		Name: "bash",
		Args: []string{
			"-lic",
			`command -v npm >/dev/null 2>&1 || exit 127; npm list -g "$1" --depth=0 >/dev/null 2>&1 || exit 42`,
			"pangaea-cli-upgrade-check",
			pkg,
		},
		Dir:         os.TempDir(),
		Description: "npm global package check",
	}
	attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, err := m.runCommand(attemptCtx, cmd)
	cancel()
	if err == nil {
		return true, nil
	}

	// npm list exits non-zero for a missing package. Treat that as a skip,
	// but keep context cancellation as a real failure so shutdown is quiet.
	if errorsIsContext(attemptCtx.Err()) {
		return false, attemptCtx.Err()
	}
	if exitCode(err) == 42 {
		return false, nil
	}
	return false, err
}

func maintenanceOutputText(out []byte) string {
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return ""
	}
	const maxFields = 40
	if len(lines) > maxFields {
		lines = slices.Concat(lines[:maxFields], []string{"..."})
	}
	return strings.Join(lines, " ")
}

func errorsIsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
