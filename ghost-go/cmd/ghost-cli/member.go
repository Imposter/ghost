package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// defaultStatusAddr is where a running member serves its status by default.
// It is next to metrics.DefaultPort, on loopback only.
const defaultStatusAddr = "127.0.0.1:9465"

// member is what ghost-cli needs from a ghost.Node or ghost.Hub.
type member interface {
	Start(ctx context.Context) error
	Close() error
	Events() <-chan ghost.Event
	Status() ghost.Status
	Netmap() (proto.Netmap, bool)
	Policy() (proto.ExitPolicy, bool)
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	Listen(network, addr string) (net.Listener, error)
	IsHubSource(addr net.Addr) bool
	PeerForAddr(addr net.Addr) string
	Snapshot() metrics.Snapshot
}

var (
	_ member = (*ghost.Node)(nil)
	_ member = (*ghost.Hub)(nil)
)

// testHooks are seams for the in-process tests: an in-memory signalling
// server, loopback-only ICE, and the addresses of the host listeners.
type testHooks struct {
	dialer      signal.Dialer
	loopbackICE bool
	onListen    func(what, addr string)
}

func (h *testHooks) tune(cfg *ghost.Config) {
	if h == nil {
		return
	}
	if h.dialer != nil {
		cfg.SignalDialer = h.dialer
	}
	if h.loopbackICE {
		cfg.UseLoopbackICE()
	}
}

func (h *testHooks) listening(what string, addr net.Addr) {
	if h != nil && h.onListen != nil {
		h.onListen(what, addr.String())
	}
}

// controlFlags select the control plane and the peer's credentials.
type controlFlags struct {
	server  string
	creds   string
	token   string
	network string
}

func (c *controlFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.server, "server", envOr("GHOST_SERVER", ""), "control plane base URL; defaults to the one in -creds ($GHOST_SERVER)")
	fs.StringVar(&c.creds, "creds", envOr("GHOST_CREDS", defaultCreds), "credentials file from enroll or POST /control/networks/{net}/peers ($GHOST_CREDS)")
	fs.StringVar(&c.token, "token", envOr("GHOST_PEER_TOKEN", ""), "peer token, instead of -creds ($GHOST_PEER_TOKEN)")
	fs.StringVar(&c.network, "network", envOr("GHOST_NETWORK", ""), "network to join; defaults to the one in -creds ($GHOST_NETWORK)")
}

// config fills the control-plane part of a ghost.Config.
func (c *controlFlags) config() (ghost.Config, error) {
	var cfg ghost.Config
	creds := credentials{PeerToken: c.token}
	if c.token == "" {
		var err error
		if creds, err = loadCredentials(c.creds); err != nil {
			return cfg, fmt.Errorf("%w (enrol first, or pass -token and -network)", err)
		}
	}
	server := c.server
	if server == "" {
		server = creds.Server
	}
	if server == "" {
		return cfg, errors.New("no control plane: pass -server")
	}
	u, err := signalURL(server)
	if err != nil {
		return cfg, err
	}
	cfg.SignalURL = u
	cfg.PeerToken = creds.PeerToken
	cfg.PeerID = creds.PeerID
	cfg.Network = c.network
	if cfg.Network == "" {
		cfg.Network = creds.Network
	}
	if cfg.Network == "" {
		return cfg, errors.New("no network: pass -network")
	}
	return cfg, nil
}

// memberFlags are shared by every long-running member.
type memberFlags struct {
	keys           string
	logLevel       string
	status         string
	stun           *listFlag
	turn           *listFlag
	turnUser       string
	turnPass       string
	forwards       *listFlag
	connectTimeout time.Duration
}

