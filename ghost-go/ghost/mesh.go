package ghost

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/internal/wireguard"
	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Status is a snapshot of a Node or Hub's state.
type Status struct {
	// Connected is true once the signalling session is established.
	Connected bool
	// SignalState is the signalling client's connection state.
	SignalState signal.State
	// PeerID is the server-assigned peer id.
	PeerID string
	// Address is the assigned tunnel address (CIDR).
	Address string
	// Network is the joined network name.
	Network string
	// Peers is the number of connected tunnel peers.
	Peers int
	// Roles are this peer's roles as the control plane reports them (the
	// constructor's expected roles until the first netmap arrives).
	Roles []proto.Role
	// NetmapPeers is the number of peers in the current netmap.
	NetmapPeers int
}

// EventKind classifies a mesh Event.
type EventKind string

const (
	// EventSignalState reports a signalling connection-state change.
	EventSignalState EventKind = "signal_state"
	// EventJoined reports a successful network join with an assigned address.
	EventJoined EventKind = "joined"
	// EventNetmap reports a new netmap snapshot or delta.
	EventNetmap EventKind = "netmap"
	// EventPolicy reports a changed exit policy (Event.Policy).
	EventPolicy EventKind = "policy"
	// EventPeerConnected reports a tunnel peer becoming reachable.
	EventPeerConnected EventKind = "peer_connected"
	// EventPeerDisconnected reports a tunnel peer going away.
	EventPeerDisconnected EventKind = "peer_disconnected"
	// EventError reports a non-fatal error.
	EventError EventKind = "error"
)

// Event is emitted on a mesh's Events channel.
type Event struct {
	Kind          EventKind
	SignalState   signal.State
	PeerID        string
	Address       string
	CandidateType string
	Policy        *proto.ExitPolicy
	Err           error
}

// healthInterval is how often a member reports tunnel health.
const healthInterval = 30 * time.Second

// peerKeepalive is the WireGuard persistent keepalive the ICE controlling side
// of a link sends.
const peerKeepalive = 15 * time.Second

// handshakeWait bounds how long a newly wired link waits for its first
// WireGuard handshake before EventPeerConnected; handshakePoll is how often it
// checks.
const (
	handshakeWait = 20 * time.Second
	handshakePoll = 20 * time.Millisecond
)

// mesh is the shared machinery behind Node and Hub: a signalling client, one
// netstack-backed WireGuard tunnel, a MultiBind, and one ICE agent per peer.
// Which peers it connects to follows the netmap the control plane sends.
type mesh struct {
	cfg       Config
	keys      *Keys
	log       *slog.Logger
	wantRoles []proto.Role // roles the constructor expects this peer to hold

	sig  *signal.Client
	bind *ice.MultiBind
	wg   *wireguard.Tunnel
	net  *wireguard.Net

	mu      sync.Mutex
	address string
	pool    string
	peerID  string
	network string
	netmap  *proto.Netmap
	links   map[string]*peerLink
	epSeq   uint32
	refused atomic.Uint64
	started bool
	closed  bool

	tmetrics *tunnelMetrics
	msrv     *http.Server // tunnel-side metrics endpoint, when configured
	mfetch   *metrics.Client
	mfetchMu sync.Mutex

	events chan Event
	ctx    context.Context
	cancel context.CancelFunc
}

// peerLink is one ICE-connected tunnel peer.
type peerLink struct {
	peerID      string
	publicKey   string
	address     string // remote CIDR
	epKey       string
	controlling bool

	agent       ice.Agent
	conn        net.Conn
	connectOnce sync.Once
	remoteSet   bool
	candType    string
	added       bool
	failed      bool
}

func newMesh(cfg Config, wantRoles []proto.Role) (*mesh, error) {
	keys, err := LoadOrCreateKeys(cfg.KeyStorePath)
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	return &mesh{
		cfg:       cfg,
		keys:      keys,
		log:       cfg.logger(),
		wantRoles: wantRoles,
		links:     make(map[string]*peerLink),
		events:    make(chan Event, 64),
	}, nil
}

// Events returns the mesh event channel.
func (m *mesh) Events() <-chan Event { return m.events }

// Keys returns this member's key pair.
func (m *mesh) Keys() *Keys { return m.keys }

// Netstack returns the userspace network for DialContext/Listen. It is only
// valid once joined.
func (m *mesh) Netstack() *wireguard.Net {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.net
}

