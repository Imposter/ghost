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

Terms are now generic and role-neutral:

- **peer** — any WireGuard endpoint.
- **node** — a member that joins a network and connects to one hub.
- **hub** — a peer that accepts many nodes (a gateway).
- **device** — a *registered identity* only: device id, device token, pairing.
  It is no longer used for tunnel roles.
- **network** — a named set of members sharing an address pool.

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
  entry per node and one WireGuard peer per node on a single netstack device.

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

1. Build a `ghost.Config` with the signalling URL, a device token, the network
   name, and a `KeyStorePath` for persistent keys.
2. Call `Node.Start(ctx)` and watch `Node.Events()` for `joined` and
   `peer_connected`.
3. Use `Node.DialContext` / `Node.Listen` on the tunnel netstack.
4. Reach targets off-mesh with the `exit` package (allowlist-gated).

The signalling wire format is stable and versioned (`proto.Version = 1`), so a
binding only needs to speak the `signal` client protocol.
