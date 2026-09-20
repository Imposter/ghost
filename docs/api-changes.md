# API changes

This file lists breaking changes and removals, newest first. No
compatibility shims or deprecated aliases were kept at any step.

## A status that follows the tunnel

A member reported a peer as linked from the moment WireGuard was configured
for it, whatever ICE then did with the path, so `ghost-cli status` and the
node app's window said `linked`, `online` and a live candidate type over a
connection that had failed.

**New**

- `joined` in `ghost-cli status -json` and in `ghost_status_json`
  (libghost), and a `joined` / `not joined` word in the text status.

**Changed**

- A link counts as up only while its ICE path does: `Status.Peers`,
  `Snapshot().Tunnel` (hence `linked`, `candidate_type`, `rtt_seconds` and
  the byte counters), the `ghost.tunnel.*` metrics and the health a member
  heartbeats all drop a peer as soon as ICE reports the path disconnected,
  failed or closed. A disconnected path is only hidden, since ICE may
  recover it; a failed one also takes the link down, and the next reconcile
  rebuilds it.

## The join is the server's answer, not the client's question

`ghost-go` set its "joined" flag when it sent `join_network`, so a refusal
(a paused node, or an authorizer failing closed during an outage) left the
member connected, out of the network, and silent until its process was
restarted. The join now follows the server's `joined` message and is retried
with backoff for as long as the session is up.

**New**

- `ghost.DefaultJoinRetryMin` (2s) and `ghost.DefaultJoinRetryMax` (30s), the
  bounds of the backoff between join attempts.
- `ghost.Status.Joined`: whether the control plane has this member in its
  network. `Status.SignalState` is the websocket's state and says nothing
  about the join.
- `signal.FakeServer.JoinFunc`: refuse a join in a test (with a non-fatal
  `forbidden` error, as ghost-server does), and allow the retry.

**Changed**

- `ghost.Status.Connected` was "the signalling session is established"; it is
  now "the member is usable": the session is up **and** the network join has
  been accepted. The `connected` field of `ghost_status_json` follows it.
  A caller that wants the old meaning reads `SignalState` / `signal_state`.
- `ghost.Netmap()` returns `false` while the member is not joined, and
  `Status.NetmapPeers` is then 0: a netmap describes the network as of the
  session that carried it, and a member that has lost its join has nothing to
  say about the peers in it. Live links are not torn down for it, because
  WireGuard over ICE outlives a signalling blip.

## libghost, the C shared library

Purely additive: a new `package main` under `ghost-go/cmd/libghost`. No Go
API changed, and nothing outside that directory imports it.

**New**

- `ghost-go/cmd/libghost`, built with `-buildmode=c-shared`, exporting
  `ghost_version`, `ghost_enroll`, `ghost_start`, `ghost_stop`,
  `ghost_status_json`, `ghost_next_event_json`, `ghost_set_policy`,
  `ghost_metrics_json` and `ghost_free`. Nodes live behind `ghost_handle`,
  an integer key into a mutex-guarded table; no Go pointer crosses the
  boundary. See [ffi.md](ffi.md) for the JSON shapes, ownership and
  threading rules.
- `ghost-go/cmd/libghost/ghost.h`, the curated header, and
  `ghost-go/cmd/libghost/build.py`, which builds the library and lays out
  `<out>/include/ghost.h` and `<out>/<os>-<arch>/`.
- `libghost` ABI version `0.1.0` (`ghost_version`). It tracks the C API and
  the JSON shapes, not the library behind them.

**Notes**

- The C API adds one policy rule that has no Go equivalent: a host
  application's local exit policy never widens the control plane's. A
  destination must pass both allowlists, the effective cap is the smaller of
  the two that are set, and the exit is paused when either side pauses it —
  so a policy push cannot un-pause an exit its owner paused.
- `ghost_start` refuses unknown JSON fields, so a misspelt `key_store_path`
  is an error rather than an ephemeral WireGuard key.

## ghost-cli, netmap labels, roles and structured source tags

Mostly additive. The breaking changes are the exit and metrics renames for
structured source tags (below); nothing else changes for existing callers.

**New**

- `ghost-go/cmd/ghost-cli`, a command-line member (`enroll`, `node`, `hub`
  with `dial`/`curl`/`metrics`, `p2p invite`/`accept`, `status`), its
  Dockerfile, and [`examples/compose`](../examples/compose).
- `exit.DefaultPort` (1080), the conventional exit port, and
  `exit.DefaultSourceHeader` (`X-Ghost-Source`).
