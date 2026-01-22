package mobile

// Constants for virtual network configuration
const (
	// DesktopVirtualIP is the IP address assigned to the desktop/server peer
	DesktopVirtualIP = "10.0.0.1"

	// MobileVirtualIP is the IP address assigned to the mobile peer
	MobileVirtualIP = "10.0.0.2"

	// VirtualSubnet is the subnet for the virtual network
	VirtualSubnet = "10.0.0.0/24"

	// DefaultMTU is the default MTU for the tunnel
	DefaultMTU = 1420

	// HTTPTestPort is the default port for HTTP test server
	HTTPTestPort = 8080
)

// CandidateJSON represents an ICE candidate in JSON-serializable form.
// This is the type used in signaling data exchange.
type CandidateJSON struct {
	Type       string `json:"type"`              // "host", "srflx", "prflx", "relay"
	Address    string `json:"address"`           // IP address
	Port       int    `json:"port"`              // Port number
	Protocol   string `json:"protocol"`          // "udp" or "tcp"
	Priority   uint32 `json:"priority"`          // Candidate priority
	Foundation string `json:"foundation"`        // Candidate foundation
	RelAddr    string `json:"relAddr,omitempty"` // Related address (for reflexive/relay)
	RelPort    int    `json:"relPort,omitempty"` // Related port
}

// CredentialsJSON represents ICE credentials in JSON form.
type CredentialsJSON struct {
	Ufrag string `json:"ufrag"` // ICE username fragment
	Pwd   string `json:"pwd"`   // ICE password
}

// KeyPairJSON represents a WireGuard key pair.
type KeyPairJSON struct {
	PrivateKey string `json:"privateKey"` // Base64-encoded private key
	PublicKey  string `json:"publicKey"`  // Base64-encoded public key
}

// SignalingDataJSON contains all data needed for peer connection.
// This is what gets exchanged via QR code or other signaling mechanism.
type SignalingDataJSON struct {
	Ufrag      string          `json:"ufrag"`      // ICE username fragment
	Pwd        string          `json:"pwd"`        // ICE password
	PublicKey  string          `json:"publicKey"`  // WireGuard public key (base64)
	Candidates []CandidateJSON `json:"candidates"` // ICE candidates
}

// ConnectionStateJSON represents the current connection state.
type ConnectionStateJSON struct {
	ICEState       string `json:"iceState"`       // "new", "checking", "connected", "completed", "failed", "disconnected", "closed"
	TunnelState    string `json:"tunnelState"`    // "inactive", "starting", "active", "error"
	IsConnected    bool   `json:"isConnected"`    // True if ICE connection is established
	IsTunnelActive bool   `json:"isTunnelActive"` // True if WireGuard tunnel is active
	LocalIP        string `json:"localIP"`        // Our virtual IP (e.g., "10.0.0.2")
	PeerIP         string `json:"peerIP"`         // Peer's virtual IP (e.g., "10.0.0.1")
	ErrorMessage   string `json:"errorMessage,omitempty"`
}

// HTTPResultJSON represents the result of an HTTP request through the tunnel.
type HTTPResultJSON struct {
	Success    bool              `json:"success"`              // True if request succeeded
	StatusCode int               `json:"statusCode,omitempty"` // HTTP status code
	Body       string            `json:"body,omitempty"`       // Response body
	Headers    map[string]string `json:"headers,omitempty"`    // Response headers
	Error      string            `json:"error,omitempty"`      // Error message if failed
	LatencyMs  int64             `json:"latencyMs"`            // Request latency in milliseconds
}

// TunnelStatsJSON represents tunnel statistics.
type TunnelStatsJSON struct {
	BytesSent       uint64 `json:"bytesSent"`       // Total bytes sent
	BytesReceived   uint64 `json:"bytesReceived"`   // Total bytes received
	PacketsSent     uint64 `json:"packetsSent"`     // Total packets sent
	PacketsReceived uint64 `json:"packetsReceived"` // Total packets received
	LastHandshake   int64  `json:"lastHandshake"`   // Unix timestamp of last WireGuard handshake
	IsActive        bool   `json:"isActive"`        // True if tunnel is active
}

// EventJSON represents an event emitted by the client.
type EventJSON struct {
	Type    string `json:"type"`              // Event type: "candidate", "state_change", "error", "connected", "disconnected"
	Data    string `json:"data,omitempty"`    // Event-specific data (JSON string)
	Message string `json:"message,omitempty"` // Human-readable message
}

// Event types
const (
	EventTypeCandidate    = "candidate"
	EventTypeStateChange  = "state_change"
	EventTypeError        = "error"
	EventTypeConnected    = "connected"
	EventTypeDisconnected = "disconnected"
	EventTypeTunnelUp     = "tunnel_up"
	EventTypeTunnelDown   = "tunnel_down"
)

// ICE state values
const (
	ICEStateNew          = "new"
	ICEStateChecking     = "checking"
	ICEStateConnected    = "connected"
	ICEStateCompleted    = "completed"
	ICEStateFailed       = "failed"
	ICEStateDisconnected = "disconnected"
	ICEStateClosed       = "closed"
)

// Tunnel state values
const (
	TunnelStateInactive = "inactive"
	TunnelStateStarting = "starting"
	TunnelStateActive   = "active"
	TunnelStateError    = "error"
)
