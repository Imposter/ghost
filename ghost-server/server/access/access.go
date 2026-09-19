// Package access implements ghost-server's external authorizer: an optional
// policy source consulted when a peer enrols, when it connects (joins its
// network, and again whenever the network's policy changes), and when a pair
// of peers signal each other. In open mode it is not consulted. In api mode
// the server POSTs a signed request to {url}/ghost/authorize; decisions are
// cached briefly, and any failure to reach the authorizer denies (fail
// closed).
//
// The package also ships Verifier, which an authorizer implementation uses to
// check a request's signature, timestamp, and nonce.
package access

import (
	"context"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Action is the operation being authorized.
type Action string

const (
	// ActionEnroll: a peer enrols (pre-auth key, or claiming an approved
	// interactive enrolment).
	ActionEnroll Action = "enroll"
	// ActionConnect: a peer joins its network over signalling. It is asked
	// again, uncached, for every online peer when the network's policy
	// changes.
	ActionConnect Action = "connect"
	// ActionConnectPeer: a peer relays offer/answer/candidates to a target.
	ActionConnectPeer Action = "connect_peer"
)

// Request is the JSON body sent to the authorizer.
type Request struct {
	Action  Action `json:"action"`
	Network string `json:"network"`
	// Peer is the acting peer id. It is empty for enroll, where the peer does
	// not exist yet.
	Peer string `json:"peer,omitempty"`
	// Target is the other peer for connect_peer.
	Target string `json:"target,omitempty"`
	// Roles, Tags and Labels describe the acting (or enrolling) peer.
	Roles  []proto.Role      `json:"roles,omitempty"`
	Tags   []string          `json:"tags,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	// TS is the Unix time in seconds when the request was signed.
	TS int64 `json:"ts"`
	// Nonce is a random, single-use hex string.
	Nonce string `json:"nonce"`
}

// Caps are exit caps an authorizer may impose.
type Caps struct {
	DailyBytes     int64 `json:"daily_bytes,omitempty"`
	BytesPerSecond int64 `json:"bytes_per_second,omitempty"`
}

// Policy is the optional policy in an allow decision.
type Policy struct {
	// ExitAllowlist, on connect, replaces the exit allowlist the network
	// policy gives this peer. An explicit [] denies every destination.
	ExitAllowlist []string `json:"exit_allowlist"`
	Caps          Caps     `json:"caps,omitempty"`
	// Labels are merged into the peer's labels (enroll) or attached to its
	// exit policy (connect).
	Labels map[string]string `json:"labels,omitempty"`
	// Tags, on enroll, are added to the peer's tags; they must be defined in
	// the network's policy.
	Tags []string `json:"tags,omitempty"`
}

// Decision is the authorizer's JSON response.
type Decision struct {
	Allow  bool    `json:"allow"`
	Reason string  `json:"reason,omitempty"`
	Policy *Policy `json:"policy,omitempty"`
	// Unavailable is set (never by the authorizer) when the denial is the
	// fail-closed result of an unreachable or broken authorizer, so callers
	// can tell an outage from a decision.
	Unavailable bool `json:"-"`
}

// ExitPolicy converts an authorizer policy into a wire exit policy for
// network. It returns nil when the decision carries no exit allowlist, so the
// network policy applies unchanged.
func (p *Policy) ExitPolicy(network string) *proto.ExitPolicy {
	if p == nil || p.ExitAllowlist == nil {
		return nil
	}
	return &proto.ExitPolicy{
		Network:        network,
		Allow:          append([]string{}, p.ExitAllowlist...),
		DailyBytes:     p.Caps.DailyBytes,
		BytesPerSecond: p.Caps.BytesPerSecond,
		Labels:         p.Labels,
	}
}

// Authorizer decides one request. An error means no decision was reached;
// callers must treat it as a denial.
type Authorizer interface {
	Authorize(ctx context.Context, req Request) (Decision, error)
}

// Open allows everything.
type Open struct{}

// Authorize always allows.
func (Open) Authorize(context.Context, Request) (Decision, error) {
	return Decision{Allow: true}, nil
}
