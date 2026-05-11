package extserver

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

const stateKeyPrefix = "antigravityUnifiedStateSync."

type StateStore struct {
	dbPath string
}

func NewStateStore(dbPath string) *StateStore {
	return &StateStore{dbPath: dbPath}
}

func (s *StateStore) InitialState(topic string) ([]byte, error) {
	key := stateKeyForTopic(topic)
	if key == "" {
		return nil, fmt.Errorf("unsupported unified state sync topic %q", topic)
	}
	value, err := s.readItemValue(key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) && topic != "uss-oauth" {
			return nil, nil
		}
		return nil, err
	}
	if value == "" {
		return nil, nil
	}
	decoded, err := decodeStateValue(value)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", key, err)
	}
	return decoded, nil
}

func (s *StateStore) ApplyRowUpdate(topic string, rowKey string, newRow []byte, deleted bool) error {
	stateKey := stateKeyForTopic(topic)
	if stateKey == "" {
		return fmt.Errorf("unsupported unified state sync topic %q", topic)
	}
	if strings.TrimSpace(rowKey) == "" {
		return fmt.Errorf("missing row key for topic %q", topic)
	}
	initialState, err := s.InitialState(topic)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	updated := upsertInitialStateRow(initialState, rowKey, newRow, deleted)
	encoded := base64.StdEncoding.EncodeToString(updated)

	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite db for update: %w", err)
	}
	defer db.Close()

	_, err = db.Exec("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)", stateKey, encoded)
	if err != nil {
		return fmt.Errorf("failed to persist unified state sync update for %s: %w", stateKey, err)
	}
	return nil
}

func stateKeyForTopic(topic string) string {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return ""
	}
	switch topic {
	case "uss-oauth":
		return stateKeyPrefix + "oauthToken"
	case "uss-userStatus":
		return stateKeyPrefix + "userStatus"
	}
	if strings.HasPrefix(topic, "uss-") {
		name := strings.TrimPrefix(topic, "uss-")
		if name != "" && !strings.ContainsAny(name, "/\\") {
			return stateKeyPrefix + name
		}
	}
	return ""
}

func decodeStateValue(value string) ([]byte, error) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return nil, nil
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		out, err := enc.DecodeString(cleaned)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *StateStore) readItemValue(key string) (string, error) {
	tmpName, cleanup, err := copyDatabase(s.dbPath)
	if err != nil {
		return "", err
	}
	defer cleanup()

	db, err := sql.Open("sqlite", tmpName)
	if err != nil {
		return "", fmt.Errorf("failed to open sqlite db: %w", err)
	}
	defer db.Close()

	var value string
	if err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

func copyDatabase(path string) (string, func(), error) {
	tmpFile, err := os.CreateTemp("", "antigravity_state_sync_*.vscdb")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp db: %w", err)
	}
	tmpName := tmpFile.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}
	defer func() {
		_ = tmpFile.Close()
	}()

	src, err := os.Open(path)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to open state db at %s: %w", path, err)
	}
	defer src.Close()

	if _, err := io.Copy(tmpFile, src); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to copy state db: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to close temp db: %w", err)
	}
	return tmpName, cleanup, nil
}
