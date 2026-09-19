// Package proto defines the versioned WebSocket JSON signalling protocol
// shared by the ghost signalling client (ghost-go/signal) and the ghost
// signalling server (ghost-server). It contains no transport code so that
// both sides can depend on it without pulling in client or server logic.
package proto

import "encoding/json"

// Version is the signalling protocol version implemented by this package.
// The client sends it in the Hello message; the server rejects mismatches.
const Version = 1

// Type is a signalling message type discriminator.
type Type string

// Message types. Every frame on the wire is an Envelope whose Type is one of
// these constants.
const (
	// TypeHello is the first client->server frame. Payload: Hello.
	TypeHello Type = "hello"
	// TypeWelcome is the server->client reply to Hello. Payload: Welcome.
	TypeWelcome Type = "welcome"
	// TypeJoinNetwork asks to join a network. Payload: JoinNetwork.
	TypeJoinNetwork Type = "join_network"
	// TypeJoined confirms a join and carries the assigned address.
	// Payload: Joined.
	TypeJoined Type = "joined"
	// TypeOffer relays an ICE offer between peers. Payload: Signal.
	TypeOffer Type = "offer"
	// TypeAnswer relays an ICE answer between peers. Payload: Signal.
	TypeAnswer Type = "answer"
	// TypeCandidate relays an ICE candidate between peers. Payload: Signal.
	TypeCandidate Type = "candidate"
	// TypePeerOnline announces that a peer joined the network.
	// Payload: PeerEvent.
	TypePeerOnline Type = "peer_online"
	// TypePeerOffline announces that a peer left the network.
	// Payload: PeerEvent.
	TypePeerOffline Type = "peer_offline"
	// TypeHeartbeat is a keepalive in either direction. Payload: Heartbeat.
	TypeHeartbeat Type = "heartbeat"
	// TypeAddressAssignment (re)assigns a tunnel address to this device.
	// Payload: AddressAssignment.
	TypeAddressAssignment Type = "address_assignment"
	// TypeError reports a protocol or authorization error. Payload: Error.
	TypeError Type = "error"
	// TypePolicy pushes the effective exit policy for a joined network to a
	// member. The server sends it whenever the policy changes. Payload:
	// ExitPolicy.
	TypePolicy Type = "policy"
)

