package ice_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/internal/nettest"
	"github.com/Imposter/ghost/ghost-go/internal/testutil"
)

// TestICEConnection_TwoPeers tests a complete ICE connection between two peers.
// This is the main integration test that validates:
// - Candidate gathering
// - Candidate exchange via mock signaling
// - ICE connection establishment
// - Data transmission over ICE connection
func TestICEConnection_TwoPeers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := testutil.NewTestLogger(t)

	// Create mock signaling channel
	signaling := testutil.NewMockSignalingChannel(logger)

	// Loopback-only, host candidates, ephemeral ports: no external STUN and no
	// fixed ports, so the test triggers no firewall prompt (operator rule).
	configA := nettest.LoopbackICEConfig(0)
	configB := nettest.LoopbackICEConfig(0)

	// Create both agents
	agentA, err := ice.NewAgent(configA, logger.With("peer", "A"))
	require.NoError(t, err, "should create agent A")
	defer agentA.Close()

	agentB, err := ice.NewAgent(configB, logger.With("peer", "B"))
	require.NoError(t, err, "should create agent B")
	defer agentB.Close()

	// Exchange credentials
	ufragA, pwdA := agentA.LocalCredentials()
	ufragB, pwdB := agentB.LocalCredentials()

	signaling.SendCredentialsFromA(ufragA, pwdA)
	signaling.SendCredentialsFromB(ufragB, pwdB)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start gathering candidates for both peers
	var wg sync.WaitGroup
	wg.Add(2)

	// Peer A: Gather and send candidates
	go func() {
		defer wg.Done()
		candChan, err := agentA.GatherCandidates(ctx)
		if err != nil {
			t.Logf("Error gathering candidates for A: %v", err)
			return
		}
		for cand := range candChan {
			t.Logf("Peer A gathered: type=%s, address=%s:%d", cand.Type, cand.Address, cand.Port)
			signaling.SendCandidateFromA(toTestCandidate(cand))
		}
		t.Log("Peer A finished gathering")
	}()

	// Peer B: Gather and send candidates
	go func() {
		defer wg.Done()
		candChan, err := agentB.GatherCandidates(ctx)
		if err != nil {
			t.Logf("Error gathering candidates for B: %v", err)
			return
		}
		for cand := range candChan {
			t.Logf("Peer B gathered: type=%s, address=%s:%d", cand.Type, cand.Address, cand.Port)
			signaling.SendCandidateFromB(toTestCandidate(cand))
		}
		t.Log("Peer B finished gathering")
	}()

	// Wait for both peers to finish gathering
	wg.Wait()
	t.Log("Both peers finished gathering candidates")

	// Exchange candidates
	candsForA := signaling.GetCandidatesForA()
	candsForB := signaling.GetCandidatesForB()

	t.Logf("Exchanging candidates: A has %d, B has %d", len(candsForB), len(candsForA))

	// Add remote candidates to each peer
	for _, cand := range candsForA {
		err := agentA.AddRemoteCandidate(fromTestCandidate(cand))
		require.NoError(t, err, "agent A should accept remote candidate")
	}

	for _, cand := range candsForB {
		err := agentB.AddRemoteCandidate(fromTestCandidate(cand))
		require.NoError(t, err, "agent B should accept remote candidate")
	}

	// Set remote credentials
	remoteCreds, err := signaling.GetCredentialsForA(ctx)
	require.NoError(t, err, "should get credentials for A")
	err = agentA.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd)
	require.NoError(t, err, "should set remote credentials on A")

	remoteCreds, err = signaling.GetCredentialsForB(ctx)
	require.NoError(t, err, "should get credentials for B")
	err = agentB.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd)
	require.NoError(t, err, "should set remote credentials on B")

	t.Log("Credentials and candidates exchanged, starting connection establishment...")

	// Connect both peers concurrently
	var connA, connB net.Conn
	var errA, errB error

	wg.Add(2)

	go func() {
		defer wg.Done()
		connA, errA = agentA.Connect(ctx, true) // Agent A is controlling
		if errA == nil {
			t.Log("Peer A connected successfully")
		}
	}()

	go func() {
		defer wg.Done()
		connB, errB = agentB.Connect(ctx, false) // Agent B is controlled
		if errB == nil {
			t.Log("Peer B connected successfully")
		}
	}()

	wg.Wait()

	// Check connection results
	require.NoError(t, errA, "agent A should connect")
	require.NoError(t, errB, "agent B should connect")
	require.NotNil(t, connA, "connection A should not be nil")
	require.NotNil(t, connB, "connection B should not be nil")

	defer connA.Close()
	defer connB.Close()

	t.Log("✅ ICE connection established between both peers")

	// Test data transmission
	testMessage := []byte("Hello from Ghost-GO ICE integration test!")

	// Send from A to B
	var readMsg []byte
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, err := connA.Write(testMessage)
		assert.NoError(t, err, "should write to connection A")
		assert.Equal(t, len(testMessage), n, "should write all bytes")
		t.Logf("Peer A sent %d bytes", n)
	}()

	go func() {
		defer wg.Done()
		buf := make([]byte, 1500)
		connB.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := connB.Read(buf)
		assert.NoError(t, err, "should read from connection B")
		readMsg = buf[:n]
		t.Logf("Peer B received %d bytes", n)
	}()

	wg.Wait()

	assert.Equal(t, testMessage, readMsg, "received message should match sent message")
	t.Log("✅ Data transmission successful")

	// Test bidirectional transmission
	responseMsg := []byte("Response from peer B")

	wg.Add(2)

	go func() {
		defer wg.Done()
		n, err := connB.Write(responseMsg)
		assert.NoError(t, err, "should write response")
		assert.Equal(t, len(responseMsg), n, "should write all bytes")
		t.Logf("Peer B sent response %d bytes", n)
	}()

	var readResponse []byte
	go func() {
		defer wg.Done()
		buf := make([]byte, 1500)
		connA.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := connA.Read(buf)
		assert.NoError(t, err, "should read response")
		readResponse = buf[:n]
		t.Logf("Peer A received response %d bytes", n)
	}()

	wg.Wait()

	assert.Equal(t, responseMsg, readResponse, "received response should match sent response")
	t.Log("✅ Bidirectional communication successful")
}

