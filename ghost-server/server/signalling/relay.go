// Package signalling is ghost-server's WebSocket signalling endpoint. It
// speaks the ghost-go signal/proto v1 protocol: it authenticates a peer's
// hello, assigns its address on join, sends each joined peer its netmap (the
// peers the network's isolation mode and ACLs let it reach) and live deltas,
// relays offer/answer/candidate only between pairs the netmap allows, records
// the health summaries carried by heartbeats, and disconnects revoked,
// expired or moved peers.
package signalling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/policy"
	"github.com/Imposter/ghost/ghost-server/server/store"
	"github.com/Imposter/ghost/ghost-server/server/telemetry"
	"github.com/Imposter/ghost/ghost-server/server/turn"
)

// Options configures a Relay.
type Options struct {
	Service *control.Service
	// STUNURLs and TURNURLs are advertised in the welcome message. TURN
	// credentials are minted per session by TURN (nil disables TURN).
	STUNURLs []string
	TURNURLs []string
	TURN     *turn.Issuer
	// HeartbeatInterval is advertised to clients; a session silent for
	// HeartbeatTimeout is closed.
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	// HelloTimeout bounds the wait for the first frame (default 10s).
	HelloTimeout time.Duration
	Logger       *slog.Logger
	Metrics      *telemetry.Metrics
}

// Relay is the signalling endpoint and live-session registry. It implements
// control.Sessions.
type Relay struct {
	svc      *control.Service
	stunURLs []string
	turnURLs []string
	turn     *turn.Issuer
	hbEvery  time.Duration
	hbDead   time.Duration
	helloTTL time.Duration
	log      *slog.Logger
	metrics  *telemetry.Metrics

	seq atomic.Uint64

	// netmapMu serialises netmap computation so every session sees its
	// deltas in order.
	netmapMu sync.Mutex

	mu       sync.RWMutex
	sessions map[string]*session // peer id -> session
	closed   bool
}

var _ control.Sessions = (*Relay)(nil)

// New returns a Relay and attaches it to the service.
func New(opts Options) *Relay {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 20 * time.Second
	}
	if opts.HeartbeatTimeout <= opts.HeartbeatInterval {
		opts.HeartbeatTimeout = 3 * opts.HeartbeatInterval
	}
	if opts.HelloTimeout <= 0 {
		opts.HelloTimeout = 10 * time.Second
	}
	r := &Relay{
		svc:      opts.Service,
		stunURLs: opts.STUNURLs,
		turnURLs: opts.TURNURLs,
		turn:     opts.TURN,
		hbEvery:  opts.HeartbeatInterval,
		hbDead:   opts.HeartbeatTimeout,
		helloTTL: opts.HelloTimeout,
		log:      opts.Logger,
		metrics:  opts.Metrics,
		sessions: map[string]*session{},
	}
	opts.Service.SetSessions(r)
	return r
}

// Presence describes one live session.
type Presence struct {
	PeerID      string       `json:"peer_id"`
	SessionID   string       `json:"session_id"`
	Name        string       `json:"name,omitempty"`
	Roles       []proto.Role `json:"roles"`
	Network     string       `json:"network,omitempty"`
	Address     string       `json:"address,omitempty"`
	Joined      bool         `json:"joined"`
	Remote      string       `json:"remote"`
	ConnectedAt time.Time    `json:"connected_at"`
	LastSeen    time.Time    `json:"last_seen"`
	// NetmapPeers is how many peers this session's netmap lists.
	NetmapPeers int `json:"netmap_peers"`
	// PolicyRevision is the revision of the policy in its last netmap.
	PolicyRevision int64 `json:"policy_revision"`
}

// Presence lists live sessions, optionally limited to one network, ordered by
// connection time.
func (r *Relay) Presence(network string) []Presence {
	r.mu.RLock()
	out := make([]Presence, 0, len(r.sessions))
	for _, s := range r.sessions {
		if network != "" && s.peer.Network != network {
			continue
		}
		out = append(out, s.presenceLocked())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.Before(out[j].ConnectedAt) })
	return out
}

// Session returns one peer's presence, if it has a live session.
func (r *Relay) Session(peerID string) (Presence, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[peerID]
	if !ok {
		return Presence{}, false
	}
	return s.presenceLocked(), true
}

// Online implements control.Sessions.
func (r *Relay) Online(peerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[peerID]
	return ok && s.joined
}

