# ghost-server policy

Each network has one **policy document**: tag definitions, ACL rules and exit
policies. Its **isolation mode** (`none` or `hub-only`) is set on the network,
and it overrides the ACLs. An optional **external authorizer** can take part in
enrolment and connection decisions and supply per-peer exit policy.

The engine lives in `ghost-server/server/policy`. It answers three questions
when the server builds a peer's netmap:

1. **Visible:** which peers need a tunnel with this peer?
2. **Filter:** which inbound traffic must this peer admit?
3. **ExitPolicy:** which exit policy applies to this peer?

Every change pushes new netmaps to the network's live peers as deltas (see
[`signalling-v1.md`](signalling-v1.md)).

## The document

```json
{
  "tags": {
    "tag:exit": { "description": "residential exits" },
    "tag:ops":  {}
  },
  "acls": [
    { "action": "accept", "src": ["role:hub"], "dst": ["*"] },
    { "action": "accept", "src": ["tag:ops"],  "dst": ["tag:exit"], "ports": [9464] }
  ],
  "exit": [
    { "name": "web", "target": ["tag:exit"], "allow": ["api.example.com:443", "*.example.org:443"],
      "daily_bytes": 5000000000, "bytes_per_second": 0, "paused": false, "labels": { "tier": "res" } }
  ]
}
```

### Selectors

| Selector        | Selects |
| --------------- | ------- |
| `*`             | every peer |
| `tag:<name>`    | peers carrying the tag (it must be defined under `tags`) |
| `role:<role>`   | peers holding the role: `hub`, `node`, `exit` or `relay` |
| `peer:<id>`     | one peer |

### Tags

- Names match `tag:[a-z0-9][a-z0-9._-]{0,62}`.
- A peer may only carry tags its network defines.
- A tag carried by a peer, or referenced by a rule, cannot be deleted.
- Tags come from the pre-auth key, the enrolment approval, the control API
  (`PATCH /control/peers/{id}`), or the authorizer at enrolment.

### ACL rules

- `action` must be `accept`. Rules only grant; anything not granted is denied.
- A rule lets peers matching `src` **open connections** to peers matching
  `dst`, on `ports` (every port when empty).
- Two peers are **visible** to each other (they appear in each other's netmap
  and may signal) when some rule lets either reach the other and the
  isolation mode permits the pair. The tunnel is symmetric, so replies flow.
- A peer's **packet filter** lists, for each rule whose `dst` matches it, the
  addresses of the visible peers matching `src`, with the rule's ports. The
  peer admits inbound connections only from those (`ghost.Node.Listen`
  enforces it).
- A peer never matches itself.

**The default document** has a single rule, `role:hub → *`, and no exit
policies. So hubs reach every peer, peers see only hubs, and exits deny every
destination. For example, hub → exit is allowed, while node → node and
exit → exit are denied.

A mesh is one rule away: `{"action":"accept","src":["*"],"dst":["*"]}` (with
isolation `none`).

### Exit policies

- An exit rule applies to the peers matching `target`.
- When several match, the most specific wins: a `peer:` selector beats `tag:`
  and `role:` selectors (first in document order), which beat `*`.
- With no matching rule, the peer's exit policy is an empty allowlist: deny
  everything.
- `allow` uses the `exit.Allowlist` syntax: `host`, `host:port`,
  `*.suffix:port`. `*`, `*:port` and empty entries are rejected: they would
  open the exit to every host.
- `daily_bytes` and `bytes_per_second` are caps (0 = unlimited; negative is
  rejected). `paused` refuses new exit connections. `labels` are passed through.
- A peer receives its policy in the netmap (`policy`), stamped with the
  network's `revision`, and ghost-go surfaces changes as `EventPolicy`.

### Validation

`PUT` of an invalid document, or of a rule inside it, returns `400`. A change
that would leave a peer carrying an undefined tag returns `409`. Every
accepted change bumps the network's `policy_revision`, is audited
(`policy.updated`), is published on the watch stream, and is pushed to the
live peers.

## Isolation

