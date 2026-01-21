package wireguard

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// Default WireGuard configuration values.
const (
	// DefaultMTU is the default maximum transmission unit.
	// 1280 bytes is the minimum IPv6 MTU and works safely across most networks
	// including those with tunneling overhead.
	DefaultMTU = 1280

	// MinMTU is the minimum allowed MTU value.
	// 576 is the minimum practical MTU for IP networks.
	MinMTU = 576

	// MaxMTU is the maximum allowed MTU value.
	// Limited by IP packet size constraints.
	MaxMTU = 65535

	// DefaultPersistentKeepalive is the default keepalive interval.
	// 25 seconds keeps NAT bindings alive while being bandwidth-efficient.
	DefaultPersistentKeepalive = 25 * time.Second

	// MinPort is the minimum valid port number (1-65535).
	MinPort = 1

	// MaxPort is the maximum valid port number (1-65535).
	MaxPort = 65535
)

// WireGuardConfig holds WireGuard-specific settings.
type WireGuardConfig struct {
	// PrivateKey is the WireGuard private key (32 bytes).
	PrivateKey []byte

	// ListenPort is the port to listen on (0 = auto-assign).
	ListenPort int

	// MTU is the maximum transmission unit.
	// Default: 1280 (safe for most networks)
	MTU int

	// PersistentKeepalive is the interval for sending keepalive packets.
	// Default: 25 seconds
	PersistentKeepalive time.Duration
}

// PeerConfig holds configuration for a WireGuard peer.
type PeerConfig struct {
	// PublicKey is the peer's WireGuard public key (32 bytes).
	PublicKey []byte

	// AllowedIPs is the list of IP addresses/networks allowed for this peer.
	// Example: ["10.0.0.2/32", "fd00::2/128"]
	AllowedIPs []string

	// Endpoint is the peer's endpoint address (optional for incoming connections).
	// Format: "host:port"
	Endpoint string

	// PersistentKeepalive is the keepalive interval for this peer.
	// If zero, uses the device's default.
	PersistentKeepalive time.Duration
}

// DefaultWireGuardConfig returns a configuration with sensible defaults.
func DefaultWireGuardConfig() *WireGuardConfig {
	return &WireGuardConfig{
		ListenPort:          0,          // Auto-assign
		MTU:                 DefaultMTU, // Safe default
		PersistentKeepalive: DefaultPersistentKeepalive,
	}
}

// Validate checks if the configuration is valid.
func (c *WireGuardConfig) Validate() error {
	if c == nil {
		return errors.New("config cannot be nil")
	}

	if len(c.PrivateKey) != 32 {
		return fmt.Errorf("private key must be 32 bytes, got %d", len(c.PrivateKey))
	}

	if c.ListenPort < 0 || c.ListenPort > MaxPort {
		return fmt.Errorf("invalid listen port: %d", c.ListenPort)
	}

	if c.MTU <= 0 {
		return fmt.Errorf("MTU must be positive, got %d", c.MTU)
	}

	if c.MTU < MinMTU {
		return fmt.Errorf("MTU too small (minimum %d), got %d", MinMTU, c.MTU)
	}

	if c.MTU > MaxMTU {
		return fmt.Errorf("MTU too large (maximum %d), got %d", MaxMTU, c.MTU)
	}

	if c.PersistentKeepalive < 0 {
		return errors.New("persistent keepalive cannot be negative")
	}

	return nil
}

// Validate checks if the peer configuration is valid.
func (p *PeerConfig) Validate() error {
	if p == nil {
		return errors.New("peer config cannot be nil")
	}

	if len(p.PublicKey) != 32 {
		return fmt.Errorf("public key must be 32 bytes, got %d", len(p.PublicKey))
	}

	if len(p.AllowedIPs) == 0 {
		return errors.New("at least one allowed IP must be specified")
	}

	// Validate each allowed IP
	for i, ipStr := range p.AllowedIPs {
		_, _, err := net.ParseCIDR(ipStr)
		if err != nil {
			return fmt.Errorf("invalid allowed IP %d (%s): %w", i, ipStr, err)
		}
	}

	// Validate endpoint if provided
	if p.Endpoint != "" {
		host, port, err := net.SplitHostPort(p.Endpoint)
		if err != nil {
			return fmt.Errorf("invalid endpoint format: %w", err)
		}
		if host == "" {
			return errors.New("endpoint host cannot be empty")
		}
		if port == "" {
			return errors.New("endpoint port cannot be empty")
		}
	}

	if p.PersistentKeepalive < 0 {
		return errors.New("persistent keepalive cannot be negative")
	}

	return nil
}
