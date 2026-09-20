# libghost: the C API

`ghost-go/cmd/libghost` builds ghost as a C shared library
(`-buildmode=c-shared`), so a host application — a Flutter app through Dart
FFI, for example — can embed a ghost node without a Go runtime of its own.

It is a thin wrapper over the same public API
[`ghost-cli`](../ghost-go/cmd/ghost-cli) uses: a node behind an opaque
integer handle, UTF-8 JSON in and out, and no Go pointer across the boundary.
A node joins a network, links to the peers in its netmap, and optionally
serves an allowlisted exit on its tunnel IP.

## The header

`ghost-go/cmd/libghost/ghost.h` is the contract. cgo also writes
`libghost.h` next to the library; it declares the same symbols but without
the `const` qualifiers, which cgo cannot express. Either is valid ffigen
input; `ghost.h` is the tidier one.

```c
typedef long long ghost_handle;

const char* ghost_version(void);                                 /* static, do not free */
char* ghost_enroll(const char* request_json);
ghost_handle ghost_start(const char* config_json, char** err);   /* 0 on failure, *err set */
int   ghost_stop(ghost_handle h);                                /* 0 ok, -1 failed */
char* ghost_status_json(ghost_handle h);
char* ghost_next_event_json(ghost_handle h, int timeout_ms);     /* NULL on timeout */
int   ghost_set_policy(ghost_handle h, const char* policy_json, char** err);
char* ghost_metrics_json(ghost_handle h);
void  ghost_free(char* p);
```

## Ownership and threading

- Every `char*` a function returns, including one written through an `err`
  out-parameter, is allocated by the library and owned by the caller, who
  releases it with `ghost_free`. `ghost_version` is the one exception: its
  pointer lives as long as the library and must not be freed.
- A `NULL` string argument reads as `""`; `ghost_free(NULL)` is a no-op.
- Passing `NULL` for `err` is allowed: the message is then dropped.
- Handles are issued from 1. `0` is never a valid handle. A stopped handle is
  never reissued during the life of the process, so a stale handle reports
  `no such ghost handle` rather than reaching another node.
- Every function may be called from any thread at any time.
  `ghost_next_event_json` blocks the calling thread for up to `timeout_ms`,
  so give it a thread (or a Dart isolate) of its own. Calling `ghost_stop`
  while another thread waits there is safe: the waiter gets the final
  `stopped` event and then `NULL`.
- Nothing in the library owns memory the host passed in; argument strings are
  copied before the call returns.

## Errors

`ghost_start` and `ghost_set_policy` report through the `err` out-parameter
and a status code. `ghost_status_json` and `ghost_metrics_json` cannot fail
for a live handle; for an unknown one they return
`{"ok":false,"error":"no such ghost handle"}`, so a caller can branch on
`ok`. `ghost_enroll` always returns a document with `ok`.

Every JSON document the library accepts is parsed strictly: an unknown field
is an error, not something to ignore. A misspelt `key_store_path` would
otherwise silently give the peer an ephemeral WireGuard key.

## ghost_enroll

Enrols this device as a peer with a pre-auth key (`gak_…`) and returns the
credentials a start config takes. It calls `POST {server}/v1/enroll` and
blocks for up to 30 seconds.

```jsonc
{
  "server": "https://ghost.example.com",   // required, the control plane base URL
  "auth_key": "gak_…",                     // required
  "name": "phone-1",                       // optional, the peer's name in netmaps
  "labels": {"geo": "ca-on"},              // optional
  "key_store_path": "/data/ghost/keys.json", // the WireGuard key file; created if missing
  "public_key": ""                         // instead of key_store_path, a key you already hold
}
```

```jsonc
{
  "ok": true,
  "creds": {
    "peer_id": "peer_…",
    "peer_token": "gpt_…",
    "network": "lab",
    "roles": ["node", "exit"],
    "tags": ["tag:mobile"],
    "server": "https://ghost.example.com",
    "public_key": "…"
  }
}
```

On failure: `{"ok":false,"error":"enroll: 401 Unauthorized: invalid auth key"}`.

The peer's WireGuard public key is registered **at enrolment**, so
`key_store_path` should be the same path the start config later uses. Persist
the whole `creds` object; it is exactly the `creds` member of a start config.