// Status returns a snapshot of the current state.
func (m *mesh) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	connected := 0
	for _, l := range m.links {
		if l.added {
			connected++
		}
	}
	var ss signal.State
	if m.sig != nil {
		ss = m.sig.State()
	}
	st := Status{
		Connected:   ss == signal.StateConnected,
		SignalState: ss,
		PeerID:      m.peerID,
		Address:     m.address,
		Network:     m.network,
		Peers:       connected,
		Roles:       slices.Clone(m.wantRoles),
	}
	if m.netmap != nil {
		st.Roles = slices.Clone(m.netmap.Self.Roles)
		st.NetmapPeers = len(m.netmap.Peers)
	}
	return st
}

// Netmap returns a copy of the current netmap, if one has arrived.
func (m *mesh) Netmap() (proto.Netmap, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.netmap == nil {
		return proto.Netmap{}, false
	}
	n := *m.netmap
	n.Peers = slices.Clone(n.Peers)
	return n, true
}

// Policy returns the exit policy from the current netmap, if any.
func (m *mesh) Policy() (proto.ExitPolicy, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.netmap == nil || m.netmap.Policy == nil {
		return proto.ExitPolicy{}, false
	}
	return *m.netmap.Policy, true
}

// Start connects to signalling and joins the network. It returns once the
// signalling session is up and the join request has been sent; peer
// connections form asynchronously (watch Events).
func (m *mesh) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("already started")
	}
	m.started = true
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	sigCfg := signal.Config{
		URL:       m.cfg.SignalURL,
		Dialer:    m.cfg.SignalDialer,
		PeerToken: m.cfg.PeerToken,
		PeerID:    m.cfg.PeerID,
		PublicKey: m.keys.PublicKey(),
		Roles:     m.wantRoles,
		Logger:    m.log,
		Health:    m.healthSummary,
	}
	m.sig = signal.New(sigCfg, signal.Handlers{})
	m.sig.Start(m.ctx)

	if err := m.registerMetrics(); err != nil {
		m.log.Warn("ghost: metrics registration failed", "error", err)
	}

	go m.loop()
	return nil
}

func (m *mesh) loop() {
	joined := false
	health := time.NewTicker(healthInterval)
	defer health.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-health.C:
			// Retry failed links and report health.
			m.reconcile()
			m.reportHealth()
		case ev, ok := <-m.sig.Events():
			if !ok {
				return
			}
			switch {
			case ev.State != "":
				m.emit(Event{Kind: EventSignalState, SignalState: ev.State})
				if ev.State == signal.StateConnected && !joined {
					joined = true
					if err := m.sig.Join(m.ctx, m.cfg.Network); err != nil {
						m.emit(Event{Kind: EventError, Err: err})
					}
				} else if ev.State == signal.StateDisconnected {
					joined = false
				}
			case ev.Welcome != nil:
				m.mu.Lock()
				m.peerID = ev.Welcome.PeerID
				m.mu.Unlock()
			case ev.Joined != nil:
				m.handleJoined(*ev.Joined)
			case ev.Netmap != nil:
				m.handleNetmap(*ev.Netmap)
			case ev.Delta != nil:
				m.handleDelta(*ev.Delta)
			case ev.Signal != nil:
				m.handleSignal(ev.Type, *ev.Signal)
			case ev.Err != nil:
				m.emit(Event{Kind: EventError, Err: fmt.Errorf("signal: %s", ev.Err.Message)})
			}
		}
	}
}

func (m *mesh) handleJoined(j proto.Joined) {
	m.mu.Lock()
	if m.wg == nil {
		if err := m.setupTunnelLocked(j); err != nil {
			m.mu.Unlock()
			m.emit(Event{Kind: EventError, Err: err})
			return
		}
	}
	m.network = j.Network
	var metricsErr error
	if m.cfg.Metrics != nil && m.msrv == nil {
		metricsErr = m.serveMetricsLocked()
	}
	addr := m.address
	m.mu.Unlock()
	if metricsErr != nil {
		m.emit(Event{Kind: EventError, Err: metricsErr})
	}
	m.emit(Event{Kind: EventJoined, Address: addr})
}

