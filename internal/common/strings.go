package common

// Reusable human-visible strings. Keep messages short and neutral; downstream
// callers can wrap with context via Wrap().
const (
	MsgConfigMissing         = "configuration file not found: %s"
	MsgConfigInvalid         = "configuration is invalid: %s"
	MsgProfileUnknown        = "profile not found: %s"
	MsgFormatUnknown         = "format not registered: %s"
	MsgAllowedClientEmpty    = "allowed_clients must not contain an empty entry"
	MsgProfileDirEmpty       = "profile %q: dir must not be empty"
	MsgSANEmpty              = "at least one SAN (IP or DNS) entry is required"
	MsgNotAfterInPast        = "certificate notAfter must be in the future"
	MsgInvalidEnvelopeV      = "unsupported envelope version: %d"
	MsgInvalidKind           = "unknown message kind: %q"
	MsgPayloadParseFailed    = "payload parse failed: %s"
	MsgTLSHandshakeFailed    = "TLS handshake failed: %s"
	MsgLockTimeout           = "failed to acquire lock within %s"
	MsgCNNotAllowed          = "client CN %q not in allowed_clients for profile %q"
	MsgCNDuplicate           = "duplicate session for CN %q; closing previous"
	MsgApplyVerifyFailed     = "applied file did not match expected fingerprint"
	MsgNoCandidate           = "no viable candidate for profile %q"
	MsgJWTProfileDenied      = "JWT subject %q not allowed for profile %q"
	MsgAuthJWTRequired       = "auth.jwt required before hello"
	MsgAuthJWTInvalid        = "auth.jwt invalid"
	MsgHelloIdentityMismatch = "hello.node_id must match authenticated identity"

	// CLI one-line descriptions.
	CLIShortRoot    = "pangaeactl — TLS WebSocket credential file sync"
	CLIShortServe   = "run the mediating server"
	CLIShortConnect = "connect to a server as a client node"
	CLIShortCA      = "certificate authority management (init / issue)"
	CLIShortJWT     = "JWT secret and token management"
	CLIShortSetup   = "interactive deployment bootstrap for server and client configs"
	CLIShortInspect = "inspect a credentials file with the registered format"
	CLIShortStatus  = "show the local daemon status"
	CLIShortVersion = "print build version"
	CLIShortRouter  = "run v2 router APIs"
)
