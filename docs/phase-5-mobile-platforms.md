# Phase 5: Mobile Platforms

**Status**: 🔜 Not Started
**Repository**: `ghost-app/` (React Native/Expo)
**Dependencies**: Phase 2a (.NET Backend), Phase 2b (ghost-proxy), Phase 4 (HTTP Proxy)

## Overview

This phase implements consumer-facing mobile applications using **React Native with Expo**. The app provides a user interface for authentication, device management, peer selection, and connection monitoring.

**Key architectural decision**: The mobile app uses **gomobile native bindings** to call into the `ghost-proxy` library directly. There is NO HTTP API for control - all interactions happen through native function calls (JNI on Android, Cgo on iOS).

The app operates entirely in **user space** without using OS-level VPN APIs (`VpnService` on Android or `NetworkExtension` on iOS). The `ghost-proxy` library starts a local HTTP/WebSocket reverse proxy that the app uses to route traffic through the P2P tunnel.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│               React Native/Expo App (TypeScript)            │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  Screens                                               │ │
│  │  • LoginScreen - OAuth2 authentication                │ │
│  │  • DeviceListScreen - Show paired devices             │ │
│  │  • ConnectionScreen - Connect to peer                 │ │
│  │  • StatusScreen - Connection stats, tunnel info       │ │
│  └───────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  Native Module Bridge (TypeScript)                    │ │
│  │  import GhostProxy from './native-modules/GhostProxy' │ │
│  │  • GhostProxy.initialize(url, jwt)                    │ │
│  │  • GhostProxy.startConnection(peerInfo)               │ │
│  │  • GhostProxy.stopConnection()                        │ │
│  │  • GhostProxy.getStatus()                             │ │
│  └───────────────────────────────────────────────────────┘ │
└────────────────┬────────────────────────────────────────────┘
                 │ Native Calls (JNI/Cgo)
                 │ No HTTP - Direct function invocation
                 ▼
┌─────────────────────────────────────────────────────────────┐
│         ghost-proxy Native Module (Go + Native Glue)        │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  gomobile Exported API (cmd/ghost-mobile/)            │ │
│  │  • Initialize(coordinationURL, deviceJWT)             │ │
│  │  • StartConnection(peerDeviceID, peerPubKey, iceJSON) │ │
│  │  • StopConnection()                                    │ │
│  │  • GetConnectionStatus() → JSON                       │ │
│  │  • SetOnStateChange(callback)                         │ │
│  └───────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  SignalR Client (internal/signaling/)                 │ │
│  │  • Connects to .NET backend SignalR hub               │ │
│  │  • Sends/receives ICE offers/answers/candidates       │ │
│  │  • E2E encrypts WireGuard keys before sending         │ │
│  └───────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  HTTP/WebSocket Reverse Proxy (pkg/proxy/)            │ │
│  │  • Listens on 127.0.0.1:RANDOM_PORT                   │ │
│  │  • Forwards app HTTP requests through tunnel          │ │
│  │  • Returns local URL to app: http://127.0.0.1:8080    │ │
│  └───────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────┐ │
│  │  Uses ghost-go (pkg/tunnel/)                          │ │
│  │  • ICE NAT traversal + WireGuard tunnel               │ │
│  └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                 │
                 │ SignalR WebSocket (offer/answer/candidates)
                 ▼
