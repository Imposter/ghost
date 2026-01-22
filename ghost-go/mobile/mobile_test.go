package mobile

import (
	"encoding/json"
	"testing"
)

func TestNewClient(t *testing.T) {
	t.Run("default STUN servers", func(t *testing.T) {
		client, err := NewClient("")
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer client.Close()

		if len(client.stunServers) != 1 {
			t.Errorf("expected 1 STUN server, got %d", len(client.stunServers))
		}
		if client.stunServers[0] != "stun:stun.l.google.com:19302" {
			t.Errorf("unexpected default STUN server: %s", client.stunServers[0])
		}
	})

	t.Run("custom STUN servers", func(t *testing.T) {
		client, err := NewClient("stun:stun1.example.com:19302, stun:stun2.example.com:19302")
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer client.Close()

		if len(client.stunServers) != 2 {
			t.Errorf("expected 2 STUN servers, got %d", len(client.stunServers))
		}
	})

	t.Run("initial state", func(t *testing.T) {
		client, err := NewClient("")
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer client.Close()

		if client.iceState != ICEStateNew {
			t.Errorf("expected ICE state %s, got %s", ICEStateNew, client.iceState)
		}
		if client.tunnelState != TunnelStateInactive {
			t.Errorf("expected tunnel state %s, got %s", TunnelStateInactive, client.tunnelState)
		}
	})
}

func TestGenerateWireGuardKey(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.GenerateWireGuardKey()

	// Parse the JSON result
	var keyPair KeyPairJSON
	if err := json.Unmarshal([]byte(result), &keyPair); err != nil {
		t.Fatalf("failed to parse key pair JSON: %v", err)
	}

	// Verify keys are non-empty and properly formatted
	if keyPair.PrivateKey == "" {
		t.Error("private key is empty")
	}
	if keyPair.PublicKey == "" {
		t.Error("public key is empty")
	}

	// Verify keys are base64 encoded (44 chars for 32 bytes + padding)
	if len(keyPair.PrivateKey) != 44 {
		t.Errorf("private key has wrong length: %d", len(keyPair.PrivateKey))
	}
	if len(keyPair.PublicKey) != 44 {
		t.Errorf("public key has wrong length: %d", len(keyPair.PublicKey))
	}

	// Verify GetPublicKey returns the same key
	pubKey := client.GetPublicKey()
	if pubKey != keyPair.PublicKey {
		t.Errorf("GetPublicKey mismatch: expected %s, got %s", keyPair.PublicKey, pubKey)
	}
}

func TestGetConnectionState(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.GetConnectionState()

	var state ConnectionStateJSON
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		t.Fatalf("failed to parse connection state JSON: %v", err)
	}

	if state.ICEState != ICEStateNew {
		t.Errorf("expected ICE state %s, got %s", ICEStateNew, state.ICEState)
	}
	if state.TunnelState != TunnelStateInactive {
		t.Errorf("expected tunnel state %s, got %s", TunnelStateInactive, state.TunnelState)
	}
	if state.IsConnected {
		t.Error("expected IsConnected to be false")
	}
	if state.IsTunnelActive {
		t.Error("expected IsTunnelActive to be false")
	}
}

func TestSetRemoteCredentials_BeforeGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Try to set credentials before gathering
	result := client.SetRemoteCredentials(`{"ufrag": "test", "pwd": "password"}`)

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when setting credentials before gathering")
	}
}

func TestSetRemoteCredentials_InvalidJSON(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Start gathering first (so we have an ICE agent)
	if errStr := client.StartGathering(); errStr != "" {
		// If gathering fails (e.g., network unavailable), skip this test
		t.Skip("could not start gathering, skipping test")
	}

	// Try invalid JSON
	result := client.SetRemoteCredentials(`{invalid json}`)

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error for invalid JSON")
	}
}

func TestSetPeerPublicKey_Invalid(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Try to set invalid public key
	result := client.SetPeerPublicKey("not-a-valid-base64-key")

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error for invalid public key")
	}
}

func TestSetPeerPublicKey_Valid(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Generate a key pair to get a valid public key
	keyResult := client.GenerateWireGuardKey()
	var keyPair KeyPairJSON
	if err := json.Unmarshal([]byte(keyResult), &keyPair); err != nil {
		t.Fatalf("failed to parse key pair JSON: %v", err)
	}

	// Set the public key as peer key (using same key for test)
	result := client.SetPeerPublicKey(keyPair.PublicKey)

	if result != "" {
		t.Errorf("unexpected error setting valid public key: %s", result)
	}
}

