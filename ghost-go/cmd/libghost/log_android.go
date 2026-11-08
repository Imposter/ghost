//go:build android && cgo

package main

/*
#cgo LDFLAGS: -llog
#include <stdlib.h>
#include <android/log.h>
*/
import "C"

import (
	"io"
	"log/slog"
	"runtime/debug"
	"unsafe"
)

// An Android app's standard error goes nowhere, so on Android a node logs to
// logcat under logTag instead, and a crash prints every goroutine: the Go
// runtime already writes its crash report to logcat (tag "Go"), but by default
// only the panicking goroutine's stack.
func init() { debug.SetTraceback("all") }

// logTag is the logcat tag node logs carry: adb logcat -s ghost.
const logTag = "ghost"

var logTagC = C.CString(logTag)

// logOutput is where node logs go: logcat.
var logOutput io.Writer = logcatWriter{}

// logcatWriter writes each slog text record to logcat at the priority of its
// level, as one entry, or as consecutive entries at that priority when it is
// longer than logcat keeps (see logcatChunks).
type logcatWriter struct{}

func (logcatWriter) Write(p []byte) (int, error) {
	prio := logcatPriority(recordLevel(p))
	for _, chunk := range logcatChunks(p, logcatMaxEntry) {
		msg := C.CString(string(chunk))
		C.__android_log_write(prio, logTagC, msg)
		C.free(unsafe.Pointer(msg))
	}
	return len(p), nil
}

// logcatPriority maps a slog level to a logcat priority; a level between two
// of slog's (DEBUG+2, say) takes the lower one's.
func logcatPriority(l slog.Level) C.int {
	switch {
	case l >= slog.LevelError:
		return C.ANDROID_LOG_ERROR
	case l >= slog.LevelWarn:
		return C.ANDROID_LOG_WARN
	case l >= slog.LevelInfo:
		return C.ANDROID_LOG_INFO
	default:
		return C.ANDROID_LOG_DEBUG
	}
}
