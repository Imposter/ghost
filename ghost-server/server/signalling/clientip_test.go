package signalling

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"172.16.0.0/12", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, remote string
		xff          []string
		trusted      bool
		want         string
	}{
		{"no proxy configured: the peer", "203.0.113.7:4000", []string{"198.51.100.1"}, false, "203.0.113.7"},
		{"an untrusted peer's header is ignored", "203.0.113.7:4000", []string{"198.51.100.1"}, true, "203.0.113.7"},
		{"one trusted proxy", "172.18.0.3:4000", []string{"198.51.100.1"}, true, "198.51.100.1"},
		{"two trusted hops", "172.18.0.3:4000", []string{"198.51.100.1, 10.0.0.5"}, true, "198.51.100.1"},
		{"a client's forged entry stays left of the real one", "172.18.0.3:4000", []string{"1.2.3.4, 198.51.100.1"}, true, "198.51.100.1"},
		{"split across headers", "172.18.0.3:4000", []string{"198.51.100.1", "10.0.0.5"}, true, "198.51.100.1"},
		{"a LAN client behind the proxy", "172.18.0.3:4000", []string{"192.168.1.20"}, true, "192.168.1.20"},
		{"no header from a trusted proxy: the proxy", "172.18.0.3:4000", nil, true, "172.18.0.3"},
		{"a malformed hop stops the walk", "172.18.0.3:4000", []string{"198.51.100.1, garbage"}, true, "172.18.0.3"},
		{"IPv6", "[2001:db8::1]:4000", nil, true, "2001:db8::1"},
		{"an IPv4-mapped peer", "[::ffff:172.18.0.3]:4000", []string{"198.51.100.1"}, true, "198.51.100.1"},
		{"a peer that cannot be parsed", "nonsense", nil, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &http.Request{RemoteAddr: c.remote, Header: http.Header{}}
			for _, h := range c.xff {
				req.Header.Add("X-Forwarded-For", h)
			}
			trusted := proxies
			if !c.trusted {
				trusted = nil
			}
			if got := ClientIP(req, trusted); got != c.want {
				t.Errorf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies([]string{" 172.16.0.0/12 ", "10.0.0.5", "", "::1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].String() != "10.0.0.5/32" || got[2].String() != "::1/128" {
		t.Errorf("parsed %v", got)
	}
	if _, err := ParseTrustedProxies([]string{"not-an-address"}); err == nil {
		t.Error("a bad entry was accepted")
	}
}
