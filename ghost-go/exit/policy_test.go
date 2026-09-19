package exit

import (
	"net"
	"testing"
)

func TestAllowlistExactAndWildcard(t *testing.T) {
	a := NewAllowlist()
	a.Set([]string{"example.com:443", "*.api.example.com", "single.host"})

	cases := []struct {
		host string
		port int
		want bool
	}{
		{"example.com", 443, true},
		{"example.com", 80, false},       // port not allowed
		{"a.api.example.com", 443, true}, // wildcard, any port
		{"a.api.example.com", 8080, true},
		{"deep.a.api.example.com", 443, true},
		{"api.example.com", 443, false}, // apex not matched by *.api.example.com
		{"evil.com", 443, false},
		{"single.host", 1234, true}, // no port in rule => any port
		{"single.host", 443, true},
		{"EXAMPLE.COM", 443, true}, // case-insensitive
	}
	for _, c := range cases {
		if got := a.Allow(c.host, c.port); got != c.want {
			t.Errorf("Allow(%q,%d)=%v want %v", c.host, c.port, got, c.want)
		}
	}
}

func TestDenyAll(t *testing.T) {
	if (DenyAll{}).Allow("example.com", 443) {
		t.Fatal("DenyAll allowed a destination")
	}
}

func TestIsForbiddenDestination(t *testing.T) {
	forbidden := []string{
		"127.0.0.1", "::1", "10.0.0.5", "192.168.1.1", "172.16.0.1",
		"169.254.1.1", "0.0.0.0", "100.64.0.1", "fd00::1", "fe80::1",
	}
	for _, s := range forbidden {
		if !IsForbiddenDestination(net.ParseIP(s)) {
			t.Errorf("expected %s to be forbidden", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"}
	for _, s := range allowed {
		if IsForbiddenDestination(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
	if !IsForbiddenDestination(nil) {
		t.Error("nil IP should be forbidden")
	}
}
