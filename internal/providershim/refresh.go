package providershim

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

type AuthRefresher interface {
	RefreshAuth(context.Context, control.AuthRefreshRequest, provider.Registration) (provider.AuthState, error)
}

type AuthRefresherFunc func(context.Context, control.AuthRefreshRequest, provider.Registration) (provider.AuthState, error)

func (f AuthRefresherFunc) RefreshAuth(ctx context.Context, request control.AuthRefreshRequest, registration provider.Registration) (provider.AuthState, error) {
	return f(ctx, request, registration)
}

type RefreshCommandSpec struct {
	Command    []string
	Env        map[string]string
	WorkingDir string
}

type RefreshCommandRunner interface {
	RunRefreshCommand(context.Context, RefreshCommandSpec) error
}

type RefreshCommandRunnerFunc func(context.Context, RefreshCommandSpec) error

func (f RefreshCommandRunnerFunc) RunRefreshCommand(ctx context.Context, spec RefreshCommandSpec) error {
	return f(ctx, spec)
}

type CommandAuthRefresherOptions struct {
	Command    []string
	Env        map[string]string
	WorkingDir string
	Timeout    time.Duration
	AuthPath   string
	Format     formats.Format
	Runner     RefreshCommandRunner
	Now        func() time.Time
}

type CommandAuthRefresher struct {
	command    []string
	env        map[string]string
	workingDir string
	timeout    time.Duration
	authPath   string
	format     formats.Format
	runner     RefreshCommandRunner
	now        func() time.Time
}

func NewCommandAuthRefresher(opts CommandAuthRefresherOptions) (*CommandAuthRefresher, error) {
	if len(opts.Command) == 0 || strings.TrimSpace(opts.Command[0]) == "" {
		return nil, fmt.Errorf("%w: refresh command is required", ErrShimConfig)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	runner := opts.Runner
	if runner == nil {
		runner = defaultRefreshCommandRunner{}
	}
	return &CommandAuthRefresher{
		command:    append([]string(nil), opts.Command...),
		env:        cloneStringMap(opts.Env),
		workingDir: opts.WorkingDir,
		timeout:    opts.Timeout,
		authPath:   strings.TrimSpace(opts.AuthPath),
		format:     opts.Format,
		runner:     runner,
		now:        now,
	}, nil
}

func (r *CommandAuthRefresher) RefreshAuth(ctx context.Context, request control.AuthRefreshRequest, registration provider.Registration) (provider.AuthState, error) {
	if r == nil || len(r.command) == 0 || r.runner == nil {
		return registration.Auth, fmt.Errorf("%w: refresh command is not configured", ErrShimConfig)
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	if !request.DeadlineAt.IsZero() {
		if deadline, ok := ctx.Deadline(); !ok || request.DeadlineAt.Before(deadline) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, request.DeadlineAt)
			defer cancel()
		}
	}
	if err := r.runner.RunRefreshCommand(ctx, RefreshCommandSpec{
		Command:    append([]string(nil), r.command...),
		Env:        cloneStringMap(r.env),
		WorkingDir: r.workingDir,
	}); err != nil {
		auth := registration.Auth
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		auth.LastRefreshAt = r.now().UTC()
		if r.authPath != "" && r.format != nil {
			if refreshedAuth, fileErr := r.authStateFromFile(ctx, auth); fileErr == nil {
				return refreshedAuth, nil
			}
		}
		return auth, err
	}
	auth := registration.Auth
	auth.Status = provider.AuthHealthy
	auth.LastRefreshAt = r.now().UTC()
	auth.LastRefreshErr = ""
	if r.authPath == "" || r.format == nil {
		return auth, nil
	}
	return r.authStateFromFile(ctx, auth)
}

func (r *CommandAuthRefresher) authStateFromFile(ctx context.Context, auth provider.AuthState) (provider.AuthState, error) {
	raw, err := os.ReadFile(r.authPath)
	if err != nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, fmt.Errorf("%w: read refreshed auth: %v", ErrShimConfig, err)
	}
	snapshot, err := r.format.Parse(raw)
	if err != nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	result, err := r.format.Validate(ctx, snapshot, formats.ValidateOpts{Clock: r.now})
	if err != nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = err.Error()
		return auth, err
	}
	auth.Status = authStatusFromValidation(result.Status)
	auth.ExpiresAt = snapshot.ExpiresAt()
	auth.SelectedSource = "container"
	auth.LastRefreshAt = r.now().UTC()
	auth.LastRefreshErr = ""
	if accountAware, ok := r.format.(formats.AccountAware); ok {
		if id, err := accountAware.Account(ctx, snapshot, r.authPath); err == nil {
			auth.Account.ID = id
		}
	}
	if displayAware, ok := r.format.(formats.AccountDisplayAware); ok {
		if display, err := displayAware.AccountDisplay(ctx, snapshot, r.authPath); err == nil {
			auth.Account.Display = display
		}
	}
	if auth.Status == provider.AuthExpired || auth.Status == provider.AuthRevoked || auth.Status == provider.AuthUnavailable {
		if result.Detail != "" {
			auth.LastRefreshErr = result.Detail
		}
		return auth, fmt.Errorf("%w: refreshed auth status %s", ErrShimConfig, result.Status)
	}
	return auth, nil
}

func authStatusFromValidation(status formats.ValidationStatus) provider.AuthStatus {
	switch status {
	case formats.StatusOK:
		return provider.AuthHealthy
	case formats.StatusScopeWarn:
		return provider.AuthConflict
	case formats.StatusExpired:
		return provider.AuthExpired
	case formats.StatusRevoked:
		return provider.AuthRevoked
	case formats.StatusUnreachable:
		return provider.AuthUnavailable
	default:
		return provider.AuthUnavailable
	}
}

type defaultRefreshCommandRunner struct{}

func (defaultRefreshCommandRunner) RunRefreshCommand(ctx context.Context, spec RefreshCommandSpec) error {
	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return fmt.Errorf("%w: refresh command is required", ErrShimConfig)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	cmd.Dir = spec.WorkingDir
	if len(spec.Env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range spec.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: refresh command failed: %v", ErrShimConfig, err)
	}
	return nil
}

func LoginShellRefreshCommand(binary string, args ...string) []string {
	if strings.TrimSpace(binary) == "" {
		return nil
	}
	command := `if [ -f "$HOME/.bashrc" ]; then . "$HOME/.bashrc"; fi; exec ` + shellQuote(binary) + ` "$@"`
	out := []string{"bash", "-lc", command, binary + "-refresh"}
	out = append(out, args...)
	return out
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
