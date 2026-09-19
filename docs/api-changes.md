# ghost-go API changes (A1)

Phase A1 turns ghost-go from a single point-to-point mobile client into a
generic multi-peer library with a public API. These are **breaking changes**;
no compatibility shims or deprecated aliases were kept.

## Module path

| Old              | New                                       |
| ---------------- | ----------------------------------------- |
| `ghost-go`       | `github.com/Imposter/ghost/ghost-go`      |

Update every import accordingly.

## Vocabulary

Terms are now generic and role-neutral (the current vocabulary, after the
peer control plane below):

- **peer** — an enrolled identity in one network (id, WireGuard key, address,
  roles, tags, labels), authenticated by a peer token.
- **role** — `hub` (a gateway many peers connect to), `node`, `exit`, `relay`,
  assigned by the control plane.
- **network** — a named set of peers sharing an address pool, a policy and an
  isolation mode.

The old "desktop"/"mobile" roles and the `isControlling` flag are gone. The
hub is the controlling ICE agent; nodes are controlled. Roles are derived, not
passed as booleans.

## New public packages

| Package                                        | Purpose                                             |
| ---------------------------------------------- | --------------------------------------------------- |
| `github.com/Imposter/ghost/ghost-go/ghost`     | `Config`, `Keys`, `Node`, `Hub`, `Status`, `Event`. |
| `github.com/Imposter/ghost/ghost-go/signal`    | Versioned WebSocket JSON signalling client + fake.  |
| `github.com/Imposter/ghost/ghost-go/signal/proto` | Shared v1 message types (server reuses these).   |
| `github.com/Imposter/ghost/ghost-go/exit`      | SOCKS5 / HTTP-CONNECT exit with policy + caps.      |

## Removed

| Removed                              | Replacement / note                                                     |
| ------------------------------------ | ---------------------------------------------------------------------- |
| `ghost-go/mobile` (`GhostClient`)    | Removed. Use `ghost.Node` / `ghost.Hub`. FFI/gomobile bindings belong in a later phase over the new API. |
| `ghost-go/cmd/demo`, `cmd/mobile-demo` | Removed. A `ghost-cli` node/hub example lands in A4.                  |
| `ghost-go/tests` (harness, android, ios) | Removed. Replaced by unit + integration tests inside each new package. |
| `mobile-testbed/` (React Native app) | Removed entirely; will not be revived.                                 |
| `scripts/mobile-testbed.py`          | Removed (only served the testbed).                                     |
| Makefile gomobile targets            | Replaced with plain `go build`/`go test` targets.                      |

### Behavioural removals

- **Fixed `10.0.0.1` / `10.0.0.2` addresses** are gone. Addresses are assigned
  by the signalling layer from the network pool (default `100.64.0.0/10`), or
  overridden via `ghost.Config.Address`.
- **Single-peer limit** is gone. A hub runs one ICE agent + one `MultiBind`
  entry per node and one WireGuard peer per node on a single netstack.

## Old JSON shapes → new

The old `mobile` package exchanged ad-hoc JSON blobs
(`SignalingDataJSON`, `CandidateJSON`, `CredentialsJSON`) out-of-band. These
are replaced by the versioned protocol in `signal/proto`, where every frame is
an `Envelope{ v, type, id, payload }`:

| Old (mobile JSON)                     | New (`signal/proto`)                          |
| ------------------------------------- | --------------------------------------------- |
| `CandidateJSON{type,address,port,…}`  | `proto.Candidate{type,address,port,…}` inside a `candidate` message. |
| `CredentialsJSON{ufrag,pwd}`          | `proto.Signal{ufrag,pwd}` inside `offer`/`answer`. |
| `SignalingDataJSON{...}` (bundled)    | Separate `offer` / `answer` / `candidate` messages, plus `hello`/`welcome`, `join_network`/`joined`, `peer_online`/`peer_offline`, `heartbeat`, `address_assignment`, `error`. |

## Migration note (for reviving a testbed)

A future mobile or FFI binding should wrap `ghost.Node` (and `ghost.Hub` for a
gateway), not the old `GhostClient`. It should:

