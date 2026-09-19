# ghost signalling protocol, v1

The WebSocket JSON protocol between a ghost member (a `ghost.Node` or
`ghost.Hub`, through `ghost-go/signal`) and `ghost-server`. The Go types live in
`ghost-go/signal/proto`; this document is the normative description.

- **Endpoint:** `GET /v1/signal`, upgraded to WebSocket (`wss://` in production).
- **Frames:** one JSON text message per frame. Binary frames are rejected.
- **Maximum frame size:** 64 KiB (server), 1 MiB (client).

## Envelope

Every frame is an envelope:

```json
{ "v": 1, "type": "offer", "id": "optional-correlation-id", "payload": { } }
```

| Field     | Type   | Meaning                                                     |
| --------- | ------ | ----------------------------------------------------------- |
| `v`       | int    | Protocol version. Always `1`. Other values get `unsupported_version`. |
| `type`    | string | Message type (below).                                       |
| `id`      | string | Optional correlation id.                                    |
| `payload` | object | The type-specific body.                                     |

## Session lifecycle

```
client                                   server
  | -- hello {device_token, role, public_key} -->
  | <-- welcome {device_id, session_id, heartbeat_interval, ice_servers}
  | -- join_network {network, role} ------------>
  | <-- joined {address, pool, hub, peers, policy}
  |            (the server tells the network: peer_online)
  | <== offer / answer / candidate ==> (relayed to and from the hub)
  | -- heartbeat --> / <-- heartbeat
  | <-- policy (whenever the exit policy changes)
  | <-- error {fatal: true} and the socket closes (revoked, moved, …)
```

1. **hello** must be the first frame, within 10 s. The server authenticates
   `device_token` against the device registry.
   - An unknown or revoked token gets `error{code: "unauthorized", fatal: true}`,
     and the socket closes.
   - `device_id`, if sent, must match the token's device.
   - `role`, if sent, must match the device's registered role (`node` or `hub`).
     A device cannot promote itself to hub.
   - `public_key` (a base64 32-byte WireGuard key) is recorded on the device.
2. **welcome** returns the device id, a session id, the heartbeat interval in
   seconds, and `ice_servers`. The ICE servers are the STUN URLs, plus the TURN
   URLs with short-lived credentials (see *TURN credentials*).
3. A newer session for the same device replaces the older one. The older
   socket is closed without a `peer_offline` announcement.

## Messages

| Type                 | Direction | Payload             | Notes |
| -------------------- | --------- | ------------------- | ----- |
| `hello`              | c→s       | `Hello`             | `{version, device_token, device_id?, role?, public_key?}` |
| `welcome`            | s→c       | `Welcome`           | `{version, device_id, session_id, heartbeat_interval, ice_servers[]}` |
| `join_network`       | c→s       | `JoinNetwork`       | `{network, role}`. The network must be the device's own network. |
| `joined`             | s→c       | `Joined`            | `{network, address, pool, hub?, peers[], policy}` |
| `offer`              | both      | `Signal`            | `{network, from, to, ufrag, pwd}` |
| `answer`             | both      | `Signal`            | as for `offer` |
| `candidate`          | both      | `Signal`            | `{network, from, to, candidate{type,address,port,protocol,priority,foundation,related_address?,related_port?}}` |
| `peer_online`        | s→c       | `PeerEvent`         | `{network, peer{device_id, public_key, address, role}}` |
| `peer_offline`       | s→c       | `PeerEvent`         | as for `peer_online` |
| `heartbeat`          | both      | `Heartbeat`         | `{nonce?}`. The server echoes the nonce. |
| `address_assignment` | s→c       | `AddressAssignment` | `{network, address, pool}`. Reserved; the v1 server assigns in `joined`. |
| `policy`             | s→c       | `ExitPolicy`        | The effective exit policy, sent on every change. |
| `error`              | s→c       | `Error`             | `{code, message, fatal?}`. The socket closes after a fatal error. |

### Join and addressing

- On the first join, the server assigns the device the lowest free `/32` in
  its network's pool (by default `100.64.0.0/10`, skipping the network and
  broadcast addresses). The address stays with the device across sessions.
  Moving the device to another network releases it.
- `joined.peers` is the audience the joiner may talk to:
  - for a **node**, the online hubs;
  - for a **hub**, every online member of the network.
- `joined.hub` is set for nodes only: it is the longest-connected online hub.
- Presence follows the same audience:
  - a hub's `peer_online` / `peer_offline` goes to everyone in the network;
  - a node's goes only to the hubs.
- `joined.policy` is the effective exit policy at join time.

### Relay rules

The server rewrites `from` and `network` on every `offer`, `answer` and
`candidate`, then forwards it to `to` only if all of these hold:

- the sender has joined;
- `to` is online and has joined the same network;
- exactly one of the two is a hub (node↔node and hub↔hub are refused);
- access control allows `connect_peer` (always, in open mode).

A failure returns a non-fatal `error` (`not_found`, `forbidden`, `bad_request`)
to the sender.

The hub is the ICE controlling agent. It sends the offer when it learns of a
node, either through `joined.peers` or through `peer_online`.

### Heartbeats

- Clients send `heartbeat` every `welcome.heartbeat_interval` seconds (20 s by
  default). The server echoes each one and updates the device's `last_seen`.
- Any inbound frame counts as liveness. A session silent for the heartbeat
  timeout (60 s by default) is closed, and its peers get `peer_offline`.

### Exit policy

```json
{ "network": "pool", "allow": ["example.com:443", "*.example.org:443"],
  "daily_bytes": 0, "bytes_per_second": 0, "paused": false,
  "labels": {}, "revision": 3 }
```

- `allow` uses the `exit.Allowlist` syntax. An empty list denies everything.
- `revision` rises with every change to the network policy.
- If the access-control authorizer returned a policy for the device at join
  time, that policy replaces the network's allowlist, caps and labels for the
  session. The network's `paused` flag and `revision` still apply.
- Nodes should apply each `policy` message to their `exit.Server`.

### Error codes

| Code                  | Fatal | Meaning |
| --------------------- | ----- | ------- |
| `unsupported_version` | yes   | Envelope or hello version is not 1. |
| `unauthorized`        | yes   | Bad or revoked token, device id mismatch, or role mismatch. |
| `bad_request`         | varies | Malformed frame, or a frame sent out of order. |
| `not_found`           | no    | The relay target is not online in the network. |
| `forbidden`           | no / yes | Access control denied a join or connect (non-fatal). Fatal when the device was moved to another network. |
| `revoked`             | yes   | An admin revoked the device. The socket closes immediately. |
| `internal`            | no    | A server-side failure. |

## TURN credentials

When TURN is configured, `welcome.ice_servers` includes the TURN URLs with
credentials in the coturn REST-API shared-secret scheme:

```
username   = "<expiry unix seconds>:<device_id>"
credential = base64(HMAC-SHA1(turn_secret, username))
```

Set coturn with `use-auth-secret` and `static-auth-secret=<turn_secret>`. The
credentials expire after `ice.turn_ttl` (1 h by default). Reconnecting mints
fresh ones.