## ghost_start

```jsonc
{
  "creds": {
    "server": "https://ghost.example.com", // base URL; {server}/v1/signal is the socket
    "signal_url": "",                      // or the signalling URL outright (ws:// or wss://)
    "peer_id": "peer_…",                   // optional, from enrolment
    "peer_token": "gpt_…",                 // required
    "network": "lab"
  },
  "network": "",                     // overrides creds.network
  "key_store_path": "/data/ghost/keys.json", // empty = an ephemeral key each run
  "roles": ["node"],                 // roles to ask for; exit.enabled adds "exit"
  "stun": ["stun:stun.l.google.com:19302"],
  "turn": [{"urls": ["turn:turn.example.com:3478"], "username": "u", "password": "p"}],
  "port_min": 0, "port_max": 0,      // bound the local UDP ports ICE binds
  "mtu": 0,                          // 0 = 1280
  "dns": [],                         // DNS servers for the tunnel netstack
  "connect_timeout_ms": 30000,       // ICE connect timeout per peer
  "log_level": "warn",               // debug, info, warn, error; logs go to stderr

  "exit": {
    "enabled": true,
    "port": 1080,                    // 0 = exit.DefaultPort (1080)
    "allow": ["*.example.com:443"],  // the local allowlist; absent = no local narrowing
    "daily_bytes": 0,                // local caps, 0 = no local cap
    "bytes_per_second": 0,
    "paused": false
  },
  "metrics": {"enabled": false, "port": 0}  // serve metrics inside the tunnel, to hubs
}
```

The control plane always advertises its own STUN and TURN servers; the ones
here are added to them. A member can *ask* for roles but never grant itself
one: the server refuses the session unless the peer was enrolled with every
role asked for.

`metrics.enabled` only decides whether the node serves its metrics **inside
the tunnel** on its tunnel IP, where the netmap's hubs may read them.
`ghost_metrics_json` works either way.

`ghost_start` returns as soon as the signalling session is up and the join has
been sent. Peers link asynchronously: wait for `joined` and `peer_connected`
on the event stream.

## ghost_status_json

```jsonc
{
  "ok": true,
  "handle": 1,
  "connected": true,                 // usable: signalling up and joined
  "signal_state": "connected",       // disconnected | connecting | connected | closed
  "joined": true,                    // the control plane has the node in its network
  "peer_id": "peer_…",
  "network": "lab",
  "address": "100.64.0.5/32",        // the tunnel address, CIDR form
  "tunnel_address": "100.64.0.5",    // and the bare IP
  "roles": ["node", "exit"],
  "isolation": "hub-only",           // the network's isolation mode
  "peer_count": 1,                   // linked tunnel peers
  "netmap_peers": 1,                 // peers the netmap lists
  "peers": [
    {
      "peer_id": "hub-1",
      "name": "hub",
      "address": "100.64.0.2/32",
      "roles": ["hub"],
      "labels": {"geo": "ca-on"},
      "online": true,                // what the control plane says
      "linked": true,                // a live tunnel to this peer: WireGuard
                                     // has it and its ICE path is up
      "candidate_type": "host",      // host | srflx | prflx | relay
      "rtt_seconds": 0.002,
      "rx_bytes": 5120,
      "tx_bytes": 4096
    }
  ],
  "exit": {
    "enabled": true,
    "listen": "100.64.0.5:1080",     // empty until the node has joined
    "port": 1080,
    "allow": ["*.example.com:443"],  // the control plane's allowlist
    "local_allow": [],               // the one ghost_set_policy set
    "local":     {"daily_bytes": 0, "bytes_per_second": 0, "paused": false},
    "effective": {"daily_bytes": 0, "bytes_per_second": 0, "paused": false},
    "used_bytes": 0,
    "limit_bytes": 0,                // today's usage against the effective cap
    "active": 0
  }
}
```

`peers` is sorted by peer id. `exit` is `null` when the node serves none.

A status never outlives what it describes. `signal_state` is the websocket
alone: a node the control plane refused a join to stays `connected` and
`"joined": false` while it keeps asking, and `connected` (the usable flag) is
false meanwhile. `peers` is empty while the node is not joined, because the
netmap it last received describes a session it no longer has. `linked`,
`candidate_type` and the byte counters are dropped as soon as ICE reports the
path to a peer disconnected or failed, so a window never shows a working
tunnel over a dead one.

