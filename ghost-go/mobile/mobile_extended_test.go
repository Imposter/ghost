package mobile

import (
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// CleanupConnectionResources Tests
// =============================================================================

func TestCleanupConnectionResources_NilResources(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Should not panic when all resources are nil
	client.mu.Lock()
	client.cleanupConnectionResources()
	client.mu.Unlock()

	// Verify state after cleanup
	client.mu.RLock()
	defer client.mu.RUnlock()

	if client.device != nil {
		t.Error("device should be nil after cleanup")
	}
	if client.iceConn != nil {
		t.Error("iceConn should be nil after cleanup")
	}
	if client.iceBind != nil {
		t.Error("iceBind should be nil after cleanup")
	}
}

// =============================================================================
// SetSignalingData Tests
// =============================================================================

func TestSetSignalingData_Valid(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Start gathering first
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	// Wait for gathering to start
	time.Sleep(100 * time.Millisecond)

	// Create valid signaling data
	keyResult := client.GenerateWireGuardKey()
	var keyPair KeyPairJSON
	json.Unmarshal([]byte(keyResult), &keyPair)

	signalingData := SignalingDataJSON{
		Ufrag:     "testufrag1234567",
		Pwd:       "testpwd12345678901234567890",
		PublicKey: keyPair.PublicKey,
		Candidates: []CandidateJSON{
			{
				Type:       "host",
				Address:    "192.168.1.100",
				Port:       54321,
				Protocol:   "udp",
				Priority:   2130706431,
				Foundation: "123",
			},
		},
	}

	jsonData, _ := json.Marshal(signalingData)
	result := client.SetSignalingData(string(jsonData))

	if result != "" {
		t.Errorf("SetSignalingData failed: %s", result)
	}

	// Verify peer key was set
	client.mu.RLock()
	peerKeySet := client.peerKey != nil
	client.mu.RUnlock()

	if !peerKeySet {
		t.Error("peer key should be set after SetSignalingData")
	}
}

func TestSetSignalingData_MissingCredentials(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Start gathering first
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	// Missing ufrag
	signalingData := SignalingDataJSON{
		Ufrag: "",
		Pwd:   "testpwd12345678901234567890",
	}

	jsonData, _ := json.Marshal(signalingData)
	result := client.SetSignalingData(string(jsonData))

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error for missing credentials")
	}
}

func TestSetSignalingData_InvalidPublicKey(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Start gathering first
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	signalingData := SignalingDataJSON{
		Ufrag:     "testufrag1234567",
		Pwd:       "testpwd12345678901234567890",
		PublicKey: "not-a-valid-key",
	}

	jsonData, _ := json.Marshal(signalingData)
	result := client.SetSignalingData(string(jsonData))

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error for invalid public key")
	}
}

func TestSetSignalingData_BeforeGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.SetSignalingData(`{"ufrag":"test","pwd":"password"}`)

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when setting signaling data before gathering")
	}
}

// =============================================================================
// GetPublicKey Tests
// =============================================================================

func TestGetPublicKey_BeforeGeneration(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	pubKey := client.GetPublicKey()
	if pubKey != "" {
		t.Errorf("expected empty public key before generation, got %s", pubKey)
	}
}

