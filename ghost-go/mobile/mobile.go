package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"ghost-go/internal/ice"
	"ghost-go/internal/wireguard"
)

// GhostClient is the main entry point for mobile applications.
// It provides a simplified, JSON-based API for establishing encrypted P2P tunnels.
//
// # Thread Safety
//
// All methods are thread-safe and can be called from any goroutine.
//
// # Resource Management
//
// GhostClient manages several resources that require cleanup:
//   - ICE agent (network sockets, goroutines)
//   - ICE connection (network socket)
//   - WireGuard device (goroutines, TUN device)
//   - HTTP connection pool
//
// IMPORTANT: You MUST call Close() when done with the client to prevent resource leaks.
// Failure to call Close() will leak goroutines and network sockets.
//
// The client is designed to be safe against multiple calls to resource-allocating methods:
//   - StartGathering() cleans up any previous ICE agent before creating a new one
//   - Connect() cleans up any previous connection before creating a new one
//   - StartTunnel() cleans up any previous tunnel before creating a new one
//
// # Lifecycle
//
//  1. Create client with NewClient()
//  2. Generate WireGuard keys with GenerateWireGuardKey()
//  3. Start ICE gathering with StartGathering()
//  4. Exchange signaling data with peer (via QR code, etc.)
//  5. Set remote credentials and candidates
//  6. Connect with Connect()
//  7. Start tunnel with StartTunnel()
//  8. Use HTTPGet/HTTPPost for communication
//  9. Close with Close() - REQUIRED to prevent resource leaks
//
// # Example
//
//	client, err := NewClient("")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close() // Always close to prevent resource leaks
//
//	// ... use client ...
type GhostClient struct {
	mu sync.RWMutex

	// Configuration
	stunServers []string
	portMin     uint16 // Minimum UDP port for ICE (0 = OS chooses)
	portMax     uint16 // Maximum UDP port for ICE (0 = OS chooses)
	logger      *slog.Logger

	// ICE components
	iceAgent ice.Agent
	iceConn  net.Conn
	iceBind  *ice.ICEBind

	// WireGuard components
	privateKey []byte
	publicKey  []byte
	peerKey    []byte
	device     *wireguard.Device
	tunNet     *wireguard.Net

	// State tracking
	localIP     string
	peerIP      string
	candidates  []*ice.Candidate
	iceState    string
	tunnelState string
	closed      bool

	// Gathering context (for cancellation)
	gatherCtx    context.Context
	gatherCancel context.CancelFunc

	// Event handling
	dispatcher *eventDispatcher

	// Connection pool
	connPool *connPool

	// Shared HTTP client for tunnel requests (created lazily when tunnel starts)
	httpClient *http.Client
}

// NewClient creates a new GhostClient.
//
// Parameters:
//   - stunServers: Comma-separated list of STUN server URLs (e.g., "stun:stun.l.google.com:19302")
//     If empty, uses Google's public STUN server.
//
// Returns the client and an error if initialization fails.
func NewClient(stunServers string) (*GhostClient, error) {
	servers := []string{"stun:stun.l.google.com:19302"}
	if stunServers != "" {
		servers = strings.Split(stunServers, ",")
		for i := range servers {
			servers[i] = strings.TrimSpace(servers[i])
		}
	}

	logger := newMobileLogger()
	logger.Info("Creating GhostClient", "stun_servers", stunServers)

	// Validate STUN servers by creating a test config
	iceConfig := &ice.ICEConfig{
		STUNServers:       servers,
		GatherTimeout:     15 * time.Second,
		ConnectionTimeout: 30 * time.Second,
		KeepaliveInterval: 15 * time.Second,
	}
	if err := iceConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid STUN servers: %w", err)
	}

	client := &GhostClient{
		stunServers: servers,
		logger:      logger,
		dispatcher:  newEventDispatcher(),
		connPool:    newConnPool(),
		iceState:    ICEStateNew,
		tunnelState: TunnelStateInactive,
	}

	logger.Info("GhostClient created", "stun_servers", len(servers))
	return client, nil
}

// SetEventCallback sets the callback for receiving events.
// This should be called before starting any operations.
func (c *GhostClient) SetEventCallback(callback EventCallback) {
	c.dispatcher.setCallback(callback)
}

