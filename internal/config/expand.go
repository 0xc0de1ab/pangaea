// Package config loads and validates the YAML configuration files used by the
// server (server.yaml), the client (client.yaml), and the profile registry
// (profiles.yaml). It also exposes a path-expander that honours
// $CLAUDE_CONFIG_DIR and ~ as documented in specs §6.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

const (
	// EnvClaudeConfigDir overrides the meaning of "~/.claude" when set, per
	// specs §6 panel notes (Han).
	EnvClaudeConfigDir = "CLAUDE_CONFIG_DIR"

	tildeClaudePrefix = "~/.claude"
	tildeOnly         = "~"
	tildeSlash        = "~/"
)

// ExpandPath turns user-typed paths into absolute filesystem paths under four
// rules (in priority order):
//
//  1. $CLAUDE_CONFIG_DIR is set AND p starts with "~/.claude" — replace the
//     "~/.claude" prefix with the env value.
//  2. p starts with "~/" or is exactly "~" — replace with the user's home dir.
//  3. $VAR / ${VAR} references are expanded from the environment. Missing vars
//     are rejected so config typos fail loudly.
//  4. otherwise return filepath.Clean(p) verbatim.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if cfgDir := os.Getenv(EnvClaudeConfigDir); cfgDir != "" {
		// Match "~/.claude" exactly or "~/.claude/..." but NOT "~/.claude-extra".
		if p == tildeClaudePrefix {
			return filepath.Clean(cfgDir), nil
		}
		if strings.HasPrefix(p, tildeClaudePrefix+"/") {
			rest := strings.TrimPrefix(p, tildeClaudePrefix)
			return filepath.Clean(cfgDir + rest), nil
		}
	}
	if p == tildeOnly {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", common.Wrap(err, common.ErrConfigInvalid, "resolve home dir")
		}
		return filepath.Clean(home), nil
	}
	if strings.HasPrefix(p, tildeSlash) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", common.Wrap(err, common.ErrConfigInvalid, "resolve home dir")
		}
		return filepath.Clean(home + strings.TrimPrefix(p, tildeOnly)), nil
	}
	if strings.Contains(p, "$") {
		expanded, err := expandEnvVars(p)
		if err != nil {
			return "", err
		}
		p = expanded
	}
	return filepath.Clean(p), nil
}

// ExpandPathFromDir resolves p against baseDir after applying ExpandPath.
// Relative paths stay relative to baseDir; absolute paths remain absolute.
func ExpandPathFromDir(baseDir, p string) (string, error) {
	v, err := ExpandPath(p)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", nil
	}
	if filepath.IsAbs(v) || baseDir == "" {
		return filepath.Clean(v), nil
	}
	return filepath.Clean(filepath.Join(baseDir, v)), nil
}

func expandEnvVars(p string) (string, error) {
	var missing []string
	expanded := os.Expand(p, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	if len(missing) > 0 {
		return "", common.Wrap(nil, common.ErrConfigInvalid, "undefined environment variable(s) in path %q: %s", p, strings.Join(uniqueStrings(missing), ", "))
	}
	return expanded, nil
}

func uniqueStrings(xs []string) []string {
	seen := make(map[string]struct{}, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
