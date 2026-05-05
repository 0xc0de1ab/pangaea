package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const (
	refreshInitialDelay   = 2 * time.Minute
	refreshCheckInterval  = 1 * time.Minute
	refreshNearExpiry     = 30 * time.Minute
	refreshCooldown       = 2 * time.Hour
	refreshCommandTimeout = 90 * time.Second
	refreshSettleWindow   = 10 * time.Second
	refreshPollInterval   = 500 * time.Millisecond
)

const (
	claudeFormatName = "claude-credentials-json-format"
	codexFormatName  = "codex-auth-json-format"
	geminiFormatName = "gemini-oauth-creds-json-format"
)

var lookPath = exec.LookPath

type refreshCommand struct {
	Name        string
	Args        []string
	Env         []string
	Dir         string
	Description string
}

type refreshState struct {
	fingerprint string
	result      formats.ValidationResult
}

type claudeRefreshFile struct {
	ClaudeAiOauth struct {
		RefreshToken string   `json:"refreshToken"`
		Scopes       []string `json:"scopes"`
	} `json:"claudeAiOauth"`
}

func (a *agent) refreshLoop(ctx context.Context) {
	if !supportsRefreshNudge(a.format.Name()) {
		return
	}

	timer := time.NewTimer(refreshInitialDelay)
	ticker := time.NewTicker(refreshCheckInterval)
	initial := timer.C
	defer timer.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-initial:
			a.maybeRefresh(ctx)
			initial = nil
		case <-ticker.C:
			a.maybeRefresh(ctx)
		}
	}
}

func supportsRefreshNudge(formatName string) bool {
	switch formatName {
	case claudeFormatName, codexFormatName, geminiFormatName:
		return true
	default:
		return false
	}
}

func (a *agent) maybeRefresh(ctx context.Context) {
	state, raw, ok := a.refreshCandidate(ctx)
	if !ok {
		return
	}
	defer zeroMaybe(raw)

	cmds, err := a.refreshCommands(raw)
	if err != nil {
		a.log.Warn("refresh nudge unavailable",
			slog.String(logging.FieldEvent, "refresh.nudge.unavailable"),
			slog.String(logging.FieldReason, err.Error()),
			slog.String(logging.FieldFingerprint, state.fingerprint),
		)
		return
	}
	cmds = filterAvailableRefreshCommands(cmds)
	if len(cmds) == 0 {
		a.log.Debug("refresh nudge skipped; provider cli not found in PATH",
			slog.String(logging.FieldEvent, "refresh.nudge.skipped"),
			slog.String("provider", a.format.Name()),
			slog.String(logging.FieldFingerprint, state.fingerprint),
		)
		return
	}
	if !a.claimRefreshAttempt(state.fingerprint, state.result.Detail) {
		return
	}

	for i, cmd := range cmds {
		attemptCtx, cancel := context.WithTimeout(ctx, refreshCommandTimeout)
		out, err := a.runCommand(attemptCtx, cmd)
		cancel()
		if err != nil {
			changed, newFP := a.waitForRefreshMutation(ctx, state.fingerprint)
			if changed {
				a.log.Info("refresh nudge updated credentials",
					slog.String(logging.FieldEvent, "refresh.nudge.updated"),
					slog.String("provider", a.format.Name()),
					slog.String("command", cmd.Description),
					slog.String("old_fingerprint", state.fingerprint),
					slog.String("new_fingerprint", newFP),
					slog.String(logging.FieldReason, refreshErrText(err, out)),
				)
				return
			}
			a.log.Warn("refresh nudge failed",
				slog.String(logging.FieldEvent, "refresh.nudge.failed"),
				slog.String("provider", a.format.Name()),
				slog.Int("attempt_index", i),
				slog.String("command", cmd.Description),
				slog.String(logging.FieldReason, refreshErrText(err, out)),
				slog.String(logging.FieldFingerprint, state.fingerprint),
			)
			continue
		}

		changed, newFP := a.waitForRefreshMutation(ctx, state.fingerprint)
		if changed {
			a.log.Info("refresh nudge updated credentials",
				slog.String(logging.FieldEvent, "refresh.nudge.updated"),
				slog.String("provider", a.format.Name()),
				slog.String("command", cmd.Description),
				slog.String("old_fingerprint", state.fingerprint),
				slog.String("new_fingerprint", newFP),
			)
			return
		}

		a.log.Info("refresh nudge completed without credential change",
			slog.String(logging.FieldEvent, "refresh.nudge.nochange"),
			slog.String("provider", a.format.Name()),
			slog.String("command", cmd.Description),
			slog.String(logging.FieldFingerprint, state.fingerprint),
		)
		return
	}
}

func (a *agent) maybeRefreshAsync(ctx context.Context) {
	go a.maybeRefresh(context.WithoutCancel(ctx))
}

func (a *agent) refreshCandidate(ctx context.Context) (refreshState, []byte, bool) {
	raw, err := readFileIfExists(a.path)
	if err != nil || raw == nil {
		return refreshState{}, nil, false
	}

	snap, err := a.format.Parse(raw)
	if err != nil {
		return refreshState{}, raw, false
	}

	now := time.Now
	if a.now != nil {
		now = a.now
	}
	res, _ := a.format.Validate(ctx, snap, formats.ValidateOpts{Clock: now})
	if !shouldRefreshNudge(now(), snap, res) {
		return refreshState{}, raw, false
	}

	return refreshState{
		fingerprint: snap.Fingerprint(),
		result:      res,
	}, raw, true
}

