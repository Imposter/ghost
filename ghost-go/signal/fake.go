package signal

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sync"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// FakeServer is an in-memory signalling server for tests. It implements the
// v1 protocol with a permissive policy: it authenticates any non-empty peer
// token, takes a peer's roles from its hello (default node) unless the token
// was registered with AddPeer (then the registered roles, tags and labels
// apply, and a hello asking for a role the peer lacks is refused), assigns
// addresses from a pool, gives every joined peer a netmap of every other
// joined peer in its network (full mesh, no packet filter), sends deltas as
// peers join and leave, and relays offer/answer/candidate. With HubOnly set it
// applies hub-only isolation instead: peers without the hub role see, and may
// signal, only hubs. It is safe for concurrent use.
//
// Obtain a Dialer with (*FakeServer).Dialer and set it on signal.Config.Dialer
// (or ghost.Config.SignalDialer) to run a Node/Hub entirely in memory.
type FakeServer struct {
	mu       sync.Mutex
	pool     netip.Prefix
	nextHost uint32
	tokens   map[string]bool     // allowed peer tokens; nil means allow any
	peers    map[string]FakePeer // registered peers by token
	sessions map[string]*fakeSession
	networks map[string]map[string]*fakeSession // network -> peerID -> session
	policies map[string]*proto.ExitPolicy
	seq      int

	// AuthFunc, if set, decides whether a hello is accepted. It returns a
	// peer id to use (empty to derive one) and an error to reject.
	AuthFunc func(h proto.Hello) (peerID string, err error)
	// HubOnly applies hub-only isolation. Set it before any peer connects.
	HubOnly bool
	// IgnoreIsolation models a faulty or compromised control plane: netmaps
	// still say hub-only when HubOnly is set, but list every peer and relay
	// every signal, so tests can check that peers enforce isolation
	// themselves. Set it before any peer connects.
	IgnoreIsolation bool
}

func (s *FakeServer) isolation() proto.Isolation {
	if s.HubOnly {
		return proto.IsolationHubOnly
	}
	return proto.IsolationNone
}

// visibleLocked reports whether a and b may see each other. Caller holds s.mu.
func (s *FakeServer) visibleLocked(a, b *fakeSession) bool {
	return !s.HubOnly || s.IgnoreIsolation || proto.HasRole(a.roles, proto.RoleHub) || proto.HasRole(b.roles, proto.RoleHub)
}

// FakePeer is what a FakeServer knows about a registered peer token: the
// identity and attributes a control plane holds for an enrolled peer.
type FakePeer struct {
	// ID is the peer id. Empty derives one, as for unregistered tokens.
	ID string
	// Name is the peer's display name in netmaps.
	Name string
	// Roles are the peer's enrolled roles (empty: node). They are the
	// session's roles whatever the hello asks for, and a hello asking for a
	// role not listed here is refused with unauthorized.
	Roles []proto.Role
	// Tags and Labels are sent in netmaps.
	Tags   []string
	Labels map[string]string
}

type fakeSession struct {
	peerID   string
	name     string
	roles    []proto.Role
	tags     []string
	labels   map[string]string
	pubKey   string
	network  string
	address  string
	seq      int64
	toClient chan proto.Envelope
	srv      *FakeServer
	closed   bool
	mu       sync.Mutex
}

// NewFakeServer creates a fake server assigning addresses from pool. If pool
// is invalid, the default 100.64.0.0/10 is used.
func NewFakeServer(pool string) *FakeServer {
	p, err := netip.ParsePrefix(pool)
	if err != nil {
		p = netip.MustParsePrefix("100.64.0.0/10")
	}
	return &FakeServer{
		pool:     p,
		nextHost: 2,
		sessions: make(map[string]*fakeSession),
		networks: make(map[string]map[string]*fakeSession),
		policies: make(map[string]*proto.ExitPolicy),
	}
}

// AllowToken restricts accepted peer tokens. If never called, any non-empty
// token is accepted.
func (s *FakeServer) AllowToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]bool)
	}
	s.tokens[token] = true
}

// AddPeer registers token as an enrolled peer with p's id, name, roles, tags
// and labels. Like AllowToken, it restricts accepted tokens to the registered
// and allowed ones.
func (s *FakeServer) AddPeer(token string, p FakePeer) {
	p.Roles = slices.Clone(p.Roles)
	p.Tags = slices.Clone(p.Tags)
	p.Labels = maps.Clone(p.Labels)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]bool)
	}
	s.tokens[token] = true
	if s.peers == nil {
		s.peers = make(map[string]FakePeer)
	}
	s.peers[token] = p
}

