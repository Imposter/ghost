// mobile-demo is a desktop peer application for testing ghost-go mobile bindings.
//
// Usage:
//
//	go run ./cmd/mobile-demo -role server
//
// This application acts as the desktop peer in a mobile testbed setup.
// It supports multiple sequential clients - when one client disconnects,
// it returns to pairing mode to accept a new client.
//
// Steps per session:
//  1. Creates ICE agent and gathers candidates
//  2. Uses provided or generates WireGuard keys (keys persist across sessions)
//  3. Displays signaling data (for QR code or manual exchange)
//  4. Waits for mobile peer's signaling data
//  5. Establishes ICE connection (controlling role)
//  6. Starts WireGuard tunnel with userspace networking
//  7. Runs HTTP test server on 10.0.0.1:8080
//  8. When client disconnects, returns to step 1
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"ghost-go/internal/ice"
	"ghost-go/internal/wireguard"
	"ghost-go/mobile"
)

const banner = `
╔════════════════════════════════════════════════════════════════╗
║                                                                ║
║               Ghost-GO Mobile Demo                             ║
║         Desktop Peer for Mobile Testbed                        ║
║                                                                ║
║    This server runs on the desktop and connects                ║
║    to mobile apps using the ghost-go mobile SDK.               ║
║                                                                ║
║    Supports multiple sequential clients.                       ║
║    When a client disconnects, returns to pairing mode.         ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝
`

// Config holds the application configuration.
type Config struct {
	Role              string
	STUNServer        string
	GatherTimeout     time.Duration
	ConnectionTimeout time.Duration
	LocalIP           string
	MTU               int
	Keepalive         time.Duration
	HTTPPort          int
	PrivateKey        string // Base64-encoded private key (optional)
	UDPPort           int    // Fixed UDP port for ICE (0 = random)
}

// SignalingData is used for signaling exchange with mobile peer.
type SignalingData struct {
	Ufrag      string          `json:"ufrag"`
	Pwd        string          `json:"pwd"`
	Candidates []CandidateJSON `json:"candidates"`
	PublicKey  string          `json:"publicKey"`
}

