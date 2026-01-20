# Phase 4: HTTP Reverse Proxy

## Overview

This phase implements the HTTP reverse proxy that runs on the client, forwarding requests through the WireGuard tunnel to target services on the server side.

---

## Components

### 4.1 Reverse Proxy (`pkg/proxy/proxy.go`)

HTTP reverse proxy that forwards requests through the tunnel.

```go
type ReverseProxy struct {
    tunnel      *tunnel.Tunnel
    listener    net.Listener
    httpServer  *http.Server
    router      *Router
    logger      *slog.Logger
}

func NewReverseProxy(tunnel *tunnel.Tunnel, config *ProxyConfig) (*ReverseProxy, error)
func (p *ReverseProxy) Start(ctx context.Context) error
func (p *ReverseProxy) Stop(ctx context.Context) error
func (p *ReverseProxy) Addr() string
```

### 4.2 Request Router (`pkg/proxy/router.go`)

Routes requests to target services based on headers or path prefix.

```go
type Router struct {
    defaultTarget string
    routes        map[string]*Route
}

type Route struct {
    PathPrefix string
    Target     string  // host:port
    Headers    map[string]string
}

// Routing strategies:
// 1. X-Target-IP header: explicit target
// 2. Path prefix: /mesh/{target}/... → strips prefix and forwards
// 3. Default target: fallback
func (r *Router) Route(req *http.Request) (target string, rewrittenReq *http.Request, error)
```

### 4.3 HTTP Handler (`pkg/proxy/handler.go`)

Handles incoming HTTP requests and proxies them.

```go
type ProxyHandler struct {
    router    *Router
    transport *http.Transport
    logger    *slog.Logger
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

**Request flow:**
1. Parse incoming request
2. Determine target via router
3. Create outgoing request to tunnel
4. Forward request body
5. Copy response headers and body back

### 4.4 Middleware (`pkg/proxy/middleware.go`)

Logging, metrics, and request modification.

```go
// Logging middleware
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler

// Metrics middleware
func MetricsMiddleware(metrics *ProxyMetrics) func(http.Handler) http.Handler

// Request ID middleware
func RequestIDMiddleware() func(http.Handler) http.Handler

// Timeout middleware
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler
```

---

## Routing Examples

### 1. Header-Based Routing

```http
GET /api/users HTTP/1.1
Host: localhost:8080
X-Target-IP: 10.0.0.5:8080

→ Forwards to http://10.0.0.5:8080/api/users through tunnel
```

### 2. Path-Based Routing

```http
GET /mesh/10.0.0.5:8080/api/users HTTP/1.1
Host: localhost:8080

→ Strips /mesh/10.0.0.5:8080 prefix
→ Forwards to http://10.0.0.5:8080/api/users through tunnel
```

### 3. Named Service Routing

```http
GET /svc/backend/api/users HTTP/1.1
Host: localhost:8080

→ Looks up "backend" in service registry
→ Forwards to registered address through tunnel
```

---

## Configuration

```go
type ProxyConfig struct {
    // Listener
    Enabled    bool   `yaml:"enabled" envDefault:"true"`
    ListenAddr string `yaml:"listen_addr" envDefault:"127.0.0.1:0"`

    // TLS (optional - for local proxy)
    TLSCert string `yaml:"tls_cert"`
    TLSKey  string `yaml:"tls_key"`

    // Timeouts
    ReadTimeout     time.Duration `yaml:"read_timeout" envDefault:"30s"`
    WriteTimeout    time.Duration `yaml:"write_timeout" envDefault:"30s"`
    IdleTimeout     time.Duration `yaml:"idle_timeout" envDefault:"60s"`

    // Routing
    DefaultTarget string            `yaml:"default_target"`
    ServiceRoutes map[string]string `yaml:"service_routes"` // name → address

    // Headers
    ForwardHeaders  []string `yaml:"forward_headers"`  // Headers to forward
    AddHeaders      map[string]string `yaml:"add_headers"` // Headers to add
}
```

---

## Transport Configuration

Custom HTTP transport using tunnel as underlying connection.

```go
func NewTunnelTransport(tunnel *tunnel.Tunnel) *http.Transport {
    return &http.Transport{
        DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
            // Dial through the WireGuard tunnel
            return tunnel.Dial(ctx, network, addr)
        },
        MaxIdleConns:        100,
        IdleConnTimeout:     90 * time.Second,
        DisableKeepAlives:   false,
        DisableCompression:  false,
    }
}
```

---

## Files to Create

| File | Purpose |
|------|---------|
| `pkg/proxy/proxy.go` | Main proxy implementation |
| `pkg/proxy/router.go` | Request routing |
| `pkg/proxy/handler.go` | HTTP handler |
| `pkg/proxy/middleware.go` | Middleware chain |
| `pkg/proxy/transport.go` | Tunnel transport |
| `pkg/proxy/metrics.go` | Proxy metrics |

---

## Metrics

```go
var (
    RequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "ghost",
            Subsystem: "proxy",
            Name:      "requests_total",
        },
        []string{"method", "target", "status"},
    )

    RequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "ghost",
            Subsystem: "proxy",
            Name:      "request_duration_seconds",
            Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10},
        },
        []string{"method", "target"},
    )

    ActiveConnections = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Namespace: "ghost",
            Subsystem: "proxy",
            Name:      "active_connections",
        },
    )
)
```

---

## Testing Strategy

- Unit tests for router logic
- Test header-based routing
- Test path-based routing with prefix stripping
- Mock tunnel for handler tests
- Integration tests with real HTTP servers
- Test timeout handling
- Test large request/response bodies
- Test WebSocket upgrade support
