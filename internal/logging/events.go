package logging

// Event names used for FieldEvent. Any new log site should pick an existing
// name or add one here (never use a free-form string).
const (
	// Per-node snapshot lifecycle.
	EvtSnapshotParsed    = "snapshot.parsed"
	EvtSnapshotValidated = "snapshot.validated"
	EvtSnapshotAbsent    = "snapshot.absent"

	// Server-side mediation.
	EvtMediatorCandidates = "mediator.candidates"
	EvtTruthSelected      = "mediator.truth_selected"
	EvtTruthUnchanged     = "mediator.truth_unchanged"
	EvtTruthPushed        = "truth.pushed"
	EvtTruthApplied       = "truth.applied"
	EvtTruthApplyFailed   = "truth.apply_failed"

	// Session lifecycle.
	EvtConnected          = "session.connected"
	EvtDisconnected       = "session.disconnected"
	EvtSessionRejected    = "session.rejected"
	EvtReconnectScheduled = "reconnect.scheduled"

	// Generic.
	EvtApplyFailed = "apply.failed"
	EvtConfigLoad  = "config.loaded"
	EvtConfigReload = "config.reloaded"
	EvtShutdown    = "shutdown"
	EvtStartup     = "startup"
)
