// Package formats defines the credential file format abstraction used by the
// pangaea server and client. A Format implementation is a *read-only
// interpreter*: it parses raw bytes into a Snapshot, optionally validates the
// snapshot (including a non-mutating live check against the issuing service),
// compares two snapshots under a named strategy, and produces a redacted
// Summary safe to log or send over the wire.
//
// Implementations register themselves in their package init() via Register so
// the server and client can resolve formats by name from configuration. The
// registry is the only mutable global state in this package.
//
// IMPORTANT: A Format MUST NEVER write the credential file, refresh tokens, or
// otherwise mutate remote state. Live checks are GET-only.
package formats

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"
)

// AccountAware is an optional interface a Format may implement when it can
// derive a stable per-LLM-account identifier from a snapshot. The client
// calls Account at report time and includes the result in
// transport.SnapshotReport so the server can partition candidates and truth
// state by (profile, account) — preventing two distinct LLM accounts that
// happen to share a profile name from overwriting each other's tokens.
//
// path is the local filesystem path of the credentials file. Implementations
// that need peer metadata (e.g. an account-name file living adjacent to the
// credentials file) read it from there. Implementations that can derive the
// account from the snapshot alone (e.g. JWT id_token claims) ignore path.
//
// A return value of "" means the format can't determine an account in this
// case — the server treats those candidates as belonging to one shared
// bucket. Errors should be reserved for unexpected failures; "missing
// metadata" is best reported as an empty string.
type AccountAware interface {
	Account(ctx context.Context, snap Snapshot, path string) (string, error)
}

// DirResolver is an optional interface a Format may implement when operators
// configure a profile with a config directory rather than a concrete
// credentials file path. Implementations map that directory to the primary
// credentials file they parse and sync.
type DirResolver interface {
	CredentialPath(dir string) string
}

// WatchPathsAware is an optional companion to DirResolver. Formats that need
// additional local files to interpret account state (for example Claude's
// sibling account metadata) can ask the client to watch those paths too. Any
// change to a watched path causes the client to re-read the primary
// credentials file and re-report the current snapshot.
type WatchPathsAware interface {
	WatchPaths(dir string) []string
}

// UsageReport is the structured outcome of UsageProbe.Probe. It is meant
// for human-facing notification output (Telegram, status command) — every
// field is optional and may be omitted when the upstream API does not
// expose that signal. Implementations MUST NOT include access tokens or
// other secrets in any field.
type UsageReport struct {
	// PlanTier is a short label (e.g. "claude_max_20x", "gpt_pro").
	PlanTier string `json:"plan_tier,omitempty"`
	// Used / Limit / RemainingPct describe quota state. Values are
	// implementation-defined units (messages, tokens, requests). The pair
	// (Used, Limit) is preferred; RemainingPct is a fallback when the
	// upstream API only exposes a percentage.
	Used         int64   `json:"used,omitempty"`
	Limit        int64   `json:"limit,omitempty"`
	RemainingPct float64 `json:"remaining_pct,omitempty"`
	// Unit labels Used/Limit (e.g. "messages", "tokens").
	Unit string `json:"unit,omitempty"`
	// ResetAt is when the current usage window rolls over.
	ResetAt time.Time `json:"reset_at,omitempty"`
	// Notes carries any free-form per-format human-readable hints
	// (organization name, role, plan label) the notifier should surface.
	Notes []string `json:"notes,omitempty"`
}

// UsageProbe is an optional interface a Format may implement when the
// upstream provider exposes a usage / quota / rate-limit endpoint reachable
// with the OAuth access_token already on disk. The server's notifier calls
// Probe periodically per (profile, account) to enrich the message it sends
// to operators.
//
// httpClient lets callers inject a pre-configured client (timeouts,
// transport). path is the credentials file path; the same fallback semantics
// as AccountAware apply for formats that need peer metadata files. The
// returned UsageReport must not embed any secrets.
type UsageProbe interface {
	Probe(ctx context.Context, snap Snapshot, path string, httpClient *http.Client) (UsageReport, error)
}

