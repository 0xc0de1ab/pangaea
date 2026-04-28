// Package transport implements the WebSocket protocol envelope used between
// the mediating server and client nodes. It is intentionally narrow: it owns
// the wire format (Envelope/Kind, typed payload structs), the JSON codec, and
// a thin Conn wrapper around *websocket.Conn that serializes writes and
// surfaces incoming messages on a channel.
//
// This package deliberately does not depend on pkg/formats. The summary field
// on payloads is carried as json.RawMessage so format implementations may
// produce/consume it without forcing transport to know format-specific types.
// The live_check.result is a string for the same reason — formats decide the
// allowed status set.
package transport

import (
	"encoding/json"
	"time"
)

// Kind is the discriminator written into Envelope.Type. The full value set
// is enumerated below; any other value is rejected at decode time.
type Kind string

const (
	KindAuthJWT         Kind = "auth.jwt"
	KindHello           Kind = "hello"
	KindWelcome         Kind = "welcome"
	KindSnapshotRequest Kind = "snapshot.request"
	KindSnapshotReport  Kind = "snapshot.report"
	KindSnapshotAbsent  Kind = "snapshot.absent"
	KindTruthPush       Kind = "truth.push"
	KindTruthAck        Kind = "truth.ack"
	KindError           Kind = "error"
)

// validKinds returns true for any Kind defined above.
func validKind(k Kind) bool {
	switch k {
	case KindAuthJWT, KindHello, KindWelcome, KindSnapshotRequest, KindSnapshotReport, KindSnapshotAbsent,
		KindTruthPush, KindTruthAck, KindError:
		return true
	}
	return false
}

// Envelope wraps every protocol message. The payload is left as a raw JSON
// blob so the typed payload struct can be unmarshalled lazily by the
// receiver based on Type.
type Envelope struct {
	Type    Kind            `json:"type"`
	V       int             `json:"v"`
	ID      string          `json:"id"`
	TS      time.Time       `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// SummaryCarrier is the wire shape for the format Summary field. transport
// stays format-agnostic — pkg/formats is responsible for producing/consuming
// JSON that fits this opaque slot.
type SummaryCarrier = json.RawMessage

// LiveCheckMeta mirrors specs §7.3 live_check sub-object.
type LiveCheckMeta struct {
	Performed bool      `json:"performed"`
	Result    string    `json:"result"`
	CheckedAt time.Time `json:"checked_at"`
}

// TruthMeta is the redacted descriptor of a truth (no raw bytes), used in
// welcome.known_truth.
type TruthMeta struct {
	Fingerprint string         `json:"fingerprint"`
	IssuedAt    time.Time      `json:"issued_at"`
	Summary     SummaryCarrier `json:"summary,omitempty"`
}

// Hello — C→S node introduction.
type Hello struct {
	NodeID       string   `json:"node_id"`
	AgentVersion string   `json:"agent_version"`
	OS           string   `json:"os"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// SnapshotRequest — S→C request for an immediate current-state report.
// It is used by operator commands to refresh the server's mirror before
// rendering an on-demand status response.
type SnapshotRequest struct {
	Profile string `json:"profile"`
	Reason  string `json:"reason,omitempty"`
}

// AuthJWT — C→S JWT bearer used only when the server asks for post-upgrade
// authentication before Hello.
type AuthJWT struct {
	Token string `json:"token"`
}

// Welcome — S→C acknowledgement after hello.
type Welcome struct {
	ServerVersion string     `json:"server_version"`
	KnownTruth    *TruthMeta `json:"known_truth,omitempty"`
}

// SnapshotReport — C→S report for a single (profile, path) tuple.
//
// MVP carries raw_b64 inline (specs §10.1 simplification). Servers must
// zero out raw bytes for non-truth candidates immediately after compare.
//
// Account is a stable per-LLM-account identifier the client extracts at
// report time (e.g. an OAuth user UUID or email). Empty means the format
// could not derive an account; the server falls back to a single shared
// bucket. Two reports with different non-empty Account values are NEVER
// reconciled into the same truth — they belong to logically different
// users sharing a profile name.
type SnapshotReport struct {
	Profile     string         `json:"profile"`
	Account     string         `json:"account,omitempty"`
	Path        string         `json:"path"`
	Format      string         `json:"format"`
	Fingerprint string         `json:"fingerprint"`
	Summary     SummaryCarrier `json:"summary,omitempty"`
	LiveCheck   LiveCheckMeta  `json:"live_check"`
	RawSize     int            `json:"raw_size"`
	RawB64      string         `json:"raw_b64,omitempty"`
}

// SnapshotAbsent — C→S report that no candidate credentials file exists for a
// profile.
// Account, if known, helps the server clear that account's candidate state
// for this node; an empty value means the client could not determine an
// account (e.g. file did not exist, format could not extract identity).
type SnapshotAbsent struct {
	Profile string `json:"profile"`
	Account string `json:"account,omitempty"`
	Path    string `json:"path"`
}

// TruthPush — S→C delivery of an authoritative file. Account names which
// per-account bucket the truth belongs to; clients verify that their local
// snapshot reports the same account before applying, so a stale push for a
// foreign account can never be silently written.
type TruthPush struct {
	Profile     string         `json:"profile"`
	Account     string         `json:"account,omitempty"`
	Format      string         `json:"format"`
	Fingerprint string         `json:"fingerprint"`
	RawB64      string         `json:"raw_b64"`
	TargetPath  string         `json:"target_path"`
	IssuedAt    time.Time      `json:"issued_at"`
	Summary     SummaryCarrier `json:"summary,omitempty"`
}

// TruthAck — C→S apply outcome for a TruthPush.
type TruthAck struct {
	Profile     string `json:"profile"`
	Account     string `json:"account,omitempty"`
	Fingerprint string `json:"fingerprint"`
	OK          bool   `json:"ok"`
	Reason      string `json:"reason,omitempty"`
}

// ErrorPayload — protocol-level error, sent before graceful close.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