func (m *memberFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&m.keys, "keys", envOr("GHOST_KEYS", defaultKeys), "WireGuard key file, created if missing ($GHOST_KEYS)")
	fs.StringVar(&m.logLevel, "log-level", envOr("GHOST_LOG_LEVEL", "info"), "debug, info, warn or error ($GHOST_LOG_LEVEL)")
	fs.StringVar(&m.status, "status", envOr("GHOST_STATUS", defaultStatusAddr), `host address to serve status on for "ghost-cli status"; empty disables ($GHOST_STATUS)`)
	m.stun = newListFlag("GHOST_STUN")
	fs.Var(m.stun, "stun", "extra STUN URL, e.g. stun:stun.example.com:3478 (repeatable; $GHOST_STUN)")
	m.turn = newListFlag("GHOST_TURN")
	fs.Var(m.turn, "turn", "extra TURN URL, with -turn-user and -turn-pass (repeatable; $GHOST_TURN)")
	fs.StringVar(&m.turnUser, "turn-user", envOr("GHOST_TURN_USER", ""), "TURN username ($GHOST_TURN_USER)")
	fs.StringVar(&m.turnPass, "turn-pass", envOr("GHOST_TURN_PASS", ""), "TURN password ($GHOST_TURN_PASS)")
	m.forwards = newListFlag("GHOST_FORWARD")
	fs.Var(m.forwards, "forward", "LOCAL=PEER:PORT: listen on the host at LOCAL and forward each connection to PEER:PORT over the tunnel (repeatable; $GHOST_FORWARD)")
	fs.DurationVar(&m.connectTimeout, "connect-timeout", 30*time.Second, "ICE connect timeout per peer")
}

func (m *memberFlags) apply(cfg *ghost.Config, log *slog.Logger) {
	cfg.KeyStorePath = m.keys
	cfg.Logger = log
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = m.connectTimeout
	}
	for _, u := range *m.stun {
		cfg.STUNServers = append(cfg.STUNServers, ghost.STUNServer{URL: u})
	}
	if len(*m.turn) > 0 {
		cfg.TURNServers = append(cfg.TURNServers, ghost.TURNServer{URLs: *m.turn, Username: m.turnUser, Password: m.turnPass})
	}
}

// forward is one -forward: a host listener whose connections go to a peer.
type forward struct {
	local  string
	target string // PEER:PORT
}

func parseForwards(specs []string) ([]forward, error) {
	out := make([]forward, 0, len(specs))
	for _, s := range specs {
		local, target, ok := strings.Cut(s, "=")
		if !ok || local == "" || target == "" {
			return nil, fmt.Errorf("-forward %q: want LOCAL=PEER:PORT", s)
		}
		if _, _, err := net.SplitHostPort(target); err != nil {
			return nil, fmt.Errorf("-forward %q: target: %w", s, err)
		}
		out = append(out, forward{local: local, target: target})
	}
	return out, nil
}

// exitFlags configure an exit and in-tunnel metrics on a node.
type exitFlags struct {
	enabled bool
	port    int
	allow   *listFlag
	metrics bool
}

func (x *exitFlags) register(fs *flag.FlagSet, withMetrics bool) {
	fs.BoolVar(&x.enabled, "exit", envOr("GHOST_EXIT", "") == "true", "serve a SOCKS5/HTTP-CONNECT exit on the tunnel IP ($GHOST_EXIT=true)")
	port, _ := strconv.Atoi(envOr("GHOST_EXIT_PORT", strconv.Itoa(exit.DefaultPort)))
	fs.IntVar(&x.port, "exit-port", port, "tunnel-side exit port ($GHOST_EXIT_PORT)")
	x.allow = newListFlag("GHOST_ALLOW")
	fs.Var(x.allow, "allow", "local exit allowlist entry: host, host:port or *.suffix:port (repeatable; $GHOST_ALLOW)")
	if withMetrics {
		fs.BoolVar(&x.metrics, "metrics", envOr("GHOST_METRICS", "") == "true",
			fmt.Sprintf("serve metrics on the tunnel IP, port %d, to the netmap's hubs ($GHOST_METRICS=true)", metrics.DefaultPort))
	}
}

// exitPolicy admits a destination only if every configured allowlist does:
// the control plane's (from the netmap) and the local one (-allow). With
// neither, it denies everything.
type exitPolicy struct {
	server *exit.Allowlist
	local  *exit.Allowlist
}

func (p exitPolicy) Allow(host string, port int) bool {
	if p.server == nil && p.local == nil {
		return false
	}
	if p.server != nil && !p.server.Allow(host, port) {
		return false
	}
	return p.local == nil || p.local.Allow(host, port)
}

// exitRuntime is a member's exit: the server, its allowlists and its
// tunnel-side address once serving.
type exitRuntime struct {
	srv    *exit.Server
	policy exitPolicy
	port   int

	mu      sync.Mutex
	listen  string            // tunnel-side address once serving
	applied *proto.ExitPolicy // the control-plane policy last applied
}

