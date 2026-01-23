package mobile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"time"
)

func init() {
	// Enable full stack traces on panic
	debug.SetTraceback("all")
}

// mobileHandler is a custom slog.Handler that writes to stderr.
// On Android, stderr is forwarded to logcat when running debug builds.
type mobileHandler struct {
	level slog.Level
}

// newMobileLogger creates a logger optimized for mobile platforms.
// It writes to stderr which Android forwards to logcat.
func newMobileLogger() *slog.Logger {
	handler := &mobileHandler{level: slog.LevelDebug}
	return slog.New(handler)
}

func (h *mobileHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *mobileHandler) Handle(_ context.Context, r slog.Record) error {
	// Format: [GHOST] LEVEL TIME MESSAGE key=value ...
	timestamp := r.Time.Format("15:04:05.000")
	level := r.Level.String()

	msg := fmt.Sprintf("[GHOST] %s %s %s", level, timestamp, r.Message)

	// Append attributes
	r.Attrs(func(a slog.Attr) bool {
		msg += fmt.Sprintf(" %s=%v", a.Key, a.Value.Any())
		return true
	})

	// Write to stderr - Android forwards this to logcat
	fmt.Fprintln(os.Stderr, msg)
	return nil
}

func (h *mobileHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h // Simplified - doesn't carry attrs
}

func (h *mobileHandler) WithGroup(name string) slog.Handler {
	return h // Simplified - doesn't support groups
}

// LogInfo logs an info message. Convenience function for mobile.
func LogInfo(msg string) {
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[GHOST] INFO %s %s\n", timestamp, msg)
}

// LogError logs an error message. Convenience function for mobile.
func LogError(msg string, err error) {
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[GHOST] ERROR %s %s: %v\n", timestamp, msg, err)
}

// LogDebug logs a debug message. Convenience function for mobile.
func LogDebug(msg string) {
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[GHOST] DEBUG %s %s\n", timestamp, msg)
}

// LogStackTrace logs the current stack trace for debugging.
// Use sparingly - prefer structured logging with error context.
func LogStackTrace(msg string) {
	timestamp := time.Now().Format("15:04:05.000")
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	fmt.Fprintf(os.Stderr, "[GHOST] TRACE %s %s\n%s\n", timestamp, msg, buf[:n])
}
