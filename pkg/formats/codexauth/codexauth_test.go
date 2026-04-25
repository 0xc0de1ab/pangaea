package codexauth

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

// makeJWT builds a minimal unsigned JWT with the given claims. Codex never
// verifies the signature locally — it only base64url-decodes the payload and
// reads claims — so an empty signature segment is sufficient for these tests.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + "."
}

// authJSON returns a freshly serialised auth.json body. expSec is the JWT exp
// (unix seconds, 0 to omit). lastRefresh is RFC3339; pass empty for absent.
func authJSON(t *testing.T, expSec int64, lastRefresh string, accessTokenTail string) []byte {
	t.Helper()
	claims := map[string]any{
		"sub": "user-abc",
		"https://api.openai.com/profile": map[string]any{
			"email": "alice@example.com",
		},
	}
	if expSec != 0 {
		claims["exp"] = expSec
	}
	access := makeJWT(t, claims) + accessTokenTail
	idClaims := map[string]any{
		"https://api.openai.com/profile": map[string]any{
			"email": "alice@example.com",
		},
	}
	id := makeJWT(t, idClaims)
	body := map[string]any{
		"auth_mode":      "Chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      id,
			"access_token":  access,
			"refresh_token": "rt-" + accessTokenTail,
			"account_id":    "acct-1",
		},
	}
	if lastRefresh != "" {
		body["last_refresh"] = lastRefresh
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal authJSON: %v", err)
	}
	return out
}

func TestParse_HappyPath(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	raw := authJSON(t, exp, time.Now().UTC().Format(time.RFC3339), "A")
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if snap.ExpiresAt().Unix() != exp {
		t.Fatalf("ExpiresAt = %v, want %v", snap.ExpiresAt().Unix(), exp)
	}
	if snap.Identity() == "" {
		t.Fatal("Identity must be non-empty")
	}
	if len(snap.Fingerprint()) != 64 {
		t.Fatalf("Fingerprint length = %d, want 64", len(snap.Fingerprint()))
	}
}

func TestParse_MissingTokensRejected(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"auth_mode": "Chatgpt"})
	_, err := (Format{}).Parse(raw)
	if !errors.Is(err, common.ErrParseFailed) {
		t.Fatalf("err = %v, want ErrParseFailed", err)
	}
}

func TestParse_EmptyAccessTokenRejected(t *testing.T) {
	body := map[string]any{
		"tokens": map[string]any{
			"refresh_token": "rt",
		},
	}
	raw, _ := json.Marshal(body)
	_, err := (Format{}).Parse(raw)
	if !errors.Is(err, common.ErrParseFailed) {
		t.Fatalf("err = %v, want ErrParseFailed", err)
	}
}

func TestParse_BOMTolerated(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	raw := append([]byte{0xEF, 0xBB, 0xBF}, authJSON(t, exp, "", "B")...)
	if _, err := (Format{}).Parse(raw); err != nil {
		t.Fatalf("Parse with BOM: %v", err)
	}
}

func TestValidate_OK(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	raw := authJSON(t, exp, time.Now().UTC().Format(time.RFC3339), "C")
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != formats.StatusOK {
		t.Fatalf("Status = %q, want ok", res.Status)
	}
}

func TestValidate_ExpiredByJWT(t *testing.T) {
	exp := time.Now().Add(-time.Hour).Unix()
	raw := authJSON(t, exp, "", "D")
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusExpired {
		t.Fatalf("Status = %q, want expired", res.Status)
	}
}

func TestValidate_ExpiredByLastRefreshAge(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	old := time.Now().Add(-9 * 24 * time.Hour).UTC().Format(time.RFC3339)
	raw := authJSON(t, exp, old, "E")
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusExpired {
		t.Fatalf("Status = %q, want expired (last_refresh > 8d)", res.Status)
	}
}

func TestValidate_UnparseableJWTReturnsScopeWarn(t *testing.T) {
	body := map[string]any{
		"tokens": map[string]any{
			"access_token":  "not-a-jwt",
			"refresh_token": "rt",
		},
	}
	raw, _ := json.Marshal(body)
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := (Format{}).Validate(context.Background(), snap, formats.ValidateOpts{})
	if res.Status != formats.StatusScopeWarn {
		t.Fatalf("Status = %q, want scope_warn", res.Status)
	}
}

func TestCompare_LaterJWTExpWins(t *testing.T) {
	older := time.Now().Add(time.Hour).Unix()
	newer := time.Now().Add(2 * time.Hour).Unix()
	a, _ := (Format{}).Parse(authJSON(t, older, "", "A"))
	b, _ := (Format{}).Parse(authJSON(t, newer, "", "B"))

	if got := (Format{}).Compare(StrategyJWTExpMax, b, a); got != 1 {
		t.Fatalf("Compare(newer, older) = %d, want 1", got)
	}
	if got := (Format{}).Compare(StrategyJWTExpMax, a, b); got != -1 {
		t.Fatalf("Compare(older, newer) = %d, want -1", got)
	}
}

func TestCompare_TiebreakOnLastRefresh(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	now := time.Now().UTC()
	a, _ := (Format{}).Parse(authJSON(t, exp, now.Add(-2*time.Hour).Format(time.RFC3339), "A"))
	b, _ := (Format{}).Parse(authJSON(t, exp, now.Format(time.RFC3339), "B"))

	if got := (Format{}).Compare(StrategyJWTExpMax, b, a); got != 1 {
		t.Fatalf("Compare(b later last_refresh, a earlier) = %d, want 1", got)
	}
}

func TestCompare_UnknownStrategyPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for unknown strategy")
		}
	}()
	exp := time.Now().Add(time.Hour).Unix()
	a, _ := (Format{}).Parse(authJSON(t, exp, "", "A"))
	_ = (Format{}).Compare("not-a-strategy", a, a)
}

func TestRedact_NoSecrets(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	raw := authJSON(t, exp, time.Now().UTC().Format(time.RFC3339), "S")
	snap, err := (Format{}).Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := (Format{}).Redact(snap)
	encoded, _ := json.Marshal(sum)
	if strings.Contains(string(encoded), "rt-") {
		t.Fatalf("redact summary leaks refresh_token: %s", encoded)
	}
	cs := snap.(*snapshot)
	if strings.Contains(string(encoded), cs.accessToken) {
		t.Fatalf("redact summary leaks full access_token: %s", encoded)
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
	if s := got.Strategies(); len(s) != 1 || s[0] != StrategyJWTExpMax {
		t.Fatalf("Strategies = %v", s)
	}
}
