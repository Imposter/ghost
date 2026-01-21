package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ghost-go/internal/ice"
	"ghost-go/internal/wireguard"
)

const banner = `
╔════════════════════════════════════════════════════════╗
║                                                        ║
║            Ghost-GO Phase 1 Demo                      ║
║    ICE + WireGuard P2P Connection Demo                ║
║                                                        ║
╚════════════════════════════════════════════════════════╝
`

type Config struct {
	Role              string
	STUNServer        string
	GatherTimeout     time.Duration
	ConnectionTimeout time.Duration
	TUNName           string
	MTU               int
	Keepalive         time.Duration
	AllowedIPs        string
}

type SignalingData struct {
	Ufrag      string          `json:"ufrag"`
	Pwd        string          `json:"pwd"`
	Candidates []CandidateJSON `json:"candidates"`
	PublicKey  string          `json:"public_key"`
}

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

	logger.Info("Starting Ghost-GO Phase 1 Demo",
		"role", config.Role,
		"stun", config.STUNServer,
	)

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("Shutting down...")
		cancel()
	}()

	// Run demo
	if err := runDemo(ctx, config, logger); err != nil {
		logger.Error("Demo failed", "error", err)
		os.Exit(1)
	}

	logger.Info("Demo completed successfully")
}

func parseFlags() *Config {
	config := &Config{}

	flag.StringVar(&config.Role, "role", "", "Peer role: 'a' or 'b' (required)")
	flag.StringVar(&config.STUNServer, "stun", "stun:stun.l.google.com:19302", "STUN server URL")
	flag.DurationVar(&config.GatherTimeout, "gather-timeout", 15*time.Second, "Candidate gathering timeout")
	flag.DurationVar(&config.ConnectionTimeout, "conn-timeout", 30*time.Second, "ICE connection timeout")
	flag.StringVar(&config.TUNName, "tun", "ghost0", "TUN device name")
	flag.IntVar(&config.MTU, "mtu", 1280, "MTU size")
	flag.DurationVar(&config.Keepalive, "keepalive", 25*time.Second, "WireGuard keepalive interval")
	flag.StringVar(&config.AllowedIPs, "allowed-ips", "10.0.0.0/24", "Allowed IP ranges")

	flag.Parse()

	if config.Role != "a" && config.Role != "b" {
		fmt.Println("Error: -role must be 'a' or 'b'")
		flag.Usage()
		os.Exit(1)
	}

	return config
}

func setupLogger() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