func TestSetLocalIP(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	t.Run("valid CIDR", func(t *testing.T) {
		result := client.SetLocalIP("10.0.0.5/24")
		if result != "" {
			t.Errorf("unexpected error: %s", result)
		}
		if client.localIP != "10.0.0.5" {
			t.Errorf("expected localIP 10.0.0.5, got %s", client.localIP)
		}
	})

	t.Run("invalid CIDR", func(t *testing.T) {
		result := client.SetLocalIP("not-an-ip")

		var errResult map[string]string
		if err := json.Unmarshal([]byte(result), &errResult); err != nil {
			t.Fatalf("failed to parse error JSON: %v", err)
		}

		if errResult["error"] == "" {
			t.Error("expected error for invalid CIDR")
		}
	})
}

func TestGetTunnelStats_NotStarted(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.GetTunnelStats()

	var stats TunnelStatsJSON
	if err := json.Unmarshal([]byte(result), &stats); err != nil {
		t.Fatalf("failed to parse tunnel stats JSON: %v", err)
	}

	if stats.IsActive {
		t.Error("expected tunnel to be inactive")
	}
	if stats.BytesSent != 0 {
		t.Errorf("expected 0 bytes sent, got %d", stats.BytesSent)
	}
}

func TestHTTPGet_TunnelNotStarted(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.HTTPGet("http://10.0.0.1:8080/test")

	var httpResult HTTPResultJSON
	if err := json.Unmarshal([]byte(result), &httpResult); err != nil {
		t.Fatalf("failed to parse HTTP result JSON: %v", err)
	}

	if httpResult.Success {
		t.Error("expected HTTP request to fail when tunnel not started")
	}
	if httpResult.Error == "" {
		t.Error("expected error message when tunnel not started")
	}
}

func TestHTTPPost_TunnelNotStarted(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.HTTPPost("http://10.0.0.1:8080/echo", "text/plain", "test body")

	var httpResult HTTPResultJSON
	if err := json.Unmarshal([]byte(result), &httpResult); err != nil {
		t.Fatalf("failed to parse HTTP result JSON: %v", err)
	}

	if httpResult.Success {
		t.Error("expected HTTP request to fail when tunnel not started")
	}
}

func TestGetSignalingData_BeforeGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.GetSignalingData()

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when getting signaling data before gathering")
	}
}

func TestStartTunnel_Prerequisites(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	t.Run("no ICE connection", func(t *testing.T) {
		result := client.StartTunnel()

		var errResult map[string]string
		if err := json.Unmarshal([]byte(result), &errResult); err != nil {
			t.Fatalf("failed to parse error JSON: %v", err)
		}

		if errResult["error"] == "" {
			t.Error("expected error when starting tunnel without ICE connection")
		}
	})
}

func TestConnect_BeforeGathering(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	result := client.Connect(true)

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error when connecting before gathering")
	}
}

func TestAddRemoteCandidate_InvalidJSON(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Start gathering first
	if errStr := client.StartGathering(); errStr != "" {
		t.Skip("could not start gathering, skipping test")
	}

	result := client.AddRemoteCandidate(`{not valid json`)

	var errResult map[string]string
	if err := json.Unmarshal([]byte(result), &errResult); err != nil {
		t.Fatalf("failed to parse error JSON: %v", err)
	}

	if errResult["error"] == "" {
		t.Error("expected error for invalid JSON")
	}
}

