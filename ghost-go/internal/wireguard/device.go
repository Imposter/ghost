package wireguard

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// WireGuard IPC configuration field names
const (
	// IPCFieldPrivateKey is the private key field name
	IPCFieldPrivateKey = "private_key"
	// IPCFieldPublicKey is the public key field name
	IPCFieldPublicKey = "public_key"
	// IPCFieldEndpoint is the endpoint field name
	IPCFieldEndpoint = "endpoint"
	// IPCFieldAllowedIP is the allowed IP field name
	IPCFieldAllowedIP = "allowed_ip"
	// IPCFieldPersistentKeepalive is the persistent keepalive interval field name
	IPCFieldPersistentKeepalive = "persistent_keepalive_interval"
	// IPCFieldRemove is the remove peer field name
	IPCFieldRemove = "remove"
)

// Device wraps a WireGuard device with lifecycle management.
type Device struct {
	device *device.Device
	tun    tun.Device
	bind   conn.Bind
	config *WireGuardConfig
	logger *slog.Logger

	mu     sync.RWMutex
	peers  map[string]*PeerConfig // keyed by base64 public key
	isUp   bool
	closed bool
}

// NewDevice creates a new WireGuard device.
// The TUN device and bind must be provided by the caller.
func NewDevice(tunDev tun.Device, bind conn.Bind, config *WireGuardConfig, logger *slog.Logger) (*Device, error) {
	if tunDev == nil {
		return nil, fmt.Errorf("TUN device cannot be nil")
	}

	if bind == nil {
		return nil, fmt.Errorf("bind cannot be nil")
	}

	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Create device logger
	// WireGuard expects a specific logger format
	deviceLogger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			logger.Debug(fmt.Sprintf(format, args...))
		},
		Errorf: func(format string, args ...any) {
			logger.Error(fmt.Sprintf(format, args...))
		},
	}

	// Create WireGuard device
	wgDevice := device.NewDevice(tunDev, bind, deviceLogger)

	logger.Info("WireGuard device created", "mtu", config.MTU)

	return &Device{
		device: wgDevice,
		tun:    tunDev,
		bind:   bind,
		config: config,
		logger: logger,
		peers:  make(map[string]*PeerConfig),
	}, nil
}

// Configure configures the WireGuard device with a private key and initial settings.
func (d *Device) Configure(privateKey []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if err := ValidatePrivateKey(privateKey); err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}

	// WireGuard IPC protocol uses HEX encoding for keys, not base64!
	// 32 bytes → 64 hex characters
	privateKeyHex := hex.EncodeToString(privateKey)

	// Build IPC configuration using constants
	config := fmt.Sprintf("%s=%s\n", IPCFieldPrivateKey, privateKeyHex)

	if d.config.ListenPort > 0 {
		config += fmt.Sprintf("listen_port=%d\n", d.config.ListenPort)
	}

	// Apply configuration via IPC
	if err := d.device.IpcSet(config); err != nil {
		return fmt.Errorf("failed to configure device: %w", err)
	}

	d.logger.Info("Device configured with private key")
	return nil
}

// AddPeer adds a peer to the WireGuard device.
func (d *Device) AddPeer(peerConfig *PeerConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if err := peerConfig.Validate(); err != nil {
		return fmt.Errorf("invalid peer config: %w", err)
	}

	publicKeyStr := EncodeKey(peerConfig.PublicKey)

	// Check if peer already exists
	if _, exists := d.peers[publicKeyStr]; exists {
		return fmt.Errorf("peer already exists: %s", publicKeyStr)
	}

	// WireGuard IPC protocol uses HEX encoding for keys!
	publicKeyHex := hex.EncodeToString(peerConfig.PublicKey)

	// Build peer configuration using IPC field constants
	config := fmt.Sprintf("%s=%s\n", IPCFieldPublicKey, publicKeyHex)

	// Add endpoint if provided
	if peerConfig.Endpoint != "" {
		config += fmt.Sprintf("%s=%s\n", IPCFieldEndpoint, peerConfig.Endpoint)
	}

	// Add allowed IPs
	for _, allowedIP := range peerConfig.AllowedIPs {
		config += fmt.Sprintf("%s=%s\n", IPCFieldAllowedIP, allowedIP)
	}

	// Set persistent keepalive
	keepalive := peerConfig.PersistentKeepalive
	if keepalive == 0 {
		keepalive = d.config.PersistentKeepalive
	}
	if keepalive > 0 {
		config += fmt.Sprintf("%s=%d\n", IPCFieldPersistentKeepalive, int(keepalive.Seconds()))
	}

	// Apply peer configuration via IPC
	if err := d.device.IpcSet(config); err != nil {
		return fmt.Errorf("failed to add peer: %w", err)
	}

	// Store peer config
	d.peers[publicKeyStr] = peerConfig

	d.logger.Info("Peer added",
		"public_key", publicKeyStr[:16]+"...",
		"allowed_ips", len(peerConfig.AllowedIPs),
	)

	return nil
}

