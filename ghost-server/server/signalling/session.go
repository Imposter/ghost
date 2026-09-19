package signalling

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/store"
)

// outFrame is a queued outbound frame. A fatal frame closes the connection
// once written.
type outFrame struct {
	env   proto.Envelope
	fatal bool
}

// session is one authenticated signalling connection. Fields marked "guarded
// by relay.mu" are only touched with the relay lock held; seq only under
// relay.netmapMu.
type session struct {
	relay       *Relay
	ws          *websocket.Conn
	id          string
	peerID      string // immutable copy of peer.ID, readable without the lock
	remote      string
	connectedAt time.Time
	lastSeen    atomic.Int64 // unix ms
	replaced    atomic.Bool

	// guarded by relay.mu
	peer     store.Peer
	joined   bool
	override *proto.ExitPolicy
	netmap   *proto.Netmap

	// guarded by relay.netmapMu
	seq int64

	deniedMu sync.Mutex
	denied   map[string]bool

	out       chan outFrame
	done      chan struct{}
	closeOnce sync.Once
	fatalOnce sync.Once
}

func (s *session) presenceLocked() Presence {
	p := Presence{
		PeerID:      s.peer.ID,
		SessionID:   s.id,
		Name:        s.peer.Name,
		Roles:       slices.Clone(s.peer.Roles),
		Network:     s.peer.Network,
		Address:     s.peer.Address,
		Joined:      s.joined,
		Remote:      s.remote,
		ConnectedAt: s.connectedAt,
		LastSeen:    time.UnixMilli(s.lastSeen.Load()).UTC(),
	}
	if s.netmap != nil {
		p.NetmapPeers = len(s.netmap.Peers)
		if s.netmap.Policy != nil {
			p.PolicyRevision = s.netmap.Policy.Revision
		}
	}
	return p
}

// firstDenial reports whether this is the first refused signal from this
// session to target (denials are audited once per pair and session).
func (s *session) firstDenial(target string) bool {
	s.deniedMu.Lock()
	defer s.deniedMu.Unlock()
	if s.denied == nil {
		s.denied = map[string]bool{}
	}
	if s.denied[target] {
		return false
	}
	s.denied[target] = true
	return true
}

// send queues a frame. A session whose queue is full is too slow to keep up
// and is closed. It reports whether the frame was queued.
func (s *session) send(t proto.Type, payload any) bool {
	return s.enqueue(t, payload, false)
}

func (s *session) enqueue(t proto.Type, payload any, fatal bool) bool {
	env, err := proto.Encode(t, payload)
	if err != nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.out <- outFrame{env: env, fatal: fatal}:
		return true
	case <-s.done:
		return false
	default:
		s.relay.log.Warn("signalling: send queue full, closing session", "peer", s.peerID)
		s.closeNow()
		return false
	}
}

func (s *session) sendError(code, msg string) {
	s.send(proto.TypeError, proto.Error{Code: code, Message: msg})
}

// fatal sends a fatal error and closes the session once it is written (or
// after a short grace period if the peer is not reading).
func (s *session) fatal(code, msg string) {
	s.fatalOnce.Do(func() {
		if !s.enqueue(proto.TypeError, proto.Error{Code: code, Message: msg, Fatal: true}, true) {
			s.closeNow()
			return
		}
		go func() {
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
				s.closeNow()
			}
		}()
	})
}

// closeNow tears the connection down immediately.
func (s *session) closeNow() {
	s.shutdown()
	_ = s.ws.CloseNow()
}

// shutdown stops the writer. It is idempotent.
func (s *session) shutdown() {
	s.closeOnce.Do(func() { close(s.done) })
}

func (s *session) writeLoop() {
	for {
		select {
		case <-s.done:
			return
		case f := <-s.out:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := writeEnvelope(ctx, s.ws, f.env)
			cancel()
			s.relay.metrics.Message(context.Background(), string(f.env.Type), "out")
			if err != nil {
				s.closeNow()
				return
			}
			if f.fatal {
				_ = s.ws.Close(websocket.StatusPolicyViolation, "closed by server")
				s.shutdown()
				return
			}
		}
	}
}

// readLoop dispatches inbound frames until the connection ends or the client
// stays silent for the heartbeat timeout.
func (s *session) readLoop(ctx context.Context) error {
	r := s.relay
	for {
		rctx, cancel := context.WithTimeout(ctx, r.hbDead)
		env, err := readEnvelope(rctx, s.ws)
		timedOut := errors.Is(rctx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			if timedOut {
				r.metrics.HeartbeatTimeout(ctx)
				r.log.Info("signalling: heartbeat timeout", "peer", s.peerID)
			}
			return err
		}
		s.lastSeen.Store(r.svc.Now().UnixMilli())
		r.metrics.Message(ctx, string(env.Type), "in")
		if env.V != proto.Version {
			s.sendError(proto.ErrCodeUnsupportedVersion, "frame version mismatch")
			continue
		}
		switch env.Type {
		case proto.TypeHeartbeat:
			var hb proto.Heartbeat
			if env.Decode(&hb) != nil {
				s.sendError(proto.ErrCodeBadRequest, "bad heartbeat")
				continue
			}
			r.handleHeartbeat(ctx, s, hb)
		case proto.TypeJoinNetwork:
			var j proto.JoinNetwork
			if env.Decode(&j) != nil {
				s.sendError(proto.ErrCodeBadRequest, "bad join_network")
				continue
			}
			r.handleJoin(ctx, s, j)
		case proto.TypeOffer, proto.TypeAnswer, proto.TypeCandidate:
			var sig proto.Signal
			if env.Decode(&sig) != nil || sig.To == "" {
				s.sendError(proto.ErrCodeBadRequest, "bad "+string(env.Type))
				continue
			}
			r.handleSignal(ctx, s, env.Type, sig)
		default:
			s.sendError(proto.ErrCodeBadRequest, "unexpected message type "+string(env.Type))
		}
	}
}

func readEnvelope(ctx context.Context, ws *websocket.Conn) (proto.Envelope, error) {
	var env proto.Envelope
	typ, data, err := ws.Read(ctx)
	if err != nil {
		return env, err
	}
	if typ != websocket.MessageText {
		return env, errors.New("signalling: binary frames are not supported")
	}
	return env, json.Unmarshal(data, &env)
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, env proto.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, data)
}

// writeFrame writes one frame directly (used before the writer starts).
func writeFrame(ctx context.Context, ws *websocket.Conn, t proto.Type, payload any) {
	env, err := proto.Encode(t, payload)
	if err != nil {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = writeEnvelope(wctx, ws, env)
}
