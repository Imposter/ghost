// Package tests provides integration test utilities for ghost-go.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/mobile"
)

// TestHarness runs two GhostClients in the same process for automated testing.
// This allows full integration tests without manual signaling exchange.
type TestHarness struct {
	t *testing.T

	// The two peers
	Desktop *mobile.GhostClient // Controlling peer (10.0.0.1)
	Mobile  *mobile.GhostClient // Controlled peer (10.0.0.2)

	// Event tracking
	mu            sync.Mutex
	desktopEvents []mobile.EventJSON
	mobileEvents  []mobile.EventJSON

	// HTTP server for testing
	httpServer *http.Server
}

// NewTestHarness creates a new test harness with two peers.
func NewTestHarness(t *testing.T) *TestHarness {
	t.Helper()

	desktop, err := mobile.NewClient("")
	if err != nil {
		t.Fatalf("failed to create desktop client: %v", err)
	}

	mobileClient, err := mobile.NewClient("")
	if err != nil {
		desktop.Close()
		t.Fatalf("failed to create mobile client: %v", err)
	}

	// Set fixed ports for firewall compatibility
	// Desktop uses port 51200, Mobile uses port 51201
	desktopPort := uint16(ice.TestPortRangeStart + 200)
	mobilePort := uint16(ice.TestPortRangeStart + 201)
	desktop.SetPortRange(desktopPort, desktopPort)
	mobileClient.SetPortRange(mobilePort, mobilePort)

	h := &TestHarness{
		t:       t,
		Desktop: desktop,
		Mobile:  mobileClient,
	}

	// Set up event callbacks
	desktop.SetEventCallback(&eventCollector{harness: h, peer: "desktop"})
	mobileClient.SetEventCallback(&eventCollector{harness: h, peer: "mobile"})

	return h
}

// Cleanup releases all resources held by the harness.
func (h *TestHarness) Cleanup() {
	if h.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.httpServer.Shutdown(ctx)
	}

	if h.Desktop != nil {
		h.Desktop.Close()
	}
	if h.Mobile != nil {
		h.Mobile.Close()
	}
}

// GenerateKeys generates WireGuard keys for both peers.
func (h *TestHarness) GenerateKeys() error {
	result := h.Desktop.GenerateWireGuardKey()
	if hasError(result) {
		return fmt.Errorf("desktop key generation failed: %s", result)
	}

	result = h.Mobile.GenerateWireGuardKey()
	if hasError(result) {
		return fmt.Errorf("mobile key generation failed: %s", result)
	}

	return nil
}

// StartGathering starts ICE candidate gathering on both peers.
func (h *TestHarness) StartGathering() error {
	result := h.Desktop.StartGathering()
	if result != "" {
		return fmt.Errorf("desktop gathering failed: %s", result)
	}

	result = h.Mobile.StartGathering()
	if result != "" {
		return fmt.Errorf("mobile gathering failed: %s", result)
	}

	return nil
}

// WaitForGatheringComplete waits for both peers to finish gathering candidates.
func (h *TestHarness) WaitForGatheringComplete(timeout time.Duration) error {
	// Wait for candidates to be gathered
	// The gathering happens asynchronously, so we poll for candidates
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		desktopCands := h.Desktop.GetLocalCandidatesJSON()
		mobileCands := h.Mobile.GetLocalCandidatesJSON()

		var desktopList, mobileList []mobile.CandidateJSON
		json.Unmarshal([]byte(desktopCands), &desktopList)
		json.Unmarshal([]byte(mobileCands), &mobileList)

		// Wait until both have at least one candidate
		if len(desktopList) > 0 && len(mobileList) > 0 {
			// Give a bit more time for additional candidates
			time.Sleep(500 * time.Millisecond)
			return nil
		}

		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("gathering timeout")
}