┌─────────────────────────────────────────────────────────────┐
│         .NET Coordination Backend (Phase 2a)                │
│  • Account management (OAuth2/OIDC)                         │
│  • Device registry (PostgreSQL)                             │
│  • SignalR hub (real-time signaling relay)                  │
│  • Same-account authorization enforcement                   │
└─────────────────────────────────────────────────────────────┘
```

---

## Component Breakdown

### 5.1 React Native App (`ghost-app/`)

**Tech Stack:**
- React Native with Expo
- TypeScript
- Expo Router for navigation
- React Query for state management
- TailwindCSS (NativeWind) for styling

**Project Structure:**
```
ghost-app/
├── app/                          # Expo Router screens
│   ├── (auth)/
│   │   ├── login.tsx             # OAuth2 login flow
│   │   └── pairing.tsx           # QR code pairing
│   ├── (tabs)/
│   │   ├── devices.tsx           # Device list
│   │   ├── connect.tsx           # Peer selection + connection
│   │   └── settings.tsx          # App settings
│   └── _layout.tsx               # Root layout
├── src/
│   ├── components/               # Reusable UI components
│   ├── hooks/                    # Custom hooks
│   ├── services/                 # API clients
│   │   ├── coordination.ts       # REST API calls
│   │   └── tunnel.ts             # Native module wrapper
│   └── types/                    # TypeScript types
├── modules/
│   └── ghost-proxy/              # Native module
│       ├── android/              # Android glue code
│       ├── ios/                  # iOS glue code
│       └── src/                  # TypeScript definitions
└── assets/
```

**Key Dependencies:**
```json
{
  "dependencies": {
    "expo": "~51.0.0",
    "expo-router": "~3.5.0",
    "expo-modules-core": "~1.12.0",
    "@tanstack/react-query": "^5.0.0",
    "nativewind": "^4.0.0"
  }
}
```

---

### 5.2 Native Module Implementation

The native module bridges TypeScript to the Go library compiled with `gomobile bind`.

#### TypeScript API (`modules/ghost-proxy/src/GhostProxyModule.ts`)

```typescript
export interface PeerInfo {
  deviceId: string;
  publicKey: string;      // Base64 WireGuard public key
  name: string;
  platform: string;
  isOnline: boolean;
}

export interface ICEServer {
  urls: string | string[];  // "stun:..." or "turn:..."
  username?: string;
  credential?: string;
}

export interface ConnectionStatus {
  state: 'disconnected' | 'connecting' | 'connected' | 'error';
  latency_ms?: number;
  bytes_sent?: number;
  bytes_received?: number;
  local_addr?: string;
  remote_addr?: string;
  error?: string;
}

export interface GhostProxyModule {
  /**
   * Initialize the proxy with coordination server URL and device JWT
   */
  initialize(coordinationURL: string, deviceJWT: string): Promise<void>;

  /**
   * Start P2P connection to peer
   * Returns local proxy URL (e.g., "http://127.0.0.1:8080")
   */
  startConnection(
    peerDeviceId: string,
    peerPublicKey: string,
    iceServers: ICEServer[]
  ): Promise<string>;

  /**
   * Stop the current connection
   */
  stopConnection(): Promise<void>;

  /**
   * Get current connection status
   */
  getStatus(): Promise<ConnectionStatus>;

  /**
   * Set callback for state changes
   */
  onStateChange(listener: (state: string) => void): void;

  /**
   * Set callback for errors
   */
  onError(listener: (error: string) => void): void;
}
```

#### Android Native Module (`modules/ghost-proxy/android/src/main/java/expo/modules/ghostproxy/GhostProxyModule.kt`)

```kotlin
package expo.modules.ghostproxy

import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import ghostproxy.Ghostproxy  // Generated by gomobile

class GhostProxyModule : Module() {
  override fun definition() = ModuleDefinition {
    Name("GhostProxy")

    AsyncFunction("initialize") { coordinationURL: String, deviceJWT: String ->
      try {
        Ghostproxy.initialize(coordinationURL, deviceJWT)
      } catch (e: Exception) {
        throw Exception("Failed to initialize: ${e.message}")
      }
    }

    AsyncFunction("startConnection") { peerDeviceId: String, peerPublicKey: String, iceServersJSON: String ->
      try {
        val proxyURL = Ghostproxy.startConnection(peerDeviceId, peerPublicKey, iceServersJSON)
        return@AsyncFunction proxyURL
      } catch (e: Exception) {
        throw Exception("Failed to start connection: ${e.message}")
      }
    }

    AsyncFunction("stopConnection") {
      try {
        Ghostproxy.stopConnection()
      } catch (e: Exception) {
        throw Exception("Failed to stop connection: ${e.message}")
      }
    }

    AsyncFunction("getStatus") {
      try {
        val statusJSON = Ghostproxy.getConnectionStatus()
        return@AsyncFunction statusJSON
      } catch (e: Exception) {
        throw Exception("Failed to get status: ${e.message}")
      }
    }

    OnCreate {
      // Module initialization
    }

    OnDestroy {
      // Cleanup
      try {
        Ghostproxy.stopConnection()
      } catch (_: Exception) {
        // Ignore cleanup errors
      }
    }
  }
}
```

#### iOS Native Module (`modules/ghost-proxy/ios/GhostProxyModule.swift`)

```swift
import ExpoModulesCore
import Ghostproxy  // Generated by gomobile