func TestGetPublicKey_AfterMultipleGenerations(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Generate first key pair
	result1 := client.GenerateWireGuardKey()
	var keyPair1 KeyPairJSON
	json.Unmarshal([]byte(result1), &keyPair1)

	pubKey1 := client.GetPublicKey()
	if pubKey1 != keyPair1.PublicKey {
		t.Error("GetPublicKey should return the generated public key")
	}

	// Generate second key pair
	result2 := client.GenerateWireGuardKey()
	var keyPair2 KeyPairJSON
	json.Unmarshal([]byte(result2), &keyPair2)

	pubKey2 := client.GetPublicKey()
	if pubKey2 != keyPair2.PublicKey {
		t.Error("GetPublicKey should return the latest generated public key")
	}

	// Keys should be different
	if pubKey1 == pubKey2 {
		t.Error("regenerated keys should be different")
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestClose_AfterGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Start gathering
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	// Wait a bit for gathering to start
	time.Sleep(100 * time.Millisecond)

	// Close should clean everything up
	if err := client.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Verify state
	client.mu.RLock()
	defer client.mu.RUnlock()

	if !client.closed {
		t.Error("client should be marked as closed")
	}
	if client.iceState != ICEStateClosed {
		t.Errorf("expected ICE state %s, got %s", ICEStateClosed, client.iceState)
	}
}

func TestClose_EmitsEvents(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	events := make(chan string, 10)
	callback := &testCallback{events: events}
	client.SetEventCallback(callback)

	// Close
	client.Close()

	// Check that events were emitted
	timeout := time.After(500 * time.Millisecond)
	hasDisconnectEvent := false

	for {
		select {
		case event := <-events:
			var parsed map[string]interface{}
			if json.Unmarshal([]byte(event), &parsed) == nil {
				if parsed["type"] == "disconnected" {
					hasDisconnectEvent = true
				}
			}
		case <-timeout:
			if !hasDisconnectEvent {
				t.Error("expected disconnect event on close")
			}
			return
		}
	}
}

// =============================================================================
// StartTunnel Prerequisite Tests
// =============================================================================

func TestStartTunnel_NoKeys(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Manually set iceConn to bypass that check (simulating connected state)
	// This is testing the keys prerequisite
	result := client.StartTunnel()

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when starting tunnel without keys")
	}
}

func TestStartTunnel_AfterClose(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	client.Close()

	result := client.StartTunnel()

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when starting tunnel after close")
	}
}

// =============================================================================
// Resource Leak Tests
// =============================================================================

func TestResourceLeak_RepeatedClientCreation(t *testing.T) {
	// Force GC to get a clean baseline
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	initialGoroutines := runtime.NumGoroutine()

	// Create and close many clients
	for i := 0; i < 10; i++ {
		client, err := NewClient("")
		if err != nil {
			t.Fatalf("NewClient failed on iteration %d: %v", i, err)
		}
		client.Close()
	}

	// Force GC to clean up
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()

	// Allow for some variance (background goroutines, etc.)
	// but shouldn't have leaked significant goroutines
	leaked := finalGoroutines - initialGoroutines
	if leaked > 5 {
		t.Errorf("potential goroutine leak: started with %d, ended with %d (leaked %d)",
			initialGoroutines, finalGoroutines, leaked)
	}
}

func TestResourceLeak_RepeatedGathering(t *testing.T) {
	// Skip if we can't do networking
	testClient, err := NewClient("")
	if err != nil {
		t.Skip("cannot create client")
	}
	if errStr := testClient.StartGathering(); errStr != "" {
		testClient.Close()
		t.Skip("cannot start gathering, skipping leak test")
	}
	testClient.Close()

	// Force GC to get a clean baseline
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	initialGoroutines := runtime.NumGoroutine()

	// Create, gather, and close multiple times
	for i := 0; i < 5; i++ {
		client, err := NewClient("")
		if err != nil {
			t.Fatalf("NewClient failed on iteration %d: %v", i, err)
		}

		if errStr := client.StartGathering(); errStr != "" {
			client.Close()
			continue
		}

		// Wait a bit for gathering
		time.Sleep(50 * time.Millisecond)

		client.Close()
	}

	// Force GC and wait for goroutines to clean up
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()

	leaked := finalGoroutines - initialGoroutines
	if leaked > 10 {
		t.Errorf("potential goroutine leak in gathering: started with %d, ended with %d (leaked %d)",
			initialGoroutines, finalGoroutines, leaked)
	}
}