// listening returns the tunnel-side address, or "" before the exit serves.
func (x *exitRuntime) listening() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.listen
}

// runOptions configure runMember.
type runOptions struct {
	mode     string
	log      *slog.Logger
	status   string
	forwards []forward
	exit     *exitRuntime
	// collector backs the recent connections in the status (may be nil).
	collector *metrics.Collector
	hooks     *testHooks
}

// runMember starts m and serves its exit, forwards and status until ctx
// ends.
func runMember(ctx context.Context, m member, o runOptions) error {
	if err := m.Start(ctx); err != nil {
		return err
	}
	defer m.Close()

	var closers []io.Closer
	defer func() {
		for _, c := range slices.Backward(closers) {
			_ = c.Close()
		}
	}()
	if o.status != "" {
		ln, err := net.Listen("tcp", o.status)
		if err != nil {
			return fmt.Errorf("status listener: %w", err)
		}
		srv := &http.Server{Handler: statusHandler(m, o), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		closers = append(closers, srv)
		o.hooks.listening("status", ln.Addr())
		o.log.Info("status", "addr", ln.Addr().String())
	}
	for _, f := range o.forwards {
		ln, err := net.Listen("tcp", f.local)
		if err != nil {
			return fmt.Errorf("forward %s: %w", f.local, err)
		}
		closers = append(closers, ln)
		go serveForward(ctx, m, ln, f.target, o.log)
		o.hooks.listening("forward", ln.Addr())
		o.log.Info("forwarding", "local", ln.Addr().String(), "target", f.target)
	}
	if o.exit != nil {
		closers = append(closers, o.exit.srv)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-m.Events():
			switch ev.Kind {
			case ghost.EventJoined:
				st := m.Status()
				o.log.Info("joined", "network", st.Network, "peer", st.PeerID, "address", ev.Address, "roles", st.Roles)
				if o.exit != nil && o.exit.listening() == "" {
					if err := startExit(m, o.exit, ev.Address, o.log); err != nil {
						return err
					}
				}
			case ghost.EventPolicy:
				if o.exit != nil && o.exit.policy.server != nil && ev.Policy != nil {
					applyPolicy(o.exit, *ev.Policy, o.log)
				}
			case ghost.EventPeerConnected:
				o.log.Info("peer connected", "peer", ev.PeerID, "address", ev.Address, "candidate", ev.CandidateType)
			case ghost.EventPeerDisconnected:
				o.log.Info("peer disconnected", "peer", ev.PeerID)
			case ghost.EventSignalState:
				o.log.Debug("signalling", "state", ev.SignalState)
			case ghost.EventNetmap:
				if nm, ok := m.Netmap(); ok {
					o.log.Debug("netmap", "seq", nm.Seq, "peers", len(nm.Peers))
				}
			case ghost.EventError:
				o.log.Warn("error", "peer", ev.PeerID, "error", ev.Err)
			}
		}
	}
}

// startExit serves the exit on the member's tunnel IP.
func startExit(m member, x *exitRuntime, cidr string, log *slog.Logger) error {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return fmt.Errorf("tunnel address %q: %w", cidr, err)
	}
	addr := netip.AddrPortFrom(p.Addr(), uint16(x.port)).String()
	ln, err := m.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("exit listener: %w", err)
	}
	x.mu.Lock()
	x.listen = addr
	x.mu.Unlock()
	go func() { _ = x.srv.Serve(ln) }()
	if x.policy.server != nil {
		if pol, ok := m.Policy(); ok {
			applyPolicy(x, pol, log)
		}
	}
	log.Info("exit serving", "addr", addr, "local_allow", entries(x.policy.local))
	return nil
}

// applyPolicy applies the control plane's exit policy, unless it is the one
// already applied.
func applyPolicy(x *exitRuntime, p proto.ExitPolicy, log *slog.Logger) {
	x.mu.Lock()
	same := x.applied != nil && reflect.DeepEqual(*x.applied, p)
	x.applied = &p
	x.mu.Unlock()
	if same {
		return
	}
	x.policy.server.Set(p.Allow)
	x.srv.SetDailyCap(p.DailyBytes)
	x.srv.SetRate(p.BytesPerSecond)
	x.srv.SetPaused(p.Paused)
	log.Info("exit policy", "revision", p.Revision, "allow", p.Allow, "daily_bytes", p.DailyBytes,
		"bytes_per_second", p.BytesPerSecond, "paused", p.Paused)
}

