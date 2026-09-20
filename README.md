# ghost

ghost builds private peer-to-peer networks. Peers find each other through a
control plane, connect directly over ICE (STUN, with TURN as a fallback), and
run WireGuard end to end on a userspace netstack. The control plane decides
who may reach whom, but it never carries tunnel traffic.

The repository holds two Go modules:

- **`ghost-go`** is the library (`github.com/Imposter/ghost/ghost-go`). A
  `ghost.Node` or `ghost.Hub` joins a network, links to the peers in its
  netmap, and exposes `DialContext` and `Listen` on its tunnel address. It
  needs no TUN device and no root. The `exit` package adds an allowlisted
  SOCKS5/HTTP-CONNECT exit.
- **`ghost-server`** is the control plane. It manages networks, peers and
  enrolment, pushes netmaps over the signalling WebSocket, enforces policy and
  isolation, stores health and audit records, and serves a control API with a
  watch stream.

## Quick start

Run the control plane. The image is built from the repository root:

```bash
docker build -f ghost-server/Dockerfile -t ghost-server .
docker run -p 8080:8080 -v ghost-data:/var/lib/ghost \
  -e GHOST_CONTROL_TOKEN=change-me-to-16-bytes-or-more \
  -e GHOST_NETWORKS=lab=100.64.0.0/10=hub-only \
  -e GHOST_STUN_URLS=stun:stun.l.google.com:19302 \
  ghost-server
```

Create a pre-auth key for the network, then enrol a peer with it:

```bash
curl -s -H "Authorization: Bearer $TOKEN" -X POST \
  http://localhost:8080/control/networks/lab/auth-keys \
  -d '{"roles":["node"],"reusable":true}'            # -> {"key":"gak_…", …}

curl -s -X POST http://localhost:8080/v1/enroll \
  -d '{"auth_key":"gak_…","name":"node-1"}'          # -> {"peer_id":"peer_…","peer_token":"gpt_…", …}
```

Join the network from Go with the peer token. By default a network is
hub-and-spoke, so a node links to the hubs in its netmap:

```go
node, err := ghost.NewNode(ghost.Config{
    SignalURL:    "ws://localhost:8080/v1/signal", // wss:// in production
    PeerToken:    peerToken,
    Network:      "lab",
    KeyStorePath: "/var/lib/ghost/keys.json", // WireGuard key; empty = ephemeral
})
if err != nil {
    log.Fatal(err)
}
if err := node.Start(ctx); err != nil {
    log.Fatal(err)
}
defer node.Close()

for ev := range node.Events() {
    if ev.Kind == ghost.EventPeerConnected {
        conn, err := node.DialContext(ctx, "tcp", "100.64.0.1:8080") // a hub's tunnel address
        // ...
    }
}
```

A hub is the same thing built with `ghost.NewHub`. It needs a peer that holds
the `hub` role, which you can create with `POST /control/networks/{net}/peers`.
A node that serves an exit sets `Config.Roles: []proto.Role{proto.RoleExit}`,
so the server refuses the session unless the peer was enrolled with the exit
role.

## ghost-cli

[`ghost-cli`](ghost-go/cmd/ghost-cli) is a small command-line member built on
the same API, for trying ghost out and for tests:

```bash
(cd ghost-go && go install ./cmd/ghost-cli)

ghost-cli enroll -server http://localhost:8080 -auth-key gak_… -name node-1   # writes ghost-creds.json
ghost-cli node -exit -metrics            # an exit on the tunnel IP, port 1080 (exit.DefaultPort)
ghost-cli hub -forward 127.0.0.1:1080=node-1:1080          # a host port to the node's exit
ghost-cli hub curl -source demo -job 1 node-1 https://example.com/
ghost-cli hub metrics node-1             # the node's in-tunnel metrics
ghost-cli p2p invite / ghost-cli p2p accept TOKEN            # no server at all
ghost-cli status                         # a running member's netmap, tunnel and exit
```

`node -exit` applies the network's exit policy (and `-allow` narrows it),
and serves only hubs. [`examples/compose`](examples/compose) runs
ghost-server, coturn, a hub and an exit node with it.

## Using ghost without the control plane

The control plane is optional. Set `ghost.Config.Signaller` to a
`ghost/direct` signaller and two members connect with no server:

- **Tokens.** One side calls `CreateInvite`, the other `AcceptInvite` (which
  returns an answer), then the first calls `AcceptAnswer`. The tokens are
  versioned base64url JSON holding only public material, and can travel by
  copy and paste, a QR code, or your own `direct.Exchanger`. ICE and
  WireGuard then run directly between the peers; STUN and TURN are optional.
- **Static peers.** `direct.Config.Static` lists peers by public key, tunnel
  address and endpoint, for plain WireGuard without ICE when one side has a
  fixed, reachable endpoint.

There is no netmap policy, isolation or health reporting in this mode. See
[docs/p2p.md](docs/p2p.md) and [`examples/p2p`](ghost-go/examples/p2p).