// ExchangeSignaling exchanges signaling data between the two peers.
// This automates what would normally be done via QR code.
func (h *TestHarness) ExchangeSignaling() error {
	// Get signaling data from both peers
	desktopData := h.Desktop.GetSignalingData()
	if hasError(desktopData) {
		return fmt.Errorf("failed to get desktop signaling data: %s", desktopData)
	}

	mobileData := h.Mobile.GetSignalingData()
	if hasError(mobileData) {
		return fmt.Errorf("failed to get mobile signaling data: %s", mobileData)
	}

	// Exchange: desktop receives mobile's data, mobile receives desktop's data
	result := h.Desktop.SetSignalingData(mobileData)
	if result != "" {
		return fmt.Errorf("desktop failed to set mobile signaling data: %s", result)
	}

	result = h.Mobile.SetSignalingData(desktopData)
	if result != "" {
		return fmt.Errorf("mobile failed to set desktop signaling data: %s", result)
	}

	return nil
}

// Connect establishes ICE connection on both peers.
// Desktop is controlling, Mobile is controlled.
func (h *TestHarness) Connect() error {
	// Connect must happen concurrently since both peers block waiting for each other
	var wg sync.WaitGroup
	var desktopErr, mobileErr error

	wg.Add(2)

	go func() {
		defer wg.Done()
		result := h.Desktop.Connect(true) // Controlling
		if result != "" {
			desktopErr = fmt.Errorf("desktop connect failed: %s", result)
		}
	}()

	go func() {
		defer wg.Done()
		result := h.Mobile.Connect(false) // Controlled
		if result != "" {
			mobileErr = fmt.Errorf("mobile connect failed: %s", result)
		}
	}()

	wg.Wait()

	if desktopErr != nil {
		return desktopErr
	}
	if mobileErr != nil {
		return mobileErr
	}

	return nil
}

// WaitForConnection waits for ICE connection to be established on both peers.
func (h *TestHarness) WaitForConnection(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		desktopState := h.Desktop.GetConnectionState()
		mobileState := h.Mobile.GetConnectionState()

		var ds, ms mobile.ConnectionStateJSON
		json.Unmarshal([]byte(desktopState), &ds)
		json.Unmarshal([]byte(mobileState), &ms)

		if ds.IsConnected && ms.IsConnected {
			return nil
		}

		if ds.ICEState == mobile.ICEStateFailed || ms.ICEState == mobile.ICEStateFailed {
			return fmt.Errorf("ICE connection failed")
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("connection timeout")
}

// SetupTunnelIPs configures the virtual IP addresses for both peers.
func (h *TestHarness) SetupTunnelIPs() error {
	result := h.Desktop.SetLocalIP(mobile.DesktopVirtualIP + "/24")
	if result != "" {
		return fmt.Errorf("desktop SetLocalIP failed: %s", result)
	}

	result = h.Mobile.SetLocalIP(mobile.MobileVirtualIP + "/24")
	if result != "" {
		return fmt.Errorf("mobile SetLocalIP failed: %s", result)
	}

	return nil
}

// StartTunnels starts WireGuard tunnels on both peers.
func (h *TestHarness) StartTunnels() error {
	var wg sync.WaitGroup
	var desktopErr, mobileErr error

	wg.Add(2)

	go func() {
		defer wg.Done()
		result := h.Desktop.StartTunnel()
		if result != "" {
			desktopErr = fmt.Errorf("desktop StartTunnel failed: %s", result)
		}
	}()

	go func() {
		defer wg.Done()
		result := h.Mobile.StartTunnel()
		if result != "" {
			mobileErr = fmt.Errorf("mobile StartTunnel failed: %s", result)
		}
	}()

	wg.Wait()

	if desktopErr != nil {
		return desktopErr
	}
	if mobileErr != nil {
		return mobileErr
	}

	return nil
}

// WaitForTunnelActive waits for both tunnels to become active.
func (h *TestHarness) WaitForTunnelActive(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		desktopState := h.Desktop.GetConnectionState()
		mobileState := h.Mobile.GetConnectionState()

		var ds, ms mobile.ConnectionStateJSON
		json.Unmarshal([]byte(desktopState), &ds)
		json.Unmarshal([]byte(mobileState), &ms)

		if ds.IsTunnelActive && ms.IsTunnelActive {
			return nil
		}

		if ds.TunnelState == mobile.TunnelStateError || ms.TunnelState == mobile.TunnelStateError {
			return fmt.Errorf("tunnel startup error")
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("tunnel activation timeout")
}

// StartHTTPServer starts a simple HTTP test server.
// It runs on a separate goroutine and returns the server for cleanup.
func (h *TestHarness) StartHTTPServer(addr string) *http.Server {
	mux := http.NewServeMux()

	// Test endpoint
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"message":   "Hello from desktop",
			"timestamp": time.Now().Unix(),
			"method":    r.Method,
		}
		json.NewEncoder(w).Encode(response)
	})

	// Echo endpoint
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		buf := make([]byte, 65536)
		n, _ := r.Body.Read(buf)
		w.Write(buf[:n])
	})

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go server.ListenAndServe()

	h.httpServer = server
	return server
}

