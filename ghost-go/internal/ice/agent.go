package ice

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/pion/ice/v3"
	"github.com/pion/stun/v2"
)

// Agent handles ICE connectivity establishment.
//
// # Thread Safety
//
// Agent is NOT thread-safe. The caller is responsible for synchronization
// when accessing an Agent from multiple goroutines. This follows Go's
// convention for internal building-block packages.
//
// Exception: The OnConnectionStateChange callback is thread-safe because
// it may be invoked from Pion's internal goroutines.
//
// # Resource Management
//
// Agent manages the following resources:
//   - Pion ICE agent (network sockets, goroutines for ICE processing)
//   - Candidate channel (goroutine for forwarding candidates)
//   - ICE connection (network socket)
//
// IMPORTANT: You MUST call Close() when done with the agent to release resources.
// Failure to do so will leak goroutines and network sockets.
//
// # Lifecycle
//
//  1. Create agent with NewAgent()
//  2. Register state change callback with OnConnectionStateChange() (optional)
//  3. Start gathering with GatherCandidates()
//  4. Exchange credentials and candidates with peer (out-of-band)
//  5. Set remote credentials with SetRemoteCredentials()
//  6. Add remote candidates with AddRemoteCandidate()
//  7. Connect with Connect()
//  8. Use the returned net.Conn for communication
//  9. Close with Close() - REQUIRED
type Agent interface {
	// GatherCandidates starts gathering local ICE candidates.
	// Returns a channel that emits candidates as they are discovered.
	// The channel is closed when gathering completes or the context is cancelled.
	//
	// Resource: Starts a background goroutine that stops when the channel closes.
	GatherCandidates(ctx context.Context) (<-chan *Candidate, error)

	// AddRemoteCandidate adds a remote candidate to the agent.
	// Returns ErrInvalidCandidate if the candidate is nil or malformed.
	// Returns ErrAlreadyClosed if the agent has been closed.
	AddRemoteCandidate(candidate *Candidate) error

	// SetRemoteCredentials sets the remote ICE credentials.
	// Both ufrag and pwd must be non-empty.
	// Returns ErrInvalidCredentials if either is empty.
	// Returns ErrAlreadyClosed if the agent has been closed.
	SetRemoteCredentials(ufrag, pwd string) error

	// LocalCredentials returns the local ICE credentials (ufrag, pwd).
	// These are generated when the agent is created and remain constant.
	LocalCredentials() (ufrag, pwd string)

	// Connect establishes the ICE connection.
	// If controlling is true, this peer acts as the controlling agent (Dial).
	// If false, this peer acts as the controlled agent (Accept).
	// Returns a net.Conn that can be used for communication.
	//
	// Prerequisites:
	//   - Remote credentials must be set via SetRemoteCredentials()
	//   - At least one remote candidate should be added via AddRemoteCandidate()
	//
	// Returns an error if:
	//   - Remote credentials are not set
	//   - Connection timeout expires (configured in ICEConfig)
	//   - Agent has been closed
	//
	// Resource: The returned connection is owned by the caller; close it when done.
	Connect(ctx context.Context, controlling bool) (net.Conn, error)

	// GetSelectedCandidatePair returns the selected candidate pair after connection.
	// Returns ErrNotConnected if Connect() hasn't been called or failed.
	// Returns ErrNoValidPair if no valid pair was selected.
	GetSelectedCandidatePair() (*CandidatePair, error)

	// OnConnectionStateChange sets a callback for ICE connection state changes.
	// This is useful for detecting disconnections from the remote peer.
	// The callback is invoked from a Pion goroutine; avoid blocking operations.
	//
	// States that indicate connection loss:
	//   - ConnectionStateDisconnected: Peer may be temporarily unreachable
	//   - ConnectionStateFailed: Connection has failed and won't recover
	//   - ConnectionStateClosed: Agent has been closed
	//
	// Note: This method IS thread-safe because the callback may be invoked
	// from Pion's internal goroutines concurrently with other operations.
	OnConnectionStateChange(callback ConnectionStateChangeCallback)

	// Close closes the agent and releases all resources.
	// This includes:
	//   - Stopping all ICE goroutines
	//   - Closing the ICE connection (if connected)
	//   - Releasing network sockets
	//
	// After Close(), all other methods return ErrAlreadyClosed.
	Close() error
}

