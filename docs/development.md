# Development

## Layout and toolchain

The repository holds two Go modules, `ghost-go` and `ghost-server`, and both
need Go 1.25. `ghost-server` pulls in `ghost-go` with
`replace ../ghost-go`, so a local change to the library is picked up
immediately. For editor support across both modules, create a git-ignored
workspace:

```bash
go work init ./ghost-go ./ghost-server
```

## Build, test, lint

The root `Makefile` runs each target in both modules:

```bash
make vet         # go vet ./...
make test        # go test ./...
make test-race   # go test -race ./... (needs cgo)
make lint        # golangci-lint run ./...
make fmt         # golangci-lint fmt ./... (gofmt + goimports)
make docker      # docker build -f ghost-server/Dockerfile -t ghost-server:dev .
```

You can also run `go test ./...` inside either module. The lint
configuration is `.golangci.yml` (golangci-lint v2). It runs the standard
linters, plus `bodyclose`, `copyloopvar`, `errorlint`, `misspell`,
`nolintlint`, `unconvert` and `usestdlibvars`, and the `gofmt` and
`goimports` formatters (imports under `github.com/Imposter/ghost` are grouped
last). To install it:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

`ghost-go/cmd/libghost` is a `c-shared` build and so needs cgo and a C
compiler for the target; with cgo off it still compiles (and its tests still
run) as an ordinary package whose `main` does nothing, because the C entry
points sit behind a `cgo` build tag. `python3 ghost-go/cmd/libghost/build.py
--docker --os linux` builds the shared library with no local toolchain. See
[ffi.md](ffi.md).

## Tests are loopback-only

Every test must run offline, on the loopback interface, with no firewall
prompt. In practice:

- ICE in tests uses host candidates on loopback with no STUN, TURN or mDNS.
  Use `nettest.LoopbackICEConfig` or `ice.TestICEConfig`. Across modules, use
  `ghost.Config.UseLoopbackICE()`, which is how ghost-server's tests connect a
  real `Node` and `Hub` through a real server.
- Bind listeners to `127.0.0.1:0`, never to `:port` or `0.0.0.0`, and let the
  OS choose ephemeral ports.
- Don't contact external hosts. Exit tests use a local target and set
  `exit.Config.AllowLoopbackForTest`, which production code must never set.
- `signal.FakeServer` runs the signalling protocol in memory when a test
  doesn't need ghost-server. `AddPeer(token, signal.FakePeer{ID, Name,
  Roles, Tags, Labels})` registers an enrolled peer, for example one holding
  the exit role; a hello asking for a role the peer lacks is refused, as
  ghost-server refuses it.

These rules are what let the suite run on the Windows CI runner.

## Running locally

```bash
cd ghost-server
GHOST_CONTROL_TOKEN=dev-token-0123456789 \
GHOST_NETWORKS=lab=100.64.0.0/10 \
go run ./cmd/ghost-server                 # SQLite at ./ghost-server.db, listens on :8080
```

Then create keys and peers through the control API
([control-plane.md](control-plane.md)), and connect a `ghost.Node` or
`ghost.Hub` to `ws://localhost:8080/v1/signal`, or use `ghost-cli`:

```bash
cd ghost-go
go run ./cmd/ghost-cli enroll -server http://localhost:8080 -auth-key gak_…
go run ./cmd/ghost-cli node -exit
```

`docker build -f ghost-go/cmd/ghost-cli/Dockerfile -t ghost-cli .` builds its
image from the repository root, and [`examples/compose`](../examples/compose)
runs a whole network.

## CI

`.github/workflows/ci.yml` runs on every push and pull request:

| Job | Runner | Steps |
| --- | ------ | ----- |
| `ghost-go`, `ghost-server` (matrix) | ubuntu-latest | `go vet ./...`, `go test -race ./...`, golangci-lint |
| `ghost-go (windows)` | windows-latest | `go vet ./...`, `go test ./...` |
| `ghost-server image` | ubuntu-latest | `docker build` of `ghost-server/Dockerfile`, not pushed |

Go comes from `actions/setup-go` (Go 1.25, with module and build caching).
Dependabot (`.github/dependabot.yml`) opens weekly updates for both Go
modules, the GitHub Actions and the Docker base images.

## Publishing images

`.github/workflows/publish.yml` builds `ghcr.io/imposter/ghost-server` for
`linux/amd64` and `linux/arm64` with buildx, and pushes it to GHCR using the
workflow's `GITHUB_TOKEN` (`packages: write`). It runs on pushes to `master`
and on `v*` tags:

| Trigger | Tags |
| ------- | ---- |
| push to `master` | `:master`, `:sha-<short>` |
| tag `v1.2.3` | `:1.2.3`, `:1.2`, `:latest`, `:sha-<short>` |

**Publishing is off by default.** The job only runs when the repository
variable `PUBLISH_IMAGES` is `true`. Until then the workflow is skipped and
nothing is pushed. To turn it on, go to *Settings → Secrets and variables →
Actions → Variables* and add `PUBLISH_IMAGES` = `true`. Delete the variable,
or set it to anything else, to turn publishing off again.

The binary's version (`main.version`) is set from the image version: the
semver on tags, the branch name on `master`.