func entries(a *exit.Allowlist) []string {
	if a == nil {
		return nil
	}
	return a.Entries()
}

// newExit builds a member's exit. Under a control plane the netmap's exit
// policy applies (and -allow narrows it further), and only hubs may use the
// exit; without one, -allow alone applies and any linked peer may use it.
func newExit(x exitFlags, controlPlane bool, m member, col *metrics.Collector, o exit.Config) (*exitRuntime, error) {
	if !x.enabled {
		return nil, nil
	}
	if x.port <= 0 || x.port > 65535 {
		return nil, fmt.Errorf("-exit-port %d out of range", x.port)
	}
	rt := &exitRuntime{port: x.port}
	if len(*x.allow) > 0 {
		rt.policy.local = exit.NewAllowlist()
		rt.policy.local.Set(*x.allow)
	}
	if controlPlane {
		rt.policy.server = exit.NewAllowlist()
		o.AllowSource = m.IsHubSource
	} else {
		if rt.policy.local == nil {
			return nil, errors.New("-exit without a control plane needs -allow")
		}
		o.AllowSource = func(a net.Addr) bool { return m.PeerForAddr(a) != "" }
	}
	o.Policy = rt.policy
	o.PeerResolver = m
	if col != nil {
		o.Accountant = col
	}
	rt.srv = exit.New(o)
	if col != nil {
		col.AttachExit(rt.srv)
	}
	return rt, nil
}

// serveForward accepts host connections and pipes each to target over the
// tunnel.
func serveForward(ctx context.Context, m member, ln net.Listener, target string, log *slog.Logger) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			addr, err := resolveTarget(m, target)
			if err != nil {
				log.Warn("forward", "target", target, "error", err)
				return
			}
			dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			t, err := m.DialContext(dctx, "tcp", addr)
			cancel()
			if err != nil {
				log.Warn("forward", "target", target, "error", err)
				return
			}
			defer t.Close()
			pipe(c, t)
		}()
	}
}

// pipe copies both ways until both directions finish.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		closeWrite(dst)
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// closeWrite half-closes c when it supports it, and closes it otherwise.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// resolvePeer finds a netmap peer by id, name or tunnel IP.
func resolvePeer(m member, ref string) (proto.PeerInfo, bool) {
	nm, ok := m.Netmap()
	if !ok {
		return proto.PeerInfo{}, false
	}
	ip, ipErr := netip.ParseAddr(ref)
	for _, p := range nm.Peers {
		if p.PeerID == ref || (p.Name != "" && p.Name == ref) {
			return p, true
		}
		if ipErr == nil {
			if pfx, err := netip.ParsePrefix(p.Address); err == nil && pfx.Addr() == ip {
				return p, true
			}
		}
	}
	return proto.PeerInfo{}, false
}

// resolveTarget turns PEER:PORT into a tunnel IP:PORT. An IP literal that is
// not in the netmap is used as is.
func resolveTarget(m member, target string) (string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", err
	}
	if p, ok := resolvePeer(m, host); ok {
		pfx, err := netip.ParsePrefix(p.Address)
		if err != nil {
			return "", fmt.Errorf("peer %s address %q: %w", p.PeerID, p.Address, err)
		}
		return net.JoinHostPort(pfx.Addr().String(), port), nil
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return target, nil
	}
	return "", fmt.Errorf("peer %q is not in the netmap", host)
}

// parseRoles parses role names.
func parseRoles(names []string) ([]proto.Role, error) {
	out := make([]proto.Role, 0, len(names))
	for _, n := range names {
		r := proto.Role(n)
		if !r.Valid() {
			return nil, fmt.Errorf("unknown role %q (want hub, node, exit or relay)", n)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// stderrLogger builds the command's logger.
func stderrLogger(e env, level string) (*slog.Logger, error) {
	w := e.stderr
	if w == nil {
		w = os.Stderr
	}
	return newLogger(w, level)
}