// SetPortRange sets the UDP port range for ICE.
// This should be called before StartGathering().
// Set both min and max to the same value for a fixed port.
// Set both to 0 to let the OS choose ports (default).
func (c *GhostClient) SetPortRange(min, max uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.portMin = min
	c.portMax = max
	c.logger.Info("Port range set", "min", min, "max", max)
}

// Close releases all resources held by the client.
// This should always be called when done with the client.
//
// Cleanup order follows the ownership pattern:
//  1. Close HTTP connection pool
//  2. Bring down and close WireGuard device (stops bind's receive loop)
//  3. Close the ICE connection (we created it, we close it)
//  4. Close the ICE agent
func (c *GhostClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	c.logger.Info("Closing GhostClient")

	// Cancel any in-progress gathering
	if c.gatherCancel != nil {
		c.gatherCancel()
		c.gatherCancel = nil
		c.gatherCtx = nil
	}

	// Close HTTP connections first
	c.connPool.closeAll()

	// Close WireGuard device (stops bind's receive loop, but doesn't close ICE conn)
	if c.device != nil {
		c.device.Down()
		c.device.Close()
		c.device = nil
	}

	// Close the ICE connection (ownership pattern: we created it, we close it)
	if c.iceConn != nil {
		if err := c.iceConn.Close(); err != nil {
			c.logger.Warn("Error closing ICE connection", "error", err)
		}
		c.iceConn = nil
	}

	// Close ICE agent
	if c.iceAgent != nil {
		c.iceAgent.Close()
		c.iceAgent = nil
	}

	c.tunnelState = TunnelStateInactive
	c.iceState = ICEStateClosed

	// Emit state change before disconnect so UI updates immediately
	c.dispatcher.emitStateChange(&ConnectionStateJSON{
		ICEState:       c.iceState,
		TunnelState:    c.tunnelState,
		IsConnected:    false,
		IsTunnelActive: false,
		LocalIP:        c.localIP,
		PeerIP:         c.peerIP,
	})
	c.dispatcher.emitDisconnected("Client closed")

	return nil
}

// cleanupConnectionResources cleans up resources when connection is lost.
// Must be called with the lock held.
// This preserves the ICE agent (for state change callbacks) but cleans up
// the device, bind, connection, and HTTP client which are no longer usable.
func (c *GhostClient) cleanupConnectionResources() {
	c.logger.Info("Cleaning up connection resources due to disconnect/failure")

	// Close HTTP client's transport to release any keep-alive connections
	if c.httpClient != nil {
		if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		c.httpClient = nil
	}

	// Close WireGuard device first (stops bind's receive loop)
	if c.device != nil {
		c.device.Down()
		c.device.Close()
		c.device = nil
	}
	c.tunNet = nil
	c.iceBind = nil

	// Close the ICE connection - it's no longer usable
	if c.iceConn != nil {
		c.iceConn.Close()
		c.iceConn = nil
	}

	// Note: We don't close the ICE agent here because its state change
	// callback is still registered and useful for detecting further state changes.
	// The agent will be cleaned up on Close() or when StartGathering() is called again.
}

// GenerateWireGuardKey generates a new WireGuard key pair.
// Returns JSON with privateKey and publicKey (both base64-encoded).
func (c *GhostClient) GenerateWireGuardKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return toErrorJSON(ErrClientClosed)
	}

	privateKey, err := wireguard.GeneratePrivateKey()
	if err != nil {
		return toErrorJSON(err)
	}

	publicKey, err := wireguard.GetPublicKey(privateKey)
	if err != nil {
		return toErrorJSON(err)
	}

	c.privateKey = privateKey
	c.publicKey = publicKey

	result := &KeyPairJSON{
		PrivateKey: wireguard.EncodeKey(privateKey),
		PublicKey:  wireguard.EncodeKey(publicKey),
	}

	return toJSON(result)
}

// GetPublicKey returns the local WireGuard public key (base64-encoded).
// Returns empty string if keys haven't been generated.
func (c *GhostClient) GetPublicKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.publicKey == nil {
		return ""
	}
	return wireguard.EncodeKey(c.publicKey)
}

