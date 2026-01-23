# Phase 1b: Mobile Test Bed (React Native Expo)

**Status**: ✅ COMPLETE
**Last Updated**: January 21, 2026
**Repository**: `mobile-testbed/`, `ghost-go/mobile/`

## Overview

This phase creates a **minimal MVP React Native Expo app** to validate the ghost-go core library on mobile platforms (Android/iOS). This is a testing-focused bridge between Phase 1 (desktop Go library) and Phase 5 (full mobile integration).

### What's Complete

- ✅ Go mobile bindings (`ghost-go/mobile/`) with JSON-based API
- ✅ Android AAR build via gomobile (34MB, all architectures)
- ✅ Desktop HTTP test server (`ghost-go/cmd/mobile-demo/`)
- ✅ React Native Expo testbed app (`mobile-testbed/`)
- ✅ Host-based integration tests (`ghost-go/tests/`)
- ✅ Minimal Android/iOS native test app structures
- ✅ Build system with Makefile

### Purpose

- ✅ Validate ghost-go core works on mobile platforms
- ✅ Create Go-to-JavaScript bindings using gomobile
- ✅ Provide interactive demo for Android/iOS
- ✅ Establish automated testing patterns for mobile
- ✅ Identify platform-specific issues early

### What This Is NOT

- ❌ NOT a production-ready app
- ❌ NOT full VpnService/NEPacketTunnelProvider integration
- ❌ NOT a polished UI/UX (minimal interface only)
- ❌ NOT feature-complete (testing core functionality only)

---

## Scope

### Minimal Features

1. **ICE Connection Test**
   - Gather ICE candidates on mobile
   - Display candidates in UI
   - Manual candidate exchange (copy/paste or QR code)
   - Establish ICE connection with desktop peer

2. **WireGuard Key Management**
   - Generate WireGuard keypair on device
   - Display public key
   - Accept peer public key (manual input or QR code)

3. **Userspace Network Stack**
   - WireGuard device with userspace networking (no TUN devices)
   - Virtual IP addresses (desktop: 10.0.0.1, mobile: 10.0.0.2)
   - Application-level network access via Go net.Conn/HTTP

4. **Data Transfer Validation**
   - HTTP server running on desktop peer (10.0.0.1:8080)
   - HTTP client on mobile peer
   - Test GET request through tunnel
   - Display response in UI (proves end-to-end connectivity)

5. **Connection Status**
   - Show connection state (disconnected, gathering, connecting, connected, data_transfer)
   - Display selected candidate pair
   - Show tunnel statistics (bytes sent/received)
   - Display HTTP test results

6. **Error Handling**
   - Display errors from Go layer
   - Proper cleanup on app backgrounding
   - Graceful shutdown

### Out of Scope

- ❌ TUN devices (no kernel-level networking)
- ❌ VpnService integration (Android system VPN)
- ❌ NEPacketTunnelProvider integration (iOS system VPN)
- ❌ Automatic reconnection
- ❌ Production UI/UX
- ❌ Full VPN routing (all traffic through tunnel)
- ❌ Split tunneling
- ❌ DNS configuration
- ❌ Raw IP packet routing

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                React Native Expo App                     │
│  ┌──────────────────────────────────────────────────┐   │
│  │          JavaScript Layer (UI)                   │   │
│  │  - Connection controls                           │   │
│  │  - Status display                                │   │
│  │  - QR code scanner/generator                     │   │
│  │  - HTTP test button                              │   │
│  └────────────────────┬─────────────────────────────┘   │
│                       │ React Native Bridge             │
│  ┌────────────────────▼─────────────────────────────┐   │
│  │       Native Module (Kotlin/Swift)               │   │
│  │  - Exposes Go functions to JavaScript            │   │
│  │  - Manages Go goroutine lifecycle                │   │
│  │  - Handles callbacks and events                  │   │
│  └────────────────────┬─────────────────────────────┘   │
│                       │ JNI (Android) / cgo (iOS)       │
│  ┌────────────────────▼─────────────────────────────┐   │
│  │        Go Mobile Bindings (gomobile)             │   │
│  │  - Exported Go functions                         │   │
│  │  - Event callbacks (candidates, status)          │   │
│  │  - HTTP client through tunnel                    │   │
│  └────────────────────┬─────────────────────────────┘   │
│                       │                                 │
│  ┌────────────────────▼─────────────────────────────┐   │
│  │          ghost-go Core Library                   │   │
│  │  - ICE agent (pion/ice)                          │   │
│  │  - ICEBind adapter                               │   │
│  │  - WireGuard device (userspace, NO TUN)          │   │
│  │  - Userspace TCP/IP stack (gvisor netstack)      │   │
│  │  - Key generation                                │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘

Data Flow (Userspace Networking - No TUN Devices):
┌─────────────────────────────────────────────────────────┐
│                                                         │
│   Mobile App                       Desktop Peer         │
│   ──────────                       ────────────         │
│                                                         │
│   HTTP Client                      HTTP Server          │
│       │                                ▲                │
│       ▼                                │                │
│   ┌─────────────┐              ┌─────────────┐         │
│   │ Userspace   │              │ Userspace   │         │
│   │ TCP/IP Stack│              │ TCP/IP Stack│         │
│   │ (netstack)  │              │ (netstack)  │         │
│   │ 10.0.0.2    │              │ 10.0.0.1    │         │
│   └──────┬──────┘              └──────┬──────┘         │
│          │                            │                 │
│          ▼                            ▼                 │
│   ┌─────────────┐              ┌─────────────┐         │
│   │ WireGuard   │◄────────────►│ WireGuard   │         │
│   │ (encrypt)   │   Encrypted  │ (encrypt)   │         │
│   └──────┬──────┘    Packets   └──────┬──────┘         │
│          │                            │                 │
│          ▼                            ▼                 │
│   ┌─────────────┐              ┌─────────────┐         │
│   │ ICE Transport◄────────────►│ ICE Transport         │
│   │ (UDP/NAT)   │   Traversal  │ (UDP/NAT)   │         │
│   └─────────────┘              └─────────────┘         │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## Components

