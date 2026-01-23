// Package mobile provides gomobile-compatible bindings for ghost-go.
//
// This package exposes a simplified, JSON-based API for establishing encrypted
// P2P tunnels on mobile platforms (Android/iOS). It wraps the internal ice and
// wireguard packages into an easy-to-use interface for mobile applications.
//
// # Thread Safety
//
// All exported types in this package are thread-safe. GhostClient methods can
// be called from any goroutine. EventCallback.OnEvent is called from a
// background goroutine and should not block.
//
// # Resource Management
//
// GhostClient manages several resources that require explicit cleanup:
//   - ICE agent (network sockets, goroutines)
//   - ICE connection (network socket)
//   - WireGuard device (goroutines, TUN device)
//   - HTTP connection pool
//
// CRITICAL: You MUST call GhostClient.Close() when done to prevent resource leaks.
// See GhostClient documentation for details.
//
// # gomobile Compatibility
//
// This package is designed for gomobile:
//   - All public methods use primitive types or JSON strings
//   - No slices, maps, or custom types in public API signatures
//   - EventCallback interface generates platform-specific protocols/interfaces
//
// Build for Android:
//
//	gomobile bind -target=android -o mobile.aar ./mobile
//
// Build for iOS:
//
//	gomobile bind -target=ios -o Mobile.xcframework ./mobile
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
