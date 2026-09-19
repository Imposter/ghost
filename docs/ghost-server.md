# ghost-server

`ghost-server` is ghost's peer control plane: networks, peers and their
enrolment, netmap distribution and ICE signalling, policy, health, audit and
a control API with a watch stream. It is written in Go and replaces the .NET
`ghost-coordination`. It is one binary, and a Docker image built from
`ghost-server/Dockerfile`.

- **Model, enrolment, control API, watch stream:** [`control-plane.md`](control-plane.md)
- **Policy, isolation, external authorizer:** [`policy.md`](policy.md)
- **Protocol:** [`signalling-v1.md`](signalling-v1.md)

## Layout

```
ghost-server/                      Go module github.com/Imposter/ghost/ghost-server
  cmd/ghost-server/                binary: config, OTel SDK, listeners
  server/                          assembly (server.New) used by the binary and tests
  server/config/                   JSON file + GHOST_* env loading
  server/store/                    database/sql store, SQLite and Postgres, embedded migrations
  server/control/                  networks, peers, enrolment, rotation, expiry, API keys, audit, janitor
  server/policy/                   policy documents: tags, ACLs, exit policies, isolation
  server/events/                   event bus behind the watch stream
  server/access/                   optional external authorizer: webhook signing, cache, Verifier
  server/signalling/               WebSocket sessions, netmaps and deltas, relay, health
  server/httpapi/                  peer API, control API, SSE watch stream
  server/ipam/                     pool address allocation
  server/turn/                     coturn REST TURN credentials
  server/telemetry/                OTel instruments (API only)
```

The module imports `ghost-go` (for `signal/proto` and `otelsetup`) through a
`replace ../ghost-go` directive. For local multi-module work, create a
`go.work` (it is git-ignored) with `go work init ./ghost-go ./ghost-server`.

## Configuration

Settings are applied in this order, each overriding the one before:

1. the defaults;
2. an optional JSON file (`-config path` or `GHOST_CONFIG`);
3. environment variables.

Durations are Go duration strings (`"30s"`).

| JSON key | Env | Default |
| -------- | --- | ------- |
| `listen` | `GHOST_LISTEN` | `:8080` |
| `metrics_listen` | `GHOST_METRICS_LISTEN` | *(empty: `/metrics` on `listen`)* |
| `otlp` | `GHOST_OTLP` | `false` (reads `OTEL_EXPORTER_OTLP_*`) |
| `log_level` | `GHOST_LOG_LEVEL` | `info` |
| `database.driver` | `GHOST_DB_DRIVER` | `sqlite` (`sqlite` or `postgres`) |
| `database.dsn` | `GHOST_DB_DSN` | `ghost-server.db` (a file path, or a `postgres://` URL) |
| `access.mode` | `GHOST_ACCESS_MODE` | `open` (`open` or `api`) |
| `access.authorizer_url` | `GHOST_AUTHORIZER_URL` | required in `api` mode |
| `access.authorizer_secret` | `GHOST_AUTHORIZER_SECRET` | required in `api` mode (at least 16 bytes) |
| `access.timeout` | `GHOST_AUTHORIZER_TIMEOUT` | `3s` |
| `access.cache_ttl` | `GHOST_AUTHORIZER_CACHE_TTL` | `30s` |
| `control.service_token` | `GHOST_CONTROL_TOKEN` | *(empty: control API off; otherwise at least 16 bytes)* |
| `ice.stun_urls` | `GHOST_STUN_URLS` (comma-separated) | — |
| `ice.turn_urls` | `GHOST_TURN_URLS` (comma-separated) | — |
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

## Storage

- One portable schema (`server/store/migrations/*.sql`) runs on SQLite and
  PostgreSQL. It is applied at startup and tracked in `schema_migrations`.
- Timestamps are Unix milliseconds; roles, tags, labels, health and policy
  documents are JSON text. Tokens and keys are stored only as SHA-256 hashes.
- SQLite runs in WAL mode with a single connection. Use it for development and
  single-node deployments.

## Observability

- **Library side:** `server/telemetry` uses only the OTel API.
- **Binary side:** the binary builds the SDK through `ghost-go/otelsetup`. It
  serves Prometheus at `/metrics`, and adds OTLP when `otlp` is on.
- **Instruments:**
  - `ghost_server.sessions.active{role}`
  - `ghost_server.signal.messages{type,direction}`
  - `ghost_server.authz.decisions{action,result,cached}`
  - `ghost_server.authz.duration`
  - `ghost_server.enrollments{result}`
  - `ghost_server.peers.revoked`
  - `ghost_server.heartbeat.timeouts`

## Docker

```bash
docker build -f ghost-server/Dockerfile -t ghost-server .   # from the repo root
docker run -p 8080:8080 -e GHOST_CONTROL_TOKEN=... -e GHOST_NETWORKS=pool=100.64.0.0/10=hub-only ghost-server
```

The image:
- is distroless and runs as a non-root user;
- holds the SQLite database at `/var/lib/ghost/ghost-server.db` (mount a volume
  there), or set `GHOST_DB_DRIVER=postgres`.