// setupTunnelLocked creates the netstack, bind and WireGuard tunnel. Caller
// holds m.mu.
func (m *mesh) setupTunnelLocked(j proto.Joined) error {
	address := m.cfg.Address
	if address == "" {
		address = j.Address
	}
	if address == "" {
		return fmt.Errorf("no address assigned")
	}
	m.address = address
	m.pool = j.Pool
	if m.pool == "" {
		m.pool = DefaultPool
	}

	prefix, err := netip.ParsePrefix(address)
	if err != nil {
		return fmt.Errorf("parse assigned address %q: %w", address, err)
	}
	var dns []netip.Addr
	for _, s := range m.cfg.DNSServers {
		if a, err := netip.ParseAddr(s); err == nil {
			dns = append(dns, a)
		}
	}

	tunDev, tnet, err := wireguard.CreateNetTUN([]netip.Addr{prefix.Addr()}, dns, m.cfg.mtu())
	if err != nil {
		return fmt.Errorf("create netstack: %w", err)
	}
	m.net = tnet
	m.bind = ice.NewMultiBind(m.log)

	wgCfg := wireguard.DefaultWireGuardConfig()
	wgCfg.PrivateKey = m.keys.privateKeyBytes()
	wgCfg.ListenPort = 0
	wgCfg.MTU = m.cfg.mtu()
	// Keepalive is set per peer (see wireUpPeer); a peer without one gets none.
	wgCfg.PersistentKeepalive = 0

	dev, err := wireguard.NewTunnel(tunDev, m.bind, wgCfg, m.log)
	if err != nil {
		return fmt.Errorf("create wireguard tunnel: %w", err)
	}
	if err := dev.Configure(m.keys.privateKeyBytes()); err != nil {
		return fmt.Errorf("configure wireguard tunnel: %w", err)
	}
	if err := dev.Up(); err != nil {
		return fmt.Errorf("wireguard tunnel up: %w", err)
	}
	m.wg = dev
	return nil
}

// handleNetmap replaces the netmap and reconciles links against it.
func (m *mesh) handleNetmap(n proto.Netmap) {
	m.mu.Lock()
	var oldPolicy *proto.ExitPolicy
	if m.netmap != nil {
		oldPolicy = m.netmap.Policy
	}
	m.netmap = &n
	m.mu.Unlock()
	m.afterNetmapChange(oldPolicy, n.Policy)
}

// handleDelta applies a delta to the current netmap and reconciles.
func (m *mesh) handleDelta(d proto.NetmapDelta) {
	m.mu.Lock()
	if m.netmap == nil {
		m.mu.Unlock()
		m.log.Debug("ghost: netmap delta before snapshot, ignored", "seq", d.Seq)
		return
	}
	oldPolicy := m.netmap.Policy
	next := m.netmap.Apply(d)
	m.netmap = &next
	m.mu.Unlock()
	m.afterNetmapChange(oldPolicy, next.Policy)
}

func (m *mesh) afterNetmapChange(oldPolicy, newPolicy *proto.ExitPolicy) {
	m.emit(Event{Kind: EventNetmap})
	if newPolicy != nil && (oldPolicy == nil || !samePolicy(*oldPolicy, *newPolicy)) {
		p := *newPolicy
		m.emit(Event{Kind: EventPolicy, Policy: &p})
	}
	m.reconcile()
}

func samePolicy(a, b proto.ExitPolicy) bool {
	return a.Revision == b.Revision && a.Paused == b.Paused && a.DailyBytes == b.DailyBytes &&
		a.BytesPerSecond == b.BytesPerSecond && slices.Equal(a.Allow, b.Allow)
}

// reconcile connects to every online netmap peer without a link and tears
// down links to peers that left the netmap, went offline, or changed key.
func (m *mesh) reconcile() {
	m.mu.Lock()
	if m.netmap == nil || m.wg == nil || m.closed {
		m.mu.Unlock()
		return
	}
	self := m.netmap.Self
	want := make(map[string]proto.PeerInfo, len(m.netmap.Peers))
	for _, p := range m.netmap.Peers {
		if p.Online && p.PublicKey != "" && m.linkableLocked(p) {
			want[p.PeerID] = p
		}
	}
	var stale []*peerLink
	for id, l := range m.links {
		p, ok := want[id]
		if !ok || p.PublicKey != l.publicKey || l.failed {
			stale = append(stale, l)
			delete(m.links, id)
		}
	}
	var connect []proto.PeerInfo
	for id, p := range want {
		if _, ok := m.links[id]; !ok {
			connect = append(connect, p)
		}
	}
	m.mu.Unlock()

	for _, l := range stale {
		m.teardownLink(l)
		if l.added {
			m.emit(Event{Kind: EventPeerDisconnected, PeerID: l.peerID})
		}
	}
	for _, p := range connect {
		m.connectToPeer(p, proto.Controlling(self.PeerID, self.Roles, p.PeerID, p.Roles))
	}
	if len(stale) > 0 {
		m.reportHealth()
	}
}

