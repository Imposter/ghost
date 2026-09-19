// Package testutil provides shared test utilities for integration testing.
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Candidate represents an ICE candidate for testing.
type Candidate struct {
	Type       string
	Protocol   string
	Address    string
	Port       int
	Priority   uint32
	Foundation string
	Component  int
}

// Credentials holds ICE credentials for a peer.
type Credentials struct {
	Ufrag string
	Pwd   string
}

// MockSignalingChannel provides in-memory candidate and credential exchange
// for testing ICE connections without a real signaling server.
type MockSignalingChannel struct {
	mu               sync.RWMutex
	peerACands       []*Candidate
	peerBCands       []*Candidate
	peerACredentials *Credentials
	peerBCredentials *Credentials

	// Channels for notification
	peerAReady chan struct{}
	peerBReady chan struct{}

	logger *slog.Logger
}

// NewMockSignalingChannel creates a new mock signaling channel.
func NewMockSignalingChannel(logger *slog.Logger) *MockSignalingChannel {
	if logger == nil {
		logger = slog.Default()
	}

	return &MockSignalingChannel{
		peerACands: make([]*Candidate, 0),
		peerBCands: make([]*Candidate, 0),
		peerAReady: make(chan struct{}),
		peerBReady: make(chan struct{}),
		logger:     logger.With("component", "mock_signaling"),
	}
}

// SendCandidateFromA sends a candidate from peer A to peer B.
func (m *MockSignalingChannel) SendCandidateFromA(cand *Candidate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerACands = append(m.peerACands, cand)
	m.logger.Debug("Candidate sent from A", "type", cand.Type, "address", cand.Address)
}

// SendCandidateFromB sends a candidate from peer B to peer A.
func (m *MockSignalingChannel) SendCandidateFromB(cand *Candidate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerBCands = append(m.peerBCands, cand)
	m.logger.Debug("Candidate sent from B", "type", cand.Type, "address", cand.Address)
}

// GetCandidatesForA returns all candidates that peer A should add (from peer B).
func (m *MockSignalingChannel) GetCandidatesForA() []*Candidate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Make a copy
	cands := make([]*Candidate, len(m.peerBCands))
	copy(cands, m.peerBCands)
	return cands
}

// GetCandidatesForB returns all candidates that peer B should add (from peer A).
func (m *MockSignalingChannel) GetCandidatesForB() []*Candidate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Make a copy
	cands := make([]*Candidate, len(m.peerACands))
	copy(cands, m.peerACands)
	return cands
}

// SendCredentialsFromA sends ICE credentials from peer A.
func (m *MockSignalingChannel) SendCredentialsFromA(ufrag, pwd string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerACredentials = &Credentials{
		Ufrag: ufrag,
		Pwd:   pwd,
	}
	m.logger.Debug("Credentials sent from A", "ufrag", ufrag)

	// Signal that peer A is ready
	select {
	case <-m.peerAReady:
		// Already closed
	default:
		close(m.peerAReady)
	}
}

// SendCredentialsFromB sends ICE credentials from peer B.
func (m *MockSignalingChannel) SendCredentialsFromB(ufrag, pwd string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerBCredentials = &Credentials{
		Ufrag: ufrag,
		Pwd:   pwd,
	}
	m.logger.Debug("Credentials sent from B", "ufrag", ufrag)

	// Signal that peer B is ready
	select {
	case <-m.peerBReady:
		// Already closed
	default:
		close(m.peerBReady)
	}
}

// GetCredentialsForA returns the credentials peer A should use (from peer B).
// Blocks until peer B has sent credentials or context is canceled.
func (m *MockSignalingChannel) GetCredentialsForA(ctx context.Context) (*Credentials, error) {
	select {
	case <-m.peerBReady:
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.peerBCredentials == nil {
			return nil, fmt.Errorf("peer B credentials not available")
		}
		return m.peerBCredentials, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetCredentialsForB returns the credentials peer B should use (from peer A).
// Blocks until peer A has sent credentials or context is canceled.
func (m *MockSignalingChannel) GetCredentialsForB(ctx context.Context) (*Credentials, error) {
	select {
	case <-m.peerAReady:
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.peerACredentials == nil {
			return nil, fmt.Errorf("peer A credentials not available")
		}
		return m.peerACredentials, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
