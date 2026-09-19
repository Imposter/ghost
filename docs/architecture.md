# Ghost-GO Architecture

**Last Updated**: 2026-01-21

## Table of Contents

1. [System Overview](#system-overview)
2. [Core Components](#core-components)
3. [Data Flow](#data-flow)
4. [Security Architecture](#security-architecture)
5. [Phase Breakdown](#phase-breakdown)
6. [Deployment Models](#deployment-models)

---

## System Overview

Ghost-GO is a peer-to-peer VPN system that combines:
- **ICE (Interactive Connectivity Establishment)** for NAT traversal
- **WireGuard** for encrypted tunneling
- **Coordination Server** for discovery and signaling

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        User Account Layer                        │
│                    (IdP/OAuth Authentication)                    │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                ┌───────────▼────────────┐
                │  Coordination Server   │
                │  - Peer registry       │
                │  - ICE signaling       │
                │  - Discovery           │
                └───────────┬────────────┘
                            │
            ┌───────────────┴───────────────┐
            │                               │
    ┌───────▼────────┐             ┌───────▼────────┐
    │   Hub (Server) │             │  Phone (Client)│
    │   ┌─────────┐  │             │   ┌─────────┐ │
    │   │ ICE     │◄─┼─────────────┼───┤ ICE     │ │
    │   │ Agent   │  │  P2P Tunnel │   │ Agent   │ │
    │   └────┬────┘  │             │   └────┬────┘ │
    │        │       │             │        │      │
    │   ┌────▼────┐  │             │   ┌────▼────┐ │
    │   │WireGuard│  │             │   │WireGuard│ │
    │   │ Tunnel  │  │             │   │ Tunnel  │ │
    │   └────┬────┘  │             │   └────┬────┘ │
    │        │       │             │        │      │
    │   ┌────▼────┐  │             │   ┌────▼────┐ │
    │   │   TUN   │  │             │   │   TUN   │ │
    │   │Interface│  │             │   │Interface│ │
    │   └─────────┘  │             │   └─────────┘ │
    └────────────────┘             └────────────────┘
```

---

## Core Components

### 1. ICE Agent (`internal/ice/agent.go`)

**Purpose**: Establishes peer-to-peer connectivity through NAT/firewalls.

**Responsibilities**:
- Gather ICE candidates (host, server-reflexive, relay)
- Exchange candidates via coordination server
- Perform connectivity checks
- Establish `net.Conn` for WireGuard

**Key Features**:
- STUN/TURN server support
- Trickle ICE (candidates sent as discovered)
- Controlling/controlled role negotiation
- Context-aware operations

### 2. ICEBind Adapter (`internal/ice/bind.go`)

**Purpose**: Bridge between ICE connection and WireGuard.

**Critical Component**: Implements WireGuard's `conn.Bind` interface using ICE's `net.Conn`.

**Design**:
- Single endpoint (ICE is point-to-point)
- BatchSize = 1 (simplified)
- Direct packet forwarding
- No additional buffering

### 3. WireGuard Tunnel (`internal/wireguard/tunnel.go`)

**Purpose**: Encrypted tunnel management.

**Responsibilities**:
- Tunnel lifecycle (Up/Down/Close)
- Peer configuration
- IPC-based configuration (standard WireGuard protocol)
- Dynamic endpoint updates

**Key Features**:
- Thread-safe operations
- HEX-encoded keys for IPC
- Peer management
- Status monitoring

### 4. TUN Interface (`internal/wireguard/tun.go`)

**Purpose**: Virtual network interface.

**Platform Support**:
- **Desktop**: Unified implementation (wireguard-go handles OS differences)
  - Linux: `/dev/net/tun`
  - Windows: WinTun driver
  - macOS: `utun` interfaces
- **Mobile** (Phase 5):
  - Android: VpnService file descriptor
  - iOS: NEPacketTunnelProvider packet flow

### 5. Coordination Server (Phase 2)

**Purpose**: Central coordination for discovery and signaling.

**Responsibilities**:
- User authentication (JWT/OAuth)
- Peer registration
- Peer discovery
- ICE signaling (WebSocket)
- Online status tracking

---

## Data Flow

### Connection Establishment

```
┌─────────────────────────────────────────────────────────────────┐
│ Phase 1: Authentication & Discovery                             │
└─────────────────────────────────────────────────────────────────┘

  1. User authenticates with IdP
     ├─→ Phone: Login → JWT token
     └─→ Hub: Login → JWT token

  2. Peers register with coordination server
     ├─→ Phone: POST /peers {jwt, public_key, peer_id}
     └─→ Hub: POST /peers {jwt, public_key, peer_id}

  3. Phone discovers hub
     └─→ GET /peers/discover {jwt}
         ← Returns: hub's peer_id, public_key, online status

┌─────────────────────────────────────────────────────────────────┐
│ Phase 2: ICE Signaling                                          │
└─────────────────────────────────────────────────────────────────┘

  4. Phone initiates connection
     └─→ POST /connections/initiate {jwt, from, to}

  5. ICE candidate gathering
     ├─→ Phone: Gathers candidates → sends to server
     └─→ Hub: Gathers candidates → sends to server

  6. Coordination server exchanges candidates
     ├─→ Phone receives hub's candidates
     └─→ Hub receives phone's candidates

  7. ICE credentials exchanged
     ├─→ Phone: ufrag_P, pwd_P
     └─→ Hub: ufrag_H, pwd_H

┌─────────────────────────────────────────────────────────────────┐
│ Phase 3: ICE Connection                                         │
└─────────────────────────────────────────────────────────────────┘

  8. ICE connectivity checks
     ├─→ Phone (controlling) → Dial()
     └─→ Hub (controlled) → Accept()

  9. ICE connection established
     └─→ net.Conn created on both sides

┌─────────────────────────────────────────────────────────────────┐
│ Phase 4: WireGuard Tunnel                                       │
└─────────────────────────────────────────────────────────────────┘

  10. ICEBind adapters created
      ├─→ Phone: NewICEBind(conn)
      └─→ Hub: NewICEBind(conn)

  11. WireGuard tunnels created
      ├─→ Phone: NewTunnel(tun, bind, config)
      └─→ Hub: NewTunnel(tun, bind, config)

  12. Peers configured
      ├─→ Phone: AddPeer(hub_public_key, allowed_ips)
      └─→ Hub: AddPeer(phone_public_key, allowed_ips)

  13. Tunnels brought up
      ├─→ Phone: tunnel.Up()
      └─→ Hub: tunnel.Up()

  14. WireGuard handshake
      └─→ Encrypted tunnel established! ✓

┌─────────────────────────────────────────────────────────────────┐
│ Phase 5: Encrypted Traffic                                      │
└─────────────────────────────────────────────────────────────────┘

  15. Application traffic flows through TUN
      └─→ Encrypted via WireGuard → Sent via ICE connection
```

---

## Security Architecture

### Multi-Layer Security Model

```
┌─────────────────────────────────────────────────────────────────┐
│ Layer 1: User Authentication (IdP/OAuth)                        │
│ - Verifies user identity                                        │
│ - Issues JWT tokens                                             │
│ - Provides: Authentication, MFA, Account Recovery               │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Layer 2: Peer Registration & Authorization                      │
│ - Maps peers to user accounts                                   │
│ - Stores WireGuard public keys                                  │
│ - Provides: Peer ownership, Authorization                       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Layer 3: Discovery & Access Control                             │
│ - User A can only discover User A's peers                       │
│ - JWT authentication required                                   │
│ - Provides: Access control, Isolation                           │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Layer 4: ICE Signaling (Authenticated)                          │
│ - Candidate exchange via WebSocket                              │
│ - JWT authentication required                                   │
│ - Provides: Secure signaling channel                            │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│ Layer 5: WireGuard Cryptographic Identity                       │
│ - Public key cryptography                                       │
│ - Can't decrypt without correct private key                     │
│ - Provides: End-to-end encryption, Identity proof               │
└─────────────────────────────────────────────────────────────────┘
```

### Authentication & Authorization

#### The Problem

**Without proper authentication**:
```
Attacker steals peer_id
  ↓
Registers fake peer with coordination server
  ↓
Victim's phone discovers fake peer
  ↓
Connects to attacker ✗
```

#### The Solution: IdP + Account System

**1. User Authentication (IdP/OAuth)**

```
User logs in → IdP (Google, Auth0, etc.)
  ↓
IdP validates credentials
  ↓
Returns JWT token: {
  user_id: "user_123",
  email: "user@example.com",
  exp: 1234567890,  // Expiry
  iss: "auth.ghostgo.com",  // Issuer
  signature: "..."  // Cryptographic signature
}
  ↓
Client stores JWT for API calls
```

**2. Peer Registration**

```
POST /api/peers
Authorization: Bearer <jwt>
Body: {
  peer_type: "phone",
  public_key: "wg_pub_key_abc123",
  peer_id: "phone_xyz789",
  peer_name: "My Phone"
}

Server:
  1. Validates JWT signature
  2. Extracts user_id from JWT
  3. Stores: users[user_123].peers[phone_xyz789] = {
       public_key: "wg_pub_key_abc123",
       ...
     }
```

**3. Discovery (Account-Scoped)**

```
GET /api/peers/discover
Authorization: Bearer <jwt>

Server:
  1. Validates JWT → extracts user_id = "user_123"
  2. Queries: SELECT * FROM peers WHERE user_id = 'user_123'
  3. Returns ONLY peers owned by user_123

Result:
  User A can ONLY see User A's peers
  User B cannot see User A's peers
```

**4. Connection Authorization**

```
POST /api/connections/initiate
Authorization: Bearer <jwt>
Body: {
  from_peer: "phone_xyz789",
  to_peer: "hub_abc123"
}

Server verifies:
  ✓ JWT is valid
  ✓ Both peers belong to JWT.user_id
  ✓ User is authorized to connect these peers
  ✗ Reject if peers belong to different users
```

**5. WireGuard Cryptographic Verification**

Even if coordination server is compromised:
```
Attacker registers fake peer
  ↓
Phone gets attacker's public key from server
  ↓
WireGuard handshake begins
  ↓
Phone sends data encrypted with pub_attacker
  ↓
Only attacker's priv_attacker can decrypt ✓
  BUT: Attacker doesn't have real peer's private key
  ↓
Can't impersonate legitimate peer
  ↓
Traffic is encrypted but to wrong destination
  (Coordination was compromised, but crypto still works)
```

### Anti-Forgery Protections

**1. Peer ID Forgery Prevention**

```
Registration requires:
  ✓ Valid JWT (user authenticated)
  ✓ Unique peer_id per user account
  ✓ Server generates peer_token (signed)

peer_token = sign({
  peer_id,
  user_id,
  public_key
}, server_secret)

Future authentications:
  Client sends: JWT + peer_token
  Server verifies: JWT signature + peer_token signature
```

**2. Public Key Pinning**

```
First connection to hub:
  1. Get hub's public_key from coordination server
  2. WireGuard handshake succeeds
  3. Store: hub_abc123 → pub_key_xyz (pinned)

Subsequent connections:
  1. Get hub's public_key from server
  2. Compare: stored pub_key_xyz == received pub_key
  3. If different → ALERT: "Key changed! Possible MITM!"
```

**3. Challenge-Response (Optional)**

```
Registration:
  1. Peer: "Register with public_key X"
  2. Server: "Prove you own the private key"
     → Sends nonce: "random_challenge_123"
  3. Peer: signature = sign(nonce, private_key)
  4. Server: verify(signature, nonce, public_key)
  5. Only real key owner can produce valid signature ✓
```

### Why Pre-Shared Keys Alone Are NOT Enough

**What WireGuard Keys Provide**:
- ✅ Cryptographic identity (can't impersonate without private key)
- ✅ End-to-end encryption
- ✅ Perfect forward secrecy

**What WireGuard Keys DON'T Provide**:
- ❌ Account association (which user owns which peer?)
- ❌ Authorization policy (who can connect to whom?)
- ❌ Discovery (how to find peers and get their keys?)
- ❌ Revocation (how to block compromised peers?)

**Example Attack**:
```
No account system, only public keys:

1. Attacker steals hub's peer_id (from logs/network/etc.)
2. Attacker registers: peer_id=hub_abc123, public_key=pub_attacker
3. Victim's phone queries: "Where is hub_abc123?"
4. Server returns: pub_attacker
5. Phone connects via WireGuard with pub_attacker
6. Handshake succeeds (cryptographically valid!)
7. BUT: Connected to attacker, not real hub ✗
```

**With account system**:
```
1. Attacker tries to register with hub's peer_id
2. Server requires JWT
3. Attacker's JWT has user_attacker (different user)
4. Server rejects: "peer_id already registered to user_123"
5. OR: Attacker uses new peer_id
   → Victim's phone won't discover it (scoped to user_123)
6. Connection prevented ✓
```

### Defense in Depth

**Think of it like SSH**:
- **SSH keys** = WireGuard keys (cryptographic identity)
- **Account system** = Unix user accounts (authorization, access control)
- **You need BOTH**: Keys prove identity, accounts control access

**Ghost-GO Security**:
```
IdP/OAuth    → Proves you're a valid user
Account DB   → Maps users to peers
Discovery    → Only shows your peers
WireGuard    → Proves peer identity + encrypts traffic
```

---

## Phase Breakdown

### Phase 1: Core Infrastructure ✅ COMPLETE

**Components**:
- ICE agent wrapper (Pion ICE v3)
- ICEBind adapter (conn.Bind)
- WireGuard tunnel wrapper
- TUN interface abstraction
- Demo application

**Status**: Fully implemented, tested, debugged

**Deliverable**: Two peers can establish encrypted tunnel via manual signaling

### Phase 2: Signaling & Coordination (NEXT)

**Components**:
- Coordination server (WebSocket signaling)
- User authentication (JWT initially, OAuth later)
- Peer registration API
- Discovery API
- ICE signaling automation

**Goal**: Eliminate manual copy-paste, automated peer discovery

### Phase 3: Connection Management

**Components**:
- Connection manager
- Multiple simultaneous tunnels
- Automatic reconnection
- Peer online/offline tracking
- Connection health monitoring

**Goal**: Robust, production-ready connection handling

### Phase 4: Advanced Features

**Components**:
- HTTP proxy mode
- Split tunneling
- Traffic statistics
- Network diagnostics
- Connection quality monitoring

**Goal**: Enhanced functionality and observability

### Phase 5: Mobile Platforms

**Components**:
- Android app (VpnService integration)
- iOS app (NEPacketTunnelProvider integration)
- Mobile-specific optimizations
- Battery usage optimization

**Goal**: Full mobile support

### Phase 6: Observability & Testing

**Components**:
- Metrics collection (Prometheus)
- Distributed tracing
- Performance testing
- Load testing
- Security auditing

**Goal**: Production-ready monitoring and validation

---

## Deployment Models

### Model 1: Hub-and-Spoke (Primary Use Case)

```
                    ┌─────────┐
                    │   Hub   │ (User's home server)
                    │  (VPN   │
                    │ Server) │
                    └────┬────┘
                         │
         ┌───────────────┼───────────────┐
         │               │               │
    ┌────▼────┐     ┌────▼────┐     ┌───▼─────┐
    │ Phone 1 │     │ Phone 2 │     │ Laptop  │
    └─────────┘     └─────────┘     └─────────┘
```

**Use Case**: Remote access to home network from phones and laptops

**Hub**:
- Runs on user's home network (Raspberry Pi, NAS, PC)
- Always online
- Routes traffic to home network
- Acts as gateway

**Clients**:
- Connect to hub on-demand
- Access home network resources
- Traffic routed through hub

### Model 2: Peer-to-Peer (Phase 3+)

```
    ┌─────────┐ ←───────→ ┌─────────┐
    │ Laptop  │            │  Phone  │
    └─────────┘ ←───────→ └─────────┘
        ↑                       ↑
        │                       │
        └──────→ ┌────────┐ ←──┘
                 │ Tablet │
                 └────────┘
```

**Use Case**: Direct peer-to-peer connections

**Features**:
- Any peer can connect to any other peer
- No central hub required
- Lower latency (direct connection)
- Multi-peer mesh networks

### Model 3: Hybrid (Phase 3+)

```
    ┌─────────┐
    │   Hub   │ (Relay when needed)
    └────┬────┘
         │
    ┌────┴────┐
    │         │
┌───▼───┐ ┌───▼───┐
│Phone 1│←│Phone 2│ (Direct P2P when possible)
└───────┘ └───────┘
```

**Use Case**: Automatic fallback

**Features**:
- Try direct P2P first
- Fall back to hub relay if P2P fails
- Automatically switch based on network conditions

---

## Technology Stack

### Backend
- **Language**: Go 1.25+
- **ICE**: Pion ICE v3
- **VPN**: WireGuard-Go
- **Server**: (Phase 2) Gin/Echo + WebSocket
- **Database**: (Phase 2) PostgreSQL
- **Auth**: (Phase 2) JWT, (Phase 3) OAuth2/OIDC

### Mobile
- **Android**: (Phase 5) Kotlin + Go Mobile (gomobile)
- **iOS**: (Phase 5) Swift + Go Mobile

### Infrastructure
- **Deployment**: Docker, Kubernetes (optional)
- **Monitoring**: (Phase 6) Prometheus, Grafana
- **Logging**: structured logging (slog)

---

## Performance Considerations

### ICE
- Candidate gathering: ~2-5 seconds
- Connection establishment: ~5-30 seconds (depending on network)
- Keepalive overhead: ~1KB every 15 seconds

### WireGuard
- Handshake: <1 second
- Throughput: Near line-rate (depends on CPU)
- Latency overhead: <1ms (after ICE connection)

### TUN
- MTU: 1280 bytes (default, safe for most networks)
- Overhead: ~100 bytes per packet (IP + UDP + WireGuard headers)

---

## References

- [Pion ICE Documentation](https://github.com/pion/ice)
- [WireGuard Protocol](https://www.wireguard.com/papers/wireguard.pdf)
- [RFC 8445 - ICE](https://www.rfc-editor.org/rfc/rfc8445)
- [OAuth 2.0 RFC 6749](https://datatracker.ietf.org/doc/html/rfc6749)
- [OpenID Connect](https://openid.net/specs/openid-connect-core-1_0.html)
- [NIST Zero Trust Architecture](https://csrc.nist.gov/publications/detail/sp/800-207/final)
- [Tailscale Architecture](https://tailscale.com/blog/how-tailscale-works/)
