// Package direct runs ghost members peer to peer, with no control plane. Its
// Signaller plugs into ghost.Config.Signaller and supports two kinds of peer:
//
//   - Token peers. One side calls CreateInvite, the other AcceptInvite (which
//     returns an answer), and the first side calls AcceptAnswer. The tokens
//     carry only public material (WireGuard public key, tunnel address, ICE
//     credentials and candidates) and can travel over any channel: copy and
//     paste, a QR code, or an Exchanger of your own. The peers then connect
//     with ICE and WireGuard directly. STUN and TURN are optional
//     (ghost.Config.STUNServers, TURNServers).
//   - Static peers (Config.Static): plain WireGuard over UDP to a known
//     endpoint, without ICE.
//
// Each token pair sets up one link, once: if the link fails or is torn down,
// swap new tokens. Apply the answer within ghost.Config.ConnectTimeout of
// accepting the invite, since the invited side starts ICE straight away.
package direct

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// DefaultNetwork is the network name used when none is configured.
const DefaultNetwork = "direct"

// DefaultTokenTTL is how long a created token stays valid by default.
const DefaultTokenTTL = 10 * time.Minute

// Errors returned by the Signaller.
var (
	ErrClosed        = errors.New("direct: signaller closed")
	ErrAddressInUse  = errors.New("direct: tunnel address already in use")
	ErrKeyInUse      = errors.New("direct: public key already in use")
	ErrOwnToken      = errors.New("direct: token carries this member's own key")
	ErrUnknownInvite = errors.New("direct: answer matches no pending invite")
	ErrSessionUsed   = errors.New("direct: session already used; exchange new tokens")
)

// Config configures a Signaller.
type Config struct {
	// Address is this member's tunnel address (CIDR, a single host, e.g.
	// "100.64.0.1/32"). Required. Every peer needs a distinct address.
	Address string
	// Network names the network in ghost.Status (default: the member's
	// Config.Network, or DefaultNetwork).
	Network string
	// Static lists peers reached with plain WireGuard, without ICE.
	Static []StaticPeer
	// TokenTTL is how long created tokens stay valid (default
	// DefaultTokenTTL; negative: no expiry).
	TokenTTL time.Duration
}

// Signaller is a ghost.Signaller that needs no server. Create it with New,
// set it as ghost.Config.Signaller, and start the member before exchanging
// tokens. It is safe for concurrent use.
type Signaller struct {
	cfg  Config
	addr netip.Prefix

	events chan signal.Event
	ready  chan struct{} // closed once the first netmap is queued
	done   chan struct{} // closed by Close

	mu       sync.Mutex
	self     ghost.SignalSelf
	selfID   string
	state    signal.State
	joined   bool
	closed   bool
	network  string
	seq      int64
	peers    map[string]proto.PeerInfo
	static   map[string]StaticPeer
	sessions map[string]*session
}

// session is one token exchange.
type session struct {
	inviter  bool
	answered bool // inviter: the answer was applied
	linked   bool // the member has started its link

	ufrag, pwd string
	cands      []proto.Candidate
	creds      chan struct{} // closed once ufrag/pwd are known
	gathered   chan struct{} // closed at the end of candidates
	credsOnce  sync.Once
	gatherOnce sync.Once
}

var _ ghost.Signaller = (*Signaller)(nil)

// New validates cfg and returns a Signaller.
func New(cfg Config) (*Signaller, error) {
	addr, err := hostPrefix(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("direct: %w", err)
	}
	s := &Signaller{
		cfg:      cfg,
		addr:     addr,
		events:   make(chan signal.Event, 256),
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
		state:    signal.StateDisconnected,
		peers:    make(map[string]proto.PeerInfo),
		static:   make(map[string]StaticPeer),
		sessions: make(map[string]*session),
	}
	for _, p := range cfg.Static {
		if err := validPublicKey(p.PublicKey); err != nil {
			return nil, fmt.Errorf("direct: static peer: %w", err)
		}
		if _, err := hostPrefix(p.Address); err != nil {
			return nil, fmt.Errorf("direct: static peer: %w", err)
		}
		if p.Endpoint == "" && p.ListenAddr == "" {
			return nil, fmt.Errorf("direct: static peer %s: set Endpoint, ListenAddr or both", p.Address)
		}
		id := "wg-" + fingerprint(p.PublicKey)
		if p.Name != "" {
			if !validID(p.Name) {
				return nil, fmt.Errorf("direct: static peer name %q", p.Name)
			}
			id = "static-" + p.Name
		}
		info := proto.PeerInfo{PeerID: id, Name: p.Name, PublicKey: p.PublicKey, Address: p.Address,
			Roles: []proto.Role{proto.RoleNode}, Online: true}
		if p.Endpoint != "" {
			info.Endpoints = []string{p.Endpoint}
		}
		if _, dup := s.peers[id]; dup {
			return nil, fmt.Errorf("direct: duplicate static peer %s", id)
		}
		if err := s.checkPeerLocked(p.PublicKey, p.Address, ""); err != nil {
			return nil, fmt.Errorf("direct: static peer %s: %w", id, err)
		}
		s.peers[id] = info
		s.static[id] = p
	}
	return s, nil
}