// netmapPeer returns a peer from the current netmap.
func (m *mesh) netmapPeer(id string) (proto.PeerInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.netmapPeerLocked(id)
}

// netmapPeerLocked is netmapPeer with m.mu held.
func (m *mesh) netmapPeerLocked(id string) (proto.PeerInfo, bool) {
	if m.netmap == nil {
		return proto.PeerInfo{}, false
	}
	for _, p := range m.netmap.Peers {
		if p.PeerID == id {
			return p, true
		}
	}
	return proto.PeerInfo{}, false
}

// connectToPeer creates (idempotently) a link to peer and drives ICE. When
// controlling, it sends an Offer; otherwise it waits for one but starts
// gathering immediately so candidates can trickle.
func (m *mesh) connectToPeer(peer proto.PeerInfo, controlling bool) {
	m.mu.Lock()
	if m.closed || m.wg == nil {
		m.mu.Unlock()
		return
	}
	if _, ok := m.links[peer.PeerID]; ok {
		m.mu.Unlock()
		return
	}
	epKey := ice.EndpointKey(atomic.AddUint32(&m.epSeq, 1))
	l := &peerLink{
		peerID:      peer.PeerID,
		publicKey:   peer.PublicKey,
		address:     peer.Address,
		epKey:       epKey,
		controlling: controlling,
	}
	m.links[peer.PeerID] = l
	m.mu.Unlock()

	agent, err := ice.NewAgent(m.iceConfig(), m.log)
	if err != nil {
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("ice agent: %w", err)})
		return
	}
	// Set the agent under the lock: heartbeats snapshot links concurrently,
	// and a link closed or torn down meanwhile must not leak the agent.
	m.mu.Lock()
	if m.closed || m.links[peer.PeerID] != l {
		m.mu.Unlock()
		_ = agent.Close()
		return
	}
	l.agent = agent
	m.mu.Unlock()

	// Gather and trickle candidates to the peer.
	go m.gather(l)

	if controlling {
		ufrag, pwd := agent.LocalCredentials()
		_ = m.sig.SendOffer(m.ctx, proto.Signal{Network: m.network, To: peer.PeerID, Ufrag: ufrag, Pwd: pwd})
	}
}

func (m *mesh) gather(l *peerLink) {
	ch, err := l.agent.GatherCandidates(m.ctx)
	if err != nil {
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("gather: %w", err)})
		return
	}
	for cand := range ch {
		_ = m.sig.SendCandidate(m.ctx, proto.Signal{
			Network:   m.network,
			To:        l.peerID,
			Candidate: candidateToProto(cand),
		})
	}
}

func (m *mesh) handleSignal(t proto.Type, s proto.Signal) {
	m.mu.Lock()
	l := m.links[s.From]
	m.mu.Unlock()

	switch t {
	case proto.TypeOffer:
		// Inbound offer from a netmap peer we have no link to yet (it came
		// online after our last reconcile): create the controlled side.
		if l == nil {
			p, ok := m.netmapPeer(s.From)
			if !ok {
				m.log.Debug("ghost: offer from a peer outside the netmap, ignored", "peer", s.From)
				return
			}
			m.mu.Lock()
			linkable := m.linkableLocked(p)
			m.mu.Unlock()
			if !linkable {
				m.refuseLink(p.PeerID)
				return
			}
			m.connectToPeer(p, false)
			m.mu.Lock()
			l = m.links[s.From]
			m.mu.Unlock()
		}
		if l == nil || l.agent == nil {
			return
		}
		_ = l.agent.SetRemoteCredentials(s.Ufrag, s.Pwd)
		l.remoteSet = true
		uf, pw := l.agent.LocalCredentials()
		_ = m.sig.SendAnswer(m.ctx, proto.Signal{Network: m.network, To: s.From, Ufrag: uf, Pwd: pw})
		m.startConnect(l)
	case proto.TypeAnswer:
		if l == nil || l.agent == nil {
			return
		}
		_ = l.agent.SetRemoteCredentials(s.Ufrag, s.Pwd)
		l.remoteSet = true
		m.startConnect(l)
	case proto.TypeCandidate:
		if l == nil || l.agent == nil || s.Candidate == nil {
			return
		}
		_ = l.agent.AddRemoteCandidate(protoToCandidate(s.Candidate))
	}
}

