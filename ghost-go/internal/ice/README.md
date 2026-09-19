# ICE Package - Component Overview

**Purpose**: Provides NAT traversal and peer-to-peer connectivity using the Interactive Connectivity Establishment (ICE) protocol, wrapping Pion's ICE library v3.

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Ghost-GO Application                │
└─────────────────────┬───────────────────────────────┘
                      │
         ┌────────────┴──────────────┐
         │      ICE Package          │
         │                           │
         │  ┌─────────────────────┐ │
         │  │   Agent (Interface) │ │  Public API
         │  └──────────┬──────────┘ │
         │             │             │
         │  ┌──────────▼──────────┐ │
         │  │   pionAgent (Impl)  │ │  Wraps Pion ICE
         │  └──────────┬──────────┘ │
         │             │             │
         │  ┌──────────▼──────────┐ │
         │  │  ICEBind Adapter    │ │  ⭐ Critical: Bridges to WireGuard
         │  └──────────┬──────────┘ │
         └─────────────┼─────────────┘
                       │
         ┌─────────────▼──────────────┐
         │   Pion ICE Library v3      │  Handles ICE protocol
         └────────────────────────────┘
```

---

## Key Components

### 1. **Agent Interface** (`agent.go`)

The main entry point for ICE functionality.

```go
type Agent interface {
    // Lifecycle
    GatherCandidates(ctx context.Context) (<-chan *Candidate, error)
    AddRemoteCandidate(candidate *Candidate) error
    SetRemoteCredentials(ufrag, pwd string) error
    Connect(ctx context.Context) (net.Conn, error)
    Close() error
    
    // Information
    LocalCredentials() (ufrag, pwd string)
    GetSelectedCandidatePair() (*CandidatePair, error)
}
```

**Usage Pattern:**
```go
// 1. Create agent
agent, err := ice.NewAgent(config, logger)

// 2. Get local credentials (for signaling)
ufrag, pwd := agent.LocalCredentials()

// 3. Gather candidates
candChan, err := agent.GatherCandidates(ctx)
for cand := range candChan {
    // Send to remote peer via signaling
}

// 4. Receive remote candidates and credentials
agent.SetRemoteCredentials(remoteUfrag, remotePwd)
for _, remoteCand := range remoteCandidates {
    agent.AddRemoteCandidate(remoteCand)
}

// 5. Connect (specify controlling/controlled role)
// Agent A (initiator) is controlling, Agent B (responder) is controlled
controlling := true  // or false depending on role
conn, err := agent.Connect(ctx, controlling)

// 6. Use connection or wrap with ICEBind
bind := ice.NewICEBind(conn, logger)
```

---

### 2. **ICEBind Adapter** (`bind.go`) ⭐ CRITICAL

Bridges ICE connections to WireGuard's `conn.Bind` interface.

**Why It's Critical:**
- WireGuard expects a `conn.Bind` interface, not a simple `net.Conn`
- Handles packet I/O, endpoint management, and lifecycle
- Enables WireGuard to work over ICE connections

**Interface Implementation:**
```go
type Bind interface {
    Open(port uint16) (fns []ReceiveFunc, actualPort uint16, err error)
    Close() error
    Send(bufs [][]byte, endpoint Endpoint) error
    ParseEndpoint(s string) (Endpoint, error)
    BatchSize() int
    SetMark(mark uint32) error
}
```

**Design Decisions:**
- `BatchSize() = 1`: Simplifies initial implementation
- Single endpoint per bind: ICE is point-to-point
- Direct reads from `net.Conn`: No additional buffering
- `SetMark()` is no-op: Not applicable for ICE

**Usage:**
```go
// After ICE connection established
// Agent A is controlling, Agent B is controlled
conn, _ := iceAgent.Connect(ctx, true)  // true = controlling role

// Wrap for WireGuard
bind := ice.NewICEBind(conn, logger)

// Use with WireGuard tunnel
tunnel, _ := wireguard.NewTunnel(tunDev, bind, wgConfig, logger)
```

---

### 3. **Configuration** (`config.go`)

Configures ICE behavior, STUN/TURN servers, and timeouts.

```go
type ICEConfig struct {
    STUNServers       []string         // e.g., "stun:stun.l.google.com:19302"
    TURNServers       []TURNServer     // For relay candidates
    GatherTimeout     time.Duration    // Candidate gathering timeout
    ConnectionTimeout time.Duration    // ICE connection timeout
    KeepaliveInterval time.Duration    // Connection keepalive
}

type TURNServer struct {
    URLs     []string  // TURN server URLs
    Username string    // Authentication
    Password string
}
```

**Validation:**
- STUN/TURN URL format: `scheme:host:port` (not `scheme://host:port`)
- All timeouts must be positive
- TURN servers require credentials

---

### 4. **Types** (`types.go`)

Core data structures for ICE.

```go
type Candidate struct {
    Type       CandidateType  // host, srflx, prflx, relay
    Address    string         // IP address
    Port       int            // Port number
    Protocol   string         // udp, tcp
    Priority   uint32         // Candidate priority
    Foundation string         // Candidate foundation
    // ...
}

type CandidateType string
const (
    CandidateTypeHost  CandidateType = "host"   // Local interface
    CandidateTypeSrflx CandidateType = "srflx"  // Server reflexive (STUN)
    CandidateTypePrflx CandidateType = "prflx"  // Peer reflexive
    CandidateTypeRelay CandidateType = "relay"  // TURN relay
)

type ICEEndpoint struct {
    addr net.Addr
}
```

