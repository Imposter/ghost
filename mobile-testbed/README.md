# Ghost Mobile Testbed

React Native Expo app for testing the ghost-go mobile bindings on Android and iOS.

## Overview

This testbed validates that the ghost-go core library works correctly on mobile platforms. It provides:

- ICE candidate gathering and exchange via QR codes
- WireGuard key generation and management
- Tunnel establishment with a desktop peer
- HTTP request testing through the encrypted tunnel
- Connection state monitoring and error handling

## Prerequisites

- Node.js 18+
- npm or yarn
- Android Studio (for Android development)
- Xcode (for iOS development, macOS only)
- Go 1.21+ with gomobile

## Setup

### 1. Build the Go Mobile Library

```bash
cd ../ghost-go
make android  # Build ghost.aar for Android
# make ios    # Build Mobile.xcframework for iOS (macOS only)
```

### 2. Install Dependencies

```bash
cd mobile-testbed
npm install
```

### 3. Generate Native Projects

```bash
npx expo prebuild --clean
```

### 4. Run the App

```bash
# Android
npx expo run:android

# iOS (macOS only)
npx expo run:ios
```

## Usage

### Connection Flow

1. **Start Desktop Peer**: Run `go run ./cmd/mobile-demo` in the ghost-go directory
2. **Generate Keys**: The app generates WireGuard keys on startup
3. **Start Gathering**: Tap to begin ICE candidate gathering
4. **Exchange Data**:
   - Copy your signaling data and send to desktop peer
   - Paste the desktop's signaling data
5. **Connect**: Establish the ICE connection
6. **Start Tunnel**: Create the WireGuard tunnel
7. **Test**: Make HTTP requests through the encrypted tunnel

### Reconnection Behavior

When the connection is lost, the app shows a "Connection Lost" screen with two options:

| Button | When to Use |
|--------|-------------|
| **Reconnect** | Try first - works if the disconnect was temporary (network hiccup) |
| **Start New Session** | Required when the ICE agent has failed - creates new keys and requires re-exchanging signaling data |

**ICE State Behavior:**
- `disconnected`: Temporary - quick reconnect may work
- `failed`: Terminal - must start new session
- `closed`: Terminal - must start new session

## Project Structure

```
mobile-testbed/
├── src/
│   ├── App.tsx                    # Navigation setup
│   ├── hooks/
│   │   └── useGhostClient.ts      # React hook for ghost client
│   └── components/
│       ├── HomeScreen.tsx         # Welcome screen
│       ├── ConnectionScreen.tsx   # QR/signaling exchange
│       └── TestScreen.tsx         # HTTP testing UI
├── native-src/
│   ├── android/
│   │   └── GhostModule.kt         # Android native module
│   └── ios/
│       ├── GhostModule.swift      # iOS native module
│       └── GhostModule.m          # Objective-C bridge
├── package.json
├── app.json                       # Expo configuration
└── tsconfig.json
```

## Native Module API

The `GhostModule` native module exposes these methods to JavaScript:

| Method | Description |
|--------|-------------|
| `newClient(stunServers)` | Create a new ghost client |
| `generateWireGuardKey()` | Generate WireGuard keypair (returns JSON) |
| `startGathering()` | Start ICE candidate gathering |
| `getSignalingData()` | Get local signaling data for exchange |
| `setSignalingData(json)` | Set peer's signaling data |
| `connect(isControlling)` | Establish ICE connection |
| `startTunnel()` | Start WireGuard tunnel |
| `getConnectionState()` | Get current connection state (JSON) |
| `httpGet(url)` | Make HTTP GET through tunnel |
| `httpPost(url, contentType, body)` | Make HTTP POST through tunnel |
| `close()` | Clean up all resources |

## Testing with Desktop Peer

### Start Desktop Demo

```bash
cd ghost-go
go run ./cmd/mobile-demo
```

The desktop will:
1. Display its signaling data (copy this to the mobile app)
2. Wait for the mobile's signaling data
3. Start an HTTP server on 10.0.0.1:8080 when connected

### Virtual Network

| Peer | IP Address |
|------|------------|
| Desktop | 10.0.0.1 |
| Mobile | 10.0.0.2 |

The mobile app can reach the desktop's HTTP server at `http://10.0.0.1:8080`.

## Troubleshooting

### "ICE agent is not usable" Error

This means the ICE agent has entered a terminal state (failed or closed). Tap "Start New Session" to create a new ICE agent and re-exchange signaling data.

### Connection Lost After WiFi Toggle

When WiFi is turned off and back on:
1. The app detects the disconnect and shows "Connection Lost"
2. Try "Reconnect" first - it may work if the disconnect was brief
3. If reconnect fails, tap "Start New Session"

### Build Errors

If you see build errors after updating the Go library:
```bash
# Clean and rebuild
cd ghost-go && make clean && make android
cd ../mobile-testbed
npx expo prebuild --clean
npx expo run:android
```

## Limitations

- **No System VPN**: Uses userspace networking only (no TUN device)
- **Manual Signaling**: Requires copy/paste or QR code exchange
- **Single Peer**: One connection at a time
- **Foreground Only**: No background operation

## Related Documentation

- [ghost-go/mobile/README.md](../ghost-go/mobile/README.md) - Go mobile API documentation
- [docs/phase-1b-mobile-testbed.md](../docs/phase-1b-mobile-testbed.md) - Phase 1b design document