func shouldRefreshNudge(now time.Time, snap formats.Snapshot, res formats.ValidationResult) bool {
	switch res.Status {
	case formats.StatusExpired, formats.StatusRevoked:
		return true
	case formats.StatusOK:
		exp := snap.ExpiresAt()
		if exp.IsZero() {
			return false
		}
		return exp.After(now) && exp.Sub(now) <= refreshNearExpiry
	default:
		return false
	}
}

func (a *agent) claimRefreshAttempt(fingerprint, reason string) bool {
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}

	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	if a.lastRefreshFingerprint == fingerprint &&
		a.lastRefreshReason == reason &&
		!a.lastRefreshAttemptAt.IsZero() &&
		now.Sub(a.lastRefreshAttemptAt) < refreshCooldown {
		return false
	}

	a.lastRefreshAttemptAt = now
	a.lastRefreshFingerprint = fingerprint
	a.lastRefreshReason = reason
	return true
}

func (a *agent) refreshCommands(raw []byte) ([]refreshCommand, error) {
	switch a.format.Name() {
	case claudeFormatName:
		return a.claudeRefreshCommands(raw)
	case geminiFormatName:
		return a.geminiRefreshCommands(), nil
	case codexFormatName:
		return a.codexRefreshCommands(), nil
	default:
		return nil, fmt.Errorf("refresh nudge unsupported for format %q", a.format.Name())
	}
}

func (a *agent) claudeRefreshCommands(raw []byte) ([]refreshCommand, error) {
	var f claudeRefreshFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	if f.ClaudeAiOauth.RefreshToken == "" {
		return nil, fmt.Errorf("claude refresh token missing")
	}

	env := []string{
		"CLAUDE_CONFIG_DIR=" + a.dir,
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN=" + f.ClaudeAiOauth.RefreshToken,
	}
	if len(f.ClaudeAiOauth.Scopes) > 0 {
		env = append(env, "CLAUDE_CODE_OAUTH_SCOPES="+strings.Join(f.ClaudeAiOauth.Scopes, " "))
	}

	return []refreshCommand{
		{
			Name:        "claude",
			Args:        []string{"auth", "login"},
			Env:         env,
			Dir:         os.TempDir(),
			Description: "claude auth login",
		},
		{
			Name: "claude",
			Args: []string{
				"-p", "Reply with OK only.",
				"--permission-mode", "plan",
				"--tools", "",
				"--output-format", "text",
			},
			Env:         []string{"CLAUDE_CONFIG_DIR=" + a.dir},
			Dir:         os.TempDir(),
			Description: "claude --print oneshot",
		},
	}, nil
}

func (a *agent) geminiRefreshCommands() []refreshCommand {
	homeParent, ok := geminiHomeParent(a.dir)
	if !ok {
		return nil
	}
	args := []string{
		"-p", "Reply with OK only.",
		"--skip-trust",
		"--approval-mode", "plan",
		"--output-format", "json",
		"--model", "gemini-2.5-flash",
	}
	env := []string{
		"HOME=" + homeParent,
		"GEMINI_CLI_TRUST_WORKSPACE=true",
		"TERM=xterm-256color",
	}
	return []refreshCommand{
		{
			Name:        "gemini",
			Args:        args,
			Env:         env,
			Dir:         os.TempDir(),
			Description: "gemini oneshot prompt",
		},
		{
			Name: "bash",
			Args: append([]string{
				"-lic",
				`exec gemini "$@"`,
				"gemini-refresh",
			}, args...),
			Env:         env,
			Dir:         os.TempDir(),
			Description: "gemini oneshot prompt via login shell",
		},
	}
}

func (a *agent) codexRefreshCommands() []refreshCommand {
	args := []string{
		"exec",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"--ephemeral",
		"--ignore-user-config",
		"--color", "never",
		"-C", os.TempDir(),
		"Reply with OK only. Do not run any tools or shell commands.",
	}
	env := []string{"CODEX_HOME=" + a.dir}
	return []refreshCommand{
		{
			Name:        "codex",
			Args:        args,
			Env:         env,
			Dir:         os.TempDir(),
			Description: "codex exec oneshot",
		},
		{
			Name: "bash",
			Args: append([]string{
				"-lic",
				`exec codex "$@"`,
				"codex-refresh",
			}, args...),
			Env:         env,
			Dir:         os.TempDir(),
			Description: "codex exec oneshot via login shell",
		},
	}
}

func geminiHomeParent(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	if filepath.Base(dir) != ".gemini" {
		return "", false
	}
	return filepath.Dir(dir), true
}

func filterAvailableRefreshCommands(cmds []refreshCommand) []refreshCommand {
	if len(cmds) == 0 {
		return nil
	}
	out := make([]refreshCommand, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd.Name == "" {
			continue
		}
		if _, err := lookPath(cmd.Name); err != nil {
			continue
		}
		out = append(out, cmd)
	}
	return out
}

func (a *agent) waitForRefreshMutation(ctx context.Context, oldFingerprint string) (bool, string) {
	deadline := time.Now().Add(refreshSettleWindow)
	if a.now != nil {
		deadline = a.now().Add(refreshSettleWindow)
	}

	for {
		raw, err := readFileIfExists(a.path)
		if err == nil && raw != nil {
			snap, parseErr := a.format.Parse(raw)
			zeroMaybe(raw)
			if parseErr == nil && snap.Fingerprint() != oldFingerprint {
				return true, snap.Fingerprint()
			}
		}

		now := time.Now()
		if a.now != nil {
			now = a.now()
		}
		if !now.Before(deadline) {
			return false, oldFingerprint
		}

		if !sleepCtx(ctx, refreshPollInterval) {
			return false, oldFingerprint
		}
	}
}

func refreshErrText(err error, out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return err.Error()
	}
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return err.Error() + ": " + text
}

func zeroMaybe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
