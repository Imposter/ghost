package signalling

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies parses the addresses and CIDR prefixes of the proxies in
// front of the server ("172.16.0.0/12", "10.0.0.5").
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if p, err := netip.ParsePrefix(entry); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is neither an address nor a prefix", entry)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// ClientIP is the address a request came from. Behind proxies the connection's
// peer is the last proxy, so when that peer is trusted, X-Forwarded-For is
// read from the right: each trusted hop appended the address it was reached
// from, and the first entry that is not a trusted proxy is the client. Entries
// to its left are the client's own claim and are never believed. With no
// trusted proxy configured, or a peer that is not one, it is the peer itself.
// It returns "" when the peer address cannot be parsed.
func ClientIP(req *http.Request, trusted []netip.Prefix) string {
	peer, ok := parseHostAddr(req.RemoteAddr)
	if !ok {
		return ""
	}
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	var hops []string
	for _, header := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(header, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		addr, ok := parseHostAddr(strings.TrimSpace(hops[i]))
		if !ok {
			break // a malformed hop: trust nothing further left
		}
		client = addr
		if !isTrusted(addr, trusted) {
			break
		}
	}
	return client.String()
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHostAddr reads "ip", "ip:port" or "[ipv6]:port".
func parseHostAddr(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
