package scraper

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestGetLatestToken(t *testing.T) {
	// Create a temporary directory for the test database.
	tmpDir, err := os.MkdirTemp("", "scraper_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.vscdb")

	// Create and initialize the test database.
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_, err = db.Exec("CREATE TABLE globalState (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create table: %v", err)
	}

	expectedToken := "test-auth-token"
	_, err = db.Exec("INSERT INTO globalState (key, value) VALUES ('antigravity_auth/auth_token', ?)", expectedToken)
	if err != nil {
		db.Close()
		t.Fatalf("failed to insert token: %v", err)
	}
	db.Close()

	// Initialize the scraper.
	s := NewSQLiteScraper(dbPath)

	// Test GetLatestToken.
	token, err := s.GetLatestToken()
	if err != nil {
		t.Fatalf("GetLatestToken failed: %v", err)
	}

	if token != expectedToken {
		t.Errorf("expected token %s, got %s", expectedToken, token)
	}
}

func TestGetLatestToken_NoRows(t *testing.T) {
	// Create a temporary directory for the test database.
	tmpDir, err := os.MkdirTemp("", "scraper_test_empty_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "state.vscdb")

	// Create and initialize an empty test database.
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_, err = db.Exec("CREATE TABLE globalState (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create table: %v", err)
	}
	db.Close()

	// Initialize the scraper.
	s := NewSQLiteScraper(dbPath)

	// Test GetLatestToken when no token is present.
	token, err := s.GetLatestToken()
	if err == nil {
		t.Fatalf("expected GetLatestToken to fail when no token is present")
	}

	if token != "" {
		t.Errorf("expected empty token, got %s", token)
	}
}
