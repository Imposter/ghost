package wireguard

import (
	"fmt"
	"net/netip"

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

// CreateNetTUN creates a userspace TUN device backed by gvisor/netstack.
// This enables WireGuard tunneling without kernel TUN devices or root privileges.
//
// Parameters:
//   - localAddresses: At least one IP address to assign to the virtual interface.
//   - dnsServers: Optional DNS servers for the network stack.
//   - mtu: MTU value (must be between MinMTU and MaxMTU).
//
// Returns:
//   - tun.Device: The TUN device for use with WireGuard.
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

	// Create the netstack TUN device
	tunDev, tnet, err := netstack.CreateNetTUN(localAddresses, dnsServers, mtu)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNetstackCreationFailed, err)
	}

	// Wrap the network stack
	net := &Net{
		Net:        tnet,
		localAddrs: localAddresses,
		dnsServers: dnsServers,
		mtu:        mtu,
	}

	return tunDev, net, nil
}