// ID returns this member's peer id (valid after Start).
func (s *Signaller) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selfID
}

// Start implements ghost.Signaller.
func (s *Signaller) Start(_ context.Context, self ghost.SignalSelf) error {
	if err := validPublicKey(self.PublicKey); err != nil {
		return fmt.Errorf("direct: %w", err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.selfID != "" {
		s.mu.Unlock()
		return errors.New("direct: already started")
	}
	for id, p := range s.static {
		if p.PublicKey == self.PublicKey {
			s.mu.Unlock()
			return fmt.Errorf("direct: static peer %s: %w", id, ErrOwnToken)
		}
	}
	s.self = self
	s.selfID = "p2p-" + fingerprint(self.PublicKey)
	s.state = signal.StateConnected
	id := s.selfID
	s.mu.Unlock()
	// The channel is new and far larger than these two events.
	s.events <- signal.Event{State: signal.StateConnected}
	s.events <- signal.Event{Type: proto.TypeWelcome, Welcome: &proto.Welcome{Version: proto.Version, PeerID: id}}
	return nil
}

// Events implements ghost.Signaller.
func (s *Signaller) Events() <-chan signal.Event { return s.events }

// State implements ghost.Signaller.
func (s *Signaller) State() signal.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Join implements ghost.Signaller: it queues the Joined event and the netmap
// (static peers plus any token peers).
func (s *Signaller) Join(_ context.Context, network string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.joined {
		s.mu.Unlock()
		return nil
	}
	s.joined = true
	s.network = s.cfg.Network
	if s.network == "" {
		s.network = network
	}
	if s.network == "" {
		s.network = DefaultNetwork
	}
	joined := proto.Joined{Network: s.network, Address: s.cfg.Address}
	nm := s.netmapLocked()
	s.mu.Unlock()
	// Join runs on the member's event loop, which drains the channel, so
	// queue from another goroutine.
	go func() {
		for _, ev := range []signal.Event{
			{Type: proto.TypeJoined, Joined: &joined},
			{Type: proto.TypeNetmap, Netmap: &nm},
		} {
			select {
			case s.events <- ev:
			case <-s.done:
				return
			}
		}
		close(s.ready)
	}()
	return nil
}

func (s *Signaller) netmapLocked() proto.Netmap {
	s.seq++
	roles := slices.Clone(s.self.Roles)
	if len(roles) == 0 {
		roles = []proto.Role{proto.RoleNode}
	}
	nm := proto.Netmap{
		Network:   s.network,
		Isolation: proto.IsolationNone,
		Seq:       s.seq,
		Self:      proto.PeerInfo{PeerID: s.selfID, PublicKey: s.self.PublicKey, Address: s.cfg.Address, Roles: roles, Online: true},
		Peers:     make([]proto.PeerInfo, 0, len(s.peers)),
	}
	for _, p := range s.peers {
		nm.Peers = append(nm.Peers, p)
	}
	sort.Slice(nm.Peers, func(i, j int) bool { return nm.Peers[i].PeerID < nm.Peers[j].PeerID })
	return nm
}

// ICEServers implements ghost.Signaller. STUN and TURN come from the
// member's own Config.
func (s *Signaller) ICEServers() []proto.ICEServer { return nil }

// Link implements ghost.Signaller. A static peer gets a UDP connection; a
// token peer gets ICE, with the inviting side controlling. A token session
// links once: asking again drops the peer.
func (s *Signaller) Link(_, peer proto.PeerInfo) (ghost.LinkPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.static[peer.PeerID]; ok {
		c, err := dialStatic(p)
		if err != nil {
			return ghost.LinkPlan{}, err
		}
		return ghost.LinkPlan{Conn: c}, nil
	}
	sess, ok := s.sessions[peer.PeerID]
	if !ok {
		return ghost.LinkPlan{}, fmt.Errorf("direct: unknown peer %s", peer.PeerID)
	}
	if sess.linked {
		s.dropLocked(peer.PeerID)
		return ghost.LinkPlan{}, ErrSessionUsed
	}
	sess.linked = true
	return ghost.LinkPlan{Controlling: sess.inviter}, nil
}

// Send implements ghost.Signaller: it records this member's ICE description
// for the token being built.
func (s *Signaller) Send(_ context.Context, t proto.Type, sig proto.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sig.To]
	if !ok {
		return nil
	}
	switch t {
	case proto.TypeOffer, proto.TypeAnswer:
		if sig.Ufrag != "" && sess.ufrag == "" {
			sess.ufrag, sess.pwd = sig.Ufrag, sig.Pwd
			sess.credsOnce.Do(func() { close(sess.creds) })
		}
	case proto.TypeCandidate:
		if sig.Candidate == nil {
			sess.gatherOnce.Do(func() { close(sess.gathered) })
		} else if len(sess.cands) < MaxCandidates {
			sess.cands = append(sess.cands, *sig.Candidate)
		}
	}
	return nil
}

// ReportHealth implements ghost.Signaller (no-op).
func (s *Signaller) ReportHealth(context.Context) error { return nil }

// Close implements ghost.Signaller.
func (s *Signaller) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.state = signal.StateClosed
		close(s.done)
	}
	return nil
}

