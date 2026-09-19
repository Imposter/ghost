# ghost-server

`ghost-server` is ghost's signalling and coordination server. It is written in
Go and replaces the .NET `ghost-coordination`. It is one binary, and a Docker
image built from `ghost-server/Dockerfile`.

- **Protocol:** [`signalling-v1.md`](signalling-v1.md)
- **Access control and admin API:** [`access-control.md`](access-control.md)

## Layout

```
ghost-server/                      Go module github.com/Imposter/ghost/ghost-server
  cmd/ghost-server/                binary: config, OTel SDK, listeners
  server/                          assembly (server.New) used by the binary and tests
  server/config/                   JSON file + GHOST_* env loading
  server/store/                    database/sql store, SQLite and Postgres, embedded migrations
  server/control/                  domain: networks, devices, pairing, revoke, move, policy
  server/access/                   open|api access control, webhook signing, Verifier
  server/signalling/               WebSocket relay, presence, heartbeats, policy push
  server/httpapi/                  public device API and bearer-token admin API
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
| `admin.token` | `GHOST_ADMIN_TOKEN` | *(empty: admin API off; otherwise at least 16 bytes)* |
| `ice.stun_urls` | `GHOST_STUN_URLS` (comma-separated) | — |
| `ice.turn_urls` | `GHOST_TURN_URLS` (comma-separated) | — |
| `ice.turn_secret` | `GHOST_TURN_SECRET` | required if TURN URLs are set |
| `ice.turn_ttl` | `GHOST_TURN_TTL` | `1h` |
| `heartbeat.interval` | `GHOST_HEARTBEAT_INTERVAL` | `20s` |
| `heartbeat.timeout` | `GHOST_HEARTBEAT_TIMEOUT` | `60s` |
| `pairing.ttl` | `GHOST_PAIRING_TTL` | `10m` |
| `default_pool` | `GHOST_DEFAULT_POOL` | `100.64.0.0/10` |
| `networks` | `GHOST_NETWORKS` (`name=pool,name2`) | networks created at startup if missing |

## Storage

- One portable schema (`server/store/migrations/*.sql`) runs on SQLite and
  PostgreSQL. It is applied at startup and tracked in `schema_migrations`.
- Timestamps are Unix milliseconds, and labels and policies are JSON text.
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
  - `ghost_server.pairings{result}`
  - `ghost_server.registrations{result}`
  - `ghost_server.devices.revoked`
  - `ghost_server.heartbeat.timeouts`
  - `ghost_server.policy.pushes`

## Docker

```bash
docker build -f ghost-server/Dockerfile -t ghost-server .   # from the repo root
docker run -p 8080:8080 -e GHOST_ADMIN_TOKEN=... -e GHOST_NETWORKS=pool ghost-server
```

The image:
- is distroless and runs as a non-root user;
- holds the SQLite database at `/var/lib/ghost/ghost-server.db` (mount a volume
  there), or set `GHOST_DB_DRIVER=postgres`.
