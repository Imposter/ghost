package ghost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// MetricsConfig makes a Node serve its strict metrics on its tunnel IP.
//
// The endpoint listens inside the tunnel netstack (no OS port is bound), so it
// is unreachable from the owner's LAN or the internet. Only peers holding the
// hub role in this member's netmap, plus the peer ids in AllowPeers, may read
// it; any other caller gets 403.
type MetricsConfig struct {
	// Collector is the exit's Accountant. It backs the JSON snapshot, the
	// recent-connections ring and Node.Snapshot. Required.
	Collector *metrics.Collector
	// Prometheus serves GET /metrics in the Prometheus text format, normally
	// otelsetup.Setup.PrometheusHandler. Optional.
	Prometheus http.Handler
	// Port is the tunnel-side port (default metrics.DefaultPort).
	Port int
	// AllowPeers lists extra peer ids allowed to read metrics besides the
	// netmap's hubs.
	AllowPeers []string
}

func (c *MetricsConfig) port() int {
	if c.Port > 0 {
		return c.Port
	}
	return metrics.DefaultPort
}

// Errors returned by the NodeMetricsFetcher methods.
var (
	// ErrUnknownPeer means no connected peer has the requested peer id.
	ErrUnknownPeer = errors.New("ghost: unknown or disconnected peer")
	// ErrNoMetrics means the member has no MetricsConfig.Collector.
	ErrNoMetrics = errors.New("ghost: metrics not configured")
)

// NodeMetricsFetcher reads a connected node's metrics over the tunnel. *Hub
// implements it; consumers such as an egress gateway should depend on this
// interface rather than on Hub. ghost-server does not proxy node metrics.
type NodeMetricsFetcher interface {
	// NodeSnapshot fetches the node's JSON snapshot (GET /metrics?format=json).
	NodeSnapshot(ctx context.Context, peerID string) (metrics.Snapshot, error)
	// NodeConnections fetches up to limit recent connections, newest first
	// (GET /metrics/connections?limit=N; limit <= 0 uses the node default).
	NodeConnections(ctx context.Context, peerID string, limit int) ([]metrics.Connection, error)
	// NodePrometheus fetches the node's Prometheus text (GET /metrics).
	NodePrometheus(ctx context.Context, peerID string) ([]byte, error)
}

var (
	_ NodeMetricsFetcher = (*Hub)(nil)
	_ metrics.Source     = (*Node)(nil)
	_ exit.PeerResolver  = (*Node)(nil)
	_ exit.PeerResolver  = (*Hub)(nil)
)

// PeerForAddr implements exit.PeerResolver: it maps a tunnel address to the
// peer id of the connected peer that owns it, or "" when none does.
func (m *mesh) PeerForAddr(addr net.Addr) string {
	ip, ok := addrIP(addr)
	if !ok {
		return ""
	}
	return m.peerForIP(ip)
}

func (m *mesh) peerForIP(ip netip.Addr) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.links {
		if a, ok := tunnelAddr(l.address); ok && a == ip {
			return id
		}
	}
	return ""
}

// Snapshot returns this member's metrics snapshot: the exit-side aggregates
// from MetricsConfig.Collector (empty when unset) plus live per-peer tunnel
// stats. It is pure Go and cheap, for in-process readers such as an FFI
// binding.
func (m *mesh) Snapshot() metrics.Snapshot {
	var s metrics.Snapshot
	if c := m.collector(); c != nil {
		s = c.Snapshot()
	} else {
		s = metrics.EmptySnapshot()
	}
	st := m.Status()
	s.PeerID = st.PeerID
	s.Address = st.Address
	for _, l := range m.linkSnapshot() {
		if !l.added {
			continue
		}
		ps := metrics.PeerStat{
			PeerID:        l.peerID,
			Address:       l.address,
			CandidateType: l.candType,
			RTTSeconds:    l.rttSeconds,
			LastHandshake: l.lastHandshake,
			RxBytes:       l.rx,
			TxBytes:       l.tx,
		}
		if l.handshakeAgeSeconds > 0 {
			ps.HandshakeAgeSeconds = l.handshakeAgeSeconds
		}
		s.Tunnel = append(s.Tunnel, ps)
	}
	slices.SortFunc(s.Tunnel, func(a, b metrics.PeerStat) int {
		switch {
		case a.PeerID < b.PeerID:
			return -1
		case a.PeerID > b.PeerID:
			return 1
		}
		return 0
	})
	return s
}

