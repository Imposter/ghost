package ice

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/pion/ice/v3"
)

// Agent handles ICE connectivity establishment.
type Agent interface {
	// GatherCandidates starts gathering local ICE candidates.
	// Returns a channel that emits candidates as they are discovered.
	GatherCandidates(ctx context.Context) (<-chan *Candidate, error)

	// AddRemoteCandidate adds a remote candidate to the agent.
	AddRemoteCandidate(candidate *Candidate) error

	// SetRemoteCredentials sets the remote ICE credentials.
	SetRemoteCredentials(ufrag, pwd string) error

	// LocalCredentials returns the local ICE credentials.
	LocalCredentials() (ufrag, pwd string)

	// Connect establishes the ICE connection.
	// Returns a net.Conn that can be used for communication.
	Connect(ctx context.Context) (net.Conn, error)

	// GetSelectedCandidatePair returns the selected candidate pair after connection.
	GetSelectedCandidatePair() (*CandidatePair, error)

	// Close closes the agent and releases all resources.
	Close() error
}

// pionAgent implements Agent using Pion's ICE library.
type pionAgent struct {
	config *ICEConfig
	agent  *ice.Agent
	logger *slog.Logger

	// State tracking
	conn       net.Conn
	localUfrag string
	localPwd   string
	closed     bool
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

	// Build ICE agent configuration
	agentConfig := &ice.AgentConfig{
		NetworkTypes: []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6},
	}

	// Add STUN servers
	for _, stunURL := range config.STUNServers {
		url, err := ice.ParseURL(stunURL)
		if err != nil {
			return nil, fmt.Errorf("invalid STUN URL %s: %w", stunURL, err)
		}
		agentConfig.Urls = append(agentConfig.Urls, url)
	}

	// Add TURN servers
	for _, turnServer := range config.TURNServers {
		for _, turnURL := range turnServer.URLs {
			url, err := ice.ParseURL(turnURL)
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

	localUfrag, localPwd, err := agent.GetLocalUserCredentials()
	if err != nil {
		agent.Close()
		return nil, fmt.Errorf("failed to get local credentials: %w", err)
	}

	logger.Info("ICE agent created",
		"ufrag", localUfrag,
		"stun_servers", len(config.STUNServers),
		"turn_servers", len(config.TURNServers),
	)

	return &pionAgent{
		config:     config,
		agent:      agent,
		logger:     logger,
		localUfrag: localUfrag,
		localPwd:   localPwd,
	}, nil
}

// GatherCandidates starts gathering local ICE candidates.
func (a *pionAgent) GatherCandidates(ctx context.Context) (<-chan *Candidate, error) {
	if a.closed {
		return nil, ErrAlreadyClosed
	}

	a.logger.Info("Starting candidate gathering", "timeout", a.config.GatherTimeout)

	candidates := make(chan *Candidate, 10)

	// Start gathering in background
	go func() {
		defer close(candidates)

		gatherCtx, cancel := context.WithTimeout(ctx, a.config.GatherTimeout)
		defer cancel()

		// Gather candidates
		if err := a.agent.GatherCandidates(); err != nil {
			a.logger.Error("Failed to start gathering", "error", err)
			return
		}

		// Stream candidates as they are discovered
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-gatherCtx.Done():
					close(done)
					return
				case <-done:
					return
				}
			}
		}()

		// Get all local candidates
		localCandidates, err := a.agent.GetLocalCandidates()
		if err != nil {
			a.logger.Error("Failed to get local candidates", "error", err)
			return
		}

		a.logger.Info("Candidate gathering complete", "count", len(localCandidates))

		for _, c := range localCandidates {
			candidate := convertPionCandidate(c)
			select {
			case candidates <- candidate:
				a.logger.Debug("Gathered candidate", "type", candidate.Type, "address", candidate.Address)
			case <-gatherCtx.Done():
				a.logger.Warn("Candidate gathering timed out")
				return
			}
		}
	}()

	return candidates, nil
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

	if err := a.agent.SetRemoteCredentials(ufrag, pwd); err != nil {
		return fmt.Errorf("failed to set remote credentials: %w", err)
	}

	a.logger.Info("Remote credentials set", "ufrag", ufrag)
	return nil
}

// LocalCredentials returns the local ICE credentials.
func (a *pionAgent) LocalCredentials() (ufrag, pwd string) {
	return a.localUfrag, a.localPwd
}

// Connect establishes the ICE connection.
func (a *pionAgent) Connect(ctx context.Context) (net.Conn, error) {
	if a.closed {
		return nil, ErrAlreadyClosed
	}

	if a.conn != nil {
		return a.conn, nil
	}

	a.logger.Info("Establishing ICE connection", "timeout", a.config.ConnectionTimeout)

	// Create context with timeout
	connectCtx, cancel := context.WithTimeout(ctx, a.config.ConnectionTimeout)
	defer cancel()

	// Wait for connection
	// Third parameter is the remote ufrag (empty for LITE agents)
	conn, err := a.agent.Dial(connectCtx, "", "")
	if err != nil {
		a.logger.Error("ICE connection failed", "error", err)
		return nil, fmt.Errorf("failed to establish connection: %w", err)
	}

	a.conn = conn

	pair, err := a.GetSelectedCandidatePair()
	if err == nil {
		a.logger.Info("ICE connection established",
			"local", pair.Local.String(),
			"remote", pair.Remote.String(),
		)
	} else {
		a.logger.Info("ICE connection established")
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
	}

	if err := a.agent.Close(); err != nil {
		return fmt.Errorf("failed to close agent: %w", err)
	}

	return nil
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
		Network:   "udp",
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
