# Architecture

ghost has two parts. **`ghost-server`** is the control plane: it knows the
networks, the peers and the policy. **`ghost-go`** is the library that peers
run. Peers talk to the control plane over one WebSocket each, and to each
other directly over ICE and WireGuard. Tunnel traffic never passes through
the control plane.

```
                       ghost-server (control plane)
         networks · peers · enrolment · policy · health · audit
         control API (/control, SSE watch) · peer API (/v1)
                 ▲                                ▲
   signalling v1 │ WebSocket                      │ WebSocket
 (netmaps, offers,│ answers, candidates,          │ heartbeats)
                 │                                │
        ┌────────┴────────┐    ICE (UDP)    ┌─────┴───────────┐
        │  ghost.Hub      │◄───────────────►│  ghost.Node     │
        │  WireGuard      │  WireGuard e2e  │  WireGuard      │
        │  netstack       │                 │  netstack       │
        └─────────────────┘                 │  exit (optional)│──► internet
                                            └─────────────────┘   (allowlisted)
```

## The library (`ghost-go`)

A member (`ghost.Node` or `ghost.Hub`; both wrap the same mesh) is built from
these layers:

| Layer | Package | Role |
| ----- | ------- | ---- |
| API | `ghost` | `Config`, `NewNode`/`NewHub`, `Start`, `Events`, `Status`, `Netmap`, `Policy`, `DialContext`, `Listen`, `IsHubSource`, `Snapshot`. |
| Signalling | `signal`, `signal/proto` | A reconnecting WebSocket client for [signalling v1](signalling-v1.md), with exponential backoff. `signal.FakeServer` is an in-memory server for tests. |
| NAT traversal | `internal/ice` | One Pion ICE agent per linked peer. `MultiBind` is a single WireGuard `conn.Bind` that multiplexes every per-peer ICE connection under an opaque endpoint key, so a connection can be swapped (after an ICE restart, say) without touching WireGuard. |
| Encryption | `internal/wireguard` | wireguard-go with one WireGuard peer per link. Each link's AllowedIPs is the other peer's own `/32`. |
| Network stack | `internal/wireguard` | A gVisor userspace netstack (`CreateNetTUN`). It accepts only packets addressed to its own tunnel address and never forwards; drops are counted in `ForwardDrops`. No OS TUN device or privileges are needed. |
| Exit | `exit` | An optional SOCKS5 (CONNECT only) and HTTP-CONNECT proxy served on the member's tunnel address. It dials allowlisted destinations through the host network, refuses loopback and private destinations, and enforces a daily byte cap, a rate limit and a pause switch. |
| Metrics | `metrics`, `otelsetup` | See [Metrics](#metrics). The libraries use only the OpenTelemetry API; `otelsetup` wires the SDK, the Prometheus exporter and optional OTLP for binaries. |

### A member's lifecycle

1. **Start.** Load or create the WireGuard key pair (`KeyStorePath`), dial the
   signalling URL, and send `hello` with the peer token and public key.
   `NewHub` also sends `roles: ["hub"]`, so the server refuses the session if
   the peer doesn't hold the hub role.
2. **Join.** After `welcome`, which carries the ICE servers and any TURN
   credentials, send `join_network`. `joined` returns the peer's `/32` from
   the pool. The member then builds the netstack and the WireGuard device on a
   `MultiBind`.
3. **Netmap.** The server sends a netmap snapshot and then deltas. On every
   change the member reconciles its links against the netmap. It links to
   every online peer listed, tears down links to peers that have left, and
   emits `EventNetmap`, plus `EventPolicy` when its exit policy changed.
4. **Link.** For each pair, the peer with the higher-ranked role (`hub` >
   `relay` > `exit` > `node`, with ties going to the smaller peer id) is the
   ICE controlling agent and sends the offer. Both sides trickle candidates
   through the server. When ICE connects, the connection is registered in the
   `MultiBind` and the WireGuard peer is configured. The member then emits
   `EventPeerConnected`.
5. **Traffic.** `DialContext` and `Listen` run on the netstack. `Listen`
   applies the netmap's packet filter: a connection from a source the ACLs
   don't admit on that port is closed before `Accept` returns it.
6. **Health.** The member sends a heartbeat with a health summary (per-link
   state, candidate type, RTT, handshake age and bytes; exit cap usage; its
   endpoints) on the server's interval, and immediately when a link changes.
7. **Revocation.** The server ends the session with a fatal `revoked`,
   `expired` or `moved` error, and refuses every later `hello`. The member
   reports the error as `EventError`. Its signalling client keeps retrying
   with backoff, so an application that sees one of these errors should
   `Close` the member.

### Enforcement on the peer

A member doesn't rely on the server alone. It links only to online netmap
peers. Under `hub-only` isolation, a non-hub member configures WireGuard only
for hubs and refuses offers from anyone else (`RefusedLinks`). Its `Listen`,
its exit (`exit.Config.AllowSource = node.IsHubSource`) and its metrics
endpoint admit only hub sources. [Policy](policy.md#isolation) lists all five
enforcement layers.

## The control plane (`ghost-server`)

```
ghost-server/
  cmd/ghost-server/   the binary: config, OTel SDK, HTTP listeners
  server/             assembly (server.New), used by the binary and the tests
  server/config/      JSON file + GHOST_* environment loading
  server/store/       database/sql store for SQLite and PostgreSQL, embedded migrations
  server/control/     networks, peers, enrolment, rotation, expiry, API keys, audit, janitor
  server/policy/      policy documents: tags, ACLs, exit policies, isolation
  server/access/      the optional external authorizer: signing, cache, Verifier
  server/events/      the in-process event bus behind the watch stream
  server/signalling/  WebSocket sessions, netmaps and deltas, the signal relay, health
  server/httpapi/     the peer API, the control API, and the SSE watch stream
  server/ipam/        pool address allocation
  server/turn/        coturn REST-API TURN credentials
  server/telemetry/   OpenTelemetry instruments (API only)
```

Every mutation goes through `control`. It writes the store and the audit log,
publishes an event on the bus, and asks the signalling relay to push netmap
deltas to the live peers that can see the change. Revoking, expiring,
deleting or moving a peer closes its session at once. The relay forwards an
offer, answer or candidate only between peers that are in each other's
netmap. See [the control plane](control-plane.md) for the API and
configuration.

`ghost-server` imports `ghost-go` for `signal/proto` (the wire types) and
`otelsetup`. It uses a `replace ../ghost-go` directive, which is why the
image is built from the repository root.

## Metrics

### Node metrics (strict, in-tunnel)

A member that sets `Config.Metrics` serves its metrics on its **tunnel
address only**. The endpoint is a netstack listener on port
`metrics.DefaultPort` (9464), so no OS port is bound. Only the hubs in its
netmap, and any peer ids in `MetricsConfig.AllowPeers`, may read it
(`AllowPeers` is ignored under `hub-only`). Everyone else gets `403`.

| Endpoint | Body |
| -------- | ---- |
| `GET /metrics` | Prometheus text (from `otelsetup`) |
| `GET /metrics?format=json` | `metrics.Snapshot` |
| `GET /metrics/connections?limit=N` | `metrics.ConnectionsResponse`, newest first |

```go
setup, _ := otelsetup.New(ctx, otelsetup.Options{ServiceName: "ghost-node"})
col := metrics.NewCollector(metrics.CollectorConfig{}) // ring buffer + bounded aggregates
node, _ := ghost.NewNode(ghost.Config{ /* … */
    MeterProvider: setup.MeterProvider,
    Metrics:       &ghost.MetricsConfig{Collector: col, Prometheus: setup.PrometheusHandler},
})
ex := exit.New(exit.Config{Policy: allow, Accountant: col, PeerResolver: node,
    AllowSource: node.IsHubSource, MeterProvider: setup.MeterProvider})
col.AttachExit(ex)
```

A `metrics.Snapshot` holds totals, per-destination, per-source, per-protocol
and per-result aggregates, the top denied hosts, and per-peer tunnel stats.
Every array is present (possibly empty) and durations are in seconds. An
in-process reader gets the same snapshot from `Node.Snapshot()` and
`Node.RecentConnections(n)`.

A hub reads its nodes over the tunnel. `*ghost.Hub` implements
`ghost.NodeMetricsFetcher` (`NodeSnapshot`, `NodeConnections` and
`NodePrometheus`, each by peer id). The control plane doesn't proxy these. It
keeps only the health summaries from heartbeats
(`GET /control/peers/{id}/health`, `GET /control/health`).

### Instruments

The label sets are bounded. Source tags beyond `exit.Config.MaxSourceTags`
become `"other"`, and denied hosts appear only in the snapshot's top-N list,
never as labels.

| Scope | Instruments |
| ----- | ----------- |
| `ghost` (a member) | `ghost.tunnel.rtt`, `ghost.tunnel.handshake_age`, `ghost.tunnel.rx_bytes`, `ghost.tunnel.tx_bytes`, `ghost.tunnel.peers` |
| `exit` | `ghost.exit.connections{result,…}`, `ghost.exit.bytes`, `ghost.exit.duration`, `ghost.exit.ttfb`, `ghost.exit.active_connections`, `ghost.exit.cap.used`, `ghost.exit.cap.limit`, `ghost.exit.paused` |
| `ghost-server` | `ghost_server.sessions.active{role}`, `ghost_server.signal.messages{type,direction}`, `ghost_server.authz.decisions{action,result,cached}`, `ghost_server.authz.duration`, `ghost_server.enrollments{result}`, `ghost_server.peers.revoked`, `ghost_server.heartbeat.timeouts` |

`ghost-server` serves Prometheus at `/metrics`, either on its main listener or
on `metrics_listen`, and adds OTLP export when `otlp` is on.
