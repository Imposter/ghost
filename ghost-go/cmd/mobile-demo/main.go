// mobile-demo is a desktop peer application for testing ghost-go mobile bindings.
//
// Usage:
//
//	go run ./cmd/mobile-demo -role server
//
// This application acts as the desktop peer in a mobile testbed setup.
// It performs the following steps:
//  1. Creates ICE agent and gathers candidates
//  2. Generates WireGuard keys
//  3. Displays signaling data (for QR code or manual exchange)
//  4. Waits for mobile peer's signaling data
//  5. Establishes ICE connection (controlling role)
//  6. Starts WireGuard tunnel with userspace networking
//  7. Runs HTTP test server on 10.0.0.1:8080
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

	// Run server
	if err := runServer(ctx, config, logger); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}

	logger.Info("Server stopped gracefully")
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

func runServer(ctx context.Context, config *Config, logger *slog.Logger) error {
	fmt.Printf("\n=== Running as Desktop Server ===\n\n")

	// Step 1: Create ICE agent
	fmt.Println("Step 1: Creating ICE agent...")
	iceConfig := ice.DefaultICEConfig()
	iceConfig.STUNServers = []string{config.STUNServer}
	iceConfig.GatherTimeout = config.GatherTimeout
	iceConfig.ConnectionTimeout = config.ConnectionTimeout

	agent, err := ice.NewAgent(iceConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to create ICE agent: %w", err)
	}
	defer agent.Close()
	fmt.Println("✓ ICE agent created")

	// Step 2: Generate WireGuard keys
	fmt.Println("\nStep 2: Generating WireGuard keys...")
	privateKey, err := wireguard.GeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	publicKey, err := wireguard.GetPublicKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to derive public key: %w", err)
	}

	publicKeyStr := wireguard.EncodeKey(publicKey)
	fmt.Printf("✓ Public key: %s\n", publicKeyStr)

	// Step 3: Gather ICE candidates
	fmt.Println("\nStep 3: Gathering ICE candidates...")
	fmt.Println("   (This may take 10-15 seconds...)")

	candChan, err := agent.GatherCandidates(ctx)
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

	// Step 4: Prepare signaling data
	fmt.Println("\nStep 4: Preparing signaling data...")
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

	// Step 5: Wait for mobile peer's signaling data
	fmt.Println("\nStep 5: Waiting for mobile peer's signaling data...")
	fmt.Println("Paste the mobile app's JSON data below, then press Enter twice:")
	fmt.Println()

	remoteData, err := readSignalingData()
	if err != nil {
		return fmt.Errorf("failed to read remote data: %w", err)
	}

	fmt.Printf("✓ Received remote data (ufrag: %s, %d candidates)\n",
		remoteData.Ufrag, len(remoteData.Candidates))

	// Step 6: Add remote candidates
	fmt.Println("\nStep 6: Adding remote candidates...")
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

	// Step 7: Set remote credentials
	fmt.Println("\nStep 7: Setting remote credentials...")
	if err := agent.SetRemoteCredentials(remoteData.Ufrag, remoteData.Pwd); err != nil {
		return fmt.Errorf("failed to set credentials: %w", err)
	}
	fmt.Println("✓ Remote credentials set")

	// Step 8: Establish ICE connection
	fmt.Println("\nStep 8: Establishing ICE connection...")
	fmt.Println("   Role: Controlling (initiating connection)")
	fmt.Println("   (This may take 5-30 seconds...)")

	connCtx, connCancel := context.WithTimeout(ctx, config.ConnectionTimeout)
	defer connCancel()

	conn, err := agent.Connect(connCtx, true)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	fmt.Println("✓ ICE connection established!")

	if pair, err := agent.GetSelectedCandidatePair(); err == nil && pair != nil {
		fmt.Printf("   Local:  %s\n", pair.Local.String())
		fmt.Printf("   Remote: %s\n", pair.Remote.String())
	}

	// Step 9: Create ICEBind and TUN device
	fmt.Println("\nStep 9: Creating WireGuard tunnel...")

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
	defer device.Close()

	// Configure device
	if err := device.Configure(privateKey); err != nil {
		return fmt.Errorf("failed to configure device: %w", err)
	}

	// Add peer
	peerPublicKey, err := wireguard.DecodeKey(remoteData.PublicKey)
	if err != nil {
		return fmt.Errorf("invalid peer public key: %w", err)
	}

	peerConfig := &wireguard.PeerConfig{
		PublicKey:           peerPublicKey,
		AllowedIPs:          []string{mobile.VirtualSubnet},
		PersistentKeepalive: config.Keepalive,
	}

	if err := device.AddPeer(peerConfig); err != nil {
		return fmt.Errorf("failed to add peer: %w", err)
	}

	// Bring device up
	if err := device.Up(); err != nil {
		return fmt.Errorf("failed to bring device up: %w", err)
	}

	fmt.Println("✓ WireGuard tunnel established!")

	// Step 10: Start HTTP server
	fmt.Println("\nStep 10: Starting HTTP test server...")

	// Create TCP listener through the tunnel
	// The netstack.Net provides ListenTCP which creates a listener on the virtual network
	tcpAddr := &net.TCPAddr{
		IP:   net.ParseIP(config.LocalIP),
		Port: config.HTTPPort,
	}
	listener, err := tunNet.ListenTCP(tcpAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	defer listener.Close()

	httpServer := NewHTTPTestServer(listener, logger)

	// Start server in background
	go func() {
		if err := httpServer.Start(); err != nil {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	fmt.Printf("✓ HTTP server listening on http://%s:%d/\n", config.LocalIP, config.HTTPPort)

	// Success!
	fmt.Println("\n" + strings.Repeat("═", 60))
	fmt.Println("🎉 SUCCESS! Desktop server is ready!")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Printf("\nServer Information:\n")
	fmt.Printf("  Virtual IP:  %s\n", config.LocalIP)
	fmt.Printf("  HTTP Port:   %d\n", config.HTTPPort)
	fmt.Printf("  MTU:         %d\n", config.MTU)
	fmt.Printf("  Keepalive:   %s\n", config.Keepalive)
	fmt.Printf("\nTest Endpoints:\n")
	fmt.Printf("  GET  http://%s:%d/test    - JSON test response\n", config.LocalIP, config.HTTPPort)
	fmt.Printf("  POST http://%s:%d/echo    - Echo request body\n", config.LocalIP, config.HTTPPort)
	fmt.Printf("  GET  http://%s:%d/health  - Health check\n", config.LocalIP, config.HTTPPort)
	fmt.Printf("  GET  http://%s:%d/info    - Server info\n", config.LocalIP, config.HTTPPort)
	fmt.Println("\nPress Ctrl+C to stop the server...")

	// Wait for shutdown signal
	<-ctx.Done()

	fmt.Println("\nShutting down...")
	httpServer.Close()
	device.Down()

	return nil
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
