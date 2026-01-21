# WireGuard Package - Component Overview

**Purpose**: Provides encrypted peer-to-peer tunneling using WireGuard protocol, wrapping wireguard-go with lifecycle management and dynamic peer configuration.

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Ghost-GO Application                │
└─────────────────────┬───────────────────────────────┘
                      │
         ┌────────────┴──────────────┐
         │   WireGuard Package       │
         │                           │
         │  ┌─────────────────────┐ │
         │  │  Device Wrapper     │ │  Lifecycle + Peer Management
         │  └──────────┬──────────┘ │
         │             │             │
         │  ┌──────────▼──────────┐ │
         │  │  TUN Abstraction    │ │  Cross-platform virtual interface
         │  └──────────┬──────────┘ │
         │             │             │
         │  ┌──────────▼──────────┐ │
         │  │  ICEBind Adapter    │ │  From ice package
         │  └──────────┬──────────┘ │
         └─────────────┼─────────────┘
                       │
         ┌─────────────▼──────────────┐
         │   wireguard-go Library     │  WireGuard implementation
         │   (golang.zx2c4.com)       │
         └────────────────────────────┘
```

---

## Key Components

### 1. **Device Wrapper** (`device.go`)

Manages WireGuard devices with full lifecycle control.

```go
type Device struct {
    device *device.Device    // wireguard-go device
    tun    tun.Device        // TUN interface
    bind   conn.Bind         // Network bind (ICEBind)
    config *WireGuardConfig  // Configuration
    logger *slog.Logger
    
    peers  map[string]*PeerConfig  // Active peers
    isUp   bool
    closed bool
}
```

**Core Methods:**
```go
// Lifecycle
func NewDevice(tunDevice tun.Device, bind conn.Bind, config *WireGuardConfig, logger *slog.Logger) (*Device, error)
func (d *Device) Up() error
func (d *Device) Down() error
func (d *Device) Close() error

// Configuration
func (d *Device) Configure(privateKey []byte) error

// Peer Management
func (d *Device) AddPeer(peerConfig *PeerConfig) error
func (d *Device) RemovePeer(publicKey []byte) error
func (d *Device) UpdatePeerEndpoint(publicKey []byte, endpoint string) error
func (d *Device) GetPeers() []*PeerConfig

// Status
func (d *Device) GetStatus() (string, error)
func (d *Device) GetMTU() (int, error)
func (d *Device) GetTUNName() (string, error)
```

**Thread Safety:** All public methods use mutexes for concurrent access.

---

### 2. **TUN Device Abstraction** (`tun.go`, platform files)

Creates platform-specific virtual network interfaces.

```go
// Cross-platform creation (Linux, Windows, macOS)
func CreateTUN(name string, mtu int) (tun.Device, error)

// Android-specific (from file descriptor)
func CreateTUNFromFD(fd int) (tun.Device, error)

// Get default interface name for platform
func getDefaultTUNName() string
```

**Platform Specifics:**
- **Linux**: `wg0`, `ghost0`, etc.
- **Windows**: `ghost-go` (user-friendly name)
- **macOS**: `utun3`, `utun4`, etc.
- **Android**: Uses `VpnService` file descriptor
- **iOS**: Uses `NEPacketTunnelProvider` (Phase 5)

**Key Insight:** wireguard-go handles all platform differences internally - we only need to call `tun.CreateTUN(name, mtu)` for desktop platforms!

---

### 3. **Configuration** (`config.go`)

WireGuard and peer configuration structures.

```go
type WireGuardConfig struct {
    PrivateKey          []byte         // 32-byte Curve25519 key
    ListenPort          int            // UDP port (0 = auto-assign)
    MTU                 int            // Maximum transmission unit
    PersistentKeepalive time.Duration  // Keepalive interval
}

type PeerConfig struct {
    PublicKey           []byte         // Peer's 32-byte public key
    Endpoint            string         // "host:port" format
    AllowedIPs          []string       // CIDR ranges (e.g., "10.0.0.0/24")
    PersistentKeepalive time.Duration  // Per-peer keepalive
}
```

**Validation:**
- Private key must be 32 bytes
- Listen port: 0-65535 (0 = auto)
- MTU: 1280-1500 (1280 for IPv6 compatibility)
- Allowed IPs must be valid CIDR notation
- Endpoint must be "host:port" format

---

### 4. **Key Management** (`keys.go`)

Curve25519 key generation and utilities.

```go
// Generate new private key
func GeneratePrivateKey() ([]byte, error)