**ICEEndpoint** implements `conn.Endpoint` for WireGuard:
- `DstToBytes()` / `SrcToBytes()` → serialize IP:port
- `DstIP()` / `SrcIP()` → return `netip.Addr` (not `net.IP`)
- `DstToString()` / `SrcToString()` → human-readable format

---

## Common Operations

### Establish ICE Connection

```go
import (
    "context"
    "time"
    "github.com/Imposter/ghost/ghost-go/internal/ice"
)

// Configure
config := &ice.ICEConfig{
    STUNServers: []string{"stun:stun.l.google.com:19302"},
    GatherTimeout: 10 * time.Second,
    ConnectionTimeout: 30 * time.Second,
}

// Create agent
agent, err := ice.NewAgent(config, logger)
if err != nil {
    return err
}
defer agent.Close()

// Get credentials for signaling
ufrag, pwd := agent.LocalCredentials()
// ... send to peer via signaling ...

// Gather candidates
ctx := context.Background()
candChan, err := agent.GatherCandidates(ctx)
if err != nil {
    return err
}

// Collect and send candidates
for cand := range candChan {
    // ... send candidate to peer via signaling ...
}

// Receive remote candidates
for _, remoteCand := range receivedCandidates {
    agent.AddRemoteCandidate(remoteCand)
}

// Set remote credentials
agent.SetRemoteCredentials(remoteUfrag, remotePwd)

// Connect (specify if this agent is controlling)
// Typically: initiator = controlling, responder = controlled
controlling := true  // This agent initiates connection
conn, err := agent.Connect(ctx, controlling)
if err != nil {
    return err
}

// Use connection
// conn is now a net.Conn for data transmission
```

### Use with WireGuard

```go
// After ICE connection (controlling = true for initiator)
conn, _ := agent.Connect(ctx, true)

// Create bind
bind := ice.NewICEBind(conn, logger)

// Create TUN interface
tunDev, _ := wireguard.CreateTUN("ghost0", 1280)

// Create WireGuard tunnel
wgConfig := &wireguard.WireGuardConfig{
    PrivateKey: privateKey,
    MTU: 1280,
}
tunnel, _ := wireguard.NewTunnel(tunDev, bind, wgConfig, logger)

// Configure peers, bring up, etc.
```

---

## Testing

### Unit Tests

Run configuration and type validation tests:
```bash
go test ./internal/ice -run "Config" -v
```

### Integration Tests

Test full ICE connection between two peers:
```bash
# Requires network access
go test ./internal/ice -run "TestICEConnection" -v

# Local-only test (no STUN)
go test ./internal/ice -run "TestICEConnection_LocalOnly" -v
```

### Mock Signaling

For testing, use `testutil.MockSignalingChannel`:
```go
import "github.com/Imposter/ghost/ghost-go/internal/testutil"

signaling := testutil.NewMockSignalingChannel(logger)

// Peer A
agentA, _ := ice.NewAgent(config, logger)
ufragA, pwdA := agentA.LocalCredentials()
signaling.SendCredentialsFromA(ufragA, pwdA)

// Peer B
agentB, _ := ice.NewAgent(config, logger)
ufragB, pwdB := agentB.LocalCredentials()
signaling.SendCredentialsFromB(ufragB, pwdB)

// Exchange
remoteCreds, _ := signaling.GetCredentialsForA(ctx)
agentA.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd)
```

---

## API Changes from Pion ICE v3

**Important differences when using Pion directly:**

1. `agent.Dial()` requires 3 parameters:
   ```go
   // Pion v3
   conn, err := agent.Dial(ctx, remoteUfrag, remotePwd)
   ```

2. `GetSelectedCandidatePair()` returns struct:
   ```go
   // Pion v3
   pair, err := agent.GetSelectedCandidatePair()
   // pair.Local, pair.Remote, pair.State
   ```

3. `CandidatePair.State` is unexported, use hardcoded "succeeded"

---

## Performance Considerations

1. **Candidate Gathering**
   - Host candidates: ~instant
   - STUN candidates: ~100-500ms (network RTT)
   - TURN candidates: ~500ms-2s (requires allocation)

2. **Connection Establishment**
   - Local network: ~100-500ms
   - Through NAT: ~1-3s
   - Via TURN relay: ~2-5s

3. **ICEBind Overhead**
   - `BatchSize=1`: Processes one packet at a time
   - Buffer channel size: 32 packets
   - Consider increasing for high throughput

---

## Error Handling

Common errors and handling:

```go
// Timeout during gathering
if errors.Is(err, context.DeadlineExceeded) {
    // Extend timeout or proceed with gathered candidates
}

// Connection failed
if errors.Is(err, ice.ErrConnectionFailed) {
    // All candidate pairs failed, check NAT/firewall
}

// Agent closed
if errors.Is(err, ice.ErrAgentClosed) {
    // Agent was closed, create new one
}
```

---

## Security Notes

1. **Credential Exchange**: ICE credentials (ufrag/pwd) should be exchanged over a secure signaling channel
2. **TURN Authentication**: TURN servers require credentials - store securely
3. **STUN Privacy**: STUN reveals your public IP - consider privacy implications
4. **Connection Validation**: ICE includes built-in connection validation

---

## Debugging

Enable debug logging:
```go
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))
```

Key debug points:
- Candidate gathering progress
- Candidate pair formation
- Connectivity checks
- Selected candidate pair
- Connection state changes

---

## Related Packages

- `internal/wireguard` - Uses ICEBind for encrypted tunneling
- `internal/testutil` - Testing utilities (mock signaling)
- `github.com/pion/ice/v3` - Underlying ICE implementation

---

**Last Updated**: January 2026  
**Maintainer**: Ghost Team
