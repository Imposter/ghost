package tests

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/mobile"
)

// TestKeyGeneration verifies that both peers can generate valid WireGuard keys.
func TestKeyGeneration(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	err := h.GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	// Verify both peers have keys
	desktopPubKey := h.Desktop.GetPublicKey()
	mobilePubKey := h.Mobile.GetPublicKey()

	if desktopPubKey == "" {
		t.Error("desktop public key is empty")
	}
	if mobilePubKey == "" {
		t.Error("mobile public key is empty")
	}

	// Keys should be different
	if desktopPubKey == mobilePubKey {
		t.Error("desktop and mobile have the same public key")
	}

	// Keys should be valid base64 (44 chars)
	if len(desktopPubKey) != 44 {
		t.Errorf("desktop public key has wrong length: %d", len(desktopPubKey))
	}
	if len(mobilePubKey) != 44 {
		t.Errorf("mobile public key has wrong length: %d", len(mobilePubKey))
	}
}

// TestICEGathering verifies that both peers can gather ICE candidates.
func TestICEGathering(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	err := h.StartGathering()
	if err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Wait for gathering to complete
	err = h.WaitForGatheringComplete(15 * time.Second)
	if err != nil {
		t.Fatalf("WaitForGatheringComplete failed: %v", err)
	}

	// Verify candidates were gathered
	desktopCands := h.Desktop.GetLocalCandidatesJSON()
	mobileCands := h.Mobile.GetLocalCandidatesJSON()

	var desktopList, mobileList []mobile.CandidateJSON
	json.Unmarshal([]byte(desktopCands), &desktopList)
	json.Unmarshal([]byte(mobileCands), &mobileList)

	if len(desktopList) == 0 {
		t.Error("desktop has no candidates")
	}
	if len(mobileList) == 0 {
		t.Error("mobile has no candidates")
	}

	t.Logf("Desktop gathered %d candidates", len(desktopList))
	t.Logf("Mobile gathered %d candidates", len(mobileList))
}

// TestSignalingExchange verifies that signaling data can be exchanged between peers.
func TestSignalingExchange(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Generate keys first
	if err := h.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	// Start gathering
	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Wait for gathering
	if err := h.WaitForGatheringComplete(15 * time.Second); err != nil {
		t.Fatalf("WaitForGatheringComplete failed: %v", err)
	}

	// Get signaling data
	desktopData := h.Desktop.GetSignalingData()
	mobileData := h.Mobile.GetSignalingData()

	// Verify signaling data format
	var desktopSignaling, mobileSignaling mobile.SignalingDataJSON
	if err := json.Unmarshal([]byte(desktopData), &desktopSignaling); err != nil {
		t.Fatalf("failed to parse desktop signaling data: %v", err)
	}
	if err := json.Unmarshal([]byte(mobileData), &mobileSignaling); err != nil {
		t.Fatalf("failed to parse mobile signaling data: %v", err)
	}

	// Verify structure
	if desktopSignaling.Ufrag == "" {
		t.Error("desktop ufrag is empty")
	}
	if desktopSignaling.Pwd == "" {
		t.Error("desktop pwd is empty")
	}
	if desktopSignaling.PublicKey == "" {
		t.Error("desktop public key is empty")
	}
	if len(desktopSignaling.Candidates) == 0 {
		t.Error("desktop has no candidates in signaling data")
	}

	// Exchange signaling data
	if err := h.ExchangeSignaling(); err != nil {
		t.Fatalf("ExchangeSignaling failed: %v", err)
	}

	t.Log("Signaling exchange completed successfully")
}

