package ghost

import (
	"fmt"
	"log/slog"
	"slices"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// iceConfig is an alias so the unexported test tuner can reference the ICE
// config type without leaking the internal package into exported API.
type iceConfig = ice.ICEConfig

// DefaultPool is the default tunnel address pool (RFC 6598 shared address
// space) used when the signalling layer does not specify one.
const DefaultPool = "100.64.0.0/10"

// DefaultMTU is the default tunnel MTU.
const DefaultMTU = 1280

// DefaultJoinRetryMin and DefaultJoinRetryMax bound the backoff between
// attempts to join the network. A member retries for as long as its
// signalling session is up, so a join the control plane refuses (a paused
// node, or an authorizer failing closed during an outage) costs the member
// the backoff, not its place in the pool.
const (
	DefaultJoinRetryMin = 2 * time.Second
	DefaultJoinRetryMax = 30 * time.Second
)

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
// SignalURL and PeerToken (or SignalDialer for in-memory tests), and Network,
// or set Signaller to run without the control plane.
type Config struct {
	// Signaller, when set, replaces the control-plane signalling client
	// (SignalURL, SignalDialer, PeerToken and PeerID are then ignored). See
	// package ghost/direct for peer-to-peer use with no server.
	Signaller Signaller

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

	// Roles are roles this member asks to hold, for example proto.RoleExit
	// for a node that serves an exit. They are sent in the hello, and the
	// control plane refuses the session unless the peer's enrolled roles
	// include every one of them: a member can require a role but never grant
	// itself one. NewHub always asks for proto.RoleHub as well. With a
	// ghost/direct Signaller they become this member's netmap roles.
	Roles []proto.Role

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

	// joinRetryMin and joinRetryMax bound the backoff between attempts to
	// join the network while the signalling session is up (defaults
	// DefaultJoinRetryMin and DefaultJoinRetryMax). They are unexported and
	// exist only for tests, which shorten them so a denied join is retried
	// within the test's own deadline.
	joinRetryMin time.Duration
	joinRetryMax time.Duration

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

// roles returns the configured roles plus extra, validated and deduplicated,
// in the order first given.
func (c *Config) roles(extra ...proto.Role) ([]proto.Role, error) {
	var out []proto.Role
	for _, r := range append(slices.Clone(c.Roles), extra...) {
		if !r.Valid() {
			return nil, fmt.Errorf("ghost: unknown role %q", r)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// joinBackoff returns the bounds of the join retry backoff.
func (c *Config) joinBackoff() (minDelay, maxDelay time.Duration) {
	minDelay, maxDelay = c.joinRetryMin, c.joinRetryMax
	if minDelay <= 0 {
		minDelay = DefaultJoinRetryMin
	}
	if maxDelay < minDelay {
		maxDelay = max(minDelay, DefaultJoinRetryMax)
	}
	return minDelay, maxDelay
}

func (c *Config) mtu() int {
	if c.MTU > 0 {
		return c.MTU
	}
	return DefaultMTU
}
