package ice

import (
	"fmt"
	"net"
	"net/netip"
)

// Transport protocol constants
const (
	// ProtocolUDP is the UDP transport protocol
	ProtocolUDP = "udp"
	// ProtocolTCP is the TCP transport protocol
	ProtocolTCP = "tcp"
)

// ConnectionState represents the ICE connection state.
type ConnectionState string

// ICE connection states
const (
	ConnectionStateNew          ConnectionState = "new"
	ConnectionStateChecking     ConnectionState = "checking"
	ConnectionStateConnected    ConnectionState = "connected"
	ConnectionStateCompleted    ConnectionState = "completed"
	ConnectionStateFailed       ConnectionState = "failed"
	ConnectionStateDisconnected ConnectionState = "disconnected"
	ConnectionStateClosed       ConnectionState = "closed"
)

// ConnectionStateChangeCallback is called when the ICE connection state changes.
type ConnectionStateChangeCallback func(state ConnectionState)

// Candidate represents an ICE candidate.
//
// ICE candidates are network paths that can be used to establish connectivity.
// There are four types:
//   - host: Direct local interface address
//   - srflx (server-reflexive): Public address discovered via STUN
//   - prflx (peer-reflexive): Address discovered during connectivity checks
//   - relay: TURN server relay address
//
// Candidates are exchanged between peers during the signaling phase.
type Candidate struct {
	// Type is the type of candidate (host, srflx, prflx, relay)
	Type CandidateType

	// Address is the IP address (e.g., "192.168.1.100" or "2001:db8::1")
	Address string

	// Port is the port number (1-65535)
	Port int

	// Protocol is the transport protocol ("udp" or "tcp")
	Protocol string

	// Priority is the candidate priority (higher = preferred)
	// Calculated based on type, local preferences, and component ID.
	Priority uint32

	// Foundation is a string that groups candidates that share a base.
	// Candidates with the same foundation will likely succeed/fail together.
	Foundation string

	// RelatedAddress is the related address for srflx and relay candidates.
	// For host candidates, this is empty.
	// For srflx, this is the local address.
	// For relay, this is the server-reflexive address.
	RelatedAddress string

	// RelatedPort is the related port for srflx and relay candidates.
	RelatedPort int
}

// Validate checks if the candidate has valid fields.
func (c *Candidate) Validate() error {
	if c == nil {
		return ErrInvalidCandidate
	}

	if c.Address == "" {
		return fmt.Errorf("%w: address is empty", ErrInvalidCandidate)
	}

	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("%w: invalid port %d", ErrInvalidCandidate, c.Port)
	}

	if c.Protocol != ProtocolUDP && c.Protocol != ProtocolTCP {
		return fmt.Errorf("%w: invalid protocol %q", ErrInvalidCandidate, c.Protocol)
	}

	switch c.Type {
	case CandidateTypeHost, CandidateTypeSrflx, CandidateTypePrflx, CandidateTypeRelay:
		// Valid
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidCandidate, c.Type)
	}

	return nil
}

// String returns a string representation of the candidate.
func (c *Candidate) String() string {
	return fmt.Sprintf("%s %s:%d (%s)", c.Type, c.Address, c.Port, c.Protocol)
}

// CandidatePair represents a pair of local and remote candidates.
type CandidatePair struct {
	// Local is the local candidate
	Local *Candidate

	// Remote is the remote candidate
	Remote *Candidate

	// State is the state of the candidate pair
	State string
}

// String returns a string representation of the candidate pair.
func (cp *CandidatePair) String() string {
	if cp.Local == nil || cp.Remote == nil {
		return "invalid pair"
	}
	return fmt.Sprintf("%s -> %s (%s)", cp.Local, cp.Remote, cp.State)
}

// ICEEndpoint implements conn.Endpoint for WireGuard integration.
// It represents a fixed ICE connection endpoint.
type ICEEndpoint struct {
	addr net.Addr
}

// NewICEEndpoint creates a new ICE endpoint.
func NewICEEndpoint(addr net.Addr) *ICEEndpoint {
	return &ICEEndpoint{addr: addr}
}

// ClearSrc is required by conn.Endpoint interface (no-op for ICE).
func (e *ICEEndpoint) ClearSrc() {}

// SrcToString returns a string representation of the source.
func (e *ICEEndpoint) SrcToString() string {
	if e.addr == nil {
		return ""
	}
	return e.addr.String()
}

// DstToString returns a string representation of the destination.
func (e *ICEEndpoint) DstToString() string {
	return e.SrcToString()
}

// DstToBytes returns a byte representation of the destination.
func (e *ICEEndpoint) DstToBytes() []byte {
	return []byte(e.DstToString())
}

// DstIP returns the destination IP address.
func (e *ICEEndpoint) DstIP() netip.Addr {
	if e.addr == nil {
		return netip.Addr{}
	}
	switch addr := e.addr.(type) {
	case *net.UDPAddr:
		ip, _ := netip.AddrFromSlice(addr.IP)
		return ip
	case *net.TCPAddr:
		ip, _ := netip.AddrFromSlice(addr.IP)
		return ip
	default:
		return netip.Addr{}
	}
}

// SrcIP returns the source IP address.
func (e *ICEEndpoint) SrcIP() netip.Addr {
	return e.DstIP()
}
