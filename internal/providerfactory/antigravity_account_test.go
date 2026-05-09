package providerfactory

import (
	"encoding/base64"
	"os"
	"testing"
	"time"
)

func TestAntigravityAccountFromNestedUserStatusState(t *testing.T) {
	inner := base64.StdEncoding.EncodeToString([]byte("Primary User:\x13primary@example.test"))
	outer := base64.StdEncoding.EncodeToString([]byte("userStatusSentinelKey\n" + inner))
	raw := []byte("prefix antigravityUnifiedStateSync.userStatus|" + outer + " suffix")

	account, ok := antigravityAccountFromState(raw)
	if !ok {
		t.Fatalf("expected account extracted")
	}
	if account.ID != "primary@example.test" || account.Display != "primary@example.test" {
		t.Fatalf("unexpected account: %#v", account)
	}
}

func TestAntigravityAccountFromStateIgnoresEmailsBeforeUserStatus(t *testing.T) {
	inner := base64.StdEncoding.EncodeToString([]byte("Name:\x13real@example.test"))
	outer := base64.StdEncoding.EncodeToString([]byte("userStatusSentinelKey\n" + inner))
	raw := []byte("noise stale@example.test antigravityUnifiedStateSync.userStatus|" + outer)

	account, ok := antigravityAccountFromState(raw)
	if !ok {
		t.Fatalf("expected account extracted")
	}
	if account.Display != "real@example.test" {
		t.Fatalf("expected user status account, got %#v", account)
	}
}

func TestAntigravityAccountFromStateIgnoresRawEmailsInsideDBPage(t *testing.T) {
	inner := base64.StdEncoding.EncodeToString([]byte("Name:\x13real@example.test"))
	outer := base64.StdEncoding.EncodeToString([]byte("userStatusSentinelKey\n" + inner))
	raw := []byte("antigravityUnifiedStateSync.userStatus|" + outer + "|git@github.com")

	account, ok := antigravityAccountFromState(raw)
	if !ok {
		t.Fatalf("expected account extracted")
	}
	if account.Display != "real@example.test" {
		t.Fatalf("expected decoded user status account, got %#v", account)
	}
}

func TestAntigravityAccountFromStateScansMultipleUserStatusOccurrences(t *testing.T) {
	inner := base64.StdEncoding.EncodeToString([]byte("Name:\x13real@example.test"))
	outer := base64.StdEncoding.EncodeToString([]byte("userStatusSentinelKey\n" + inner))
	raw := []byte("antigravityUnifiedStateSync.userStatus|index-page-only|antigravityUnifiedStateSync.userStatus|" + outer)

	account, ok := antigravityAccountFromState(raw)
	if !ok {
		t.Fatalf("expected account extracted")
	}
	if account.Display != "real@example.test" {
		t.Fatalf("expected decoded user status account, got %#v", account)
	}
}

func TestAntigravityOAuthExpiryFromState(t *testing.T) {
	want := time.Date(2026, 4, 29, 0, 0, 23, 0, time.UTC)
	tokenInfo := append([]byte("ya29.X"), []byte{0x12, 0x06}...)
	tokenInfo = append(tokenInfo, []byte("Bearer")...)
	tokenInfo = append(tokenInfo, []byte{0x22, 0x06, 0x08}...)
	tokenInfo = append(tokenInfo, testAntigravityVarint(uint64(want.Unix()))...)
	inner := base64.StdEncoding.EncodeToString(tokenInfo)
	outer := base64.StdEncoding.EncodeToString([]byte("oauthTokenInfoSentinelKey\n" + inner))
	raw := []byte("antigravityUnifiedStateSync.oauthToken|" + outer)

	got, ok := antigravityOAuthExpiryFromState(raw)
	if !ok {
		t.Fatalf("expected expiry extracted")
	}
	if !got.Equal(want) {
		t.Fatalf("unexpected expiry: got %s want %s", got, want)
	}
}

func TestAntigravityOAuthExpiryFromLiveStateFile(t *testing.T) {
	path := os.Getenv("PANGAEA_TEST_ANTIGRAVITY_STATE")
	if path == "" {
		t.Skip("PANGAEA_TEST_ANTIGRAVITY_STATE is not set")
	}
	got, ok := antigravityOAuthExpiryFromStateFile(path)
	if !ok {
		t.Fatalf("expected expiry extracted from %s", path)
	}
	t.Logf("expiry: %s", got.Format(time.RFC3339))
}

func testAntigravityVarint(value uint64) []byte {
	out := []byte{}
	for value >= 0x80 {
		out = append(out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}
