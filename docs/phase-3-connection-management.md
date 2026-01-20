# Phase 3: Connection Management

## Overview

This phase implements robust connection lifecycle management including state machine, error handling, retry strategies, and automatic recovery.

---

## Components

### 3.1 Connection State Machine (`internal/state/machine.go`)

Manages tunnel connection states with well-defined transitions.

```go
type State string

const (
    StateDisconnected   State = "disconnected"
    StateDiscovering    State = "discovering"
    StateSignaling      State = "signaling"
    StateConnecting     State = "connecting"
    StateAuthenticating State = "authenticating"
    StateConnected      State = "connected"
    StateReconnecting   State = "reconnecting"
    StateFailed         State = "failed"
)

type Event string

const (
    EventConnect        Event = "connect"
    EventPeerFound      Event = "peer_found"
    EventCandidatesReady Event = "candidates_ready"
    EventICEConnected   Event = "ice_connected"
    EventWGHandshakeOK  Event = "wg_handshake_ok"
    EventDisconnect     Event = "disconnect"
    EventError          Event = "error"
    EventTimeout        Event = "timeout"
    EventRetry          Event = "retry"
)
```

**State Diagram:**
```
Disconnected ──connect──► Discovering ──peer_found──► Signaling
                              │                          │
                              │ timeout                  │ candidates_ready
                              │                          ▼
                              └──────────────────► Connecting
                                                        │
                                                        │ ice_connected
                                                        ▼
                                                 Authenticating
                                                        │
                                                        │ wg_handshake_ok
                                                        ▼
                                                   Connected ◄───┐
                                                        │        │
                                                        │ error  │ retry
                                                        ▼        │
                                                  Reconnecting ──┘
                                                        │
                                                        │ max_retries
                                                        ▼
                                                     Failed
```

### 3.2 Tunnel Manager (`pkg/tunnel/manager.go`)

Manages multiple tunnels and their lifecycle.

```go
type Manager interface {
    CreateTunnel(ctx context.Context, peerID string, config *Config) (Tunnel, error)
    GetTunnel(peerID string) (Tunnel, bool)
    RemoveTunnel(ctx context.Context, peerID string) error
    ListTunnels() []Tunnel
    Close() error
}

type Tunnel interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    State() State
    Metrics() *Metrics
    LocalAddr() net.IP
    RemoteAddr() net.IP
    Conn() net.Conn
}
```

### 3.3 Error Handling (`pkg/ghost/errors.go`)

Domain-specific error types with retry classification.

```go
var (
    // Connection errors
    ErrNotConnected     = errors.New("not connected")
    ErrConnectionFailed = errors.New("connection failed")
    ErrConnectionLost   = errors.New("connection lost")

    // ICE errors
    ErrICEFailed          = errors.New("ICE negotiation failed")
    ErrNoViableCandidates = errors.New("no viable ICE candidates")

    // Security errors
    ErrAuthenticationFailed = errors.New("authentication failed")
    ErrInvalidSignature     = errors.New("invalid signature")
)

type GhostError struct {
    Op      string
    Err     error
    Retry   bool     // Should this error trigger retry?
    Details string
}

func IsRetryable(err error) bool
```

### 3.4 Retry Strategy (`internal/retry/retry.go`)

Exponential backoff with jitter.

```go
type Config struct {
    MaxAttempts     int           // 0 = unlimited
    InitialInterval time.Duration // 1s
    MaxInterval     time.Duration // 30s
    Multiplier      float64       // 2.0
    Jitter          float64       // 0.2
}

type Retryer struct {
    config  *Config
    attempt int
}

func (r *Retryer) Do(ctx context.Context, op func() error) error
func (r *Retryer) Reset()
```

### 3.5 Recovery Manager (`internal/recovery/recovery.go`)

Automatic connection recovery with health monitoring.

```go
type RecoveryManager struct {
    tunnel      *Tunnel
    retryer     *retry.Retryer
    healthCheck *HealthChecker
}

func (rm *RecoveryManager) Start(ctx context.Context)
func (rm *RecoveryManager) initiateRecovery(ctx context.Context)

type HealthChecker struct {
    tunnel  *Tunnel
    timeout time.Duration
}

func (hc *HealthChecker) Check(ctx context.Context) error
```

---

## Transition Actions

| Transition | Action |
|-----------|--------|
| `Disconnected → Discovering` | Start peer discovery via coordination service |
| `Discovering → Signaling` | Begin ICE candidate gathering, connect to signaling |
| `Signaling → Connecting` | Exchange complete, start ICE negotiation |
| `Connecting → Authenticating` | ICE connected, configure WireGuard with exchanged keys |
| `Authenticating → Connected` | WireGuard handshake complete, tunnel ready |
| `* → Reconnecting` | Tear down existing connection, prepare for retry |
| `Reconnecting → Discovering` | Begin reconnection attempt |
| `Reconnecting → Failed` | Max retries exceeded |

---

## Configuration

```go
type ConnectionConfig struct {
    // Timeouts
    DiscoveryTimeout time.Duration `yaml:"discovery_timeout" envDefault:"30s"`
    SignalingTimeout time.Duration `yaml:"signaling_timeout" envDefault:"30s"`
    ICETimeout       time.Duration `yaml:"ice_timeout" envDefault:"30s"`
    HandshakeTimeout time.Duration `yaml:"handshake_timeout" envDefault:"10s"`

    // Retry
    MaxRetries       int           `yaml:"max_retries" envDefault:"5"`
    RetryInterval    time.Duration `yaml:"retry_interval" envDefault:"5s"`
    MaxRetryInterval time.Duration `yaml:"max_retry_interval" envDefault:"60s"`

    // Health check
    HealthCheckInterval time.Duration `yaml:"health_check_interval" envDefault:"10s"`
    HealthCheckTimeout  time.Duration `yaml:"health_check_timeout" envDefault:"5s"`
}
```

---

## Files to Create

| File | Purpose |
|------|---------|
| `internal/state/machine.go` | Generic state machine |
| `internal/state/connection.go` | Connection state definitions |
| `internal/state/events.go` | Event types |
| `pkg/tunnel/tunnel.go` | Tunnel interface |
| `pkg/tunnel/manager.go` | Tunnel manager |
| `pkg/tunnel/state.go` | State types |
| `pkg/tunnel/metrics.go` | Tunnel metrics |
| `pkg/ghost/errors.go` | Error types |
| `internal/retry/retry.go` | Retry logic |
| `internal/recovery/recovery.go` | Recovery manager |
| `internal/recovery/health.go` | Health checker |

---

## Testing Strategy

- Unit tests for state machine transitions
- Test invalid transitions are rejected
- Test retry backoff calculations
- Integration tests for recovery scenarios
- Test health check failure detection
- Simulate network interruptions