// pionAgent implements Agent using Pion's ICE library.
//
// # Thread Safety
//
// This type is NOT thread-safe. The caller is responsible for synchronization.
// This follows Go's convention for internal building-block packages.
//
// Exception: The stateChangeCallback is protected by callbackMu because
// callbacks are invoked from Pion's internal goroutines, which are outside
// the caller's control.
type pionAgent struct {
	config *ICEConfig
	agent  *ice.Agent
	logger *slog.Logger

	// State tracking (NOT thread-safe - caller must synchronize)
	conn        net.Conn
	localUfrag  string
	localPwd    string
	remoteUfrag string
	remotePwd   string
	closed      bool

	// Candidate gathering
	candidateChan chan *Candidate
	gatherDone    chan struct{}

	// callbackMu protects stateChangeCallback because callbacks are invoked
	// from Pion's internal goroutines
	callbackMu sync.RWMutex
	// Connection state change callback
	stateChangeCallback ConnectionStateChangeCallback
}

// NewAgent creates a new ICE agent with the given configuration.
func NewAgent(config *ICEConfig, logger *slog.Logger) (Agent, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Create candidate channel for OnCandidate callback
	candidateChan := make(chan *Candidate, 10)
	gatherDone := make(chan struct{})

	// Build ICE agent configuration
	agentConfig := &ice.AgentConfig{
		NetworkTypes:        []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6},
		IncludeLoopback:     true, // Required for localhost testing
		KeepaliveInterval:   &config.KeepaliveInterval,
		DisconnectedTimeout: &config.DisconnectedTimeout,
		FailedTimeout:       &config.FailedTimeout,
	}

	// Set port range if specified (useful for firewall rules)
	if config.PortMin > 0 && config.PortMax > 0 {
		agentConfig.PortMin = config.PortMin
		agentConfig.PortMax = config.PortMax
		logger.Info("Using fixed port range for ICE", "min", config.PortMin, "max", config.PortMax)
	}

	// Add STUN servers
	for _, stunURL := range config.STUNServers {
		url, err := stun.ParseURI(stunURL)
		if err != nil {
			return nil, fmt.Errorf("invalid STUN URL %s: %w", stunURL, err)
		}
		agentConfig.Urls = append(agentConfig.Urls, url)
	}

	// Add TURN servers
	for _, turnServer := range config.TURNServers {
		for _, turnURL := range turnServer.URLs {
			url, err := stun.ParseURI(turnURL)
			if err != nil {
				return nil, fmt.Errorf("invalid TURN URL %s: %w", turnURL, err)
			}
			url.Username = turnServer.Username
			url.Password = turnServer.Password
			agentConfig.Urls = append(agentConfig.Urls, url)
		}
	}

	// Create Pion ICE agent
	agent, err := ice.NewAgent(agentConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create ICE agent: %w", err)
	}

	// Set OnCandidate callback to receive candidates as they're discovered
	if err := agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			// nil candidate signals gathering is complete
			close(gatherDone)
			return
		}

		candidate := convertPionCandidate(c)
		select {
		case candidateChan <- candidate:
			logger.Debug("Gathered candidate", "type", candidate.Type, "address", candidate.Address)
		default:
			logger.Warn("Candidate channel full, dropping candidate")
		}
	}); err != nil {
		agent.Close()
		return nil, fmt.Errorf("failed to set OnCandidate callback: %w", err)
	}

	// Create the pionAgent early so we can reference it in the callback
	pa := &pionAgent{
		config:        config,
		agent:         agent,
		logger:        logger,
		candidateChan: candidateChan,
		gatherDone:    gatherDone,
	}

	// Set OnConnectionStateChange callback to detect disconnections
	if err := agent.OnConnectionStateChange(func(state ice.ConnectionState) {
		var connState ConnectionState
		switch state {
		case ice.ConnectionStateNew:
			connState = ConnectionStateNew
		case ice.ConnectionStateChecking:
			connState = ConnectionStateChecking
		case ice.ConnectionStateConnected:
			connState = ConnectionStateConnected
		case ice.ConnectionStateCompleted:
			connState = ConnectionStateCompleted
		case ice.ConnectionStateFailed:
			connState = ConnectionStateFailed
		case ice.ConnectionStateDisconnected:
			connState = ConnectionStateDisconnected
		case ice.ConnectionStateClosed:
			connState = ConnectionStateClosed
		default:
			connState = ConnectionState(state.String())
		}

		logger.Info("ICE connection state changed", "state", connState)

		// Use separate mutex for callback to avoid deadlocks
		pa.callbackMu.RLock()
		callback := pa.stateChangeCallback
		pa.callbackMu.RUnlock()

		if callback != nil {
			callback(connState)
		}
	}); err != nil {
		agent.Close()
		return nil, fmt.Errorf("failed to set OnConnectionStateChange callback: %w", err)
	}

	localUfrag, localPwd, err := agent.GetLocalUserCredentials()
	if err != nil {
		agent.Close()
		return nil, fmt.Errorf("failed to get local credentials: %w", err)
	}

	// Set credentials on the already-created pionAgent
	pa.localUfrag = localUfrag
	pa.localPwd = localPwd

	logger.Info("ICE agent created",
		"ufrag", localUfrag,
		"stun_servers", len(config.STUNServers),
		"turn_servers", len(config.TURNServers),
	)

	return pa, nil
}