// startConnect runs ICE Connect once per link and, on success, wires the
// connection into the WireGuard tunnel.
func (m *mesh) startConnect(l *peerLink) {
	l.connectOnce.Do(func() {
		go func() {
			connCtx := m.ctx
			if m.cfg.ConnectTimeout > 0 {
				var cancel context.CancelFunc
				connCtx, cancel = context.WithTimeout(m.ctx, m.cfg.ConnectTimeout)
				defer cancel()
			}
			conn, err := l.agent.Connect(connCtx, l.controlling)
			if err != nil {
				m.mu.Lock()
				l.failed = true
				m.mu.Unlock()
				m.emit(Event{Kind: EventError, Err: fmt.Errorf("ice connect to %s: %w", l.peerID, err)})
				m.reportHealth()
				return
			}
			candType := ""
			if pair, err := l.agent.GetSelectedCandidatePair(); err == nil && pair.Local != nil {
				candType = string(pair.Local.Type)
			}
			m.mu.Lock()
			l.conn = conn
			l.candType = candType
			m.mu.Unlock()
			if !m.wireUpPeer(l) {
				return
			}
			if !m.awaitHandshake(l) {
				return
			}
			m.emit(Event{Kind: EventPeerConnected, PeerID: l.peerID, Address: l.address, CandidateType: candType})
			m.reportHealth()
		}()
	})
}

// wireUpPeer registers the ICE connection in the bind and adds the peer to the
// WireGuard tunnel. It reports whether the peer was added.
func (m *mesh) wireUpPeer(l *peerLink) bool {
	m.mu.Lock()
	if m.closed || m.wg == nil || l.conn == nil || m.links[l.peerID] != l {
		m.mu.Unlock()
		return false
	}
	// Last line of defence: never configure a WireGuard key that hub-only
	// isolation forbids, even if the link got this far.
	if p, ok := m.netmapPeerLocked(l.peerID); !ok || !m.linkableLocked(p) {
		m.mu.Unlock()
		m.refuseLink(l.peerID)
		return false
	}
	pubKey, err := wireguard.DecodeKey(l.publicKey)
	if err != nil {
		m.mu.Unlock()
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("decode peer key: %w", err)})
		return false
	}
	// Exactly one side of a link starts the WireGuard handshake: the ICE
	// controlling side. Its persistent keepalive fires the moment the peer is
	// added, which initiates the handshake at once. If both sides did that,
	// they would wire up within milliseconds of each other (ICE completes on
	// both ends together) and send crossing initiations; each side consumes the
	// other's initiation, which discards its own pending one, so both responses
	// are rejected and the tunnel stays down until the 5s retry, which under
	// load can collide again. The controlled side only responds: ICE's own
	// keepalives hold the path open from both ends, and the controlling side,
	// as the session's initiator, also owns its rekeys.
	pc := &wireguard.PeerConfig{
		PublicKey:  pubKey,
		AllowedIPs: m.allowedIPsFor(l),
		Endpoint:   l.epKey,
	}
	if l.controlling {
		pc.PersistentKeepalive = peerKeepalive
		// The initiation goes out during AddPeer, so the connection must be
		// in the bind first.
		m.bind.SetConn(l.epKey, l.conn)
	}
	if err := m.wg.AddPeer(pc); err != nil {
		if l.controlling {
			m.bind.RemoveConn(l.epKey)
		}
		m.mu.Unlock()
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("add wg peer: %w", err)})
		return false
	}
	if !l.controlling {
		// The controlled side registers the connection only once the peer
		// exists, so the controlling side's first initiation (possibly already
		// buffered on the ICE connection) is never read before WireGuard knows
		// the key and dropped until the retry.
		m.bind.SetConn(l.epKey, l.conn)
	}
	l.added = true
	m.mu.Unlock()
	return true
}