func runDemo(ctx context.Context, config *Config, logger *slog.Logger) error {
	fmt.Printf("\n=== Running as Peer %s ===\n\n", strings.ToUpper(config.Role))

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

	// Step 2: Get local credentials
	fmt.Println("\nStep 2: Getting local ICE credentials...")
	ufrag, pwd := agent.LocalCredentials()

	fmt.Printf("✓ Local credentials:\n")
	fmt.Printf("   ufrag: %s\n", ufrag)
	fmt.Printf("   pwd:   %s\n", pwd)

	// Step 3: Generate WireGuard keys
	fmt.Println("\nStep 3: Generating WireGuard keys...")
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

	// Step 4: Gather ICE candidates
	fmt.Println("\nStep 4: Gathering ICE candidates...")
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

	// Step 5: Export signaling data
	fmt.Println("\nStep 5: Preparing signaling data...")
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

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("YOUR SIGNALING DATA (copy this and send to peer):")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(string(localJSON))
	fmt.Println(strings.Repeat("=", 60))

	// Step 6: Wait for remote signaling data
	fmt.Println("\nStep 6: Waiting for remote peer's signaling data...")
	fmt.Println("Paste the peer's JSON data below, then press Enter twice:")
	fmt.Println()

	remoteData, err := readSignalingData()
	if err != nil {
		return fmt.Errorf("failed to read remote data: %w", err)
	}

	fmt.Printf("✓ Received remote data (ufrag: %s, %d candidates)\n",
		remoteData.Ufrag, len(remoteData.Candidates))

	// Step 7: Add remote candidates
	fmt.Println("\nStep 7: Adding remote candidates...")
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
		} else {
			logger.Debug("added remote candidate", "type", cand.Type, "address", cand.Address, "port", cand.Port)
		}
	}

	fmt.Printf("✓ Added %d remote candidates\n", len(remoteData.Candidates))

	// Give time for remote candidates to be processed
	fmt.Println("   (Processing remote candidates...)")
	time.Sleep(1 * time.Second)

	// Step 8: Set remote credentials
	fmt.Println("\nStep 8: Setting remote credentials...")
	if err := agent.SetRemoteCredentials(remoteData.Ufrag, remoteData.Pwd); err != nil {
		return fmt.Errorf("failed to set credentials: %w", err)
	}
	fmt.Println("✓ Remote credentials set")

	// Step 9: Establish ICE connection
	fmt.Println("\nStep 9: Establishing ICE connection...")

	// Determine ICE role based on peer role
	// Peer A = controlling (Dial), Peer B = controlled (Accept)
	controlling := config.Role == "a"
	if controlling {
		fmt.Println("   Role: Controlling (initiating connection)")
	} else {
		fmt.Println("   Role: Controlled (accepting connection)")
	}
	fmt.Println("   (This may take 5-30 seconds...)")
	fmt.Println("   Performing connectivity checks...")

	connCtx, connCancel := context.WithTimeout(ctx, config.ConnectionTimeout)
	defer connCancel()

	conn, err := agent.Connect(connCtx, controlling)
	if err != nil {
		// Add detailed error context
		return fmt.Errorf("failed to connect (check both peers started, have matching candidates, firewall allows UDP): %w", err)
	}
	defer conn.Close()

	fmt.Println("✓ ICE connection established!")

	// Show selected candidate pair
	if pair, err := agent.GetSelectedCandidatePair(); err == nil && pair != nil {
		fmt.Printf("   Local:  %s\n", pair.Local.String())
		fmt.Printf("   Remote: %s\n", pair.Remote.String())
	}

	// Step 10: Create ICEBind
	fmt.Println("\nStep 10: Creating ICEBind adapter...")
	bind := ice.NewICEBind(conn, logger)
	defer bind.Close()
	fmt.Println("✓ ICEBind created")

	// Step 11: Create TUN device
	fmt.Println("\nStep 11: Creating TUN device...")
	fmt.Printf("   (May require administrator/root privileges)\n")

	tunDev, err := wireguard.CreateTUN(config.TUNName, config.MTU)
	if err != nil {
		return fmt.Errorf("failed to create TUN: %w", err)
	}
	defer tunDev.Close()

	tunName, _ := tunDev.Name()
	fmt.Printf("✓ TUN device created: %s\n", tunName)

	// Step 12: Create WireGuard device
	fmt.Println("\nStep 12: Creating WireGuard device...")
	wgConfig := &wireguard.WireGuardConfig{
		PrivateKey:          privateKey,
		ListenPort:          0, // Auto-assign
		MTU:                 config.MTU,
		PersistentKeepalive: config.Keepalive,
	}

	device, err := wireguard.NewDevice(tunDev, bind, wgConfig, logger)
	if err != nil {
		return fmt.Errorf("failed to create device: %w", err)
	}
	defer device.Close()

	fmt.Println("✓ WireGuard device created")

	// Step 13: Configure device
	fmt.Println("\nStep 13: Configuring WireGuard...")
	if err := device.Configure(privateKey); err != nil {
		return fmt.Errorf("failed to configure: %w", err)
	}
	fmt.Println("✓ Device configured")

	// Step 14: Add peer
	fmt.Println("\nStep 14: Adding WireGuard peer...")

	peerPublicKey, err := wireguard.DecodeKey(remoteData.PublicKey)
	if err != nil {
		return fmt.Errorf("invalid peer public key: %w", err)
	}

	peerConfig := &wireguard.PeerConfig{
		PublicKey:           peerPublicKey,
		AllowedIPs:          []string{config.AllowedIPs},
		PersistentKeepalive: config.Keepalive,
	}

	if err := device.AddPeer(peerConfig); err != nil {
		return fmt.Errorf("failed to add peer: %w", err)
	}

	fmt.Printf("✓ Peer added (allowed IPs: %s)\n", config.AllowedIPs)

	// Step 15: Bring device up
	fmt.Println("\nStep 15: Bringing device up...")
	if err := device.Up(); err != nil {
		return fmt.Errorf("failed to bring up device: %w", err)
	}
	fmt.Println("✓ Device is UP")

	// Success!
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🎉 SUCCESS! Encrypted tunnel established!")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("\nTunnel Information:\n")
	fmt.Printf("  TUN device:  %s\n", tunName)
	fmt.Printf("  MTU:         %d\n", config.MTU)
	fmt.Printf("  Allowed IPs: %s\n", config.AllowedIPs)
	fmt.Printf("  Keepalive:   %s\n", config.Keepalive)

	// Enter interactive mode
	fmt.Println("\nEntering interactive mode...")
	fmt.Println("Commands: status, peer, quit")
	fmt.Println()

	return interactiveMode(ctx, device, conn, logger)
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

func interactiveMode(ctx context.Context, device *wireguard.Device, conn interface{}, logger *slog.Logger) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("> ")

		if !scanner.Scan() {
			break
		}

		cmd := strings.TrimSpace(scanner.Text())

		switch cmd {
		case "status":
			showStatus(device, logger)
		case "peer":
			showPeers(device)
		case "quit", "exit":
			fmt.Println("Shutting down...")
			return nil
		case "help":
			fmt.Println("Available commands:")
			fmt.Println("  status  - Show device status")
			fmt.Println("  peer    - Show peer information")
			fmt.Println("  quit    - Exit demo")
		case "":
			continue
		default:
			fmt.Printf("Unknown command: %s (type 'help' for commands)\n", cmd)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan error: %w", err)
	}

	return nil
}

func showStatus(device *wireguard.Device, logger *slog.Logger) {
	status, err := device.GetStatus()
	if err != nil {
		fmt.Printf("Error getting status: %v\n", err)
		return
	}

	fmt.Println("\n=== Device Status ===")
	fmt.Println(status)
}

func showPeers(device *wireguard.Device) {
	peers := device.GetPeers()

	fmt.Println("\n=== Connected Peers ===")
	if len(peers) == 0 {
		fmt.Println("No peers connected")
		return
	}

	for i, peer := range peers {
		fmt.Printf("\nPeer %d:\n", i+1)
		fmt.Printf("  Public Key: %s\n", wireguard.EncodeKey(peer.PublicKey))
		fmt.Printf("  Endpoint:   %s\n", peer.Endpoint)
		fmt.Printf("  Allowed IPs: %v\n", peer.AllowedIPs)
		if peer.PersistentKeepalive > 0 {
			fmt.Printf("  Keepalive:  %s\n", peer.PersistentKeepalive)
		}
	}
}