- `ghost.Config.Roles`: roles a member asks to hold. They are sent in the
  hello, which the control plane refuses unless the peer holds them all.
  `NewHub` adds `hub`. An unknown role fails `NewNode`/`NewHub`.
- `signal.FakeServer.AddPeer(token, signal.FakePeer{ID, Name, Roles, Tags,
  Labels})`: a registered peer gets its registered roles (not the hello's),
  a hello asking for a role it lacks is refused with `unauthorized`, and
  netmaps carry its name, tags and labels. Unregistered tokens behave as
  before.

**Wire protocol** (still v1, additive)

- `PeerInfo.labels` (`proto.PeerInfo.Labels`): a netmap's `self` and
  `peers` carry each peer's control-plane labels; absent when a peer has
  none. A label change is pushed as a netmap delta.

**Structured source tags** (see [architecture.md](architecture.md#source-tags))

A tag is parsed as `source=<s>&job=<j>`; metrics are labelled by the source
only, and the job stays in the connection record and on the span.

| Old | New |
| --- | --- |
| `exit.Config.MaxSourceTags` | `exit.Config.MaxSources` |
| `exit.DefaultMaxSourceTags` | `exit.DefaultMaxSources` |
| metric attribute `ghost.source.tag` (the whole tag, bounded) | `ghost.source.name` (the source part only, bounded); Prometheus `ghost_source_tag` becomes `ghost_source_name` |
| `metrics.SourceStat.Tag` (JSON `sources[].tag`) | `metrics.SourceStat.Source` (JSON `sources[].source`); sources aggregate by peer and source name, never by job |
| — | `exit.SourceTag{Source, Job}`, `exit.ParseSourceTag`, `SourceTag.String`, `exit.SourceLabel`, `exit.SourceKey`, `exit.JobKey`, `exit.MaxSourceLen`, `exit.OverflowSource` |
| — | `exit.ConnInfo.Source`, `.Job`; `metrics.Connection.Source`, `.Job` (JSON `source`, `job`) next to the raw `source_tag` |
| span attribute `ghost.source.tag` | unchanged (the raw tag), plus `ghost.source.name` and `ghost.source.job` |

A tag without `=` is a whole source name, so a bare tag labels the same way
as before, as long as it is a valid label. A tag such as `job=42`, which used
to be its own series, now has no source label. The exit's instrumentation
scope version is `0.3.0`.

## Authorizer request context and on-demand re-checks

Additive: existing authorizers and control API clients keep working. One
audit value changed (last bullet).

**Authorizer protocol** (see [policy.md](policy.md#request))

- Every request (`enroll`, `connect`, `connect_peer`) carries the acting
  peer's WireGuard `public_key` when one is known, and its
  `enrollment_method`: `auth_key` (with `auth_key_id`), `interactive` or
  `direct`.
- The decision cache is also keyed on these fields.

**Storage**

- `peers.enrollment_method` (migration `0003_peer_enrollment_method.sql`).
  Existing peers are backfilled where the method is recoverable: a pre-auth
  key id means `auth_key`, and a claimed enrolment still naming the peer
  means `interactive`. Direct creations and interactive peers whose
  enrolment was already pruned stay unknown, and their requests omit
  `enrollment_method`.

**HTTP API**

- `POST /control/peers/{id}/reauthorize` and
  `POST /control/networks/{net}/reauthorize` (`peers:write`) re-ask the
  authorizer about live sessions and disconnect the denied. `409` in `open`
  mode.
- `PeerView` has `enrollment_method` and `auth_key_id`.
- New audit actions `peer.reauthorized` and `network.reauthorized`.
  `peer.connect_denied` from a re-check carries `on: "policy_change"` or
  `on: "reauthorize"`.
- `via`, in the `peer.enrolled` and `peer.enroll_denied` audit entries and
  in the `peer.enrolled` watch event, is now `direct` (was `control`) for
  peers created through the control API, matching `enrollment_method`.

**ghost-server (Go)**

| Old | New |
| --- | --- |
| — | `access.Request.PublicKey`, `.EnrollmentMethod`, `.AuthKeyID`; `access.EnrollmentMethod` (`EnrollAuthKey`, `EnrollInteractive`, `EnrollDirect`) |
| — | `access.Controller.Consulted()` |
| — | `store.Peer.EnrollmentMethod` |
| — | `control.Service.ReauthorizePeer`, `control.Service.ReauthorizeNetwork`, `control.Reauthorization`, `control.ErrAuthorizerOpen` (`409`) |
| `control.Sessions` | adds `Reauthorize(ctx, peerID)` and `ReauthorizeNetwork(ctx, network)`; `signalling.Relay` implements both |

## Per-network interactive enrolment switch

Networks gain `interactive_enrollment` (default `true`). Existing networks
are migrated to `true` (migration `0002_network_interactive_enrollment.sql`,
shared by SQLite and PostgreSQL), so behaviour is unchanged until it is
turned off. See
[control-plane.md](control-plane.md#turning-interactive-enrolment-off).

**HTTP API**

- `POST /control/networks` accepts `interactive_enrollment`.
- `PATCH /control/networks/{net}` takes `{isolation?, interactive_enrollment?}`.
  Both fields are optional, but at least one is required: an empty body is
  now `400` (it used to fail isolation validation, also `400`). Changing only
  `interactive_enrollment` does not bump the policy revision.
- `NetworkView` has `interactive_enrollment`.
- While the switch is off, `POST /v1/enroll/interactive`,
  `POST /control/enrollments/{code}/approve` and claiming an approved
  enrolment through `POST /v1/enroll/poll` answer `403`.
- New audit action `network.interactive_enrollment`. `network.updated`
  events carry `interactive_enrollment` when it changes.

**ghost-server (Go)**

| Old | New |
| --- | --- |
| `control.Service.SetIsolation(ctx, net, iso)` | `control.Service.UpdateNetwork(ctx, net, control.NetworkPatch{Isolation: &iso})` |
| `store.Store.SetNetworkIsolation(ctx, net, iso)` | `store.Store.UpdateNetwork(ctx, net, store.NetworkUpdate{Isolation: &iso})` |
| — | `store.Network.InteractiveEnrollment`, `control.NetworkInput.InteractiveEnrollment` (`*bool`) |
| — | `control.ErrForbidden` (`403`), `control.ErrInteractiveEnrollmentDisabled` |

## Peer to peer without the control plane

Additions only; existing code keeps working unchanged.

- `ghost.Signaller`, `ghost.SignalSelf` and `ghost.LinkPlan`: the seam between
  a member and its signalling. `ghost.Config.Signaller` selects one; nil keeps
  the control-plane client.
- New package `ghost/direct`: `New`, `Config`, `StaticPeer`, `Signaller`
  (`CreateInvite`, `AcceptInvite`, `AcceptAnswer`, `ID`), `Token`,
  `ParseToken`, `Exchanger`, `Invite`, `Answer` and `Pipe`.
- A member now links to a netmap peer listed without a public key, and
  configures WireGuard once the key arrives. The control plane never lists
  such peers.

## Repository cleanup

Everything removed here was internal or unused, so the public API is
unchanged.

- `internal/ice`: removed `ICEBind`, `NewICEBind`, `ICEEndpoint`,
  `NewICEEndpoint`, `ReceiveChannelBufferSize`, `MaxUDPPacketSize` (now
  unexported), `TestICEConfigWithSTUN`, `ErrGatherTimeout`,
  `ErrConnectionFailed`, `DefaultSTUNPort` and `DefaultTURNPort`. `ghost`
  uses `MultiBind`.
- `internal/wireguard`: removed `CreateTUN`, `CreateTUNFromFD` and the per-OS
  TUN names (ghost runs on the userspace netstack), plus `MinPort`,
  `ErrInvalidKey`, `ErrTunnelNotUp`, `ErrTUNCreationFailed` and
  `IPCFieldRemove`.
- `internal/testutil`: removed `MockSignalingChannel.WaitForCandidates`,
  `MockSignalingChannel.Reset` and `Candidate.Copy`.
- The `ghost-server` Docker image is based on Alpine instead of distroless,
  and runs as `ghost` (uid 65532) with a healthcheck.
- `ghost-go/Makefile` was replaced by a root `Makefile` covering both modules.

## Peer control plane (A2/A3)

The word "device" is gone from code, the database, API paths, JSON and docs.
Old clients and databases must be replaced; there is no data migration.

**ghost-go**

| Old | New |
| --- | --- |
| `ghost.Config.DeviceToken`, `DeviceID` | `PeerToken`, `PeerID` |
| `signal.Config.DeviceToken`, `DeviceID`, `Role` | `PeerToken`, `PeerID`, `Roles []proto.Role` |
| `signal.Handlers.OnPeerOnline`, `OnPeerOffline`, `OnPolicy` | `OnNetmap`, `OnNetmapDelta` |
| `signal.Event.Peer`, `Address`, `Policy` | `Event.Netmap`, `Event.Delta` |
| `metrics.Snapshot.DeviceID` | `PeerID` |
| `internal/wireguard.Device`, `NewDevice`, `ErrDeviceClosed` | `Tunnel`, `NewTunnel`, `ErrTunnelClosed` |

**Wire protocol** (`signal/proto`, still v1)

| Old | New |
| --- | --- |
| `hello {device_token, device_id, role}` | `hello {peer_token, peer_id, roles[]}` |
| `welcome {device_id}` | `welcome {peer_id}` |
| `join_network {network, role}` | `join_network {network}` |
| `joined {address, pool, hub, peers[], policy}` | `joined {network, address, pool}`, then a `netmap` |
| `peer_online`, `peer_offline`, `policy`, `address_assignment` | removed: `netmap` and `netmap_delta` carry peers, online state and policy |
| roles `node`, `hub` (one per peer) | `hub`, `node`, `exit`, `relay` (several per peer) |

**HTTP API**

| Old | New |
| --- | --- |
| `POST /v1/register`, `POST /v1/pair` | `POST /v1/enroll` (pre-auth key), `POST /v1/enroll/interactive` + `/v1/enroll/poll` |
| `POST /admin/pairing-codes` | `POST /control/networks/{net}/auth-keys`, `POST /control/enrollments/{code}/approve` |
| `/admin/networks[/{name}]` | `/control/networks[/{net}]` |
| `PUT /admin/networks/{name}/policy` (`ExitPolicy`) | `PUT /control/networks/{net}/policy` (a policy document), plus `…/acls`, `…/tags/{tag}`, `…/exit-policies/{name}` |
| `/admin/devices[/{id}]`, `…/revoke`, `…/move` | `/control/peers[/{id}]`, `…/revoke`, `…/move`, `…/expire` |
| `GET /admin/devices/{id}/metrics[/connections]` | removed: hubs pull node metrics (`ghost.NodeMetricsFetcher`); the server serves `GET /control/peers/{id}/health` and `GET /control/health` |
| `/admin/presence`, `/admin/stats` | `/control/presence`, `/control/stats` |

**JSON, database, configuration, metrics**

| Old | New |
| --- | --- |
| `device_id`, `device_token` | `peer_id`, `peer_token` |
| `DeviceView {…, role}` | `PeerView {…, roles[], tags[], labels, endpoints, status, …}` |
| stats `devices`, `revoked_devices` | `peers`, `revoked_peers` |
| authorizer `device`, `peer`, `role`; actions `register`, `pair`, `join_network` | `peer`, `target`, `roles[]`, `tags[]`; actions `enroll`, `connect` |
| tables `devices`, `pairing_codes` | `peers`, `auth_keys`, `enrollments` (plus `api_keys`, `audit_events`) |
| `admin.token` / `GHOST_ADMIN_TOKEN` | `control.service_token` / `GHOST_CONTROL_TOKEN` |
| `pairing.ttl` / `GHOST_PAIRING_TTL` | `enrollment.code_ttl` / `GHOST_ENROLLMENT_CODE_TTL` |
| `ghost_server.registrations`, `ghost_server.pairings` | `ghost_server.enrollments` |
| `ghost_server.devices.revoked` | `ghost_server.peers.revoked` |
| `ghost_server.policy.pushes` | removed |

## Strict node metrics (A3b)

- `exit.ConnInfo.SourcePeer` is now the peer id, or the bare tunnel IP when
  the peer is unknown. It used to be `IP:port`.
- `ghost.exit.connections`: `server.address` keeps the host whenever the
  policy permitted it, including on dial errors. `server.port` is set only
  for permitted hosts.
- The exit's `Accountant` is called after the metrics and span are recorded.
- New: the `metrics` package, `ghost.Config.Metrics`, `NodeMetricsPort`,
  `Node.Snapshot`, `Node.RecentConnections`, `PeerForAddr`,
  `NodeMetricsFetcher`, and the gauges `ghost.exit.cap.used`,
  `ghost.exit.cap.limit` and `ghost.exit.paused`.

## Generic multi-peer library (A1)

- The module path is `github.com/Imposter/ghost/ghost-go`.
- Removed `ghost-go/mobile` (`GhostClient`); use `ghost.Node` or `ghost.Hub`.
- Removed `cmd/demo`, `cmd/mobile-demo`, the `tests` harnesses, the
  `mobile-testbed/` app, `scripts/mobile-testbed.py`, and the gomobile
  Makefile targets.
- The desktop/mobile roles and `isControlling` are gone. The ICE controlling
  side is derived from roles.
- Addresses come from the network pool (default `100.64.0.0/10`), not from
  fixed `10.0.0.1`/`10.0.0.2`. One member links to many peers.
- The ad hoc JSON (`SignalingDataJSON`, `CandidateJSON`, `CredentialsJSON`)
  was replaced by the versioned `signal/proto` envelope
  `{v, type, id, payload}`.