public class GhostProxyModule: Module {
  public func definition() -> ModuleDefinition {
    Name("GhostProxy")

    AsyncFunction("initialize") { (coordinationURL: String, deviceJWT: String) in
      var error: NSError?
      GhostproxyInitialize(coordinationURL, deviceJWT, &error)
      if let error = error {
        throw Exception(name: "InitializationError", description: error.localizedDescription)
      }
    }

    AsyncFunction("startConnection") { (peerDeviceId: String, peerPublicKey: String, iceServersJSON: String) -> String in
      var error: NSError?
      let proxyURL = GhostproxyStartConnection(peerDeviceId, peerPublicKey, iceServersJSON, &error)
      if let error = error {
        throw Exception(name: "ConnectionError", description: error.localizedDescription)
      }
      return proxyURL ?? ""
    }

    AsyncFunction("stopConnection") {
      var error: NSError?
      GhostproxyStopConnection(&error)
      if let error = error {
        throw Exception(name: "DisconnectionError", description: error.localizedDescription)
      }
    }

    AsyncFunction("getStatus") -> String {
      return GhostproxyGetConnectionStatus() ?? "{}"
    }

    OnDestroy {
      // Cleanup
      var error: NSError?
      GhostproxyStopConnection(&error)
    }
  }
}
```

---

### 5.3 Application Flow

#### Connection Establishment

```typescript
// src/services/tunnel.ts
import GhostProxy from '@/modules/ghost-proxy';
import { coordinationAPI } from './coordination';

class TunnelService {
  private proxyURL: string | null = null;

  async initialize(deviceJWT: string) {
    await GhostProxy.initialize(
      'https://coordination.example.com',
      deviceJWT
    );
  }

  async connectToPeer(peerDeviceId: string) {
    // 1. Fetch peer info from coordination server
    const peerInfo = await coordinationAPI.getDevice(peerDeviceId);

    // 2. Get ICE servers from config
    const iceServers = [
      { urls: 'stun:stun.l.google.com:19302' },
      {
        urls: 'turn:turn.example.com:3478',
        username: 'user',
        credential: 'pass'
      }
    ];

    // 3. Start P2P connection
    this.proxyURL = await GhostProxy.startConnection(
      peerInfo.id,
      peerInfo.publicKey,
      iceServers
    );

    console.log(`Tunnel established, proxy at: ${this.proxyURL}`);
    return this.proxyURL;
  }

  async disconnect() {
    await GhostProxy.stopConnection();
    this.proxyURL = null;
  }

  async makeRequestToPeer(path: string) {
    if (!this.proxyURL) {
      throw new Error('Not connected');
    }

    // All requests go through the local proxy
    const response = await fetch(`${this.proxyURL}${path}`);
    return response.json();
  }
}

export const tunnelService = new TunnelService();
```

#### User Flow

```
1. Launch App
   ↓
2. Login Screen
   • User taps "Login with Google/Auth0"
   • OAuth2 flow completes
   • Receive JWT with account_id + device_id claims
   ↓
3. Initialize Module
   • GhostProxy.initialize(coordinationURL, jwt)
   • Connects to SignalR hub
   • Registers device as online
   ↓
4. Device List Screen
   • Fetch devices from /api/devices
   • Show: Name, Platform, Online status, Last seen
   ↓
5. User selects peer → Connection Screen
   ↓