func TestResourceLeak_RepeatedClose(t *testing.T) {
	// Force GC to get a clean baseline
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	initialGoroutines := runtime.NumGoroutine()

	// Create and close clients repeatedly
	for i := 0; i < 20; i++ {
		client, err := NewClient("")
		if err != nil {
			t.Fatalf("NewClient failed on iteration %d: %v", i, err)
		}
		client.GenerateWireGuardKey()
		client.Close()
	}

	// Force GC
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()

	leaked := finalGoroutines - initialGoroutines
	if leaked > 3 {
		t.Errorf("potential goroutine leak in close: started with %d, ended with %d (leaked %d)",
			initialGoroutines, finalGoroutines, leaked)
	}
}

func TestResourceLeak_KeyGeneration(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Generate many keys (should not leak memory or goroutines)
	for i := 0; i < 100; i++ {
		result := client.GenerateWireGuardKey()

		var keyPair KeyPairJSON
		if err := json.Unmarshal([]byte(result), &keyPair); err != nil {
			t.Fatalf("failed to parse key pair on iteration %d: %v", i, err)
		}

		if keyPair.PrivateKey == "" || keyPair.PublicKey == "" {
			t.Errorf("invalid keys on iteration %d", i)
		}
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestConcurrentAccess_GetConnectionState(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	const numGoroutines = 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				result := client.GetConnectionState()
				var state ConnectionStateJSON
				if err := json.Unmarshal([]byte(result), &state); err != nil {
					t.Errorf("failed to parse connection state: %v", err)
				}
			}
		}()
	}

	wg.Wait()
}

func TestConcurrentAccess_GetPublicKey(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Generate initial key
	client.GenerateWireGuardKey()

	var wg sync.WaitGroup
	const numGoroutines = 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = client.GetPublicKey()
			}
		}()
	}

	wg.Wait()
}

func TestConcurrentAccess_MixedOperations(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup

	// Concurrent GetConnectionState
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			client.GetConnectionState()
		}
	}()

	// Concurrent GetPublicKey
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			client.GetPublicKey()
		}
	}()

	// Concurrent GetTunnelStats
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			client.GetTunnelStats()
		}
	}()

	// Concurrent GenerateWireGuardKey
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			client.GenerateWireGuardKey()
		}
	}()

	wg.Wait()
}

// =============================================================================
// Event Dispatcher Tests
// =============================================================================

func TestEventDispatcher_AllEventTypes(t *testing.T) {
	dispatcher := newEventDispatcher()

	events := make(chan string, 20)
	callback := &testCallback{events: events}
	dispatcher.setCallback(callback)

	// Emit all event types
	dispatcher.emitError(ErrNotConnected)
	dispatcher.emitConnected("test")
	dispatcher.emitDisconnected("test")
	dispatcher.emitTunnelUp()
	dispatcher.emitTunnelDown()
	dispatcher.emitCandidate(&CandidateJSON{Type: "host", Address: "1.2.3.4", Port: 1234})
	dispatcher.emitStateChange(&ConnectionStateJSON{ICEState: ICEStateConnected})

	// Wait for events
	time.Sleep(100 * time.Millisecond)

	eventTypes := make(map[string]bool)

	// Drain the channel
drainLoop:
	for {
		select {
		case event := <-events:
			var parsed map[string]interface{}
			if json.Unmarshal([]byte(event), &parsed) == nil {
				if t, ok := parsed["type"].(string); ok {
					eventTypes[t] = true
				}
			}
		default:
			break drainLoop
		}
	}

	expectedTypes := []string{"error", "connected", "disconnected", "tunnel_up", "tunnel_down", "candidate", "state_change"}
	for _, expected := range expectedTypes {
		if !eventTypes[expected] {
			t.Errorf("missing event type: %s", expected)
		}
	}
}

func TestEventDispatcher_NilCallback(t *testing.T) {
	dispatcher := newEventDispatcher()

	// Should not panic with nil callback
	dispatcher.emitError(ErrNotConnected)
	dispatcher.emitConnected("test")
	dispatcher.emitDisconnected("test")
	dispatcher.emitTunnelUp()
	dispatcher.emitTunnelDown()
}

