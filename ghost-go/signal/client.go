// Package signal is a versioned WebSocket JSON signalling client for ghost.
// It speaks the protocol defined in ghost-go/signal/proto (v1): hello/auth
// with a peer token, join-network, netmap snapshots and deltas,
// offer/answer/candidate relay, health reports, and heartbeats. It reconnects
// with exponential backoff and exposes both Go channels and callbacks.
package signal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Config configures a signalling Client.
type Config struct {
	// URL is the signalling server WebSocket URL (ws:// or wss://).
	URL string
	// PeerToken authenticates the peer.
	PeerToken string
	// PeerID is the caller's peer id, if known.
	PeerID string
	// PublicKey is the peer's WireGuard public key (base64).
	PublicKey string
	// Roles, when set, are roles the peer expects to hold; the server rejects
	// the hello if the peer lacks any of them.
	Roles []proto.Role
	// Dialer, if set, is used to open the WebSocket instead of the default.
	// It enables the in-memory fake server used in tests.
	Dialer Dialer
	// Logger is optional (defaults to slog.Default()).
	Logger *slog.Logger

	// MinBackoff and MaxBackoff bound the reconnect backoff.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// HandshakeTimeout bounds a single connect+hello exchange.
	HandshakeTimeout time.Duration
	// HeartbeatInterval is how often to send heartbeats (0 uses the server's
	// suggested interval, or a default).
	HeartbeatInterval time.Duration
}

func (c *Config) applyDefaults() {
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = 500 * time.Millisecond
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = 15 * time.Second
	}
}

// Conn is the minimal WebSocket connection the client needs. It is satisfied
// by *websocket.Conn and by the in-memory fake used in tests.
type Conn interface {
	Read(ctx context.Context) (proto.Envelope, error)
	Write(ctx context.Context, env proto.Envelope) error
	Close() error
}

// Dialer opens a signalling connection. The default dialer speaks real
// WebSocket; tests supply a dialer backed by the fake server.
type Dialer interface {
	Dial(ctx context.Context, url string) (Conn, error)
}

// Handlers holds optional callbacks invoked as messages arrive. They run on
// the client's read goroutine, so they must not block for long.
type Handlers struct {
	OnWelcome     func(proto.Welcome)
	OnJoined      func(proto.Joined)
	OnNetmap      func(proto.Netmap)
	OnNetmapDelta func(proto.NetmapDelta)
	OnSignal      func(proto.Type, proto.Signal)
	OnError       func(proto.Error)
	// OnStateChange reports connection state transitions.
	OnStateChange func(State)
}

// State is the signalling client's connection state.
type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateClosed       State = "closed"
)