6. Start Connection
   • Call GhostProxy.startConnection(peerId, peerPubKey, iceServers)
   • Show loading state "Connecting..."
   • ghost-proxy:
     - Generates WireGuard keys
     - Gathers ICE candidates
     - Sends offer via SignalR
     - Receives answer
     - Establishes tunnel via ghost-go
     - Starts HTTP proxy on 127.0.0.1:RANDOM_PORT
     - Returns proxy URL
   ↓
7. Connected Screen
   • Display: "Connected to John's iPhone"
   • Show stats: Latency, Bytes sent/received
   • Show proxy URL (for debugging)
   • App can now make requests:
     fetch(`${proxyURL}/api/files`)
     fetch(`${proxyURL}/api/photos/123`)
   ↓
8. Disconnect
   • User taps "Disconnect"
   • GhostProxy.stopConnection()
   • Tunnel and proxy shut down
```

---

### 5.4 Background Execution Considerations

**Key Limitation**: Since we're NOT using VPN APIs, the OS may suspend the app when backgrounded.

#### Android
- **Foreground Service** (optional): If you need to keep the tunnel alive for background syncing or notifications, use a Foreground Service with type `dataSync`.
- **Battery Optimization**: User may need to disable battery optimization for the app.
- **Expected Behavior**: Tunnel stays active while app is in foreground. May pause when backgrounded unless using Foreground Service.

#### iOS
- **Background Modes**: Limited options without VPN entitlement.
  - `background-fetch`: Periodic updates (15-30 min intervals, unreliable).
  - `voip`: Background execution for VoIP apps (may get rejected if misused).
- **Expected Behavior**: Tunnel only active while app is in foreground. Connection pauses when app goes to background.
- **App Store Compliance**: Using background modes for non-VoIP purposes may result in rejection.

**Recommendation**: Design the app UX assuming the tunnel is **foreground-only**. Users should keep the app open when actively using the P2P connection.

### 5.5 Build Workflow

#### Prerequisites
1. **Install Go** (1.21+)
2. **Install gomobile**:
   ```bash
   go install golang.org/x/mobile/cmd/gomobile@latest
   gomobile init
   ```
3. **Install Node.js** (18+) and Expo CLI
4. **Install Android Studio** (for Android builds)
5. **Install Xcode** (for iOS builds, macOS only)

#### Build Steps

**Step 1: Build ghost-proxy Go library**

```bash
cd ghost-proxy

# Android
gomobile bind -target=android \
  -o ../ghost-app/modules/ghost-proxy/android/libs/ghostproxy.aar \
  -javapkg=com.ghost.proxy \
  ./cmd/ghost-mobile

# iOS
gomobile bind -target=ios \
  -o ../ghost-app/modules/ghost-proxy/ios/Ghostproxy.xcframework \
  ./cmd/ghost-mobile
```

**Step 2: Link bindings in React Native app**

Android (`modules/ghost-proxy/android/build.gradle`):
```gradle
dependencies {
  implementation files('libs/ghostproxy.aar')
}
```

iOS: Xcode automatically links `.xcframework` if placed in the module folder.

**Step 3: Create Expo Development Build**

```bash
cd ghost-app

# Android
npx expo run:android

# iOS
npx expo run:ios
```

**Step 4: Test the integration**

```bash
# Run tests
npm test

