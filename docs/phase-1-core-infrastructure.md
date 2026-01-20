# Phase 1: Core Infrastructure

**Status**: 60% Complete - Core components implemented, integration pending  
**Last Updated**: January 20, 2026

## Overview

This phase establishes the foundational components: Pion/ICE integration for NAT punchthrough and WireGuard integration for encrypted tunneling. The critical piece is the `ICEBind` adapter that bridges these two technologies.

### ✅ What's Complete
- ICE agent wrapper with STUN/TURN support
- ICEBind adapter (critical component)
- WireGuard configuration and key management
- Cross-platform TUN device abstraction
- 22 unit tests passing

### ⏳ What's Remaining
- WireGuard device wrapper
- Integration tests
- Documentation (CLAUDE.md files)
- Demo application

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

**Status**: ⏳ Pending (Stage 7)

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

**Status**: ✅ Complete - Simplified implementation

Platform-agnostic TUN device creation. **wireguard-go handles platform differences internally**, so we only need to set default names per platform.

```go
// CreateTUN creates a TUN device for desktop platforms
// Works identically on Linux, Windows, and macOS
func CreateTUN(name string, mtu int) (tun.Device, error)

// CreateTUNFromFD creates a TUN from an existing file descriptor
// Used for Android in Phase 5 (VpnService provides the fd)
// iOS uses a different approach (NEPacketTunnelProvider packet flow)
func CreateTUNFromFD(fd int) (tun.Device, error)
```

**Platform-specific files** (simplified to constants only):
- `tun_linux.go`: Default name `"ghost%d"` (kernel replaces %d)
- `tun_windows.go`: Default name `"Ghost"` (WinTun handles it)
- `tun_darwin.go`: Default name `"utun"` (macOS convention)

**Mobile Platform Notes**:
- **Android**: VpnService creates TUN fd → Pass to `CreateTUNFromFD(fd)`
- **iOS**: NEPacketTunnelProvider uses packet flow API (no fd) → Custom handlers needed

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

## Files Status

| File | Status | Purpose |
|------|--------|---------|
| `internal/ice/agent.go` | ✅ Complete | ICE agent wrapper |
| `internal/ice/bind.go` | ✅ Complete | ICEBind adapter (conn.Bind) ⭐ CRITICAL |
| `internal/ice/config.go` | ✅ Complete | ICE configuration with validation |
| `internal/ice/types.go` | ✅ Complete | Candidate, Endpoint types |
| `internal/ice/errors.go` | ✅ Complete | Domain-specific errors |
| `internal/wireguard/device.go` | ⏳ Pending | WireGuard device wrapper |
| `internal/wireguard/config.go` | ✅ Complete | WireGuard configuration |
| `internal/wireguard/keys.go` | ✅ Complete | Key generation utilities |
| `internal/wireguard/errors.go` | ✅ Complete | Domain-specific errors |
| `internal/wireguard/tun.go` | ✅ Complete | TUN device abstraction |
| `internal/wireguard/tun_linux.go` | ✅ Simplified | Linux default name constant |
| `internal/wireguard/tun_darwin.go` | ✅ Simplified | macOS default name constant |
| `internal/wireguard/tun_windows.go` | ✅ Simplified | Windows default name constant |

**Test Coverage**: 22 unit tests passing (config & keys validated)

---

## Testing Strategy

### ✅ Completed
- Unit tests for ICEConfig validation (10 tests)
- Unit tests for WireGuardConfig validation (12 tests)
- Unit tests for key generation and validation (10 tests)
- All tests passing

### ⏳ Pending
- Unit tests for ICEBind (mock net.Conn)
- Unit tests for ICE agent
- Integration tests with local STUN server
- Test candidate gathering on different network types
- End-to-end test: Two peers over ICE with WireGuard encryption

---

## Next Steps

1. **Stage 7**: Create WireGuard device wrapper (`device.go`)
2. **Stage 8**: Write unit tests for agent and bind
3. **Stage 9**: Create integration test (2-peer connection)
4. **Stage 10**: Document components (CLAUDE.md files)
5. **Stage 11**: Build demo application

---

## Known Simplifications

### TUN Device Implementation
- Desktop platforms (Linux/Windows/macOS) all use `tun.CreateTUN()` identically
- Platform-specific files reduced to default name constants only
- wireguard-go handles all OS-specific logic internally
- Mobile platforms require different approaches (Phase 5):
  - **Android**: OS creates TUN fd via VpnService API → Pass to Go
  - **iOS**: OS provides packet flow API → Custom bridging needed

### ICEBind Design
- BatchSize = 1 (simplifies initial implementation)
- Single endpoint per bind (ICE is point-to-point)
- Direct reads from net.Conn (no additional buffering)
- SetMark is no-op (not applicable for ICE connections)

---

## CLAUDE.md Documentation

After Stage 7 completion, create component documentation:

**`internal/ice/CLAUDE.md`**:
- Component overview
- Key interfaces (Agent, ICEBind)
- Common operations (gather, connect, bind)
- Testing commands
- Known limitations

**`internal/wireguard/CLAUDE.md`**:
- Component overview
- Key interfaces (Device, TUN)
- Platform differences
- Testing commands
- Mobile considerations
