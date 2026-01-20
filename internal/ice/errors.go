package ice

import "errors"

var (
	// ErrGatherTimeout indicates that candidate gathering timed out.
	ErrGatherTimeout = errors.New("candidate gathering timed out")

	// ErrConnectionFailed indicates that ICE connection establishment failed.
	ErrConnectionFailed = errors.New("ICE connection failed")

	// ErrInvalidCandidate indicates that a candidate is invalid.
	ErrInvalidCandidate = errors.New("invalid candidate")

	// ErrNoValidPair indicates that no valid candidate pair was found.
	ErrNoValidPair = errors.New("no valid candidate pair found")

	// ErrNotConnected indicates that the agent is not connected.
	ErrNotConnected = errors.New("agent not connected")

	// ErrAlreadyClosed indicates that the agent is already closed.
	ErrAlreadyClosed = errors.New("agent already closed")

	// ErrInvalidCredentials indicates that credentials are invalid.
	ErrInvalidCredentials = errors.New("invalid credentials")
)