// Disconnect implements control.Sessions.
func (r *Relay) Disconnect(peerID, code, message string) bool {
	r.mu.RLock()
	s := r.sessions[peerID]
	r.mu.RUnlock()
	if s == nil {
		return false
	}
	s.fatal(code, message)
	return true
}

// PolicyChanged implements control.Sessions: it re-asks the authorizer,
// uncached, whether each live session in the network may stay connected and
// which exit policy it contributes, disconnects the ones it now denies, and
// pushes the new netmaps.
func (r *Relay) PolicyChanged(ctx context.Context, network string) {
	for _, s := range r.joinedIn(network) {
		r.mu.RLock()
		peer := s.peer
		r.mu.RUnlock()
		d := r.svc.Access().CheckFresh(ctx, connectRequest(peer))
		if !d.Allow {
			r.svc.Audit(ctx, network, "peer.connect_denied", peer.ID, map[string]any{"reason": d.Reason, "on": "policy_change"})
			s.fatal(proto.ErrCodeForbidden, "connection no longer authorized: "+d.Reason)
			continue
		}
		r.mu.Lock()
		s.override = d.Policy.ExitPolicy(network)
		r.mu.Unlock()
	}
	r.NetworkChanged(ctx, network)
}

// NetworkChanged implements control.Sessions: every joined session in the
// network gets a delta against its last netmap (or its first snapshot).
func (r *Relay) NetworkChanged(ctx context.Context, network string) {
	r.netmapMu.Lock()
	defer r.netmapMu.Unlock()

	sessions := r.joinedIn(network)
	if len(sessions) == 0 {
		return
	}
	n, err := r.svc.Network(ctx, network)
	if err != nil {
		r.log.Warn("signalling: netmap: network", "network", network, "error", err)
		return
	}
	peers, err := r.svc.Store().ListPeers(ctx, store.PeerFilter{Network: network})
	if err != nil {
		r.log.Warn("signalling: netmap: peers", "network", network, "error", err)
		return
	}
	byID := make(map[string]store.Peer, len(peers))
	for _, p := range peers {
		byID[p.ID] = p
	}
	online := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		online[s.peerID] = true
	}
	now := r.svc.Now()
	for _, s := range sessions {
		fresh, ok := byID[s.peerID]
		if !ok || fresh.Status(now) != store.PeerActive {
			continue // revoked, expired or deleted: its disconnect is in flight
		}
		r.mu.Lock()
		s.peer = fresh
		override := s.override
		prev := s.netmap
		r.mu.Unlock()

		nm := buildNetmap(n, fresh, peers, online, override, now)
		if prev == nil {
			s.seq++
			nm.Seq = s.seq
			s.send(proto.TypeNetmap, nm)
		} else if d, changed := diffNetmap(*prev, nm); changed {
			s.seq++
			d.Seq, nm.Seq = s.seq, s.seq
			s.send(proto.TypeNetmapDelta, d)
		} else {
			nm.Seq = prev.Seq
		}
		r.mu.Lock()
		s.netmap = &nm
		r.mu.Unlock()
	}
	r.svc.Bus().Publish(events.Event{Type: events.NetmapUpdated, Network: network, Data: map[string]any{
		"revision": n.PolicyRevision, "sessions": len(sessions),
	}})
}

// joinedIn returns the joined sessions of a network.
func (r *Relay) joinedIn(network string) []*session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*session
	for _, s := range r.sessions {
		if s.joined && s.peer.Network == network {
			out = append(out, s)
		}
	}
	return out
}

// Close disconnects every session.
func (r *Relay) Close() {
	r.mu.Lock()
	r.closed = true
	all := make([]*session, 0, len(r.sessions))
	for _, s := range r.sessions {
		all = append(all, s)
	}
	r.mu.Unlock()
	for _, s := range all {
		s.closeNow()
	}
}