## Embedding ghost in another language

[`libghost`](ghost-go/cmd/libghost) builds the library as a C shared object,
so an application that is not written in Go — a Flutter app through Dart FFI,
for example — can run a node in its own process. It is the same thin wrapper
`ghost-cli` is: a node behind an integer handle, UTF-8 JSON in and out, and
no Go pointer across the boundary.

```c
ghost_handle h = ghost_start(config_json, &err);   /* creds, exit, caps, metrics */
char *ev = ghost_next_event_json(h, 1000);         /* one event per call */
char *st = ghost_status_json(h);                   /* tunnel address, peers, candidate type */
ghost_set_policy(h, "{\"paused\":true}", &err);    /* allowlist, caps, pause */
ghost_stop(h);
```

```bash
cd ghost-go/cmd/libghost && python3 build.py --docker --os linux
```

See [docs/ffi.md](docs/ffi.md) for the header, the JSON shapes, the ownership
and threading rules, and the toolchains each target needs.

## Glossary

- **peer**: an enrolled identity in one network. It has an id (`peer_…`), a
  WireGuard key, an address from the pool, roles, tags and labels, and
  authenticates with a peer token (`gpt_…`).
- **network**: a named set of peers that share an address pool (default
  `100.64.0.0/10`), a policy document and an isolation mode.
- **role**: a capability that the control plane assigns: `hub` (a gateway
  many peers connect to), `node` (an ordinary member), `exit` (runs an
  allowlisted exit) or `relay` (reserved).
- **hub / node**: a peer that holds that role. `ghost.Hub` and `ghost.Node`
  are the library types.
- **netmap**: the peers a peer may reach, plus its exit policy and inbound
  packet filter. The control plane sends a snapshot, then deltas.
- **policy**: a network's tags, ACL rules and exit policies
  ([docs/policy.md](docs/policy.md)).
- **isolation**: `none` (the ACLs decide) or `hub-only` (peers without the hub
  role only ever see hubs).
- **exit**: a SOCKS5/HTTP-CONNECT proxy on a peer's tunnel address. It dials
  allowlisted destinations through the host's own network.
- **control plane**: `ghost-server` ([docs/control-plane.md](docs/control-plane.md)).

## Documentation

- [Architecture](docs/architecture.md): the components, a connection's
  lifecycle, and metrics.
- [Control plane](docs/control-plane.md): the model, enrolment, the control
  and peer APIs, configuration, and running the server.
- [Policy](docs/policy.md): ACLs, exit policies, isolation, and the external
  authorizer.
- [Signalling v1](docs/signalling-v1.md): the WebSocket protocol between peers
  and the control plane.
- [Peer to peer](docs/p2p.md): using the library without the control plane.
- [C API](docs/ffi.md): `libghost`, the C shared library for Dart FFI and
  other non-Go hosts.
- [Development](docs/development.md): building, testing, linting, CI, and
  image publishing.
- [API changes](docs/api-changes.md): breaking changes and removals.

## Repository layout

```
ghost-go/                  library module
  ghost/                   Node, Hub, Config, Signaller, keys, events, the tunnel netstack
  ghost/direct/            standalone Signaller: invite/answer tokens, static peers
  cmd/ghost-cli/           command-line member: enroll, node, hub, p2p, status (and its Dockerfile)
  cmd/libghost/            C shared library (-buildmode=c-shared): ghost.h, build.py
  examples/p2p/            two members linked by pasted tokens
  exit/                    SOCKS5 / HTTP-CONNECT exit: allowlist, caps, accounting
  metrics/                 a node's strict metrics: collector, handler, hub-side client
  signal/                  signalling client (and an in-memory fake server)
  signal/proto/            v1 wire types shared with ghost-server
  otelsetup/               OpenTelemetry SDK and Prometheus wiring for binaries
  internal/ice/            Pion ICE agent and the WireGuard MultiBind
  internal/wireguard/      wireguard-go tunnel, keys, and the userspace netstack
  internal/bounded/        cardinality-bounded sets and a top-N counter
  internal/nettest/        loopback-only ICE config for tests
  internal/testutil/       test loggers and in-memory ICE signalling
ghost-server/              control plane module (imports ghost-go via replace)
  cmd/ghost-server/        the binary
  server/...               config, store, control, policy, access, signalling, httpapi, ...
  Dockerfile
examples/compose/          ghost-server, coturn, a hub and an exit node in Docker Compose
docs/                      the documents listed above
.github/                   CI, image publishing, Dependabot
```

## Requirements

Go 1.25 or newer. A peer needs outbound UDP for ICE, and TURN if both ends are
behind symmetric NATs. Neither module needs root or a TUN driver.

## Acknowledgements

- [Pion](https://github.com/pion) for ICE.
- [wireguard-go](https://git.zx2c4.com/wireguard-go/) and its gVisor netstack.