// SetPolicy sets a network's exit policy and pushes it to joined peers as a
// netmap delta.
func (s *FakeServer) SetPolicy(p proto.ExitPolicy) {
	s.mu.Lock()
	s.policies[p.Network] = &p
	var targets []*fakeSession
	for _, m := range s.networks[p.Network] {
		targets = append(targets, m)
	}
	s.mu.Unlock()
	for _, t := range targets {
		pc := p
		t.sendDelta(proto.NetmapDelta{Network: p.Network, Policy: &pc})
	}
}

// Dialer returns a Dialer that opens in-memory connections to this server.
func (s *FakeServer) Dialer() Dialer { return fakeDialer{s: s} }

func (s *FakeServer) assignAddress() string {
	v := s.pool.Addr().As4()
	base := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	ip := base + s.nextHost
	s.nextHost++
	out := netip.AddrFrom4([4]byte{byte(ip >> 24), byte(ip >> 16), byte(ip >> 8), byte(ip)})
	return fmt.Sprintf("%s/32", out.String())
}

type fakeDialer struct{ s *FakeServer }

func (d fakeDialer) Dial(ctx context.Context, _ string) (Conn, error) {
	sess := &fakeSession{
		srv:      d.s,
		toClient: make(chan proto.Envelope, 64),
	}
	return &fakeClientConn{sess: sess}, nil
}

// fakeClientConn is the client side of an in-memory connection.
type fakeClientConn struct {
	sess *fakeSession
}

func (c *fakeClientConn) Read(ctx context.Context) (proto.Envelope, error) {
	select {
	case env, ok := <-c.sess.toClient:
		if !ok {
			return proto.Envelope{}, ErrNotConnected
		}
		return env, nil
	case <-ctx.Done():
		return proto.Envelope{}, ctx.Err()
	}
}

func (c *fakeClientConn) Write(ctx context.Context, env proto.Envelope) error {
	return c.sess.handleClient(env)
}

func (c *fakeClientConn) Close() error {
	c.sess.close()
	return nil
}

func (sess *fakeSession) sendToClient(t proto.Type, payload any) {
	env, err := proto.Encode(t, payload)
	if err != nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return
	}
	select {
	case sess.toClient <- env:
	default:
	}
}

func (sess *fakeSession) sendDelta(d proto.NetmapDelta) {
	sess.mu.Lock()
	sess.seq++
	d.Seq = sess.seq
	sess.mu.Unlock()
	sess.sendToClient(proto.TypeNetmapDelta, d)
}

// infoLocked describes sess for other peers. Caller holds srv.mu.
func (sess *fakeSession) infoLocked() proto.PeerInfo {
	return proto.PeerInfo{
		PeerID: sess.peerID, Name: sess.name, PublicKey: sess.pubKey, Address: sess.address,
		Roles: slices.Clone(sess.roles), Tags: slices.Clone(sess.tags), Labels: maps.Clone(sess.labels), Online: true,
	}
}

