# Example: a control plane, TURN, a hub and an exit node

This Compose project runs a complete ghost network on one machine:

| Service | What it is |
| ------- | ---------- |
| `ghost-server` | the control plane, with the network `lab` (`hub-only`); its control API is on `127.0.0.1:8080` |
| `coturn` | STUN and TURN. ghost-server hands peers short-lived TURN credentials signed with the shared secret coturn also holds (the TURN REST API scheme) |
| `setup` | a one-shot script ([`setup.sh`](setup.sh)): it sets the exit policy, creates the hub peers and makes a pre-auth key for the node |
| `node` | `ghost-cli node -exit -metrics`: enrols with the pre-auth key, then serves an exit and its metrics on its tunnel IP |
| `hub` | `ghost-cli hub -forward 127.0.0.1:1080=node-1:1080`: forwards a port in its container to the node's exit |
| `hub-cli` | a second hub peer for one-shot commands (profile `tools`) |

Both images are built from this repository.

## Run it

```bash
cd examples/compose
docker compose up --build -d
docker compose logs -f node hub     # wait for "peer connected"
```

The exit only goes to the hosts in `EXIT_ALLOW` (default
`example.com:443,www.example.com:443`), which `setup.sh` puts in the
network's exit policy. Set `EXIT_ALLOW`, `GHOST_CONTROL_TOKEN` and
`GHOST_TURN_SECRET` in the environment or an `.env` file before the first
`up` to change them, and `GHOST_HOST_PORT` to publish the control API on
another host port than 8080.

## Use the exit

From inside the hub's container, through the forwarded port. The exit speaks
SOCKS5 and HTTP CONNECT. The source tag names the source (a metric label)
and the job (kept in the node's connection log only):

```bash
docker compose exec hub curl -sS -p -x http://127.0.0.1:1080 \
  --proxy-header 'X-Ghost-Source: source=demo&job=1' https://example.com/

docker compose exec hub curl -sS -x socks5h://127.0.0.1:1080 \
  --proxy-user 'source=demo&job=2:x' https://example.com/
```

Or with a one-shot hub, which waits for the node and fetches through its
exit:

```bash
docker compose run --rm hub-cli curl -source demo -job 3 node-1 https://example.com/
docker compose run --rm hub-cli metrics node-1                 # the node's metrics snapshot
docker compose run --rm hub-cli metrics -connections 5 node-1  # its recent connections
```

A destination outside the policy is refused by the exit:

```bash
docker compose exec hub curl -sS -p -x http://127.0.0.1:1080 https://example.org/   # 403
```

## Look around

```bash
docker compose exec node ghost-cli status   # the node's netmap, tunnel and recent exit connections
docker compose exec hub ghost-cli status

TOKEN=example-control-token-change-me
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/control/peers?network=lab
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/control/health?network=lab
curl -s -H "Authorization: Bearer $TOKEN" -X POST http://127.0.0.1:8080/control/peers/<node peer id>/revoke
```

Revoking the node closes its session at once, and the hub drops the link.

## Notes

- Every container shares one bridge network, so ICE normally picks host
  candidates. TURN is there for peers behind NATs: the TURN URLs name
  `coturn`, so only containers in this project can use them as written.
  Behind real NATs, publish coturn's ports (3478 and the relay range
  49160-49200/udp) and advertise an address the peers can reach.
- `setup` runs once; its output lives in the `shared` volume. `docker compose
  down -v` removes every volume, so the next `up` starts over.
- See [docs/control-plane.md](../../docs/control-plane.md) for the control
  API and [docs/policy.md](../../docs/policy.md) for the policy document.
