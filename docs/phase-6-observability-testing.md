# Phase 6: Observability & Testing

## Overview

This phase implements comprehensive logging, metrics, and testing infrastructure to ensure reliability and debuggability.

---

## Logging

### 6.1 Structured Logger (`internal/logging/logger.go`)

```go
func NewLogger(config *LogConfig) *slog.Logger {
    var handler slog.Handler

    opts := &slog.HandlerOptions{
        Level:     parseLevel(config.Level),
        AddSource: config.AddSource,
    }

    switch config.Format {
    case "json":
        handler = slog.NewJSONHandler(config.Output, opts)
    default:
        handler = slog.NewTextHandler(config.Output, opts)
    }

    return slog.New(handler).With(
        slog.String("service", "ghost"),
        slog.String("version", Version),
    )
}

// Context-aware logging
func LogContext(logger *slog.Logger, tunnelID, peerID string) *slog.Logger {
    return logger.With(
        slog.String("tunnel_id", tunnelID),
        slog.String("peer_id", peerID),
    )
}
```

### 6.2 Log Levels

| Level | Use For |
|-------|---------|
| ERROR | Errors requiring immediate attention |
| WARN | Potential issues, degraded operation |
| INFO | Significant events (startup, connections) |
| DEBUG | Detailed diagnostic information |

### 6.3 What to Log

**Do Log:**
- Application startup/shutdown
- Connection state changes
- Authentication events
- Error details with context
- Performance metrics

**Don't Log:**
- Secrets, tokens, or keys
- Personal data (PII)
- Full request/response bodies
- High-frequency events in production

---

## Metrics (Prometheus)

### 6.4 Connection Metrics

```go
var (
    ConnectionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "ghost",
            Name:      "connections_total",
        },
        []string{"status", "type"},
    )

    ConnectionDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "ghost",
            Name:      "connection_duration_seconds",
            Buckets:   []float64{0.5, 1, 2, 5, 10, 30, 60},
        },
        []string{"phase"},
    )

    ActiveTunnels = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Namespace: "ghost",
            Name:      "active_tunnels",
        },
    )
)
```

### 6.5 Traffic Metrics

```go
var (
    BytesSent = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "ghost",
            Name:      "bytes_sent_total",
        },
        []string{"tunnel_id"},
    )

    BytesReceived = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "ghost",
            Name:      "bytes_received_total",
        },
        []string{"tunnel_id"},
    )
)
```

### 6.6 ICE Metrics

```go
var (
    ICECandidateGatherTime = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Namespace: "ghost",
            Name:      "ice_gather_duration_seconds",
            Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10},
        },
    )

    ICECandidateTypes = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "ghost",
            Name:      "ice_candidate_types_total",
        },
        []string{"type"},
    )
)
```

### 6.7 Error Metrics

```go
var (
    ErrorsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "ghost",
            Name:      "errors_total",
        },
        []string{"type", "recoverable"},
    )
)
```

---

## Testing

### 6.8 Test Coverage Requirements

| Component | Minimum Coverage |
|-----------|------------------|
| Go Bridge (internal/ice, internal/wireguard) | 80% |
| Signaling (pkg/signaling) | 80% |
| Connection Management (internal/state, internal/retry) | 80% |
| HTTP Proxy (pkg/proxy) | 70% |
| Mobile Platform Code | 60% |

### 6.9 Test Naming Convention

```go
// TestFunctionName_Scenario_ExpectedBehavior
func TestConnect_WithValidConfig_ReturnsPort(t *testing.T) {}
func TestConnect_WithMissingURL_ReturnsError(t *testing.T) {}
func TestConnect_WithTimeout_RetriesThreeTimes(t *testing.T) {}
```

### 6.10 Test Categories

**Unit Tests** (`*_test.go` alongside code)
- Test individual functions
- Mock dependencies
- Fast execution

**Integration Tests** (`test/integration/`)
- Test component interactions
- Use real dependencies where feasible
- May require network

**E2E Tests** (`test/e2e/`)
- Test full user flows
- Real ICE/WireGuard connections
- Requires test infrastructure

### 6.11 Test Infrastructure

```go
// test/helpers/ice.go
// Mock ICE agent for testing
type MockICEAgent struct {
    GatherCandidatesFunc func(ctx context.Context) (<-chan ice.Candidate, error)
    ConnectFunc          func(ctx context.Context) (net.Conn, error)
}

// test/helpers/tunnel.go
// Mock tunnel for proxy testing
type MockTunnel struct {
    DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
}

// test/helpers/signaling.go
// Local signaling server for testing
func StartTestSignalingServer(t *testing.T) (*SignalingServer, string)
```

---

## Files to Create

| File | Purpose |
|------|---------|
| `internal/logging/logger.go` | Structured logger |
| `internal/logging/context.go` | Context logging |
| `internal/metrics/metrics.go` | Prometheus metrics |
| `internal/metrics/collector.go` | Metrics collection |
| `test/helpers/ice.go` | ICE test helpers |
| `test/helpers/tunnel.go` | Tunnel test helpers |
| `test/helpers/signaling.go` | Signaling test helpers |
| `test/integration/tunnel_test.go` | Tunnel integration tests |
| `test/e2e/connection_test.go` | E2E connection tests |

---

## CI/CD Integration

### Makefile Targets

```makefile
.PHONY: test lint coverage

test:
	go test -v ./...

lint:
	golangci-lint run

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-integration:
	go test -v -tags=integration ./test/integration/...

test-e2e:
	go test -v -tags=e2e ./test/e2e/...
```

### golangci-lint Configuration (`.golangci.yml`)

```yaml
linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gosec
    - bodyclose
    - contextcheck

linters-settings:
  gosec:
    excludes:
      - G104  # Unhandled errors (we handle them)
```

---

## Verification Steps

After all phases are complete, verify:

1. **Unit Tests Pass**
   ```bash
   go test ./...
   ```

2. **Coverage Meets Targets**
   ```bash
   go test -coverprofile=coverage.out ./...
   ```

3. **Linting Passes**
   ```bash
   golangci-lint run
   ```

4. **Integration Test**
   - Start local STUN server
   - Run tunnel between two local peers
   - Verify ICE negotiation completes
   - Verify WireGuard handshake succeeds
   - Verify traffic flows through tunnel

5. **Proxy Test**
   - Start HTTP server on one peer
   - Connect proxy on other peer
   - Make requests through proxy
   - Verify responses match