// CreateInvite starts a token exchange and returns the invite for the other
// peer. The member must be started. It returns once this member's ICE
// candidates are gathered.
func (s *Signaller) CreateInvite(ctx context.Context) (string, error) {
	if err := s.waitReady(ctx); err != nil {
		return "", err
	}
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	sess := newSession(true)
	s.mu.Lock()
	s.sessions[id] = sess
	s.peers[id] = proto.PeerInfo{PeerID: id, Roles: []proto.Role{proto.RoleNode}, Online: true}
	ev := s.upsertLocked(id)
	s.mu.Unlock()
	if err := s.emit(ctx, ev); err != nil {
		s.drop(id)
		return "", err
	}
	tok, err := s.localToken(ctx, id, sess, KindInvite)
	if err != nil {
		s.drop(id)
		return "", err
	}
	return tok, nil
}

// AcceptInvite applies an invite from another peer, starts connecting to it,
// and returns the answer to send back.
func (s *Signaller) AcceptInvite(ctx context.Context, invite string) (string, error) {
	if err := s.waitReady(ctx); err != nil {
		return "", err
	}
	tok, err := ParseToken(invite)
	if err != nil {
		return "", err
	}
	if tok.Kind != KindInvite {
		return "", fmt.Errorf("%w: expected an invite, got %s", ErrTokenInvalid, tok.Kind)
	}
	id := tok.Session
	sess := newSession(false)
	s.mu.Lock()
	if _, ok := s.peers[id]; ok {
		s.mu.Unlock()
		return "", ErrSessionUsed
	}
	if err := s.checkPeerLocked(tok.PublicKey, tok.Address, ""); err != nil {
		s.mu.Unlock()
		return "", err
	}
	s.sessions[id] = sess
	s.peers[id] = proto.PeerInfo{PeerID: id, PublicKey: tok.PublicKey, Address: tok.Address,
		Roles: []proto.Role{proto.RoleNode}, Online: true}
	evs := append([]signal.Event{s.upsertLocked(id)}, s.remoteEvents(proto.TypeOffer, tok)...)
	s.mu.Unlock()
	if err := s.emit(ctx, evs...); err != nil {
		s.drop(id)
		return "", err
	}
	answer, err := s.localToken(ctx, id, sess, KindAnswer)
	if err != nil {
		s.drop(id)
		return "", err
	}
	return answer, nil
}

// AcceptAnswer applies the answer to an invite this member created; the two
// peers then connect.
func (s *Signaller) AcceptAnswer(ctx context.Context, answer string) error {
	tok, err := ParseToken(answer)
	if err != nil {
		return err
	}
	if tok.Kind != KindAnswer {
		return fmt.Errorf("%w: expected an answer, got %s", ErrTokenInvalid, tok.Kind)
	}
	id := tok.Session
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if !ok || !sess.inviter {
		s.mu.Unlock()
		return ErrUnknownInvite
	}
	if sess.answered {
		s.mu.Unlock()
		return ErrSessionUsed
	}
	if err := s.checkPeerLocked(tok.PublicKey, tok.Address, id); err != nil {
		s.mu.Unlock()
		return err
	}
	sess.answered = true
	p := s.peers[id]
	p.PublicKey, p.Address = tok.PublicKey, tok.Address
	s.peers[id] = p
	evs := append([]signal.Event{s.upsertLocked(id)}, s.remoteEvents(proto.TypeAnswer, tok)...)
	s.mu.Unlock()
	return s.emit(ctx, evs...)
}

