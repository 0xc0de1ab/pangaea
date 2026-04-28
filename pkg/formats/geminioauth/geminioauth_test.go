package geminioauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

func makeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + "."
}

// authJSON returns a freshly serialised gemini oauth_creds.json body. expMs
// is the epoch-ms expiry (0 to omit the field).
func authJSON(t *testing.T, expMs int64, accessTail string) []byte {
	t.Helper()
	body := map[string]any{
		"access_token":  "ya29." + accessTail,
		"refresh_token": "1//rt-" + accessTail,
		"scope":         "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email",
		"token_type":    "Bearer",
		"id_token":      "id-" + accessTail,
	}
	if expMs > 0 {
		body["expiry_date"] = expMs
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal authJSON: %v", err)
	}
	return out
}

func TestParse_HappyPath(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).UnixMilli()
	raw := authJSON(t, exp, "A")
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if snap.ExpiresAt().UnixMilli() != exp {
		t.Fatalf("ExpiresAt = %v, want %v", snap.ExpiresAt().UnixMilli(), exp)
	}
	if snap.Identity() == "" {
		t.Fatal("Identity must be non-empty")
	}
	if len(snap.Fingerprint()) != 64 {
		t.Fatalf("Fingerprint length = %d, want 64", len(snap.Fingerprint()))
	}
}

func TestAccountDisplay_UsesEmail(t *testing.T) {
	exp := time.Now().Add(time.Hour).UnixMilli()
	body := map[string]any{
		"access_token":  "ya29.display",
		"refresh_token": "1//rt-display",
		"id_token": makeIDToken(t, map[string]any{
			"sub":   "google-sub-1",
			"email": "gemini@example.com",
		}),
		"expiry_date": exp,
	}
	raw, _ := json.Marshal(body)
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Format{}).AccountDisplay(context.Background(), snap, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "gemini@example.com" {
		t.Fatalf("AccountDisplay = %q", got)
	}
	sum := (Format{}).Redact(snap)
	if sum.Extra["email"] != "gemini@example.com" {
		t.Fatalf("summary email = %q", sum.Extra["email"])
	}
}

func TestParse_MissingAccessTokenRejected(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"refresh_token": "rt"})
	_, err := (Format{}).Parse(raw)
	if !errors.Is(err, common.ErrParseFailed) {
		t.Fatalf("err = %v, want ErrParseFailed", err)
	}
}

func TestParse_MissingRefreshTokenRejected(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"access_token": "ya29.x"})
	_, err := (Format{}).Parse(raw)
	if !errors.Is(err, common.ErrParseFailed) {
		t.Fatalf("err = %v, want ErrParseFailed", err)
	}
}

func TestParse_MissingExpiryAllowed(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"access_token":  "ya29.x",
		"refresh_token": "rt",
	})
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !snap.ExpiresAt().IsZero() {
		t.Fatalf("ExpiresAt should be zero when expiry_date absent, got %v", snap.ExpiresAt())
	}
}

func TestParse_BOMTolerated(t *testing.T) {
	exp := time.Now().Add(time.Hour).UnixMilli()
	raw := append([]byte{0xEF, 0xBB, 0xBF}, authJSON(t, exp, "B")...)
	if _, err := (Format{}).Parse(raw); err != nil {
		t.Fatalf("Parse with BOM: %v", err)
	}
}

func TestValidate_OK(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).UnixMilli()
	snap, err := (Format{}).Parse(authJSON(t, exp, "C"))
	if err != nil {
		t.Fatal(err)
	}
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusOK {
		t.Fatalf("Status = %q, want ok", res.Status)
	}
}

func TestValidate_ExpiredHard(t *testing.T) {
	exp := time.Now().Add(-time.Hour).UnixMilli()
	snap, _ := (Format{}).Parse(authJSON(t, exp, "D"))
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusExpired {
		t.Fatalf("Status = %q, want expired", res.Status)
	}
}

func TestValidate_ExpiredByEagerRefreshSkew(t *testing.T) {
	// Within the 5-minute eager-refresh window — google-auth-library would
	// already be refreshing, so we treat it as expired for sharing purposes.
	exp := time.Now().Add(2 * time.Minute).UnixMilli()
	snap, _ := (Format{}).Parse(authJSON(t, exp, "E"))
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusExpired {
		t.Fatalf("Status = %q, want expired (skew window)", res.Status)
	}
}

func TestValidate_MissingExpiryDateExpired(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"access_token":  "ya29.x",
		"refresh_token": "rt",
	})
	snap, _ := (Format{}).Parse(raw)
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusExpired {
		t.Fatalf("Status = %q, want expired (no expiry_date)", res.Status)
	}
}

func TestCompare_LaterExpiryWins(t *testing.T) {
	older := time.Now().Add(time.Hour).UnixMilli()
	newer := time.Now().Add(2 * time.Hour).UnixMilli()
	a, _ := (Format{}).Parse(authJSON(t, older, "A"))
	b, _ := (Format{}).Parse(authJSON(t, newer, "B"))
	if got := (Format{}).Compare(StrategyExpiryDateMax, b, a); got != 1 {
		t.Fatalf("Compare(b newer, a older) = %d, want 1", got)
	}
	if got := (Format{}).Compare(StrategyExpiryDateMax, a, b); got != -1 {
		t.Fatalf("Compare(a older, b newer) = %d, want -1", got)
	}
}

func TestCompare_UnknownStrategyPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for unknown strategy")
		}
	}()
	exp := time.Now().Add(time.Hour).UnixMilli()
	a, _ := (Format{}).Parse(authJSON(t, exp, "A"))
	_ = (Format{}).Compare("not-a-strategy", a, a)
}

func TestRedact_NoSecrets(t *testing.T) {
	exp := time.Now().Add(time.Hour).UnixMilli()
	snap, _ := (Format{}).Parse(authJSON(t, exp, "S"))
	sum := (Format{}).Redact(snap)
	encoded, _ := json.Marshal(sum)
	if strings.Contains(string(encoded), "1//rt-") {
		t.Fatalf("redact summary leaks refresh_token: %s", encoded)
	}
	cs := snap.(*snapshot)
	if strings.Contains(string(encoded), cs.accessToken) {
		t.Fatalf("redact summary leaks full access_token: %s", encoded)
	}
	if strings.Contains(string(encoded), cs.idToken) {
		t.Fatalf("redact summary leaks id_token: %s", encoded)
	}
	if sum.TokenTail4 == "" {
		t.Fatal("token_tail4 should be set for correlation")
	}
}

func TestRegistry(t *testing.T) {
	got, ok := formats.Get(FormatName)
	if !ok {
		t.Fatalf("format %q not registered", FormatName)
	}
	if got.Name() != FormatName {
		t.Fatalf("Name() = %q", got.Name())
	}
	if s := got.Strategies(); len(s) != 1 || s[0] != StrategyExpiryDateMax {
		t.Fatalf("Strategies = %v", s)
	}
}
