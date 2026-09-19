package wireguard

import (
	"fmt"
	"net/netip"
	"slices"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Net wraps netstack.Net with accessors and helpers for userspace networking.
// It provides access to the underlying network stack created by CreateNetTUN.
type Net struct {
	*netstack.Net
	localAddrs []netip.Addr
	dnsServers []netip.Addr
	mtu        int
	guard      *localOnlyTUN
}

// ForwardDrops returns how many inbound packets the stack refused because
// they were addressed to someone else (see localOnlyTUN).
func (n *Net) ForwardDrops() uint64 {
	return n.guard.dropped.Load()
}

// localOnlyTUN wraps the netstack TUN so the stack only ever receives packets
// addressed to one of its own addresses. WireGuard hands every decrypted
// packet to the TUN, whatever its destination; a peer that routes a wider
// prefix through us (or spoofs a destination) must not get its packets
// forwarded to another peer. gVisor does not forward by default either; this
// guard makes "never forward" explicit and independent of stack settings.
type localOnlyTUN struct {
	tun.Device
	local   []netip.Addr
	dropped atomic.Uint64
}

// Write passes on the packets addressed to a local address and drops the
// rest. It always reports every packet as consumed.
func (t *localOnlyTUN) Write(bufs [][]byte, offset int) (int, error) {
	var kept [][]byte
	for i, b := range bufs {
		if t.isLocal(b[offset:]) {
			if kept != nil {
				kept = append(kept, b)
			}
			continue
		}
		t.dropped.Add(1)
		if kept == nil {
			kept = append(make([][]byte, 0, len(bufs)), bufs[:i]...)
		}
	}
	if kept == nil {
		kept = bufs
	}
	if len(kept) == 0 {
		return len(bufs), nil
	}
	if _, err := t.Device.Write(kept, offset); err != nil {
		return 0, err
	}
	return len(bufs), nil
}

// isLocal reports whether an IP packet's destination is a local address.
func (t *localOnlyTUN) isLocal(pkt []byte) bool {
	if len(pkt) < 1 {
		return false
	}
	var dst netip.Addr
	switch pkt[0] >> 4 {
	case 4:
		if len(pkt) < 20 {
			return false
		}
		dst = netip.AddrFrom4([4]byte(pkt[16:20]))
	case 6:
		if len(pkt) < 40 {
			return false
		}
		dst = netip.AddrFrom16([16]byte(pkt[24:40]))
	default:
		return false
	}
	return slices.Contains(t.local, dst)
}

// LocalAddresses returns the local addresses configured for this network stack.
func (n *Net) LocalAddresses() []netip.Addr {
	return n.localAddrs
}

// DNSServers returns the DNS servers configured for this network stack.
func (n *Net) DNSServers() []netip.Addr {
	return n.dnsServers
}

// MTU returns the MTU configured for this network stack.
func (n *Net) MTU() int {
	return n.mtu
}

// HasIPv4 returns true if any of the local addresses is an IPv4 address.
func (n *Net) HasIPv4() bool {
	for _, addr := range n.localAddrs {
		if addr.Is4() {
			return true
		}
	}
	return false
}

// HasIPv6 returns true if any of the local addresses is an IPv6 address.
func (n *Net) HasIPv6() bool {
	for _, addr := range n.localAddrs {
		if addr.Is6() {
			return true
		}
	}
	return false
}

// CreateNetTUN creates a userspace TUN interface backed by gvisor/netstack.
// This enables WireGuard tunneling without kernel TUN interfaces or root privileges.
//
// Parameters:
//   - localAddresses: At least one IP address to assign to the virtual interface.
//   - dnsServers: Optional DNS servers for the network stack.
//   - mtu: MTU value (must be between MinMTU and MaxMTU).
//
// Returns:
//   - tun.Device: The TUN interface for use with WireGuard. It delivers to
//     the stack only packets addressed to localAddresses (no forwarding).
//   - *Net: Network stack wrapper with dial/listen capabilities.
//   - error: Any error encountered during creation.
func CreateNetTUN(localAddresses []netip.Addr, dnsServers []netip.Addr, mtu int) (tun.Device, *Net, error) {
	// Validate local addresses
	if len(localAddresses) == 0 {
		return nil, nil, ErrNoLocalAddresses
	}
	for i, addr := range localAddresses {
		if !addr.IsValid() {
			return nil, nil, fmt.Errorf("%w: invalid local address at index %d", ErrNoLocalAddresses, i)
		}
	}

	// Validate MTU
	if mtu < MinMTU || mtu > MaxMTU {
		return nil, nil, fmt.Errorf("%w: must be between %d and %d, got %d", ErrInvalidMTU, MinMTU, MaxMTU, mtu)
	}

	// Validate DNS servers if provided
	for i, dns := range dnsServers {
		if !dns.IsValid() {
			return nil, nil, fmt.Errorf("invalid DNS server at index %d", i)
		}
	}

	// Create the netstack TUN interface
	tunDev, tnet, err := netstack.CreateNetTUN(localAddresses, dnsServers, mtu)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNetstackCreationFailed, err)
	}

	// Only packets for our own addresses reach the stack: never forward.
	guard := &localOnlyTUN{Device: tunDev, local: slices.Clone(localAddresses)}

	// Wrap the network stack
	net := &Net{
		Net:        tnet,
		localAddrs: localAddresses,
		dnsServers: dnsServers,
		mtu:        mtu,
		guard:      guard,
	}

	return guard, net, nil
}