// buildNetmap computes self's netmap: the active, addressed peers that the
// isolation mode and ACLs let self reach or be reached by, its packet filter,
// and its exit policy (the network's, with the authorizer's override).
func buildNetmap(n store.Network, self store.Peer, peers []store.Peer, online map[string]bool,
	override *proto.ExitPolicy, now time.Time) proto.Netmap {

	selfSubj := self.Subject()
	infos := []proto.PeerInfo{}
	var visible []store.Peer
	for _, p := range peers {
		if p.ID == self.ID || p.Address == "" || p.PublicKey == "" || p.Status(now) != store.PeerActive {
			continue
		}
		if !n.Policy.Visible(n.Isolation, selfSubj, p.Subject()) {
			continue
		}
		visible = append(visible, p)
		infos = append(infos, peerInfo(p, online[p.ID]))
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].PeerID < infos[j].PeerID })

	subjects := make([]policy.Subject, 0, len(visible))
	for _, p := range visible {
		subjects = append(subjects, p.Subject())
	}
	filter := n.Policy.Filter(n.Isolation, selfSubj, subjects)
	exit := n.Policy.ExitPolicy(n.Name, n.PolicyRevision, selfSubj)
	if override != nil {
		exit.Allow = slices.Clone(override.Allow)
		exit.DailyBytes = override.DailyBytes
		exit.BytesPerSecond = override.BytesPerSecond
		exit.Paused = exit.Paused || override.Paused
		if override.Labels != nil {
			exit.Labels = override.Labels
		}
	}
	if exit.Allow == nil {
		exit.Allow = []string{}
	}
	return proto.Netmap{
		Network:   n.Name,
		Isolation: proto.Isolation(n.Isolation),
		Self:      peerInfo(self, true),
		Peers:     infos,
		Policy:    &exit,
		Filter:    &filter,
	}
}

func peerInfo(p store.Peer, online bool) proto.PeerInfo {
	return proto.PeerInfo{
		PeerID: p.ID, Name: p.Name, PublicKey: p.PublicKey, Address: p.Address,
		Roles: slices.Clone(p.Roles), Tags: slices.Clone(p.Tags), Endpoints: slices.Clone(p.Endpoints), Online: online,
	}
}

// diffNetmap returns the delta from prev to next and whether anything changed.
func diffNetmap(prev, next proto.Netmap) (proto.NetmapDelta, bool) {
	d := proto.NetmapDelta{Network: next.Network}
	changed := false
	if !reflect.DeepEqual(prev.Self, next.Self) {
		self := next.Self
		d.Self, changed = &self, true
	}
	old := make(map[string]proto.PeerInfo, len(prev.Peers))
	for _, p := range prev.Peers {
		old[p.PeerID] = p
	}
	for _, p := range next.Peers {
		if o, ok := old[p.PeerID]; !ok || !reflect.DeepEqual(o, p) {
			d.Upsert = append(d.Upsert, p)
		}
		delete(old, p.PeerID)
	}
	for id := range old {
		d.Remove = append(d.Remove, id)
	}
	sort.Strings(d.Remove)
	if len(d.Upsert) > 0 || len(d.Remove) > 0 {
		changed = true
	}
	if !reflect.DeepEqual(prev.Policy, next.Policy) {
		d.Policy, changed = next.Policy, true
	}
	if !reflect.DeepEqual(prev.Filter, next.Filter) {
		d.Filter, changed = next.Filter, true
	}
	if prev.Isolation != next.Isolation {
		d.Isolation, changed = next.Isolation, true
	}
	return d, changed
}

func connectRequest(p store.Peer) access.Request {
	return access.Request{Action: access.ActionConnect, Network: p.Network, Peer: p.ID, Roles: p.Roles, Tags: p.Tags, Labels: p.Labels}
}

// ServeHTTP upgrades to WebSocket and runs one session.
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 10)
	ctx := req.Context()

	peer, err := r.handshake(ctx, ws)
	if err != nil {
		r.log.Debug("signalling: handshake failed", "remote", req.RemoteAddr, "error", err)
		_ = ws.Close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}
	ctx = control.WithActor(ctx, "peer:"+peer.ID)

	now := r.svc.Now()
	s := &session{
		relay:       r,
		ws:          ws,
		id:          "sess_" + strconv.FormatUint(r.seq.Add(1), 10),
		peerID:      peer.ID,
		peer:        peer,
		remote:      req.RemoteAddr,
		connectedAt: now,
		out:         make(chan outFrame, 256),
		done:        make(chan struct{}),
	}
	s.lastSeen.Store(now.UnixMilli())

	if !r.register(s) {
		_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	r.metrics.SessionOpened(ctx, primaryRole(peer.Roles))
	_ = r.svc.Store().TouchPeer(ctx, peer.ID, now.UTC())

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		s.writeLoop()
	}()
	s.send(proto.TypeWelcome, r.welcome(s))

	readErr := s.readLoop(ctx)
	s.shutdown()
	<-writerDone
	_ = ws.CloseNow()
	r.unregister(context.WithoutCancel(ctx), s)
	r.metrics.SessionClosed(context.Background(), primaryRole(peer.Roles))
	r.log.Debug("signalling: session closed", "peer", peer.ID, "session", s.id, "error", readErr)
}