1. Build a `ghost.Config` with the signalling URL, a peer token, the network
   name, and a `KeyStorePath` for persistent keys.
2. Call `Node.Start(ctx)` and watch `Node.Events()` for `joined` and
   `peer_connected`.
3. Use `Node.DialContext` / `Node.Listen` on the tunnel netstack.
4. Reach targets off-mesh with the `exit` package (allowlist-gated).

The signalling wire format is stable and versioned (`proto.Version = 1`), so a
binding only needs to speak the `signal` client protocol.

---

# ghost-go API changes (A3b: strict node metrics)

Additive except where marked **breaking**. No shims were kept.

## New packages

| Package                                         | Purpose |
| ----------------------------------------------- | ------- |
| `github.com/Imposter/ghost/ghost-go/metrics`    | `Collector` (an `exit.Accountant`: ring buffer, bounded aggregates, top-N denied hosts), `NewHandler` (the tunnel-only HTTP endpoint), `Client` (hub-side fetch), and the wire types `Snapshot`, `Connection`, `ConnectionsResponse`, `StatusError`. |
| `internal/bounded`                              | `Set` (first-N label values, overflow `"other"`) and `TopN` (Space-Saving). |

## `ghost`

- `Config.Metrics *MetricsConfig` — when set, the member serves
  `GET /metrics`, `GET /metrics?format=json` and
  `GET /metrics/connections?limit=N` on its tunnel IP (netstack, port
  `metrics.DefaultPort` = 9464) once joined. Only the hub and
  `MetricsConfig.AllowPeers` are admitted; everyone else gets 403.
- `Config.NodeMetricsPort` — the port a Hub fetches node metrics from.
- `Node.Snapshot() metrics.Snapshot` / `Node.RecentConnections(limit)` — pure-Go
  in-process reads for the desktop app's FFI binding (also on `Hub`).
- `Node.PeerForAddr` / `Hub.PeerForAddr` implement `exit.PeerResolver`.
- `NodeMetricsFetcher` interface (`NodeSnapshot`, `NodeConnections`,
  `NodePrometheus` by peer id), implemented by `*Hub`. The egress gateway
  depends on the interface; ghost-server does not proxy node metrics.
- `ErrUnknownPeer`, `ErrNoMetrics`.

## `exit`

- `Config.PeerResolver` / `PeerResolver` interface: `ConnInfo.SourcePeer` is
  now the **peer id** (via the resolver) or its bare tunnel IP.
  **Breaking:** it was `IP:port`, an unbounded label.
- `Config.MaxSourceTags` (default `DefaultMaxSourceTags` = 64): the
  `ghost.source.tag` label admits the first N tags, then `"other"`. The raw tag
  stays in `ConnInfo` and on the span.
- `ConnInfo.PolicyAllowed`: whether the policy permitted the destination
  (true even when the dial later failed).
- `DeniedHost` (`"denied"`) constant; `ConnRing.Cap()`.
- The SOCKS5 non-CONNECT refusal is now a full record (metrics + span), not
  only an `Accountant` call.
- The `Accountant` is now called **after** the metrics and span are recorded.

### Metric changes (instrumentation scope version 0.2.0)

| Instrument                         | Change |
| ---------------------------------- | ------ |
| `ghost.exit.connections`           | `server.address` is the host when the **policy** permitted it (dial errors and timeouts now keep the host; previously they collapsed to `denied`). `server.port` is set only for permitted hosts. `tls.server.name` only when the SNI itself is permitted. |
| `ghost.exit.duration`              | Now also carries `result`. |
| `ghost.exit.cap.used`, `ghost.exit.cap.limit` (By), `ghost.exit.paused` | New async gauges: daily cap usage and pause state. |

Policy denials are `ghost.exit.connections{result="denied"}`; the hosts behind
them are kept only in the collector's capped top-N (`Snapshot.DeniedHosts`)
and the ring buffer, never as labels.

## Snapshot JSON