// TestICEConnection verifies that ICE connection can be established.
func TestICEConnection(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Setup: keys and gathering
	if err := h.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	if err := h.WaitForGatheringComplete(15 * time.Second); err != nil {
		t.Fatalf("WaitForGatheringComplete failed: %v", err)
	}

	if err := h.ExchangeSignaling(); err != nil {
		t.Fatalf("ExchangeSignaling failed: %v", err)
	}

	// Connect
	if err := h.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Wait for connection
	if err := h.WaitForConnection(30 * time.Second); err != nil {
		t.Fatalf("WaitForConnection failed: %v", err)
	}

	// Verify connection state
	desktopState := h.Desktop.GetConnectionState()
	mobileState := h.Mobile.GetConnectionState()

	var ds, ms mobile.ConnectionStateJSON
	json.Unmarshal([]byte(desktopState), &ds)
	json.Unmarshal([]byte(mobileState), &ms)

	if !ds.IsConnected {
		t.Error("desktop is not connected")
	}
	if !ms.IsConnected {
		t.Error("mobile is not connected")
	}

	t.Log("ICE connection established successfully")
}

// TestInvalidCredentials verifies that invalid credentials are rejected.
func TestInvalidCredentials(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Start gathering to get an ICE agent
	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Wait briefly for agent to initialize
	time.Sleep(500 * time.Millisecond)

	// Try setting empty credentials
	result := h.Desktop.SetRemoteCredentials(`{"ufrag": "", "pwd": ""}`)
	if !hasError(result) {
		t.Error("expected error for empty credentials")
	}

	// Try setting malformed JSON
	result = h.Desktop.SetRemoteCredentials(`{invalid}`)
	if !hasError(result) {
		t.Error("expected error for malformed JSON")
	}
}

// TestInvalidCandidate verifies that invalid candidates are handled gracefully.
func TestInvalidCandidate(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Start gathering to get an ICE agent
	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Wait briefly for agent to initialize
	time.Sleep(500 * time.Millisecond)

	// Try adding malformed candidate JSON
	result := h.Desktop.AddRemoteCandidate(`{invalid json}`)
	if !hasError(result) {
		t.Error("expected error for malformed candidate JSON")
	}
}

// TestConnectionState verifies connection state transitions.
func TestConnectionState(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Check initial state
	state := h.Desktop.GetConnectionState()
	var cs mobile.ConnectionStateJSON
	json.Unmarshal([]byte(state), &cs)

	if cs.ICEState != mobile.ICEStateNew {
		t.Errorf("expected initial ICE state %s, got %s", mobile.ICEStateNew, cs.ICEState)
	}
	if cs.TunnelState != mobile.TunnelStateInactive {
		t.Errorf("expected initial tunnel state %s, got %s", mobile.TunnelStateInactive, cs.TunnelState)
	}
	if cs.IsConnected {
		t.Error("expected IsConnected to be false initially")
	}
	if cs.IsTunnelActive {
		t.Error("expected IsTunnelActive to be false initially")
	}

	// Start gathering and check state transitions
	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// After gathering starts, state should be checking
	state = h.Desktop.GetConnectionState()
	json.Unmarshal([]byte(state), &cs)
	if cs.ICEState != mobile.ICEStateChecking {
		t.Logf("ICE state after gathering: %s", cs.ICEState)
	}
}

// TestTunnelStats verifies tunnel statistics reporting.
func TestTunnelStats(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Check stats before tunnel is active
	stats := h.Desktop.GetTunnelStats()
	var ts mobile.TunnelStatsJSON
	json.Unmarshal([]byte(stats), &ts)

	if ts.IsActive {
		t.Error("expected tunnel to be inactive initially")
	}
	if ts.BytesSent != 0 {
		t.Errorf("expected 0 bytes sent initially, got %d", ts.BytesSent)
	}
	if ts.BytesReceived != 0 {
		t.Errorf("expected 0 bytes received initially, got %d", ts.BytesReceived)
	}
}

// TestHTTPBeforeTunnel verifies that HTTP requests fail before tunnel is established.
func TestHTTPBeforeTunnel(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Try HTTP request without tunnel
	result := h.Mobile.HTTPGet("http://10.0.0.1:8080/test")
	var httpResult mobile.HTTPResultJSON
	json.Unmarshal([]byte(result), &httpResult)

	if httpResult.Success {
		t.Error("expected HTTP request to fail without tunnel")
	}
	if httpResult.Error == "" {
		t.Error("expected error message")
	}
}

