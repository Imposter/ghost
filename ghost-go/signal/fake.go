package signal

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// FakeServer is an in-memory signalling server for tests. It implements the
// v1 protocol with a permissive policy: it authenticates any non-empty peer
// token, takes a peer's roles from its hello (default node), assigns
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
	tokens   map[string]bool // allowed peer tokens; nil means allow any
	sessions map[string]*fakeSession
	networks map[string]map[string]*fakeSession // network -> peerID -> session
	policies map[string]*proto.ExitPolicy
	seq      int

	// AuthFunc, if set, decides whether a hello is accepted. It returns a
	// peer id to use (empty to derive one) and an error to reject.
	AuthFunc func(h proto.Hello) (peerID string, err error)
	// HubOnly applies hub-only isolation. Set it before any peer connects.
	HubOnly bool
}

func (s *FakeServer) isolation() proto.Isolation {
	if s.HubOnly {
		return proto.IsolationHubOnly
	}
	return proto.IsolationNone
}

// visibleLocked reports whether a and b may see each other. Caller holds s.mu.
func (s *FakeServer) visibleLocked(a, b *fakeSession) bool {
	return !s.HubOnly || proto.HasRole(a.roles, proto.RoleHub) || proto.HasRole(b.roles, proto.RoleHub)
}

type fakeSession struct {
	peerID   string
	roles    []proto.Role
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
		PeerID: sess.peerID, PublicKey: sess.pubKey, Address: sess.address,
		Roles: slices.Clone(sess.roles), Online: true,
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
		s.seq++
		if peerID == "" {
			peerID = fmt.Sprintf("peer-%d", s.seq)
		}
		sess.peerID = peerID
		sess.roles = h.Roles
		if len(sess.roles) == 0 {
			sess.roles = []proto.Role{proto.RoleNode}
		}
		sess.pubKey = h.PublicKey
		s.sessions[peerID] = sess
		s.mu.Unlock()
		sess.sendToClient(proto.TypeWelcome, proto.Welcome{
			Version:           proto.Version,
			PeerID:            peerID,
			SessionID:         fmt.Sprintf("sess-%d", s.seq),
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
