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
- [Development](docs/development.md): building, testing, linting, CI, and
  image publishing.
- [API changes](docs/api-changes.md): breaking changes and removals.

## Repository layout

```
ghost-go/                  library module
  ghost/                   Node, Hub, Config, keys, events, the tunnel netstack
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
docs/                      the documents listed above
.github/                   CI, image publishing, Dependabot
```

## Requirements

Go 1.25 or newer. A peer needs outbound UDP for ICE, and TURN if both ends are
behind symmetric NATs. Neither module needs root or a TUN driver.

## Acknowledgements

- [Pion](https://github.com/pion) for ICE.
- [wireguard-go](https://git.zx2c4.com/wireguard-go/) and its gVisor netstack.
