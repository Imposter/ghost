package ghost

import (
	"net"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
)

// UseLoopbackICE restricts this member's ICE to the loopback interface: host
// candidates only, no STUN or TURN (configured or advertised by the server).
// It exists for integration tests in other modules (for example
// ghost-server's), so a Node and a Hub can connect through a real signalling
// server without binding a routable socket or triggering a firewall prompt.
// It replaces any tuner set earlier.
func (c *Config) UseLoopbackICE() {
	c.iceTuner = func(ic *iceConfig) {
		ic.STUNServers = nil
		ic.TURNServers = nil
		ic.CandidateTypes = []ice.CandidateType{ice.CandidateTypeHost}
		ic.IPFilter = func(ip net.IP) bool { return ip.IsLoopback() }
	}
}
