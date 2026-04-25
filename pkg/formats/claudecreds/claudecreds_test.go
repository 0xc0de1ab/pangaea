package claudecreds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
	"github.com/dh-kam/claude-creds-share/pkg/formats"
)

// goldenJSON is a representative valid credentials file (specs §9.1 schema).
// Token strings carry the canonical sk-ant-* prefixes so the test exercises
// the same shape downstream redactors will see in production.
const goldenJSON = `{
  "claudeAiOauth": {
    "accessToken": "test-claude-WXYZ",
    "refreshToken": "test-refresh-ABCD",
    "expiresAt": 1893456000000,
    "scopes": ["user:profile", "user:inference"],
    "subscriptionType": "max",
    "rateLimitTier": "default_claude_max_20x"
  }
}`

func mustParse(t *testing.T, raw string) formats.Snapshot {
	t.Helper()
	snap, err := Format{}.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse(golden) error: %v", err)
	}
	return snap
}

func TestParse_GoldenAcceptedFields(t *testing.T) {
	snap := mustParse(t, goldenJSON)

	if got, want := snap.ExpiresAt().UnixMilli(), int64(1893456000000); got != want {
		t.Errorf("ExpiresAt unix ms = %d, want %d", got, want)
	}
	if snap.Identity() == "" {
		t.Errorf("Identity must not be empty")
	}
	if got, want := len(snap.Identity()), 16; got != want {
		t.Errorf("Identity length = %d, want %d", got, want)
	}

	// Fingerprint = sha256 of the original raw bytes.
	want := sha256.Sum256([]byte(goldenJSON))
	if got, w := snap.Fingerprint(), hex.EncodeToString(want[:]); got != w {
		t.Errorf("Fingerprint = %q, want %q", got, w)
	}

	// Identity = first 16 hex chars of sha256(accessToken).
	tokSum := sha256.Sum256([]byte("test-claude-WXYZ"))
	if got, w := snap.Identity(), hex.EncodeToString(tokSum[:])[:16]; got != w {
		t.Errorf("Identity = %q, want %q", got, w)
	}
}

func TestParse_RawIsDefensiveCopy(t *testing.T) {
	snap := mustParse(t, goldenJSON)
	a := snap.Raw()
	b := snap.Raw()
	if &a[0] == &b[0] {
		t.Errorf("Raw() returned aliased slices; expected defensive copy each call")
	}
	a[0] = 'X'
	c := snap.Raw()
	if c[0] == 'X' {
		t.Errorf("Mutating the returned Raw() slice leaked into the snapshot")
	}
}

func TestParse_LegacyAccessTokenKey(t *testing.T) {
	const raw = `{
  "claudeAiOauth": {
    "access_token": "sk-ant-legacy-AAAA",
    "refreshToken": "sk-ant-ort01-BBBB",
    "expiresAt": 1893456000000
  }
}`
	snap, err := Format{}.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse legacy access_token: %v", err)
	}
	tail := Format{}.Redact(snap).TokenTail4
	if tail != "AAAA" {
		t.Errorf("TokenTail4 = %q, want %q (legacy key not honored)", tail, "AAAA")
	}
}

func TestParse_BOMAccepted(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(goldenJSON)...)
	if _, err := (Format{}).Parse(raw); err != nil {
		t.Fatalf("Parse with BOM: %v", err)
	}
}

func TestParse_MalformedJSON(t *testing.T) {
	_, err := Format{}.Parse([]byte("{not-json"))
	if err == nil {
		t.Fatalf("expected error for malformed JSON")
	}
	if !errors.Is(err, common.ErrParseFailed) {
		t.Errorf("err = %v, want errors.Is ErrParseFailed", err)
	}
}

func TestParse_MissingAccessToken(t *testing.T) {
	const raw = `{"claudeAiOauth":{"refreshToken":"r","expiresAt":1}}`
	_, err := Format{}.Parse([]byte(raw))
	if !errors.Is(err, common.ErrParseFailed) {
		t.Errorf("err = %v, want errors.Is ErrParseFailed", err)
	}
}

func TestParse_MissingRefreshToken(t *testing.T) {
	const raw = `{"claudeAiOauth":{"accessToken":"a","expiresAt":1}}`
	_, err := Format{}.Parse([]byte(raw))
	if !errors.Is(err, common.ErrParseFailed) {
		t.Errorf("err = %v, want errors.Is ErrParseFailed", err)
	}
}

func TestParse_MissingExpiresAt(t *testing.T) {
	const raw = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`
	_, err := Format{}.Parse([]byte(raw))
	if !errors.Is(err, common.ErrParseFailed) {
		t.Errorf("err = %v, want errors.Is ErrParseFailed", err)
	}
}

func TestParse_EmptyInput(t *testing.T) {
	_, err := Format{}.Parse(nil)
	if !errors.Is(err, common.ErrParseFailed) {
		t.Errorf("err = %v, want errors.Is ErrParseFailed", err)
	}
}

func TestValidate_NotExpired(t *testing.T) {
	snap := mustParse(t, goldenJSON)
	clock := func() time.Time { return time.UnixMilli(1893456000000 - 60_000) }
	res, err := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		LiveCheck: false,
		Clock:     clock,
	})
	if err != nil {
		t.Fatalf("Validate err: %v", err)
	}
	if res.Status != formats.StatusOK {
		t.Errorf("Status = %q, want ok", res.Status)
	}
}

func TestValidate_Expired(t *testing.T) {
	const raw = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1000}}`
	snap, err := Format{}.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	res, err := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		Clock: func() time.Time { return time.Unix(9999999999, 0) },
	})
	if err != nil {
		t.Fatalf("Validate err: %v", err)
	}
	if res.Status != formats.StatusExpired {
		t.Errorf("Status = %q, want expired", res.Status)
	}
}

