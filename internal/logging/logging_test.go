package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNew_Defaults(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Output: &buf})
	l.Info("hello", slog.String("k", "v"))

	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("expected JSON output: %v\n%s", err, buf.String())
	}
	if got["msg"] != "hello" {
		t.Fatalf("msg field missing, got: %v", got)
	}
}

func TestNew_UnknownLevelFallback(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "banana", Output: &buf})
	l.Info("x")

	// Should contain a warn line about fallback AND the info record.
	if !strings.Contains(buf.String(), "unknown log level") {
		t.Fatalf("want fallback warn, got: %s", buf.String())
	}
	// Info line still present.
	if !strings.Contains(buf.String(), "\"msg\":\"x\"") {
		t.Fatalf("info record missing: %s", buf.String())
	}
}

func TestRedact_KeyBasedAccessToken(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Output: &buf})
	l.Info("auth",
		slog.String("accessToken", "sk-ant-oat01-ABCDEF1234567"),
		slog.String("user", "alice"),
	)
	if strings.Contains(buf.String(), "ABCDEF1234567") {
		t.Fatalf("token leaked through key-based redact: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "alice") {
		t.Fatalf("non-sensitive field lost: %s", buf.String())
	}
}

func TestRedact_ValuePatternNested(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Output: &buf})

	// Nested map via slog.Any — value carries a Bearer token in free text.
	nested := map[string]any{
		"note":    "Authorization: Bearer abcdefghijklmno",
		"inner":   map[string]any{"tok": "Bearer XYZ123456"},
		"harmless": []any{"ok", "also ok"},
	}
	l.Info("req", slog.Any("payload", nested))

	out := buf.String()
	if strings.Contains(out, "abcdefghijklmno") {
		t.Fatalf("bearer token leaked: %s", out)
	}
	if strings.Contains(out, "XYZ123456") {
		t.Fatalf("nested bearer token leaked: %s", out)
	}
	if !strings.Contains(out, "harmless") {
		t.Fatalf("expected harmless key to remain")
	}
}

func TestRedact_Oversize(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Output: &buf})

	big := strings.Repeat("a", 128*1024)
	l.Info("big", slog.String("blob", big))
	if !strings.Contains(buf.String(), "<redacted:oversize>") {
		t.Fatalf("oversize value not redacted")
	}
	if strings.Contains(buf.String(), strings.Repeat("a", 1024)) {
		t.Fatalf("oversize content still present")
	}
}

func TestRedact_JWT(t *testing.T) {
	// Synthetic JWT-shaped value.
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	if !HasTokenResidue(jwt) {
		t.Fatalf("pattern did not match JWT")
	}
	if RedactString(jwt) != "<redacted>" {
		t.Fatalf("JWT not fully redacted: %q", RedactString(jwt))
	}
}

func TestRedact_TextHandler(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Format: "text", Output: &buf})
	l.Info("auth", slog.String("refreshToken", "sk-ant-ort01-SHOULD_VANISH"))
	if strings.Contains(buf.String(), "SHOULD_VANISH") {
		t.Fatalf("refreshToken leaked in text format: %s", buf.String())
	}
}