func (sess *fakeSession) handleClient(env proto.Envelope) error {
	s := sess.srv
	switch env.Type {
	case proto.TypeHello:
		var h proto.Hello
		if err := env.Decode(&h); err != nil {
			sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeBadRequest, Message: "bad hello", Fatal: true})
			return nil
		}
		if h.Version != proto.Version {
			sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnsupportedVersion, Message: "unsupported version", Fatal: true})
			return nil
		}
		peerID := h.PeerID
		if s.AuthFunc != nil {
			id, err := s.AuthFunc(h)
			if err != nil {
				sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnauthorized, Message: err.Error(), Fatal: true})
				return nil
			}
			if id != "" {
				peerID = id
			}
		} else {
			s.mu.Lock()
			tokenOK := h.PeerToken != "" && (s.tokens == nil || s.tokens[h.PeerToken])
			s.mu.Unlock()
			if !tokenOK {
				sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnauthorized, Message: "invalid peer token", Fatal: true})
				return nil
			}
		}
		s.mu.Lock()
		reg, registered := s.peers[h.PeerToken]
		roles := slices.Clone(h.Roles)
		if registered {
			if reg.ID != "" {
				if h.PeerID != "" && h.PeerID != reg.ID {
					s.mu.Unlock()
					sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnauthorized, Message: "peer id does not match token", Fatal: true})
					return nil
				}
				peerID = reg.ID
			}
			roles = slices.Clone(reg.Roles)
			if len(roles) == 0 {
				roles = []proto.Role{proto.RoleNode}
			}
			for _, want := range h.Roles {
				if !proto.HasRole(roles, want) {
					s.mu.Unlock()
					sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnauthorized,
						Message: fmt.Sprintf("peer does not hold the %s role", want), Fatal: true})
					return nil
				}
			}
			sess.name, sess.tags, sess.labels = reg.Name, slices.Clone(reg.Tags), maps.Clone(reg.Labels)
		}
		s.seq++
		if peerID == "" {
			peerID = fmt.Sprintf("peer-%d", s.seq)
		}
		sess.peerID = peerID
		sess.roles = roles
		if len(sess.roles) == 0 {
			sess.roles = []proto.Role{proto.RoleNode}
		}
		sess.pubKey = h.PublicKey
		s.sessions[peerID] = sess
		sessionID := fmt.Sprintf("sess-%d", s.seq)
		s.mu.Unlock()
		sess.sendToClient(proto.TypeWelcome, proto.Welcome{
			Version:           proto.Version,
			PeerID:            peerID,
			SessionID:         sessionID,
			HeartbeatInterval: 20,
		})
		return nil

	case proto.TypeJoinNetwork:
		var j proto.JoinNetwork
		_ = env.Decode(&j)
		s.mu.Lock()
		sess.network = j.Network
		sess.address = s.assignAddress()
		members := s.networks[j.Network]
		if members == nil {
			members = make(map[string]*fakeSession)
			s.networks[j.Network] = members
		}
		var peers []proto.PeerInfo
		var notify []*fakeSession
		for _, other := range members {
			if !s.visibleLocked(sess, other) {
				continue
			}
			peers = append(peers, other.infoLocked())
			notify = append(notify, other)
		}
		members[sess.peerID] = sess
		self := sess.infoLocked()
		policy := s.policies[j.Network]
		pool := s.pool.String()
		s.mu.Unlock()

		sess.sendToClient(proto.TypeJoined, proto.Joined{Network: j.Network, Address: sess.address, Pool: pool})
		sess.mu.Lock()
		sess.seq++
		seq := sess.seq
		sess.mu.Unlock()
		sess.sendToClient(proto.TypeNetmap, proto.Netmap{
			Network: j.Network, Isolation: s.isolation(), Seq: seq, Self: self, Peers: peers, Policy: policy,
		})
		for _, other := range notify {
			other.sendDelta(proto.NetmapDelta{Network: j.Network, Upsert: []proto.PeerInfo{self}})
		}
		return nil

	case proto.TypeOffer, proto.TypeAnswer, proto.TypeCandidate:
		var sig proto.Signal
		_ = env.Decode(&sig)
		sig.From = sess.peerID
		if sig.Network == "" {
			sig.Network = sess.network
		}
		s.mu.Lock()
		target := s.sessions[sig.To]
		if target != nil && !s.visibleLocked(sess, target) {
			target = nil
		}
		s.mu.Unlock()
		if target == nil {
			sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeNotFound, Message: "peer not found: " + sig.To})
			return nil
		}
		target.sendToClient(env.Type, sig)
		return nil

	case proto.TypeHeartbeat:
		sess.sendToClient(proto.TypeHeartbeat, proto.Heartbeat{})
		return nil
	}
	return nil
}

func (sess *fakeSession) close() {
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return
	}
	sess.closed = true
	close(sess.toClient)
	sess.mu.Unlock()

	if sess.peerID == "" {
		return
	}
	s := sess.srv
	s.mu.Lock()
	if s.sessions[sess.peerID] == sess {
		delete(s.sessions, sess.peerID)
	}
	var notify []*fakeSession
	if members := s.networks[sess.network]; members != nil && members[sess.peerID] == sess {
		delete(members, sess.peerID)
		for _, other := range members {
			if s.visibleLocked(sess, other) {
				notify = append(notify, other)
			}
		}
	}
	network := sess.network
	s.mu.Unlock()
	for _, other := range notify {
		other.sendDelta(proto.NetmapDelta{Network: network, Remove: []string{sess.peerID}})
	}
}
