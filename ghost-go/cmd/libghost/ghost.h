/*
 * ghost.h -- the C API of libghost, the c-shared build of ghost-go.
 *
 * This header is the contract; cgo also generates libghost.h next to the
 * library, which declares the same symbols without the const qualifiers (cgo
 * cannot express them). Either works with ffigen; this one is the tidier
 * input.
 *
 * Every string crossing the boundary is UTF-8 JSON. Every char* a function
 * returns, including one written through an err out-parameter, is owned by
 * the caller and must be released with ghost_free. The one exception is
 * ghost_version, whose pointer lives as long as the library.
 *
 * The functions may be called from any thread. ghost_next_event_json blocks
 * the calling thread for up to timeout_ms, so give it a thread of its own.
 *
 * See docs/ffi.md for the JSON shapes and the build steps.
 */

#ifndef GHOST_H
#define GHOST_H

#ifdef __cplusplus
extern "C" {
#endif

/* A running node. Handles are issued from 1; 0 is never a valid handle. */
typedef long long ghost_handle;

/* The library's ABI version, e.g. "0.1.0". Static: do not free. */
const char *ghost_version(void);

/*
 * Enrol a peer with a pre-auth key.
 *   in:  {"server","auth_key","name","labels","key_store_path"}
 *   out: {"ok":true,"creds":{…}} or {"ok":false,"error":"…"}
 * Blocks for up to 30 seconds. Free the result with ghost_free.
 */
char *ghost_enroll(const char *request_json);

/*
 * Start a node from a JSON config and return its handle. Returns 0 on
 * failure and, when err is not NULL, writes a message there to free with
 * ghost_free. Peers connect asynchronously: watch ghost_next_event_json.
 */
ghost_handle ghost_start(const char *config_json, char **err);

/*
 * Stop a node and release its handle. Returns 0 on success, -1 for an
 * unknown handle or a failed shutdown. Safe to call while another thread
 * waits in ghost_next_event_json.
 */
int ghost_stop(ghost_handle h);

/*
 * The node's status as JSON: signalling state, tunnel address, every netmap
 * peer with its link state and ICE candidate type, and the exit. Returns
 * {"ok":false,"error":"…"} for an unknown handle. Free with ghost_free.
 */
char *ghost_status_json(ghost_handle h);

/*
 * The next buffered event as JSON, one per call, waiting up to timeout_ms
 * (negative waits until an event arrives or the node stops). Returns NULL on
 * timeout, for an unknown handle, and once a stopped node's events have run
 * out. Free a result with ghost_free.
 */
char *ghost_next_event_json(ghost_handle h, int timeout_ms);

/*
 * Apply the host application's local exit policy: allowlist, caps and pause.
 *   {"allow":[…],"daily_bytes":N,"bytes_per_second":N,"paused":bool}
 * Absent fields are left alone. The local policy only narrows the control
 * plane's. Returns 0, or -1 with a message through err (free with
 * ghost_free).
 */
int ghost_set_policy(ghost_handle h, const char *policy_json, char **err);

/*
 * The node's metrics snapshot and its recent exit connections as JSON.
 * Returns {"ok":false,"error":"…"} for an unknown handle. Free with
 * ghost_free.
 */
char *ghost_metrics_json(ghost_handle h);

/* Release a string this library returned. NULL is ignored. */
void ghost_free(char *p);

#ifdef __cplusplus
}
#endif

#endif /* GHOST_H */
