package antigravity

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/pkg/formats/antigravitystate"
)

func TestAuthRefresherTriggersLSCoreAndReadsState(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if got := r.Header.Get("authorization"); got != "Bearer proxy-key" {
			t.Fatalf("authorization header = %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	authPath := filepath.Join(t.TempDir(), "state.vscdb")
	if err := os.WriteFile(authPath, testStateBytes(t, "antigravity-user@example.test", now.Add(time.Hour), "test-refreshed-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	refresher, err := NewAuthRefresher(RefreshOptions{
		BaseURL:  server.URL,
		APIKey:   "proxy-key",
		AuthPath: authPath,
		Format:   antigravitystate.Format{},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAuthRefresher: %v", err)
	}

	auth, err := refresher.RefreshAuth(context.Background(), control.AuthRefreshRequest{
		ProviderInstanceID: "antigravity-sidecar",
	}, provider.Registration{
		Identity: provider.ProviderIdentity{ProviderInstanceID: "antigravity-sidecar"},
		Auth:     provider.AuthState{Status: provider.AuthRefreshSoon, Account: provider.Account{Display: "antigravity-user@example.test"}},
	})
	if err != nil {
		t.Fatalf("RefreshAuth: %v", err)
	}
	if strings.Join(paths, ",") != "/v1/account,/v1/models/status" {
		t.Fatalf("probe paths = %#v", paths)
	}
	if auth.Status != provider.AuthHealthy || !auth.Refreshable || auth.Account.Display != "antigravity-user@example.test" {
		t.Fatalf("unexpected auth state: %#v", auth)
	}
	if !auth.LastRefreshAt.Equal(now) || !auth.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected refresh timestamps: %#v", auth)
	}
}

func TestAuthRefresherRejectsUnauthorizedProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	refresher, err := NewAuthRefresher(RefreshOptions{
		BaseURL: server.URL,
		APIKey:  "bad-key",
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAuthRefresher: %v", err)
	}
	auth, err := refresher.RefreshAuth(context.Background(), control.AuthRefreshRequest{}, provider.Registration{
		Auth: provider.AuthState{Status: provider.AuthRefreshSoon},
	})
	if err == nil {
		t.Fatal("expected unauthorized refresh error")
	}
	if strings.Contains(err.Error(), "bad-key") {
		t.Fatalf("refresh error leaked api key: %v", err)
	}
	if auth.Status != provider.AuthUnavailable || !auth.LastRefreshAt.Equal(now) {
		t.Fatalf("unexpected auth state after failure: %#v", auth)
	}
}

func testStateBytes(t *testing.T, email string, exp time.Time, token string) []byte {
	t.Helper()
	userOuter := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString([]byte(email))))
	tokenInfo := append([]byte(token+"|"), testTimestampMessage(exp.Unix())...)
	tokenOuter := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(tokenInfo)))
	return []byte("antigravityUnifiedStateSync.userStatus|" + userOuter + "|antigravityUnifiedStateSync.oauthToken|" + tokenOuter)
}

func testTimestampMessage(seconds int64) []byte {
	var out []byte
	out = append(out, 0x22, 0x01, 0x08)
	for seconds >= 0x80 {
		out = append(out, byte(seconds)|0x80)
		seconds >>= 7
	}
	out = append(out, byte(seconds))
	return out
}
