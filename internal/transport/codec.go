package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// Marshal builds an Envelope around payload and serializes it to JSON.
// payload may be nil (encoded as JSON null).
//
// id is left to the caller; pass NewID() for fresh values. ts likewise — the
// codec never injects time.Now to keep the surface deterministic for tests.
func Marshal(kind Kind, v int, id string, ts time.Time, payload any) ([]byte, error) {
	if !validKind(kind) {
		return nil, common.Wrap(nil, common.ErrInvalidMessage, common.MsgInvalidKind, string(kind))
	}
	if v != common.EnvelopeV {
		return nil, common.Wrap(nil, common.ErrInvalidMessage, common.MsgInvalidEnvelopeV, v)
	}
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, common.Wrap(err, common.ErrInvalidMessage, common.MsgPayloadParseFailed, err.Error())
		}
		raw = b
	}
	env := Envelope{
		Type:    kind,
		V:       v,
		ID:      id,
		TS:      ts,
		Payload: raw,
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, common.Wrap(err, common.ErrInvalidMessage, common.MsgPayloadParseFailed, err.Error())
	}
	return out, nil
}

// Unmarshal decodes a wire message into an Envelope. It validates the kind
// and protocol version; payload is left as RawMessage for the caller.
func Unmarshal(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, common.Wrap(err, common.ErrInvalidMessage, common.MsgPayloadParseFailed, err.Error())
	}
	if env.V != common.EnvelopeV {
		return Envelope{}, common.Wrap(nil, common.ErrInvalidMessage, common.MsgInvalidEnvelopeV, env.V)
	}
	if !validKind(env.Type) {
		return Envelope{}, common.Wrap(nil, common.ErrInvalidMessage, common.MsgInvalidKind, string(env.Type))
	}
	return env, nil
}

// NewID returns a UUIDv4-shaped string built from crypto/rand bytes (no
// external dependency). The format is the canonical 8-4-4-4-12 hex grouping
// with version=4 and RFC 4122 variant bits set.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure on a healthy host is unrecoverable; fall back
		// to a timestamp-derived id rather than panicking.
		ns := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			b[i] = byte(ns >> (8 * i))
		}
	}
	// version 4
	b[6] = (b[6] & 0x0f) | 0x40
	// variant 10
	b[8] = (b[8] & 0x3f) | 0x80
	hexs := hex.EncodeToString(b[:])
	return hexs[0:8] + "-" + hexs[8:12] + "-" + hexs[12:16] + "-" + hexs[16:20] + "-" + hexs[20:]
}
