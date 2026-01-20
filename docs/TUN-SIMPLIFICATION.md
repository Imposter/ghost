# Phase 1 Update - TUN Simplification

**Date**: January 20, 2026  
**Change**: Simplified TUN device implementation based on architecture review

---

## Problem Identified

The original TUN implementation had 3 platform-specific files (Linux/Windows/macOS) with ~35 lines each, all doing essentially the same thing: calling `tun.CreateTUN(name, mtu)`.

This was redundant because **wireguard-go already handles all platform differences internally**.

---

## Changes Made

### Before: Redundant Implementation
```
internal/wireguard/
├── tun.go            (~25 lines - platform routing logic)
├── tun_linux.go      (~35 lines - Linux implementation)
├── tun_windows.go    (~35 lines - Windows implementation)  
└── tun_darwin.go     (~25 lines - macOS implementation)

Total: ~120 lines, mostly duplicated logic
```

### After: Simplified Implementation
```
internal/wireguard/
├── tun.go            (~45 lines - single implementation + mobile docs)
├── tun_linux.go      (6 lines - default name constant "ghost%d")
├── tun_windows.go    (5 lines - default name constant "Ghost")
└── tun_darwin.go     (5 lines - default name constant "utun")

Total: ~61 lines, DRY principle followed
```

---

## Key Insights

### Desktop Platforms
All desktop platforms (Linux, Windows, macOS) use **identical** TUN creation:
```go
device, err := tun.CreateTUN(name, mtu)
```

wireguard-go's internal implementation handles:
- Linux: `/dev/net/tun` character device
- Windows: WinTun driver integration
- macOS: `utun` device creation

We only need to provide platform-specific **default names**.

### Mobile Platforms (Phase 5)

Mobile platforms have **fundamentally different** TUN handling:

#### Android
```
Java/Kotlin Layer:
  VpnService.Builder.establish()
    ↓
  Returns ParcelFileDescriptor
    ↓
  Extract fd with parcelFd.getFd()
    ↓
  Pass to Go via JNI
    ↓
Go Layer:
  CreateTUNFromFD(fd)
    ↓
  wireguard-go uses the fd
```

#### iOS
```
Swift Layer:
  NEPacketTunnelProvider.packetFlow
    ↓
  Provides read/write packet APIs
    ↓
  NO file descriptor exposed
    ↓
Go Layer:
  Custom packet flow handlers
    ↓
  Bridge Swift packet flow ↔ Go implementation
```

iOS is **not fd-based** - it uses packet flow callbacks, requiring custom bridging code.

---

## Updated Code

### `tun.go` (Main Implementation)
```go
package wireguard

import (
    "fmt"
    "golang.zx2c4.com/wireguard/tun"
)

// CreateTUN creates a TUN device for desktop platforms
// Works identically on Linux, Windows, and macOS
func CreateTUN(name string, mtu int) (tun.Device, error) {
    if mtu <= 0 {
        return nil, fmt.Errorf("invalid MTU: %d", mtu)
    }

    if name == "" {
        name = getDefaultTUNName()
    }

    // wireguard-go handles platform differences internally
    device, err := tun.CreateTUN(name, mtu)
    if err != nil {
        return nil, fmt.Errorf("failed to create TUN device: %w", err)
    }

    return device, nil
}

// CreateTUNFromFD for Android (Phase 5)
func CreateTUNFromFD(fd int) (tun.Device, error) {
    return nil, fmt.Errorf("CreateTUNFromFD not yet implemented - needed for Android in Phase 5")
}

func getDefaultTUNName() string {
    return defaultTUNName // Defined in platform-specific files
}
```

### Platform Files (Constants Only)
```go
// tun_linux.go
//go:build linux
package wireguard

const defaultTUNName = "ghost%d"

// tun_windows.go
//go:build windows
package wireguard

const defaultTUNName = "Ghost"

// tun_darwin.go
//go:build darwin
package wireguard

const defaultTUNName = "utun"
```

---

## Benefits

✅ **Reduced code duplication**: 120 → 61 lines  
✅ **Clearer architecture**: Single source of truth  
✅ **Easier maintenance**: Changes in one place  
✅ **Better documentation**: Mobile platform differences clearly explained  
✅ **Follow Go best practices**: Use build tags for minimal platform differences  

---

## Testing

All tests still pass:
```
ok      ghost-go/internal/ice           0.422s
ok      ghost-go/internal/wireguard     0.402s
```

---

## Documentation Updated

### `docs/phase-1-core-infrastructure.md`
- Added status indicators (✅ Complete, ⏳ Pending)
- Documented TUN simplification
- Explained mobile platform differences
- Updated file status table
- Added test coverage summary (22 passing tests)

### `plan.md` (Session Plan)
- Marked Stages 1-6 as complete
- Documented TUN simplification rationale
- Added mobile platform notes

---

## Next Steps

Phase 1 remains at **60% complete**. Remaining work:

1. **Stage 7**: WireGuard device wrapper (`device.go`)
2. **Stage 8**: Unit tests for agent & bind
3. **Stage 9**: Integration test (2-peer connection)
4. **Stage 10**: CLAUDE.md documentation
5. **Stage 11**: Demo application

---

## References

- [WireGuard-Android Go Backend](https://deepwiki.com/WireGuard/wireguard-android/5.1-go-backend) - How Android passes TUN fd to Go
- [Android VpnService API](https://developer.android.com/develop/connectivity/vpn) - How Android creates TUN devices
- [wireguard-go Repository](https://github.com/WireGuard/wireguard-go) - Cross-platform TUN handling
