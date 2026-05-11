package extserver

import (
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInitialStateReadsTopicState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state.vscdb")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{0x0a, 0x03, 'a', 'b', 'c'}
	_, err = db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", "antigravityUnifiedStateSync.oauthToken", base64.StdEncoding.EncodeToString(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store := NewStateStore(dbPath)
	got, err := store.InitialState("uss-oauth")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch: got %x want %x", got, payload)
	}
}

func TestInitialStateAllowsMissingNonOAuthTopic(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state.vscdb")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store := NewStateStore(dbPath)
	got, err := store.InitialState("uss-browserPreferences")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty state, got %x", got)
	}
}

func TestInitialStateRequiresOAuthState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "missing.vscdb")
	if err := os.WriteFile(dbPath, nil, 0600); err != nil {
		t.Fatal(err)
	}

	store := NewStateStore(dbPath)
	if _, err := store.InitialState("uss-oauth"); err == nil {
		t.Fatal("expected missing oauth state to fail")
	}
}

func TestApplyRowUpdatePersistsInitialState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state.vscdb")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	row := appendBytesField(nil, 1, []byte("oauthTokenInfoSentinelKey"))
	row = appendBytesField(row, 2, []byte("refreshed"))
	store := NewStateStore(dbPath)
	if err := store.ApplyRowUpdate("uss-oauth", "oauthTokenInfoSentinelKey", row, false); err != nil {
		t.Fatal(err)
	}

	got, err := store.InitialState("uss-oauth")
	if err != nil {
		t.Fatal(err)
	}
	persistedRow, ok := readBytesField(got, 1)
	if !ok {
		t.Fatal("expected persisted row")
	}
	value, ok := readBytesField(persistedRow, 2)
	if !ok {
		t.Fatal("expected persisted row value")
	}
	if string(value) != "refreshed" {
		t.Fatalf("expected refreshed row, got %q", value)
	}
}