func primaryRole(roles []proto.Role) string {
	best := proto.RoleNode
	for _, r := range roles {
		if r.Rank() > best.Rank() {
			best = r
		}
	}
	return string(best)
}

var errHandshake = errors.New("signalling: handshake rejected")

// handshake reads and authenticates the hello frame, replying with a fatal
// error frame on failure.
func (r *Relay) handshake(ctx context.Context, ws *websocket.Conn) (store.Peer, error) {
	rctx, cancel := context.WithTimeout(ctx, r.helloTTL)
	env, err := readEnvelope(rctx, ws)
	cancel()
	if err != nil {
		return store.Peer{}, err
	}
	r.metrics.Message(ctx, string(env.Type), "in")
	reject := func(code, msg string) (store.Peer, error) {
		writeFrame(ctx, ws, proto.TypeError, proto.Error{Code: code, Message: msg, Fatal: true})
		return store.Peer{}, fmt.Errorf("%w: %s", errHandshake, msg)
	}
	var hello proto.Hello
	if env.Type != proto.TypeHello || env.Decode(&hello) != nil {
		return reject(proto.ErrCodeBadRequest, "first frame must be hello")
	}
	if env.V != proto.Version || hello.Version != proto.Version {
		return reject(proto.ErrCodeUnsupportedVersion, fmt.Sprintf("server speaks v%d", proto.Version))
	}
	peer, err := r.svc.Authenticate(ctx, hello.PeerToken)
	if err != nil {
		return reject(control.DisconnectCodeFor(err), err.Error())
	}
	if hello.PeerID != "" && hello.PeerID != peer.ID {
		return reject(proto.ErrCodeUnauthorized, "peer id does not match token")
	}
	for _, want := range hello.Roles {
		if !proto.HasRole(peer.Roles, want) {
			return reject(proto.ErrCodeUnauthorized, fmt.Sprintf("peer does not hold the %s role", want))
		}
	}
	if hello.PublicKey != "" {
		actx := control.WithActor(ctx, "peer:"+peer.ID)
		if peer, err = r.svc.SetPublicKey(actx, peer, hello.PublicKey); err != nil {
			return reject(proto.ErrCodeBadRequest, err.Error())
		}
	}
	return peer, nil
}

func (r *Relay) welcome(s *session) proto.Welcome {
	w := proto.Welcome{
		Version:           proto.Version,
		PeerID:            s.peerID,
		SessionID:         s.id,
		HeartbeatInterval: int(r.hbEvery / time.Second),
	}
	if len(r.stunURLs) > 0 {
		w.ICEServers = append(w.ICEServers, proto.ICEServer{URLs: r.stunURLs})
	}
	if len(r.turnURLs) > 0 && r.turn != nil {
		c := r.turn.Issue(s.peerID, r.svc.Now())
		w.ICEServers = append(w.ICEServers, proto.ICEServer{URLs: r.turnURLs, Username: c.Username, Credential: c.Password})
	}
	return w
}

// register adds s, replacing (and closing) any older session of the peer.
func (r *Relay) register(s *session) bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	old := r.sessions[s.peerID]
	r.sessions[s.peerID] = s
	r.mu.Unlock()
	if old != nil {
		old.replaced.Store(true)
		old.closeNow()
	}
	return true
}

// unregister removes s (unless a newer session replaced it) and tells the
// network the peer went offline.
func (r *Relay) unregister(ctx context.Context, s *session) {
	r.mu.Lock()
	current := r.sessions[s.peerID] == s
	if current {
		delete(r.sessions, s.peerID)
	}
	peer, joined := s.peer, s.joined
	r.mu.Unlock()
	_ = r.svc.Store().TouchPeer(ctx, peer.ID, r.svc.Now().UTC())
	if !current || !joined {
		return
	}
	r.svc.Bus().Publish(events.Event{Type: events.PeerOffline, Network: peer.Network, PeerID: peer.ID})
	r.NetworkChanged(ctx, peer.Network)
}