// RecentConnections returns up to limit recent exit connections, newest first
// (limit <= 0 returns all held). It is empty when metrics are not configured.
func (m *mesh) RecentConnections(limit int) []metrics.Connection {
	c := m.collector()
	if c == nil {
		return []metrics.Connection{}
	}
	return c.Recent(limit)
}

func (m *mesh) collector() *metrics.Collector {
	if m.cfg.Metrics == nil {
		return nil
	}
	return m.cfg.Metrics.Collector
}

// authorizeMetrics admits netmap peers holding the hub role and, unless the
// network is hub-only, the configured AllowPeers, identified by their tunnel
// source address.
func (m *mesh) authorizeMetrics(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	id := m.peerForIP(ap.Addr().Unmap())
	if id == "" {
		return false
	}
	if p, ok := m.netmapPeer(id); ok && proto.HasRole(p.Roles, proto.RoleHub) {
		return true
	}
	m.mu.Lock()
	hubOnly := m.hubOnlyLocked()
	m.mu.Unlock()
	// Under hub-only isolation only hubs may read metrics, whatever
	// AllowPeers says.
	return !hubOnly && slices.Contains(m.cfg.Metrics.AllowPeers, id)
}

// serveMetricsLocked starts the metrics endpoint on the tunnel IP inside the
// netstack. Caller holds m.mu and has set up the tunnel.
func (m *mesh) serveMetricsLocked() error {
	cfg := m.cfg.Metrics
	if cfg.Collector == nil {
		return fmt.Errorf("metrics: %w", ErrNoMetrics)
	}
	ip, ok := tunnelAddr(m.address)
	if !ok {
		return fmt.Errorf("metrics: bad tunnel address %q", m.address)
	}
	ln, err := m.net.ListenTCP(&net.TCPAddr{IP: ip.AsSlice(), Port: cfg.port()})
	if err != nil {
		return fmt.Errorf("metrics: listen on tunnel: %w", err)
	}
	srv := &http.Server{
		Handler: metrics.NewHandler(metrics.HandlerConfig{
			Source:         m,
			Prometheus:     cfg.Prometheus,
			Authorize:      m.authorizeMetrics,
			MaxConnections: cfg.Collector.RingSize(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.msrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Debug("metrics server stopped", "error", err)
		}
	}()
	return nil
}

// NodeSnapshot implements NodeMetricsFetcher.
func (h *Hub) NodeSnapshot(ctx context.Context, peerID string) (metrics.Snapshot, error) {
	ip, err := h.nodeIP(peerID)
	if err != nil {
		return metrics.Snapshot{}, err
	}
	return h.metricsClient().Snapshot(ctx, ip)
}

// NodeConnections implements NodeMetricsFetcher.
func (h *Hub) NodeConnections(ctx context.Context, peerID string, limit int) ([]metrics.Connection, error) {
	ip, err := h.nodeIP(peerID)
	if err != nil {
		return nil, err
	}
	return h.metricsClient().Connections(ctx, ip, limit)
}

// NodePrometheus implements NodeMetricsFetcher.
func (h *Hub) NodePrometheus(ctx context.Context, peerID string) ([]byte, error) {
	ip, err := h.nodeIP(peerID)
	if err != nil {
		return nil, err
	}
	return h.metricsClient().Prometheus(ctx, ip)
}

func (h *Hub) nodeIP(peerID string) (string, error) {
	h.mu.Lock()
	l, ok := h.links[peerID]
	var address string
	added := false
	if ok {
		address, added = l.address, l.added
	}
	h.mu.Unlock()
	if !ok || !added {
		return "", fmt.Errorf("%w: %s", ErrUnknownPeer, peerID)
	}
	ip, ok := tunnelAddr(address)
	if !ok {
		return "", fmt.Errorf("%w: %s has no tunnel address", ErrUnknownPeer, peerID)
	}
	return ip.String(), nil
}

func (h *Hub) metricsClient() *metrics.Client {
	h.mfetchMu.Lock()
	defer h.mfetchMu.Unlock()
	if h.mfetch == nil {
		h.mfetch = metrics.NewClient(metrics.ClientConfig{
			Dial: h.DialContext,
			Port: h.cfg.NodeMetricsPort,
		})
	}
	return h.mfetch
}

// tunnelAddr parses a tunnel address in CIDR or bare form.
func tunnelAddr(s string) (netip.Addr, bool) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

// addrIP extracts the IP of a net.Addr.
func addrIP(addr net.Addr) (netip.Addr, bool) {
	if addr == nil {
		return netip.Addr{}, false
	}
	if t, ok := addr.(*net.TCPAddr); ok {
		a, ok := netip.AddrFromSlice(t.IP)
		return a.Unmap(), ok
	}
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap(), true
}