// StartGathering starts ICE candidate gathering.
// Candidates will be emitted via the EventCallback as they are discovered.
// Returns an error string if gathering fails to start, empty string on success.
//
// # Resource Management
//
// This method allocates the following resources:
//   - ICE agent (network sockets, goroutines for candidate gathering)
//
// If called multiple times, any existing ICE agent is cleaned up before creating
// a new one. This makes it safe to restart gathering without manually calling Close().
//
// All resources are automatically cleaned up when Close() is called.
func (c *GhostClient) StartGathering() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return toErrorJSON(ErrClientClosed)
	}

	// Cancel any in-progress gathering
	if c.gatherCancel != nil {
		c.logger.Info("Cancelling previous gathering")
		c.gatherCancel()
		c.gatherCancel = nil
		c.gatherCtx = nil
	}

	// Clean up any existing ICE agent before creating a new one
	// This prevents resource leaks when StartGathering is called multiple times
	if c.iceAgent != nil {
		c.logger.Info("Cleaning up previous ICE agent before starting new gathering")
		c.iceAgent.Close()
		c.iceAgent = nil
		c.candidates = nil
	}

	// Create ICE config with fast disconnect detection
	iceConfig := &ice.ICEConfig{
		STUNServers:         c.stunServers,
		GatherTimeout:       15 * time.Second,
		ConnectionTimeout:   30 * time.Second,
		KeepaliveInterval:   2 * time.Second,  // Send keepalives every 2s
		DisconnectedTimeout: 5 * time.Second,  // Detect disconnect in ~5s
		FailedTimeout:       15 * time.Second, // Transition to failed after 15s
		PortMin:             c.portMin,
		PortMax:             c.portMax,
	}

	// Create ICE agent
	agent, err := ice.NewAgent(iceConfig, c.logger)
	if err != nil {
		return toErrorJSON(fmt.Errorf("failed to create ICE agent: %w", err))
	}
	c.iceAgent = agent
	c.iceState = ICEStateChecking

	// Register ICE connection state change callback to detect disconnections
	agent.OnConnectionStateChange(func(state ice.ConnectionState) {
		c.mu.Lock()
		defer c.mu.Unlock()

		// Map ICE connection state to our state
		switch state {
		case ice.ConnectionStateConnected:
			c.iceState = ICEStateConnected
		case ice.ConnectionStateCompleted:
			c.iceState = ICEStateCompleted
		case ice.ConnectionStateFailed:
			c.iceState = ICEStateFailed
			c.tunnelState = TunnelStateInactive
			// Clean up resources since connection is no longer usable
			c.cleanupConnectionResources()
			c.dispatcher.emitError(fmt.Errorf("ICE connection failed"))
			c.dispatcher.emitStateChange(&ConnectionStateJSON{
				ICEState:       c.iceState,
				TunnelState:    c.tunnelState,
				IsConnected:    false,
				IsTunnelActive: false,
				LocalIP:        c.localIP,
				PeerIP:         c.peerIP,
				ErrorMessage:   "ICE connection failed",
			})
		case ice.ConnectionStateDisconnected:
			c.iceState = ICEStateDisconnected
			c.tunnelState = TunnelStateInactive
			// Clean up resources since connection is no longer usable
			c.cleanupConnectionResources()
			c.dispatcher.emitTunnelDown()
			c.dispatcher.emitStateChange(&ConnectionStateJSON{
				ICEState:       c.iceState,
				TunnelState:    c.tunnelState,
				IsConnected:    false,
				IsTunnelActive: false,
				LocalIP:        c.localIP,
				PeerIP:         c.peerIP,
			})
			c.dispatcher.emitDisconnected("Peer disconnected")
		case ice.ConnectionStateClosed:
			c.iceState = ICEStateClosed
		}
	})

	// Create cancellable context for gathering
	c.gatherCtx, c.gatherCancel = context.WithCancel(context.Background())

	// Start gathering in background
	candChan, err := agent.GatherCandidates(c.gatherCtx)
	if err != nil {
		c.gatherCancel()
		c.gatherCancel = nil
		c.gatherCtx = nil
		return toErrorJSON(fmt.Errorf("failed to start gathering: %w", err))
	}

	// Process candidates asynchronously
	go func() {
		for cand := range candChan {
			c.mu.Lock()
			c.candidates = append(c.candidates, cand)
			count := len(c.candidates)
			c.mu.Unlock()

			// Log with count captured under lock to avoid race
			c.logger.Debug("Candidate gathered", "type", cand.Type, "total", count)

			// Emit candidate event
			candJSON := &CandidateJSON{
				Type:       string(cand.Type),
				Address:    cand.Address,
				Port:       cand.Port,
				Protocol:   cand.Protocol,
				Priority:   cand.Priority,
				Foundation: cand.Foundation,
				RelAddr:    cand.RelatedAddress,
				RelPort:    cand.RelatedPort,
			}
			c.dispatcher.emitCandidate(candJSON)
		}

		// Get final count under lock to avoid race
		c.mu.RLock()
		count := len(c.candidates)
		c.mu.RUnlock()
		c.logger.Info("Candidate gathering complete", "count", count)
	}()

	return ""
}

