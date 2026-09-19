package testutil

import (
	"net"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
)

// LoopbackICEConfig returns an ICE config restricted to the loopback interface
// with host candidates only, no STUN/TURN, and no mDNS. Using it in tests
// avoids Windows firewall prompts and new-port approvals: no candidate ever
// binds a non-loopback address and no external server is contacted.
//
// portOffset lets a test give each agent a distinct fixed loopback port when a
// fixed port is needed; pass 0 to let the OS choose an ephemeral port.
func LoopbackICEConfig(portOffset int) *ice.ICEConfig {
	c := ice.DefaultICEConfig()
	c.STUNServers = nil
	c.TURNServers = nil
	c.CandidateTypes = []ice.CandidateType{ice.CandidateTypeHost}
	c.IPFilter = func(ip net.IP) bool { return ip.IsLoopback() }
	if portOffset > 0 {
		port := uint16(ice.TestPortRangeStart + portOffset)
		c.PortMin = port
		c.PortMax = port
	}
	return c
}