func TestEventDispatcher_SetCallbackMultipleTimes(t *testing.T) {
	dispatcher := newEventDispatcher()

	events1 := make(chan string, 5)
	callback1 := &testCallback{events: events1}

	events2 := make(chan string, 5)
	callback2 := &testCallback{events: events2}

	// Set first callback
	dispatcher.setCallback(callback1)
	dispatcher.emitConnected("first")

	time.Sleep(50 * time.Millisecond)

	// Set second callback
	dispatcher.setCallback(callback2)
	dispatcher.emitConnected("second")

	time.Sleep(50 * time.Millisecond)

	// First callback should have received first event
	select {
	case <-events1:
		// Good
	default:
		t.Error("first callback should have received event")
	}

	// Second callback should have received second event
	select {
	case <-events2:
		// Good
	default:
		t.Error("second callback should have received event")
	}
}

// =============================================================================
// State Transition Tests
// =============================================================================

func TestStateTransitions_InitialToGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Initial state
	if client.iceState != ICEStateNew {
		t.Errorf("expected initial ICE state %s, got %s", ICEStateNew, client.iceState)
	}

	// Start gathering
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	// State should be checking
	client.mu.RLock()
	state := client.iceState
	client.mu.RUnlock()

	if state != ICEStateChecking {
		t.Errorf("expected ICE state %s after gathering, got %s", ICEStateChecking, state)
	}
}

func TestStateTransitions_GatheringToClosed(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Start gathering
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	// Close
	client.Close()

	// State should be closed
	client.mu.RLock()
	state := client.iceState
	client.mu.RUnlock()

	if state != ICEStateClosed {
		t.Errorf("expected ICE state %s after close, got %s", ICEStateClosed, state)
	}
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestErrorHandling_InvalidSTUNServer(t *testing.T) {
	// Empty STUN server list should use default
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient with empty STUN should not fail: %v", err)
	}
	client.Close()

	// Invalid but parseable STUN server
	client2, err := NewClient("stun:invalid.example.com:19302")
	if err != nil {
		t.Fatalf("NewClient with unreachable STUN should not fail at creation: %v", err)
	}
	client2.Close()
}

func TestErrorHandling_GetLocalCredentialsBeforeGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.GetLocalCredentials()

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when getting credentials before gathering")
	}
}

func TestErrorHandling_GetLocalCandidatesBeforeGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.GetLocalCandidatesJSON()

	// Should return empty array, not an error
	var candidates []CandidateJSON
	if err := json.Unmarshal([]byte(result), &candidates); err != nil {
		t.Fatalf("failed to parse candidates: %v", err)
	}

	if len(candidates) != 0 {
		t.Errorf("expected empty candidates before gathering, got %d", len(candidates))
	}
}

// =============================================================================
// Logger Tests
// =============================================================================

func TestLoggerFunctions(t *testing.T) {
	// These should not panic
	LogInfo("test info message")
	LogDebug("test debug message")
	LogError("test error", ErrNotConnected)
	LogStackTrace("test stack trace")
}

// =============================================================================
// Memory Safety Tests
// =============================================================================

func TestMemorySafety_NilChecks(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// These should all handle nil internal state gracefully
	_ = client.GetConnectionState()
	_ = client.GetPublicKey()
	_ = client.GetTunnelStats()
	_ = client.GetLocalCandidatesJSON()
}

func TestMemorySafety_DoubleClose(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Multiple closes should not panic or error
	for i := 0; i < 5; i++ {
		if err := client.Close(); err != nil {
			t.Errorf("Close failed on iteration %d: %v", i, err)
		}
	}
}

func TestMemorySafety_OperationsAfterClose(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	client.Close()

	// All operations should return errors gracefully, not panic
	_ = client.GetConnectionState()
	_ = client.GetPublicKey()
	_ = client.GetTunnelStats()
	_ = client.GenerateWireGuardKey()
	_ = client.StartGathering()
	_ = client.Connect(true)
	_ = client.StartTunnel()
	_ = client.HTTPGet("http://example.com")
	_ = client.HTTPPost("http://example.com", "text/plain", "test")
}