// Client is a reconnecting signalling client.
type Client struct {
	cfg      Config
	handlers Handlers
	log      *slog.Logger

	mu      sync.RWMutex
	conn    Conn
	state   State
	welcome proto.Welcome
	closed  bool

	events chan Event
	sendMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Event is a decoded signalling message delivered on the Events channel.
type Event struct {
	Type    proto.Type
	Welcome *proto.Welcome
	Joined  *proto.Joined
	Netmap  *proto.Netmap
	Delta   *proto.NetmapDelta
	Signal  *proto.Signal
	Err     *proto.Error
	// State is set for connection-state change events (Type == "").
	State State
}

// New creates a signalling client. Call Start to connect.
func New(cfg Config, handlers Handlers) *Client {
	cfg.applyDefaults()
	return &Client{
		cfg:      cfg,
		handlers: handlers,
		log:      cfg.Logger,
		state:    StateDisconnected,
		events:   make(chan Event, 64),
	}
}

// Events returns the channel of decoded signalling events.
func (c *Client) Events() <-chan Event { return c.events }

// State returns the current connection state.
func (c *Client) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Welcome returns the last Welcome received (valid once connected).
func (c *Client) Welcome() proto.Welcome {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.welcome
}

// Start begins connecting and reconnecting in the background until Close.
func (c *Client) Start(ctx context.Context) {
	c.mu.Lock()
	if c.ctx != nil {
		c.mu.Unlock()
		return
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	c.wg.Add(1)
	go c.run()
}

func (c *Client) run() {
	defer c.wg.Done()
	backoff := c.cfg.MinBackoff
	for {
		select {
		case <-c.ctx.Done():
			c.setState(StateClosed)
			return
		default:
		}

		c.setState(StateConnecting)
		err := c.connectAndServe(c.ctx)
		if c.isClosed() {
			c.setState(StateClosed)
			return
		}
		if err != nil {
			c.log.Warn("signal: connection ended", "error", err, "retry_in", backoff)
		}
		c.setState(StateDisconnected)

		// Backoff with jitter.
		jitter := time.Duration(rand.Int63n(int64(backoff/2) + 1))
		select {
		case <-time.After(backoff + jitter):
		case <-c.ctx.Done():
			c.setState(StateClosed)
			return
		}
		backoff *= 2
		if backoff > c.cfg.MaxBackoff {
			backoff = c.cfg.MaxBackoff
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	dialer := c.cfg.Dialer
	if dialer == nil {
		dialer = defaultDialer{}
	}

	hsCtx, cancel := context.WithTimeout(ctx, c.cfg.HandshakeTimeout)
	conn, err := dialer.Dial(hsCtx, c.cfg.URL)
	if err != nil {
		cancel()
		return fmt.Errorf("dial: %w", err)
	}

	// Send hello.
	hello, _ := proto.Encode(proto.TypeHello, proto.Hello{
		Version:   proto.Version,
		PeerToken: c.cfg.PeerToken,
		PeerID:    c.cfg.PeerID,
		Roles:     c.cfg.Roles,
		PublicKey: c.cfg.PublicKey,
	})
	if err := conn.Write(hsCtx, hello); err != nil {
		cancel()
		_ = conn.Close()
		return fmt.Errorf("send hello: %w", err)
	}

	// Await welcome.
	env, err := conn.Read(hsCtx)
	cancel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("await welcome: %w", err)
	}
	if env.Type == proto.TypeError {
		var e proto.Error
		_ = env.Decode(&e)
		_ = conn.Close()
		c.dispatchError(e)
		return fmt.Errorf("server rejected hello: %s", e.Message)
	}
	if env.Type != proto.TypeWelcome {
		_ = conn.Close()
		return fmt.Errorf("expected welcome, got %s", env.Type)
	}
	var welcome proto.Welcome
	if err := env.Decode(&welcome); err != nil {
		_ = conn.Close()
		return fmt.Errorf("decode welcome: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.welcome = welcome
	c.mu.Unlock()
	c.setState(StateConnected)
	c.emit(Event{Type: proto.TypeWelcome, Welcome: &welcome})
	if c.handlers.OnWelcome != nil {
		c.handlers.OnWelcome(welcome)
	}

	// Heartbeat loop.
	hbInterval := c.cfg.HeartbeatInterval
	if hbInterval <= 0 && welcome.HeartbeatInterval > 0 {
		hbInterval = time.Duration(welcome.HeartbeatInterval) * time.Second
	}
	if hbInterval <= 0 {
		hbInterval = 20 * time.Second
	}
	serveCtx, serveCancel := context.WithCancel(ctx)
	defer serveCancel()
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		t := time.NewTicker(hbInterval)
		defer t.Stop()
		for {
			select {
			case <-serveCtx.Done():
				return
			case <-t.C:
				_ = c.send(serveCtx, proto.TypeHeartbeat, proto.Heartbeat{Nonce: time.Now().UnixNano()})
			}
		}
	}()

	// Read loop.
	readErr := c.readLoop(serveCtx, conn)
	serveCancel()
	hbWG.Wait()

	c.mu.Lock()
	c.conn = nil
	c.mu.Unlock()
	_ = conn.Close()
	return readErr
}

func (c *Client) readLoop(ctx context.Context, conn Conn) error {
	for {
		env, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		c.dispatch(env)
	}
}

func (c *Client) dispatch(env proto.Envelope) {
	switch env.Type {
	case proto.TypeWelcome:
		var w proto.Welcome
		_ = env.Decode(&w)
		c.emit(Event{Type: env.Type, Welcome: &w})
		if c.handlers.OnWelcome != nil {
			c.handlers.OnWelcome(w)
		}
	case proto.TypeJoined:
		var j proto.Joined
		_ = env.Decode(&j)
		c.emit(Event{Type: env.Type, Joined: &j})
		if c.handlers.OnJoined != nil {
			c.handlers.OnJoined(j)
		}
	case proto.TypeOffer, proto.TypeAnswer, proto.TypeCandidate:
		var s proto.Signal
		_ = env.Decode(&s)
		c.emit(Event{Type: env.Type, Signal: &s})
		if c.handlers.OnSignal != nil {
			c.handlers.OnSignal(env.Type, s)
		}
	case proto.TypeNetmap:
		var n proto.Netmap
		_ = env.Decode(&n)
		c.emit(Event{Type: env.Type, Netmap: &n})
		if c.handlers.OnNetmap != nil {
			c.handlers.OnNetmap(n)
		}
	case proto.TypeNetmapDelta:
		var d proto.NetmapDelta
		_ = env.Decode(&d)
		c.emit(Event{Type: env.Type, Delta: &d})
		if c.handlers.OnNetmapDelta != nil {
			c.handlers.OnNetmapDelta(d)
		}
	case proto.TypeHeartbeat:
		// Server keepalive; nothing to do.
	case proto.TypeError:
		var e proto.Error
		_ = env.Decode(&e)
		c.dispatchError(e)
	default:
		c.log.Debug("signal: ignoring unknown message", "type", env.Type)
	}
}

func (c *Client) dispatchError(e proto.Error) {
	c.emit(Event{Type: proto.TypeError, Err: &e})
	if c.handlers.OnError != nil {
		c.handlers.OnError(e)
	}
}

// Join asks to join a network. It is safe to call once connected; the reply
// arrives as a Joined event followed by a Netmap event.
func (c *Client) Join(ctx context.Context, network string) error {
	return c.send(ctx, proto.TypeJoinNetwork, proto.JoinNetwork{Network: network})
}

// SendHealth reports tunnel health to the server.
func (c *Client) SendHealth(ctx context.Context, h proto.Health) error {
	return c.send(ctx, proto.TypeHealth, h)
}

// SendOffer relays an ICE offer to a peer.
func (c *Client) SendOffer(ctx context.Context, s proto.Signal) error {
	return c.send(ctx, proto.TypeOffer, s)
}

// SendAnswer relays an ICE answer to a peer.
func (c *Client) SendAnswer(ctx context.Context, s proto.Signal) error {
	return c.send(ctx, proto.TypeAnswer, s)
}

// SendCandidate relays an ICE candidate to a peer.
func (c *Client) SendCandidate(ctx context.Context, s proto.Signal) error {
	return c.send(ctx, proto.TypeCandidate, s)
}

func (c *Client) send(ctx context.Context, t proto.Type, payload any) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return ErrNotConnected
	}
	env, err := proto.Encode(t, payload)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return conn.Write(ctx, env)
}

func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	default:
		c.log.Debug("signal: event queue full, dropping", "type", ev.Type)
	}
}

