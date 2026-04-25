package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options controls logger construction.
type Options struct {
	Level  string    // "debug" | "info" | "warn" | "error" (case-insensitive). Empty = info.
	Format string    // "json" | "text". Empty = json.
	Output io.Writer // nil = os.Stderr.
}

// New returns a slog.Logger with the redacting handler installed. Bad Level
// falls back to info and writes a single warning line to Output so operators
// notice the mis-configuration without the logger silently swallowing it.
func New(o Options) *slog.Logger {
	out := o.Output
	if out == nil {
		out = os.Stderr
	}

	lvl, badLevel := parseLevel(o.Level)
	hopts := &slog.HandlerOptions{Level: lvl}

	var base slog.Handler
	switch strings.ToLower(strings.TrimSpace(o.Format)) {
	case "", "json":
		base = slog.NewJSONHandler(out, hopts)
	case "text":
		base = slog.NewTextHandler(out, hopts)
	default:
		base = slog.NewJSONHandler(out, hopts)
		fmt.Fprintf(out, "{\"level\":\"WARN\",\"msg\":\"unknown log format %q, using json\"}\n", o.Format)
	}

	h := newRedactHandler(base)
	l := slog.New(h)
	if badLevel != "" {
		l.Warn("unknown log level, falling back to info", slog.String("given", badLevel))
	}
	return l
}

func parseLevel(s string) (slog.Level, string) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, ""
	case "debug":
		return slog.LevelDebug, ""
	case "warn", "warning":
		return slog.LevelWarn, ""
	case "error":
		return slog.LevelError, ""
	default:
		return slog.LevelInfo, s
	}
}
