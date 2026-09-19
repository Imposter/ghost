package signalling

import (
	"context"
	"encoding/json"
	"errors"
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

// session is one authenticated signalling connection. Fields under "guarded
// by relay.mu" are only touched with the relay lock held.
type session struct {
	relay       *Relay
	ws          *websocket.Conn
	id          string
	dev         store.Device
	pubKey      string
	remote      string
	connectedAt time.Time
	lastSeen    atomic.Int64 // unix ms
	replaced    atomic.Bool

	// guarded by relay.mu
	network   string
	address   string
	joined    bool
	override  *proto.ExitPolicy
	policyRev int64

	out       chan outFrame
	done      chan struct{}
	closeOnce sync.Once
	fatalOnce sync.Once
}

func (s *session) presenceLocked() Presence {
	return Presence{
		DeviceID:       s.dev.ID,
		SessionID:      s.id,
		Name:           s.dev.Name,
		Role:           s.dev.Role,
		Network:        s.network,
		Address:        s.address,
		Joined:         s.joined,
		Remote:         s.remote,
		ConnectedAt:    s.connectedAt,
		LastSeen:       time.UnixMilli(s.lastSeen.Load()).UTC(),
		PolicyRevision: s.policyRev,
	}
}

func (s *session) peerInfoLocked() proto.PeerInfo {
	return proto.PeerInfo{DeviceID: s.dev.ID, PublicKey: s.pubKey, Address: s.address, Role: s.dev.Role}
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
		s.relay.log.Warn("signalling: send queue full, closing session", "device", s.dev.ID)
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
		timedOut := rctx.Err() == context.DeadlineExceeded
		cancel()
		if err != nil {
			if timedOut {
				r.metrics.HeartbeatTimeout(ctx)
				r.log.Info("signalling: heartbeat timeout", "device", s.dev.ID)
			}
			return err
		}
		now := r.svc.Now()
		s.lastSeen.Store(now.UnixMilli())
		r.metrics.Message(ctx, string(env.Type), "in")
		if env.V != proto.Version {
			s.sendError(proto.ErrCodeUnsupportedVersion, "frame version mismatch")
			continue
		}
		switch env.Type {
		case proto.TypeHeartbeat:
			var hb proto.Heartbeat
			_ = env.Decode(&hb)
			s.send(proto.TypeHeartbeat, proto.Heartbeat{Nonce: hb.Nonce})
			_ = r.svc.Store().TouchDevice(ctx, s.dev.ID, now.UTC())
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