// Derive public key from private
func GetPublicKey(privateKey []byte) ([]byte, error)

// Encoding/decoding
func EncodeKey(key []byte) string           // Base64
func DecodeKey(encoded string) ([]byte, error)

// Validation
func ValidatePrivateKey(key []byte) error
func ValidatePublicKey(key []byte) error
```

**Security:**
- Uses `crypto/rand` for key generation
- Keys are never logged
- Zero key validation prevents common errors

---

## Common Operations

### Create and Configure Device

```go
import (
    "ghost-go/internal/wireguard"
    "ghost-go/internal/ice"
)

// 1. Generate keys
privateKey, _ := wireguard.GeneratePrivateKey()
publicKey, _ := wireguard.GetPublicKey(privateKey)

// 2. Create TUN device
tunDev, _ := wireguard.CreateTUN("ghost0", 1280)

// 3. Create ICE connection and bind
iceConn, _ := iceAgent.Connect(ctx)
bind := ice.NewICEBind(iceConn, logger)

// 4. Configure WireGuard
config := &wireguard.WireGuardConfig{
    PrivateKey: privateKey,
    ListenPort: 0,  // Auto-assign
    MTU: 1280,
    PersistentKeepalive: 25 * time.Second,
}

// 5. Create device
device, _ := wireguard.NewDevice(tunDev, bind, config, logger)

// 6. Configure with private key
device.Configure(privateKey)

// 7. Add peer
peerConfig := &wireguard.PeerConfig{
    PublicKey: peerPublicKey,
    AllowedIPs: []string{"10.0.0.0/24"},
    PersistentKeepalive: 25 * time.Second,
}
device.AddPeer(peerConfig)

// 8. Bring up device
device.Up()

// Device is now ready for encrypted communication!
```

### Update Peer Endpoint (NAT Rebinding)

```go
// When peer's endpoint changes (e.g., mobile switching networks)
newEndpoint := "203.0.113.45:51820"
err := device.UpdatePeerEndpoint(peerPublicKey, newEndpoint)
if err != nil {
    logger.Error("failed to update endpoint", "err", err)
}
```

### Remove Peer

```go
err := device.RemovePeer(peerPublicKey)
if err != nil {
    logger.Error("failed to remove peer", "err", err)
}
```

### Get Device Status

```go
status, err := device.GetStatus()
if err != nil {
    return err
}

// status contains IPC format output:
// private_key=...
// public_key=...
// listen_port=...
// peer=...
//   endpoint=...
//   allowed_ip=...
```

### Clean Shutdown

```go
// Proper cleanup order
err := device.Down()        // Stop device
if err != nil {
    logger.Warn("down failed", "err", err)
}

err = device.Close()        // Close resources
if err != nil {
    logger.Error("close failed", "err", err)
}
```

---

## IPC Configuration Format

WireGuard uses a simple IPC format for configuration:

```
private_key=<base64>
listen_port=<port>
public_key=<base64>
endpoint=<host:port>
allowed_ip=<cidr>
persistent_keepalive_interval=<seconds>
```

Example:
```
private_key=YAnz5TF+lXXJte14tji3zlMNftq3W+MC/bQSC/73
1uk=
listen_port=51820
public_key=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw=
endpoint=203.0.113.42:51820
allowed_ip=10.0.0.0/24
persistent_keepalive_interval=25
```

This format is compatible with standard WireGuard tools (`wg`, `wg-quick`).

---

## Integration with ICE

Complete example showing ICE + WireGuard:

```go
// Phase 1: Establish ICE connection
iceConfig := &ice.ICEConfig{
    STUNServers: []string{"stun:stun.l.google.com:19302"},
    GatherTimeout: 10 * time.Second,
}

iceAgent, _ := ice.NewAgent(iceConfig, logger)

// Exchange candidates via signaling...
iceConn, _ := iceAgent.Connect(ctx)

// Phase 2: Create WireGuard over ICE
bind := ice.NewICEBind(iceConn, logger)
tunDev, _ := wireguard.CreateTUN("ghost0", 1280)