// withLiveCheckURL temporarily redirects the live-check endpoint to a test
// server. Saved-and-restored to keep tests independent.
func withLiveCheckURL(t *testing.T, url string) {
	t.Helper()
	saved := liveCheckURL
	liveCheckURL = url
	t.Cleanup(func() { liveCheckURL = saved })
}

func TestLiveCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-claude-WXYZ" {
			t.Errorf("Authorization header = %q, want Bearer ...", got)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET (live check must be read-only)", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	withLiveCheckURL(t, srv.URL)

	snap := mustParse(t, goldenJSON)
	res, err := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		LiveCheck: true,
		Clock:     func() time.Time { return time.UnixMilli(1893456000000 - 60_000) },
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Validate err: %v", err)
	}
	if res.Status != formats.StatusOK {
		t.Errorf("Status = %q, want ok", res.Status)
	}
}

func TestLiveCheck_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	withLiveCheckURL(t, srv.URL)

	snap := mustParse(t, goldenJSON)
	res, _ := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		LiveCheck: true,
		Clock:     func() time.Time { return time.UnixMilli(1893456000000 - 60_000) },
	})
	if res.Status != formats.StatusExpired {
		t.Errorf("Status = %q, want expired", res.Status)
	}
}

func TestLiveCheck_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	withLiveCheckURL(t, srv.URL)

	snap := mustParse(t, goldenJSON)
	res, _ := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		LiveCheck: true,
		Clock:     func() time.Time { return time.UnixMilli(1893456000000 - 60_000) },
	})
	if res.Status != formats.StatusScopeWarn {
		t.Errorf("Status = %q, want scope_warn", res.Status)
	}
}

func TestLiveCheck_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withLiveCheckURL(t, srv.URL)

	snap := mustParse(t, goldenJSON)
	res, _ := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		LiveCheck: true,
		Clock:     func() time.Time { return time.UnixMilli(1893456000000 - 60_000) },
	})
	if res.Status != formats.StatusUnreachable {
		t.Errorf("Status = %q, want unreachable", res.Status)
	}
}

func TestLiveCheck_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the configured Timeout to force the client to
		// abort. This exercises the timeout path without depending on real
		// DNS or external network state.
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	withLiveCheckURL(t, srv.URL)

	snap := mustParse(t, goldenJSON)
	res, _ := Format{}.Validate(context.Background(), snap, formats.ValidateOpts{
		LiveCheck: true,
		Timeout:   25 * time.Millisecond,
		Clock:     func() time.Time { return time.UnixMilli(1893456000000 - 60_000) },
	})
	if res.Status != formats.StatusUnreachable {
		t.Errorf("Status = %q, want unreachable", res.Status)
	}
}

func TestCompare_ExpiresAtMax(t *testing.T) {
	mk := func(exp int64) formats.Snapshot {
		raw := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"a%d","refreshToken":"r","expiresAt":%d}}`, exp, exp)
		s, err := Format{}.Parse([]byte(raw))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return s
	}
	older := mk(1000)
	newer := mk(2000)
	equal := mk(1000)

	if got := (Format{}).Compare(StrategyExpiresAtMax, newer, older); got != 1 {
		t.Errorf("Compare(newer,older) = %d, want +1", got)
	}
	if got := (Format{}).Compare(StrategyExpiresAtMax, older, newer); got != -1 {
		t.Errorf("Compare(older,newer) = %d, want -1", got)
	}
	if got := (Format{}).Compare(StrategyExpiresAtMax, older, equal); got != 0 {
		t.Errorf("Compare(equal,equal) = %d, want 0", got)
	}
}

func TestCompare_UnknownStrategyPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Compare with unknown strategy did not panic")
		}
	}()
	a := mustParse(t, goldenJSON)
	_ = (Format{}).Compare("nonexistent", a, a)
}

func TestRedact_NoSecretLeak(t *testing.T) {
	snap := mustParse(t, goldenJSON)
	sum := Format{}.Redact(snap)

	if sum.Identity == "" {
		t.Errorf("Identity empty")
	}
	if sum.Subscription != "max" {
		t.Errorf("Subscription = %q, want %q", sum.Subscription, "max")
	}
	if got, want := sum.TokenTail4, "WXYZ"; got != want {
		t.Errorf("TokenTail4 = %q, want %q", got, want)
	}
	if len(sum.FingerprintShort) != fingerprintShortLen {
		t.Errorf("FingerprintShort length = %d, want %d", len(sum.FingerprintShort), fingerprintShortLen)
	}
	// Make sure the full access token never appears anywhere in the summary's
	// stringified fields.
	combined := sum.Identity + sum.Subscription + sum.FingerprintShort + sum.TokenTail4
	for _, sc := range sum.Scopes {
		combined += sc
	}
	if contains(combined, "test-claude-WXYZ") {
		t.Errorf("Summary leaked access token: %v", sum)
	}
}

// contains avoids pulling in strings just for substring assertions in tests.
func contains(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestFormat_RegisteredInGlobalRegistry(t *testing.T) {
	got, ok := formats.Get(FormatName)
	if !ok {
		t.Fatalf("Format %q not registered via init()", FormatName)
	}
	if got.Name() != FormatName {
		t.Errorf("Get returned a Format named %q, want %q", got.Name(), FormatName)
	}
	if got, want := got.Strategies(), []string{StrategyExpiresAtMax}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Strategies() = %v, want %v", got, want)
	}
}
