# ghost-server: the peer control plane

`ghost-server` is ghost's control plane, in the style of Tailscale's
coordination server or headscale. It is responsible for:

- networks;
- peers and their enrolment;
- keys and credential lifecycle;
- netmap distribution over signalling;
- ICE signal relay;
- policy (ACLs, exit policies, isolation, an optional external authorizer);
- health;
- the audit log;
- a control API with a watch stream.

It carries no tunnel traffic. Peers connect to each other directly over
ICE, and WireGuard runs end to end.

- **Wire protocol:** [`signalling-v1.md`](signalling-v1.md)
- **Policy, isolation and the authorizer:** [`policy.md`](policy.md)
- **Package layout and metrics:** [`architecture.md`](architecture.md)
- **Running the server:** [configuration](#configuration), [storage](#storage)
  and [Docker](#docker), below

## Model

**Network:** a name (`[a-z0-9][a-z0-9._-]{0,62}`), an address pool (default
`100.64.0.0/10`), an isolation mode (`none` or `hub-only`), an
`interactive_enrollment` switch (default `true`, see
[below](#turning-interactive-enrolment-off)), and a policy document with a
revision.

**Peer:** an enrolled identity in exactly one network.

| Field        | Meaning |
| ------------ | ------- |
| `id`         | `peer_…`, assigned at enrolment. |
| `public_key` | WireGuard public key (base64, 32 bytes). Set at enrolment or by the peer's `hello`. A different key in a later `hello` is a key rotation. |
| `network`    | The peer's network (`move` changes it). |
| `address`    | A `/32` from the pool, assigned on first join and kept across sessions. A move releases it. |
| `roles`      | Any of `hub`, `node`, `exit`, `relay`, assigned by the control plane, never self-declared. The default is `node`. |
| `tags`       | `tag:…` names the network's policy defines. |
| `labels`     | Free-form key/value pairs (at most 32). They are sent in netmaps, so a hub can pick peers by them. |
| `status`     | `active`, `expired` or `revoked`. |
| `endpoints`  | The candidate addresses the peer last reported. |
| `last_seen`, `expires_at`, `revoked_at` | Timestamps. |
| `ephemeral`  | Ephemeral peers are deleted after staying offline past `peers.ephemeral_grace`. |
| `enrollment_method` | How the peer enrolled: `auth_key`, `interactive` or `direct`. Recorded at enrolment and sent to the authorizer with every request about the peer. |
| `auth_key_id` | The pre-auth key an `auth_key` enrolment used. |
| `health`     | The last health summary from the peer's heartbeats. |

**Roles.**
- `hub`: a gateway that many peers connect to. It is the ICE controlling side.
- `node`: an ordinary member.
- `exit`: runs an allowlisted SOCKS5/HTTP-CONNECT exit on its tunnel IP.
- `relay`: reserved for peers that forward for others.

Only the policy decides who connects to whom. Roles are inputs to it (the
`role:` selectors and hub-only isolation).

## Enrolment

Each path creates a peer and returns its **credentials** exactly once:
`{peer_id, peer_token, network, roles, tags, ephemeral?, expires_at?}`. Only a
SHA-256 of the `gpt_…` token is stored. In `api` access mode, the authorizer
is asked (`enroll`) before the peer is created.

### Pre-auth keys

Create one with `POST /control/networks/{net}/auth-keys`:

```json
{ "reusable": false, "ephemeral": false, "roles": ["node", "exit"], "tags": ["tag:exit"],
  "labels": { "owner": "u-42" }, "expires_in_seconds": 3600, "peer_ttl_seconds": 0 }
```

- The key (`gak_…`) appears only in the create response.
- A key is **single-use** unless `reusable` is set, and expires after
  `expires_in_seconds` (default 1 h, at most 90 days).
- Its roles, tags and labels are applied to every peer it enrols.
- `ephemeral` marks enrolled peers ephemeral.
- `peer_ttl_seconds` sets enrolled peers' expiry.
- Revoke a key with `DELETE …/auth-keys/{id}`. Peers it already enrolled are
  unaffected.

A peer enrols with the key through `POST /v1/enroll`:

```json
{ "auth_key": "gak_…", "name": "exit-1", "public_key": "…", "labels": {} }
```

### Interactive enrolment codes

1. The peer calls `POST /v1/enroll/interactive` with
   `{network, name, public_key?, labels?}`. It gets back
   `{code: "ABCDE-FGHJK", poll_token, expires_at, interval_seconds}` and shows
   the code to its user.
2. An operator approves it with
   `POST /control/enrollments/{code}/approve {roles, tags, labels, name}`, or
   denies it with `…/deny {reason}`.
3. The peer polls `POST /v1/enroll/poll {poll_token}`. The status is
   `pending`, `approved`, `denied`, `expired` or `claimed`.
   - The first poll after approval creates the peer and returns its
     credentials.
   - Later polls return `claimed` without them.

**Codes:**
- are 10 characters of Crockford base32;
- are case-insensitive, and dashes and spaces are ignored;
- expire after `enrollment.code_ttl` (10 min by default);
- are stored only as hashes, as are poll tokens.

#### Turning interactive enrolment off

Each network has an `interactive_enrollment` switch. It defaults to `true`,
so a network created without it, one from `networks` in the configuration,
and every network that existed before the switch was added keep accepting
codes. Interactive enrolment already needs an operator's approval, so leaving
it on opens nothing new. Set the switch with `POST /control/networks
{…, "interactive_enrollment": false}` or `PATCH /control/networks/{net}
{"interactive_enrollment": false}`. It is not part of the policy, so changing
it does not bump the policy revision or push netmaps.

While it is off, for that network:

- `POST /v1/enroll/interactive` answers `403` with
  `{"error": "forbidden: interactive enrolment is disabled for network <net>"}`
  (an unknown network is still `404`);
- `POST /control/enrollments/{code}/approve` on a pending code answers the
  same `403`, and the code stays pending;
- `POST /v1/enroll/poll` for an enrolment approved before the switch went off
  answers the same `403` and does not create the peer. The enrolment stays
  approved, and can be claimed if the switch is turned back on before it
  expires;
- pending enrolments can still be listed and denied;
- pre-auth keys (`POST /v1/enroll`) and direct creation are unaffected.

### Direct creation

`POST /control/networks/{net}/peers {name, public_key?, roles, tags, labels,
ephemeral, ttl_seconds}` creates a peer and returns its credentials. Use it
for infrastructure such as hubs.

### Rotation and expiry

- **Token rotation:** a peer calls `POST /v1/peer/rotate`
  (`Authorization: Bearer <peer token>`, body `{public_key?}`). It gets a new
  token, and the old one stops working at once.
- **Key rotation:** a peer can also rotate its WireGuard key by sending a new
  `public_key` in `hello`. The peers that see it get the new key in a netmap
  delta. Rotations are audited (`peer.credentials_rotated`,
  `peer.key_rotated`).
- **Expiry:** `expires_at` comes from the key's peer TTL, from `ttl_seconds`,
  from `PATCH /control/peers/{id} {expires_at | clear_expiry}`, or from
  `POST /control/peers/{id}/expire`.

## Disconnect guarantees

- **Revoke, expire, delete, move.** Each of these, from the control API or
  the janitor, sends the peer's live session a fatal `revoked`, `expired` or
  `moved` error and closes it **immediately**. The peer disappears from
  everyone's netmap in the same step.
- **Reconnects** are refused at `hello`.
- **Authorizer re-checks.** A policy or isolation change re-asks the
  authorizer about every live peer (in `api` mode), and disconnects the ones
  it now denies (`forbidden`, fatal). `POST /control/peers/{id}/reauthorize`
  and `POST /control/networks/{net}/reauthorize` run the same re-check on
  demand (see [policy.md](policy.md#re-asking-on-demand)).
- **The janitor** runs every `peers.janitor_interval`. It disconnects peers
  whose credentials expired while online, deletes stale ephemeral peers, and
  prunes old enrolments.

## Netmaps

After `joined`, each peer receives a **netmap snapshot**, then **deltas**
whenever anything it can see changes:

- a visible peer's online state, key, address, roles, tags, labels or
  endpoints;
- its own entry;
- its exit policy;
- its packet filter;
- the isolation mode.

The netmap lists only the peers the isolation mode and ACLs make visible,
with their online state, and carries the peer's exit policy and inbound
packet filter. `seq` increases per session. Details are in
[`signalling-v1.md`](signalling-v1.md).

## Health

Peers send a compact **health summary** in their heartbeats:

- each link's state, selected candidate type, RTT, WireGuard handshake age,
  and rx/tx bytes;
- the exit's cap usage and paused state;
- their own candidate endpoints.

The server stores the last summary per peer and publishes each one on the
watch stream (`peer.health`). It serves the summaries at:

- `GET /control/peers/{id}/health`: `{peer_id, network, online, last_seen, health, health_at}`.
- `GET /control/health?network=`: every peer's health, plus fleet totals
  `{peers, online, links_connected, links_failed, by_candidate_type, exits_paused, cap_used_bytes}`.

Detailed node metrics (per-connection accounting, Prometheus text) are **not**
proxied by the server. The hub pulls them over the tunnel through
`ghost.NodeMetricsFetcher` (`NodeSnapshot`, `NodeConnections`,
`NodePrometheus`).

## Control API

Every route is under `/control` and needs `Authorization: Bearer <token>`.

- **The service token** (`control.service_token`) holds every scope on every
  network. Without it, the control API is not mounted.
- **API keys** (`gck_…`) carry scopes and, optionally, a list of networks.
  Outside those networks, resources answer `404`.

| Scope | Grants |
| ----- | ------ |
| `networks:read` / `networks:write` | networks, isolation and the interactive enrolment switch |
| `policy:read` / `policy:write` | the policy document, ACLs, tags, exit policies |
| `peers:read` / `peers:write` | peers, health, presence, stats, and reauthorizing peers |
| `keys:read` / `keys:write` | pre-auth keys and interactive enrolments |
| `audit:read` | the audit log |
| `watch` | the watch stream |
| `admin` | API keys |

| Method & path | Body → result |
| ------------- | ------------- |
| `GET /control/networks` | `{networks: [NetworkView]}` |
| `POST /control/networks` | `{name, pool?, isolation?, interactive_enrollment?}` → `201 NetworkView` |
| `GET /control/networks/{net}` | `NetworkView {name, pool, isolation, interactive_enrollment, policy_revision, created_at, online}` |
| `PATCH /control/networks/{net}` | `{isolation?, interactive_enrollment?}` (at least one) → `NetworkView`. Changing `isolation` bumps the policy revision and pushes netmaps. |
| `DELETE /control/networks/{net}` | `204`. `409` while it has peers. |
| `POST /control/networks/{net}/reauthorize` | (`peers:write`) re-asks the authorizer about every live peer, disconnecting the denied → `{network, peers: [Reauthorization]}` (live peers only). `409` in `open` mode. |
| `GET /control/networks/{net}/policy` | `PolicyView {network, isolation, revision, policy}` |
| `PUT /control/networks/{net}/policy` | a policy document → `PolicyView` |
| `GET / PUT /control/networks/{net}/acls` | `{acls: [...]}` |
| `GET /control/networks/{net}/tags` | `{tags: {...}, revision}` |
| `PUT / DELETE /control/networks/{net}/tags/{tag}` | `{description?}` → `PolicyView` |
| `GET /control/networks/{net}/exit-policies` | `{exit: [...], revision}` |
| `PUT / DELETE /control/networks/{net}/exit-policies/{name}` | an exit rule → `PolicyView` |
| `GET / POST /control/networks/{net}/auth-keys` | list, or create → `201 AuthKeyView` with `key` |
| `DELETE /control/networks/{net}/auth-keys/{id}` | revoke → `AuthKeyView` |
| `GET /control/networks/{net}/enrollments?status=` | `{enrollments: [...]}` |
| `GET /control/enrollments/{code}` | `EnrollmentView` |
| `POST /control/enrollments/{code}/approve` | `{roles, tags, labels, name}` → `EnrollmentView`. `403` while the network's interactive enrolment is off. |
| `POST /control/enrollments/{code}/deny` | `{reason}` → `EnrollmentView` |
| `POST /control/networks/{net}/peers` | create → `201` credentials |
| `GET /control/peers?network=&tag=&role=&include_revoked=` | `{peers: [PeerView]}` |
| `GET /control/peers/{id}` | `PeerView`, including the live `session` |
| `PATCH /control/peers/{id}` | `{name?, roles?, tags?, labels?, expires_at?, clear_expiry?}` → `PeerView` |
| `DELETE /control/peers/{id}` | `204`, and disconnects the peer |
| `POST /control/peers/{id}/revoke` | `PeerView`, and disconnects the peer |
| `POST /control/peers/{id}/expire` | `PeerView`, and disconnects the peer |
| `POST /control/peers/{id}/move` | `{network}` → `PeerView`, and disconnects the peer |
| `POST /control/peers/{id}/reauthorize` | re-asks the authorizer about the peer's live session, disconnecting it on a denial → `Reauthorization`. `409` in `open` mode. |
| `GET /control/peers/{id}/health` | `HealthView` |
| `GET /control/health?network=` | `{peers: [HealthView], totals}` |
| `GET /control/presence?network=` | `{sessions: [Presence]}` |
| `GET /control/stats` | `{networks, peers, revoked_peers, online, online_by_network}` |
| `GET /control/audit?network=&since=&limit=` | `{events: [AuditEvent]}` |
| `GET /control/watch?network=&types=&since=` | Server-Sent Events (below) |
| `GET / POST /control/api-keys`, `DELETE /control/api-keys/{id}` | `{name, scopes, networks, expires_in_seconds}` |

**PeerView** is:

```
{id, network, name, public_key, address, roles, tags, labels, endpoints,
 ephemeral, enrollment_method?, auth_key_id?, status, created_at, last_seen,
 expires_at, revoked_at, online, session?, health?, health_at?}
```

Token and key hashes are never exposed.

**Reauthorization** is
`{peer_id, network, online, allowed, reason?, disconnected}`. An offline peer
(`online: false`) is not asked about, and `allowed` and `disconnected` are
`false`. A denial, including the fail-closed denial of an unreachable
authorizer, has `disconnected: true`.

**Errors** are `{"error": "..."}`, with these statuses:

| Status | Meaning |
| ------ | ------- |
| `400` | invalid |
| `401` | unauthenticated |
| `403` | missing scope, an authorizer denial, or interactive enrolment turned off for the network |
| `404` | not found, or outside the key's networks |
| `409` | conflict, or a reauthorize call in `open` mode (no authorizer to ask) |
| `503` | authorizer unavailable |

### Watch stream

`GET /control/watch` streams bus events as SSE:

```
id: 42
event: peer.online
data: {"seq":42,"type":"peer.online","time":"…","network":"pool","peer_id":"peer_…","data":{…}}
```

**Event types:**
- **peer:** `peer.enrolled`, `peer.updated`, `peer.online`, `peer.offline`,
  `peer.revoked`, `peer.expired`, `peer.deleted`, `peer.key_rotated`,
  `peer.health`
- **network and policy:** `netmap.updated`, `policy.updated`,
  `network.updated`
- **enrolment:** `enrollment.pending`
- **audit:** `audit` (every audit entry)

**Query parameters:**
- `network=` limits the stream to some networks (comma-separated).
- `types=` takes exact types, or prefixes ending in `.` (such as `peer.`).
- `since=`, or the `Last-Event-ID` header, replays buffered events after that
  sequence number.

A slow consumer is disconnected and should resume with its last id. Keys
confined to some networks never see network-less events.

### Audit log

Every mutation is appended to the audit log (`{id, time, network, actor,
action, target, detail}`) and published on the watch stream.

**Actors:** `service`, `apikey:<id>`, `peer:<id>`, `enrollment`, `system`.

**Actions:**
- **networks and policy:** `network.created`, `network.deleted`,
  `network.isolation`, `network.interactive_enrollment` (`{enabled}`),
  `policy.updated`
- **peers:** `peer.enrolled`, `peer.enroll_denied`, `peer.updated`,
  `peer.moved`, `peer.revoked`, `peer.expired`, `peer.deleted`,
  `peer.credentials_rotated`, `peer.key_rotated`, `peer.connect_denied`
  (`{reason, on?}`, where `on` is `policy_change` or `reauthorize`),
  `peer.reauthorized` (`{online, allowed, reason}`), `network.reauthorized`
  (`{checked, disconnected}`)
- **signalling:** `signal.denied`
- **enrolment:** `auth_key.created`, `auth_key.revoked`, `enrollment.started`,
  `enrollment.approved`, `enrollment.denied`
- **API keys:** `api_key.created`, `api_key.revoked`

## Peer API

| Method & path | Body → result |
| ------------- | ------------- |
| `GET /healthz` | `{status: "ok"}` |
| `POST /v1/enroll` | `{auth_key, name?, public_key?, labels?}` → `201` credentials |
| `POST /v1/enroll/interactive` | `{network, name?, public_key?, labels?}` → `201 {code, poll_token, expires_at, interval_seconds}`. `403` while the network's interactive enrolment is off. |
| `POST /v1/enroll/poll` | `{poll_token}` → `{status, reason?, credentials?}`. `403` when claiming while the network's interactive enrolment is off. |
| `POST /v1/peer/rotate` | Bearer peer token, `{public_key?}` → `{peer_id, peer_token, public_key}` |
| `GET /v1/signal` | WebSocket, [`signalling-v1.md`](signalling-v1.md) |

## Configuration

Settings are applied in this order, and each overrides the one before:

1. the defaults;
2. an optional JSON file (`-config path` or `GHOST_CONFIG`);
3. environment variables.

Durations are Go duration strings (`"30s"`).

| JSON key | Env | Default |
| -------- | --- | ------- |
| `listen` | `GHOST_LISTEN` | `:8080` |
| `metrics_listen` | `GHOST_METRICS_LISTEN` | *(empty: `/metrics` is served on `listen`)* |
| `otlp` | `GHOST_OTLP` | `false` (reads `OTEL_EXPORTER_OTLP_*`) |
| `log_level` | `GHOST_LOG_LEVEL` | `info` |
| `database.driver` | `GHOST_DB_DRIVER` | `sqlite` (`sqlite` or `postgres`) |
| `database.dsn` | `GHOST_DB_DSN` | `ghost-server.db` (a file path, or a `postgres://` URL) |
| `access.mode` | `GHOST_ACCESS_MODE` | `open` (`open` or `api`) |
| `access.authorizer_url` | `GHOST_AUTHORIZER_URL` | required in `api` mode |
| `access.authorizer_secret` | `GHOST_AUTHORIZER_SECRET` | required in `api` mode (at least 16 bytes) |
| `access.timeout` | `GHOST_AUTHORIZER_TIMEOUT` | `3s` |
| `access.cache_ttl` | `GHOST_AUTHORIZER_CACHE_TTL` | `30s` |
| `control.service_token` | `GHOST_CONTROL_TOKEN` | *(empty: the control API is off; otherwise at least 16 bytes)* |
| `ice.stun_urls` | `GHOST_STUN_URLS` (comma-separated) | none |
| `ice.turn_urls` | `GHOST_TURN_URLS` (comma-separated) | none |
| `ice.turn_secret` | `GHOST_TURN_SECRET` | required if TURN URLs are set |
| `ice.turn_ttl` | `GHOST_TURN_TTL` | `1h` |
| `heartbeat.interval` | `GHOST_HEARTBEAT_INTERVAL` | `20s` |
| `heartbeat.timeout` | `GHOST_HEARTBEAT_TIMEOUT` | `60s` |
| `enrollment.code_ttl` | `GHOST_ENROLLMENT_CODE_TTL` | `10m` |
| `enrollment.poll_interval` | `GHOST_ENROLLMENT_POLL_INTERVAL` | `2s` |
| `peers.ephemeral_grace` | `GHOST_EPHEMERAL_GRACE` | `5m` |
| `peers.janitor_interval` | `GHOST_JANITOR_INTERVAL` | `30s` |
| `default_pool` | `GHOST_DEFAULT_POOL` | `100.64.0.0/10` |
| `networks` | `GHOST_NETWORKS` (`name`, `name=pool` or `name=pool=isolation`, comma-separated) | networks created at startup if missing |

The server logs JSON to stdout. Its metrics are listed in
[architecture.md](architecture.md#instruments).

## Storage

- One portable schema (`server/store/migrations/*.sql`) runs on **PostgreSQL**
  (production) and **SQLite** (development and single-node deployments). It
  is applied at startup and tracked in `schema_migrations`.
- The tables are `networks` (pool, isolation, interactive enrolment switch,
  policy document, revision),
  `peers`, `auth_keys`, `enrollments`, `api_keys` and `audit_events`.
- Timestamps are Unix milliseconds. Roles, tags, labels, health and policy
  documents are stored as JSON text.
- Tokens and keys are stored only as SHA-256 hashes.
- SQLite runs in WAL mode with a single connection.

## Docker

```bash
docker build -f ghost-server/Dockerfile -t ghost-server .   # from the repository root
docker run -p 8080:8080 -v ghost-data:/var/lib/ghost   -e GHOST_CONTROL_TOKEN=... -e GHOST_NETWORKS=pool=100.64.0.0/10=hub-only ghost-server
```

The image:

- is a static binary on Alpine, and runs as the non-root user `ghost`
  (uid 65532);
- keeps the SQLite database at `/var/lib/ghost/ghost-server.db`, so mount a
  volume there, or set `GHOST_DB_DRIVER=postgres` and a `GHOST_DB_DSN`;
- has a `HEALTHCHECK` that polls `GET /healthz` on the `GHOST_LISTEN` port.

Published images are `ghcr.io/imposter/ghost-server`. See
[development.md](development.md#publishing-images).
