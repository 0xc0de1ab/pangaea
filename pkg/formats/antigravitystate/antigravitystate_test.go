package antigravitystate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func makeTimestampMessage(seconds int64) []byte {
	out := []byte{0x22, 0x08, 0x08}
	v := uint64(seconds)
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	out = append(out, byte(v))
	return out
}

func testAccessToken(seed string) string {
	return "ya29." + strings.Repeat(seed, 64)
}

func makeStateBytes(t *testing.T, email string, exp time.Time, token string) []byte {
	t.Helper()
	userJSON, err := json.Marshal(map[string]any{"email": email})
	if err != nil {
		t.Fatal(err)
	}
	userOuter := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(userJSON)))
	tokenInfo := append([]byte(token+"|"), makeTimestampMessage(exp.Unix())...)
	tokenOuter := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(tokenInfo)))
	return []byte("antigravityUnifiedStateSync.userStatus|" + userOuter + "|antigravityUnifiedStateSync.oauthToken|" + tokenOuter)
}

func TestParseHappyPath(t *testing.T) {
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	raw := makeStateBytes(t, "user@example.com", exp, testAccessToken("A"))
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !snap.ExpiresAt().Equal(exp) {
		t.Fatalf("ExpiresAt = %s, want %s", snap.ExpiresAt(), exp)
	}
	if len(snap.Fingerprint()) != 64 {
		t.Fatalf("fingerprint length = %d", len(snap.Fingerprint()))
	}
	account, err := (Format{}).Account(context.Background(), snap, "")
	if err != nil {
		t.Fatal(err)
	}
	if account != "user@example.com" {
		t.Fatalf("account = %q", account)
	}
}

func TestValidateTreatsStaleExpiryAsRouteable(t *testing.T) {
	raw := makeStateBytes(t, "user@example.com", time.Now().Add(time.Minute), testAccessToken("B"))
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
}

func TestParseRejectsUnrelatedBytes(t *testing.T) {
	_, err := (Format{}).Parse([]byte("not antigravity"))
	if !errors.Is(err, common.ErrParseFailed) {
		t.Fatalf("err = %v, want ErrParseFailed", err)
	}
}

func TestRedactDoesNotLeakToken(t *testing.T) {
	raw := makeStateBytes(t, "user@example.com", time.Now().Add(time.Hour), testAccessToken("C"))
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := (Format{}).Redact(snap)
	encoded, _ := json.Marshal(sum)
	if strings.Contains(string(encoded), testAccessToken("C")) {
		t.Fatalf("redacted summary leaks token: %s", encoded)
	}
	if sum.TokenTail4 == "" {
		t.Fatal("token tail should be present")
	}
}

func TestCredentialPathScansKnownLocations(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, ".config", "Antigravity", "User", "globalStorage")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, "state.vscdb")
	if err := os.WriteFile(statePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (Format{}).CredentialPath(home); got != statePath {
		t.Fatalf("CredentialPath = %q, want %q", got, statePath)
	}
	watched := (Format{}).WatchPaths(home)
	if len(watched) == 0 || watched[0] != statePath {
		t.Fatalf("WatchPaths = %#v, want first %q", watched, statePath)
	}
	if got := (Format{}).CredentialPathForEvent(home, statePath); got != statePath {
		t.Fatalf("CredentialPathForEvent = %q, want %q", got, statePath)
	}
}