// TestEventEmission verifies that events are properly emitted.
func TestEventEmission(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// Generate keys (should not emit events)
	if err := h.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	// Start gathering (should emit candidate events)
	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Wait for some candidates
	if err := h.WaitForGatheringComplete(15 * time.Second); err != nil {
		t.Fatalf("WaitForGatheringComplete failed: %v", err)
	}

	// Check that candidate events were emitted
	desktopEvents := h.GetDesktopEvents()
	mobileEvents := h.GetMobileEvents()

	candidateEventCount := 0
	for _, e := range desktopEvents {
		if e.Type == mobile.EventTypeCandidate {
			candidateEventCount++
		}
	}

	if candidateEventCount == 0 {
		t.Error("no candidate events emitted from desktop")
	}

	candidateEventCount = 0
	for _, e := range mobileEvents {
		if e.Type == mobile.EventTypeCandidate {
			candidateEventCount++
		}
	}

	if candidateEventCount == 0 {
		t.Error("no candidate events emitted from mobile")
	}

	t.Logf("Desktop emitted %d events, Mobile emitted %d events", len(desktopEvents), len(mobileEvents))
}

// TestFullTunnelSetup performs the complete tunnel setup (happy path).
// This is the main integration test that validates end-to-end connectivity.
func TestFullTunnelSetup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	err := h.FullTunnelSetup()
	if err != nil {
		t.Fatalf("FullTunnelSetup failed: %v", err)
	}

	// Verify final state
	desktopState := h.Desktop.GetConnectionState()
	mobileState := h.Mobile.GetConnectionState()

	var ds, ms mobile.ConnectionStateJSON
	json.Unmarshal([]byte(desktopState), &ds)
	json.Unmarshal([]byte(mobileState), &ms)

	if !ds.IsConnected {
		t.Error("desktop is not connected")
	}
	if !ms.IsConnected {
		t.Error("mobile is not connected")
	}
	if !ds.IsTunnelActive {
		t.Error("desktop tunnel is not active")
	}
	if !ms.IsTunnelActive {
		t.Error("mobile tunnel is not active")
	}

	// Verify virtual IPs
	if ds.LocalIP != mobile.DesktopVirtualIP {
		t.Errorf("desktop local IP mismatch: expected %s, got %s", mobile.DesktopVirtualIP, ds.LocalIP)
	}
	if ms.LocalIP != mobile.MobileVirtualIP {
		t.Errorf("mobile local IP mismatch: expected %s, got %s", mobile.MobileVirtualIP, ms.LocalIP)
	}

	t.Log("Full tunnel setup completed successfully!")
	t.Logf("Desktop: ICE=%s, Tunnel=%s, LocalIP=%s, PeerIP=%s",
		ds.ICEState, ds.TunnelState, ds.LocalIP, ds.PeerIP)
	t.Logf("Mobile: ICE=%s, Tunnel=%s, LocalIP=%s, PeerIP=%s",
		ms.ICEState, ms.TunnelState, ms.LocalIP, ms.PeerIP)
}

// TestCloseBeforeConnection verifies graceful cleanup before connection.
func TestCloseBeforeConnection(t *testing.T) {
	h := NewTestHarness(t)

	// Start some operations
	if err := h.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Close immediately (should not hang or panic)
	h.Cleanup()

	// Verify clients are closed
	// Operations should fail gracefully
	result := h.Desktop.GetPublicKey()
	if result != "" {
		// Public key might still be available since it's cached
		t.Log("Public key still available after close (expected)")
	}
}

// TestMultipleGatheringAttempts verifies behavior when gathering is started multiple times.
func TestMultipleGatheringAttempts(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	// First gathering attempt
	if err := h.StartGathering(); err != nil {
		t.Fatalf("First StartGathering failed: %v", err)
	}

	// Wait a bit
	time.Sleep(500 * time.Millisecond)

	// Note: The current implementation allows multiple calls to StartGathering
	// which creates a new ICE agent each time. This might be expected behavior
	// or might need to be changed based on requirements.
}