// awaitHandshake waits, up to handshakeWait, for the first WireGuard handshake
// with l's peer, so EventPeerConnected means the tunnel carries traffic. It
// matters most on the ICE controlled side, which never initiates: an app that
// sent data there before the controlling side's initiation arrived would
// start a second, crossing handshake. If the wait runs out the link stays
// wired (WireGuard keeps retrying), an EventError reports it and it still
// reports true. It reports false when the mesh closed or the link was torn
// down or replaced meanwhile, so the peer must not be announced.
func (m *mesh) awaitHandshake(l *peerLink) bool {
	deadline := time.NewTimer(handshakeWait)
	defer deadline.Stop()
	tick := time.NewTicker(handshakePoll)
	defer tick.Stop()
	for {
		m.mu.Lock()
		dev, current := m.wg, !m.closed && m.links[l.peerID] == l
		m.mu.Unlock()
		if dev == nil || !current {
			return false
		}
		status, err := dev.GetStatus()
		if err != nil {
			return false // tunnel closed
		}
		if _, ok := parseHandshakeTimes(status)[l.publicKey]; ok {
			return true
		}
		select {
		case <-m.ctx.Done():
			return false
		case <-deadline.C:
			m.emit(Event{Kind: EventError, PeerID: l.peerID,
				Err: fmt.Errorf("wireguard handshake with %s not complete after %s", l.peerID, handshakeWait)})
			return true
		case <-tick.C:
		}
	}
}

// allowedIPsFor returns the WireGuard AllowedIPs for a link: exactly the
// peer's own tunnel address as a /32 (or /128), whatever prefix length the
// netmap gives. Every reachable peer has its own link, so no peer routes for
// another; a packet for any other address has no route and is dropped.
func (m *mesh) allowedIPsFor(l *peerLink) []string {
	if m.cfg.allowedIPsOverride != nil {
		return m.cfg.allowedIPsOverride(l.peerID, l.address)
	}
	if a, ok := tunnelAddr(l.address); ok {
		return []string{netip.PrefixFrom(a, a.BitLen()).String()}
	}
	return nil
}

// linkableLocked reports whether this member may hold a WireGuard link to p.
// Under hub-only isolation a member without the hub role links only to hubs,
// even if the netmap (from a faulty control plane) lists other peers. Caller
// holds m.mu.
func (m *mesh) linkableLocked(p proto.PeerInfo) bool {
	return !m.hubOnlyLocked() || proto.HasRole(p.Roles, proto.RoleHub)
}

// refuseLink records a link refused by hub-only isolation.
func (m *mesh) refuseLink(peerID string) {
	m.refused.Add(1)
	m.log.Warn("ghost: link refused by hub-only isolation", "peer", peerID)
	m.emit(Event{Kind: EventError, PeerID: peerID, Err: fmt.Errorf("link to non-hub peer %s refused: network is hub-only", peerID)})
}

// RefusedLinks returns how many links hub-only isolation refused (offers
// from, or netmap entries for, peers without the hub role).
func (m *mesh) RefusedLinks() uint64 { return m.refused.Load() }

// ForwardDrops returns how many inbound tunnel packets addressed to another
// peer were dropped: a member never forwards between peers.
func (m *mesh) ForwardDrops() uint64 {
	if n := m.Netstack(); n != nil {
		return n.ForwardDrops()
	}
	return 0
}

// reportHealth sends a heartbeat carrying the current health summary now,
// instead of waiting for the next heartbeat tick.
func (m *mesh) reportHealth() {
	if m.sig == nil || m.sig.State() != signal.StateConnected {
		return
	}
	_ = m.sig.SendHeartbeat(m.ctx)
}

// healthSummary builds the compact health summary carried by heartbeats: one
// entry per link, plus the exit's cap usage and pause state when metrics are
// configured.
func (m *mesh) healthSummary() *proto.Health {
	stats := m.linkSnapshot()
	h := &proto.Health{Links: make([]proto.LinkHealth, 0, len(stats))}
	m.mu.Lock()
	failed := map[string]bool{}
	for id, l := range m.links {
		failed[id] = l.failed
	}
	m.mu.Unlock()
	for _, s := range stats {
		lh := proto.LinkHealth{
			PeerID:        s.peerID,
			State:         proto.LinkConnecting,
			CandidateType: s.candType,
			RTTSeconds:    s.rttSeconds,
			RxBytes:       s.rx,
			TxBytes:       s.tx,
		}
		switch {
		case s.added:
			lh.State = proto.LinkConnected
		case failed[s.peerID]:
			lh.State = proto.LinkFailed
		}
		if s.handshakeAgeSeconds >= 0 {
			lh.HandshakeAgeSeconds = s.handshakeAgeSeconds
		}
		h.Links = append(h.Links, lh)
	}
	slices.SortFunc(h.Links, func(a, b proto.LinkHealth) int {
		switch {
		case a.PeerID < b.PeerID:
			return -1
		case a.PeerID > b.PeerID:
			return 1
		}
		return 0
	})
	if c := m.collector(); c != nil {
		t := c.Snapshot().Totals
		h.Exit = &proto.ExitHealth{CapUsedBytes: t.CapUsedBytes, CapLimitBytes: t.CapLimitBytes, Paused: t.Paused}
	}
	return h
}

