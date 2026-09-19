package ice

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Imposter/ghost/ghost-go/internal/testutil"
)

func TestNewAgent(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	config := TestICEConfig(20) // loopback host candidates only

	agent, err := NewAgent(config, logger)
	require.NoError(t, err, "should create agent")
	require.NotNil(t, agent, "agent should not be nil")

	defer agent.Close()

	// Verify we can get credentials
	ufrag, pwd := agent.LocalCredentials()
	assert.NotEmpty(t, ufrag, "should have ufrag")
	assert.NotEmpty(t, pwd, "should have password")
}

func TestAgentGatherCandidates(t *testing.T) {
	logger := testutil.NewTestLogger(t)

	config := TestICEConfig(21) // loopback host candidates, ephemeral port

	agent, err := NewAgent(config, logger)
	require.NoError(t, err, "should create agent")
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Gather candidates
	candChan, err := agent.GatherCandidates(ctx)
	require.NoError(t, err, "should start gathering")

	var candidates []*Candidate
	done := make(chan struct{})

	go func() {
		for cand := range candChan {
			candidates = append(candidates, cand)
			t.Logf("Gathered candidate: type=%s, address=%s:%d, protocol=%s",
				cand.Type, cand.Address, cand.Port, cand.Protocol)
		}
		close(done)
	}()

	// Wait for gathering to complete
	select {
	case <-done:
		t.Logf("Gathering complete, found %d candidates", len(candidates))
	case <-ctx.Done():
		t.Fatal("Timeout waiting for candidate gathering")
	}

	// Should have at least a host candidate
	assert.NotEmpty(t, candidates, "should gather at least one candidate")

	// Verify we have a host candidate
	hasHost := false
	for _, cand := range candidates {
		if cand.Type == CandidateTypeHost {
			hasHost = true
			break
		}
	}
	assert.True(t, hasHost, "should have at least one host candidate")
}

func TestAgentAddRemoteCandidate(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	config := TestICEConfig(22) // Port 51022

	agent, err := NewAgent(config, logger)
	require.NoError(t, err, "should create agent")
	defer agent.Close()

	// Add a fake remote candidate (this won't connect, just testing the API)
	remoteCand := &Candidate{
		Type:       CandidateTypeHost,
		Protocol:   ProtocolUDP,
		Address:    "192.168.1.100",
		Port:       54321,
		Priority:   100,
		Foundation: "test",
	}

	err = agent.AddRemoteCandidate(remoteCand)
	assert.NoError(t, err, "should accept remote candidate")
}

func TestAgentCredentials(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	config := TestICEConfig(23) // Port 51023

	agent, err := NewAgent(config, logger)
	require.NoError(t, err, "should create agent")
	defer agent.Close()

	ufrag, pwd := agent.LocalCredentials()

	// Verify credentials are generated
	assert.NotEmpty(t, ufrag, "ufrag should not be empty")
	assert.NotEmpty(t, pwd, "password should not be empty")
	assert.Greater(t, len(ufrag), 4, "ufrag should be at least 4 characters")
	assert.Greater(t, len(pwd), 20, "password should be at least 22 characters")
}

func TestAgentSetRemoteCredentials(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	config := TestICEConfig(24) // Port 51024

	agent, err := NewAgent(config, logger)
	require.NoError(t, err, "should create agent")
	defer agent.Close()

	// Set remote credentials
	err = agent.SetRemoteCredentials("testufrag", "testpassword123456789012")
	assert.NoError(t, err, "should set remote credentials")
}