// GatherCandidates starts gathering local ICE candidates.
func (a *pionAgent) GatherCandidates(ctx context.Context) (<-chan *Candidate, error) {
	if a.closed {
		return nil, ErrAlreadyClosed
	}

	a.logger.Info("Starting candidate gathering", "timeout", a.config.GatherTimeout)

	// Create output channel
	outChan := make(chan *Candidate, 10)

	// Start gathering in background
	go func() {
		defer close(outChan)

		gatherCtx, cancel := context.WithTimeout(ctx, a.config.GatherTimeout)
		defer cancel()

		// Start gathering - candidates will be sent to a.candidateChan via OnCandidate callback
		if err := a.agent.GatherCandidates(); err != nil {
			a.logger.Error("Failed to start gathering", "error", err)
			return
		}

		// Forward candidates from internal channel to output channel
		candidateCount := 0
		for {
			select {
			case cand, ok := <-a.candidateChan:
				if !ok {
					// Channel closed, shouldn't happen
					a.logger.Warn("Candidate channel closed unexpectedly")
					return
				}
				candidateCount++
				select {
				case outChan <- cand:
				case <-gatherCtx.Done():
					a.logger.Warn("Candidate gathering timed out", "gathered", candidateCount)
					return
				}
			case <-a.gatherDone:
				// Gathering complete (nil candidate received)
				a.logger.Info("Candidate gathering complete", "count", candidateCount)
				return
			case <-gatherCtx.Done():
				a.logger.Warn("Candidate gathering timed out", "gathered", candidateCount)
				return
			}
		}
	}()

	return outChan, nil
}

// AddRemoteCandidate adds a remote candidate to the agent.
func (a *pionAgent) AddRemoteCandidate(candidate *Candidate) error {
	if a.closed {
		return ErrAlreadyClosed
	}

	if candidate == nil {
		return ErrInvalidCandidate
	}

	// Convert to Pion candidate
	pionCandidate, err := convertToPionCandidate(candidate)
	if err != nil {
		return fmt.Errorf("invalid candidate: %w", err)
	}

	if err := a.agent.AddRemoteCandidate(pionCandidate); err != nil {
		return fmt.Errorf("failed to add remote candidate: %w", err)
	}

	a.logger.Debug("Added remote candidate", "type", candidate.Type, "address", candidate.Address)
	return nil
}

// SetRemoteCredentials sets the remote ICE credentials.
func (a *pionAgent) SetRemoteCredentials(ufrag, pwd string) error {
	if a.closed {
		return ErrAlreadyClosed
	}

	if ufrag == "" || pwd == "" {
		return ErrInvalidCredentials
	}

	// Store credentials for later use in Connect
	a.remoteUfrag = ufrag
	a.remotePwd = pwd

	a.logger.Info("Remote credentials set", "ufrag", ufrag)
	return nil
}

// LocalCredentials returns the local ICE credentials.
func (a *pionAgent) LocalCredentials() (ufrag, pwd string) {
	return a.localUfrag, a.localPwd
}

// Connect establishes the ICE connection.
// If controlling is true, this peer will use Dial (controlling agent).
// If false, this peer will use Accept (controlled agent).
func (a *pionAgent) Connect(ctx context.Context, controlling bool) (net.Conn, error) {
	if a.closed {
		return nil, ErrAlreadyClosed
	}

	if a.conn != nil {
		return a.conn, nil
	}

	// Validate remote credentials are set
	if a.remoteUfrag == "" || a.remotePwd == "" {
		return nil, fmt.Errorf("remote credentials not set")
	}

	role := "controlled (Accept)"
	if controlling {
		role = "controlling (Dial)"
	}
	a.logger.Info("Establishing ICE connection",
		"timeout", a.config.ConnectionTimeout,
		"role", role)

	// Create context with timeout
	connectCtx, cancel := context.WithTimeout(ctx, a.config.ConnectionTimeout)
	defer cancel()

	var conn *ice.Conn
	var err error

	if controlling {
		// Controlling agent uses Dial
		conn, err = a.agent.Dial(connectCtx, a.remoteUfrag, a.remotePwd)
	} else {
		// Controlled agent uses Accept
		conn, err = a.agent.Accept(connectCtx, a.remoteUfrag, a.remotePwd)
	}

	if err != nil {
		a.logger.Error("ICE connection failed", "error", err, "role", role)
		return nil, fmt.Errorf("failed to establish connection: %w", err)
	}

	a.conn = conn

	pair, err := a.GetSelectedCandidatePair()
	if err == nil {
		a.logger.Info("ICE connection established",
			"local", pair.Local.String(),
			"remote", pair.Remote.String(),
			"role", role)
	} else {
		a.logger.Info("ICE connection established", "role", role)
	}

	return conn, nil
}