// RemovePeer removes a peer from the WireGuard device.
func (d *Device) RemovePeer(publicKey []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if err := ValidatePublicKey(publicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	publicKeyStr := EncodeKey(publicKey)

	// Check if peer exists
	if _, exists := d.peers[publicKeyStr]; !exists {
		return ErrPeerNotFound
	}

	// WireGuard IPC protocol uses HEX encoding for keys!
	publicKeyHex := hex.EncodeToString(publicKey)

	// Build remove configuration
	config := fmt.Sprintf("public_key=%s\nremove=true\n", publicKeyHex)

	// Apply configuration via IPC
	if err := d.device.IpcSet(config); err != nil {
		return fmt.Errorf("failed to remove peer: %w", err)
	}

	// Remove from local tracking
	delete(d.peers, publicKeyStr)

	d.logger.Info("Peer removed", "public_key", publicKeyStr[:16]+"...")
	return nil
}

// Up brings the WireGuard device up.
func (d *Device) Up() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if d.isUp {
		return nil
	}

	// Bring up the device
	if err := d.device.Up(); err != nil {
		return fmt.Errorf("failed to bring device up: %w", err)
	}
	d.isUp = true

	d.logger.Info("Device is up")
	return nil
}

// Down brings the WireGuard device down.
func (d *Device) Down() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if !d.isUp {
		return nil
	}

	// Bring down the device
	if err := d.device.Down(); err != nil {
		return fmt.Errorf("failed to bring device down: %w", err)
	}
	d.isUp = false

	d.logger.Info("Device is down")
	return nil
}

// IsUp returns whether the device is up.
func (d *Device) IsUp() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.isUp
}

// GetPeers returns a list of configured peers.
func (d *Device) GetPeers() []*PeerConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peers := make([]*PeerConfig, 0, len(d.peers))
	for _, peer := range d.peers {
		peers = append(peers, peer)
	}
	return peers
}

// GetStatus returns the current device status from WireGuard.
func (d *Device) GetStatus() (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return "", ErrDeviceClosed
	}

	// Get status via IPC
	status, err := d.device.IpcGet()
	if err != nil {
		return "", fmt.Errorf("failed to get device status: %w", err)
	}

	return status, nil
}

// SetMTU sets the MTU for the TUN device.
func (d *Device) SetMTU(mtu int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if mtu <= 0 {
		return fmt.Errorf("invalid MTU: %d", mtu)
	}

	if mtu < 576 {
		return fmt.Errorf("MTU too small (minimum 576): %d", mtu)
	}

	// Update MTU in config
	d.config.MTU = mtu
	d.logger.Info("MTU updated", "mtu", mtu)

	return nil
}

// GetMTU returns the current MTU.
func (d *Device) GetMTU() (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return 0, ErrDeviceClosed
	}

	mtu, err := d.tun.MTU()
	if err != nil {
		return 0, fmt.Errorf("failed to get MTU: %w", err)
	}

	return mtu, nil
}

// GetTUNName returns the name of the TUN device.
func (d *Device) GetTUNName() (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return "", ErrDeviceClosed
	}

	name, err := d.tun.Name()
	if err != nil {
		return "", fmt.Errorf("failed to get TUN name: %w", err)
	}

	return name, nil
}

// UpdatePeerEndpoint updates the endpoint for an existing peer.
// This is useful when the peer's address changes (e.g., after NAT rebinding).
func (d *Device) UpdatePeerEndpoint(publicKey []byte, endpoint string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return ErrDeviceClosed
	}

	if err := ValidatePublicKey(publicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	publicKeyStr := EncodeKey(publicKey)

	// Check if peer exists
	peer, exists := d.peers[publicKeyStr]
	if !exists {
		return ErrPeerNotFound
	}

	// Validate endpoint
	if endpoint == "" {
		return ErrInvalidEndpoint
	}

	// WireGuard IPC protocol uses HEX encoding for keys!
	publicKeyHex := hex.EncodeToString(publicKey)

	// Build IPC command to update endpoint using constants
	config := fmt.Sprintf("%s=%s\n%s=%s\n", IPCFieldPublicKey, publicKeyHex, IPCFieldEndpoint, endpoint)

	// Apply configuration via IPC
	if err := d.device.IpcSet(config); err != nil {
		return fmt.Errorf("failed to update peer endpoint: %w", err)
	}

	// Update local tracking
	peer.Endpoint = endpoint

	d.logger.Info("Peer endpoint updated",
		"public_key", publicKeyStr[:16]+"...",
		"endpoint", endpoint,
	)

	return nil
}

// Close closes the WireGuard device and releases all resources.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	d.logger.Info("Closing WireGuard device")

	d.closed = true

	// Close the WireGuard device
	d.device.Close()

	// Close the bind
	if err := d.bind.Close(); err != nil {
		d.logger.Warn("Error closing bind", "error", err)
	}

	// Close the TUN device
	if err := d.tun.Close(); err != nil {
		d.logger.Warn("Error closing TUN device", "error", err)
	}

	d.logger.Info("WireGuard device closed")
	return nil
}

// parseAllowedIP parses an allowed IP string and returns the prefix.
func parseAllowedIP(allowedIP string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(allowedIP)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid allowed IP format: %w", err)
	}
	return prefix, nil
}
