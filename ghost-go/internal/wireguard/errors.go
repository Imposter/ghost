package wireguard

import "errors"

var (
	// ErrInvalidKey indicates that a key is invalid.
	ErrInvalidKey = errors.New("invalid key")

	// ErrTunnelNotUp indicates that the tunnel is not up.
	ErrTunnelNotUp = errors.New("tunnel not up")

	// ErrTUNCreationFailed indicates that TUN interface creation failed.
	ErrTUNCreationFailed = errors.New("TUN interface creation failed")

	// ErrTunnelClosed indicates that the tunnel is closed.
	ErrTunnelClosed = errors.New("tunnel closed")

	// ErrPeerNotFound indicates that a peer was not found.
	ErrPeerNotFound = errors.New("peer not found")

	// ErrInvalidEndpoint indicates that an endpoint is invalid.
	ErrInvalidEndpoint = errors.New("invalid endpoint")

	// ErrNoLocalAddresses indicates no local addresses were provided.
	ErrNoLocalAddresses = errors.New("at least one local address is required")

	// ErrInvalidMTU indicates the MTU value is out of valid range.
	ErrInvalidMTU = errors.New("invalid MTU value")

	// ErrNetstackCreationFailed indicates userspace TUN creation failed.
	ErrNetstackCreationFailed = errors.New("netstack TUN creation failed")
)