// GetSelectedCandidatePair returns the selected candidate pair after connection.
func (a *pionAgent) GetSelectedCandidatePair() (*CandidatePair, error) {
	if a.closed {
		return nil, ErrAlreadyClosed
	}

	if a.conn == nil {
		return nil, ErrNotConnected
	}

	pair, err := a.agent.GetSelectedCandidatePair()
	if err != nil {
		return nil, fmt.Errorf("failed to get selected pair: %w", err)
	}
	if pair == nil {
		return nil, ErrNoValidPair
	}

	return &CandidatePair{
		Local:  convertPionCandidate(pair.Local),
		Remote: convertPionCandidate(pair.Remote),
		State:  "succeeded", // Pion v3 doesn't expose state publicly
	}, nil
}

// Close closes the agent and releases all resources.
func (a *pionAgent) Close() error {
	if a.closed {
		return ErrAlreadyClosed
	}

	a.closed = true

	a.logger.Info("Closing ICE agent")

	if a.conn != nil {
		if err := a.conn.Close(); err != nil {
			a.logger.Warn("Error closing connection", "error", err)
		}
		a.conn = nil
	}

	if err := a.agent.Close(); err != nil {
		return fmt.Errorf("failed to close agent: %w", err)
	}

	return nil
}

// OnConnectionStateChange sets a callback to be invoked when the ICE connection state changes.
// Thread-safe: uses a separate mutex to avoid deadlocks when the callback is invoked.
func (a *pionAgent) OnConnectionStateChange(callback ConnectionStateChangeCallback) {
	a.callbackMu.Lock()
	a.stateChangeCallback = callback
	a.callbackMu.Unlock()
}

// convertPionCandidate converts a Pion candidate to our Candidate type.
func convertPionCandidate(c ice.Candidate) *Candidate {
	if c == nil {
		return nil
	}

	var candType CandidateType
	switch c.Type() {
	case ice.CandidateTypeHost:
		candType = CandidateTypeHost
	case ice.CandidateTypeServerReflexive:
		candType = CandidateTypeSrflx
	case ice.CandidateTypePeerReflexive:
		candType = CandidateTypePrflx
	case ice.CandidateTypeRelay:
		candType = CandidateTypeRelay
	}

	relatedAddr := ""
	relatedPort := 0
	if c.RelatedAddress() != nil {
		relatedAddr = c.RelatedAddress().Address
		relatedPort = c.RelatedAddress().Port
	}

	return &Candidate{
		Type:           candType,
		Address:        c.Address(),
		Port:           c.Port(),
		Protocol:       c.NetworkType().NetworkShort(),
		Priority:       c.Priority(),
		Foundation:     c.Foundation(),
		RelatedAddress: relatedAddr,
		RelatedPort:    relatedPort,
	}
}

// convertToPionCandidate converts our Candidate type to a Pion candidate.
func convertToPionCandidate(c *Candidate) (ice.Candidate, error) {
	var candType ice.CandidateType
	switch c.Type {
	case CandidateTypeHost:
		candType = ice.CandidateTypeHost
	case CandidateTypeSrflx:
		candType = ice.CandidateTypeServerReflexive
	case CandidateTypePrflx:
		candType = ice.CandidateTypePeerReflexive
	case CandidateTypeRelay:
		candType = ice.CandidateTypeRelay
	default:
		return nil, fmt.Errorf("unknown candidate type: %s", c.Type)
	}

	config := &ice.CandidateHostConfig{
		Network:   ProtocolUDP,
		Address:   c.Address,
		Port:      c.Port,
		Component: 1,
	}

	switch candType {
	case ice.CandidateTypeHost:
		return ice.NewCandidateHost(config)
	case ice.CandidateTypeServerReflexive:
		return ice.NewCandidateServerReflexive(&ice.CandidateServerReflexiveConfig{
			Network:   config.Network,
			Address:   config.Address,
			Port:      config.Port,
			Component: config.Component,
			RelAddr:   c.RelatedAddress,
			RelPort:   c.RelatedPort,
		})
	case ice.CandidateTypeRelay:
		return ice.NewCandidateRelay(&ice.CandidateRelayConfig{
			Network:   config.Network,
			Address:   config.Address,
			Port:      config.Port,
			Component: config.Component,
			RelAddr:   c.RelatedAddress,
			RelPort:   c.RelatedPort,
		})
	default:
		return nil, fmt.Errorf("unsupported candidate type: %s", candType)
	}
}
