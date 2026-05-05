// Package codexauth implements the "codex-auth-json-format" Format for
// OpenAI Codex CLI's $CODEX_HOME/auth.json file (default ~/.codex/auth.json).
//
// This package is a *read-only interpreter* of the file. It NEVER refreshes
// tokens, NEVER writes the auth file, and NEVER POSTs to OpenAI. There is no
// live-check probe — codex does not expose a non-mutating endpoint we can
// reuse, and a freshness decision is purely local (access-token JWT exp).
//
// Source survey of github.com/openai/codex (Rust rewrite at codex-rs/login):
//   - File schema:        codex-rs/login/src/auth/storage.rs (struct AuthDotJson, TokenData)
//   - Freshness rule:     Codex chat auth refresh path (access-token exp + safety skew)
//   - JWT exp parsing:    codex-rs/login/src/token_data.rs (parse_jwt_expiration)
package codexauth

import (
	"fmt"
	"path/filepath"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// FormatName is the registry key referenced from profiles.yaml's `format:`
// field. Exported so cmd/inspect and tests can reference it without a literal.
const FormatName = "codex-auth-json-format"

// StrategyJWTExpMax orders snapshots by the access-token JWT's `exp` claim
// (later wins), tiebreaking on `last_refresh`. Mirrors how codex itself
// decides which token to keep when reconciling on-disk state.
const StrategyJWTExpMax = "jwt_exp_max"

// Format is the singleton registered in init(). Stateless.
type Format struct{}

// Name returns the format identifier.
func (Format) Name() string { return FormatName }

// Strategies lists the supported comparison strategies; first is the default.
func (Format) Strategies() []string {
	return []string{StrategyJWTExpMax}
}

// CredentialPath resolves the Codex config directory to auth.json.
func (Format) CredentialPath(dir string) string {
	return filepath.Join(dir, "auth.json")
}

func init() {
	formats.Register(Format{})
}

// strategyMustBeKnown panics with a helpful message if strategy is unknown.
// Per spec, the caller is responsible for checking against Strategies(); a
// loud panic surfaces the bug at the call site rather than producing a silent
// 0 return.
func strategyMustBeKnown(strategy string) {
	if strategy != StrategyJWTExpMax {
		panic(fmt.Sprintf(
			"codexauth: unknown comparison strategy %q (supported: %v); caller must validate against Format.Strategies() before calling Compare",
			strategy, []string{StrategyJWTExpMax},
		))
	}
}
