package signal

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// FakeServer is an in-memory signalling server for tests. It implements the
// same v1 protocol as ghost-server: it authenticates any non-empty device
// token, assigns addresses from a pool, relays offer/answer/candidate between
// peers in a network, and announces peer-online/offline. It is safe for
// concurrent use.
//
// Obtain a Dialer with (*FakeServer).Dialer and set it on signal.Config.Dialer
// (or ghost.Config.SignalDialer) to run a Node/Hub entirely in memory.
type FakeServer struct {
	mu       sync.Mutex
	pool     netip.Prefix
	nextHost uint32
	tokens   map[string]bool // allowed device tokens; nil means allow any
	sessions map[string]*fakeSession
	networks map[string]map[string]*fakeSession // network -> deviceID -> session
	seq      int

	// AuthFunc, if set, decides whether a hello is accepted. It returns a
	// device id to use (empty to derive one) and an error to reject.
	AuthFunc func(h proto.Hello) (deviceID string, err error)
}

type fakeSession struct {
	deviceID string
	role     proto.Role
	pubKey   string
	network  string
	address  string
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
	}
}

// AllowToken restricts accepted device tokens. If never called, any non-empty
// token is accepted.
func (s *FakeServer) AllowToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]bool)
	}
	s.tokens[token] = true
}

// Dialer returns a Dialer that opens in-memory connections to this server.
func (s *FakeServer) Dialer() Dialer { return fakeDialer{s: s} }

func (s *FakeServer) assignAddress() string {
	addr := s.pool.Addr()
	// advance nextHost slots from the network base
	v := addr.As4()
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
	closed := sess.closed
	sess.mu.Unlock()
	if closed {
		return
	}
	select {
	case sess.toClient <- env:
	default:
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
		deviceID := h.DeviceID
		if s.AuthFunc != nil {
			id, err := s.AuthFunc(h)
			if err != nil {
				sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnauthorized, Message: err.Error(), Fatal: true})
				return nil
			}
			if id != "" {
				deviceID = id
			}
		} else {
			s.mu.Lock()
			tokenOK := h.DeviceToken != "" && (s.tokens == nil || s.tokens[h.DeviceToken])
			s.mu.Unlock()
			if !tokenOK {
				sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeUnauthorized, Message: "invalid device token", Fatal: true})
				return nil
			}
		}
		s.mu.Lock()
		s.seq++
		if deviceID == "" {
			deviceID = fmt.Sprintf("dev-%d", s.seq)
		}
		sess.deviceID = deviceID
		sess.role = h.Role
		sess.pubKey = h.PublicKey
		s.sessions[deviceID] = sess
		s.mu.Unlock()
		sess.sendToClient(proto.TypeWelcome, proto.Welcome{
			Version:           proto.Version,
			DeviceID:          deviceID,
			SessionID:         fmt.Sprintf("sess-%d", s.seq),
			HeartbeatInterval: 20,
		})
		return nil

	case proto.TypeJoinNetwork:
		var j proto.JoinNetwork
		_ = env.Decode(&j)
		s.mu.Lock()
		sess.network = j.Network
		if sess.role == "" {
			sess.role = j.Role
		}
		sess.address = s.assignAddress()
		members := s.networks[j.Network]
		if members == nil {
			members = make(map[string]*fakeSession)
			s.networks[j.Network] = members
		}
		// Build peer list and locate a hub.
		var peers []proto.PeerInfo
		var hub *proto.PeerInfo
		for _, other := range members {
			pi := proto.PeerInfo{DeviceID: other.deviceID, PublicKey: other.pubKey, Address: other.address, Role: other.role}
			peers = append(peers, pi)
			if other.role == proto.RoleHub {
				h := pi
				hub = &h
			}
		}
		members[sess.deviceID] = sess
		selfInfo := proto.PeerInfo{DeviceID: sess.deviceID, PublicKey: sess.pubKey, Address: sess.address, Role: sess.role}
		notify := make([]*fakeSession, 0, len(members))
		for id, other := range members {
			if id != sess.deviceID {
				notify = append(notify, other)
			}
		}
		pool := s.pool.String()
		s.mu.Unlock()

		sess.sendToClient(proto.TypeJoined, proto.Joined{
			Network: j.Network,
			Address: sess.address,
			Pool:    pool,
			Hub:     hub,
			Peers:   peers,
		})
		// Announce this peer to existing members.
		for _, other := range notify {
			other.sendToClient(proto.TypePeerOnline, proto.PeerEvent{Network: j.Network, Peer: selfInfo})
		}
		return nil

	case proto.TypeOffer, proto.TypeAnswer, proto.TypeCandidate:
		var sig proto.Signal
		_ = env.Decode(&sig)
		sig.From = sess.deviceID
		if sig.Network == "" {
			sig.Network = sess.network
		}
		s.mu.Lock()
		target := s.sessions[sig.To]
		s.mu.Unlock()
		if target == nil {
			sess.sendToClient(proto.TypeError, proto.Error{Code: proto.ErrCodeNotFound, Message: "peer not found: " + sig.To})
			return nil
		}
		target.sendToClient(env.Type, sig)
		return nil

	case proto.TypeHeartbeat:
		// Echo a heartbeat back.
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

	if sess.deviceID == "" {
		return
	}
	s := sess.srv
	s.mu.Lock()
	delete(s.sessions, sess.deviceID)
	var notify []*fakeSession
	if members := s.networks[sess.network]; members != nil {
		delete(members, sess.deviceID)
		for _, other := range members {
			notify = append(notify, other)
		}
	}
	info := proto.PeerInfo{DeviceID: sess.deviceID, PublicKey: sess.pubKey, Address: sess.address, Role: sess.role}
	network := sess.network
	s.mu.Unlock()
	for _, other := range notify {
		other.sendToClient(proto.TypePeerOffline, proto.PeerEvent{Network: network, Peer: info})
	}
}
