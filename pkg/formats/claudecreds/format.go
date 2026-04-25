// Package claudecreds implements the "claude-credentials-json-format" Format.
//
// This package is a *read-only interpreter* of ~/.claude/.credentials.json.
// It NEVER refreshes tokens, NEVER writes the credentials file, and NEVER
// POSTs to Anthropic. The optional live check issues a single GET to
// /api/oauth/profile to verify the access token is still accepted.
package claudecreds

import (
	"fmt"
	"path/filepath"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// FormatName is the registry key referenced from profiles.yaml's `format:`
// field. Exported so cmd/inspect and tests can reference it without a literal.
const FormatName = "claude-credentials-json-format"

// StrategyExpiresAtMax orders snapshots by ExpiresAt; later wins.
const StrategyExpiresAtMax = "expires_at_max"

// Format is the singleton registered in init(). It carries no state — all
// behaviour lives on its methods.
type Format struct{}

// Name returns the format identifier.
func (Format) Name() string { return FormatName }

// Strategies lists the comparison strategies this format supports. The first
// entry is the default when a profile leaves validate.strategy blank.
func (Format) Strategies() []string {
	return []string{StrategyExpiresAtMax}
}

// CredentialPath resolves the Claude config directory to its primary OAuth
// credentials file.
func (Format) CredentialPath(dir string) string {
	return filepath.Join(dir, ".credentials.json")
}

// WatchPaths asks the client to watch both the primary credentials file and
// the account metadata files that Claude uses to describe the logged-in user.
func (Format) WatchPaths(dir string) []string {
	homeDir := filepath.Dir(dir)
	return []string{
		filepath.Join(dir, ".credentials.json"),
		filepath.Join(homeDir, ".claude.json"),
		filepath.Join(dir, ".config.json"),
	}
}

// init registers the singleton. Done in init so any binary that imports this
// package automatically wires the format into the registry.
func init() {
	formats.Register(Format{})
}

// strategyMustBeKnown panics with a helpful message if strategy is unknown.
// Per spec, the caller is responsible for checking against Strategies(); a
// loud panic surfaces the bug at the call site rather than producing a silent
// 0 return.
func strategyMustBeKnown(strategy string) {
	if strategy != StrategyExpiresAtMax {
		panic(fmt.Sprintf(
			"claudecreds: unknown comparison strategy %q (supported: %v); caller must validate against Format.Strategies() before calling Compare",
			strategy, []string{StrategyExpiresAtMax},
		))
	}
}