// GetDesktopEvents returns collected events from desktop peer.
func (h *TestHarness) GetDesktopEvents() []mobile.EventJSON {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]mobile.EventJSON{}, h.desktopEvents...)
}

// GetMobileEvents returns collected events from mobile peer.
func (h *TestHarness) GetMobileEvents() []mobile.EventJSON {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]mobile.EventJSON{}, h.mobileEvents...)
}

// eventCollector implements mobile.EventCallback for test event tracking.
type eventCollector struct {
	harness *TestHarness
	peer    string
}

func (c *eventCollector) OnEvent(eventJSON string) {
	var event mobile.EventJSON
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		return
	}

	c.harness.mu.Lock()
	defer c.harness.mu.Unlock()

	if c.peer == "desktop" {
		c.harness.desktopEvents = append(c.harness.desktopEvents, event)
	} else {
		c.harness.mobileEvents = append(c.harness.mobileEvents, event)
	}
}

// hasError checks if a JSON result contains an error.
func hasError(result string) bool {
	if result == "" {
		return false
	}
	var errMap map[string]string
	if err := json.Unmarshal([]byte(result), &errMap); err != nil {
		return false
	}
	_, hasErr := errMap["error"]
	return hasErr
}

// AssertNoError is a test helper that fails if the result contains an error.
func AssertNoError(t *testing.T, result string, msg string) {
	t.Helper()
	if hasError(result) {
		t.Fatalf("%s: %s", msg, result)
	}
}

// FullTunnelSetup performs the complete setup process for both peers.
// This is the "happy path" sequence used by most integration tests.
func (h *TestHarness) FullTunnelSetup() error {
	// 1. Generate keys
	if err := h.GenerateKeys(); err != nil {
		return fmt.Errorf("key generation: %w", err)
	}

	// 2. Start gathering
	if err := h.StartGathering(); err != nil {
		return fmt.Errorf("start gathering: %w", err)
	}

	// 3. Wait for gathering
	if err := h.WaitForGatheringComplete(15 * time.Second); err != nil {
		return fmt.Errorf("gathering: %w", err)
	}

	// 4. Exchange signaling
	if err := h.ExchangeSignaling(); err != nil {
		return fmt.Errorf("signaling exchange: %w", err)
	}

	// 5. Set local IPs
	if err := h.SetupTunnelIPs(); err != nil {
		return fmt.Errorf("tunnel IP setup: %w", err)
	}

	// 6. Connect (ICE)
	if err := h.Connect(); err != nil {
		return fmt.Errorf("ICE connect: %w", err)
	}

	// 7. Wait for connection
	if err := h.WaitForConnection(30 * time.Second); err != nil {
		return fmt.Errorf("wait for connection: %w", err)
	}

	// 8. Start tunnels
	if err := h.StartTunnels(); err != nil {
		return fmt.Errorf("start tunnels: %w", err)
	}

	// 9. Wait for tunnels to be active
	if err := h.WaitForTunnelActive(10 * time.Second); err != nil {
		return fmt.Errorf("wait for tunnel active: %w", err)
	}

	return nil
}