```json
{
  "time": "…", "peer_id": "…", "address": "100.64.0.2/32",
  "totals": {"connections": 3, "bytes_in": 150, "bytes_out": 15, "active": 0,
             "denied": 1, "cap_used_bytes": 165, "cap_limit_bytes": 0, "paused": false},
  "destinations": [{"host": "api.example", "port": 443, "connections": 2, "bytes_in": 150, "bytes_out": 15}],
  "sources":      [{"peer": "<hub peer id>", "tag": "job=1", "connections": 1, "bytes_in": 100, "bytes_out": 10}],
  "protocols":    [{"protocol": "socks5", "transport": "tcp", "connections": 2, "bytes_in": 150, "bytes_out": 15}],
  "results":      [{"result": "allowed", "count": 2}],
  "denied_hosts": [{"host": "evil.example", "count": 1}],
  "tunnel":       [{"peer_id": "…", "address": "…", "candidate_type": "host", "rtt_seconds": 0.001,
                    "last_handshake": "…", "handshake_age_seconds": 4.2, "rx_bytes": 9000, "tx_bytes": 8000}]
}
```

Every array is present (empty, never `null`). Durations are in seconds.

# A2/A3 changes: ghost-server as a peer control plane

ghost-server is now a peer control plane: networks, peers, enrolment, netmap
distribution, an ACL and exit-policy engine, per-network isolation, health,
audit, and a control API with a watch stream. See
[`control-plane.md`](control-plane.md), [`policy.md`](policy.md) and
[`signalling-v1.md`](signalling-v1.md).

These are **breaking changes**. The word "device" is gone from code, the
database, API paths, JSON and docs. No shims, aliases or deprecated names
were kept. Old clients and old databases must be replaced; there is no data
migration from the device schema.

## Renames

### Go (ghost-go)

| Old | New |
| --- | --- |
| `ghost.Config.DeviceToken`, `DeviceID` | `PeerToken`, `PeerID` |
| `signal.Config.DeviceToken`, `DeviceID`, `Role` | `PeerToken`, `PeerID`, `Roles []proto.Role` (roles the peer expects to hold) |
| `signal.Handlers.OnPeerOnline`, `OnPeerOffline`, `OnPolicy` | `OnNetmap`, `OnNetmapDelta` (policy travels in the netmap) |
| `signal.Event.Peer`, `Event.Address`, `Event.Policy` | `Event.Netmap`, `Event.Delta` |
| `metrics.Snapshot.DeviceID` | `PeerID` |
| `internal/wireguard.Device`, `NewDevice`, `ErrDeviceClosed`, `ErrDeviceNotUp` (file `device.go`) | `Tunnel`, `NewTunnel`, `ErrTunnelClosed`, `ErrTunnelNotUp` (file `tunnel.go`) |

### Wire protocol (`signal/proto`, still v1)

| Old | New |
| --- | --- |
| `hello {device_token, device_id, role}` | `hello {peer_token, peer_id, roles[]}` |
| `welcome {device_id}` | `welcome {peer_id}` |
| `join_network {network, role}` | `join_network {network}` |
| `joined {address, pool, hub, peers[], policy}` | `joined {network, address, pool}`, followed by a `netmap` |
| `PeerInfo {device_id, role}` | `PeerInfo {peer_id, name, public_key, address, roles[], tags[], endpoints[], online}` |
| `peer_online`, `peer_offline` (`PeerEvent`) | removed: `netmap_delta` upserts with `online` |
| `policy` message (`ExitPolicy`) | removed: `netmap.policy` / `netmap_delta.policy` |
| `address_assignment` | removed (the address is in `joined`) |
| `Role` values `node`, `hub` | `hub`, `node`, `exit`, `relay`; a peer holds several |
| — | new `netmap`, `netmap_delta`, `Netmap.Apply`, `PacketFilter`, `Isolation`, `Health`, `LinkHealth`, `ExitHealth`, `Heartbeat.health`, `ErrCodeExpired`, `ErrCodeMoved`, `proto.Controlling` |

### HTTP API

