// Package signalling is ghost-server's WebSocket signalling endpoint. It
// speaks the ghost-go signal/proto v1 protocol: it authenticates a device's
// hello, assigns its address on join, relays offer/answer/candidate between a
// node and the hub(s) of its network, announces presence, answers
// heartbeats, pushes exit-policy changes, and disconnects revoked devices.
package signalling

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/control"
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

	mu       sync.RWMutex
	sessions map[string]*session // device id -> session
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
	DeviceID    string     `json:"device_id"`
	SessionID   string     `json:"session_id"`
	Name        string     `json:"name"`
	Role        proto.Role `json:"role"`
	Network     string     `json:"network,omitempty"`
	Address     string     `json:"address,omitempty"`
	Joined      bool       `json:"joined"`
	Remote      string     `json:"remote"`
	ConnectedAt time.Time  `json:"connected_at"`
	LastSeen    time.Time  `json:"last_seen"`
	// PolicyRevision is the revision of the last exit policy sent.
	PolicyRevision int64 `json:"policy_revision"`
}

// Presence lists live sessions, optionally limited to one network, ordered by
// connection time.
func (r *Relay) Presence(network string) []Presence {
	r.mu.RLock()
	out := make([]Presence, 0, len(r.sessions))
	for _, s := range r.sessions {
		if network != "" && s.network != network {
			continue
		}
		out = append(out, s.presenceLocked())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.Before(out[j].ConnectedAt) })
	return out
}

// Online returns one device's presence, if it has a live session.
func (r *Relay) Online(deviceID string) (Presence, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[deviceID]
	if !ok {
		return Presence{}, false
	}
	return s.presenceLocked(), true
}

// Disconnect implements control.Sessions.
func (r *Relay) Disconnect(deviceID, code, message string) bool {
	r.mu.RLock()
	s := r.sessions[deviceID]
	r.mu.RUnlock()
	if s == nil {
		return false
	}
	s.fatal(code, message)
	return true
}

// PushNetworkPolicy implements control.Sessions. Every joined session in the
// network receives its effective policy (the network policy, or its
// authorizer override with the network's pause switch applied).
func (r *Relay) PushNetworkPolicy(p proto.ExitPolicy) int {
	type target struct {
		s      *session
		policy proto.ExitPolicy
	}
	r.mu.Lock()
	var targets []target
	for _, s := range r.sessions {
		if s.joined && s.network == p.Network {
			s.policyRev = p.Revision
			targets = append(targets, target{s, effectivePolicy(p, s.override)})
		}
	}
	r.mu.Unlock()
	n := 0
	for _, t := range targets {
		if t.s.send(proto.TypePolicy, t.policy) {
			n++
		}
	}
	return n
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

// effectivePolicy returns the policy a session should enforce. An authorizer
// override replaces the allowlist, caps and labels; the network's pause
// switch and revision always apply.
func effectivePolicy(network proto.ExitPolicy, override *proto.ExitPolicy) proto.ExitPolicy {
	if override == nil {
		return network
	}
	p := *override
	p.Network = network.Network
	p.Paused = network.Paused || override.Paused
	p.Revision = network.Revision
	if p.Allow == nil {
		p.Allow = []string{}
	}
	return p
}

// ServeHTTP upgrades to WebSocket and runs one session.
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 10)
	ctx := req.Context()

	dev, hello, err := r.handshake(ctx, ws)
	if err != nil {
		r.log.Debug("signalling: handshake failed", "remote", req.RemoteAddr, "error", err)
		_ = ws.Close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}

	now := r.svc.Now()
	s := &session{
		relay:       r,
		ws:          ws,
		id:          "sess_" + strconv.FormatUint(r.seq.Add(1), 10),
		dev:         dev,
		pubKey:      hello.PublicKey,
		remote:      req.RemoteAddr,
		connectedAt: now,
		out:         make(chan outFrame, 128),
		done:        make(chan struct{}),
	}
	s.lastSeen.Store(now.UnixMilli())

	if !r.register(s) {
		_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	r.metrics.SessionOpened(ctx, string(dev.Role))
	_ = r.svc.Store().TouchDevice(ctx, dev.ID, now.UTC())

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
	r.unregister(s)
	r.metrics.SessionClosed(context.Background(), string(dev.Role))
	_ = r.svc.Store().TouchDevice(context.Background(), dev.ID, r.svc.Now().UTC())
	r.log.Debug("signalling: session closed", "device", dev.ID, "session", s.id, "error", readErr)
}