func (c *Client) setState(s State) {
	c.mu.Lock()
	if c.state == s {
		c.mu.Unlock()
		return
	}
	c.state = s
	c.mu.Unlock()
	c.emit(Event{State: s})
	if c.handlers.OnStateChange != nil {
		c.handlers.OnStateChange(s)
	}
}

func (c *Client) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// Close stops the client and closes any active connection.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cancel := c.cancel
	conn := c.conn
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	c.wg.Wait()
	return nil
}

// ErrNotConnected is returned when a send is attempted while disconnected.
var ErrNotConnected = errors.New("signal: not connected")

// defaultDialer opens a real WebSocket connection.
type defaultDialer struct{}

func (defaultDialer) Dial(ctx context.Context, url string) (Conn, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(1 << 20)
	return &wsConn{ws: ws}, nil
}

// wsConn adapts *websocket.Conn to Conn using JSON envelopes.
type wsConn struct {
	ws *websocket.Conn
}

func (w *wsConn) Read(ctx context.Context) (proto.Envelope, error) {
	var env proto.Envelope
	_, data, err := w.ws.Read(ctx)
	if err != nil {
		return env, err
	}
	if err := jsonUnmarshal(data, &env); err != nil {
		return env, err
	}
	return env, nil
}

func (w *wsConn) Write(ctx context.Context, env proto.Envelope) error {
	data, err := jsonMarshal(env)
	if err != nil {
		return err
	}
	return w.ws.Write(ctx, websocket.MessageText, data)
}

func (w *wsConn) Close() error {
	return w.ws.Close(websocket.StatusNormalClosure, "")
}
