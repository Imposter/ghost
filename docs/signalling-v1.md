# ghost signalling protocol, v1

This is the WebSocket JSON protocol between a ghost peer (a `ghost.Node` or
`ghost.Hub`, through `ghost-go/signal`) and the `ghost-server` control plane.
The Go types live in `ghost-go/signal/proto`. This document is the normative
description.

- **Endpoint:** `GET /v1/signal`, upgraded to WebSocket (`wss://` in production).
- **Frames:** one JSON text message per frame. Binary frames are rejected.
- **Maximum frame size:** 64 KiB (server), 1 MiB (client).

## Envelope

```json
{ "v": 1, "type": "offer", "id": "optional-correlation-id", "payload": { } }
```

| Field     | Meaning |
| --------- | ------- |
| `v`       | Protocol version, always `1`. Anything else gets `unsupported_version`. |
| `type`    | Message type (below). |
| `id`      | Optional correlation id. |
| `payload` | The type-specific body. |

## Session lifecycle

```
peer                                         server
  | -- hello {peer_token, public_key, roles?} ---->
  | <-- welcome {peer_id, session_id, heartbeat_interval, ice_servers}
  | -- join_network {network} -------------------->
  | <-- joined {network, address, pool}
  | <-- netmap {seq, isolation, self, peers[], policy, filter}
  |            (peers that can see this one get a netmap_delta)
  | <== offer / answer / candidate ==>  (only between netmap peers)
  | -- heartbeat {health} --> / <-- heartbeat
  | <-- netmap_delta (whenever anything visible changes)
  | <-- error {fatal: true}, then the socket closes (revoked, expired, moved, …)
```

### hello

`hello` must be the first frame, and must arrive within 10 s.

**Authentication.** The server authenticates `peer_token`. An unknown token
gets a fatal `unauthorized`; a revoked peer gets `revoked`; an expired peer
gets `expired`. The socket then closes.

**Fields:**
- `peer_id`, if sent, must match the token's peer.
- `roles`, if sent, lists roles the client asks to hold. The hello is
  rejected (`unauthorized`) unless the peer holds every one of them. ghost-go
  sends `ghost.Config.Roles` here, and `ghost.NewHub` adds `hub`; a node
  that serves an exit sends `["exit"]`, so a peer enrolled without the exit
  role never comes up as one. Roles are assigned by the control plane (the
  pre-auth key, the approval, or the control API) and are never taken from
  the client: asking for a role can only refuse a session, never grant one.
- `public_key` (a base64 32-byte WireGuard key) is recorded on the peer. A
  different key from the stored one is a key rotation, which is audited and
  pushed to the peers that see this one. A peer must have a key before it
  joins.

### welcome

`welcome` returns:

- the peer id;
- a session id;
- the heartbeat interval, in seconds;
- `ice_servers`: the STUN URLs, plus the TURN URLs with short-lived
  credentials (see *TURN credentials*).

A newer session for the same peer replaces the older one, which is closed.

## Messages

| Type           | Direction | Payload       |
| -------------- | --------- | ------------- |
| `hello`        | c→s       | `{version, peer_token, peer_id?, roles?, public_key?}` |
| `welcome`      | s→c       | `{version, peer_id, session_id, heartbeat_interval, ice_servers[]}` |
| `join_network` | c→s       | `{network}`. It must be the peer's own network. |
| `joined`       | s→c       | `{network, address, pool}` |
| `netmap`       | s→c       | `Netmap` (below) |
| `netmap_delta` | s→c       | `NetmapDelta` (below) |
| `offer`        | both      | `{network, from, to, ufrag, pwd}` |
| `answer`       | both      | as for `offer` |
| `candidate`    | both      | `{network, from, to, candidate{type, address, port, protocol, priority, foundation, related_address?, related_port?}}` |
| `heartbeat`    | both      | `{nonce?, health?}`. The server echoes the nonce, without health. |
| `error`        | s→c       | `{code, message, fatal?}` |

### Join and addressing

- On the first join, the server assigns the lowest free `/32` in the
  network's pool, skipping the network and broadcast addresses. The address
  stays with the peer across sessions; a move releases it.
- In `api` access mode, the authorizer's `connect` decision gates the join.
  A denial gets a non-fatal `forbidden` error.
- `joined` is always followed by the first `netmap`.
- A peer is in the network only once `joined` arrives, never because it sent
  `join_network`. A refusal leaves the session open, so the client keeps
  asking with backoff (`ghost-go` waits `ghost.DefaultJoinRetryMin`, doubling
  to `ghost.DefaultJoinRetryMax`) until the join is answered or the session
  ends. A peer whose owner paused it, or whose join an authorizer outage
  denied, therefore returns by itself when the answer changes, with no
  reconnect and no restart. A joined peer stops asking: a second
  `join_network` on a joined session is a `bad_request`.

### Netmap

```json
{
  "network": "pool", "isolation": "hub-only", "seq": 1,
  "self":  { "peer_id": "peer_a", "public_key": "…", "address": "100.64.0.5/32",
             "roles": ["exit", "node"], "tags": ["tag:exit"], "endpoints": [], "online": true },
  "peers": [ { "peer_id": "peer_h", "name": "hub-1", "public_key": "…", "address": "100.64.0.1/32",
               "roles": ["hub"], "tags": [], "labels": { "geo": "ca-on" },
               "endpoints": ["203.0.113.7:51820"], "online": true } ],
  "policy": { "network": "pool", "allow": ["api.example.com:443"], "daily_bytes": 0,
              "bytes_per_second": 0, "paused": false, "labels": {}, "revision": 7 },
  "filter": { "rules": [ { "src": ["100.64.0.1/32"], "ports": [1080, 9464] } ] }
}
```

