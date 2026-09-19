# API changes

This file lists breaking changes and removals, newest first. No
compatibility shims or deprecated aliases were kept at any step.

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