`isolation` is set per network: `POST /control/networks {"isolation": ...}`,
or `PATCH /control/networks/{net}`.

| Mode       | Effect |
| ---------- | ------ |
| `none`     | The ACLs alone decide (any topology, including a mesh). |
| `hub-only` | A pair may connect only if at least one side holds the `hub` role. Peers without the hub role never see or reach each other, whatever the ACLs say. |

`hub-only` is enforced at five layers, so a single failure does not break it.

1. **Netmap (server).** A non-hub peer's netmap and deltas contain only hub
   peers. Other peers are never named: no ids, keys, addresses or counts
   reach it, and its packet filter lists only hub addresses.
2. **Signalling (server).** An offer, answer or candidate is relayed only if
   each side is in the other's netmap. Anything else gets a `forbidden` error
   and a `signal.denied` audit entry (once per pair and session).
3. **WireGuard (peer).** A non-hub `ghost.Node` links to, and configures
   WireGuard keys for, hubs only, even if a faulty control plane lists other
   peers or relays their offers (`RefusedLinks` counts refusals). Every
   link's AllowedIPs is the peer's own `/32`.
4. **No forwarding (peer).** The netstack only accepts packets addressed to
   its own tunnel address, and drops everything else (`ForwardDrops`). A hub
   never forwards between peers, even when a modified client routes the pool
   through it with a spoofed destination.
5. **Sources (peer).** Under hub-only, a node's `Listen`, its exit
   (`exit.Config.AllowSource = node.IsHubSource`) and its `/metrics` endpoint
   accept only hub source addresses. `MetricsConfig.AllowPeers` is ignored.

In `api` access mode the authorizer adds a sixth, external layer: a
`connect_peer` request carries both sides' roles (`roles`, `target_roles`),
so the policy source can refuse a pair its pool forbids even if the network's
isolation or ACLs would allow it.

Changing the isolation mode bumps the revision, re-asks the authorizer, and
pushes new netmaps. Under `hub-only`, peers drop their non-hub links.

## The external authorizer (optional policy source)

With `access.mode = "api"`, ghost-server asks a webhook before:

| Action         | When | Cached |
| -------------- | ---- | ------ |
| `enroll`       | a peer is created: pre-auth key (before the key is spent), interactive claim, or `POST /control/networks/{net}/peers` | yes |
| `connect`      | a peer joins its network over signalling; asked again, **uncached**, for every live peer whenever the network's policy or isolation changes, and on demand through `POST /control/peers/{id}/reauthorize` or `POST /control/networks/{net}/reauthorize` | on join |
| `connect_peer` | every relayed offer, answer and candidate | yes |

In `open` mode, it is not consulted: credentials, ACLs and isolation decide.
Peer authentication applies in both modes. An unknown, revoked or expired
token never gets a session.

### Request

```
POST {access.authorizer_url}/ghost/authorize
Content-Type: application/json
X-Ghost-Signature: sha256=<hex HMAC-SHA256(authorizer_secret, raw body)>
```

```json
{
  "action": "connect", "network": "scrape-pool",
  "peer": "peer_ab12…", "target": "peer_cd34…", "target_roles": ["hub"],
  "roles": ["exit", "node"], "tags": ["tag:exit"], "labels": { "owner": "u-42" },
  "public_key": "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
  "enrollment_method": "auth_key", "auth_key_id": "key_3fz…",
  "ts": 1790000000, "nonce": "9f86d081884c7d659a2feaa0c55ad015"
}
```