- **`peers`** are exactly the peers this one may reach or be reached by: the
  isolation mode and the ACLs make them visible (see [`policy.md`](policy.md)).
  Each entry is active (not revoked or expired), has an address and a key,
  and carries its online state.
  - Nothing about other peers is sent: no ids, keys or counts. Under
    `hub-only`, a peer without the hub role sees only hubs.
- **`labels`**, on `self` and on each peer, are the peer's control-plane
  labels (see [control-plane.md](control-plane.md#model)), for example the
  geo or ASN labels a hub picks exits by. The field is absent when a peer has
  none. Labels are informational: the ACLs and isolation never read them.
- **`policy`** is this peer's exit policy. An empty `allow` denies
  everything. `revision` is the network's policy revision.
- **`filter`** is this peer's inbound packet filter. Traffic from a source
  that matches no rule (on the rule's ports, all when empty) must be refused.
- **`isolation`** is the network's mode. A peer enforces it too (see below).

### Netmap deltas

```json
{ "network": "pool", "seq": 2,
  "self": null, "upsert": [ {"peer_id": "peer_x", "online": false, …} ], "remove": ["peer_y"],
  "policy": null, "filter": null, "isolation": "" }
```

- A delta changes the previous netmap. Fields that are absent or empty are
  unchanged.
- `upsert` replaces or adds entries by `peer_id`, and `remove` drops them.
  `self`, `policy` and `filter` replace the old value when present.
- `seq` increases by one per netmap or delta in the session.
  `proto.Netmap.Apply` implements the merge.
- The server sends a delta when a visible peer comes online, goes offline, or
  changes key, roles, tags, labels, address or endpoints. It also sends one when a
  peer becomes visible or invisible (enrolment, revocation, expiry, deletion,
  move, ACL or isolation change), and when this peer's own entry, exit policy
  or filter changes.

### Relay rules

The server rewrites `from` and `network` on every `offer`, `answer` and
`candidate`. It forwards the frame to `to` only if:

- the sender has joined;
- `to` is in the sender's netmap, and the sender is in `to`'s netmap;
- `to` is online;
- the authorizer allows `connect_peer` (in `api` mode).

Otherwise the sender gets a non-fatal error:

- `forbidden` when the pair is not allowed; it is audited as `signal.denied`,
  once per pair and session;
- `not_found` when the target is offline;
- `bad_request` otherwise.

**ICE roles.** In a pair, the peer with the higher-ranked role is the ICE
controlling agent (`hub` > `relay` > `exit` > `node`); ties go to the smaller
peer id. The controlling side sends the offer when the other peer appears
online in its netmap. The other side answers, and both trickle candidates.

### Peer-side enforcement

A ghost-go peer also enforces isolation and policy itself:

- it links only to online netmap peers;
- under `hub-only`, a non-hub peer links to, and configures WireGuard keys
  for, hubs only, and refuses offers from anyone else;
- every link's AllowedIPs is the other peer's own `/32`;
- its netstack accepts only packets addressed to itself (it never forwards);
- `Listen` applies `filter` (only hubs under `hub-only`).

### Heartbeats and health

- Clients send `heartbeat` every `welcome.heartbeat_interval` seconds (20 s by
  default), and immediately when a link changes.
- A client heartbeat carries the peer's **health summary**:

  ```json
  { "nonce": 1790000000000,
    "health": { "links": [ { "peer_id": "peer_h", "state": "connected", "candidate_type": "host",
                             "rtt_seconds": 0.004, "handshake_age_seconds": 12.5,
                             "rx_bytes": 90000, "tx_bytes": 80000 } ],
                "exit": { "cap_used_bytes": 1234, "cap_limit_bytes": 5000000000, "paused": false },
                "endpoints": ["192.168.1.20:50123"] } }
  ```

- `state` is `connecting`, `connected`, `failed` or `disconnected`.
- The server stores the summary (`GET /control/peers/{id}/health`), updates
  `last_seen` and the peer's endpoints, and publishes `peer.health` on the
  watch stream.
- Any inbound frame counts as liveness. A session silent for the heartbeat
  timeout (60 s by default) is closed, and the peers that see it get a delta
  marking it offline.

### Error codes

| Code                  | Fatal | Meaning |
| --------------------- | ----- | ------- |
| `unsupported_version` | yes   | The envelope or hello version is not 1. |
| `unauthorized`        | yes   | Unknown token, peer id mismatch, or a missing expected role. |
| `revoked`             | yes   | The peer was revoked or deleted. Sent on revoke and on every later hello. |
| `expired`             | yes   | The peer's credentials expired. |
| `moved`               | yes   | The peer was moved to another network. It must join that network from now on. |
| `forbidden`           | no / yes | A join or signal the ACLs, isolation or authorizer refuse (non-fatal). It is fatal when a policy change makes the authorizer deny a live session. |
| `bad_request`         | no / yes | A malformed frame, or one sent out of order. It is fatal only as the reply to a bad `hello`. |
| `not_found`           | no    | The relay target is offline. |
| `internal`            | no    | A server-side failure. |

## TURN credentials

When TURN is configured, `welcome.ice_servers` includes the TURN URLs, with
credentials in coturn's REST-API shared-secret scheme:

```
username   = "<expiry unix seconds>:<peer_id>"
credential = base64(HMAC-SHA1(turn_secret, username))
```

Configure coturn with `use-auth-secret` and `static-auth-secret=<turn_secret>`.
The credentials expire after `ice.turn_ttl` (1 h by default), and a
reconnect mints fresh ones.