## ghost_next_event_json

One event per call, oldest first, waiting up to `timeout_ms` (a negative
timeout waits until an event arrives or the node stops). `NULL` means "none
yet": a timeout, an unknown handle, or a stopped node whose events have run
out.

```jsonc
{
  "seq": 12,
  "kind": "peer_connected",
  "time": "2026-09-19T21:04:05.12Z",
  "dropped": 0,
  "peer_id": "hub-1",
  "address": "100.64.0.2/32",
  "candidate_type": "host"
}
```

| `kind` | Meaning and extra fields |
| ------ | ------------------------ |
| `signal_state` | the signalling connection changed: `signal_state` |
| `joined` | the network was joined: `address`, this node's tunnel address |
| `netmap` | a new netmap snapshot or delta; read `ghost_status_json` for it |
| `policy` | the control plane's exit policy changed: `policy` |
| `peer_connected` | a tunnel peer is reachable: `peer_id`, `address`, `candidate_type` |
| `peer_disconnected` | a tunnel peer went away: `peer_id` |
| `error` | a non-fatal error: `error`, sometimes `peer_id` |
| `stopped` | the node was stopped; always the last event |

`policy` carries the control plane's document: `{"network","allow",
"daily_bytes","bytes_per_second","paused","labels","revision"}`.

Events sit in a bounded buffer (256 by default), so a host application that
reads slowly never blocks the node. When it overflows the **oldest** events
are dropped, and the next event delivered reports how many in `dropped`.
`seq` numbers every event the node produced, dropped ones included, so a gap
in `seq` is exactly what `dropped` says.

## ghost_set_policy

The host application's own exit policy. Every field is optional; an absent
one is left alone, so `{"paused":true}` pauses and changes nothing else.

```jsonc
{
  "allow": ["*.example.com:443"],  // replaces the local allowlist; [] denies all, null leaves it
  "daily_bytes": 1073741824,       // 0 = no local cap
  "bytes_per_second": 1048576,     // 0 = no local cap
  "paused": true
}
```

The local policy never **widens** the control plane's:

- a destination must pass both allowlists;
- the effective cap is the smaller of the two that are set (0 means
  unlimited on either side);
- the exit is paused when either side pauses it.

So a later policy push from the control plane cannot silently un-pause an
exit the owner paused, and pausing locally cannot be used to escape a
server-side cap. `ghost_status_json` shows `local` and `effective` side by
side. The call fails with `policy: this node serves no exit` when the start
config did not enable one.

Allowlist entries are `host`, `host:port`, or `*.example.com:443`; a leading
`*.` matches any subdomain but not the apex, and a missing port matches any
port. Loopback, link-local, private and tunnel addresses are refused whatever
the allowlist says.

## ghost_metrics_json

```jsonc
{
  "ok": true,
  "handle": 1,
  "snapshot": {
    "time": "2026-09-19T21:04:05Z",
    "peer_id": "peer_…",
    "address": "100.64.0.5/32",
    "totals": {"connections": 3, "bytes_in": 9000, "bytes_out": 400, "active": 1,
               "denied": 1, "cap_used_bytes": 9400, "cap_limit_bytes": 0, "paused": false},
    "destinations": [{"host": "a.example", "port": 443, "connections": 3, "bytes_in": 9000, "bytes_out": 400}],
    "sources":      [{"peer": "hub-1", "source": "scraper", "connections": 3, "bytes_in": 9000, "bytes_out": 400}],
    "protocols":    [{"protocol": "socks5", "transport": "tcp", "connections": 3, "bytes_in": 9000, "bytes_out": 400}],
    "results":      [{"result": "ok", "count": 3}],
    "denied_hosts": [{"host": "blocked.example", "count": 1}],
    "tunnel":       [{"peer_id": "hub-1", "address": "100.64.0.2/32", "candidate_type": "host",
                      "rtt_seconds": 0.002, "last_handshake": "2026-09-19T21:03:58Z",
                      "handshake_age_seconds": 7.1, "rx_bytes": 5120, "tx_bytes": 4096}]
  },
  "connections": [
    {"start": "2026-09-19T21:03:59Z", "source_peer": "hub-1", "source_tag": "source=scraper&job=42",
     "source": "scraper", "job": "42", "protocol": "socks5", "transport": "tcp",
     "sni": "a.example", "host": "a.example", "port": 443, "ip": "93.184.216.34",
     "policy_allowed": true, "result": "ok", "bytes_in": 3000, "bytes_out": 140,
     "duration_seconds": 1.4, "ttfb_seconds": 0.2}
  ]
}
```

`snapshot` is `metrics.Snapshot`, the single contract every ghost metrics
consumer shares (see [architecture.md](architecture.md)); every slice is
sorted deterministically and never `null`. `connections` holds up to 100
recent exit connections, newest first, from the node's bounded ring buffer.
Both are empty when the node serves no exit.

## Building

`ghost-go/cmd/libghost/build.py` drives `go build -buildmode=c-shared`:

```bash
cd ghost-go/cmd/libghost
python3 build.py                        # this machine, into ./dist
python3 build.py --docker --os linux    # linux/amd64 in golang:1.25, no local toolchain
python3 build.py --os linux --arch arm64 --cc aarch64-linux-gnu-gcc
python3 build.py --out ../../../build/libghost
```

Output layout, which is what the host application packages:

```
<out>/include/ghost.h              the curated header (ffigen input)
<out>/<os>-<arch>/libghost.so      linux
<out>/<os>-<arch>/libghost.dylib   darwin
<out>/<os>-<arch>/ghost.dll        windows
<out>/<os>-<arch>/libghost.h       the header cgo generated for that build
```

cgo names the generated header after the library, so on Windows it is
`ghost.h` rather than `libghost.h`; the curated header in `include/` is the
one to hand to ffigen either way.

The Linux library is about 19 MiB unstripped; `strip` or
`--tags` trimming brings it down. `dist/` is ignored by git.

On Windows the interpreter is usually called `python`, not `python3`.

### Toolchains

cgo needs a C compiler **for the target**, which is the only awkward part:

| Target | Compiler |
| ------ | -------- |
| linux/amd64, linux/arm64 | the host gcc, a `*-linux-gnu-gcc` cross compiler, or `--docker` |
| darwin | the Xcode command line tools (`clang`) |
| android | the NDK's `*-linux-android<api>-clang`, with `GOOS=android` |
| windows/amd64 | **mingw-w64** (`--cc x86_64-w64-mingw32-gcc`) or the MSVC build tools |

`build.py` checks for the compiler before it starts and says which one is
missing.

Windows is the one that bites. A bare LLVM install is *not* enough: the
`clang` shipped by llvm.org targets the MSVC ABI and takes its headers and
import libraries from a Visual Studio installation, so without one `CC=clang`
fails at `#include <stdlib.h>`. Install mingw-w64 (MSYS2, or the winget
`mingw` package) and pass `--cc x86_64-w64-mingw32-gcc`, or install the
Visual Studio Build Tools and build from a developer command prompt.

## Testing

The tests in `ghost-go/cmd/libghost` drive the Go implementation the C entry
points call, in process: the wrappers in `export.go` do nothing but convert
strings, and building a shared library to exercise them would need a C
toolchain wherever the suite runs. `TestNodeLifecycle` runs two nodes against
`signal.NewFakeServer` with loopback-only ICE — no control plane, no STUN, no
packet off 127.0.0.1 — and walks the whole surface: start, status, events,
`set_policy`, metrics, stop.

```bash
cd ghost-go && go test ./cmd/libghost/
```

`go test -race` needs cgo, and so a C toolchain, like any race-detector run
in this repository.

## Notes for the host application

- Keep `key_store_path` in the app's private storage and back it up with the
  credentials: the control plane knows the peer by that WireGuard key.
- Call `ghost_stop` before the process exits. It closes the tunnel, the ICE
  agents and the signalling socket; leaking a handle leaks all three.
- One process can run several nodes at once; handles are independent.
- Everything the node does happens on Go's own threads. A Dart isolate that
  calls `ghost_next_event_json` in a loop with a 1–5 second timeout, and
  forwards each event to the UI isolate, is the shape this API was built for.