// TestICEConnection_LocalOnly tests ICE connection on localhost only.
// This is faster and doesn't require external STUN servers.
func TestICEConnection_LocalOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := testutil.NewQuietTestLogger(t)

	signaling := testutil.NewMockSignalingChannel(logger)

	// Loopback-only host candidates on ephemeral ports (operator rule).
	configA := nettest.LoopbackICEConfig(0)
	configB := nettest.LoopbackICEConfig(0)

	agentA, err := ice.NewAgent(configA, logger.With("peer", "A"))
	require.NoError(t, err)
	defer agentA.Close()

	agentB, err := ice.NewAgent(configB, logger.With("peer", "B"))
	require.NoError(t, err)
	defer agentB.Close()

	// Exchange credentials
	ufragA, pwdA := agentA.LocalCredentials()
	ufragB, pwdB := agentB.LocalCredentials()
	signaling.SendCredentialsFromA(ufragA, pwdA)
	signaling.SendCredentialsFromB(ufragB, pwdB)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Gather candidates
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		candChan, err := agentA.GatherCandidates(ctx)
		if err != nil {
			return
		}
		for cand := range candChan {
			signaling.SendCandidateFromA(toTestCandidate(cand))
		}
	}()

	go func() {
		defer wg.Done()
		candChan, err := agentB.GatherCandidates(ctx)
		if err != nil {
			return
		}
		for cand := range candChan {
			signaling.SendCandidateFromB(toTestCandidate(cand))
		}
	}()

	wg.Wait()

	// Exchange
	for _, cand := range signaling.GetCandidatesForA() {
		agentA.AddRemoteCandidate(fromTestCandidate(cand))
	}
	for _, cand := range signaling.GetCandidatesForB() {
		agentB.AddRemoteCandidate(fromTestCandidate(cand))
	}

	remoteCreds, _ := signaling.GetCredentialsForA(ctx)
	agentA.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd)
	remoteCreds, _ = signaling.GetCredentialsForB(ctx)
	agentB.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd)

	// Connect
	wg.Add(2)
	var connA, connB net.Conn
	var errA, errB error

	go func() {
		defer wg.Done()
		connA, errA = agentA.Connect(ctx, true) // Agent A is controlling
	}()

	go func() {
		defer wg.Done()
		connB, errB = agentB.Connect(ctx, false) // Agent B is controlled
	}()

	wg.Wait()

	require.NoError(t, errA, "agent A should connect")
	require.NoError(t, errB, "agent B should connect")
	require.NotNil(t, connA, "should have connection A")
	require.NotNil(t, connB, "should have connection B")

	defer connA.Close()
	defer connB.Close()

	// Quick ping-pong test
	msg := []byte("ping")
	wg.Add(2)

	var received []byte
	go func() {
		defer wg.Done()
		connA.Write(msg)
	}()

	go func() {
		defer wg.Done()
		buf := make([]byte, 100)
		connB.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := connB.Read(buf)
		if err == nil {
			received = buf[:n]
		}
	}()

	wg.Wait()

	assert.Equal(t, msg, received, "should receive message")
	t.Log("✅ Local-only ICE connection works")
}