### 1. Go Mobile Bindings (`ghost-go/mobile/`)

Create gomobile-compatible API that exposes core functionality.

#### File Structure (Implemented)

```
ghost-go/
├── mobile/                      # Go mobile bindings
│   ├── mobile.go               # GhostClient implementation (~550 lines)
│   ├── callbacks.go            # Event callback interface (~60 lines)
│   ├── types.go                # JSON types and constants (~120 lines)
│   ├── conns.go                # Connection pool for Dial API (~80 lines)
│   ├── errors.go               # Mobile-specific errors (~40 lines)
│   └── mobile_test.go          # Unit tests (25+ tests)
├── tests/                       # Host-based integration tests
│   ├── harness.go              # TestHarness for two-peer testing (~200 lines)
│   └── integration_test.go     # Full tunnel tests (~350 lines)
├── cmd/mobile-demo/            # Desktop HTTP test server
│   ├── main.go                 # Entry point (~400 lines)
│   └── server.go               # HTTP test endpoints (~100 lines)
├── build/                       # Build outputs (gitignored)
│   ├── ghost.aar               # Android library
│   └── ghost-sources.jar       # Android sources
├── Makefile                     # Build system
└── .gitignore

mobile-testbed/                  # React Native Expo app
├── src/
│   ├── App.tsx                 # Navigation setup
│   ├── hooks/
│   │   └── useGhostClient.ts   # React hook for ghost client
│   └── components/
│       ├── HomeScreen.tsx      # Home screen
│       ├── ConnectionScreen.tsx # QR/signaling exchange
│       └── TestScreen.tsx      # HTTP testing UI
├── package.json
├── app.json
├── tsconfig.json
├── babel.config.js
├── metro.config.js
└── index.js
```

#### API Surface (mobile.go)

```go
// Package mobile provides gomobile bindings for ghost-go
package mobile

import (
    "context"

    "ghost-go/internal/ice"
    "ghost-go/internal/wireguard"
)

// GhostClient is the main mobile API
type GhostClient struct {
    agent      ice.Agent
    ctx        context.Context
    cancel     context.CancelFunc
    eventChan  chan Event
}

// NewClient creates a new Ghost client for mobile
func NewClient(stunServers string) (*GhostClient, error)

// Event types for callbacks
type Event struct {
    Type    string  // "candidate", "state_change", "error"
    Data    string  // JSON-encoded data
}

// Core API Methods

// StartGathering begins ICE candidate gathering
// Returns immediately, sends candidates via events
func (c *GhostClient) StartGathering() error

// GetLocalCredentials returns ICE ufrag and pwd as JSON
func (c *GhostClient) GetLocalCredentials() string

// SetRemoteCredentials accepts peer's ICE credentials (JSON)
func (c *GhostClient) SetRemoteCredentials(jsonData string) error

// AddRemoteCandidate adds a remote ICE candidate (JSON)
func (c *GhostClient) AddRemoteCandidate(jsonData string) error

// Connect starts ICE connection establishment
func (c *GhostClient) Connect(isControlling bool) error

// GetConnectionState returns current state as JSON
// { "state": "connected", "localCandidate": {...}, "remoteCandidate": {...} }
func (c *GhostClient) GetConnectionState() string

// GenerateWireGuardKey generates and returns a WireGuard keypair (JSON)
// { "privateKey": "hex...", "publicKey": "hex..." }
func (c *GhostClient) GenerateWireGuardKey() string

// SetWireGuardPeer configures the WireGuard peer (JSON)
func (c *GhostClient) SetWireGuardPeer(jsonConfig string) error

// SetLocalIP configures the local virtual IP address for userspace networking
// ipAddress: "10.0.0.2/24" (CIDR notation)
func (c *GhostClient) SetLocalIP(ipAddress string) error

// GetTunnelStats returns bytes sent/received as JSON
// { "bytesSent": 12345, "bytesReceived": 67890 }
func (c *GhostClient) GetTunnelStats() string

// HTTPGet performs an HTTP GET request through the WireGuard tunnel
// Uses userspace networking (no TUN device required)
// Returns JSON: { "success": true, "statusCode": 200, "body": "...", "latencyMs": 123 }
func (c *GhostClient) HTTPGet(url string) string

// HTTPPost performs an HTTP POST request through the WireGuard tunnel
// Returns JSON: { "success": true, "statusCode": 200, "body": "...", "latencyMs": 123 }
func (c *GhostClient) HTTPPost(url string, contentType string, body string) string

// Dial creates a net.Conn through the WireGuard tunnel (for advanced use)
// Returns a connection ID that can be used with Read/Write/Close methods
func (c *GhostClient) Dial(network string, address string) (int64, error)

// ConnRead reads from an established connection
func (c *GhostClient) ConnRead(connID int64, maxBytes int) ([]byte, error)

// ConnWrite writes to an established connection
func (c *GhostClient) ConnWrite(connID int64, data []byte) (int, error)

// ConnClose closes an established connection
func (c *GhostClient) ConnClose(connID int64) error

// Close cleans up all resources
func (c *GhostClient) Close() error

// Event Callbacks

// SetEventCallback sets the callback for events
// NOTE: gomobile doesn't support Go interfaces, so we use a global callback
func SetEventCallback(callback EventCallback)

// EventCallback is called when events occur
type EventCallback interface {
    OnEvent(eventJSON string)
}
```