// CancelGathering cancels any in-progress ICE candidate gathering.
// This should be called when the user cancels the connection flow or navigates away.
// After cancellation, StartGathering() can be called again to restart.
// Returns empty string on success.
func (c *GhostClient) CancelGathering() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return toErrorJSON(ErrClientClosed)
	}

	// Cancel the gathering context if active
	if c.gatherCancel != nil {
		c.logger.Info("Cancelling ICE gathering")
		c.gatherCancel()
		c.gatherCancel = nil
		c.gatherCtx = nil
	}

	// Clean up the ICE agent
	if c.iceAgent != nil {
		c.logger.Info("Closing ICE agent after gathering cancellation")
		c.iceAgent.Close()
		c.iceAgent = nil
		c.candidates = nil
	}

	// Reset state to allow fresh start
	c.iceState = ICEStateNew

	c.dispatcher.emitStateChange(&ConnectionStateJSON{
		ICEState:       c.iceState,
		TunnelState:    c.tunnelState,
		IsConnected:    false,
		IsTunnelActive: false,
		LocalIP:        c.localIP,
		PeerIP:         c.peerIP,
	})

	return ""
}

// GetLocalCredentials returns the local ICE credentials as JSON.
func (c *GhostClient) GetLocalCredentials() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.iceAgent == nil {
		return toErrorJSON(ErrGatheringNotStarted)
	}

	ufrag, pwd := c.iceAgent.LocalCredentials()
	result := &CredentialsJSON{
		Ufrag: ufrag,
		Pwd:   pwd,
	}
	return toJSON(result)
}

// GetLocalCandidatesJSON returns gathered candidates as a JSON array.
func (c *GhostClient) GetLocalCandidatesJSON() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	candidates := make([]CandidateJSON, len(c.candidates))
	for i, cand := range c.candidates {
		candidates[i] = CandidateJSON{
			Type:       string(cand.Type),
			Address:    cand.Address,
			Port:       cand.Port,
			Protocol:   cand.Protocol,
			Priority:   cand.Priority,
			Foundation: cand.Foundation,
			RelAddr:    cand.RelatedAddress,
			RelPort:    cand.RelatedPort,
		}
	}
	return toJSON(candidates)
}

// SetRemoteCredentials sets the remote ICE credentials.
// json should be a CredentialsJSON object: {"ufrag": "...", "pwd": "..."}
func (c *GhostClient) SetRemoteCredentials(jsonStr string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.iceAgent == nil {
		return toErrorJSON(ErrGatheringNotStarted)
	}

	var creds CredentialsJSON
	if err := json.Unmarshal([]byte(jsonStr), &creds); err != nil {
		return toErrorJSON(fmt.Errorf("%w: %v", ErrInvalidJSON, err))
	}

	if creds.Ufrag == "" || creds.Pwd == "" {
		return toErrorJSON(ErrInvalidCredentials)
	}

	if err := c.iceAgent.SetRemoteCredentials(creds.Ufrag, creds.Pwd); err != nil {
		return toErrorJSON(err)
	}

	return ""
}

