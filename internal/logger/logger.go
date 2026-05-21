// Package logger provides initialisation for the structured application logger.
//
// GoGateway uses log/slog (Go 1.21+) for structured, levelled logging with
// JSON output suitable for production log aggregation.
package logger

import (
	"io"
	"log/slog"
	"os"
)

// Init configures the slog default logger with JSON output at the given level.
// If level is the zero value, slog.LevelInfo is used.
//
// The output writer defaults to os.Stdout. Pass a non-nil writer for tests.
func Init(level slog.Level, w io.Writer) {
	if w == nil {
		w = os.Stdout
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	handler := slog.NewJSONHandler(w, opts)
	slog.SetDefault(slog.New(handler))
}