func (m *mesh) iceConfig() *ice.ICEConfig {
	c := ice.DefaultICEConfig()
	c.STUNServers = nil
	for _, s := range m.cfg.STUNServers {
		c.STUNServers = append(c.STUNServers, s.URL)
	}
	for _, t := range m.cfg.TURNServers {
		c.TURNServers = append(c.TURNServers, ice.TURNServer{URLs: t.URLs, Username: t.Username, Password: t.Password})
	}
	// Merge TURN servers advertised by the signalling Welcome.
	if m.sig != nil {
		for _, s := range m.sig.Welcome().ICEServers {
			if s.Username != "" {
				c.TURNServers = append(c.TURNServers, ice.TURNServer{URLs: s.URLs, Username: s.Username, Password: s.Credential})
			} else {
				c.STUNServers = append(c.STUNServers, s.URLs...)
			}
		}
	}
	c.PortMin = m.cfg.PortMin
	c.PortMax = m.cfg.PortMax
	if m.cfg.ConnectTimeout > 0 {
		c.ConnectionTimeout = m.cfg.ConnectTimeout
	}
	if m.cfg.iceTuner != nil {
		m.cfg.iceTuner(c)
	}
	return c
}

func (m *mesh) teardownLink(l *peerLink) {
	if l.added && l.publicKey != "" {
		if pub, err := wireguard.DecodeKey(l.publicKey); err == nil {
			m.mu.Lock()
			if m.wg != nil {
				_ = m.wg.RemovePeer(pub)
			}
			m.mu.Unlock()
		}
	}
	if m.bind != nil {
		m.bind.RemoveConn(l.epKey)
	}
	if l.conn != nil {
		_ = l.conn.Close()
	}
	if l.agent != nil {
		_ = l.agent.Close()
	}
}

func (m *mesh) emit(ev Event) {
	select {
	case m.events <- ev:
	default:
	}
}

// Close shuts down the mesh and releases all resources.
func (m *mesh) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	cancel := m.cancel
	links := m.links
	m.links = make(map[string]*peerLink)
	dev := m.wg
	bind := m.bind
	sig := m.sig
	msrv := m.msrv
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if msrv != nil {
		_ = msrv.Close()
	}
	for _, l := range links {
		if l.conn != nil {
			_ = l.conn.Close()
		}
		if l.agent != nil {
			_ = l.agent.Close()
		}
	}
	if dev != nil {
		_ = dev.Close()
	}
	if bind != nil {
		bind.Shutdown()
	}
	if sig != nil {
		_ = sig.Close()
	}
	if m.tmetrics != nil && m.tmetrics.reg != nil {
		_ = m.tmetrics.reg.Unregister()
	}
	return nil
}

func candidateToProto(c *ice.Candidate) *proto.Candidate {
	if c == nil {
		return nil
	}
	return &proto.Candidate{
		Type:           string(c.Type),
		Address:        c.Address,
		Port:           c.Port,
		Protocol:       c.Protocol,
		Priority:       c.Priority,
		Foundation:     c.Foundation,
		RelatedAddress: c.RelatedAddress,
		RelatedPort:    c.RelatedPort,
	}
}

func protoToCandidate(c *proto.Candidate) *ice.Candidate {
	if c == nil {
		return nil
	}
	return &ice.Candidate{
		Type:           ice.CandidateType(c.Type),
		Address:        c.Address,
		Port:           c.Port,
		Protocol:       c.Protocol,
		Priority:       c.Priority,
		Foundation:     c.Foundation,
		RelatedAddress: c.RelatedAddress,
		RelatedPort:    c.RelatedPort,
	}
}