#### Key Design Decisions

1. **JSON for Complex Data**: gomobile doesn't handle complex Go types well, use JSON strings
2. **Event-Based Callbacks**: ICE candidate gathering is async, use events
3. **Simple State Machine**: Expose minimal state (disconnected, gathering, connecting, connected)
4. **Userspace Networking**: Uses ghost-go's `CreateNetTUN()` (see [Phase 1 Section 1.5](phase-1-core-infrastructure.md#15-userspace-networking-internalwireguardtun_netstackgo))
   - No kernel-level networking, no root/admin permissions
   - Returns `tun.Device` + `*Net` with standard Go interfaces
   - Mobile app uses `tunNet.DialContext()` for HTTP client
   - Desktop uses `tunNet.ListenTCP()` for HTTP server
5. **Virtual IP Addresses**: Assigned in userspace (no OS configuration needed)
   - Desktop: 10.0.0.1/24
   - Mobile: 10.0.0.2/24

#### Implementation: Userspace Network Stack

The mobile bindings use ghost-go's userspace networking (Phase 1, Section 1.5). The `GhostClient` wraps the core library:

```go
// GhostClient wraps ghost-go's userspace networking for mobile
type GhostClient struct {
    iceAgent  *ice.Agent
    wgDevice  *device.Device
    tunNet    *wireguard.Net   // From ghost-go's CreateNetTUN
    // ...
}

// Setup uses ghost-go core library
func (c *GhostClient) setupTunnel() error {
    // Use ghost-go's userspace networking (Phase 1, Section 1.5)
    tunDev, tunNet, err := wireguard.CreateNetTUN(
        []netip.Addr{c.localIP},  // 10.0.0.2
        []netip.Addr{},            // DNS (optional)
        1420,                      // MTU
    )
    if err != nil {
        return err
    }

    // Standard ghost-go WireGuard setup
    c.wgDevice = device.NewDevice(tunDev, c.iceBind, logger)
    c.tunNet = tunNet  // Store for DialContext/ListenTCP
    return nil
}

// HTTPGet wraps tunNet.DialContext for mobile API (returns JSON)
func (c *GhostClient) HTTPGet(url string) string {
    client := &http.Client{
        Transport: &http.Transport{
            DialContext: c.tunNet.DialContext,
        },
    }
    // ... make request, return JSON result
}
```

