package ghost

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
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
	// DeviceID is the server-assigned device id.
	DeviceID string
	// Address is the assigned tunnel address (CIDR).
	Address string
	// Network is the joined network name.
	Network string
	// Peers is the number of connected tunnel peers.
	Peers int
	// Role is this member's role.
	Role proto.Role
}

// EventKind classifies a mesh Event.
type EventKind string

const (
	// EventSignalState reports a signalling connection-state change.
	EventSignalState EventKind = "signal_state"
	// EventJoined reports a successful network join with an assigned address.
	EventJoined EventKind = "joined"
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
	Err           error
}

// mesh is the shared machinery behind Node and Hub: a signalling client, one
// netstack-backed WireGuard device, a MultiBind, and one ICE agent per peer.
type mesh struct {
	cfg         Config
	keys        *Keys
	log         *slog.Logger
	role        proto.Role
	controlling bool // true when this member drives ICE (the hub)

	sig    *signal.Client
	bind   *ice.MultiBind
	device *wireguard.Device
	net    *wireguard.Net

	mu       sync.Mutex
	address  string
	pool     string
	deviceID string
	network  string
	links    map[string]*peerLink
	epSeq    uint32
	started  bool
	closed   bool

	tmetrics *tunnelMetrics
	hubID    string       // the hub this node joined through (nodes only)
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
}

func newMesh(cfg Config, role proto.Role) (*mesh, error) {
	keys, err := LoadOrCreateKeys(cfg.KeyStorePath)
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	return &mesh{
		cfg:         cfg,
		keys:        keys,
		log:         cfg.logger(),
		role:        role,
		controlling: role == proto.RoleHub,
		links:       make(map[string]*peerLink),
		events:      make(chan Event, 64),
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
	return Status{
		Connected:   ss == signal.StateConnected,
		SignalState: ss,
		DeviceID:    m.deviceID,
		Address:     m.address,
		Network:     m.network,
		Peers:       connected,
		Role:        m.role,
	}
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
		URL:         m.cfg.SignalURL,
		Dialer:      m.cfg.SignalDialer,
		DeviceToken: m.cfg.DeviceToken,
		DeviceID:    m.cfg.DeviceID,
		PublicKey:   m.keys.PublicKey(),
		Role:        m.role,
		Logger:      m.log,
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
	for {
		select {
		case <-m.ctx.Done():
			return
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
				m.deviceID = ev.Welcome.DeviceID
				m.mu.Unlock()
			case ev.Joined != nil:
				m.handleJoined(*ev.Joined)
			case ev.Signal != nil:
				m.handleSignal(ev.Type, *ev.Signal)
			case ev.Peer != nil:
				switch ev.Type {
				case proto.TypePeerOnline:
					m.handlePeerOnline(ev.Peer.Peer)
				case proto.TypePeerOffline:
					m.handlePeerOffline(ev.Peer.Peer)
				}
			case ev.Err != nil:
				m.emit(Event{Kind: EventError, Err: fmt.Errorf("signal: %s", ev.Err.Message)})
			}
		}
	}
}

func (m *mesh) handleJoined(j proto.Joined) {
	m.mu.Lock()
	if m.device == nil {
		if err := m.setupDeviceLocked(j); err != nil {
			m.mu.Unlock()
			m.emit(Event{Kind: EventError, Err: err})
			return
		}
	}
	m.network = j.Network
	if j.Hub != nil {
		m.hubID = j.Hub.DeviceID
	}
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

	// The hub connects to every peer already present. A node connects to its
	// hub.
	if m.controlling {
		for _, p := range j.Peers {
			if p.Role == proto.RoleNode {
				m.connectToPeer(p, true)
			}
		}
	} else if j.Hub != nil {
		m.connectToPeer(*j.Hub, false)
	}
}

// setupDeviceLocked creates the netstack, bind and WireGuard device. Caller
// holds m.mu.
func (m *mesh) setupDeviceLocked(j proto.Joined) error {
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

	dev, err := wireguard.NewDevice(tunDev, m.bind, wgCfg, m.log)
	if err != nil {
		return fmt.Errorf("create device: %w", err)
	}
	if err := dev.Configure(m.keys.privateKeyBytes()); err != nil {
		return fmt.Errorf("configure device: %w", err)
	}
	if err := dev.Up(); err != nil {
		return fmt.Errorf("device up: %w", err)
	}
	m.device = dev
	return nil
}

func (m *mesh) handlePeerOnline(p proto.PeerInfo) {
	// Only the hub proactively connects to nodes. A node ignores other nodes.
	if m.controlling && p.Role == proto.RoleNode {
		m.connectToPeer(p, true)
	}
}

