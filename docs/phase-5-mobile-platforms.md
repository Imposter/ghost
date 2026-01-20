# Phase 5: Mobile Platforms

## Overview

This phase implements platform-specific integrations for Android (VpnService) and iOS (Network Extension), including power management and secure key storage.

---

## Android Integration

### 5.1 VPN Service Bridge (`internal/platform/android/vpnservice.go`)

Go code that bridges to Android's VpnService.

```go
// +build android

// StartTunnel is called from Kotlin when VpnService starts
//export StartTunnel
func StartTunnel(fd int, configJSON string) error

// StopTunnel is called when VpnService stops
//export StopTunnel
func StopTunnel() error

// GetStatus returns current tunnel status as JSON
//export GetStatus
func GetStatus() string
```

### 5.2 Kotlin VPN Service (`GhostVpnService.kt`)

Android VpnService implementation (reference).

```kotlin
class GhostVpnService : VpnService() {

    external fun startTunnel(fd: Int, config: String): String?
    external fun stopTunnel(): String?

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // 1. Start foreground notification (required Android 8+)
        // 2. Build VPN interface with Builder
        // 3. Call protect() on tunnel sockets
        // 4. Call startTunnel() with file descriptor
    }
}
```

**Key considerations:**
- **Foreground Service**: Required for background execution
- **Socket Protection**: Must call `protect()` on tunnel socket
- **Always-On VPN**: Support system-managed VPN
- **Per-App VPN**: Support app-specific routing

### 5.3 Android Keystore Integration

```kotlin
object SecureStorage {
    private const val KEYSTORE_ALIAS = "ghost_keys"

    fun storePrivateKey(key: ByteArray) {
        // Store in Android Keystore (hardware-backed when available)
    }

    fun getPrivateKey(): ByteArray? {
        // Retrieve from Keystore
    }
}
```

---

## iOS Integration

### 5.4 Network Extension Bridge (`internal/platform/ios/tunnel.go`)

Go code for iOS Network Extension.

```go
// +build ios

//export CreateTunnel
func CreateTunnel(configJSON *C.char) unsafe.Pointer

//export StartTunnel
func StartTunnel(handlePtr unsafe.Pointer, tunFd C.int) C.int

//export StopTunnel
func StopTunnel(handlePtr unsafe.Pointer)

//export SetCallbacks
func SetCallbacks(stateCallback, errorCallback)
```

### 5.5 Swift Packet Tunnel Provider (`PacketTunnelProvider.swift`)

iOS Network Extension implementation (reference).

```swift
class PacketTunnelProvider: NEPacketTunnelProvider {

    override func startTunnel(options: [String : NSObject]?,
                              completionHandler: @escaping (Error?) -> Void) {
        // 1. Configure tunnel settings
        // 2. Call setTunnelNetworkSettings
        // 3. Get tunnel file descriptor
        // 4. Call Go StartTunnel
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                             completionHandler: @escaping () -> Void) {
        // Call Go StopTunnel
    }
}
```

**Key considerations:**
- **Memory Limits**: 15MB limit for Network Extension
- **Separate Process**: Extension runs in separate process
- **System Lifecycle**: iOS manages extension lifecycle
- **No Background Tasks**: Cannot prevent termination

### 5.6 iOS Keychain Integration

```swift
struct SecureStorage {
    static func storeKey(_ key: Data, identifier: String) throws {
        // Store in iOS Keychain (Secure Enclave when available)
    }

    static func getKey(identifier: String) throws -> Data? {
        // Retrieve from Keychain
    }
}
```

---

## Power Management

### 5.7 Power Manager (`internal/platform/power.go`)

Platform-agnostic power state handling.

```go
type PowerManager interface {
    OnBatteryLow()
    OnScreenOff()
    OnScreenOn()
    AdjustKeepalive(state PowerState) time.Duration
}

type PowerState string

const (
    PowerStateNormal     PowerState = "normal"
    PowerStateLowBattery PowerState = "low_battery"
    PowerStateCharging   PowerState = "charging"
    PowerStateScreenOff  PowerState = "screen_off"
)
```

**Keepalive adjustments:**

| State | WireGuard Keepalive |
|-------|---------------------|
| Normal | 25s |
| Low Battery | 60s |
| Charging | 15s |
| Screen Off | 45s |

---

## Mobile Build Configuration

### gomobile Setup

```bash
# Install gomobile
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

# Build Android AAR
gomobile bind -target=android -o ghost.aar ./cmd/ghost-mobile

# Build iOS Framework
gomobile bind -target=ios -o Ghost.xcframework ./cmd/ghost-mobile
```

### Build Script (`scripts/build-mobile.sh`)

```bash
#!/bin/bash
set -euo pipefail

# Android
echo "Building Android..."
gomobile bind -target=android \
    -androidapi 21 \
    -o build/ghost.aar \
    ./cmd/ghost-mobile

# iOS
echo "Building iOS..."
gomobile bind -target=ios \
    -o build/Ghost.xcframework \
    ./cmd/ghost-mobile
```

---

## Files to Create

| File | Purpose |
|------|---------|
| `cmd/ghost-mobile/main.go` | Mobile library entry point |
| `internal/platform/android/vpnservice.go` | Android VPN bridge |
| `internal/platform/android/tunnel.go` | Android tunnel management |
| `internal/platform/ios/tunnel.go` | iOS Network Extension bridge |
| `internal/platform/ios/extension.go` | Extension lifecycle |
| `internal/platform/power.go` | Power management interface |
| `internal/platform/power_android.go` | Android power management |
| `internal/platform/power_ios.go` | iOS power management |
| `scripts/build-mobile.sh` | Mobile build script |

---

## Platform Comparison

| Feature | Android | iOS |
|---------|---------|-----|
| VPN API | VpnService | NEPacketTunnelProvider |
| Background | Foreground Service | Extension process |
| Memory | ~50-100MB | 15MB limit |
| Key Storage | Android Keystore | iOS Keychain |
| Socket Protection | protect() method | Automatic |
| Lifecycle | App controls | System controls |

---

## Testing Strategy

- Unit tests for Go bindings
- Android instrumented tests
- iOS XCTest for extension
- Test VPN configuration
- Test key storage
- Test power state handling
- Test reconnection on network change
