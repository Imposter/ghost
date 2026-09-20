package main

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// policyRequest is the JSON body of ghost_set_policy. Every field is
// optional: an absent field leaves that part of the local policy alone, so
// {"paused":true} pauses and changes nothing else.
//
// The local policy never widens the control plane's: a destination must pass
// both allowlists, the effective cap is the smaller non-zero of the two, and
// the exit is paused when either side pauses it.
type policyRequest struct {
	// Allow replaces the local allowlist ("host", "host:port",
	// "*.example.com:443"). An empty array denies every destination; null
	// leaves the current list in place.
	Allow *[]string `json:"allow,omitempty"`
	// DailyBytes is the local per-UTC-day byte budget (0 = no local cap).
	DailyBytes *int64 `json:"daily_bytes,omitempty"`
	// BytesPerSecond is the local throughput cap (0 = no local cap).
	BytesPerSecond *int64 `json:"bytes_per_second,omitempty"`
	// Paused refuses new exit connections while true.
	Paused *bool `json:"paused,omitempty"`
}

// localPolicy is the caps and pause flag the host application owns.
type localPolicy struct {
	DailyBytes     int64 `json:"daily_bytes"`
	BytesPerSecond int64 `json:"bytes_per_second"`
	Paused         bool  `json:"paused"`
}

// bothAllowlists admits a destination only if both allowlists do: the control
// plane's, from the netmap, and the local one the host application sets. An
// empty control-plane list denies everything, which is what a node does
// before its first netmap arrives.
type bothAllowlists struct {
	server *exit.Allowlist
	local  atomic.Pointer[exit.Allowlist] // nil: no local narrowing
}

// Allow implements exit.Policy. It is safe for concurrent use.
func (p *bothAllowlists) Allow(host string, port int) bool {
	if !p.server.Allow(host, port) {
		return false
	}
	if l := p.local.Load(); l != nil {
		return l.Allow(host, port)
	}
	return true
}

// setLocal replaces the local allowlist.
func (p *bothAllowlists) setLocal(entries []string) {
	l := exit.NewAllowlist()
	l.Set(entries)
	p.local.Store(l)
}

// exitRuntime is a node's exit: the server, the two allowlists, the policies
// behind them, and the tunnel-side address once it is serving.
type exitRuntime struct {
	srv    *exit.Server
	policy *bothAllowlists
	port   int

	mu        sync.Mutex
	listen    string
	local     localPolicy
	server    proto.ExitPolicy
	hasServer bool
}

// newExitRuntime builds the exit x describes, with cfg as the base exit
// server config (the caller has filled in AllowSource, PeerResolver,
// Accountant and instrumentation). It returns nil when x is absent or off.
func newExitRuntime(x *exitConfig, cfg exit.Config) *exitRuntime {
	if x == nil || !x.Enabled {
		return nil
	}
	rt := &exitRuntime{
		port:   x.exitPort(),
		policy: &bothAllowlists{server: exit.NewAllowlist()},
		local: localPolicy{
			DailyBytes:     x.DailyBytes,
			BytesPerSecond: x.BytesPerSecond,
			Paused:         x.Paused,
		},
	}
	if x.Allow != nil {
		rt.policy.setLocal(x.Allow)
	}
	cfg.Policy = rt.policy
	rt.srv = exit.New(cfg)
	rt.apply()
	return rt
}

// listening returns the tunnel-side address, or "" before the exit serves.
func (x *exitRuntime) listening() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.listen
}

// serve starts the exit on the node's tunnel IP. cidr is the tunnel address
// the joined event carried.
func (x *exitRuntime) serve(l tunnelListener, cidr string) error {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return fmt.Errorf("exit: tunnel address %q: %w", cidr, err)
	}
	// x.port is range checked when the start config is parsed.
	addr := netip.AddrPortFrom(p.Addr(), uint16(x.port)).String()
	ln, err := l.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("exit: listen on the tunnel: %w", err)
	}
	x.mu.Lock()
	x.listen = addr
	x.mu.Unlock()
	go func() { _ = x.srv.Serve(ln) }()
	return nil
}

// setServerPolicy records the control plane's policy and applies the
// combination of it and the local one. It reports whether anything changed.
func (x *exitRuntime) setServerPolicy(p proto.ExitPolicy) bool {
	x.mu.Lock()
	same := x.hasServer && samePolicy(x.server, p)
	x.server, x.hasServer = p, true
	x.mu.Unlock()
	if same {
		return false
	}
	x.policy.server.Set(p.Allow)
	x.apply()
	return true
}

// setLocalPolicy applies a ghost_set_policy request.
func (x *exitRuntime) setLocalPolicy(req policyRequest) {
	x.mu.Lock()
	if req.DailyBytes != nil {
		x.local.DailyBytes = *req.DailyBytes
	}
	if req.BytesPerSecond != nil {
		x.local.BytesPerSecond = *req.BytesPerSecond
	}
	if req.Paused != nil {
		x.local.Paused = *req.Paused
	}
	x.mu.Unlock()
	if req.Allow != nil {
		x.policy.setLocal(*req.Allow)
	}
	x.apply()
}

// effective is the policy now in force on the exit server.
func (x *exitRuntime) effective() localPolicy {
	x.mu.Lock()
	local, server, hasServer := x.local, x.server, x.hasServer
	x.mu.Unlock()
	if !hasServer {
		return local
	}
	return localPolicy{
		DailyBytes:     minCap(local.DailyBytes, server.DailyBytes),
		BytesPerSecond: minCap(local.BytesPerSecond, server.BytesPerSecond),
		Paused:         local.Paused || server.Paused,
	}
}

// apply pushes the effective caps and pause flag onto the exit server.
func (x *exitRuntime) apply() {
	e := x.effective()
	x.srv.SetDailyCap(e.DailyBytes)
	x.srv.SetRate(e.BytesPerSecond)
	x.srv.SetPaused(e.Paused)
}

// localView returns a copy of the local policy.
func (x *exitRuntime) localView() localPolicy {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.local
}

// minCap combines two limits in which 0 means unlimited: the result is the
// smaller of the two that are set, or 0 when neither is.
func minCap(a, b int64) int64 {
	switch {
	case a <= 0:
		return max(b, 0)
	case b <= 0:
		return a
	default:
		return min(a, b)
	}
}

// entries renders an allowlist, or an empty slice when there is none.
func entries(a *exit.Allowlist) []string {
	if a == nil {
		return []string{}
	}
	return a.Entries()
}

// samePolicy reports whether two control-plane exit policies would have the
// same effect.
func samePolicy(a, b proto.ExitPolicy) bool {
	return a.Revision == b.Revision && a.DailyBytes == b.DailyBytes &&
		a.BytesPerSecond == b.BytesPerSecond && a.Paused == b.Paused &&
		slices.Equal(a.Allow, b.Allow)
}

// tunnelListener is the part of a ghost.Node the exit needs: a listener on
// the tunnel netstack.
type tunnelListener interface {
	Listen(network, addr string) (net.Listener, error)
}
