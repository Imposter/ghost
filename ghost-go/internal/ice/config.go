package ice

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Default timeout and interval values for ICE operations.
const (
	// DefaultGatherTimeout is the default maximum time to wait for candidate gathering.
	// 10 seconds is typically sufficient for gathering host, srflx, and relay candidates.
	DefaultGatherTimeout = 10 * time.Second

	// DefaultConnectionTimeout is the default maximum time to wait for ICE connection establishment.
	// 30 seconds allows for multiple connectivity check rounds per RFC 8445.
	DefaultConnectionTimeout = 30 * time.Second

	// DefaultKeepaliveInterval is the default interval between keepalive packets.
	// 2 seconds provides fast disconnect detection while being reasonable for bandwidth.
	DefaultKeepaliveInterval = 2 * time.Second

	// DefaultDisconnectedTimeout is the time after which a connection is considered disconnected
	// if no packets are received. 5 seconds provides fast detection.
	DefaultDisconnectedTimeout = 5 * time.Second

	// DefaultFailedTimeout is the time after which a disconnected connection is considered failed.
	// 15 seconds allows time for recovery attempts.
	DefaultFailedTimeout = 15 * time.Second

	// DefaultSTUNPort is the standard STUN server port (RFC 5389).
	DefaultSTUNPort = 19302

	// DefaultTURNPort is the standard TURN server port (RFC 5766).
	DefaultTURNPort = 3478
)

// CandidateType represents the type of ICE candidate.
type CandidateType string

const (
	// CandidateTypeHost represents a host candidate (local address).
	CandidateTypeHost CandidateType = "host"
	// CandidateTypeSrflx represents a server reflexive candidate (from STUN).
	CandidateTypeSrflx CandidateType = "srflx"
	// CandidateTypePrflx represents a peer reflexive candidate.
	CandidateTypePrflx CandidateType = "prflx"
	// CandidateTypeRelay represents a relay candidate (from TURN).
	CandidateTypeRelay CandidateType = "relay"
)

// TURNServer holds TURN server configuration.
type TURNServer struct {
	// URLs for the TURN server (e.g., "turn:turn.example.com:3478")
	URLs []string
	// Username for TURN authentication
	Username string
	// Password for TURN authentication
	Password string
}

// ICEConfig holds ICE-specific settings.
type ICEConfig struct {
	// STUNServers is a list of STUN server URLs for NAT traversal.
	// Example: ["stun:stun.l.google.com:19302"]
	STUNServers []string

	// TURNServers is a list of TURN server configurations for relay.
	TURNServers []TURNServer

	// GatherTimeout is the maximum time to wait for candidate gathering.
	// Default: 10 seconds
	GatherTimeout time.Duration

	// ConnectionTimeout is the maximum time to wait for connection establishment.
	// Default: 30 seconds
	ConnectionTimeout time.Duration

	// KeepaliveInterval is the interval between keepalive packets.
	// Default: 2 seconds
	KeepaliveInterval time.Duration

	// DisconnectedTimeout is the time to wait before transitioning to disconnected state
	// after no packets are received. Default: 5 seconds
	DisconnectedTimeout time.Duration

	// FailedTimeout is the time to wait before transitioning from disconnected to failed.
	// Default: 15 seconds
	FailedTimeout time.Duration

	// InterfaceFilter is a list of network interface names to use.
	// If empty, all interfaces are used.
	InterfaceFilter []string

	// CandidateTypes specifies which candidate types to gather.
	// If empty, all types are gathered.
	CandidateTypes []CandidateType

	// PortMin is the minimum port number for ICE UDP sockets.
	// If 0, the OS will choose ports automatically.
	// Set both PortMin and PortMax to the same value for a fixed port.
	PortMin uint16

	// PortMax is the maximum port number for ICE UDP sockets.
	// If 0, the OS will choose ports automatically.
	// Set both PortMin and PortMax to the same value for a fixed port.
	PortMax uint16
}

// DefaultICEConfig returns a configuration with sensible defaults.
func DefaultICEConfig() *ICEConfig {
	return &ICEConfig{
		STUNServers:         []string{"stun:stun.l.google.com:19302"},
		TURNServers:         []TURNServer{},
		GatherTimeout:       DefaultGatherTimeout,
		ConnectionTimeout:   DefaultConnectionTimeout,
		KeepaliveInterval:   DefaultKeepaliveInterval,
		DisconnectedTimeout: DefaultDisconnectedTimeout,
		FailedTimeout:       DefaultFailedTimeout,
		InterfaceFilter:     []string{},
		CandidateTypes: []CandidateType{
			CandidateTypeHost,
			CandidateTypeSrflx,
			CandidateTypeRelay,
		},
	}
}

// Validate checks if the configuration is valid.
func (c *ICEConfig) Validate() error {
	if c == nil {
		return errors.New("config cannot be nil")
	}

	// Validate STUN servers
	for _, stun := range c.STUNServers {
		if err := validateServerURL(stun, "stun"); err != nil {
			return fmt.Errorf("invalid STUN server: %w", err)
		}
	}

	// Validate TURN servers
	for i, turn := range c.TURNServers {
		if len(turn.URLs) == 0 {
			return fmt.Errorf("TURN server %d: no URLs specified", i)
		}
		for _, u := range turn.URLs {
			if err := validateServerURL(u, "turn"); err != nil {
				return fmt.Errorf("TURN server %d: invalid URL: %w", i, err)
			}
		}
		if turn.Username == "" {
			return fmt.Errorf("TURN server %d: username is required", i)
		}
		if turn.Password == "" {
			return fmt.Errorf("TURN server %d: password is required", i)
		}
	}

	// Validate timeouts
	if c.GatherTimeout <= 0 {
		return errors.New("gather timeout must be positive")
	}
	if c.ConnectionTimeout <= 0 {
		return errors.New("connection timeout must be positive")
	}
	if c.KeepaliveInterval <= 0 {
		return errors.New("keepalive interval must be positive")
	}

	// Validate port range
	if c.PortMin > 0 && c.PortMax > 0 {
		if c.PortMin > c.PortMax {
			return errors.New("port min cannot be greater than port max")
		}
		if c.PortMin < 1024 {
			return errors.New("port min must be >= 1024 (non-privileged ports)")
		}
	}

	return nil
}

// validateServerURL validates a STUN or TURN server URL.
// Format: scheme:host:port (e.g., stun:stun.l.google.com:19302)
func validateServerURL(serverURL, scheme string) error {
	if serverURL == "" {
		return errors.New("server URL cannot be empty")
	}

	// STUN/TURN URLs use format "scheme:host:port" not "scheme://host:port"
	// So we normalize it for url.Parse by adding //
	normalizedURL := serverURL
	if len(serverURL) > len(scheme)+1 && serverURL[:len(scheme)] == scheme && serverURL[len(scheme)] == ':' {
		// Replace "scheme:" with "scheme://"
		normalizedURL = scheme + "://" + serverURL[len(scheme)+1:]
	}

	u, err := url.Parse(normalizedURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if u.Scheme != scheme {
		return fmt.Errorf("invalid scheme: expected %s, got %s", scheme, u.Scheme)
	}

	if u.Host == "" {
		return errors.New("host cannot be empty")
	}

	return nil
}