// checkPeerLocked refuses a peer whose key or address clashes with this
// member or another peer (except the peer with id except).
func (s *Signaller) checkPeerLocked(key, address, except string) error {
	a, err := hostPrefix(address)
	if err != nil {
		return err
	}
	if key == s.self.PublicKey {
		return ErrOwnToken
	}
	if a.Addr() == s.addr.Addr() {
		return fmt.Errorf("%w: %s is this member's address", ErrAddressInUse, a.Addr())
	}
	for id, p := range s.peers {
		if id == except {
			continue
		}
		if p.PublicKey != "" && p.PublicKey == key {
			return fmt.Errorf("%w by peer %s", ErrKeyInUse, id)
		}
		if pa, err := netip.ParsePrefix(p.Address); err == nil && pa.Addr() == a.Addr() {
			return fmt.Errorf("%w: %s by peer %s", ErrAddressInUse, a.Addr(), id)
		}
	}
	return nil
}

// remoteEvents turns a remote token into the signal events a member expects:
// the offer or answer, then each candidate. Caller holds s.mu.
func (s *Signaller) remoteEvents(t proto.Type, tok Token) []signal.Event {
	evs := []signal.Event{{Type: t, Signal: &proto.Signal{Network: s.network, From: tok.Session, To: s.selfID, Ufrag: tok.Ufrag, Pwd: tok.Pwd}}}
	for i := range tok.Candidates {
		c := tok.Candidates[i]
		evs = append(evs, signal.Event{Type: proto.TypeCandidate,
			Signal: &proto.Signal{Network: s.network, From: tok.Session, To: s.selfID, Candidate: &c}})
	}
	return evs
}

// localToken waits for this member's ICE description for session id and
// encodes it as a token.
func (s *Signaller) localToken(ctx context.Context, id string, sess *session, kind Kind) (string, error) {
	for _, ch := range []chan struct{}{sess.creds, sess.gathered} {
		select {
		case <-ch:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.done:
			return "", ErrClosed
		}
	}
	s.mu.Lock()
	tok := Token{
		V:          TokenVersion,
		Kind:       kind,
		Session:    id,
		PublicKey:  s.self.PublicKey,
		Address:    s.cfg.Address,
		AllowedIPs: []string{netip.PrefixFrom(s.addr.Addr(), s.addr.Addr().BitLen()).String()},
		Ufrag:      sess.ufrag,
		Pwd:        sess.pwd,
		Candidates: slices.Clone(sess.cands),
	}
	s.mu.Unlock()
	ttl := s.cfg.TokenTTL
	if ttl == 0 {
		ttl = DefaultTokenTTL
	}
	if ttl > 0 {
		tok.Expires = time.Now().Add(ttl).Unix()
	}
	return tok.Encode()
}

// upsertLocked returns a netmap delta carrying peer id. Caller holds s.mu.
func (s *Signaller) upsertLocked(id string) signal.Event {
	s.seq++
	return signal.Event{Type: proto.TypeNetmapDelta, Delta: &proto.NetmapDelta{Network: s.network, Seq: s.seq, Upsert: []proto.PeerInfo{s.peers[id]}}}
}

// drop forgets a token peer and removes it from the member's netmap.
func (s *Signaller) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLocked(id)
}

func (s *Signaller) dropLocked(id string) {
	if _, ok := s.sessions[id]; !ok {
		return
	}
	delete(s.sessions, id)
	delete(s.peers, id)
	s.seq++
	ev := signal.Event{Type: proto.TypeNetmapDelta, Delta: &proto.NetmapDelta{Network: s.network, Seq: s.seq, Remove: []string{id}}}
	// May run on the member's event loop: queue without blocking it.
	go func() { _ = s.emit(context.Background(), ev) }()
}

func (s *Signaller) emit(ctx context.Context, evs ...signal.Event) error {
	for _, ev := range evs {
		select {
		case s.events <- ev:
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return ErrClosed
		}
	}
	return nil
}

func (s *Signaller) waitReady(ctx context.Context) error {
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("direct: member not joined: %w", ctx.Err())
	case <-s.done:
		return ErrClosed
	}
}

func newSession(inviter bool) *session {
	return &session{inviter: inviter, creds: make(chan struct{}), gathered: make(chan struct{})}
}

func newSessionID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// fingerprint is a short, stable id derived from a public key.
func fingerprint(publicKey string) string {
	sum := sha256.Sum256([]byte(publicKey))
	return hex.EncodeToString(sum[:6])
}
