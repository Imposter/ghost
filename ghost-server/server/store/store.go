// Package store persists networks, devices and pairing codes. It has one
// implementation over database/sql that runs on SQLite (development) and
// PostgreSQL (production) with a shared, embedded, portable schema.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Sentinel errors.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: already exists")
)

// Network is a named set of devices sharing an address pool and an exit
// policy.
type Network struct {
	Name string
	Pool string
	// Policy is the network's exit policy; Policy.Revision increases on each
	// change.
	Policy    proto.ExitPolicy
	CreatedAt time.Time
}

// Device is a registered identity. TokenHash is the SHA-256 of the device's
// bearer token; the token itself is never stored.
type Device struct {
	ID        string
	TokenHash string
	Network   string
	Role      proto.Role
	Name      string
	Labels    map[string]string
	PublicKey string
	// Address is the tunnel address assigned from the network pool (CIDR),
	// empty until the device first joins.
	Address   string
	CreatedAt time.Time
	LastSeen  *time.Time
	RevokedAt *time.Time
}

// Revoked reports whether the device has been revoked.
func (d Device) Revoked() bool { return d.RevokedAt != nil }

// PairingCode is a short-lived, single-use code that mints a device.
type PairingCode struct {
	// CodeHash is the SHA-256 of the normalized code.
	CodeHash  string
	Network   string
	Role      proto.Role
	Name      string
	Labels    map[string]string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	DeviceID  string
}

// DeviceFilter narrows ListDevices.
type DeviceFilter struct {
	// Network, when set, limits the result to one network.
	Network string
	// IncludeRevoked includes revoked devices.
	IncludeRevoked bool
}

// Stats are fleet counters.
type Stats struct {
	Networks       int
	Devices        int
	RevokedDevices int
}

// Store is the persistence interface used by the server.
type Store interface {
	CreateNetwork(ctx context.Context, n Network) error
	GetNetwork(ctx context.Context, name string) (Network, error)
	ListNetworks(ctx context.Context) ([]Network, error)
	// SetNetworkPolicy stores p (its Revision is ignored) and returns the
	// stored policy with the new revision.
	SetNetworkPolicy(ctx context.Context, name string, p proto.ExitPolicy) (proto.ExitPolicy, error)

	CreateDevice(ctx context.Context, d Device) error
	GetDevice(ctx context.Context, id string) (Device, error)
	GetDeviceByTokenHash(ctx context.Context, hash string) (Device, error)
	ListDevices(ctx context.Context, f DeviceFilter) ([]Device, error)
	// UsedAddresses lists the addresses held by devices in a network,
	// including revoked ones (addresses are not recycled).
	UsedAddresses(ctx context.Context, network string) ([]string, error)
	// SetDeviceAddress stores an address; ErrConflict if another device in
	// the network holds it.
	SetDeviceAddress(ctx context.Context, id, address string) error
	SetDevicePublicKey(ctx context.Context, id, key string) error
	TouchDevice(ctx context.Context, id string, at time.Time) error
	RevokeDevice(ctx context.Context, id string, at time.Time) error
	// MoveDevice changes a device's network and clears its address.
	MoveDevice(ctx context.Context, id, network string) error

	CreatePairingCode(ctx context.Context, c PairingCode) error
	// GetPairingCode returns a code whether or not it is used or expired.
	GetPairingCode(ctx context.Context, codeHash string) (PairingCode, error)
	// RedeemPairingCode atomically marks an unused, unexpired code as used
	// and returns it. It returns ErrNotFound for unknown, used or expired
	// codes.
	RedeemPairingCode(ctx context.Context, codeHash string, now time.Time) (PairingCode, error)
	// BindPairingCode records the device a redeemed code minted.
	BindPairingCode(ctx context.Context, codeHash, deviceID string) error
	// DeleteExpiredPairingCodes removes codes that expired before now.
	DeleteExpiredPairingCodes(ctx context.Context, now time.Time) (int64, error)

	Stats(ctx context.Context) (Stats, error)
	Close() error
}