// Benchmark ICE connection establishment
func BenchmarkICEConnection(b *testing.B) {
	logger := testutil.NewQuietTestLogger(&testing.T{})

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// Use unique port offsets for each benchmark iteration
		portOffset := i * 2
		configA := ice.TestICEConfig(100 + portOffset)     // Ports 51100, 51102, ...
		configB := ice.TestICEConfig(100 + portOffset + 1) // Ports 51101, 51103, ...

		signaling := testutil.NewMockSignalingChannel(logger)
		agentA, errA := ice.NewAgent(configA, logger)
		if errA != nil {
			b.Fatalf("Failed to create agent A: %v", errA)
		}
		agentB, errB := ice.NewAgent(configB, logger)
		if errB != nil {
			b.Fatalf("Failed to create agent B: %v", errB)
		}

		ufragA, pwdA := agentA.LocalCredentials()
		ufragB, pwdB := agentB.LocalCredentials()
		signaling.SendCredentialsFromA(ufragA, pwdA)
		signaling.SendCredentialsFromB(ufragB, pwdB)

		ctx := context.Background()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			candChan, err := agentA.GatherCandidates(ctx)
			if err != nil {
				b.Logf("Failed to gather candidates for agent A: %v", err)
				return
			}
			for cand := range candChan {
				signaling.SendCandidateFromA(toTestCandidate(cand))
			}
		}()
		go func() {
			defer wg.Done()
			candChan, err := agentB.GatherCandidates(ctx)
			if err != nil {
				b.Logf("Failed to gather candidates for agent B: %v", err)
				return
			}
			for cand := range candChan {
				signaling.SendCandidateFromB(toTestCandidate(cand))
			}
		}()
		wg.Wait()

		for _, cand := range signaling.GetCandidatesForA() {
			if err := agentA.AddRemoteCandidate(fromTestCandidate(cand)); err != nil {
				b.Logf("Failed to add candidate to agent A: %v", err)
			}
		}
		for _, cand := range signaling.GetCandidatesForB() {
			if err := agentB.AddRemoteCandidate(fromTestCandidate(cand)); err != nil {
				b.Logf("Failed to add candidate to agent B: %v", err)
			}
		}

		remoteCreds, err := signaling.GetCredentialsForA(ctx)
		if err != nil {
			b.Logf("Failed to get credentials for A: %v", err)
		} else {
			if err := agentA.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd); err != nil {
				b.Logf("Failed to set remote credentials for A: %v", err)
			}
		}

		remoteCreds, err = signaling.GetCredentialsForB(ctx)
		if err != nil {
			b.Logf("Failed to get credentials for B: %v", err)
		} else {
			if err := agentB.SetRemoteCredentials(remoteCreds.Ufrag, remoteCreds.Pwd); err != nil {
				b.Logf("Failed to set remote credentials for B: %v", err)
			}
		}

		b.StartTimer()

		// This is what we're benchmarking
		wg.Add(2)
		var connA, connB net.Conn
		go func() {
			defer wg.Done()
			connA, errA = agentA.Connect(ctx, true)
		}()
		go func() {
			defer wg.Done()
			connB, errB = agentB.Connect(ctx, false)
		}()
		wg.Wait()

		b.StopTimer()

		// Check for errors (but don't fail benchmark if occasional timeout)
		if errA != nil {
			b.Logf("Agent A connection error: %v", errA)
		}
		if errB != nil {
			b.Logf("Agent B connection error: %v", errB)
		}

		if connA != nil {
			if err := connA.Close(); err != nil {
				b.Logf("Failed to close connA: %v", err)
			}
		}
		if connB != nil {
			if err := connB.Close(); err != nil {
				b.Logf("Failed to close connB: %v", err)
			}
		}

		if err := agentA.Close(); err != nil {
			b.Logf("Failed to close agent A: %v", err)
		}
		if err := agentB.Close(); err != nil {
			b.Logf("Failed to close agent B: %v", err)
		}
	}
}

// Helper functions to convert between ice.Candidate and testutil.Candidate

func toTestCandidate(c *ice.Candidate) *testutil.Candidate {
	return &testutil.Candidate{
		Type:       string(c.Type),
		Protocol:   c.Protocol,
		Address:    c.Address,
		Port:       c.Port,
		Priority:   c.Priority,
		Foundation: c.Foundation,
		Component:  1, // Default component ID
	}
}

func fromTestCandidate(c *testutil.Candidate) *ice.Candidate {
	return &ice.Candidate{
		Type:       ice.CandidateType(c.Type),
		Protocol:   c.Protocol,
		Address:    c.Address,
		Port:       c.Port,
		Priority:   c.Priority,
		Foundation: c.Foundation,
	}
}
