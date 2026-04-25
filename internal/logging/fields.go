// Package logging wraps log/slog with structured field/event name constants
// and a redacting handler that scrubs credential-like values.
package logging

// Structured field keys. All log sites MUST use these rather than string
// literals so field names stay consistent and grep-able.
const (
	FieldComponent    = "component"
	FieldProfile      = "profile"
	FieldNodeID       = "node_id"
	FieldEvent        = "event"
	FieldOutcome      = "outcome"
	FieldPath         = "path"
	FieldFingerprint  = "fingerprint"
	FieldTargetNodes  = "target_nodes"
	FieldReason       = "reason"
	FieldRemoteAddr   = "remote_addr"
	FieldPeerCN       = "peer_cn"
	FieldLatencyMS    = "latency_ms"
	FieldAttempt      = "attempt"
	FieldDelay        = "delay"
	FieldStatus       = "status"
	FieldExpiresAt    = "expires_at"
	FieldScopes       = "scopes"
	FieldSubscription = "subscription"
	FieldFormat       = "format"
	FieldStrategy     = "strategy"
)

// Canonical outcome values.
const (
	OutcomeOK        = "ok"
	OutcomeError     = "error"
	OutcomeRejected  = "rejected"
	OutcomeDegraded  = "degraded"
	OutcomeSkipped   = "skipped"
)

// Component labels used for FieldComponent.
const (
	ComponentServer     = "server"
	ComponentClient     = "client"
	ComponentSelfClient = "self-client"
	ComponentMediator   = "mediator"
	ComponentWatcher    = "watcher"
	ComponentTransport  = "transport"
	ComponentPKI        = "pki"
	ComponentFormat     = "format"
)