privateKey, _ := wireguard.GeneratePrivateKey()
wgConfig := &wireguard.WireGuardConfig{
    PrivateKey: privateKey,
    MTU: 1280,
}

device, _ := wireguard.NewDevice(tunDev, bind, wgConfig, logger)
device.Configure(privateKey)

// Add peer
device.AddPeer(&wireguard.PeerConfig{
    PublicKey: peerPublicKey,
    AllowedIPs: []string{"10.0.0.0/24"},
})

device.Up()

// Now you have an encrypted tunnel over ICE!
// Packets sent to 10.0.0.x will be encrypted and sent through ICE
```

---

## Testing

### Unit Tests

```bash
# Configuration tests
go test ./internal/wireguard -run "Config" -v

# Key generation tests
go test ./internal/wireguard -run "Key" -v
```

### Integration Tests

**Note:** Full device tests require proper network setup:
```bash
# Run with elevated privileges (TUN creation)
sudo go test ./internal/wireguard -run "TestDevice" -v
```

---

## Platform Support

| Platform | Status | Notes |
|----------|--------|-------|
| Linux | ✅ Full | Native TUN support |
| Windows | ✅ Full | Via wireguard-go's Windows adapter |
| macOS | ✅ Full | Uses utun interfaces |
| Android | ⏳ Phase 5 | Requires VpnService integration |
| iOS | ⏳ Phase 5 | Requires NEPacketTunnelProvider |

---

## Performance Considerations

1. **MTU Settings**
   - 1500: Standard Ethernet
   - 1280: Safe for IPv6 (recommended for Ghost-GO)
   - Lower MTU reduces fragmentation over ICE

2. **Keepalive Interval**
   - 25s: Good for mobile (prevents NAT timeout)
   - 0: Disabled (not recommended for Ghost-GO)
   - Trade-off: Battery vs connection stability

3. **Encryption Overhead**
   - ~60-80 bytes per packet (WireGuard headers)
   - Minimal CPU overhead (modern cryptography)

4. **Memory Usage**
   - ~50 goroutines per device
   - ~1-2MB per active peer
   - Scales well to 100+ peers

---

## Security Features

1. **Cryptokey Routing**
   - Each peer identified by public key
   - No IP-based authentication

2. **Perfect Forward Secrecy**
   - Session keys rotated automatically
   - Compromise of one session doesn't affect others

3. **Minimal Attack Surface**
   - < 4,000 lines of crypto code
   - Formal verification of protocol

4. **Denial of Service Protection**
   - Cookie-based handshake under load
   - Rate limiting built-in

---

## Error Handling

Common errors:

```go
// Device already up
if errors.Is(err, wireguard.ErrDeviceNotDown) {
    // Call Down() first
}

// Device closed
if errors.Is(err, wireguard.ErrDeviceClosed) {
    // Create new device
}

// Peer not found
if errors.Is(err, wireguard.ErrPeerNotFound) {
    // Peer was removed or never added
}

// Invalid key
if errors.Is(err, wireguard.ErrInvalidKey) {
    // Key must be 32 bytes, non-zero
}
```

---

## Debugging

Enable debug logging:
```go
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
```

Key debug points:
- Device creation and configuration
- Peer addition/removal
- Endpoint updates
- Handshake completion
- Data transfer statistics

Get detailed status:
```go
status, _ := device.GetStatus()
fmt.Println(status)  // Full IPC output
```

---

## Limitations

1. **Device Tests Hang**: Full WireGuard device testing is complex due to internal goroutine management. Integration tests should use real network interfaces.

2. **Mobile Support**: Android and iOS require platform-specific integration (Phase 5).

3. **IPv6**: Fully supported but requires proper network configuration.

4. **Multihoming**: Single bind per device (ICE connection). Use multiple devices for multiple connections.

---

## Related Packages

- `internal/ice` - Provides ICEBind for NAT traversal
- `internal/testutil` - Testing utilities
- `golang.zx2c4.com/wireguard` - Underlying WireGuard implementation
- `golang.zx2c4.com/wireguard/tun` - TUN device abstraction

---

**Last Updated**: January 2026  
**Maintainer**: Ghost Team