See [Phase 1 Section 1.5](phase-1-core-infrastructure.md#15-userspace-networking-internalwireguardtun_netstackgo) for full API documentation and data flow diagrams.

### 2. React Native Native Modules

#### Android Module (Kotlin)

```
mobile-testbed/
└── android/
    └── app/
        └── src/
            └── main/
                └── java/com/ghosttestbed/
                    ├── GhostModule.kt        # React Native bridge
                    ├── GhostEventEmitter.kt  # Event handling
                    └── ghost.aar             # gomobile output
```

**GhostModule.kt**:
```kotlin
class GhostModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext), EventCallback {

    private var client: GhostClient? = null

    @ReactMethod
    fun createClient(stunServers: String, promise: Promise) {
        try {
            client = Mobile.newClient(stunServers)
            Mobile.setEventCallback(this)
            promise.resolve(true)
        } catch (e: Exception) {
            promise.reject("ERROR", e.message)
        }
    }

    @ReactMethod
    fun startGathering(promise: Promise) {
        client?.startGathering()
        promise.resolve(true)
    }

    @ReactMethod
    fun getLocalCredentials(promise: Promise) {
        val creds = client?.getLocalCredentials()
        promise.resolve(creds)
    }

    // Implement EventCallback
    override fun onEvent(eventJSON: String) {
        // Send to JavaScript via DeviceEventEmitter
        reactApplicationContext
            .getJSModule(DeviceEventEmitter::class.java)
            .emit("GhostEvent", eventJSON)
    }

    // ... other methods
}
```

#### iOS Module (Swift)

```
mobile-testbed/
└── ios/
    └── GhostTestbed/
        ├── GhostBridge.swift         # React Native bridge
        ├── GhostEventHandler.swift   # Event handling
        └── Mobile.xcframework        # gomobile output
```

**GhostBridge.swift**:
```swift
@objc(GhostModule)
class GhostModule: RCTEventEmitter, MobileEventCallbackProtocol {

    private var client: MobileGhostClient?

    @objc
    func createClient(_ stunServers: String,
                     resolver resolve: @escaping RCTPromiseResolveBlock,
                     rejecter reject: @escaping RCTPromiseRejectBlock) {
        do {
            client = try MobileNewClient(stunServers)
            MobileSetEventCallback(self)
            resolve(true)
        } catch {
            reject("ERROR", error.localizedDescription, error)
        }
    }

    @objc
    func startGathering(_ resolve: @escaping RCTPromiseResolveBlock,
                       rejecter reject: @escaping RCTPromiseRejectBlock) {
        try? client?.startGathering()
        resolve(true)
    }

    // Implement MobileEventCallbackProtocol
    func onEvent(_ eventJSON: String?) {
        sendEvent(withName: "GhostEvent", body: eventJSON)
    }

    // Required for RCTEventEmitter
    override func supportedEvents() -> [String]! {
        return ["GhostEvent"]
    }

    // ... other methods
}
```

### 3. React Native UI (TypeScript)

```
mobile-testbed/
└── src/
    ├── App.tsx                  # Main app component
    ├── components/
    │   ├── ConnectionControl.tsx    # Start/stop buttons
    │   ├── StatusDisplay.tsx        # Connection status
    │   ├── CandidateList.tsx        # ICE candidates
    │   └── QRCodeExchange.tsx       # QR code for signaling
    ├── hooks/
    │   └── useGhostClient.ts        # Ghost client hook
    └── types/
        └── ghost.d.ts               # TypeScript types
```

#### Minimal UI Components

**useGhostClient.ts**:
```typescript
import { useEffect, useState } from 'react';
import { NativeModules, NativeEventEmitter } from 'react-native';

const { GhostModule } = NativeModules;
const eventEmitter = new NativeEventEmitter(GhostModule);

export type ConnectionState = 'disconnected' | 'gathering' | 'connecting' | 'connected';

export function useGhostClient() {
  const [state, setState] = useState<ConnectionState>('disconnected');
  const [candidates, setCandidates] = useState<any[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Create client
    GhostModule.createClient('stun:stun.l.google.com:19302')
      .catch((e: Error) => setError(e.message));

    // Listen for events
    const subscription = eventEmitter.addListener('GhostEvent', (eventJSON: string) => {
      const event = JSON.parse(eventJSON);

      switch (event.type) {
        case 'candidate':
          setCandidates(prev => [...prev, JSON.parse(event.data)]);
          break;
        case 'state_change':
          setState(event.data);
          break;
        case 'error':
          setError(event.data);
          break;
      }
    });

    return () => subscription.remove();
  }, []);

  const startGathering = async () => {
    await GhostModule.startGathering();
    setState('gathering');
  };

  const connect = async (isControlling: boolean) => {
    await GhostModule.connect(isControlling);
    setState('connecting');
  };

  // HTTP request through userspace networking (no TUN device)
  const httpGet = async (url: string): Promise<{
    success: boolean;
    statusCode?: number;
    body?: string;
    latencyMs?: number;
    error?: string;
  }> => {
    const resultJson = await GhostModule.httpGet(url);
    return JSON.parse(resultJson);
  };

  const getTunnelStats = async (): Promise<{ bytesSent: number; bytesReceived: number }> => {
    const statsJson = await GhostModule.getTunnelStats();
    return JSON.parse(statsJson);
  };

  return { state, candidates, error, startGathering, connect, httpGet, getTunnelStats };
}
```

**App.tsx** (Minimal UI):
```typescript
import React, { useState, useEffect } from 'react';
import { View, Text, Button, ScrollView, StyleSheet } from 'react-native';
import { useGhostClient } from './hooks/useGhostClient';

export default function App() {
  const { state, candidates, error, startGathering, connect, httpGet, getTunnelStats } = useGhostClient();
  const [httpResult, setHttpResult] = useState<string | null>(null);
  const [tunnelStats, setTunnelStats] = useState({ bytesSent: 0, bytesReceived: 0 });

  // Poll tunnel stats when connected
  useEffect(() => {
    if (state !== 'connected') return;
    const interval = setInterval(async () => {
      const stats = await getTunnelStats();
      setTunnelStats(stats);
    }, 1000);
    return () => clearInterval(interval);
  }, [state]);

  const handleTestHTTP = async () => {
    // HTTP request goes through userspace WireGuard tunnel (no TUN device)
    const result = await httpGet('http://10.0.0.1:8080/test');
    setHttpResult(result.success
      ? `✓ ${result.statusCode} - ${result.body} (${result.latencyMs}ms)`
      : `✗ Error: ${result.error}`
    );
  };

  return (
    <View style={styles.container}>
      <Text style={styles.title}>Ghost-GO Mobile Test</Text>

      <Text style={styles.status}>Status: {state}</Text>

      {error && <Text style={styles.error}>{error}</Text>}

      <View style={styles.controls}>
        <Button
          title="Start Gathering"
          onPress={startGathering}
          disabled={state !== 'disconnected'}
        />
        <Button
          title="Connect"
          onPress={() => connect(false)} // Mobile is controlled peer
          disabled={state !== 'gathering'}
        />
        <Button
          title="Test HTTP"
          onPress={handleTestHTTP}
          disabled={state !== 'connected'}
        />
      </View>

      {/* Tunnel Statistics */}
      {state === 'connected' && (
        <View style={styles.statsBox}>
          <Text style={styles.statsTitle}>Tunnel Statistics</Text>
          <Text style={styles.statText}>Sent: {tunnelStats.bytesSent} bytes</Text>
          <Text style={styles.statText}>Received: {tunnelStats.bytesReceived} bytes</Text>
        </View>
      )}

      {/* HTTP Test Result */}
      {httpResult && (
        <View style={styles.resultBox}>
          <Text style={styles.resultTitle}>HTTP Test Result</Text>
          <Text style={styles.resultText}>{httpResult}</Text>
        </View>
      )}

      <Text style={styles.subtitle}>Candidates ({candidates.length})</Text>
      <ScrollView style={styles.candidateList}>
        {candidates.map((c, i) => (
          <Text key={i} style={styles.candidate}>
            {c.type}: {c.address}:{c.port}
          </Text>
        ))}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, padding: 20, backgroundColor: '#fff' },
  title: { fontSize: 24, fontWeight: 'bold', marginBottom: 20 },
  status: { fontSize: 18, marginBottom: 10 },
  error: { color: 'red', marginBottom: 10 },
  controls: { flexDirection: 'row', gap: 10, marginBottom: 20, flexWrap: 'wrap' },
  statsBox: { backgroundColor: '#e3f2fd', padding: 10, borderRadius: 8, marginBottom: 10 },
  statsTitle: { fontSize: 16, fontWeight: 'bold', marginBottom: 5 },
  statText: { fontSize: 14, fontFamily: 'monospace' },
  resultBox: { backgroundColor: '#f1f8e9', padding: 10, borderRadius: 8, marginBottom: 10 },
  resultTitle: { fontSize: 16, fontWeight: 'bold', marginBottom: 5 },
  resultText: { fontSize: 14, fontFamily: 'monospace', color: '#33691e' },
  subtitle: { fontSize: 16, fontWeight: 'bold', marginBottom: 10 },
  candidateList: { flex: 1, borderWidth: 1, borderColor: '#ccc', padding: 10 },
  candidate: { fontSize: 12, fontFamily: 'monospace', marginBottom: 5 },
});
```

---

### 4. Desktop HTTP Test Server (Go)

The desktop peer (server side) runs an HTTP server on the userspace network stack to handle requests from the mobile client. This is the **key verification mechanism** that proves end-to-end connectivity through the WireGuard tunnel.

**Purpose:**
- Validates that the mobile client can reach the server through the encrypted tunnel
- Confirms WireGuard handshake completed successfully
- Provides measurable test data (latency, response content)
- Logs incoming requests for debugging

**File**: `ghost-go/cmd/mobile-demo/server.go`

```go
package main

import (
    "encoding/json"
    "log"
    "net"
    "net/http"
    "time"

    "golang.zx2c4.com/wireguard/tun/netstack"
)

// TestServer wraps the HTTP server running on userspace networking
type TestServer struct {
    tunNet   *netstack.Net
    listener net.Listener
    mux      *http.ServeMux
}

// NewTestServer creates a test HTTP server on the userspace network
// The server listens on the virtual IP (10.0.0.1:8080) - NOT on the OS network
func NewTestServer(tunNet *netstack.Net, ip string, port int) (*TestServer, error) {
    mux := http.NewServeMux()

    // Test endpoint - proves end-to-end tunnel connectivity
    mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
        response := map[string]interface{}{
            "success":   true,
            "message":   "Hello from desktop peer via WireGuard tunnel!",
            "timestamp": time.Now().Unix(),
            "server":    "ghost-go-desktop",
            "clientIP":  r.RemoteAddr,
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(response)

        log.Printf("✓ HTTP request received from %s via tunnel", r.RemoteAddr)
    })

    // Echo endpoint - returns request body (for testing POST)
    mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
        io.Copy(w, r.Body)
        log.Printf("✓ Echo request from %s", r.RemoteAddr)
    })

    // Health check
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    })

    // Listen on userspace network (not OS network!)
    listener, err := tunNet.ListenTCP(&net.TCPAddr{
        IP:   net.ParseIP(ip),
        Port: port,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to listen on userspace network: %w", err)
    }

    return &TestServer{
        tunNet:   tunNet,
        listener: listener,
        mux:      mux,
    }, nil
}

// Start begins serving HTTP requests
func (s *TestServer) Start() error {
    log.Printf("HTTP test server listening on userspace network")
    return http.Serve(s.listener, s.mux)
}

// Close shuts down the server
func (s *TestServer) Close() error {
    return s.listener.Close()
}
```

**Updated Desktop Demo** (`cmd/mobile-demo/main.go`):
```go
func main() {
    // ... ICE and WireGuard setup ...

    // After WireGuard handshake completes, start the test HTTP server
    // This server runs on the USERSPACE network (10.0.0.1), not the OS network
    testServer, err := NewTestServer(client.tunNet, "10.0.0.1", 8080)
    if err != nil {
        log.Fatalf("Failed to create test server: %v", err)
    }

    // Start server in background
    go func() {
        log.Println("✓ HTTP test server running on http://10.0.0.1:8080 (userspace)")
        log.Println("  Endpoints:")
        log.Println("    GET  /test   - Returns JSON with timestamp (connectivity test)")
        log.Println("    POST /echo   - Echoes request body (data transfer test)")
        log.Println("    GET  /health - Returns 'OK' (health check)")
        if err := testServer.Start(); err != nil {
            log.Printf("Test server error: %v", err)
        }
    }()

    log.Println("")
    log.Println("Mobile peer can now test connectivity!")
    log.Println("No TUN device or root access required - all networking is userspace!")

    // Wait for shutdown signal
    // ...
}
```

**Test Verification Flow:**
```
┌──────────────────┐                    ┌──────────────────┐
│   Mobile Client  │                    │  Desktop Server  │
│   (10.0.0.2)     │                    │   (10.0.0.1)     │
├──────────────────┤                    ├──────────────────┤
│                  │                    │                  │
│  httpGet(        │  HTTP GET /test    │  TestServer      │
│   "http://       │ =================> │  listening on    │
│    10.0.0.1:     │  (through WG       │  userspace       │
│    8080/test"    │   tunnel)          │  10.0.0.1:8080   │
│  )               │                    │                  │
│                  │  {"success":true,  │                  │
│  Result:         │   "message":"...", │                  │
│  ✓ success=true  │ <================= │                  │
│  ✓ latency=42ms  │   "timestamp":...} │                  │
│                  │                    │                  │
└──────────────────┘                    └──────────────────┘

This proves:
✓ ICE connection established
✓ WireGuard handshake completed
✓ Encryption working (WireGuard)
✓ Userspace TCP/IP stack working
✓ HTTP layer functional end-to-end
```

---

## Usage Example

### Complete Flow: Desktop + Mobile

**Step 1: Start Desktop Peer**
```bash
cd ghost-go
go run ./cmd/mobile-demo -role server  # No sudo needed!

# Output:
# ✓ ICE agent created
# ✓ Gathering candidates...
# ✓ WireGuard keys generated
# ✓ Userspace networking initialized (10.0.0.1/24)
# ✓ HTTP server running on http://10.0.0.1:8080 (userspace)
#
# === Your Signaling Data (QR Code) ===
# [QR code displayed]
#
# Waiting for mobile peer...
# Note: No TUN device or root access required!
```

**Step 2: Start Mobile App**
```
1. Launch mobile-testbed app
2. Tap "Start Gathering"
3. App displays QR code with mobile signaling data
4. Scan desktop's QR code with mobile (or copy/paste JSON)
```

**Step 3: Desktop Scans Mobile QR**
```
Desktop sees mobile's QR code → scans it
# ✓ Remote peer data received
# ✓ ICE connection established
# ✓ WireGuard handshake complete
# Ready for data transfer!
```

**Step 4: Test HTTP Through Tunnel**
```
Mobile app:
1. Connection status shows "connected"
2. Tap "Test HTTP" button
3. App sends GET request to http://10.0.0.1:8080/test
4. Response displayed:
   {
     "success": true,
     "message": "Hello from desktop peer via WireGuard tunnel!",
     "timestamp": 1738123456
   }

Desktop logs:
# HTTP request from 10.0.0.2:54321 via tunnel
# ✓ Data transfer successful!

Mobile displays:
✓ HTTP Test Successful
Tunnel Stats:
  Sent: 245 bytes
  Received: 1,234 bytes
```

**Success!** End-to-end encrypted data transfer through ICE + WireGuard tunnel confirmed!

---

## Implementation Steps

### Step 1: Setup gomobile Bindings

```bash
# Install gomobile
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
```

### Step 2: Build Mobile Libraries

Use the Makefile in `ghost-go/`:

```bash
cd ghost-go

# Build Android AAR (auto-detects NDK on Windows)
make android

# Build iOS XCFramework (macOS only)
make ios

# Run tests
make test

# Clean build artifacts
make clean

# Show all targets
make help
```

**Build outputs go to `ghost-go/build/`:**
- `build/ghost.aar` - Android library (34MB, all architectures)
- `build/ghost-sources.jar` - Android sources
- `build/Mobile.xcframework/` - iOS framework (when built on macOS)

**Important Build Note:**

The build uses `-ldflags="-checklinkname=0"` to work around a Go 1.23+ linker compatibility issue with `github.com/wlynxg/anet` (used by pion/transport for Android networking). This flag disables the linker check for internal symbol references.

### Step 3: Setup React Native Expo Project

```bash
npx create-expo-app mobile-testbed --template blank-typescript
cd mobile-testbed

# Prebuild for native modules
npx expo prebuild
```

### Step 4: Add Native Modules

1. Copy `ghost.aar` to `android/app/libs/`
2. Copy `Mobile.xcframework` to `ios/`
3. Create native modules (GhostModule.kt, GhostBridge.swift)
4. Update build configurations

**android/app/build.gradle**:
```gradle
dependencies {
    implementation files('libs/ghost.aar')
    // ... other deps
}
```

**ios/Podfile**:
```ruby
pod 'Mobile', :path => '../Mobile.xcframework'
```

### Step 5: Create TypeScript Bindings

Create `src/hooks/useGhostClient.ts` and basic UI components.

### Step 6: Testing Strategy

#### Manual Testing
1. Run app on Android device/emulator
2. Run app on iOS device/simulator
3. Run desktop demo peer (`cmd/demo`)
4. Test ICE connection establishment

#### Automated Testing

**Unit Tests (Go)**:
```bash
cd ghost-go/mobile
go test -v .
```

**Integration Tests (Detox/Appium)**:
```typescript
// e2e/connection.test.ts
describe('Ghost Connection', () => {
  it('should gather ICE candidates', async () => {
    await element(by.id('start-gathering')).tap();
    await waitFor(element(by.id('candidate-list')))
      .toBeVisible()
      .withTimeout(15000);
    // Verify candidates appeared
  });

  it('should establish connection', async () => {
    // Setup mock peer
    // Exchange signaling data
    // Verify connected state
  });
});
```

**Snapshot Tests (Jest)**:
```bash
npm test -- --updateSnapshot
```

---

## Testing Checklist

### ICE Functionality (Host Tests ✅)
- [x] ICE agent creation (tested in host integration tests)
- [x] Candidate gathering (host candidates)
- [x] STUN server connectivity (srflx candidates)
- [x] Credential exchange
- [x] Connection establishment between peers
- [x] Selected candidate pair reported correctly
- [ ] ICE agent on Android device (requires device testing)
- [ ] ICE agent on iOS device (requires device testing)

### WireGuard Functionality (Host Tests ✅)
- [x] Key generation
- [x] Public key export
- [x] Key validation (32 bytes, non-zero)
- [x] Userspace network stack initialization (no TUN device)
- [x] Virtual IP address assignment
- [x] WireGuard handshake completes successfully
- [ ] Key generation on Android device (requires device testing)
- [ ] Key generation on iOS device (requires device testing)

### Data Transfer & Connectivity (Host Tests ✅)
- [x] HTTP server starts on virtual IP
- [x] Client connects through userspace stack
- [x] HTTP GET request succeeds via userspace networking
- [x] Response data received correctly
- [x] Tunnel statistics update (bytes sent/received)
- [x] Multiple HTTP requests work (connection persistence)
- [x] No TUN device or root permissions required
- [ ] Large payload transfer (>10KB) - needs device testing
- [ ] Latency validation (<500ms) - needs device testing

### Build System ✅
- [x] Android AAR builds successfully (34MB)
- [x] All Android architectures included (arm64, arm, x86_64, x86)
- [x] Makefile targets work (android, test, clean, help)
- [ ] iOS XCFramework builds (requires macOS)

### App Lifecycle (Requires Device Testing)
- [ ] Proper cleanup on app backgrounding
- [ ] Reconnection on app foregrounding
- [ ] No crashes on rapid start/stop
- [ ] Memory leaks checked (Android Profiler/Xcode Instruments)

### Error Handling (Host Tests ✅)
- [x] Invalid signaling data error
- [x] Timeout handling
- [x] ICE connection failure scenarios
- [ ] Network unavailable error (device testing)
- [ ] Graceful error display in UI (device testing)

### Platform-Specific (Requires Device Testing)
- [ ] Android 8+ compatibility
- [ ] iOS 13+ compatibility
- [x] Android permissions configured (INTERNET, ACCESS_NETWORK_STATE, CAMERA)
- [ ] iOS network privacy strings
- [ ] Battery usage acceptable (<5% drain)

---

## Deliverables

### Code
1. `ghost-go/mobile/` - gomobile bindings
2. `mobile-testbed/` - React Native Expo app
3. `mobile-testbed/android/` - Android native module
4. `mobile-testbed/ios/` - iOS native module

### Documentation
1. `mobile-testbed/README.md` - Setup and usage
2. `mobile-testbed/TESTING.md` - Testing guide
3. `docs/GOMOBILE-GUIDE.md` - gomobile best practices

### Artifacts
1. Android APK (debug build)
2. iOS IPA (ad-hoc build)
3. Test reports (Jest + Detox)

---

## Success Criteria

### Host-Based Testing ✅ COMPLETE

✅ **Go mobile package:**
- [x] Creates ICE agent and gathers candidates
- [x] Exchanges signaling data programmatically
- [x] Establishes ICE connection (controlling/controlled roles)
- [x] Reports connection state accurately
- [x] Generates WireGuard keys (Curve25519)
- [x] Initializes userspace network stack (no TUN device required)
- [x] Completes WireGuard handshake between peers
- [x] HTTP requests work through userspace tunnel
- [x] Tunnel statistics track data transfer
- [x] Handles errors gracefully (invalid credentials, timeouts)
- [x] All unit tests pass (25+ tests)
- [x] All integration tests pass
- [x] No root access or special permissions required

✅ **Build system:**
- [x] Android AAR builds successfully (34MB)
- [x] All Android architectures: armeabi-v7a, arm64-v8a, x86, x86_64
- [x] Makefile with android, ios, test, clean targets
- [x] Auto-detects Android NDK on Windows
- [x] Works around Go 1.23+ linker issues

✅ **React Native testbed:**
- [x] Expo project structure created
- [x] Navigation between screens
- [x] useGhostClient hook implementation
- [x] Connection screen for signaling exchange
- [x] Test screen for HTTP testing

### Device Testing (Phase 5 Prerequisite)

⏳ **Mobile app on device:**
- [ ] Creates ICE agent on Android
- [ ] Creates ICE agent on iOS
- [ ] Gathers ICE candidates via STUN
- [ ] Establishes connection with desktop peer
- [ ] HTTP request through tunnel succeeds
- [ ] Memory usage < 50MB
- [ ] No crashes during normal operation

### Demonstrated Feasibility ✅

- [x] gomobile for Go-to-mobile bridge
- [x] JSON-based API for cross-language compatibility
- [x] Event-based async architecture
- [x] Host-based testing patterns (no emulator needed)
- [x] End-to-end encrypted data transfer through WireGuard tunnel
- [x] Userspace networking without TUN devices (gvisor/netstack)
- [x] Cross-platform compatibility without platform-specific permissions

---

## Reconnection Behavior

ICE agents have different states that determine what action is required when a connection is lost:

### ICE States

| State | Description | Recovery Action |
|-------|-------------|-----------------|
| `disconnected` | Temporary loss (network hiccup) | Try `Connect()` - may recover |
| `failed` | Terminal failure | Must call `StartGathering()` |
| `closed` | Agent explicitly closed | Must call `StartGathering()` |

### Connect() State Validation

The `Connect()` method validates the ICE state before attempting connection:
- **Allowed**: `new`, `checking`, `connected`, `completed`, `disconnected`
- **Blocked**: `failed`, `closed` (returns error with message to call `StartGathering()`)

This prevents using a broken agent and provides clear guidance to the user.

### Mobile App Reconnection Flow

The mobile testbed implements a two-step reconnection strategy:

1. **Quick Reconnect** (try first):
   - Attempts to reconnect using the existing ICE agent
   - Works if the disconnect was temporary (e.g., brief WiFi interruption)
   - No need to re-exchange signaling data

2. **Start New Session** (when quick reconnect fails):
   - Creates a new ICE agent via `StartGathering()`
   - Generates new WireGuard keys
   - Requires re-exchanging signaling data with the peer

---

## Known Limitations (Deferred to Phase 5)

1. **No System VPN Integration**
   - Userspace networking only (application-level)
   - No VpnService (Android) or NEPacketTunnelProvider (iOS)
   - Only application-initiated traffic goes through tunnel
   - No routing of other app traffic

2. **No Automatic Signaling**
   - Manual QR code/copy-paste only
   - Phase 2 adds WebSocket signaling

3. **No Background Operation**
   - App must be in foreground
   - VPN services in Phase 5 enable background

4. **Basic UI Only**
   - Minimal test interface
   - Production UI in Phase 5

5. **Limited Routing**
   - Only direct peer-to-peer (mobile ↔ desktop)
   - No internet routing through tunnel
   - No split tunneling
   - Application must explicitly use tunnel APIs

6. **No Automatic Reconnection**
   - Application must handle reconnection manually
   - Quick reconnect may work for temporary disconnects
   - Full new session required for terminal failures

---

## Dependencies

### Go Packages
```go
// mobile/go.mod
require (
    golang.org/x/mobile v0.x.x          // gomobile bindings
    ghost-go v0.x.x                     // Core library (local replace)
)
```

The mobile bindings depend on ghost-go core, which provides:
- ICE agent (pion/ice)
- WireGuard device (wireguard-go)
- Userspace networking (gvisor/netstack via wireguard-go/tun/netstack)

See [Phase 1 Dependencies](phase-1-core-infrastructure.md#dependencies) for the full list.

### React Native Packages
```json
{
  "dependencies": {
    "expo": "~50.0.0",
    "react-native": "0.73.0",
    "react-native-qrcode-svg": "^6.2.0",  // QR code generation
    "react-native-camera": "^4.2.1"        // QR code scanning
  },
  "devDependencies": {
    "@types/react": "^18.2.0",
    "detox": "^20.0.0",                    // E2E testing
    "jest": "^29.0.0"
  }
}
```

---

## Files Status

### Go Mobile Package (`ghost-go/mobile/`)

| File | Status | Purpose | Lines |
|------|--------|---------|-------|
| `mobile.go` | ✅ Complete | GhostClient implementation | ~550 |
| `callbacks.go` | ✅ Complete | EventCallback interface | ~60 |
| `types.go` | ✅ Complete | JSON types and constants | ~120 |
| `conns.go` | ✅ Complete | Connection pool for Dial API | ~80 |
| `errors.go` | ✅ Complete | Mobile-specific errors | ~40 |
| `mobile_test.go` | ✅ Complete | Unit tests (25+ tests) | ~400 |

### Integration Tests (`ghost-go/tests/`)

| File | Status | Purpose | Lines |
|------|--------|---------|-------|
| `harness.go` | ✅ Complete | TestHarness for two-peer testing | ~200 |
| `integration_test.go` | ✅ Complete | Full tunnel tests | ~350 |

### Desktop Demo (`ghost-go/cmd/mobile-demo/`)

| File | Status | Purpose | Lines |
|------|--------|---------|-------|
| `main.go` | ✅ Complete | Desktop peer entry point | ~400 |
| `server.go` | ✅ Complete | HTTP test endpoints | ~100 |

### React Native Testbed (`mobile-testbed/`)

| File | Status | Purpose |
|------|--------|---------|
| `src/App.tsx` | ✅ Complete | Navigation setup |
| `src/hooks/useGhostClient.ts` | ✅ Complete | React hook for ghost client |
| `src/components/HomeScreen.tsx` | ✅ Complete | Home screen |
| `src/components/ConnectionScreen.tsx` | ✅ Complete | QR/signaling exchange |
| `src/components/TestScreen.tsx` | ✅ Complete | HTTP testing UI |
| `package.json` | ✅ Complete | Dependencies |
| `app.json` | ✅ Complete | Expo configuration |

### Build System

| File | Status | Purpose |
|------|--------|---------|
| `ghost-go/Makefile` | ✅ Complete | Build system (android, ios, test, clean) |
| `ghost-go/.gitignore` | ✅ Complete | Ignores build/ directory |

### Native Test Apps (Minimal Structure)

| Directory | Status | Purpose |
|-----------|--------|---------|
| `ghost-go/tests/android/` | ✅ Complete | Android app with AndroidJUnit tests |
| `ghost-go/tests/ios/` | ✅ Complete | iOS app with XCTest |

**Total Code**: ~2,300 lines of Go + ~1,500 lines of TypeScript/React Native

---

## Implementation Phases (Completed)

### Phase A: gomobile Bindings ✅
- Setup gomobile toolchain
- Implement mobile API with userspace networking
- Integrate gvisor/netstack for userspace TCP/IP
- Unit tests for mobile package
- Build .aar (iOS build requires macOS)

### Phase B: Desktop Demo + Native Test Apps ✅
- Create desktop HTTP test server
- Create minimal Android test app structure
- Create minimal iOS test app structure

### Phase C: React Native Testbed ✅
- Create Expo project
- Implement TypeScript hooks
- Build minimal UI (Home, Connection, Test screens)
- Configure navigation

---

## References

- [gomobile Documentation](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile)
- [React Native Native Modules](https://reactnative.dev/docs/native-modules-intro)
- [Android JNI Guide](https://developer.android.com/training/articles/perf-jni)
- [iOS Swift/C Interop](https://developer.apple.com/documentation/swift/imported_c_and_objective-c_apis)
- [Detox Testing](https://wix.github.io/Detox/)
- [Expo Prebuild](https://docs.expo.dev/workflow/prebuild/)
- [gvisor netstack](https://gvisor.dev/docs/user_guide/networking/) - Userspace TCP/IP stack
- [wireguard-go tun/netstack](https://github.com/WireGuard/wireguard-go/tree/master/tun/netstack) - WireGuard userspace networking
- [Tailscale tsnet](https://pkg.go.dev/tailscale.com/tsnet) - Example of userspace WireGuard networking

---

## Next Phase

**Phase 2**: Signaling & Coordination Server
- Eliminates manual signaling (QR codes)
- WebSocket-based candidate exchange
- Device discovery
- Automatic connection establishment

**Phase 5**: Full Mobile Integration
- VpnService integration (Android)
- NEPacketTunnelProvider integration (iOS)
- Actual packet routing
- Production-ready apps
