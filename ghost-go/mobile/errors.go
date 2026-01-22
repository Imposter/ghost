// Package mobile provides gomobile-compatible bindings for ghost-go.
// This package exposes a JSON-based API suitable for use from Kotlin/Swift
// via gomobile bindings.
package mobile

import "errors"

// Mobile-specific errors
var (
	// ErrClientClosed is returned when an operation is attempted on a closed client.
	ErrClientClosed = errors.New("client is closed")

	// ErrNotConnected is returned when an operation requires an active connection.
	ErrNotConnected = errors.New("not connected")

	// ErrAlreadyConnected is returned when trying to connect an already connected client.
	ErrAlreadyConnected = errors.New("already connected")

	// ErrGatheringNotStarted is returned when trying to get candidates before gathering.
	ErrGatheringNotStarted = errors.New("candidate gathering not started")

	// ErrTunnelNotStarted is returned when trying to use tunnel features before starting.
	ErrTunnelNotStarted = errors.New("tunnel not started")

	// ErrInvalidJSON is returned when JSON parsing fails.
	ErrInvalidJSON = errors.New("invalid JSON")

	// ErrInvalidCredentials is returned when ICE credentials are invalid.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrInvalidCandidate is returned when a candidate is malformed.
	ErrInvalidCandidate = errors.New("invalid candidate")

	// ErrInvalidPublicKey is returned when a WireGuard public key is invalid.
	ErrInvalidPublicKey = errors.New("invalid public key")

	// ErrConnectionNotFound is returned when a connection ID is not found.
	ErrConnectionNotFound = errors.New("connection not found")
)