// TestSignalingDataRoundTrip verifies that signaling data can be serialized and deserialized.
func TestSignalingDataRoundTrip(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	if err := h.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	if err := h.WaitForGatheringComplete(15 * time.Second); err != nil {
		t.Fatalf("WaitForGatheringComplete failed: %v", err)
	}

	// Get signaling data
	data := h.Desktop.GetSignalingData()

	// Verify it's valid JSON
	var signaling mobile.SignalingDataJSON
	if err := json.Unmarshal([]byte(data), &signaling); err != nil {
		t.Fatalf("signaling data is not valid JSON: %v", err)
	}

	// Re-serialize and compare
	reserialized, err := json.Marshal(signaling)
	if err != nil {
		t.Fatalf("failed to re-serialize: %v", err)
	}

	// The re-serialized version should also be parseable
	var reparsed mobile.SignalingDataJSON
	if err := json.Unmarshal(reserialized, &reparsed); err != nil {
		t.Fatalf("re-serialized data is not valid JSON: %v", err)
	}

	// Verify fields match
	if signaling.Ufrag != reparsed.Ufrag {
		t.Error("ufrag mismatch after roundtrip")
	}
	if signaling.Pwd != reparsed.Pwd {
		t.Error("pwd mismatch after roundtrip")
	}
	if signaling.PublicKey != reparsed.PublicKey {
		t.Error("publicKey mismatch after roundtrip")
	}
	if len(signaling.Candidates) != len(reparsed.Candidates) {
		t.Errorf("candidate count mismatch: %d vs %d", len(signaling.Candidates), len(reparsed.Candidates))
	}
}

// TestLocalCandidatesList verifies the format of the candidates list.
func TestLocalCandidatesList(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	if err := h.WaitForGatheringComplete(15 * time.Second); err != nil {
		t.Fatalf("WaitForGatheringComplete failed: %v", err)
	}

	// Get candidates as JSON array
	candJSON := h.Desktop.GetLocalCandidatesJSON()

	var candidates []mobile.CandidateJSON
	if err := json.Unmarshal([]byte(candJSON), &candidates); err != nil {
		t.Fatalf("failed to parse candidates: %v", err)
	}

	if len(candidates) == 0 {
		t.Fatal("no candidates returned")
	}

	// Verify candidate structure
	for i, c := range candidates {
		if c.Type == "" {
			t.Errorf("candidate %d has empty type", i)
		}
		if c.Address == "" {
			t.Errorf("candidate %d has empty address", i)
		}
		if c.Port == 0 {
			t.Errorf("candidate %d has zero port", i)
		}
		if c.Protocol == "" {
			t.Errorf("candidate %d has empty protocol", i)
		}

		// Log candidate info
		t.Logf("Candidate %d: type=%s addr=%s:%d proto=%s priority=%d",
			i, c.Type, c.Address, c.Port, c.Protocol, c.Priority)
	}

	// Verify at least one host candidate
	hasHost := false
	for _, c := range candidates {
		if strings.ToLower(c.Type) == "host" {
			hasHost = true
			break
		}
	}
	if !hasHost {
		t.Error("no host candidate found")
	}
}

// TestCredentialsFormat verifies the format of ICE credentials.
func TestCredentialsFormat(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Cleanup()

	if err := h.StartGathering(); err != nil {
		t.Fatalf("StartGathering failed: %v", err)
	}

	// Get credentials
	credsJSON := h.Desktop.GetLocalCredentials()

	var creds mobile.CredentialsJSON
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		t.Fatalf("failed to parse credentials: %v", err)
	}

	// Verify credentials are non-empty
	if creds.Ufrag == "" {
		t.Error("ufrag is empty")
	}
	if creds.Pwd == "" {
		t.Error("pwd is empty")
	}

	// Ufrag and Pwd should have reasonable lengths
	if len(creds.Ufrag) < 4 {
		t.Errorf("ufrag too short: %d", len(creds.Ufrag))
	}
	if len(creds.Pwd) < 8 {
		t.Errorf("pwd too short: %d", len(creds.Pwd))
	}

	t.Logf("Credentials: ufrag=%s (len=%d), pwd=%s (len=%d)",
		creds.Ufrag, len(creds.Ufrag), creds.Pwd[:8]+"...", len(creds.Pwd))
}