// AddRemoteCandidate adds a remote ICE candidate.
// json should be a CandidateJSON object.
func (c *GhostClient) AddRemoteCandidate(jsonStr string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.iceAgent == nil {
		return toErrorJSON(ErrGatheringNotStarted)
	}

	var candJSON CandidateJSON
	if err := json.Unmarshal([]byte(jsonStr), &candJSON); err != nil {
		return toErrorJSON(fmt.Errorf("%w: %v", ErrInvalidJSON, err))
	}

	cand := &ice.Candidate{
		Type:           ice.CandidateType(candJSON.Type),
		Address:        candJSON.Address,
		Port:           candJSON.Port,
		Protocol:       candJSON.Protocol,
		Priority:       candJSON.Priority,
		Foundation:     candJSON.Foundation,
		RelatedAddress: candJSON.RelAddr,
		RelatedPort:    candJSON.RelPort,
	}

	if err := c.iceAgent.AddRemoteCandidate(cand); err != nil {
		return toErrorJSON(err)
	}

	return ""
}

// Connect establishes the ICE connection.
// isControlling: true for the initiating peer, false for the responding peer.
// Returns error string if connection fails, empty on success.
//
// # Resource Management
//
// This method allocates the following resources:
//   - ICE connection (network socket)
//
// If called multiple times, any existing connection and associated tunnel resources
// are cleaned up before creating a new connection. This makes it safe to reconnect
// without manually calling Close().
//
// The connection is automatically cleaned up when:
//   - Close() is called
//   - The peer disconnects (detected via ICE state change callbacks)
//   - Connect() is called again
//
// # Error: Agent Not Usable
//
// If the ICE agent has entered a failed or disconnected state (e.g., due to network
// loss), this method will return an error. You must call StartGathering() to create
// a new ICE agent before reconnecting.
func (c *GhostClient) Connect(isControlling bool) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.iceAgent == nil {
		return toErrorJSON(ErrGatheringNotStarted)
	}

	// Check if the ICE agent is in a usable state
	// Failed/Closed states are terminal - a new agent must be created via StartGathering()
	// Disconnected is temporary and the agent may recover, so we allow reconnection attempts
	if c.iceState == ICEStateFailed || c.iceState == ICEStateClosed {
		return toErrorJSON(fmt.Errorf("ICE agent is not usable (state: %s). Call StartGathering() to create a new agent", c.iceState))
	}

	// Clean up any existing connection before creating a new one
	// This also cleans up the tunnel since it depends on the connection
	if c.iceConn != nil {
		c.logger.Info("Cleaning up previous connection before establishing new one")
		c.cleanupConnectionResources()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := c.iceAgent.Connect(ctx, isControlling)
	if err != nil {
		c.iceState = ICEStateFailed
		c.dispatcher.emitError(err)
		return toErrorJSON(fmt.Errorf("ICE connection failed: %w", err))
	}

	c.iceConn = conn
	c.iceState = ICEStateConnected
	c.dispatcher.emitConnected("ICE connection established")

	// Emit state change so UI updates immediately
	c.dispatcher.emitStateChange(&ConnectionStateJSON{
		ICEState:       c.iceState,
		TunnelState:    c.tunnelState,
		IsConnected:    true,
		IsTunnelActive: false,
		LocalIP:        c.localIP,
		PeerIP:         c.peerIP,
	})

	return ""
}

// GetConnectionState returns the current connection state as JSON.
func (c *GhostClient) GetConnectionState() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state := &ConnectionStateJSON{
		ICEState:       c.iceState,
		TunnelState:    c.tunnelState,
		IsConnected:    c.iceState == ICEStateConnected || c.iceState == ICEStateCompleted,
		IsTunnelActive: c.tunnelState == TunnelStateActive,
		LocalIP:        c.localIP,
		PeerIP:         c.peerIP,
	}
	return toJSON(state)
}

// SetPeerPublicKey sets the peer's WireGuard public key.
// base64Key should be a base64-encoded 32-byte public key.
func (c *GhostClient) SetPeerPublicKey(base64Key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return toErrorJSON(ErrClientClosed)
	}

	peerKey, err := wireguard.DecodeKey(base64Key)
	if err != nil {
		return toErrorJSON(fmt.Errorf("%w: %v", ErrInvalidPublicKey, err))
	}

	if err := wireguard.ValidatePublicKey(peerKey); err != nil {
		return toErrorJSON(fmt.Errorf("%w: %v", ErrInvalidPublicKey, err))
	}

	c.peerKey = peerKey
	return ""
}

