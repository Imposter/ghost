package wireguard

import "errors"

var (
	// ErrInvalidKey indicates that a key is invalid.
	ErrInvalidKey = errors.New("invalid key")

	// ErrDeviceNotUp indicates that the device is not up.
	ErrDeviceNotUp = errors.New("device not up")

	// ErrTUNCreationFailed indicates that TUN device creation failed.
	ErrTUNCreationFailed = errors.New("TUN device creation failed")

	// ErrDeviceClosed indicates that the device is closed.
	ErrDeviceClosed = errors.New("device closed")

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
