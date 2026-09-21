package ghost

import (
	"context"
	"net"

	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Signaller is everything a Node or Hub needs from signalling: its identity
// and address, the set of peers it may link to, and a way to swap ICE
// descriptions with them. The control-plane client (the default when
// Config.Signaller is nil) is one implementation; package ghost/direct is a
// standalone one that needs no server.
//
// A Signaller delivers, in order, on Events:
//
//   - a State event with signal.StateConnected once it is ready, and a Welcome
//     carrying this member's peer id;
//   - after Join, a Joined event (the tunnel address) and then a Netmap
//     snapshot, followed by NetmapDelta events as peers come and go;
//   - Signal events (offer, answer, candidate) carrying a remote peer's ICE
//     credentials and candidates, with From set to that peer's id.
//
// The member sends its own description back through Send. Methods may be
// called from several goroutines.
type Signaller interface {
	// Start begins signalling for the member described by self.
	Start(ctx context.Context, self SignalSelf) error
	// Events returns the channel of signalling events.
	Events() <-chan signal.Event
	// State returns the connection state (StateConnected when usable).
	State() signal.State
	// Join asks to join network. A Joined event and a Netmap follow.
	Join(ctx context.Context, network string) error
	// ICEServers returns STUN/TURN servers to add to the configured ones.
	ICEServers() []proto.ICEServer
	// Link says how to reach peer, which is in the current netmap. An error
	// skips the peer.
	Link(self, peer proto.PeerInfo) (LinkPlan, error)
	// Send delivers this member's local description to s.To: an offer or
	// answer (Ufrag and Pwd) or a candidate. A candidate Signal with a nil
	// Candidate marks the end of gathering.
	Send(ctx context.Context, t proto.Type, s proto.Signal) error
	// ReportHealth reports the member's health now (a no-op without a
	// control plane).
	ReportHealth(ctx context.Context) error
	// Close stops the signaller.
	Close() error
}

// SignalSelf is what a Signaller learns about the member it serves.
type SignalSelf struct {
	// PublicKey is the member's WireGuard public key (base64).
	PublicKey string
	// Roles are the roles the member asks to hold (Config.Roles, plus hub
	// for NewHub). The control plane requires the peer to hold them; a
	// standalone signaller may simply adopt them.
	Roles []proto.Role
	// Health returns the member's current health summary.
	Health func() *proto.Health
}

// LinkPlan is a Signaller's answer to how a peer is reached.
type LinkPlan struct {
	// Controlling makes this member the ICE controlling agent, the side
	// that sends the offer. Exactly one side of a pair must be controlling.
	Controlling bool
	// Conn, when set, is a ready packet connection to the peer: the member
	// skips ICE and runs WireGuard over it directly (static peers). The
	// member closes it when the link goes away.
	Conn net.Conn
}

// controlPlane adapts the ghost-server signalling client to Signaller.
type controlPlane struct {
	cfg Config
	c   *signal.Client
}

func (p *controlPlane) Start(ctx context.Context, self SignalSelf) error {
	p.c = signal.New(signal.Config{
		URL:       p.cfg.SignalURL,
		Dialer:    p.cfg.SignalDialer,
		TLS:       p.cfg.SignalTLS,
		PeerToken: p.cfg.PeerToken,
		PeerID:    p.cfg.PeerID,
		PublicKey: self.PublicKey,
		Roles:     self.Roles,
		Logger:    p.cfg.logger(),
		Health:    self.Health,
	}, signal.Handlers{})
	p.c.Start(ctx)
	return nil
}

func (p *controlPlane) Events() <-chan signal.Event { return p.c.Events() }

func (p *controlPlane) State() signal.State { return p.c.State() }

func (p *controlPlane) Join(ctx context.Context, network string) error {
	return p.c.Join(ctx, network)
}

func (p *controlPlane) ICEServers() []proto.ICEServer { return p.c.Welcome().ICEServers }

func (p *controlPlane) Link(self, peer proto.PeerInfo) (LinkPlan, error) {
	return LinkPlan{Controlling: proto.Controlling(self.PeerID, self.Roles, peer.PeerID, peer.Roles)}, nil
}

func (p *controlPlane) Send(ctx context.Context, t proto.Type, s proto.Signal) error {
	switch t {
	case proto.TypeOffer:
		return p.c.SendOffer(ctx, s)
	case proto.TypeAnswer:
		return p.c.SendAnswer(ctx, s)
	case proto.TypeCandidate:
		if s.Candidate == nil {
			return nil // the relay has no end-of-candidates message
		}
		return p.c.SendCandidate(ctx, s)
	}
	return nil
}

func (p *controlPlane) ReportHealth(ctx context.Context) error { return p.c.SendHeartbeat(ctx) }

func (p *controlPlane) Close() error { return p.c.Close() }
