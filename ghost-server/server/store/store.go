// Package store persists the control plane's state: networks (with their
// policy documents), peers, pre-auth keys, interactive enrolments, API keys,
// and the audit log. It has one implementation over database/sql that runs on
// SQLite (development) and PostgreSQL (production) with a shared, embedded,
// portable schema.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/policy"
)

// Sentinel errors.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
)

// Network is a named set of peers sharing an address pool and a policy.
type Network struct {
	Name string
	Pool string
	// Isolation limits which peers may see each other on top of the ACLs.
	Isolation      policy.Isolation
	Policy         policy.Document
	PolicyRevision int64
	CreatedAt      time.Time
}

// Peer is an enrolled identity. TokenHash is the SHA-256 of the peer's bearer
// token; the token itself is never stored.
type Peer struct {
	ID        string
	TokenHash string
	Network   string
	Name      string
	PublicKey string
	// Address is the tunnel address from the network pool (CIDR), empty until
	// the peer first joins.
	Address   string
	Roles     []proto.Role
	Tags      []string
	Labels    map[string]string
	Endpoints []string
	// Ephemeral peers are deleted after staying offline past the grace period.
	Ephemeral bool
	// AuthKeyID is the pre-auth key the peer enrolled with, if any.
	AuthKeyID string
	// Health is the last health summary the peer reported, if any.
	Health    *proto.Health
	HealthAt  *time.Time
	CreatedAt time.Time
	LastSeen  *time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

// PeerStatus summarises a peer's credential state.
type PeerStatus string

const (
	PeerActive  PeerStatus = "active"
	PeerExpired PeerStatus = "expired"
	PeerRevoked PeerStatus = "revoked"
)

// Status returns the peer's credential state at now.
func (p Peer) Status(now time.Time) PeerStatus {
	switch {
	case p.RevokedAt != nil:
		return PeerRevoked
	case p.ExpiresAt != nil && !now.Before(*p.ExpiresAt):
		return PeerExpired
	}
	return PeerActive
}

// Subject returns the peer as the policy engine sees it.
func (p Peer) Subject() policy.Subject {
	return policy.Subject{ID: p.ID, Roles: p.Roles, Tags: p.Tags, Address: p.Address}
}

// AuthKey is a pre-auth key. KeyHash is the SHA-256 of the key.
type AuthKey struct {
	ID        string
	KeyHash   string
	Network   string
	Reusable  bool
	Ephemeral bool
	Roles     []proto.Role
	Tags      []string
	Labels    map[string]string
	// PeerTTL, when positive, sets the expiry of peers enrolled with the key.
	PeerTTL    time.Duration
	Uses       int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Usable reports whether the key can enrol a peer at now.
func (k AuthKey) Usable(now time.Time) bool {
	return k.RevokedAt == nil && now.Before(k.ExpiresAt) && (k.Reusable || k.Uses == 0)
}

// EnrollmentStatus is the state of an interactive enrolment.
type EnrollmentStatus string

const (
	EnrollmentPending  EnrollmentStatus = "pending"
	EnrollmentApproved EnrollmentStatus = "approved"
	EnrollmentDenied   EnrollmentStatus = "denied"
	EnrollmentClaimed  EnrollmentStatus = "claimed"
	EnrollmentExpired  EnrollmentStatus = "expired"
)

// Enrollment is an interactive enrolment request: the enrolling peer shows a
// short code to a person, who approves it through the control API. The
// user-facing code and the peer's poll token are stored only as hashes.
type Enrollment struct {
	CodeHash  string
	PollHash  string
	Network   string
	Name      string
	PublicKey string
	Labels    map[string]string
	Status    EnrollmentStatus
	// Roles and Tags are set on approval.
	Roles     []proto.Role
	Tags      []string
	Reason    string
	PeerID    string
	CreatedAt time.Time
	ExpiresAt time.Time
	DecidedAt *time.Time
}

// APIKey is a scoped control API key. KeyHash is the SHA-256 of the key.
type APIKey struct {
	ID      string
	KeyHash string
	Name    string
	Scopes  []string
	// Networks limits the key to these networks (empty: every network).
	Networks   []string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// AuditEvent is one entry of the audit log.
type AuditEvent struct {
	ID      string         `json:"id"`
	Time    time.Time      `json:"time"`
	Network string         `json:"network,omitempty"`
	Actor   string         `json:"actor"`
	Action  string         `json:"action"`
	Target  string         `json:"target,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// PeerFilter narrows ListPeers.
type PeerFilter struct {
	Network        string
	IncludeRevoked bool
}

// AuditFilter narrows ListAudit.
type AuditFilter struct {
	Network string
	Since   time.Time
	Limit   int
}

// Stats are fleet counters.
type Stats struct {
	Networks     int
	Peers        int
	RevokedPeers int
}

// Store is the persistence interface used by the control plane.
type Store interface {
	CreateNetwork(ctx context.Context, n Network) error
	GetNetwork(ctx context.Context, name string) (Network, error)
	ListNetworks(ctx context.Context) ([]Network, error)
	// DeleteNetwork removes an empty network; ErrConflict if it has peers.
	DeleteNetwork(ctx context.Context, name string) error
	// SetNetworkPolicy stores a policy document, bumps the revision and
	// returns the updated network.
	SetNetworkPolicy(ctx context.Context, name string, doc policy.Document) (Network, error)
	// SetNetworkIsolation changes a network's isolation mode, bumps the
	// policy revision and returns the updated network.
	SetNetworkIsolation(ctx context.Context, name string, iso policy.Isolation) (Network, error)

	CreatePeer(ctx context.Context, p Peer) error
	GetPeer(ctx context.Context, id string) (Peer, error)
	GetPeerByTokenHash(ctx context.Context, hash string) (Peer, error)
	ListPeers(ctx context.Context, f PeerFilter) ([]Peer, error)
	// UpdatePeer reads the peer, applies fn and writes it back atomically. It
	// returns ErrConflict if the new address is taken.
	UpdatePeer(ctx context.Context, id string, fn func(*Peer) error) (Peer, error)
	DeletePeer(ctx context.Context, id string) error
	// UsedAddresses lists addresses held in a network (revoked peers
	// included: addresses are not recycled while the peer row exists).
	UsedAddresses(ctx context.Context, network string) ([]string, error)
	TouchPeer(ctx context.Context, id string, at time.Time) error
	// SetPeerHealth stores a health summary and marks the peer seen at.
	SetPeerHealth(ctx context.Context, id string, h proto.Health, at time.Time) error

	CreateAuthKey(ctx context.Context, k AuthKey) error
	GetAuthKeyByHash(ctx context.Context, hash string) (AuthKey, error)
	ListAuthKeys(ctx context.Context, network string) ([]AuthKey, error)
	// UseAuthKey atomically records one use of a usable key; ErrNotFound if
	// the key is revoked, expired, or a spent single-use key.
	UseAuthKey(ctx context.Context, id string, now time.Time) (AuthKey, error)
	RevokeAuthKey(ctx context.Context, id string, at time.Time) (AuthKey, error)

	CreateEnrollment(ctx context.Context, e Enrollment) error
	GetEnrollmentByCode(ctx context.Context, codeHash string) (Enrollment, error)
	GetEnrollmentByPoll(ctx context.Context, pollHash string) (Enrollment, error)
	ListEnrollments(ctx context.Context, network string, status EnrollmentStatus) ([]Enrollment, error)
	// UpdateEnrollment reads the enrolment, applies fn and writes it back
	// atomically.
	UpdateEnrollment(ctx context.Context, codeHash string, fn func(*Enrollment) error) (Enrollment, error)
	// DeleteEnrollmentsBefore removes enrolments that expired before t.
	DeleteEnrollmentsBefore(ctx context.Context, t time.Time) (int64, error)

	CreateAPIKey(ctx context.Context, k APIKey) error
	GetAPIKeyByHash(ctx context.Context, hash string) (APIKey, error)
	ListAPIKeys(ctx context.Context) ([]APIKey, error)
	RevokeAPIKey(ctx context.Context, id string, at time.Time) (APIKey, error)
	TouchAPIKey(ctx context.Context, id string, at time.Time) error

	AppendAudit(ctx context.Context, e AuditEvent) error
	ListAudit(ctx context.Context, f AuditFilter) ([]AuditEvent, error)

	Stats(ctx context.Context) (Stats, error)
	Close() error
}