// Format is a read-only interpreter for a particular credential file schema.
// All methods must be safe for concurrent use.
type Format interface {
	// Name is a stable identifier referenced from profiles.yaml's `format:` key.
	Name() string

	// Strategies returns the comparison strategies this format supports.
	// The first entry is the default if a profile leaves the field blank.
	Strategies() []string

	// Parse decodes raw file bytes into a Snapshot. Implementations MUST return
	// errors that errors.Is(common.ErrParseFailed) — typically via common.Wrap.
	// expiresAt being in the past is NOT a parse error; Validate decides that.
	Parse(raw []byte) (Snapshot, error)

	// Validate inspects the snapshot. With opts.LiveCheck=false it performs only
	// local checks (e.g. expiry vs Clock). With LiveCheck=true it issues a
	// read-only request to the issuing service. The function returns a
	// ValidationResult and a non-nil error only for unexpected programmer
	// errors; transport/HTTP failures are reported via Status=unreachable.
	Validate(ctx context.Context, snap Snapshot, opts ValidateOpts) (ValidationResult, error)

	// Compare orders two snapshots under the named strategy. The caller MUST
	// have verified that strategy is contained in Strategies(); implementations
	// are encouraged to panic on unknown strategies to surface programmer
	// errors loudly.
	//
	// Return values: -1 if a is older/lesser than b, 0 if equal, +1 if a is
	// newer/greater than b.
	Compare(strategy string, a, b Snapshot) int

	// Redact returns a Summary containing only fields safe to log or transmit.
	// It MUST NOT include access or refresh tokens.
	Redact(snap Snapshot) Summary
}

// Snapshot is the parsed view of one credential file. Raw() yields the
// original bytes; callers must treat them as sensitive (do not log, do not
// retain aliases beyond the snapshot's lifetime).
type Snapshot interface {
	// Identity is a short, stable, non-reversible identifier suitable for
	// log correlation. It MUST NOT be a raw token.
	Identity() string

	// ExpiresAt is the credential's absolute expiry instant. Zero time means
	// "no expiry recorded" and is treated as expired by Validate.
	ExpiresAt() time.Time

	// Raw returns a defensive copy of the original file bytes. Callers should
	// safeio.Zeroize the returned slice when done.
	Raw() []byte

	// Fingerprint is the lowercase hex sha256 of the raw bytes.
	Fingerprint() string
}

// ValidateOpts controls Validate behaviour. The zero value is valid and means
// "local check only, default clock".
type ValidateOpts struct {
	// LiveCheck enables a network probe of the issuing service.
	LiveCheck bool
	// Timeout caps the live-check request. Zero falls back to
	// common.LiveCheckDefaultTimeout.
	Timeout time.Duration
	// HTTPClient is the client used for live checks. Nil falls back to a
	// freshly-constructed default with the configured Timeout.
	HTTPClient *http.Client
	// Clock returns the current time. Nil falls back to time.Now.
	Clock func() time.Time
}

// ResolveCredentialPath maps a configured directory to the primary
// credentials file path for f.
func ResolveCredentialPath(f Format, dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("formats.ResolveCredentialPath: empty dir")
	}
	r, ok := f.(DirResolver)
	if !ok {
		return "", fmt.Errorf("formats.ResolveCredentialPath: format %q does not implement DirResolver", f.Name())
	}
	path := filepath.Clean(r.CredentialPath(dir))
	if path == "." || path == "" {
		return "", fmt.Errorf("formats.ResolveCredentialPath: format %q returned an empty credential path", f.Name())
	}
	return path, nil
}

// ResolveWatchPaths returns the deduplicated local files the client should
// watch for one configured directory. The primary credentials file is always
// included.
func ResolveWatchPaths(f Format, dir string) ([]string, error) {
	credPath, err := ResolveCredentialPath(f, dir)
	if err != nil {
		return nil, err
	}

	raw := []string{credPath}
	if w, ok := f.(WatchPathsAware); ok {
		raw = append(raw, w.WatchPaths(dir)...)
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, p := range raw {
		p = filepath.Clean(p)
		if p == "." || p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("formats.ResolveWatchPaths: format %q returned no watch paths", f.Name())
	}
	return out, nil
}
