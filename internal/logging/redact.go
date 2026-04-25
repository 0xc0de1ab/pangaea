package logging

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

const (
	redactedMark         = "<redacted>"
	redactedOversizeMark = "<redacted:oversize>"
	oversizeThreshold    = 64 * 1024
)

// Keys whose values are always scrubbed regardless of content.
var redactedKeys = map[string]struct{}{
	"raw":           {},
	"raw_b64":       {},
	"accessToken":   {},
	"refreshToken":  {},
	"Authorization": {},
	"authorization": {},
}

// Value patterns that, when matched inside any string value, trigger redaction.
// Patterns intentionally greedy up to whitespace so tokens embedded in
// arbitrary fields still get caught.
var redactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-oat01-\S+`),
	regexp.MustCompile(`sk-ant-ort01-\S+`),
	regexp.MustCompile(`(?i)Bearer\s+\S+`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
}

type redactHandler struct {
	inner slog.Handler
}

func newRedactHandler(h slog.Handler) slog.Handler { return &redactHandler{inner: h} }

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	cleaned := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		cleaned.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, cleaned)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = redactAttr(a)
	}
	return &redactHandler{inner: h.inner.WithAttrs(red)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if _, hit := redactedKeys[a.Key]; hit {
		return slog.String(a.Key, redactedMark)
	}
	return slog.Attr{Key: a.Key, Value: redactValue(a.Value)}
}

func redactValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(scrubString(v.String()))
	case slog.KindAny:
		return slog.AnyValue(scrubAny(v.Any()))
	case slog.KindGroup:
		attrs := v.Group()
		out := make([]slog.Attr, len(attrs))
		for i, a := range attrs {
			out[i] = redactAttr(a)
		}
		return slog.GroupValue(out...)
	default:
		return v
	}
}

func scrubString(s string) string {
	if len(s) > oversizeThreshold {
		return redactedOversizeMark
	}
	out := s
	for _, re := range redactPatterns {
		out = re.ReplaceAllString(out, redactedMark)
	}
	return out
}

func scrubAny(v any) any {
	switch x := v.(type) {
	case string:
		return scrubString(x)
	case []byte:
		if len(x) > oversizeThreshold {
			return redactedOversizeMark
		}
		return scrubString(string(x))
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			if _, hit := redactedKeys[k]; hit {
				out[k] = redactedMark
				continue
			}
			out[k] = scrubAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = scrubAny(vv)
		}
		return out
	default:
		// For fmt.Stringer-ish or other types, fall back to string scrub on
		// the textual form. Gives best-effort coverage without reflection
		// over arbitrary struct types (which would be fragile).
		s, ok := v.(interface{ String() string })
		if ok {
			return scrubString(s.String())
		}
		if str, isStr := v.(string); isStr {
			return scrubString(str)
		}
		// Check for big []byte by interface assertion already done; leave as-is.
		if sv, isStringer := tryString(v); isStringer {
			return scrubString(sv)
		}
		return v
	}
}

func tryString(v any) (string, bool) {
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String(), true
	}
	if s, ok := v.(error); ok {
		return s.Error(), true
	}
	return "", false
}

// Exposed for tests / tooling.
func RedactString(s string) string { return scrubString(s) }

// For tests that want to assert a string has no obvious token residue.
func HasTokenResidue(s string) bool {
	for _, re := range redactPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// helper to reduce boilerplate in tests
var _ = strings.Contains
