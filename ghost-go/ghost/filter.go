package ghost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// ErrNotJoined is returned by DialContext and Listen before the member has
// joined its network.
var ErrNotJoined = errors.New("ghost: not joined")

// DialContext dials addr ("host:port") over the tunnel netstack.
func (m *mesh) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ns := m.Netstack()
	if ns == nil {
		return nil, ErrNotJoined
	}
	return netstackDialContext(ctx, ns, network, addr)
}

// Listen listens on addr ("ip:port", normally this member's tunnel IP) over
// the tunnel netstack. Accepted connections are checked against the netmap's
// packet filter: a connection from a source the ACLs do not admit to this
// port is closed before Accept returns it.
func (m *mesh) Listen(network, addr string) (net.Listener, error) {
	if network != "tcp" && network != "tcp4" {
		return nil, fmt.Errorf("ghost: listen %s: only tcp is supported", network)
	}
	ns := m.Netstack()
	if ns == nil {
		return nil, ErrNotJoined
	}
	tcpAddr, err := resolveTCPAddr(addr)
	if err != nil {
		return nil, err
	}
	ln, err := ns.ListenTCP(tcpAddr)
	if err != nil {
		return nil, err
	}
	return &filteredListener{Listener: ln, m: m, port: tcpAddr.Port}, nil
}

// IsHubSource reports whether addr is the tunnel address of a peer holding
// the hub role in the current netmap. Pass it as exit.Config.AllowSource so a
// peer's exit only serves its hubs.
func (m *mesh) IsHubSource(addr net.Addr) bool {
	ip, ok := addrIP(addr)
	if !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isHubIPLocked(ip)
}

func (m *mesh) isHubIPLocked(ip netip.Addr) bool {
	if m.netmap == nil {
		return false
	}
	for _, p := range m.netmap.Peers {
		if a, ok := tunnelAddr(p.Address); ok && a == ip && proto.HasRole(p.Roles, proto.RoleHub) {
			return true
		}
	}
	return false
}

// hubOnlyLocked reports whether hub-only isolation binds this member: the
// network is hub-only and this member is not a hub. Caller holds m.mu.
func (m *mesh) hubOnlyLocked() bool {
	return m.netmap != nil && m.netmap.Isolation == proto.IsolationHubOnly &&
		!proto.HasRole(m.netmap.Self.Roles, proto.RoleHub)
}

// admits reports whether src may reach port on this member. Under hub-only
// isolation a non-hub member admits only its netmap's hubs. Otherwise the
// netmap's packet filter decides; with no filter (in-memory test servers),
// everything is admitted.
func (m *mesh) admits(src netip.Addr, port int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hubOnlyLocked() && !m.isHubIPLocked(src) {
		return false
	}
	if m.netmap == nil || m.netmap.Filter == nil {
		return true
	}
	for _, r := range m.netmap.Filter.Rules {
		if len(r.Ports) > 0 && !slices.Contains(r.Ports, port) {
			continue
		}
		for _, s := range r.Src {
			if p, err := netip.ParsePrefix(s); err == nil && p.Contains(src) {
				return true
			}
			if a, err := netip.ParseAddr(s); err == nil && a == src {
				return true
			}
		}
	}
	return false
}

// filteredListener drops connections the packet filter does not admit.
type filteredListener struct {
	net.Listener
	m    *mesh
	port int
}

func (l *filteredListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if src, ok := addrIP(c.RemoteAddr()); ok && l.m.admits(src, l.port) {
			return c, nil
		}
		l.m.log.Debug("ghost: connection refused by packet filter", "remote", c.RemoteAddr(), "port", l.port)
		_ = c.Close()
	}
}
