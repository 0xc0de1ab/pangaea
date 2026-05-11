package extserver

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"testing"
)

func TestReadStringField(t *testing.T) {
	payload := appendBytesField(nil, 1, []byte("uss-oauth"))
	got, ok := readStringField(payload, 1)
	if !ok {
		t.Fatal("expected field to be present")
	}
	if got != "uss-oauth" {
		t.Fatalf("expected uss-oauth, got %q", got)
	}
}

func TestReadRequestPayloadConnectFrame(t *testing.T) {
	payload := appendBytesField(nil, 1, []byte("uss-oauth"))
	body := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(body[1:], uint32(len(payload)))
	body = append(body, payload...)
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", connectContentType)

	got, err := readRequestPayload(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %x want %x", got, payload)
	}
}

func TestEncodeUnifiedStateInitialUpdate(t *testing.T) {
	initial := []byte{0x0a, 0x03, 'a', 'b', 'c'}
	got := encodeUnifiedStateInitialUpdate(initial)
	want := appendBytesField(nil, 1, initial)
	if !bytes.Equal(got, want) {
		t.Fatalf("update mismatch: got %x want %x", got, want)
	}
}

func TestUpsertInitialStateRow(t *testing.T) {
	oldRow := appendBytesField(nil, 1, []byte("token"))
	oldRow = appendBytesField(oldRow, 2, []byte("old"))
	initial := appendBytesField(nil, 1, oldRow)
	newRow := appendBytesField(nil, 1, []byte("token"))
	newRow = appendBytesField(newRow, 2, []byte("new"))

	got := upsertInitialStateRow(initial, "token", newRow, false)
	row, ok := readBytesField(got, 1)
	if !ok {
		t.Fatal("expected row")
	}
	value, ok := readBytesField(row, 2)
	if !ok {
		t.Fatal("expected row value")
	}
	if string(value) != "new" {
		t.Fatalf("expected updated row, got %q", value)
	}
}

func TestUpsertInitialStateRowDelete(t *testing.T) {
	row := appendBytesField(nil, 1, []byte("token"))
	row = appendBytesField(row, 2, []byte("old"))
	initial := appendBytesField(nil, 1, row)

	got := upsertInitialStateRow(initial, "token", nil, true)
	if len(got) != 0 {
		t.Fatalf("expected row to be deleted, got %x", got)
	}
}