var errHandshake = errors.New("signalling: handshake rejected")

// handshake reads and authenticates the hello frame, replying with a fatal
// error frame on failure.
func (r *Relay) handshake(ctx context.Context, ws *websocket.Conn) (store.Device, proto.Hello, error) {
	var hello proto.Hello
	rctx, cancel := context.WithTimeout(ctx, r.helloTTL)
	env, err := readEnvelope(rctx, ws)
	cancel()
	if err != nil {
		return store.Device{}, hello, err
	}
	r.metrics.Message(ctx, string(env.Type), "in")
	reject := func(code, msg string) (store.Device, proto.Hello, error) {
		writeFrame(ctx, ws, proto.TypeError, proto.Error{Code: code, Message: msg, Fatal: true})
		return store.Device{}, hello, fmt.Errorf("%w: %s", errHandshake, msg)
	}
	if env.Type != proto.TypeHello || env.Decode(&hello) != nil {
		return reject(proto.ErrCodeBadRequest, "first frame must be hello")
	}
	if env.V != proto.Version || hello.Version != proto.Version {
		return reject(proto.ErrCodeUnsupportedVersion, fmt.Sprintf("server speaks v%d", proto.Version))
	}
	dev, err := r.svc.Authenticate(ctx, hello.DeviceToken)
	if err != nil {
		return reject(proto.ErrCodeUnauthorized, "invalid or revoked device token")
	}
	if hello.DeviceID != "" && hello.DeviceID != dev.ID {
		return reject(proto.ErrCodeUnauthorized, "device id does not match token")
	}
	if hello.Role != "" && hello.Role != dev.Role {
		return reject(proto.ErrCodeUnauthorized, fmt.Sprintf("device is registered as %s", dev.Role))
	}
	if hello.PublicKey != "" {
		if !validWireGuardKey(hello.PublicKey) {
			return reject(proto.ErrCodeBadRequest, "public_key must be a base64 32-byte key")
		}
		if hello.PublicKey != dev.PublicKey {
			if err := r.svc.Store().SetDevicePublicKey(ctx, dev.ID, hello.PublicKey); err != nil {
				return reject(proto.ErrCodeInternal, "could not store public key")
			}
			dev.PublicKey = hello.PublicKey
		}
	} else {
		hello.PublicKey = dev.PublicKey
	}
	return dev, hello, nil
}

func validWireGuardKey(k string) bool {
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}

func (r *Relay) welcome(s *session) proto.Welcome {
	w := proto.Welcome{
		Version:           proto.Version,
		DeviceID:          s.dev.ID,
		SessionID:         s.id,
		HeartbeatInterval: int(r.hbEvery / time.Second),
	}
	if len(r.stunURLs) > 0 {
		w.ICEServers = append(w.ICEServers, proto.ICEServer{URLs: r.stunURLs})
	}
	if len(r.turnURLs) > 0 && r.turn != nil {
		c := r.turn.Issue(s.dev.ID, r.svc.Now())
		w.ICEServers = append(w.ICEServers, proto.ICEServer{URLs: r.turnURLs, Username: c.Username, Credential: c.Password})
	}
	return w
}

// register adds s, replacing (and closing) any older session of the device.
func (r *Relay) register(s *session) bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	old := r.sessions[s.dev.ID]
	r.sessions[s.dev.ID] = s
	r.mu.Unlock()
	if old != nil {
		old.replaced.Store(true)
		old.closeNow()
	}
	return true
}

// unregister removes s (unless already replaced) and announces it offline.
func (r *Relay) unregister(s *session) {
	r.mu.Lock()
	if r.sessions[s.dev.ID] == s {
		delete(r.sessions, s.dev.ID)
	}
	var notify []*session
	info := s.peerInfoLocked()
	network, joined := s.network, s.joined
	if joined && !s.replaced.Load() {
		notify = r.audienceLocked(s)
	}
	r.mu.Unlock()
	for _, o := range notify {
		o.send(proto.TypePeerOffline, proto.PeerEvent{Network: network, Peer: info})
	}
}

