# Development

## Layout and toolchain

The repository holds two Go modules, `ghost-go` and `ghost-server`, and both
need Go 1.26. `ghost-server` pulls in `ghost-go` with
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

## Test deadlines scale with the build

`ghost-server/server`'s tests run a real server over real WebSocket sessions,
so they wait for things. Every deadline in the package goes through `wait()`
(`server/waits_test.go`), which multiplies it by `raceWaitScale` -- 8 under
`-race` (`//go:build race`), 1 otherwise -- and by `$GHOST_TEST_TIMEOUT_SCALE`
when it is set, for a machine slower still:

```bash
GHOST_TEST_TIMEOUT_SCALE=2 go test -race ./server/
```

Write a deadline as what the server should need and let the scale cover what
the machine can manage; never raise the constant instead. A fixed wait that a
loaded race build loses fails whichever test happened to be running, which is
a poor way to choose what to distrust.

## Tests are loopback-only

Every test must run offline, on the loopback interface, with no firewall
prompt. In practice:

- ICE in tests uses host candidates on loopback with no STUN, TURN or mDNS.
  Use `nettest.LoopbackICEConfig` or `ice.TestICEConfig`. Across modules, use
  `ghost.Config.UseLoopbackICE()`, which is how ghost-server's tests connect a
  real `Node` and `Hub` through a real server. They are also the only configs
  that set `ICEConfig.IncludeLoopback`: production gathers no loopback
  candidate, so a test that builds its own config must ask for one.
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

`master` takes no direct push: changes merge through a pull request.
`.github/workflows/ci.yml` checks each pull request, for the modules it touches
only (a change to the workflow itself checks both):

| Job | Runs when | Runner | Steps |
| --- | --------- | ------ | ----- |
| `ghost-go` | `ghost-go/` changed | ubuntu-latest | `go vet ./...`, `go test -race ./...`, golangci-lint |
| `ghost-server` | `ghost-server/` or `ghost-go/` changed | ubuntu-latest | the same |
| `ghost-go (windows)` | `ghost-go/` changed | windows-latest | `go vet ./...`, `go test ./...` |

A manual run (*Actions → CI → Run workflow*) checks everything. Go comes from
`actions/setup-go` (Go 1.26, with module and build caching). Dependabot
(`.github/dependabot.yml`) opens weekly updates for both Go modules, the GitHub
Actions and the Docker base images.

## Publishing images

`.github/workflows/publish.yml` publishes `ghcr.io/imposter/ghost-server` for
`linux/amd64` and `linux/arm64`, pushed to GHCR with the workflow's
`GITHUB_TOKEN` (`packages: write`), **for releases only**: a published GitHub
release of a `vX.Y.Z` tag, or a manual run naming one. Nothing is published on a
push or a pull request.

```sh
gh release create v1.2.3 --generate-notes   # tags master's head and publishes the release
```

The image is rebuilt only when `ghost-server/` or `ghost-go/` changed since the
previous release; otherwise the previous release's image is tagged with the new
version, so every release names an image. A manual run with **force** rebuilds.

| Release | Tags |
| ------- | ---- |
| `v1.2.3` | `:1.2.3`, `:1.2`, `:latest` |
| `v1.3.0-rc.1` (pre-release) | `:1.3.0-rc.1` |

The binary's version (`main.version`) is the release's version (`1.2.3`).
