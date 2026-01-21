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
)
