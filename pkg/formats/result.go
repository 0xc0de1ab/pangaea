package formats

import "time"

// ValidationStatus captures the outcome of Format.Validate. The string values
// are part of the wire protocol (snapshot.report.live_check.result) and MUST
// remain stable.
type ValidationStatus string

const (
	StatusOK          ValidationStatus = "ok"
	StatusExpired     ValidationStatus = "expired"
	StatusRevoked     ValidationStatus = "revoked"
	StatusUnreachable ValidationStatus = "unreachable"
	StatusParseError  ValidationStatus = "parse_error"
	StatusScopeWarn   ValidationStatus = "scope_warn"
)

// ValidationResult is the redaction-safe outcome of Format.Validate.
type ValidationResult struct {
	Status    ValidationStatus `json:"status"`
	Detail    string           `json:"detail,omitempty"`
	CheckedAt time.Time        `json:"checked_at"`
}

// Summary is the redaction-safe view emitted by Format.Redact. The JSON shape
// is part of the transport contract (see internal/transport.SnapshotReport)
// and MUST stay backwards compatible.
type Summary struct {
	Identity         string            `json:"identity"`
	Subscription     string            `json:"subscription,omitempty"`
	FingerprintShort string            `json:"fingerprint_short"`
	TokenTail4       string            `json:"token_tail4,omitempty"`
	ExpiresAt        time.Time         `json:"expires_at"`
	Scopes           []string          `json:"scopes,omitempty"`
	Extra            map[string]string `json:"extra,omitempty"`
}