| Old | New |
| --- | --- |
| `POST /v1/register` (self-registration) | removed: `POST /v1/enroll` with a pre-auth key, or `POST /v1/enroll/interactive` + `POST /v1/enroll/poll` |
| `POST /v1/pair` (pairing code) | `POST /v1/enroll` (pre-auth key) |
| `POST /admin/pairing-codes` | `POST /control/networks/{net}/auth-keys` (pre-auth keys), `POST /control/enrollments/{code}/approve` (interactive codes) |
| `/admin/networks[/{name}]` | `/control/networks[/{net}]` (+ `PATCH` for isolation, `DELETE`) |
| `PUT /admin/networks/{name}/policy` (`ExitPolicy`) | `PUT /control/networks/{net}/policy` (policy document), `…/acls`, `…/tags/{tag}`, `…/exit-policies/{name}` |
| `GET /admin/devices`, `/admin/devices/{id}` | `GET /control/peers`, `/control/peers/{id}` |
| `POST /admin/devices/{id}/revoke`, `/move` | `POST /control/peers/{id}/revoke`, `/move` (+ `/expire`, `PATCH`, `DELETE`) |
| `GET /admin/devices/{id}/metrics[/connections]` (501 stub) | removed: detailed metrics are hub-pulled (`ghost.NodeMetricsFetcher`); the server serves `GET /control/peers/{id}/health` and `GET /control/health` |
| `GET /admin/presence`, `/admin/stats` | `GET /control/presence`, `/control/stats` |
| — | new: `POST /control/networks/{net}/peers`, enrolments, `/control/audit`, `/control/watch` (SSE), `/control/api-keys`, `POST /v1/peer/rotate` |

### JSON

| Old | New |
| --- | --- |
| `device_id`, `device_token` | `peer_id`, `peer_token` |
| `DeviceView {…, role}` | `PeerView {…, roles[], tags[], labels, endpoints, ephemeral, status, expires_at, revoked_at, health}` |
| stats `devices`, `revoked_devices` | `peers`, `revoked_peers` |
| authorizer request `device`, `peer`, `role` | `peer`, `target`, `roles[]` (+ `tags[]`) |
| authorizer actions `register`, `pair`, `join_network` | `enroll`, `connect` (`connect_peer` unchanged) |
| — | authorizer `policy.tags` (added at enrolment) |

### Database, configuration and metrics

| Old | New |
| --- | --- |
| table `devices` (one `role`) | `peers` (`roles`, `tags`, `endpoints`, `ephemeral`, `auth_key_id`, `health`, `health_at`, `expires_at`) |
| table `pairing_codes` | `auth_keys`, `enrollments` |
| — | `api_keys`, `audit_events`; `networks.isolation`, `networks.policy` is a policy document |
| `admin.token` / `GHOST_ADMIN_TOKEN` | `control.service_token` / `GHOST_CONTROL_TOKEN` |
| `pairing.ttl` / `GHOST_PAIRING_TTL` | `enrollment.code_ttl` / `GHOST_ENROLLMENT_CODE_TTL` (+ `enrollment.poll_interval`, `peers.ephemeral_grace`, `peers.janitor_interval`) |
| `GHOST_NETWORKS` `name=pool` | also `name=pool=isolation` |
| `ghost_server.registrations`, `ghost_server.pairings` | `ghost_server.enrollments` |
| `ghost_server.devices.revoked` | `ghost_server.peers.revoked` |
| `ghost_server.policy.pushes` | removed (policy travels in netmap deltas) |

## Behaviour a client should know

- **Roles are assigned by the control plane**, from the pre-auth key, the
  enrolment approval or the control API. `hello.roles` only states what the
  client expects: the server rejects the hello if the peer lacks one.
- **Who connects to whom follows the netmap.** `ghost.Node` and `ghost.Hub`
  link to every online peer their netmap lists. The side with the
  higher-ranked role is the ICE controlling agent.
- **The default policy is hub-and-spoke:** `role:hub → *`. A mesh needs an
  ACL; isolation `hub-only` forbids non-hub pairs whatever the ACLs say.
- **Hub-only is enforced on the peer too.** A non-hub member links only to
  hubs, uses a `/32` AllowedIPs per link, never forwards, and admits only hub
  sources on `Listen`, on its exit (`exit.Config.AllowSource = node.IsHubSource`)
  and on `/metrics`.
- **Revoked, expired, deleted or moved** peers get a fatal `revoked`,
  `expired` or `moved` error at once, and every later hello is refused.
