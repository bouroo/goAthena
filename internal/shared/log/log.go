// Package log builds the process-wide structured logger (log/slog) from config.
package log

import (
	"log/slog"
	"os"
	"strings"

	"github.com/bouroo/goAthena/internal/config"
)

// New returns a slog.Logger configured for the requested level and format.
// JSON is the default (container-friendly); text is friendlier for local dev.
func New(cfg config.LogConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level(cfg.Level)}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func level(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