# Run on device
npx expo run:android --device
npx expo run:ios --device
```

---

### 5.6 Platform-Specific Considerations

#### Android
- **Minimum SDK**: 24 (Android 7.0)
- **Target SDK**: 34 (Android 14)
- **Permissions Required**:
  ```xml
  <uses-permission android:name="android.permission.INTERNET" />
  <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
  ```
- **Foreground Service** (optional, for background tunneling):
  ```xml
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE" />
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC" />
  ```

#### iOS
- **Minimum Version**: iOS 14.0
- **Capabilities**: None required (not using VPN or Network Extension)
- **Background Modes**: Optional, limited effectiveness
- **App Store Review**: Standard consumer app, no special entitlements needed

---

## Alternative Use Case: Local Network Pairing

**NOTE**: This use case is **out of scope** for the core `ghost-go` library, but is documented here as a common deployment pattern for Phone ↔ Hub scenarios.

### Overview

In this use case:
- **Phone**: Mobile device (iOS/Android)
- **Hub**: Local device (Raspberry Pi, NAS, home server)
- **Key Requirement**: Keys are only exchanged when both devices are on the **same local network**
- **No cloud dependency**: No .NET coordination backend required for pairing

This provides better privacy and works offline, making it ideal for self-hosted scenarios.

---

### Local Pairing Architecture

```
┌────────────────────────────────────────────────────────────┐
│                    Local Network (192.168.1.x)             │
│                                                            │
│  ┌──────────────┐                    ┌─────────────────┐  │
│  │    Phone     │                    │   Home Hub      │  │
│  │              │◄──mDNS Discovery──►│  (Raspberry Pi) │  │
│  │              │                    │                 │  │
│  │ 1. Scan QR   │                    │ 2. Show QR Code │  │
│  │              │                    │    on screen    │  │
│  │              │                    │                 │  │
│  │ 3. HTTP POST │                    │                 │  │
│  │ /pair ───────┼───────────────────►│ 4. Verify +    │  │
│  │              │    (over local IP) │    Store keys   │  │
│  │              │                    │                 │  │
│  │ 5. Store hub │◄───────────────────┤ 6. Return hub   │  │
│  │    info +    │    Hub public key  │    public key   │  │
│  │    pub key   │                    │                 │  │
│  └──────────────┘                    └─────────────────┘  │
│                                                            │
└────────────────────────────────────────────────────────────┘

After pairing, devices can connect via P2P even when NOT on same network.
Both devices store each other's public keys locally.
```

---

### Local Pairing Flow

#### 1. Hub Discovery (mDNS/Bonjour)

Hub broadcasts its presence on the local network:

```go
// Hub side (out of scope for ghost-go, but shown for reference)
import "github.com/hashicorp/mdns"

func broadcastHub() {
    service, _ := mdns.NewMDNSService(
        "GhostHub-" + hostname,
        "_ghosthub._tcp",
        "",
        "",
        8080,  // Pairing HTTP server port
        nil,
        []string{"txtvers=1", "id=" + deviceID},
    )
    server, _ := mdns.NewServer(&mdns.Config{Zone: service})
    defer server.Shutdown()
}
```

Phone discovers hubs:

```typescript
// Phone app (React Native)
import { DiscoverHubs } from '@/native-modules/MDNSDiscovery';

const hubs = await DiscoverHubs({
  serviceType: '_ghosthub._tcp',
  timeout: 5000  // 5 seconds
});

// Returns: [{ name: "GhostHub-raspberrypi", address: "192.168.1.100", port: 8080 }]
```

#### 2. QR Code Pairing

Hub generates a pairing QR code containing:
```json
{
  "version": 1,
  "hub_id": "550e8400-e29b-41d4-a716-446655440000",
  "hub_name": "Living Room Pi",
  "pairing_token": "eyJhbGc...",  // Short-lived JWT (5 min expiry)
  "local_ip": "192.168.1.100",
  "pairing_port": 8080
}
```

Phone scans QR code and extracts pairing info.

#### 3. Key Exchange (Over Local Network)

Phone sends pairing request to hub:

```http
POST http://192.168.1.100:8080/api/pair
Content-Type: application/json
Authorization: Bearer <pairing_token_from_qr>

{
  "device_id": "phone-uuid",
  "device_name": "Alice's iPhone",
  "platform": "ios",
  "public_key": "base64-encoded-wireguard-public-key"
}
```

Hub validates token and responds:

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "hub_id": "550e8400-e29b-41d4-a716-446655440000",
  "hub_name": "Living Room Pi",
  "hub_public_key": "base64-encoded-wireguard-public-key",
  "tunnel_ip_range": "10.0.0.0/24",
  "phone_tunnel_ip": "10.0.0.2",
  "hub_tunnel_ip": "10.0.0.1"
}
```

Both devices now store each other's:
- Device ID
- WireGuard public key
- Assigned tunnel IP addresses

#### 4. Subsequent Connections (P2P)

After pairing, devices can connect directly via P2P **even when not on the same network**:

