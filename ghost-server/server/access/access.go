// Package access implements ghost-server's access control. In open mode every
// authenticated request is allowed. In api mode the server asks an external
// authorizer webhook (POST {url}/ghost/authorize, HMAC-SHA256 signed) before
// a device may register, pair, join a network, or connect to a peer. Decisions
// are cached briefly, and any failure to reach the authorizer denies (fail
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
	// ActionRegister: a device self-registers into a network.
	ActionRegister Action = "register"
	// ActionPair: a device redeems a pairing code.
	ActionPair Action = "pair"
	// ActionJoinNetwork: a device joins its network over signalling.
	ActionJoinNetwork Action = "join_network"
	// ActionConnectPeer: a device relays offer/answer/candidates to a peer.
	ActionConnectPeer Action = "connect_peer"
)

// Request is the JSON body sent to the authorizer.
type Request struct {
	Action  Action `json:"action"`
	Network string `json:"network"`
	// Device is the acting device id. It is empty for register and pair,
	// where the device does not exist yet.
	Device string `json:"device,omitempty"`
	// Peer is the target device id for connect_peer.
	Peer string `json:"peer,omitempty"`
	// Role is the acting device's role (node or hub).
	Role proto.Role `json:"role,omitempty"`
	// Labels are the acting device's labels (requested labels for register
	// and pair).
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
	// ExitAllowlist overrides the network's exit allowlist for this device.
	ExitAllowlist []string `json:"exit_allowlist,omitempty"`
	Caps          Caps     `json:"caps,omitempty"`
	// Labels are stored as the device's labels (register, pair) or attached
	// to its exit policy (join_network).
	Labels map[string]string `json:"labels,omitempty"`
}

// Decision is the authorizer's JSON response.
type Decision struct {
	Allow  bool    `json:"allow"`
	Reason string  `json:"reason,omitempty"`
	Policy *Policy `json:"policy,omitempty"`
}

// ExitPolicy converts an authorizer policy into a wire exit policy for
// network. It returns nil when the decision carries no exit allowlist, so the
// network's own policy applies.
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
