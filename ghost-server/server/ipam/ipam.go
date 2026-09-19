// Package ipam assigns tunnel addresses from a network's IPv4 pool.
package ipam

import (
	"errors"
	"fmt"
	"net/netip"
)

// ErrPoolExhausted is returned when every usable address is taken.
var ErrPoolExhausted = errors.New("ipam: address pool exhausted")

// ParsePool parses and validates an IPv4 pool in CIDR form. The pool must
// leave at least one usable host address.
func ParsePool(pool string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(pool)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("ipam: pool %q: %w", pool, err)
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("ipam: pool %q is not IPv4", pool)
	}
	if p.Bits() > 30 {
		return netip.Prefix{}, fmt.Errorf("ipam: pool %q is too small", pool)
	}
	return p.Masked(), nil
}

// Allocate returns the lowest free host address in pool as a /32 CIDR,
// skipping the network and broadcast addresses and anything in used. used
// holds addresses in either bare ("100.64.0.2") or CIDR ("100.64.0.2/32")
// form; unparsable entries are ignored.
func Allocate(pool netip.Prefix, used []string) (string, error) {
	taken := make(map[netip.Addr]struct{}, len(used))
	for _, u := range used {
		if a, ok := parseAddr(u); ok {
			taken[a] = struct{}{}
		}
	}
	base := toUint(pool.Addr())
	size := uint32(1) << (32 - pool.Bits())
	// Hosts are base+1 .. base+size-2 (network and broadcast excluded).
	for off := uint32(1); off < size-1; off++ {
		a := fromUint(base + off)
		if _, ok := taken[a]; !ok {
			return netip.PrefixFrom(a, 32).String(), nil
		}
	}
	return "", ErrPoolExhausted
}

// Contains reports whether addr (bare or CIDR) lies inside pool.
func Contains(pool netip.Prefix, addr string) bool {
	a, ok := parseAddr(addr)
	return ok && pool.Contains(a)
}

func parseAddr(s string) (netip.Addr, bool) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	return netip.Addr{}, false
}

func toUint(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func fromUint(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
