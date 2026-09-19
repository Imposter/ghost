// Package exit provides a SOCKS5 (CONNECT only) and HTTP-CONNECT proxy server
// that listens only on a node's tunnel IP (via a netstack) and dials targets
// through the host OS network. It enforces a pluggable allowlist policy,
// records per-connection accounting through a hook interface, and supports a
// bandwidth cap and pause/resume.
package exit

import (
	"net"
	"strings"
	"sync"
)

// Policy decides whether an outbound connection to host:port is permitted.
// Implementations must be safe for concurrent use.
type Policy interface {
	// Allow reports whether a connection to the given host (a DNS name or IP
	// literal, without port) and port is permitted.
	Allow(host string, port int) bool
}

// DenyAll denies every destination. It is the default policy.
type DenyAll struct{}

// Allow always returns false.
func (DenyAll) Allow(string, int) bool { return false }

// rule is one allowlist entry.
type rule struct {
	host     string // lowercased host or wildcard pattern
	wildcard bool   // true if host began with "*."
	suffix   string // for wildcard: the ".example.com" part
	port     int    // 0 means any port
}

// Allowlist is a Policy that permits only host:port pairs it has been given.
// Host matching is exact, or wildcard for a leading "*." which matches any
// single-or-multi-label subdomain (but not the bare apex). Port 0 in a rule
// means any port. The zero value denies everything; use NewAllowlist.
//
// Allowlist never permits loopback, link-local, or private IP destinations
// unless an explicit rule names that exact IP (see host matching). Address
// classification is enforced separately by the server at dial time.
type Allowlist struct {
	mu    sync.RWMutex
	rules []rule
}

// NewAllowlist creates an empty allowlist (denies all until entries are added).
func NewAllowlist() *Allowlist { return &Allowlist{} }

// Set replaces the allowlist with the given entries. Each entry is
// "host" or "host:port"; host may be "*.example.com" for a wildcard. A "*"
// or "" port, or an entry with no port, matches any port.
func (a *Allowlist) Set(entries []string) {
	rules := make([]rule, 0, len(entries))
	for _, e := range entries {
		if r, ok := parseRule(e); ok {
			rules = append(rules, r)
		}
	}
	a.mu.Lock()
	a.rules = rules
	a.mu.Unlock()
}

// Add appends a single entry to the allowlist.
func (a *Allowlist) Add(entry string) {
	if r, ok := parseRule(entry); ok {
		a.mu.Lock()
		a.rules = append(a.rules, r)
		a.mu.Unlock()
	}
}

// Entries returns the current allowlist entries in a stable string form.
func (a *Allowlist) Entries() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.rules))
	for _, r := range a.rules {
		h := r.host
		if r.wildcard {
			h = "*" + r.suffix
		}
		if r.port == 0 {
			out = append(out, h)
		} else {
			out = append(out, net.JoinHostPort(h, itoa(r.port)))
		}
	}
	return out
}

func parseRule(entry string) (rule, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return rule{}, false
	}
	host := entry
	port := 0
	// Split off a :port if present (but keep bracketed IPv6 intact).
	if h, p, err := net.SplitHostPort(entry); err == nil {
		host = h
		if p != "" && p != "*" {
			port = atoi(p)
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return rule{}, false
	}
	if strings.HasPrefix(host, "*.") {
		return rule{host: host, wildcard: true, suffix: host[1:], port: port}, true
	}
	return rule{host: host, port: port}, true
}

// Allow reports whether host:port matches any rule.
func (a *Allowlist) Allow(host string, port int) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, r := range a.rules {
		if r.port != 0 && r.port != port {
			continue
		}
		if r.wildcard {
			// "*.example.com" matches "a.example.com" and deeper, not apex.
			if strings.HasSuffix(host, r.suffix) && len(host) > len(r.suffix) {
				return true
			}
			continue
		}
		if host == r.host {
			return true
		}
	}
	return false
}

// IsForbiddenDestination reports whether ip is one that must never be reached
// through the exit unless a policy explicitly names it: loopback, link-local
// (including link-local multicast), private (RFC1918 / ULA), unspecified, or
// interface-local. This is a hard safety guard applied after DNS resolution.
func IsForbiddenDestination(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	// Carrier-grade NAT / shared address space 100.64.0.0/10 is where tunnel
	// addresses live; never dial into it from the exit.
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1]&0xc0 == 64 {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}
