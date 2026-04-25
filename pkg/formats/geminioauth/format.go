// Package geminioauth implements the "gemini-oauth-creds-json-format" Format
// for Google Gemini CLI's ~/.gemini/oauth_creds.json file. The on-disk schema
// is a `google-auth-library` Credentials object — a thin wrapper over the
// Google OAuth2 token response — with `expiry_date` carrying epoch
// milliseconds.
//
// This package is a *read-only interpreter*. It NEVER refreshes tokens, NEVER
// writes the file, and NEVER POSTs to Google. There is no live-check probe;
// freshness is purely local (expiry_date vs clock with a 5-minute eager-refresh
// skew matching the upstream library).
//
// Source survey of github.com/google-gemini/gemini-cli + the underlying
// googleapis/google-auth-library-nodejs (the file format is the latter's,
// the freshness logic is also there):
//   - File schema:      google-auth-library-nodejs/src/auth/credentials.ts (interface Credentials, lines 18-42)
//   - Persistence:      gemini-cli/packages/core/src/code_assist/oauth2.ts:749-760 (cacheCredentials, mode 0o600)
//   - Freshness rule:   google-auth-library-nodejs/src/auth/oauth2client.ts:1291-1297 (isTokenExpiring)
//   - Skew window:      google-auth-library-nodejs/src/auth/authclient.ts:102 (eagerRefreshThresholdMillis = 5min)
package geminioauth

import (
	"fmt"
	"path/filepath"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// FormatName is the registry key referenced from profiles.yaml's `format:`
// field.
const FormatName = "gemini-oauth-creds-json-format"

// StrategyExpiryDateMax orders snapshots by expiry_date (epoch ms); later
// wins. There are no other monotonic fields on disk so this is the only
// comparison strategy that mirrors the library's own behaviour.
const StrategyExpiryDateMax = "expiry_date_max"

// Format is the singleton registered in init(). Stateless.
type Format struct{}

// Name returns the format identifier.
func (Format) Name() string { return FormatName }

// Strategies lists the supported comparison strategies; first is the default.
func (Format) Strategies() []string {
	return []string{StrategyExpiryDateMax}
}

// CredentialPath resolves the Gemini config directory to oauth_creds.json.
func (Format) CredentialPath(dir string) string {
	return filepath.Join(dir, "oauth_creds.json")
}

func init() {
	formats.Register(Format{})
}

// strategyMustBeKnown panics with a helpful message if strategy is unknown.
// Per spec, the caller is responsible for checking against Strategies();
// a loud panic surfaces the bug at the call site.
func strategyMustBeKnown(strategy string) {
	if strategy != StrategyExpiryDateMax {
		panic(fmt.Sprintf(
			"geminioauth: unknown comparison strategy %q (supported: %v); caller must validate against Format.Strategies() before calling Compare",
			strategy, []string{StrategyExpiryDateMax},
		))
	}
}
