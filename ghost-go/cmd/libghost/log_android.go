//go:build android && cgo

package main

/*
#cgo LDFLAGS: -llog
#include <stdlib.h>
#include <android/log.h>
*/
import "C"

import (
	"bytes"
	"io"
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

// logcatWriter writes each slog text record as one logcat entry, at the
// priority its level= field names.
type logcatWriter struct{}

func (logcatWriter) Write(p []byte) (int, error) {
	msg := C.CString(string(bytes.TrimRight(p, "\n")))
	defer C.free(unsafe.Pointer(msg))
	C.__android_log_write(logcatPriority(p), logTagC, msg)
	return len(p), nil
}

// logcatPriority maps a text record's level to a logcat priority.
func logcatPriority(record []byte) C.int {
	switch {
	case bytes.Contains(record, []byte(" level=ERROR")):
		return C.ANDROID_LOG_ERROR
	case bytes.Contains(record, []byte(" level=WARN")):
		return C.ANDROID_LOG_WARN
	case bytes.Contains(record, []byte(" level=DEBUG")):
		return C.ANDROID_LOG_DEBUG
	default:
		return C.ANDROID_LOG_INFO
	}
}
