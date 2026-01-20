# Phase 1: Core Infrastructure

## Overview

This phase establishes the foundational components: Pion/ICE integration for NAT punchthrough and WireGuard integration for encrypted tunneling. The critical piece is the `ICEBind` adapter that bridges these two technologies.

---

## Components

### 1.1 ICE Agent Wrapper (`internal/ice/agent.go`)

Wraps Pion's ICE agent with Ghost-specific functionality.

```go
// Agent interface
type Agent interface {
    GatherCandidates(ctx context.Context) (<-chan Candidate, error)
    AddRemoteCandidate(candidate Candidate) error
    SetRemoteCredentials(ufrag, pwd string) error
    LocalCredentials() (ufrag, pwd string)
    Connect(ctx context.Context) (net.Conn, error)
    GetSelectedCandidatePair() (*CandidatePair, error)
    Close() error
}
```

**Key responsibilities:**
- Configure STUN/TURN servers
- Gather local ICE candidates (host, srflx, relay)
- Handle remote candidate addition (trickle ICE)
- Establish connectivity checks
- Return a `net.Conn` for WireGuard

### 1.2 ICEBind Adapter (`internal/ice/bind.go`)

**This is the most critical component.** It implements WireGuard's `conn.Bind` interface using an ICE-established connection.

```go
// ICEBind implements wireguard-go's conn.Bind
type ICEBind struct {
    conn      net.Conn    // ICE connection
    endpoint  *ICEEndpoint
    // ...
}

// Required methods:
func (b *ICEBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error)
func (b *ICEBind) Send(bufs [][]byte, ep conn.Endpoint) error
func (b *ICEBind) Close() error
func (b *ICEBind) SetMark(mark uint32) error
func (b *ICEBind) BatchSize() int
func (b *ICEBind) ParseEndpoint(s string) (conn.Endpoint, error)
```

**Design notes:**
- Single receive function (ICE manages the actual socket)
- Send writes directly to ICE connection
- Endpoint is fixed (single peer per bind)
- BatchSize of 1 for simplicity

### 1.3 WireGuard Device Wrapper (`internal/wireguard/device.go`)

Wraps wireguard-go's device with configuration helpers.

```go
type Device struct {
    device    *device.Device
    tun       tun.Device
    bind      conn.Bind
    logger    *slog.Logger
}

func NewDevice(tunDevice tun.Device, bind conn.Bind, config *WireGuardConfig) (*Device, error)
func (d *Device) Configure(privateKey, peerPublicKey []byte, allowedIPs []string) error
func (d *Device) Up() error
func (d *Device) Down() error
func (d *Device) Close() error
```

### 1.4 TUN Device Abstraction (`internal/wireguard/tun.go`)

Platform-specific TUN device creation.

```go
// CreateTUN creates a TUN device (platform-specific)
func CreateTUN(name string, mtu int) (tun.Device, error)

// CreateTUNFromFD creates a TUN from an existing file descriptor (mobile)
func CreateTUNFromFD(fd int) (tun.Device, error)
```

---

## Dependencies

```go
// go.mod additions
require (
    github.com/pion/ice/v3 v3.x.x
    github.com/pion/stun/v2 v2.x.x
    github.com/pion/turn/v3 v3.x.x
    golang.zx2c4.com/wireguard v0.x.x
    golang.zx2c4.com/wireguard/tun v0.x.x
)
```

---

## Configuration Types

```go
// ICEConfig holds ICE-specific settings
type ICEConfig struct {
    STUNServers       []string          // e.g., ["stun:stun.l.google.com:19302"]
    TURNServers       []TURNServer
    GatherTimeout     time.Duration     // Default: 10s
    ConnectionTimeout time.Duration     // Default: 30s
    KeepaliveInterval time.Duration     // Default: 15s
    InterfaceFilter   []string          // Allowed interfaces
    CandidateTypes    []CandidateType   // host, srflx, prflx, relay
}

// WireGuardConfig holds WireGuard-specific settings
type WireGuardConfig struct {
    PrivateKey          []byte
    ListenPort          int               // 0 = auto
    MTU                 int               // Default: 1280
    PersistentKeepalive time.Duration     // Default: 25s
}
```

---

## Integration Flow

```
1. Create ICE Agent with STUN/TURN config
2. Gather local ICE candidates
3. Exchange candidates via signaling (Phase 2)
4. Add remote candidates
5. Connect ICE agent → returns net.Conn
6. Create ICEBind with ICE connection
7. Create TUN device (platform-specific)
8. Create WireGuard device with ICEBind + TUN
9. Configure WireGuard peer with exchanged public key
10. Bring up WireGuard device
```

---

## Files to Create

| File | Purpose |
|------|---------|
| `internal/ice/agent.go` | ICE agent wrapper |
| `internal/ice/bind.go` | ICEBind adapter (conn.Bind) |
| `internal/ice/gatherer.go` | Candidate gathering logic |
| `internal/ice/config.go` | ICE configuration |
| `internal/wireguard/device.go` | WireGuard device wrapper |
| `internal/wireguard/config.go` | WireGuard configuration |
| `internal/wireguard/keys.go` | Key generation utilities |
| `internal/wireguard/tun.go` | TUN device abstraction |
| `internal/wireguard/tun_linux.go` | Linux TUN implementation |
| `internal/wireguard/tun_darwin.go` | macOS TUN implementation |
| `internal/wireguard/tun_windows.go` | Windows TUN implementation |

---

## Testing Strategy

- Unit tests for ICEBind (mock net.Conn)
- Integration tests with local STUN server
- Test candidate gathering on different network types
- Test WireGuard handshake over mocked ICE connection
- Platform-specific TUN tests

---

## CLAUDE.md for this folder

After implementation, create `internal/ice/CLAUDE.md` and `internal/wireguard/CLAUDE.md` with:
- Component overview
- Key interfaces
- Common operations
- Testing commands
- Known issues/limitations
