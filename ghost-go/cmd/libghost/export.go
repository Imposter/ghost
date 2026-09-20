//go:build cgo

package main

/*
#include <stdlib.h>
*/
import "C"

import "unsafe"

// This file is the only place a C type appears. Every entry point converts at
// the boundary and hands the work to the ordinary Go code in the rest of the
// package, which is what the tests drive.
//
// Ownership: every char* an entry point returns, including the ones written
// through an err out-parameter, is allocated by this library and owned by the
// caller, who must release it with ghost_free. The single exception is
// ghost_version, which returns a pointer to a string that lives as long as
// the library.
//
// Threading: the entry points may be called from any thread at any time.
// ghost_next_event_json blocks the calling thread for up to timeout_ms, so it
// belongs on a thread of its own.

// versionC is allocated once and never freed; ghost_version hands out the
// same pointer every time.
var versionC = C.CString(apiVersion())

// goString copies a C string into Go. A NULL pointer becomes "".
func goString(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}

// setErr writes msg through an optional char** out-parameter.
func setErr(out **C.char, msg string) {
	if out != nil {
		*out = C.CString(msg)
	}
}

// ghost_version returns the library's ABI version. The pointer is static: do
// not free it.
//
//export ghost_version
func ghost_version() *C.char { return versionC }

// ghost_enroll enrols a peer with a pre-auth key and returns
// {"ok":true,"creds":{…}} or {"ok":false,"error":"…"}. It blocks for up to 30
// seconds. Free the result with ghost_free.
//
//export ghost_enroll
func ghost_enroll(requestJSON *C.char) *C.char {
	return C.CString(apiEnroll(goString(requestJSON)))
}

// ghost_start builds a node from a JSON config, starts it, and returns its
// handle. It returns 0 on failure and, when err is not NULL, writes an error
// message there for the caller to free with ghost_free. Peers connect
// asynchronously; watch ghost_next_event_json.
//
//export ghost_start
func ghost_start(configJSON *C.char, err **C.char) C.longlong {
	h, e := apiStart(goString(configJSON), nil)
	if e != nil {
		setErr(err, e.Error())
		return 0
	}
	return C.longlong(h)
}

// ghost_stop shuts a node down and releases the handle. It returns 0 on
// success and -1 for an unknown handle or a failed shutdown. It is safe to
// call while another thread waits in ghost_next_event_json: that call returns
// the final "stopped" event and then reports no more.
//
//export ghost_stop
func ghost_stop(h C.longlong) C.int {
	if err := apiStop(int64(h)); err != nil {
		return -1
	}
	return 0
}

// ghost_status_json returns the node's status as JSON, or
// {"ok":false,"error":"…"} for an unknown handle. Free it with ghost_free.
//
//export ghost_status_json
func ghost_status_json(h C.longlong) *C.char {
	return C.CString(apiStatus(int64(h)))
}

// ghost_next_event_json returns the next buffered event as JSON, waiting up
// to timeout_ms for one (a negative timeout waits until an event arrives or
// the node stops). It returns NULL on timeout, for an unknown handle, and
// once a stopped node's events have run out. Free a result with ghost_free.
//
//export ghost_next_event_json
func ghost_next_event_json(h C.longlong, timeoutMS C.int) *C.char {
	s, ok := apiNextEvent(int64(h), int(timeoutMS))
	if !ok {
		return nil
	}
	return C.CString(s)
}

// ghost_set_policy applies the host application's local exit policy: the
// allowlist, the caps and pause. It returns 0 on success and -1 on failure,
// writing an error message through err when that is not NULL. The local
// policy only ever narrows the control plane's.
//
//export ghost_set_policy
func ghost_set_policy(h C.longlong, policyJSON *C.char, err **C.char) C.int {
	if e := apiSetPolicy(int64(h), goString(policyJSON)); e != nil {
		setErr(err, e.Error())
		return -1
	}
	return 0
}

// ghost_metrics_json returns the node's metrics snapshot and its recent exit
// connections as JSON, or {"ok":false,"error":"…"} for an unknown handle.
// Free it with ghost_free.
//
//export ghost_metrics_json
func ghost_metrics_json(h C.longlong) *C.char {
	return C.CString(apiMetrics(int64(h)))
}

// ghost_free releases a string this library returned. A NULL pointer is
// ignored.
//
//export ghost_free
func ghost_free(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}