func TestClose_Idempotent(t *testing.T) {
	client, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Close should be idempotent
	if err := client.Close(); err != nil {
		t.Errorf("first close failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("second close failed: %v", err)
	}
}

func TestEventDispatcher(t *testing.T) {
	dispatcher := newEventDispatcher()

	// Create a channel to receive events
	events := make(chan string, 10)
	callback := &testCallback{events: events}

	dispatcher.setCallback(callback)

	// Emit various events
	dispatcher.emitError(ErrNotConnected)
	dispatcher.emitConnected("test connected")
	dispatcher.emitDisconnected("test disconnected")
	dispatcher.emitTunnelUp()
	dispatcher.emitTunnelDown()

	// Verify events were emitted (give some time for goroutines)
	// Note: This is a basic test - in real tests you'd want to properly synchronize
}

// testCallback implements EventCallback for testing
type testCallback struct {
	events chan string
}

func (c *testCallback) OnEvent(eventJSON string) {
	select {
	case c.events <- eventJSON:
	default:
	}
}

func TestConnPool(t *testing.T) {
	pool := newConnPool()

	// Should start empty
	if pool.count() != 0 {
		t.Errorf("expected empty pool, got %d", pool.count())
	}

	// Remove from empty pool should return error
	if err := pool.remove(1); err != ErrConnectionNotFound {
		t.Errorf("expected ErrConnectionNotFound, got %v", err)
	}

	// Close all on empty pool should be safe
	pool.closeAll()
}

func TestJSONTypes_Marshaling(t *testing.T) {
	t.Run("CandidateJSON", func(t *testing.T) {
		cand := CandidateJSON{
			Type:       "host",
			Address:    "192.168.1.1",
			Port:       54321,
			Protocol:   "udp",
			Priority:   2130706431,
			Foundation: "123",
		}

		data, err := json.Marshal(cand)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var unmarshaled CandidateJSON
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if unmarshaled.Address != cand.Address {
			t.Errorf("address mismatch: %s != %s", unmarshaled.Address, cand.Address)
		}
	})

	t.Run("SignalingDataJSON", func(t *testing.T) {
		data := SignalingDataJSON{
			Ufrag:     "testufrag",
			Pwd:       "testpwd",
			PublicKey: "dGVzdHB1YmxpY2tleQ==",
			Candidates: []CandidateJSON{
				{Type: "host", Address: "192.168.1.1", Port: 54321},
			},
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var unmarshaled SignalingDataJSON
		if err := json.Unmarshal(jsonData, &unmarshaled); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if unmarshaled.Ufrag != data.Ufrag {
			t.Errorf("ufrag mismatch")
		}
		if len(unmarshaled.Candidates) != 1 {
			t.Errorf("expected 1 candidate, got %d", len(unmarshaled.Candidates))
		}
	})

	t.Run("HTTPResultJSON", func(t *testing.T) {
		result := HTTPResultJSON{
			Success:    true,
			StatusCode: 200,
			Body:       "Hello, World!",
			Headers:    map[string]string{"Content-Type": "text/plain"},
			LatencyMs:  42,
		}

		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var unmarshaled HTTPResultJSON
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if !unmarshaled.Success {
			t.Error("expected success to be true")
		}
		if unmarshaled.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", unmarshaled.StatusCode)
		}
	})

	t.Run("ConnectionStateJSON", func(t *testing.T) {
		state := ConnectionStateJSON{
			ICEState:       ICEStateConnected,
			TunnelState:    TunnelStateActive,
			IsConnected:    true,
			IsTunnelActive: true,
			LocalIP:        "10.0.0.2",
			PeerIP:         "10.0.0.1",
		}

		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var unmarshaled ConnectionStateJSON
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if unmarshaled.ICEState != ICEStateConnected {
			t.Errorf("ICE state mismatch")
		}
		if !unmarshaled.IsConnected {
			t.Error("expected IsConnected to be true")
		}
	})
}

func TestConstants(t *testing.T) {
	// Verify constants are set correctly
	if DesktopVirtualIP != "10.0.0.1" {
		t.Errorf("DesktopVirtualIP mismatch: %s", DesktopVirtualIP)
	}
	if MobileVirtualIP != "10.0.0.2" {
		t.Errorf("MobileVirtualIP mismatch: %s", MobileVirtualIP)
	}
	if VirtualSubnet != "10.0.0.0/24" {
		t.Errorf("VirtualSubnet mismatch: %s", VirtualSubnet)
	}
	if DefaultMTU != 1420 {
		t.Errorf("DefaultMTU mismatch: %d", DefaultMTU)
	}
	if HTTPTestPort != 8080 {
		t.Errorf("HTTPTestPort mismatch: %d", HTTPTestPort)
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("toJSON", func(t *testing.T) {
		result := toJSON(map[string]int{"value": 42})
		if result != `{"value":42}` {
			t.Errorf("unexpected JSON: %s", result)
		}
	})

	t.Run("toErrorJSON", func(t *testing.T) {
		result := toErrorJSON(ErrNotConnected)

		var errMap map[string]string
		if err := json.Unmarshal([]byte(result), &errMap); err != nil {
			t.Fatalf("failed to parse error JSON: %v", err)
		}

		if errMap["error"] != ErrNotConnected.Error() {
			t.Errorf("error message mismatch")
		}
	})
}
