// Package platform holds tiny shared helpers (logging, HTTP client).
// M0 file: created on Ahmed's side to compile A0/A1; Mohammed owns it from now on.
package platform

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds a slog logger. level: debug|info|warn|error; format: text|json.
func NewLogger(level, format string) *slog.Logger {
	return NewLoggerTo(os.Stdout, level, format)
}

// NewLoggerTo is NewLogger writing to w (useful in tests).
func NewLoggerTo(w io.Writer, level, format string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if strings.ToLower(format) == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