// SetLocalIP sets the local virtual IP address for the tunnel.
// cidr should be in CIDR notation (e.g., "10.0.0.2/24").
// If not called, defaults to MobileVirtualIP.
func (c *GhostClient) SetLocalIP(cidr string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return toErrorJSON(ErrClientClosed)
	}

	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return toErrorJSON(fmt.Errorf("invalid CIDR: %w", err))
	}

	c.localIP = prefix.Addr().String()
	return ""
}

// StartTunnel starts the WireGuard tunnel.
// Prerequisites: ICE connection must be established, keys must be set.
//
// # Resource Management
//
// This method allocates the following resources:
//   - WireGuard device (goroutines for packet processing)
//   - Userspace TUN device via netstack (goroutines, memory buffers)
//   - ICEBind adapter (goroutines for packet receiving)
//
// If called multiple times, any existing tunnel is cleaned up before creating
// a new one. This makes it safe to restart the tunnel without manually calling Close().
//
// The tunnel is automatically cleaned up when:
//   - Close() is called
//   - The peer disconnects (detected via ICE state change callbacks)
//   - StartTunnel() is called again
//   - An error occurs during tunnel creation (partial resources are cleaned up)
func (c *GhostClient) StartTunnel() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return toErrorJSON(ErrClientClosed)
	}

	if c.iceConn == nil {
		return toErrorJSON(ErrNotConnected)
	}

	if c.privateKey == nil || c.publicKey == nil {
		return toErrorJSON(fmt.Errorf("WireGuard keys not generated"))
	}

	if c.peerKey == nil {
		return toErrorJSON(fmt.Errorf("peer public key not set"))
	}

	// Clean up any existing tunnel before starting a new one
	if c.device != nil {
		c.logger.Info("Cleaning up previous tunnel before starting new one")
		c.device.Down()
		c.device.Close()
		c.device = nil
		c.tunNet = nil
		c.iceBind = nil
	}

	c.tunnelState = TunnelStateStarting

	// Default local IP if not set
	if c.localIP == "" {
		c.localIP = MobileVirtualIP
	}

	// Parse local IP
	localAddr, err := netip.ParseAddr(c.localIP)
	if err != nil {
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("invalid local IP: %w", err))
	}

	// Create userspace TUN device via netstack
	tunDev, tunNet, err := wireguard.CreateNetTUN(
		[]netip.Addr{localAddr},
		nil, // No DNS servers
		DefaultMTU,
	)
	if err != nil {
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("failed to create TUN: %w", err))
	}

	// Helper to clean up on error - prevents resource leaks
	cleanupOnError := func() {
		if tunDev != nil {
			tunDev.Close()
		}
		c.tunNet = nil
		c.iceBind = nil
	}

	c.tunNet = tunNet

	// Create ICEBind from ICE connection
	c.iceBind = ice.NewICEBind(c.iceConn, c.logger)
	if c.iceBind == nil {
		cleanupOnError()
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("ICE connection is no longer valid (peer may have disconnected)"))
	}

	// Create WireGuard config
	wgConfig := &wireguard.WireGuardConfig{
		PrivateKey:          c.privateKey,
		ListenPort:          0, // Auto-assign
		MTU:                 DefaultMTU,
		PersistentKeepalive: 25 * time.Second,
	}

	// Create WireGuard device
	device, err := wireguard.NewDevice(tunDev, c.iceBind, wgConfig, c.logger)
	if err != nil {
		cleanupOnError()
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("failed to create WireGuard device: %w", err))
	}
	c.device = device

	// Helper to clean up device on error after device creation
	cleanupDeviceOnError := func() {
		device.Down()
		device.Close()
		c.device = nil
		c.tunNet = nil
		c.iceBind = nil
	}

	// Configure device with private key
	if err := device.Configure(c.privateKey); err != nil {
		cleanupDeviceOnError()
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("failed to configure device: %w", err))
	}

	// Add peer
	peerConfig := &wireguard.PeerConfig{
		PublicKey:           c.peerKey,
		AllowedIPs:          []string{VirtualSubnet},
		PersistentKeepalive: 25 * time.Second,
	}
	if err := device.AddPeer(peerConfig); err != nil {
		cleanupDeviceOnError()
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("failed to add peer: %w", err))
	}

	// Determine peer IP (opposite of our local IP)
	if c.localIP == MobileVirtualIP {
		c.peerIP = DesktopVirtualIP
	} else {
		c.peerIP = MobileVirtualIP
	}

	// Bring device up
	if err := device.Up(); err != nil {
		cleanupDeviceOnError()
		c.tunnelState = TunnelStateError
		return toErrorJSON(fmt.Errorf("failed to bring device up: %w", err))
	}

	c.tunnelState = TunnelStateActive

	// Create shared HTTP client using tunnel's dial function
	// This enables connection reuse across multiple HTTP requests
	c.httpClient = &http.Client{
		Transport: &http.Transport{
			DialContext: tunNet.DialContext,
		},
		Timeout: HTTPTimeout,
	}

	c.dispatcher.emitTunnelUp()

	// Emit full state change so UI updates immediately
	c.dispatcher.emitStateChange(&ConnectionStateJSON{
		ICEState:       c.iceState,
		TunnelState:    c.tunnelState,
		IsConnected:    c.iceState == ICEStateConnected || c.iceState == ICEStateCompleted,
		IsTunnelActive: true,
		LocalIP:        c.localIP,
		PeerIP:         c.peerIP,
	})

	return ""
}

