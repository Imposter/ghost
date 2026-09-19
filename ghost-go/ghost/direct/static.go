package direct

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

// StaticPeer is a peer reached with plain WireGuard over UDP, without ICE.
// At least one side of the pair needs a fixed, reachable endpoint.
type StaticPeer struct {
	// Name, if set, makes the peer id "static-<Name>" ([A-Za-z0-9_-]).
	// Otherwise the id is derived from PublicKey.
	Name string
	// PublicKey is the peer's WireGuard public key (base64).
	PublicKey string
	// Address is the peer's tunnel address (CIDR, a single host).
	Address string
	// Endpoint is the peer's reachable UDP address (host:port). Leave it
	// empty when the peer has no fixed endpoint: it then contacts this
	// member at ListenAddr, and replies go to wherever its packets last came
	// from.
	Endpoint string
	// ListenAddr is the local UDP address used for this peer (host:port).
	// It is required when Endpoint is empty, and must be the address the
	// peer lists as this member's Endpoint. Each static peer needs its own.
	ListenAddr string
}

var errNoPeerAddr = errors.New("direct: static peer has not been heard from yet")

// staticConn is a net.Conn to one static peer over a UDP socket. With a fixed
// endpoint it talks only to that address. Without one it replies to the
// source of the latest packet; WireGuard drops anything it cannot
// authenticate, so a spoofed packet can at most divert replies until the
// peer's next packet.
type staticConn struct {
	pc    *net.UDPConn
	fixed bool

	mu     sync.Mutex
	remote netip.AddrPort
}

func dialStatic(p StaticPeer) (net.Conn, error) {
	var laddr *net.UDPAddr
	if p.ListenAddr != "" {
		a, err := net.ResolveUDPAddr("udp", p.ListenAddr)
		if err != nil {
			return nil, fmt.Errorf("listen address %q: %w", p.ListenAddr, err)
		}
		laddr = a
	}
	c := &staticConn{}
	if p.Endpoint != "" {
		a, err := net.ResolveUDPAddr("udp", p.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("endpoint %q: %w", p.Endpoint, err)
		}
		c.remote = unmap(a.AddrPort())
		c.fixed = true
	}
	pc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return nil, err
	}
	c.pc = pc
	return c, nil
}

func unmap(a netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(a.Addr().Unmap(), a.Port())
}

func (c *staticConn) Read(b []byte) (int, error) {
	for {
		n, from, err := c.pc.ReadFromUDPAddrPort(b)
		if err != nil {
			return n, err
		}
		from = unmap(from)
		c.mu.Lock()
		switch {
		case !c.fixed:
			c.remote = from
		case from != c.remote:
			c.mu.Unlock()
			continue // not our peer
		}
		c.mu.Unlock()
		return n, nil
	}
}

func (c *staticConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	to := c.remote
	c.mu.Unlock()
	if !to.IsValid() {
		return 0, errNoPeerAddr
	}
	return c.pc.WriteToUDPAddrPort(b, to)
}

func (c *staticConn) Close() error        { return c.pc.Close() }
func (c *staticConn) LocalAddr() net.Addr { return c.pc.LocalAddr() }

func (c *staticConn) RemoteAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.remote.IsValid() {
		return nil
	}
	return net.UDPAddrFromAddrPort(c.remote)
}

func (c *staticConn) SetDeadline(t time.Time) error      { return c.pc.SetDeadline(t) }
func (c *staticConn) SetReadDeadline(t time.Time) error  { return c.pc.SetReadDeadline(t) }
func (c *staticConn) SetWriteDeadline(t time.Time) error { return c.pc.SetWriteDeadline(t) }
