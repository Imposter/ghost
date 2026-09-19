package ghost

import (
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/signal"
)

// iceConfig is an alias so the unexported test tuner can reference the ICE
// config type without leaking the internal package into exported API.
type iceConfig = ice.ICEConfig

// DefaultPool is the default tunnel address pool (RFC 6598 shared address
// space) used when the signalling layer does not specify one.
const DefaultPool = "100.64.0.0/10"

// DefaultMTU is the default tunnel MTU.
const DefaultMTU = 1280

// STUNServer configures a STUN server URL.
type STUNServer struct {
	// URL is a STUN URL, e.g. "stun:stun.l.google.com:19302".
	URL string
}

// TURNServer configures a TURN server with credentials.
type TURNServer struct {
	// URLs are TURN URLs, e.g. ["turn:turn.example.com:3478"].
	URLs []string
	// Username and Password authenticate to the TURN server.
	Username string
	Password string
}

// Config configures a Node or Hub. The zero value is not usable; set at least
// SignalURL and PeerToken (or SignalDialer for in-memory tests), and Network.
type Config struct {
	// SignalURL is the signalling server WebSocket URL.
	SignalURL string
	// SignalDialer, if set, overrides the WebSocket dialer. Tests set this to
	// a signal.FakeServer dialer to run without a network.
	SignalDialer signal.Dialer

	// PeerToken authenticates this peer to the control plane.
	PeerToken string
	// PeerID is this peer's stable identifier, if already known.
	PeerID string

	// Network is the name of the network to join.
	Network string

	// KeyStorePath is where the WireGuard key pair is persisted. If empty,
	// keys are ephemeral (generated per run). If the file is missing it is
	// created.
	KeyStorePath string

	// STUNServers and TURNServers configure ICE NAT traversal. TURN servers
	// received from the signalling Welcome are merged with these.
	STUNServers []STUNServer
	TURNServers []TURNServer

	// PortMin and PortMax bound the local UDP port range for ICE (0 = OS
	// assigned). Useful for firewall rules.
	PortMin uint16
	PortMax uint16

	// Address, when set, overrides the address assigned by the signalling
	// layer (CIDR form, e.g. "100.64.0.2/32"). Normally left empty so the
	// server assigns from the pool.
	Address string

	// MTU is the tunnel MTU (default DefaultMTU).
	MTU int

	// DNSServers are DNS servers for the tunnel netstack (optional).
	DNSServers []string

	// Logger is optional (defaults to slog.Default()).
	Logger *slog.Logger

	// ConnectTimeout bounds ICE connection establishment per peer.
	ConnectTimeout time.Duration

	// MeterProvider and TracerProvider supply OpenTelemetry instrumentation.
	// Both are optional and default to the global providers. The ghost package
	// depends only on the OTel API.
	MeterProvider  metric.MeterProvider
	TracerProvider trace.TracerProvider

	// Metrics, when set, makes this member serve its metrics endpoint on its
	// tunnel IP once joined, and backs Snapshot / RecentConnections. See
	// MetricsConfig.
	Metrics *MetricsConfig

	// NodeMetricsPort is the port a Hub fetches node metrics from (default
	// metrics.DefaultPort). It must match the nodes' MetricsConfig.Port.
	NodeMetricsPort int

	// iceTuner, when set, adjusts the ICE config just before an agent is
	// created. It is unexported and exists only for tests, which use it to
	// restrict candidates to the loopback interface (host candidates only, no
	// STUN/TURN) so the suite never triggers a Windows firewall prompt.
	iceTuner func(*iceConfig)

	// allowedIPsOverride, when set, replaces the WireGuard AllowedIPs of every
	// link. It is unexported and exists only for tests that model a
	// misbehaving peer (for example one routing the whole pool through its
	// hub to reach another peer).
	allowedIPsOverride func(peerID, address string) []string
}

func (c *Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Config) mtu() int {
	if c.MTU > 0 {
		return c.MTU
	}
	return DefaultMTU
}