// HTTPGet performs an HTTP GET request through the tunnel.
// url should be a full URL (e.g., "http://10.0.0.1:8080/test").
// Returns HTTPResultJSON as a JSON string.
//
// Response bodies are limited to MaxHTTPBodySize (10MB) to prevent DoS.
func (c *GhostClient) HTTPGet(url string) string {
	c.mu.RLock()
	httpClient := c.httpClient
	tunnelState := c.tunnelState
	c.mu.RUnlock()

	if tunnelState != TunnelStateActive || httpClient == nil {
		return toJSON(&HTTPResultJSON{
			Success: false,
			Error:   ErrTunnelNotStarted.Error(),
		})
	}

	start := time.Now()

	resp, err := httpClient.Get(url)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return toJSON(&HTTPResultJSON{
			Success:   false,
			Error:     err.Error(),
			LatencyMs: latency,
		})
	}
	defer resp.Body.Close()

	// Limit body size to prevent DoS attacks
	limitedReader := io.LimitReader(resp.Body, MaxHTTPBodySize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return toJSON(&HTTPResultJSON{
			Success:    false,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("failed to read body: %v", err),
			LatencyMs:  latency,
		})
	}

	// Check if body was truncated
	if len(body) > MaxHTTPBodySize {
		return toJSON(&HTTPResultJSON{
			Success:    false,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("response body too large (max %d bytes)", MaxHTTPBodySize),
			LatencyMs:  latency,
		})
	}

	// Extract headers
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return toJSON(&HTTPResultJSON{
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
		StatusCode: resp.StatusCode,
		Body:       string(body),
		Headers:    headers,
		LatencyMs:  latency,
	})
}

// HTTPPost performs an HTTP POST request through the tunnel.
// Returns HTTPResultJSON as a JSON string.
//
// Response bodies are limited to MaxHTTPBodySize (10MB) to prevent DoS.
func (c *GhostClient) HTTPPost(url, contentType, body string) string {
	c.mu.RLock()
	httpClient := c.httpClient
	tunnelState := c.tunnelState
	c.mu.RUnlock()

	if tunnelState != TunnelStateActive || httpClient == nil {
		return toJSON(&HTTPResultJSON{
			Success: false,
			Error:   ErrTunnelNotStarted.Error(),
		})
	}

	start := time.Now()

	resp, err := httpClient.Post(url, contentType, strings.NewReader(body))
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return toJSON(&HTTPResultJSON{
			Success:   false,
			Error:     err.Error(),
			LatencyMs: latency,
		})
	}
	defer resp.Body.Close()

	// Limit body size to prevent DoS attacks
	limitedReader := io.LimitReader(resp.Body, MaxHTTPBodySize+1)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return toJSON(&HTTPResultJSON{
			Success:    false,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("failed to read body: %v", err),
			LatencyMs:  latency,
		})
	}

	// Check if body was truncated
	if len(respBody) > MaxHTTPBodySize {
		return toJSON(&HTTPResultJSON{
			Success:    false,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("response body too large (max %d bytes)", MaxHTTPBodySize),
			LatencyMs:  latency,
		})
	}

	// Extract headers
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return toJSON(&HTTPResultJSON{
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Headers:    headers,
		LatencyMs:  latency,
	})
}