| Field    | Present for | Meaning |
| -------- | ----------- | ------- |
| `peer`   | `connect`, `connect_peer` | The acting peer. It is absent for `enroll`, because the peer doesn't exist yet. |
| `target` | `connect_peer` | The peer being signalled. |
| `target_roles` | `connect_peer` | The signalled peer's roles, so the authorizer can judge the pair and not just the caller. A pool that is hub-only, say, denies any pair in which neither `roles` nor `target_roles` holds `hub`; ghost enforces that in the netmaps and the relay, and the authorizer's answer is the layer that still holds if a network's isolation or ACLs are mis-set. |
| `roles`, `tags`, `labels` | all | The acting (or enrolling) peer's. For `enroll`, these are the key's or approval's values merged with the requested labels. |
| `public_key` | all, when known | The acting (or enrolling) peer's WireGuard public key (base64, 32 bytes). On `connect` and `connect_peer` it is the key the peer presented in its `hello`. An `enroll` without a key (interactive or direct, when none was given) omits it. |
| `enrollment_method` | all | How the acting (or enrolling) peer enrolled: `auth_key` (a pre-auth key), `interactive` (a claimed interactive code) or `direct` (`POST /control/networks/{net}/peers`). It is recorded on the peer, so `connect` and `connect_peer` carry it too. It is omitted only for peers enrolled before ghost recorded it whose method could not be recovered (see [api-changes.md](api-changes.md)). |
| `auth_key_id` | `enrollment_method: auth_key` | The id (`key_…`) of the pre-auth key the peer enrolled with. |
| `ts`, `nonce` | all | Unix seconds at signing time, and 128-bit random hex (single use). |

### Verifying (authorizer side)

1. Recompute `HMAC-SHA256(secret, raw body)` and compare it in constant time.
2. Reject if `ts` is too far from your clock (5 minutes).
3. Reject a `nonce` seen within that window (replay).

`ghost-server/server/access.Verifier` does all three:
`v := access.NewVerifier(secret, 5*time.Minute, nil); req, err := v.VerifyHTTP(r)`.
Answer `401` to a request that fails verification.

### Response (HTTP 200)

```json
{ "allow": true, "reason": "",
  "policy": { "exit_allowlist": ["example.com:443"],
              "caps": { "daily_bytes": 5000000000, "bytes_per_second": 0 },
              "labels": { "tier": "residential" }, "tags": ["tag:vetted"] } }
```

- `allow: false` denies. `reason` is returned to the client, as the HTTP `403`
  body or in the signalling `forbidden` error.
- On `enroll`, `tags` are added to the peer (they must be defined), and
  `labels` are merged into its labels.
- On `connect`, if `exit_allowlist` is present (even `[]`), it replaces the
  network policy's allowlist and caps for the session, and `labels` replace
  the rule's labels. The rule's `paused` still applies. Without
  `exit_allowlist`, the network policy applies unchanged.
- On `connect_peer`, only allow or deny matters.

### Re-asking on demand

When the authorizer's answer about a peer changes (it paused a node, say),
tell ghost-server instead of waiting for the next policy change:

- `POST /control/peers/{id}/reauthorize` re-asks about one peer;
- `POST /control/networks/{net}/reauthorize` re-asks about every live peer
  in the network.

Both need `peers:write`. Each live, joined session gets a fresh, uncached
`connect` request, exactly as on a policy change: a denial closes it with a
fatal `forbidden` error (`connection no longer authorized: <reason>`) and is
audited as `peer.connect_denied` with `on: "reauthorize"`; an allow
refreshes the session's exit policy. Offline peers are not asked about, but
their cached decisions are dropped, so their next join is asked afresh.
There is no separate paused state in ghost: the authorizer denies the
reconnect as well. In `open` mode there is nothing to ask, and both routes
answer `409`. Responses are listed in
[control-plane.md](control-plane.md#control-api).

### Fail closed

- Timeouts (`access.timeout`, 3 s by default), transport errors, non-200
  answers and undecodable bodies all deny.
- These failures are never cached. An outage during enrolment returns `503`,
  and an interactive claim stays retryable.
- Decisions are cached for `access.cache_ttl` (30 s by default), keyed on
  action, network, peer, target, roles, target roles, tags, labels, public
  key, enrolment method and pre-auth key id. Revoking, expiring, moving, updating,
  reauthorizing or deleting a peer drops its cached decisions. A denial is
  cached too, so a peer denied on a re-check stays out until the entry
  expires or the peer is reauthorized.
- Metrics: `ghost_server.authz.decisions{action,result,cached}` and
  `ghost_server.authz.duration`.