// Envelope is the outer frame for every signalling message. Payload holds the
// type-specific body as raw JSON so a receiver can dispatch on Type first and
// decode the body second.
type Envelope struct {
	// V is the protocol version. Always Version for frames this package emits.
	V int `json:"v"`
	// Type is the message discriminator.
	Type Type `json:"type"`
	// ID is an optional correlation id, echoed in replies where relevant.
	ID string `json:"id,omitempty"`
	// Payload is the type-specific body.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Role identifies how a device participates in a network.
type Role string

const (
	// RoleNode is a joining peer that connects to one hub.
	RoleNode Role = "node"
	// RoleHub is a gateway that accepts many nodes.
	RoleHub Role = "hub"
)

// Hello is the first client->server message. It authenticates the device with
// a device token and states the protocol version and role.
type Hello struct {
	Version int `json:"version"`
	// DeviceToken authenticates the device (opaque bearer credential).
	DeviceToken string `json:"device_token"`
	// DeviceID is the caller's stable device identifier, if known.
	DeviceID string `json:"device_id,omitempty"`
	// Role is how this device intends to participate.
	Role Role `json:"role"`
	// PublicKey is the device's WireGuard public key (base64).
	PublicKey string `json:"public_key,omitempty"`
}

// Welcome is the server's reply to a successful Hello.
type Welcome struct {
	Version int `json:"version"`
	// DeviceID is the server-side identifier for this device.
	DeviceID string `json:"device_id"`
	// SessionID identifies this signalling session.
	SessionID string `json:"session_id,omitempty"`
	// HeartbeatInterval is the seconds between heartbeats the server expects.
	HeartbeatInterval int `json:"heartbeat_interval,omitempty"`
	// ICEServers are the STUN/TURN servers the client should use, including
	// any short-lived TURN credentials.
	ICEServers []ICEServer `json:"ice_servers,omitempty"`
}

// ICEServer describes a STUN or TURN server for the client.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// JoinNetwork asks to join a named network.
type JoinNetwork struct {
	Network string `json:"network"`
	// Role in this network.
	Role Role `json:"role"`
}

// Joined confirms a join and carries the address assigned from the network
// pool along with the current members.
type Joined struct {
	Network string `json:"network"`
	// Address is the assigned tunnel address in CIDR form (e.g.
	// "100.64.0.5/32").
	Address string `json:"address"`
	// Pool is the network's address pool (e.g. "100.64.0.0/10").
	Pool string `json:"pool,omitempty"`
	// Hub identifies the hub a node should connect to (empty for a hub).
	Hub *PeerInfo `json:"hub,omitempty"`
	// Peers are the members already online in the network.
	Peers []PeerInfo `json:"peers,omitempty"`
	// Policy is the effective exit policy for this member at join time. Later
	// changes arrive as policy messages.
	Policy *ExitPolicy `json:"policy,omitempty"`
}

// ExitPolicy is the exit policy a network (or the access-control authorizer)
// imposes on a member. Allow entries use the exit.Allowlist syntax:
// "host", "host:port" or "*.example.com:443". An empty Allow denies every
// destination.
type ExitPolicy struct {
	Network string `json:"network"`
	// Allow is the host:port allowlist.
	Allow []string `json:"allow"`
	// DailyBytes caps bytes per UTC day (0 = unlimited).
	DailyBytes int64 `json:"daily_bytes,omitempty"`
	// BytesPerSecond caps throughput (0 = unlimited).
	BytesPerSecond int64 `json:"bytes_per_second,omitempty"`
	// Paused refuses new exit connections while true.
	Paused bool `json:"paused,omitempty"`
	// Labels are free-form policy labels (e.g. from the authorizer).
	Labels map[string]string `json:"labels,omitempty"`
	// Revision increases each time the policy changes.
	Revision int64 `json:"revision"`
}

// PeerInfo describes another member of a network.
type PeerInfo struct {
	DeviceID string `json:"device_id"`
	// PublicKey is the peer's WireGuard public key (base64).
	PublicKey string `json:"public_key"`
	// Address is the peer's assigned tunnel address (CIDR).
	Address string `json:"address"`
	Role    Role   `json:"role"`
}

// Signal relays an offer, answer, or candidate between two peers. The server
// fills From when forwarding.
type Signal struct {
	Network string `json:"network"`
	// From is the sender device id (set by the server on delivery).
	From string `json:"from,omitempty"`
	// To is the recipient device id.
	To string `json:"to"`
	// Ufrag and Pwd carry ICE credentials for offer/answer.
	Ufrag string `json:"ufrag,omitempty"`
	Pwd   string `json:"pwd,omitempty"`
	// Candidate carries a single ICE candidate for candidate messages.
	Candidate *Candidate `json:"candidate,omitempty"`
}

// Candidate is a wire representation of an ICE candidate. It mirrors the
// fields of ice.Candidate without importing that package.
type Candidate struct {
	Type           string `json:"type"`
	Address        string `json:"address"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Priority       uint32 `json:"priority"`
	Foundation     string `json:"foundation"`
	RelatedAddress string `json:"related_address,omitempty"`
	RelatedPort    int    `json:"related_port,omitempty"`
}

// PeerEvent announces a peer coming online or going offline.
type PeerEvent struct {
	Network string   `json:"network"`
	Peer    PeerInfo `json:"peer"`
}

// Heartbeat is a keepalive. Nonce lets the sender match a reply if it wants.
type Heartbeat struct {
	Nonce int64 `json:"nonce,omitempty"`
}

// AddressAssignment (re)assigns a tunnel address for a network.
type AddressAssignment struct {
	Network string `json:"network"`
	Address string `json:"address"`
	Pool    string `json:"pool,omitempty"`
}

// Error reports a protocol or authorization failure. When Fatal is true the
// server closes the connection after sending it.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// Common error codes.
const (
	ErrCodeUnsupportedVersion = "unsupported_version"
	ErrCodeUnauthorized       = "unauthorized"
	ErrCodeBadRequest         = "bad_request"
	ErrCodeNotFound           = "not_found"
	ErrCodeInternal           = "internal"
	// ErrCodeForbidden reports an authenticated request that access control
	// denied (for example a join or a peer connection).
	ErrCodeForbidden = "forbidden"
	// ErrCodeRevoked reports that the device was revoked; the server closes
	// the session after sending it.
	ErrCodeRevoked = "revoked"
)

// Encode marshals a payload into an Envelope of the given type.
func Encode(t Type, payload any) (Envelope, error) {
	env := Envelope{V: Version, Type: t}
	if payload == nil {
		return env, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return env, err
	}
	env.Payload = b
	return env, nil
}

// Decode unmarshals an Envelope's payload into v.
func (e Envelope) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}
