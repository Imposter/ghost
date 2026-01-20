# Ghost Implementation Plan

## Project Goal
Build a secure, peer-to-peer network tunneling solution (P2P VPN) enabling direct access to internal services behind NATs, with mobile support for iOS and Android.

## Design Decisions
1.  **Coordination Service**: Standalone centralized server (Deployment separate from library).
2.  **Relay (TURN)**: Use existing solutions (coturn/eturnal).
3.  **Mobile Strategy**: React Native with Expo (CNG/Development Builds) + Go Shared Library.
4.  **Priority**: Equal priority for Android and iOS.

---

## Phase 1: Core Infrastructure
**Goal:** Establish ICE connectivity and WireGuard tunneling.

- [ ] **ICE Agent** (`internal/ice/`): Wrapper around Pion/ICE for candidate gathering and connectivity.
- [ ] **ICEBind Adapter** (`internal/ice/bind.go`): Bridge ICE connection to WireGuard `conn.Bind`.
- [ ] **WireGuard Device** (`internal/wireguard/`): Wrapper for `wireguard-go` device management.
- [ ] **TUN Interface** (`internal/tun/`): Platform-specific TUN device creation (Linux/Windows/macOS).
- [ ] **Integration Tests**: Verify packets pass between two peers over ICE.

## Phase 2: Signaling & Coordination
**Goal:** Implement discovery and key exchange.

- [ ] **Signaling Protocol**: Define JSON-RPC or WebSocket message format.
- [ ] **Coordination Server** (`cmd/ghost-coord/`): Standalone server for peer discovery.
- [ ] **Signaling Client** (`pkg/signaling/`): WebSocket client for peers.
- [ ] **E2E Encryption**: Ensure signaling messages (SDP, WG Keys) are encrypted between peers.
- [ ] **Authentication**: Simple token-based or PKI auth for peers.

## Phase 3: Connection Management
**Goal:** Robust connection lifecycle and recovery.

- [ ] **State Machine** (`internal/state/`): Manage states (Disconnected, Connecting, Connected, Reconnecting).
- [ ] **Retry Logic**: Exponential backoff for signaling and ICE failures.
- [ ] **Keepalives**: Application-level heartbeats to detect dead tunnels.
- [ ] **Roaming**: Handle network changes (WiFi <-> LTE).

## Phase 4: HTTP Reverse Proxy
**Goal:** Forward HTTP/WebSocket traffic through the tunnel.

- [ ] **Proxy Server** (`pkg/proxy/`): HTTP reverse proxy listener on client (Supports WebSocket hijacking).
- [ ] **Virtual Resolver**: Map internal hostnames to WireGuard peer IPs.
- [ ] **Access Control**: Simple IP/Token-based ACLs for exposed services.
- [ ] **Protocol Support**: HTTP/1.1, HTTP/2, and WebSocket support.

## Phase 5: Mobile Platforms (React Native + Expo)
**Goal:** User-space mobile clients (No OS-GW VPN).

- [ ] **Go Mobile Bindings** (`cmd/ghost-mobile/`): Export functions to Start/Stop the user-space node and proxy.
- [ ] **User-Space Tunneling**: Use `wireguard-go` netstack instead of TUN device.
- [ ] **Local Proxy**: Expose `localhost` proxy for React Native usage.
- [ ] **Expo Native Module**:
    - Light wrapper around Go shared library.
    - No `VpnService` or `NetworkExtension` entitlement required.
- [ ] **React Native App**:
    - UI for connection management.
    - Routing traffic via `localhost` proxy.

## Phase 6: Observability & Testing
**Goal:** Monitoring and Stability.

- [ ] **Logging**: Structured logging (slog) with platform sinks (Logcat/os_log).
- [ ] **Metrics**: Connection stats, throughput, latency.
- [ ] **CI/CD**: GitHub Actions for building Go binaries and Mobile bundles.
- [ ] **Load Testing**: Simulate multiple peers and heavy throughput.

---

## Execution Strategy
- Sequential implementation of Phase 1-3.
- Parallel capability for Phase 4 and Phase 5 once Core is stable.
- Continuous integration testing throughout.