// handleJoin authorizes a join, assigns the address, replies with joined and
// sends the first netmap; the rest of the network gets a delta.
func (r *Relay) handleJoin(ctx context.Context, s *session, j proto.JoinNetwork) {
	r.mu.RLock()
	peer, joined := s.peer, s.joined
	r.mu.RUnlock()
	if joined {
		s.sendError(proto.ErrCodeBadRequest, "already joined")
		return
	}
	if j.Network != peer.Network {
		s.sendError(proto.ErrCodeForbidden, fmt.Sprintf("peer belongs to network %q", peer.Network))
		return
	}
	if peer.PublicKey == "" {
		s.sendError(proto.ErrCodeBadRequest, "hello must carry a public_key before join")
		return
	}
	d := r.svc.Access().Check(ctx, connectRequest(peer))
	if !d.Allow {
		r.svc.Audit(ctx, peer.Network, "peer.connect_denied", peer.ID, map[string]any{"reason": d.Reason})
		s.sendError(proto.ErrCodeForbidden, "join denied: "+d.Reason)
		return
	}
	peer, err := r.svc.EnsureAddress(ctx, peer.ID)
	if err != nil {
		r.log.Error("signalling: address assignment", "peer", peer.ID, "error", err)
		s.sendError(proto.ErrCodeInternal, "address assignment failed")
		return
	}
	n, err := r.svc.Network(ctx, peer.Network)
	if err != nil {
		s.sendError(proto.ErrCodeInternal, "network lookup failed")
		return
	}

	r.mu.Lock()
	if r.sessions[peer.ID] != s {
		r.mu.Unlock()
		return
	}
	s.peer, s.joined, s.override = peer, true, d.Policy.ExitPolicy(n.Name)
	r.mu.Unlock()

	s.send(proto.TypeJoined, proto.Joined{Network: n.Name, Address: peer.Address, Pool: n.Pool})
	r.svc.Bus().Publish(events.Event{Type: events.PeerOnline, Network: n.Name, PeerID: peer.ID,
		Data: map[string]any{"address": peer.Address, "roles": peer.Roles}})
	r.NetworkChanged(ctx, n.Name)
}

// handleSignal relays an offer, answer or candidate, but only to a peer in the
// sender's netmap that is online; anything else is refused and audited once
// per pair and session.
func (r *Relay) handleSignal(ctx context.Context, s *session, t proto.Type, sig proto.Signal) {
	r.mu.RLock()
	peer, joined, nm := s.peer, s.joined, s.netmap
	target := r.sessions[sig.To]
	var targetNM *proto.Netmap
	if target != nil {
		targetNM = target.netmap
	}
	r.mu.RUnlock()

	if !joined || nm == nil {
		s.sendError(proto.ErrCodeBadRequest, "join a network before signalling")
		return
	}
	// The pair must be in each other's netmap: the netmaps are where the
	// isolation mode and the ACLs are applied.
	allowed := inNetmap(nm, sig.To) && (targetNM == nil || inNetmap(targetNM, peer.ID))
	if !allowed {
		if s.firstDenial(sig.To) {
			r.svc.Audit(ctx, peer.Network, "signal.denied", peer.ID, map[string]any{"to": sig.To, "type": t})
		}
		s.sendError(proto.ErrCodeForbidden, "signalling to "+sig.To+" is not allowed by the network policy")
		return
	}
	if target == nil || !r.Online(sig.To) {
		s.sendError(proto.ErrCodeNotFound, "peer not online: "+sig.To)
		return
	}
	d := r.svc.Access().Check(ctx, access.Request{
		Action: access.ActionConnectPeer, Network: peer.Network, Peer: peer.ID, Target: sig.To,
		Roles: peer.Roles, Tags: peer.Tags, Labels: peer.Labels,
	})
	if !d.Allow {
		if s.firstDenial(sig.To) {
			r.svc.Audit(ctx, peer.Network, "signal.denied", peer.ID, map[string]any{"to": sig.To, "type": t, "reason": d.Reason})
		}
		s.sendError(proto.ErrCodeForbidden, "connection to "+sig.To+" denied: "+d.Reason)
		return
	}
	sig.From = peer.ID
	sig.Network = peer.Network
	target.send(t, sig)
}

func inNetmap(nm *proto.Netmap, peerID string) bool {
	for _, p := range nm.Peers {
		if p.PeerID == peerID {
			return true
		}
	}
	return false
}

// handleHeartbeat answers a heartbeat and records its health summary.
func (r *Relay) handleHeartbeat(ctx context.Context, s *session, hb proto.Heartbeat) {
	s.send(proto.TypeHeartbeat, proto.Heartbeat{Nonce: hb.Nonce})
	r.mu.RLock()
	peer := s.peer
	r.mu.RUnlock()
	if hb.Health != nil {
		r.svc.RecordHealth(ctx, peer, *hb.Health)
		return
	}
	_ = r.svc.Store().TouchPeer(ctx, peer.ID, r.svc.Now().UTC())
}
