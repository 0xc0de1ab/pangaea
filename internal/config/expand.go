// Package config loads and validates the YAML configuration files used by the
// server (server.yaml), the client (client.yaml), and the profile registry
// (profiles.yaml). It also exposes a path-expander that honours
// $CLAUDE_CONFIG_DIR and ~ as documented in specs §6.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

const (
	// EnvClaudeConfigDir overrides the meaning of "~/.claude" when set, per
	// specs §6 panel notes (Han).
	EnvClaudeConfigDir = "CLAUDE_CONFIG_DIR"

	tildeClaudePrefix = "~/.claude"
	tildeOnly         = "~"
	tildeSlash        = "~/"
)

// ExpandPath turns user-typed paths into absolute filesystem paths under three
// rules (in priority order):
//
//  1. $CLAUDE_CONFIG_DIR is set AND p starts with "~/.claude" — replace the
//     "~/.claude" prefix with the env value.
//  2. p starts with "~/" or is exactly "~" — replace with the user's home dir.
//  3. otherwise return filepath.Clean(p) verbatim.
//
// Embedded $VAR references elsewhere in p are NOT expanded; the spec is
// strict about that to avoid surprising substitutions in path contexts.
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
	return filepath.Clean(p), nil
}