// GetTunnelStats returns tunnel statistics as JSON.
func (c *GhostClient) GetTunnelStats() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := &TunnelStatsJSON{
		IsActive: c.tunnelState == TunnelStateActive,
	}

	if c.device != nil {
		// Get stats from WireGuard device via IPC
		status, err := c.device.GetStatus()
		if err == nil {
			// Parse basic stats from IPC output
			// The IPC format includes tx_bytes and rx_bytes
			for _, line := range strings.Split(status, "\n") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}
				key, value := parts[0], parts[1]
				switch key {
				case "tx_bytes":
					var v uint64
					fmt.Sscanf(value, "%d", &v)
					stats.BytesSent = v
				case "rx_bytes":
					var v uint64
					fmt.Sscanf(value, "%d", &v)
					stats.BytesReceived = v
				case "last_handshake_time_sec":
					var v int64
					fmt.Sscanf(value, "%d", &v)
					stats.LastHandshake = v
				}
			}
		}
	}

	return toJSON(stats)
}

// GetSignalingData returns all local signaling data as JSON.
// This combines credentials, public key, and candidates into a single object
// suitable for QR code encoding.
func (c *GhostClient) GetSignalingData() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.iceAgent == nil {
		return toErrorJSON(ErrGatheringNotStarted)
	}

	ufrag, pwd := c.iceAgent.LocalCredentials()

	candidates := make([]CandidateJSON, len(c.candidates))
	for i, cand := range c.candidates {
		candidates[i] = CandidateJSON{
			Type:       string(cand.Type),
			Address:    cand.Address,
			Port:       cand.Port,
			Protocol:   cand.Protocol,
			Priority:   cand.Priority,
			Foundation: cand.Foundation,
			RelAddr:    cand.RelatedAddress,
			RelPort:    cand.RelatedPort,
		}
	}

	publicKey := ""
	if c.publicKey != nil {
		publicKey = wireguard.EncodeKey(c.publicKey)
	}

	data := &SignalingDataJSON{
		Ufrag:      ufrag,
		Pwd:        pwd,
		PublicKey:  publicKey,
		Candidates: candidates,
	}

	return toJSON(data)
}

// SetSignalingData sets all remote peer signaling data from JSON.
// This is the counterpart to GetSignalingData() for receiving peer data.
func (c *GhostClient) SetSignalingData(jsonStr string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.iceAgent == nil {
		return toErrorJSON(ErrGatheringNotStarted)
	}

	var data SignalingDataJSON
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return toErrorJSON(fmt.Errorf("%w: %v", ErrInvalidJSON, err))
	}

	// Set credentials
	if data.Ufrag == "" || data.Pwd == "" {
		return toErrorJSON(ErrInvalidCredentials)
	}
	if err := c.iceAgent.SetRemoteCredentials(data.Ufrag, data.Pwd); err != nil {
		return toErrorJSON(err)
	}

	// Set peer public key
	if data.PublicKey != "" {
		peerKey, err := wireguard.DecodeKey(data.PublicKey)
		if err != nil {
			return toErrorJSON(fmt.Errorf("%w: %v", ErrInvalidPublicKey, err))
		}
		c.peerKey = peerKey
	}

	// Add candidates
	for _, candJSON := range data.Candidates {
		cand := &ice.Candidate{
			Type:           ice.CandidateType(candJSON.Type),
			Address:        candJSON.Address,
			Port:           candJSON.Port,
			Protocol:       candJSON.Protocol,
			Priority:       candJSON.Priority,
			Foundation:     candJSON.Foundation,
			RelatedAddress: candJSON.RelAddr,
			RelatedPort:    candJSON.RelPort,
		}
		// Ignore errors for individual candidates
		_ = c.iceAgent.AddRemoteCandidate(cand)
	}

	return ""
}

// Helper functions

// toJSON marshals a value to JSON string.
func toJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return toErrorJSON(err)
	}
	return string(data)
}

// toErrorJSON creates a JSON error response.
func toErrorJSON(err error) string {
	result := map[string]string{"error": err.Error()}
	data, _ := json.Marshal(result)
	return string(data)
}