func (m *mesh) handlePeerOffline(p proto.PeerInfo) {
	m.mu.Lock()
	l := m.links[p.DeviceID]
	delete(m.links, p.DeviceID)
	m.mu.Unlock()
	if l == nil {
		return
	}
	m.teardownLink(l)
	m.emit(Event{Kind: EventPeerDisconnected, PeerID: p.DeviceID})
}

// connectToPeer creates (idempotently) a link to peer and drives ICE. When
// controlling, it sends an Offer; otherwise it waits for one but starts
// gathering immediately so candidates can trickle.
func (m *mesh) connectToPeer(peer proto.PeerInfo, controlling bool) {
	m.mu.Lock()
	if m.closed || m.device == nil {
		m.mu.Unlock()
		return
	}
	if _, ok := m.links[peer.DeviceID]; ok {
		m.mu.Unlock()
		return
	}
	epKey := ice.EndpointKey(atomic.AddUint32(&m.epSeq, 1))
	l := &peerLink{
		peerID:      peer.DeviceID,
		publicKey:   peer.PublicKey,
		address:     peer.Address,
		epKey:       epKey,
		controlling: controlling,
	}
	m.links[peer.DeviceID] = l
	m.mu.Unlock()

	iceCfg := m.iceConfig()
	agent, err := ice.NewAgent(iceCfg, m.log)
	if err != nil {
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("ice agent: %w", err)})
		return
	}
	l.agent = agent

	// Gather and trickle candidates to the peer.
	go m.gather(l)

	if controlling {
		ufrag, pwd := agent.LocalCredentials()
		_ = m.sig.SendOffer(m.ctx, proto.Signal{Network: m.network, To: peer.DeviceID, Ufrag: ufrag, Pwd: pwd})
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
		// Inbound offer: create the link if needed (controlled side).
		if l == nil {
			m.connectToPeer(proto.PeerInfo{DeviceID: s.From}, false)
			m.mu.Lock()
			l = m.links[s.From]
			m.mu.Unlock()
		}
		if l == nil || l.agent == nil {
			return
		}
		_ = l.agent.SetRemoteCredentials(s.Ufrag, s.Pwd)
		l.remoteSet = true
		// Answer with our credentials.
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
// connection into the WireGuard device.
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
				m.emit(Event{Kind: EventError, Err: fmt.Errorf("ice connect to %s: %w", l.peerID, err)})
				return
			}
			l.conn = conn
			if pair, err := l.agent.GetSelectedCandidatePair(); err == nil && pair.Local != nil {
				l.candType = string(pair.Local.Type)
			}
			m.wireUpPeer(l)
		}()
	})
}

// wireUpPeer registers the ICE connection in the bind and adds the peer to the
// WireGuard device.
func (m *mesh) wireUpPeer(l *peerLink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.device == nil || l.conn == nil {
		return
	}
	m.bind.SetConn(l.epKey, l.conn)

	pubKey, err := wireguard.DecodeKey(l.publicKey)
	if err != nil {
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("decode peer key: %w", err)})
		return
	}
	allowed := m.allowedIPsFor(l)
	pc := &wireguard.PeerConfig{
		PublicKey:           pubKey,
		AllowedIPs:          allowed,
		Endpoint:            l.epKey,
		PersistentKeepalive: 15 * time.Second,
	}
	if err := m.device.AddPeer(pc); err != nil {
		m.emit(Event{Kind: EventError, Err: fmt.Errorf("add wg peer: %w", err)})
		return
	}
	l.added = true
	m.emit(Event{Kind: EventPeerConnected, PeerID: l.peerID, Address: l.address, CandidateType: l.candType})
}

// allowedIPsFor returns the WireGuard AllowedIPs for a link. A node routes the
// whole pool through its hub; a hub routes only the node's own address.
func (m *mesh) allowedIPsFor(l *peerLink) []string {
	if m.controlling {
		// Hub -> node: just the node's address.
		if l.address != "" {
			return []string{normalizeCIDR(l.address, true)}
		}
		return []string{m.pool}
	}
	// Node -> hub: route the whole pool.
	return []string{m.pool}
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
			if m.device != nil {
				_ = m.device.RemovePeer(pub)
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
	dev := m.device
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

// normalizeCIDR ensures addr is in CIDR form. When host32 is true a bare IP
// becomes a /32 (IPv4) or /128 (IPv6).
func normalizeCIDR(addr string, host32 bool) string {
	if _, err := netip.ParsePrefix(addr); err == nil {
		return addr
	}
	if a, err := netip.ParseAddr(addr); err == nil {
		if a.Is4() {
			return addr + "/32"
		}
		return addr + "/128"
	}
	return addr
}
