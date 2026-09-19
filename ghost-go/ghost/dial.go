package ghost

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/Imposter/ghost/ghost-go/internal/wireguard"
)

// netstackDialContext dials addr ("host:port") over the given netstack. Only
// TCP is supported. A host name is resolved through the netstack's DNS.
func netstackDialContext(ctx context.Context, ns *wireguard.Net, network, addr string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6", "":
	default:
		return nil, fmt.Errorf("unsupported network %q (tcp only)", network)
	}
	tcpAddr, err := resolveTCPAddrNS(ctx, ns, addr)
	if err != nil {
		return nil, err
	}
	return ns.DialContextTCP(ctx, tcpAddr)
}

func resolveTCPAddr(addr string) (*net.TCPAddr, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, fmt.Errorf("listen address must be an IP literal: %w", err)
	}
	return &net.TCPAddr{IP: ip.AsSlice(), Port: port}, nil
}

func resolveTCPAddrNS(ctx context.Context, ns *wireguard.Net, addr string) (*net.TCPAddr, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return &net.TCPAddr{IP: ip.AsSlice(), Port: port}, nil
	}
	addrs, err := ns.LookupContextHost(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses for %q", host)
	}
	ip, err := netip.ParseAddr(addrs[0])
	if err != nil {
		return nil, err
	}
	return &net.TCPAddr{IP: ip.AsSlice(), Port: port}, nil
}