// CandidateJSON represents an ICE candidate in JSON form.
type CandidateJSON struct {
	Type       string `json:"type"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Protocol   string `json:"protocol"`
	Priority   uint32 `json:"priority"`
	Foundation string `json:"foundation"`
}

// Session represents an active client session.
type Session struct {
	agent      ice.Agent
	conn       net.Conn
	device     *wireguard.Device
	httpServer *HTTPTestServer
	listener   net.Listener

	// Cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Disconnect notification
	disconnectCh chan struct{}
	closeOnce    sync.Once
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.cancel()

		if s.httpServer != nil {
			s.httpServer.Close()
		}
		if s.listener != nil {
			s.listener.Close()
		}
		if s.device != nil {
			s.device.Down()
			s.device.Close()
		}
		if s.conn != nil {
			s.conn.Close()
		}
		if s.agent != nil {
			s.agent.Close()
		}
	})
}

func (s *Session) notifyDisconnect() {
	select {
	case s.disconnectCh <- struct{}{}:
	default:
	}
}

func main() {
	fmt.Print(banner)

	// Parse flags
	config := parseFlags()

	// Setup logger
	logger := setupLogger()

	logger.Info("Starting Mobile Demo",
		"role", config.Role,
		"stun", config.STUNServer,
		"localIP", config.LocalIP,
	)

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nReceived shutdown signal...")
		cancel()
	}()

	// Run server loop
	if err := runServerLoop(ctx, config, logger); err != nil {
		if ctx.Err() != nil {
			// Normal shutdown
			logger.Info("Server stopped gracefully")
		} else {
			logger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}
}

func parseFlags() *Config {
	config := &Config{}

	flag.StringVar(&config.Role, "role", "server", "Role: 'server' (desktop peer)")
	flag.StringVar(&config.STUNServer, "stun", "stun:stun.l.google.com:19302", "STUN server URL")
	flag.DurationVar(&config.GatherTimeout, "gather-timeout", 15*time.Second, "Candidate gathering timeout")
	flag.DurationVar(&config.ConnectionTimeout, "conn-timeout", 30*time.Second, "ICE connection timeout")
	flag.StringVar(&config.LocalIP, "local-ip", mobile.DesktopVirtualIP, "Local virtual IP address")
	flag.IntVar(&config.MTU, "mtu", mobile.DefaultMTU, "MTU size")
	flag.DurationVar(&config.Keepalive, "keepalive", 25*time.Second, "WireGuard keepalive interval")
	flag.IntVar(&config.HTTPPort, "http-port", mobile.HTTPTestPort, "HTTP test server port")
	flag.StringVar(&config.PrivateKey, "private-key", "", "WireGuard private key (base64, optional - generates new if not provided)")
	flag.IntVar(&config.UDPPort, "udp-port", 0, "Fixed UDP port for ICE (0 = random, useful for firewall rules)")

	flag.Parse()

	return config
}

func setupLogger() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

// runServerLoop runs the main server loop, accepting multiple sequential clients.
func runServerLoop(ctx context.Context, config *Config, logger *slog.Logger) error {
	fmt.Printf("\n=== Running as Desktop Server ===\n\n")

	// Get or generate WireGuard keys (persist across sessions)
	privateKey, publicKeyStr, err := getOrGenerateKeys(config)
	if err != nil {
		return err
	}

	sessionNum := 0
	for {
		sessionNum++

		// Check if we should stop
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		fmt.Printf("\n%s\n", strings.Repeat("═", 60))
		fmt.Printf("  SESSION #%d - Waiting for client...\n", sessionNum)
		fmt.Printf("%s\n\n", strings.Repeat("═", 60))

		// Run a single session
		err := runSession(ctx, config, logger, privateKey, publicKeyStr, sessionNum)
		if err != nil {
			if ctx.Err() != nil {
				// Main context cancelled - shutting down
				return nil
			}
			logger.Error("Session failed", "session", sessionNum, "error", err)
		}

		// Small delay before starting new session
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(1 * time.Second):
		}

		fmt.Println("\n🔄 Returning to pairing mode...")
	}
}

// getOrGenerateKeys gets WireGuard keys from config or generates new ones.
func getOrGenerateKeys(config *Config) ([]byte, string, error) {
	var privateKey []byte

	if config.PrivateKey != "" {
		fmt.Println("Using provided WireGuard private key...")
		var err error
		privateKey, err = wireguard.DecodeKey(config.PrivateKey)
		if err != nil {
			return nil, "", fmt.Errorf("invalid private key: %w", err)
		}
		if err := wireguard.ValidatePrivateKey(privateKey); err != nil {
			return nil, "", fmt.Errorf("invalid private key: %w", err)
		}
		fmt.Println("✓ Using provided private key")
	} else {
		fmt.Println("Generating WireGuard keys...")
		var err error
		privateKey, err = wireguard.GeneratePrivateKey()
		if err != nil {
			return nil, "", fmt.Errorf("failed to generate private key: %w", err)
		}
		fmt.Println("✓ Generated new private key")
	}

	publicKey, err := wireguard.GetPublicKey(privateKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to derive public key: %w", err)
	}

	publicKeyStr := wireguard.EncodeKey(publicKey)
	fmt.Printf("✓ Public key: %s\n", publicKeyStr)

	return privateKey, publicKeyStr, nil
}

// runSession handles a single client session.
func runSession(ctx context.Context, config *Config, logger *slog.Logger,
	privateKey []byte, publicKeyStr string, sessionNum int) error {

	// Create session context
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	session := &Session{
		ctx:          sessionCtx,
		cancel:       sessionCancel,
		disconnectCh: make(chan struct{}, 1),
	}
	defer session.Close()

	// Step 1: Create ICE agent
	fmt.Println("Step 1: Creating ICE agent...")
	iceConfig := ice.DefaultICEConfig()
	iceConfig.STUNServers = []string{config.STUNServer}
	iceConfig.GatherTimeout = config.GatherTimeout
	iceConfig.ConnectionTimeout = config.ConnectionTimeout

	// Set fixed port if specified (useful for firewall rules)
	if config.UDPPort > 0 {
		iceConfig.PortMin = uint16(config.UDPPort)
		iceConfig.PortMax = uint16(config.UDPPort)
		fmt.Printf("   Using fixed UDP port: %d\n", config.UDPPort)
	}

	agent, err := ice.NewAgent(iceConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to create ICE agent: %w", err)
	}
	session.agent = agent

	// Set up connection state monitoring
	agent.OnConnectionStateChange(func(state ice.ConnectionState) {
		logger.Info("ICE connection state changed", "state", state, "session", sessionNum)

		switch state {
		case ice.ConnectionStateFailed, ice.ConnectionStateClosed, ice.ConnectionStateDisconnected:
			fmt.Printf("\n⚠️  Client disconnected (ICE state: %s)\n", state)
			session.notifyDisconnect()
		}
	})

	fmt.Println("✓ ICE agent created")

	// Step 2: Gather ICE candidates
	fmt.Println("\nStep 2: Gathering ICE candidates...")
	fmt.Println("   (This may take 10-15 seconds...)")

	candChan, err := agent.GatherCandidates(sessionCtx)
	if err != nil {
		return fmt.Errorf("failed to start gathering: %w", err)
	}

	var candidates []*ice.Candidate
	for cand := range candChan {
		candidates = append(candidates, cand)
		fmt.Printf("   • Found %s candidate: %s:%d\n", cand.Type, cand.Address, cand.Port)
	}

	if len(candidates) == 0 {
		return fmt.Errorf("no candidates gathered - check network connectivity")
	}

	fmt.Printf("✓ Gathered %d candidates\n", len(candidates))

	// Step 3: Prepare and display signaling data
	fmt.Println("\nStep 3: Preparing signaling data...")
	ufrag, pwd := agent.LocalCredentials()

	localData := &SignalingData{
		Ufrag:      ufrag,
		Pwd:        pwd,
		Candidates: make([]CandidateJSON, len(candidates)),
		PublicKey:  publicKeyStr,
	}

	for i, cand := range candidates {
		localData.Candidates[i] = CandidateJSON{
			Type:       string(cand.Type),
			Address:    cand.Address,
			Port:       cand.Port,
			Protocol:   cand.Protocol,
			Priority:   cand.Priority,
			Foundation: cand.Foundation,
		}
	}

	localJSON, err := json.MarshalIndent(localData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Display signaling data
	fmt.Println("\n" + strings.Repeat("═", 60))
	fmt.Println("YOUR SIGNALING DATA (scan as QR code or copy to mobile):")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println(string(localJSON))
	fmt.Println(strings.Repeat("═", 60))

	// Step 4: Wait for mobile peer's signaling data
	fmt.Println("\nStep 4: Waiting for mobile peer's signaling data...")
	fmt.Println("Paste the mobile app's JSON data below, then press Enter twice:")
	fmt.Println()

	// Read signaling data with context cancellation support
	remoteDataCh := make(chan *SignalingData, 1)
	errCh := make(chan error, 1)

	go func() {
		data, err := readSignalingData()
		if err != nil {
			errCh <- err
		} else {
			remoteDataCh <- data
		}
	}()

	var remoteData *SignalingData
	select {
	case <-sessionCtx.Done():
		return sessionCtx.Err()
	case err := <-errCh:
		return fmt.Errorf("failed to read remote data: %w", err)
	case remoteData = <-remoteDataCh:
	}

	fmt.Printf("✓ Received remote data (ufrag: %s, %d candidates)\n",
		remoteData.Ufrag, len(remoteData.Candidates))

	// Step 5: Add remote candidates
	fmt.Println("\nStep 5: Adding remote candidates...")
	for _, candJSON := range remoteData.Candidates {
		cand := &ice.Candidate{
			Type:       ice.CandidateType(candJSON.Type),
			Address:    candJSON.Address,
			Port:       candJSON.Port,
			Protocol:   candJSON.Protocol,
			Priority:   candJSON.Priority,
			Foundation: candJSON.Foundation,
		}
		if err := agent.AddRemoteCandidate(cand); err != nil {
			logger.Warn("failed to add candidate", "error", err, "address", cand.Address)
		}
	}
	fmt.Printf("✓ Added %d remote candidates\n", len(remoteData.Candidates))

	// Step 6: Set remote credentials
	fmt.Println("\nStep 6: Setting remote credentials...")
	if err := agent.SetRemoteCredentials(remoteData.Ufrag, remoteData.Pwd); err != nil {
		return fmt.Errorf("failed to set credentials: %w", err)
	}
	fmt.Println("✓ Remote credentials set")

	// Step 7: Establish ICE connection
	fmt.Println("\nStep 7: Establishing ICE connection...")
	fmt.Println("   Role: Controlling (initiating connection)")
	fmt.Println("   (This may take 5-30 seconds...)")

	connCtx, connCancel := context.WithTimeout(sessionCtx, config.ConnectionTimeout)
	defer connCancel()

	conn, err := agent.Connect(connCtx, true)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	session.conn = conn

	fmt.Println("✓ ICE connection established!")

	if pair, err := agent.GetSelectedCandidatePair(); err == nil && pair != nil {
		fmt.Printf("   Local:  %s\n", pair.Local.String())
		fmt.Printf("   Remote: %s\n", pair.Remote.String())
	}

	// Step 8: Create ICEBind and TUN device
	fmt.Println("\nStep 8: Creating WireGuard tunnel...")

	// Create ICEBind
	bind := ice.NewICEBind(conn, logger)

	// Parse local IP
	localAddr, err := netip.ParseAddr(config.LocalIP)
	if err != nil {
		return fmt.Errorf("invalid local IP: %w", err)
	}

	// Create userspace TUN device
	tunDev, tunNet, err := wireguard.CreateNetTUN(
		[]netip.Addr{localAddr},
		nil,
		config.MTU,
	)
	if err != nil {
		return fmt.Errorf("failed to create TUN: %w", err)
	}

	// Create WireGuard config
	wgConfig := &wireguard.WireGuardConfig{
		PrivateKey:          privateKey,
		ListenPort:          0,
		MTU:                 config.MTU,
		PersistentKeepalive: config.Keepalive,
	}

	// Create WireGuard device
	device, err := wireguard.NewDevice(tunDev, bind, wgConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to create device: %w", err)
	}
	session.device = device

	// Configure device
	if err := device.Configure(privateKey); err != nil {
		return fmt.Errorf("failed to configure device: %w", err)
	}

	// Add peer
	peerPublicKey, err := wireguard.DecodeKey(remoteData.PublicKey)
	if err != nil {
		return fmt.Errorf("invalid peer public key: %w", err)
	}

	peerEndpoint := conn.RemoteAddr().String()

	peerConfig := &wireguard.PeerConfig{
		PublicKey:           peerPublicKey,
		AllowedIPs:          []string{mobile.VirtualSubnet},
		PersistentKeepalive: config.Keepalive,
		Endpoint:            peerEndpoint,
	}

	if err := device.AddPeer(peerConfig); err != nil {
		return fmt.Errorf("failed to add peer: %w", err)
	}

	// Bring device up
	if err := device.Up(); err != nil {
		return fmt.Errorf("failed to bring device up: %w", err)
	}

	fmt.Println("✓ WireGuard tunnel established!")

	// Step 9: Start HTTP server
	fmt.Println("\nStep 9: Starting HTTP test server...")

	tcpAddr := &net.TCPAddr{
		IP:   net.ParseIP(config.LocalIP),
		Port: config.HTTPPort,
	}
	listener, err := tunNet.ListenTCP(tcpAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	session.listener = listener

	httpServer := NewHTTPTestServer(listener, logger)
	session.httpServer = httpServer

	// Start server in background
	go func() {
		if err := httpServer.Start(); err != nil {
			// Ignore error if we're shutting down
			if sessionCtx.Err() == nil {
				logger.Error("HTTP server error", "error", err)
			}
		}
	}()

	fmt.Printf("✓ HTTP server listening on http://%s:%d/\n", config.LocalIP, config.HTTPPort)

	// Success!
	fmt.Println("\n" + strings.Repeat("═", 60))
	fmt.Printf("🎉 SESSION #%d CONNECTED!\n", sessionNum)
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("\nServer Information:\n")
	fmt.Printf("  Virtual IP:  %s\n", config.LocalIP)
	fmt.Printf("  HTTP Port:   %d\n", config.HTTPPort)
	fmt.Printf("  Peer Key:    %s\n", remoteData.PublicKey[:16]+"...")
	fmt.Printf("\nTest Endpoints:\n")
	fmt.Printf("  GET  http://%s:%d/test    - JSON test response\n", config.LocalIP, config.HTTPPort)
	fmt.Printf("  POST http://%s:%d/echo    - Echo request body\n", config.LocalIP, config.HTTPPort)
	fmt.Printf("  GET  http://%s:%d/health  - Health check\n", config.LocalIP, config.HTTPPort)
	fmt.Printf("  GET  http://%s:%d/info    - Server info\n", config.LocalIP, config.HTTPPort)
	fmt.Println("\nWaiting for client activity or disconnect...")
	fmt.Println("Press Ctrl+C to stop the server completely.")

	// Wait for disconnect or shutdown
	select {
	case <-sessionCtx.Done():
		fmt.Println("\nSession cancelled...")
		return nil
	case <-session.disconnectCh:
		fmt.Println("\nClient disconnected, cleaning up session...")
		return nil
	}
}

func readSignalingData() (*SignalingData, error) {
	scanner := bufio.NewScanner(os.Stdin)
	var lines []string

	// Read until empty line or EOF
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan error: %w", err)
	}

	jsonData := strings.Join(lines, "\n")

	var data SignalingData
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return &data, nil
}