// audienceLocked returns the joined sessions that should see s's presence: a
// hub is visible to everyone in its network, a node only to the hubs.
func (r *Relay) audienceLocked(s *session) []*session {
	var out []*session
	for _, o := range r.sessions {
		if o == s || !o.joined || o.network != s.network {
			continue
		}
		if s.dev.Role == proto.RoleHub || o.dev.Role == proto.RoleHub {
			out = append(out, o)
		}
	}
	return out
}

// handleJoin authorizes a join, assigns the address, replies with joined and
// announces the member.
func (r *Relay) handleJoin(ctx context.Context, s *session, j proto.JoinNetwork) {
	if j.Network != s.dev.Network {
		s.sendError(proto.ErrCodeForbidden, fmt.Sprintf("device belongs to network %q", s.dev.Network))
		return
	}
	if s.pubKey == "" {
		s.sendError(proto.ErrCodeBadRequest, "hello must carry a public_key before join")
		return
	}
	d := r.svc.Access().Check(ctx, access.Request{
		Action: access.ActionJoinNetwork, Network: j.Network, Device: s.dev.ID,
		Role: s.dev.Role, Labels: s.dev.Labels,
	})
	if !d.Allow {
		s.sendError(proto.ErrCodeForbidden, "join denied: "+d.Reason)
		return
	}
	addr, err := r.svc.EnsureAddress(ctx, s.dev)
	if err != nil {
		r.log.Error("signalling: address assignment", "device", s.dev.ID, "error", err)
		s.sendError(proto.ErrCodeInternal, "address assignment failed")
		return
	}
	n, err := r.svc.Network(ctx, j.Network)
	if err != nil {
		s.sendError(proto.ErrCodeInternal, "network lookup failed")
		return
	}
	override := d.Policy.ExitPolicy(n.Name)

	r.mu.Lock()
	if r.sessions[s.dev.ID] != s {
		r.mu.Unlock()
		return
	}
	s.network, s.address, s.joined, s.override = n.Name, addr, true, override
	s.policyRev = n.Policy.Revision
	self := s.peerInfoLocked()
	var (
		peers []proto.PeerInfo
		hub   *proto.PeerInfo
		hubAt time.Time
	)
	audience := r.audienceLocked(s)
	for _, o := range audience {
		pi := o.peerInfoLocked()
		peers = append(peers, pi)
		if o.dev.Role == proto.RoleHub && s.dev.Role == proto.RoleNode && (hub == nil || o.connectedAt.Before(hubAt)) {
			h := pi
			hub, hubAt = &h, o.connectedAt
		}
	}
	r.mu.Unlock()

	sort.Slice(peers, func(a, b int) bool { return peers[a].DeviceID < peers[b].DeviceID })
	policy := effectivePolicy(n.Policy, override)
	s.send(proto.TypeJoined, proto.Joined{
		Network: n.Name, Address: addr, Pool: n.Pool, Hub: hub, Peers: peers, Policy: &policy,
	})
	for _, o := range audience {
		o.send(proto.TypePeerOnline, proto.PeerEvent{Network: n.Name, Peer: self})
	}
}

// handleSignal relays an offer, answer or candidate between a node and a hub
// of the same network.
func (r *Relay) handleSignal(ctx context.Context, s *session, t proto.Type, sig proto.Signal) {
	r.mu.RLock()
	joined, network := s.joined, s.network
	target := r.sessions[sig.To]
	var targetOK bool
	if target != nil {
		targetOK = target.joined && target.network == network
	}
	r.mu.RUnlock()

	if !joined {
		s.sendError(proto.ErrCodeBadRequest, "join a network before signalling")
		return
	}
	if !targetOK {
		s.sendError(proto.ErrCodeNotFound, "peer not online in this network: "+sig.To)
		return
	}
	// Exactly one side must be a hub: nodes never talk to each other.
	if (s.dev.Role == proto.RoleHub) == (target.dev.Role == proto.RoleHub) {
		s.sendError(proto.ErrCodeForbidden, "signalling is only allowed between a node and a hub")
		return
	}
	d := r.svc.Access().Check(ctx, access.Request{
		Action: access.ActionConnectPeer, Network: network, Device: s.dev.ID, Peer: target.dev.ID,
		Role: s.dev.Role, Labels: s.dev.Labels,
	})
	if !d.Allow {
		s.sendError(proto.ErrCodeForbidden, "connection to "+target.dev.ID+" denied: "+d.Reason)
		return
	}
	sig.From = s.dev.ID
	sig.Network = network
	target.send(t, sig)
}
