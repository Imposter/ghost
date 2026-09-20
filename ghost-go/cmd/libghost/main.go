// Command libghost builds the ghost library as a C shared object
// (-buildmode=c-shared) so a host application — a Flutter app through Dart
// FFI, for example — can embed a ghost node without a Go runtime of its own.
//
// It is a thin wrapper around the public ghost-go API, in the spirit of
// ghost-cli: every call takes and returns UTF-8 JSON, a node lives behind an
// opaque integer handle, and no Go pointer ever crosses the boundary.
//
//	const char* ghost_version(void);
//	char*       ghost_enroll(const char* request_json);
//	ghost_handle ghost_start(const char* config_json, char** err);
//	int         ghost_stop(ghost_handle h);
//	char*       ghost_status_json(ghost_handle h);
//	char*       ghost_next_event_json(ghost_handle h, int timeout_ms);
//	int         ghost_set_policy(ghost_handle h, const char* policy_json, char** err);
//	char*       ghost_metrics_json(ghost_handle h);
//	void        ghost_free(char* p);
//
// The C entry points live in export.go, which needs cgo; everything else is
// ordinary Go and is what the tests drive. See docs/ffi.md for the JSON
// shapes, the ownership and threading rules, and the build steps.
package main

// main is never called in a shared library; it exists because
// -buildmode=c-shared requires package main.
func main() {}
