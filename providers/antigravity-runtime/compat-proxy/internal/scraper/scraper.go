package scraper

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/google/antigravity-compat-proxy/internal/interfaces"
	_ "modernc.org/sqlite"
)

// SQLiteScraper implements interfaces.AuthProvider by reading from state.vscdb.
type SQLiteScraper struct {
	dbPath string
}

// NewSQLiteScraper creates a new instance of SQLiteScraper.
func NewSQLiteScraper(dbPath string) *SQLiteScraper {
	return &SQLiteScraper{dbPath: dbPath}
}

var tokenRegex = regexp.MustCompile(`ya29\.[a-zA-Z0-9._-]+`)
var b64TokenRegex = regexp.MustCompile(`eWEyOS5[a-zA-Z0-9_/\-+=]+`)

// GetLatestToken reads the token from the SQLite database using a file-copy strategy to avoid locking.
func (s *SQLiteScraper) GetLatestToken() (string, error) {
	// 1. Create a temporary file to copy the database into.
	tmpFile, err := os.CreateTemp("", "state_vscdb_copy_*.db")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)
	defer tmpFile.Close()

	// 2. Open the source database file.
	src, err := os.Open(s.dbPath)
	if err != nil {
		return "", fmt.Errorf("failed to open source db at %s: %w", s.dbPath, err)
	}
	defer src.Close()

	// 3. Copy the source database to the temporary file.
	if _, err := io.Copy(tmpFile, src); err != nil {
		return "", fmt.Errorf("failed to copy db: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp file before reading: %w", err)
	}

	// 4. Open the copied database.
	db, err := sql.Open("sqlite", tmpName)
	if err != nil {
		return "", fmt.Errorf("failed to open sqlite db: %w", err)
	}
	defer db.Close()

	// 5. Try globalState table (Linux Headless format)
	var token string
	query := "SELECT value FROM globalState WHERE key = 'antigravity_auth/auth_token'"
	err = db.QueryRow(query).Scan(&token)
	if err == nil && token != "" {
		return token, nil
	}

	// 6. Try ItemTable (Windows/VS Code format)
	// Key: antigravityUnifiedStateSync.oauthToken
	var blob string
	query = "SELECT value FROM ItemTable WHERE key = 'antigravityUnifiedStateSync.oauthToken'"
	err = db.QueryRow(query).Scan(&blob)
	if err == nil && blob != "" {
		// The blob is often Base64 encoded in the DB
		decodedBytes, err := base64.StdEncoding.DecodeString(blob)
		if err == nil {
			// Search for ya29 token in decoded bytes
			if match := tokenRegex.Find(decodedBytes); match != nil {
				return string(match), nil
			}
			// Search for b64(ya29) token
			if match := b64TokenRegex.Find(decodedBytes); match != nil {
				finalToken, err := base64.StdEncoding.DecodeString(string(match))
				if err == nil {
					if tokenMatch := tokenRegex.Find(finalToken); tokenMatch != nil {
						return string(tokenMatch), nil
					}
				}
			}
		} else {
			// Try searching in the raw blob if it's not valid b64
			if match := tokenRegex.FindString(blob); match != "" {
				return match, nil
			}
		}
	}

	return "", fmt.Errorf("token not found in any supported table")
}

// WatchTokenChanges starts a polling ticker to detect changes in the auth token.
func (s *SQLiteScraper) WatchTokenChanges(ctx context.Context) (<-chan string, error) {
	ch := make(chan string)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		lastToken, _ := s.GetLatestToken()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				token, err := s.GetLatestToken()
				if err != nil {
					continue
				}
				if token != lastToken {
					lastToken = token
					select {
					case ch <- token:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return ch, nil
}

var _ interfaces.AuthProvider = (*SQLiteScraper)(nil)
