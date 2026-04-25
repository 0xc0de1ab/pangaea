// Package formats defines the credential file format abstraction used by the
// claude-creds-share server and client. A Format implementation is a *read-only
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
	"net/http"
	"time"
)

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
