// Package security contains reusable authentication and redaction primitives
// for v2 router-facing APIs.
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

var ErrInvalidAPIKey = errors.New("invalid api key")

type APIKeyPrincipal struct {
	ID       string `json:"id"`
	Prefix   string `json:"prefix"`
	TenantID string `json:"tenant_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
}

type APIKeyStore struct {
	mu     sync.RWMutex
	pepper []byte
	keys   map[string]apiKeyRecord
}

type apiKeyRecord struct {
	principal APIKeyPrincipal
	digest    [sha256.Size]byte
}

func NewAPIKeyStore(pepper []byte) *APIKeyStore {
	if len(pepper) == 0 {
		pepper = []byte("pangaea-dev-api-key-pepper")
	}
	return &APIKeyStore{
		pepper: append([]byte(nil), pepper...),
		keys:   make(map[string]apiKeyRecord),
	}
}

func (s *APIKeyStore) AddRawKey(id, raw, tenantID, userID string) (APIKeyPrincipal, error) {
	raw = strings.TrimSpace(raw)
	if s == nil || strings.TrimSpace(id) == "" || raw == "" {
		return APIKeyPrincipal{}, ErrInvalidAPIKey
	}
	principal := APIKeyPrincipal{
		ID:       id,
		Prefix:   keyPrefix(raw),
		TenantID: tenantID,
		UserID:   userID,
	}
	record := apiKeyRecord{
		principal: principal,
		digest:    s.digest(raw),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[id] = record
	return principal, nil
}

func (s *APIKeyStore) Authenticate(raw string) (APIKeyPrincipal, bool) {
	raw = strings.TrimSpace(raw)
	if s == nil || raw == "" {
		return APIKeyPrincipal{}, false
	}
	digest := s.digest(raw)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.keys {
		if hmac.Equal(digest[:], record.digest[:]) {
			return record.principal, true
		}
	}
	return APIKeyPrincipal{}, false
}

func (s *APIKeyStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys)
}

func (s *APIKeyStore) DebugDigests() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.keys))
	for _, record := range s.keys {
		out = append(out, hex.EncodeToString(record.digest[:]))
	}
	return out
}

func (s *APIKeyStore) digest(raw string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, s.pepper)
	_, _ = mac.Write([]byte(raw))
	sum := mac.Sum(nil)
	var out [sha256.Size]byte
	copy(out[:], sum)
	return out
}

func keyPrefix(raw string) string {
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}
