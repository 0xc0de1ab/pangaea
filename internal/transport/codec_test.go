package transport

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if len(id) != 36 {
		t.Fatalf("expected 36 chars got %d (%q)", len(id), id)
	}
	parts := strings.Split(id, "-")
	wantLens := []int{8, 4, 4, 4, 12}
	if len(parts) != 5 {
		t.Fatalf("expected 5 dash-separated groups got %d (%q)", len(parts), id)
	}
	for i, p := range parts {
		if len(p) != wantLens[i] {
			t.Fatalf("group %d expected len %d got %d (%q)", i, wantLens[i], len(p), p)
		}
	}
	// Two consecutive ids must not collide.
	if NewID() == id {
		t.Fatalf("NewID returned identical values back-to-back")
	}
}

func TestMarshalUnmarshal_Roundtrip(t *testing.T) {
	ts := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	hello := Hello{
		NodeID:       "host-a",
		AgentVersion: "0.1.0",
		OS:           "linux",
		Capabilities: []string{"truth.push"},
	}
	id := NewID()
	data, err := Marshal(KindHello, common.EnvelopeV, id, ts, hello)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	env, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Type != KindHello {
		t.Fatalf("type got %q want %q", env.Type, KindHello)
	}
	if env.V != common.EnvelopeV {
		t.Fatalf("v got %d want %d", env.V, common.EnvelopeV)
	}
	if env.ID != id {
		t.Fatalf("id mismatch")
	}
	if !env.TS.Equal(ts) {
		t.Fatalf("ts mismatch: got %v want %v", env.TS, ts)
	}
	var got Hello
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got.NodeID != hello.NodeID || got.AgentVersion != hello.AgentVersion ||
		got.OS != hello.OS || len(got.Capabilities) != len(hello.Capabilities) {
		t.Fatalf("payload mismatch: %+v vs %+v", got, hello)
	}
}

func TestUnmarshal_RejectsUnknownKind(t *testing.T) {
	raw := []byte(`{"type":"made.up","v":1,"id":"x","ts":"2026-04-25T12:00:00Z","payload":{}}`)
	_, err := Unmarshal(raw)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, common.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage got %v", err)
	}
}

func TestUnmarshal_RejectsBadVersion(t *testing.T) {
	raw := []byte(`{"type":"hello","v":99,"id":"x","ts":"2026-04-25T12:00:00Z","payload":{}}`)
	_, err := Unmarshal(raw)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, common.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage got %v", err)
	}
}

func TestUnmarshal_RejectsMalformedJSON(t *testing.T) {
	_, err := Unmarshal([]byte(`{not json`))
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, common.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage got %v", err)
	}
}

func TestMarshal_RejectsBadKind(t *testing.T) {
	_, err := Marshal(Kind("wat"), common.EnvelopeV, "id", time.Now(), nil)
	if err == nil || !errors.Is(err, common.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage got %v", err)
	}
}

func TestMarshal_RejectsBadVersion(t *testing.T) {
	_, err := Marshal(KindHello, 9, "id", time.Now(), nil)
	if err == nil || !errors.Is(err, common.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage got %v", err)
	}
}
