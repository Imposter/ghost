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

// LogPanic logs a panic message and stack trace.
func LogPanic(msg string) {
	timestamp := time.Now().Format("15:04:05.000")
	buf := make([]byte, 16384)    // Larger buffer for full stack
	n := runtime.Stack(buf, true) // true = all goroutines
	fmt.Fprintf(os.Stderr, "[GHOST] PANIC %s %s\n%s\n", timestamp, msg, buf[:n])
}

// LogStackTrace logs the current stack trace without panicking.
func LogStackTrace(msg string) {
	timestamp := time.Now().Format("15:04:05.000")
	buf := make([]byte, 16384)
	n := runtime.Stack(buf, false)
	fmt.Fprintf(os.Stderr, "[GHOST] TRACE %s %s\n%s\n", timestamp, msg, buf[:n])
}

// RecoverAndLog recovers from a panic and logs the full stack trace.
// Use with defer: defer RecoverAndLog("methodName")
func RecoverAndLog(methodName string) {
	if r := recover(); r != nil {
		timestamp := time.Now().Format("15:04:05.000")
		buf := make([]byte, 16384)
		n := runtime.Stack(buf, true) // true = all goroutines
		fmt.Fprintf(os.Stderr, "[GHOST] PANIC %s in %s: %v\n%s\n", timestamp, methodName, r, buf[:n])
	}
}

// WrapWithRecover wraps a function with panic recovery that logs and re-panics.
// This ensures stack traces are captured but the panic still propagates.
func WrapWithRecover(methodName string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			timestamp := time.Now().Format("15:04:05.000")
			buf := make([]byte, 16384)
			n := runtime.Stack(buf, true)
			fmt.Fprintf(os.Stderr, "[GHOST] PANIC %s in %s: %v\n%s\n", timestamp, methodName, r, buf[:n])
			panic(r) // Re-panic after logging
		}
	}()
	fn()
}
