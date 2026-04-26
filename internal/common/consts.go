// Package common holds cross-cutting constants, sentinel errors, and reusable
// string templates that the rest of the codebase depends on. No business logic
// lives here; this package should have zero internal dependencies.
package common

import "time"

// Networking / transport defaults.
const (
	DefaultPort          = 8443
	DefaultWSPath        = "/ws/profile/"
	DefaultReverseWSPath = "/reverse/profile/"
	DefaultAttachWSPath  = "/attach/profile/"
	// EnvelopeV is the current protocol version written into every message envelope.
	EnvelopeV = 1
)

// Channel / queue sizes.
const (
	ChannelBuf = 64
)

// Reconnect policy (clients).
const (
	ReconnectInitial = 5 * time.Second
	ReconnectMax     = 60 * time.Second
	ReconnectJitter  = 1 * time.Second
	// ReconnectTLSFailure is the fixed backoff applied when the peer certificate
	// fails to verify — a configuration-level error where tight retry loops
	// would only produce noise.
	ReconnectTLSFailure = 60 * time.Second
)

// Transport timing.
const (
	PingInterval = 30 * time.Second
	WriteTimeout = 10 * time.Second
	ReadTimeout  = PingInterval + 15*time.Second
)

// Watcher defaults.
const (
	WatcherDebounceCore = 50 * time.Millisecond
	WatcherStableWindow = 200 * time.Millisecond
	WatcherDefaultQueue = 64
)

// Live-check / validation defaults.
const (
	LiveCheckDefaultTimeout = 5 * time.Second
)

// File safety.
const (
	CredentialsFileMode = 0o600
	LockAcquireTimeout  = 500 * time.Millisecond
	LockRetryMax        = 3
)

// Shutdown.
const (
	ShutdownGrace = 30 * time.Second
)