**Without Coordination Server**:
```typescript
// Phone uses local signaling (e.g., WebRTC data channel bootstrapped via STUN)
await GhostProxy.startConnection(
  hubId,
  hubPublicKey,
  [{ urls: 'stun:stun.l.google.com:19302' }]  // Public STUN only, no TURN
);
```

**Important**: Since there's no coordination server to relay signaling messages, you need an alternative signaling mechanism:
- **Option A**: Direct IP connection (if devices are on same network or have port forwarding)
- **Option B**: Fallback signaling via public STUN/TURN servers
- **Option C**: Local signaling server on hub (WebSocket)

---

### Implementation Notes for Local Pairing

**What's In Scope for ghost-go:**
- ✅ P2P tunnel establishment (ICE + WireGuard)
- ✅ Direct connection when signaling info is provided

**What's Out of Scope (app-specific):**
- ❌ mDNS discovery
- ❌ QR code generation/scanning
- ❌ HTTP pairing API
- ❌ Key storage (use platform secure storage: Keychain/Keystore)
- ❌ Signaling mechanism (you choose: WebSocket, HTTP polling, etc.)

**Recommended Libraries:**
- **mDNS Discovery**: `github.com/hashicorp/mdns` (Go), `react-native-zeroconf` (RN)
- **QR Code**: `github.com/skip2/go-qrcode` (Go), `expo-camera` + `expo-barcode-scanner` (RN)
- **Secure Storage**: Platform Keychain/Keystore via `expo-secure-store`

---

### Security Considerations for Local Pairing

1. **Pairing Token**: Use short-lived JWT (5 min expiry) embedded in QR code
2. **HTTPS**: Use self-signed cert on hub, pin cert fingerprint in QR code
3. **Confirmation**: Require user confirmation on hub after phone scans QR
4. **Key Verification**: Display key fingerprints on both devices for manual verification
5. **Revocation**: Hub should provide API to revoke paired devices

**Example Pairing Token (Hub generates):**
```go
token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
    "hub_id": hubID,
    "exp": time.Now().Add(5 * time.Minute).Unix(),
    "iat": time.Now().Unix(),
})
tokenString, _ := token.SignedString(hubSecret)
```

---

## Dependencies

### Go (ghost-proxy)
- `golang.org/x/mobile` - Mobile bindings
- `github.com/philippseith/signalr` - SignalR client
- `ghost-go` - Core P2P library (Phase 1)

### React Native (ghost-app)
- `expo` - React Native framework
- `expo-modules-core` - Native modules
- `expo-router` - Navigation
- `@tanstack/react-query` - State management
- `expo-secure-store` - Secure key storage
- `expo-camera` (optional) - QR code scanning for local pairing

---

## Testing Strategy

### Unit Tests
- Native module bindings (Kotlin/Swift)
- TypeScript service layer
- Connection state management

### Integration Tests
- End-to-end P2P connection (two emulators)
- Proxy request forwarding
- Connection lifecycle (connect, disconnect, reconnect)

### Platform Tests
- Android: Test on emulator + physical device
- iOS: Test on simulator + physical device
- Test on different network conditions (Wi-Fi, cellular, offline)

---

## Deployment

### Development
```bash
npx expo start --dev-client
```

### Production Builds

**Android**:
```bash
eas build --platform android --profile production
```

**iOS**:
```bash
eas build --platform ios --profile production
```

**Distribution**:
- Android: Google Play Store or direct APK
- iOS: App Store or TestFlight

---

## Summary

Phase 5 delivers a consumer-facing mobile app that:
- ✅ Uses gomobile bindings (no HTTP API for control)
- ✅ Connects to .NET backend for account-based device management
- ✅ Establishes P2P tunnels via ghost-proxy → ghost-go
- ✅ Routes app traffic through local HTTP reverse proxy
- ✅ Works entirely in user space (no VPN APIs required)
- ✅ Supports local network pairing as an alternative use case

**Next Steps**: Implement Phase 2a (.NET backend) and Phase 2b (ghost-proxy) before starting mobile app development.
