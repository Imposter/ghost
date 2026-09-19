# ghost-server access control

ghost-server decides who may **register**, **pair**, **join a network** and
**connect to a peer**. Set the mode with `access.mode` (`GHOST_ACCESS_MODE`):

| Mode   | Behaviour |
| ------ | --------- |
| `open` | Every request from a valid device token is allowed. Self-registration is open to anyone who can reach the server. This mode is for development and closed networks. |
| `api`  | Before each of the four actions, the server asks an external **authorizer** webhook. No answer means deny (fail closed). |

Device authentication applies in both modes: a revoked or unknown token never
gets a session. Access control is an extra decision on top of it.

## The authorizer webhook

```
POST {access.authorizer_url}/ghost/authorize
Content-Type: application/json
X-Ghost-Signature: sha256=<hex HMAC-SHA256(authorizer_secret, raw body)>
```

### Request body

```json
{
  "action":  "join_network",
  "network": "scrape-pool",
  "device":  "dev_ab12…",
  "peer":    "dev_cd34…",
  "role":    "node",
  "labels":  { "owner": "u-42" },
  "ts":      1790000000,
  "nonce":   "9f86d081884c7d659a2feaa0c55ad015"
}
```

| Field     | Present for | Meaning |
| --------- | ----------- | ------- |
| `action`  | all | `register`, `pair`, `join_network` or `connect_peer`. |
| `network` | all | The network concerned. For `pair`, it is the pairing code's network. |
| `device`  | `join_network`, `connect_peer` | The acting device. For `register` and `pair` the device doesn't exist yet. |
| `peer`    | `connect_peer` | The device being signalled. |
| `role`    | all | The acting (or future) device's role: `node` or `hub`. |
| `labels`  | all | The device's labels. For `register` and `pair`, these are the requested labels merged with the pairing code's labels. |
| `ts`      | all | Unix seconds at signing time. |
| `nonce`   | all | 128-bit random hex. Single use. |

### Verifying a request (authorizer side)

1. Recompute `HMAC-SHA256(secret, raw body)` and compare it in constant time
   with the `X-Ghost-Signature` value after `sha256=`. Reject on mismatch.
2. Reject if `ts` is more than 5 minutes from your clock.
3. Reject if you have seen `nonce` within the window (replay).

The signature covers the whole body, including `ts` and `nonce`.

For Go authorizers, `ghost-server/server/access.Verifier` implements all three
steps:

```go
v := access.NewVerifier([]byte(secret), 5*time.Minute, nil)
req, err := v.VerifyHTTP(r) // access.ErrBadSignature, ErrStale, ErrReplay, ErrMalformed
```

Answer `401` to a request that fails verification. ghost-server treats any
non-200 response as a denial.

### Response body (HTTP 200)

```json
{
  "allow": true,
  "reason": "",
  "policy": {
    "exit_allowlist": ["example.com:443"],
    "caps":   { "daily_bytes": 5000000000, "bytes_per_second": 0 },
    "labels": { "tier": "residential" }
  }
}
```

- `allow`: `false` denies. The server passes `reason` back to the client in the
  HTTP 403 body or the signalling `forbidden` error.
- `policy` is optional:
  - **register / pair:** `labels` are merged over the device's labels when the
    device is created.
  - **join_network:** if `exit_allowlist` is present (even as `[]`), it
    replaces the network's allowlist for this session, along with `caps` and
    `labels`. The network's `paused` flag still applies. Without
    `exit_allowlist`, the network policy applies unchanged.
  - **connect_peer:** ignored.

### Caching and failure

- Decisions, both allow and deny, are cached for `access.cache_ttl`
  (`GHOST_AUTHORIZER_CACHE_TTL`, 30 s by default). The cache key is action,
  network, device, peer, role and labels.
- Revoking or moving a device drops its cached decisions.
- Timeouts (`access.timeout`, 3 s by default), transport errors, non-200
  statuses and bodies that don't decode all **deny**. These failures are never
  cached, so the next attempt asks again.
- `connect_peer` is checked for every relayed `offer`, `answer` and
  `candidate`, which the cache makes cheap.
- A `pair` is authorized **before** the code is consumed. A denial or an
  outage leaves the code usable until it expires.

### Metrics

`ghost_server.authz.decisions{action, result=allow|deny|error, cached}` and
`ghost_server.authz.duration` (seconds).

## Admin API

Every route needs `Authorization: Bearer <admin.token>` (`GHOST_ADMIN_TOKEN`,
at least 16 bytes). Without a token, the admin API is not mounted.

| Method & path | Body | Result |
| ------------- | ---- | ------ |
| `GET /admin/networks` | — | `{networks: [NetworkView]}` |
| `POST /admin/networks` | `{name, pool?}` | `201 NetworkView` |
| `GET /admin/networks/{name}` | — | `NetworkView` |
| `PUT /admin/networks/{name}/policy` | `{allow[], daily_bytes, bytes_per_second, paused, labels}` | `{policy, pushed}`. Stored, then pushed as a `policy` message to every live session in the network. |
| `GET /admin/devices?network=&include_revoked=` | — | `{devices: [DeviceView]}` |
| `GET /admin/devices/{id}` | — | `DeviceView`, including the live `session` if online |
| `POST /admin/devices/{id}/revoke` | — | `DeviceView`. Revokes the device and **disconnects the live session immediately** (`error{code: revoked, fatal}`). |
| `POST /admin/devices/{id}/move` | `{network}` | `DeviceView`. Releases the address and closes the live session. |
| `GET /admin/devices/{id}/metrics` | — | Proxied node metrics. Returns `501` until a `DeviceMetricsProxy` is plugged in (see A3b). |
| `GET /admin/devices/{id}/metrics/connections` | — | As above, for the node's recent-connections ring. |
| `POST /admin/pairing-codes` | `{network, role?, name?, labels?, ttl_seconds?}` | `201 {code, network, role, expires_at}` |
| `GET /admin/presence?network=` | — | `{sessions: [Presence]}` |
| `GET /admin/stats` | — | `{networks, devices, revoked_devices, online, online_by_network{net:{role:n}}}` |

**Policy rules:**
- An `allow` entry of `*`, `*:<port>` or an empty string is rejected with
  `400`, because it would turn the exit into an open proxy.
- Caps must not be negative.

**DeviceView:**
`{id, network, role, name, labels, public_key, address, created_at, last_seen, revoked_at, online, session?}`.
The token hash is never exposed.

## Device API (public)

| Method & path | Body | Result |
| ------------- | ---- | ------ |
| `POST /v1/register` | `{network, name?, role?, labels?, public_key?}` | `201 {device_id, device_token, network, role}`. Access action `register`. |
| `POST /v1/pair` | `{code, name?, labels?, public_key?}` | `201` with the same body. Access action `pair`. |
| `GET /v1/signal` | WebSocket | See `signalling-v1.md`. |

- The device token is shown **once**. Only its SHA-256 is stored.
- Pairing codes:
  - are 10 characters from Crockford base32 (`XXXXX-XXXXX`);
  - are case-insensitive, and dashes and spaces are ignored;
  - can be used once;
  - expire after `pairing.ttl` (10 minutes by default, at most 24 h per code);
  - are stored only as a hash.
