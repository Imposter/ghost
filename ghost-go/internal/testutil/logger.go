package testutil

import (
	"log/slog"
	"os"
	"testing"
)

// NewTestLogger creates a logger suitable for testing.
// Uses DEBUG level to help with test debugging.
func NewTestLogger(t *testing.T) *slog.Logger {
	// Create a handler that outputs to testing.T
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	logger := slog.New(handler).With(
		"test", t.Name(),
	)

	return logger
}

// NewQuietTestLogger creates a logger that only logs errors and above.
// Useful for tests where you don't want verbose output.
func NewQuietTestLogger(t *testing.T) *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	logger := slog.New(handler).With(
		"test", t.Name(),
	)

	return logger
}
