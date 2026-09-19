// Package proto defines the versioned WebSocket JSON signalling protocol
// shared by the ghost signalling client (ghost-go/signal) and the ghost control
// plane (ghost-server). It contains no transport code so that both sides can
// depend on it without pulling in client or server logic.
//
// A session authenticates a peer (hello/welcome), joins its network
// (join_network/joined), and then receives a netmap: a snapshot of the peers
// it may reach under the network's ACLs, followed by live deltas. ICE offers,
// answers and candidates are relayed only between peer pairs the netmap
// allows. See docs/signalling-v1.md.
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
	// TypeJoinNetwork asks to join the peer's network. Payload: JoinNetwork.
	TypeJoinNetwork Type = "join_network"
	// TypeJoined confirms a join and carries the assigned address.
	// Payload: Joined.
	TypeJoined Type = "joined"
	// TypeNetmap is a full netmap snapshot (server->client). Payload: Netmap.
	TypeNetmap Type = "netmap"
	// TypeNetmapDelta changes the last netmap (server->client).
	// Payload: NetmapDelta.
	TypeNetmapDelta Type = "netmap_delta"
	// TypeOffer relays an ICE offer between peers. Payload: Signal.
	TypeOffer Type = "offer"
	// TypeAnswer relays an ICE answer between peers. Payload: Signal.
	TypeAnswer Type = "answer"
	// TypeCandidate relays an ICE candidate between peers. Payload: Signal.
	TypeCandidate Type = "candidate"
	// TypeHealth reports tunnel health (client->server). Payload: Health.
	TypeHealth Type = "health"
	// TypeHeartbeat is a keepalive in either direction. Payload: Heartbeat.
	TypeHeartbeat Type = "heartbeat"
	// TypeError reports a protocol or authorization error. Payload: Error.
	TypeError Type = "error"
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

// Role is a capability a peer holds in its network. Roles are assigned by the
// control plane (from the pre-auth key, the enrolment approval, or the
// control API), never self-declared.
type Role string

const (
	// RoleHub is a gateway that many peers connect to.
	RoleHub Role = "hub"
	// RoleNode is an ordinary member.
	RoleNode Role = "node"
	// RoleExit runs an exit (SOCKS5/HTTP-CONNECT) on its tunnel IP.
	RoleExit Role = "exit"
	// RoleRelay forwards traffic for other peers.
	RoleRelay Role = "relay"
)

// Roles lists every valid role.
var Roles = []Role{RoleHub, RoleNode, RoleExit, RoleRelay}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	for _, v := range Roles {
		if r == v {
			return true
		}
	}
	return false
}

// Rank orders roles for ICE: in a pair, the peer with the higher rank is the
// controlling agent. Ties are broken by the smaller peer id.
func (r Role) Rank() int {
	switch r {
	case RoleHub:
		return 3
	case RoleRelay:
		return 2
	case RoleExit:
		return 1
	}
	return 0
}

// MaxRank returns the highest Rank among roles.
func MaxRank(roles []Role) int {
	best := 0
	for _, r := range roles {
		if r.Rank() > best {
			best = r.Rank()
		}
	}
	return best
}

// HasRole reports whether roles contains r.
func HasRole(roles []Role, r Role) bool {
	for _, v := range roles {
		if v == r {
			return true
		}
	}
	return false
}

// Controlling reports whether the peer (selfID, selfRoles) should be the ICE
// controlling agent towards (peerID, peerRoles). Both sides compute the same
// answer, so exactly one of them sends the offer.
func Controlling(selfID string, selfRoles []Role, peerID string, peerRoles []Role) bool {
	a, b := MaxRank(selfRoles), MaxRank(peerRoles)
	if a != b {
		return a > b
	}
	return selfID < peerID
}

// Hello is the first client->server message. It authenticates the peer with
// its peer token.
type Hello struct {
	Version int `json:"version"`
	// PeerToken authenticates the peer (opaque bearer credential).
	PeerToken string `json:"peer_token"`
	// PeerID is the caller's peer id, if known. It must match the token.
	PeerID string `json:"peer_id,omitempty"`
	// Roles, when set, are the roles the client expects to hold. The server
	// rejects the hello unless the peer holds every one of them.
	Roles []Role `json:"roles,omitempty"`
	// PublicKey is the peer's WireGuard public key (base64). A new key rotates
	// the peer's key.
	PublicKey string `json:"public_key,omitempty"`
}

// Welcome is the server's reply to a successful Hello.
type Welcome struct {
	Version int `json:"version"`
	// PeerID is the server-side identifier for this peer.
	PeerID string `json:"peer_id"`
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

// JoinNetwork asks to join the peer's network.
type JoinNetwork struct {
	Network string `json:"network"`
}

// Joined confirms a join. A netmap snapshot follows.
type Joined struct {
	Network string `json:"network"`
	// Address is the assigned tunnel address in CIDR form (e.g.
	// "100.64.0.5/32").
	Address string `json:"address"`
	// Pool is the network's address pool (e.g. "100.64.0.0/10").
	Pool string `json:"pool,omitempty"`
}

// PeerInfo describes a peer in a netmap.
type PeerInfo struct {
	PeerID string `json:"peer_id"`
	Name   string `json:"name,omitempty"`
	// PublicKey is the peer's WireGuard public key (base64).
	PublicKey string `json:"public_key"`
	// Address is the peer's assigned tunnel address (CIDR).
	Address string   `json:"address"`
	Roles   []Role   `json:"roles"`
	Tags    []string `json:"tags,omitempty"`
	// Endpoints are the peer's last known public endpoints (host:port or
	// host), for diagnostics and future direct paths.
	Endpoints []string `json:"endpoints,omitempty"`
	// Online reports whether the peer has a live, joined session.
	Online bool `json:"online"`
}

// FilterRule admits inbound tunnel traffic from Src addresses to the listed
// Ports on this peer. Empty Ports means every port.
type FilterRule struct {
	Src   []string `json:"src"`
	Ports []int    `json:"ports,omitempty"`
}

// PacketFilter is the set of inbound rules a peer enforces. Traffic matching
// no rule is refused.
type PacketFilter struct {
	Rules []FilterRule `json:"rules"`
}

// Netmap is a full snapshot of what a peer may reach.
type Netmap struct {
	Network string `json:"network"`
	// Seq increases with every netmap or delta sent in the session.
	Seq int64 `json:"seq"`
	// Self is this peer as the control plane sees it.
	Self PeerInfo `json:"self"`
	// Peers are the peers the ACLs let this peer reach or be reached by.
	Peers []PeerInfo `json:"peers"`
	// Policy is the exit policy that applies to this peer.
	Policy *ExitPolicy `json:"policy,omitempty"`
	// Filter is the inbound packet filter for this peer. Nil means no
	// filtering (only in-memory test servers omit it).
	Filter *PacketFilter `json:"filter,omitempty"`
}

// NetmapDelta changes the last netmap. Fields that are nil or empty are
// unchanged.
type NetmapDelta struct {
	Network string `json:"network"`
	Seq     int64  `json:"seq"`
	// Self replaces the self entry when set (roles, tags or key changed).
	Self *PeerInfo `json:"self,omitempty"`
	// Upsert adds or replaces peers by PeerID.
	Upsert []PeerInfo `json:"upsert,omitempty"`
	// Remove drops peers by id.
	Remove []string `json:"remove,omitempty"`
	// Policy replaces the exit policy when set.
	Policy *ExitPolicy `json:"policy,omitempty"`
	// Filter replaces the packet filter when set.
	Filter *PacketFilter `json:"filter,omitempty"`
}

// Apply returns the netmap that results from applying d to n.
func (n Netmap) Apply(d NetmapDelta) Netmap {
	out := n
	out.Seq = d.Seq
	if d.Self != nil {
		out.Self = *d.Self
	}
	if d.Policy != nil {
		out.Policy = d.Policy
	}
	if d.Filter != nil {
		out.Filter = d.Filter
	}
	if len(d.Upsert) == 0 && len(d.Remove) == 0 {
		return out
	}
	drop := make(map[string]bool, len(d.Remove)+len(d.Upsert))
	for _, id := range d.Remove {
		drop[id] = true
	}
	for _, p := range d.Upsert {
		drop[p.PeerID] = true
	}
	peers := make([]PeerInfo, 0, len(n.Peers)+len(d.Upsert))
	for _, p := range n.Peers {
		if !drop[p.PeerID] {
			peers = append(peers, p)
		}
	}
	out.Peers = append(peers, d.Upsert...)
	return out
}

// ExitPolicy is the exit policy the control plane imposes on a peer. Allow
// entries use the exit.Allowlist syntax: "host", "host:port" or
// "*.example.com:443". An empty Allow denies every destination.
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
	// Revision is the network policy revision this was derived from.
	Revision int64 `json:"revision"`
}

// Signal relays an offer, answer, or candidate between two peers. The server
// fills From and Network when forwarding.
type Signal struct {
	Network string `json:"network"`
	// From is the sender peer id (set by the server on delivery).
	From string `json:"from,omitempty"`
	// To is the recipient peer id.
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

// LinkState is the state of a tunnel link to one peer.
type LinkState string

const (
	LinkConnecting   LinkState = "connecting"
	LinkConnected    LinkState = "connected"
	LinkFailed       LinkState = "failed"
	LinkDisconnected LinkState = "disconnected"
)

// LinkHealth is one peer link in a health report.
type LinkHealth struct {
	PeerID string    `json:"peer_id"`
	State  LinkState `json:"state"`
	// CandidateType is the selected local ICE candidate type (host, srflx,
	// prflx, relay).
	CandidateType string  `json:"candidate_type,omitempty"`
	RTTSeconds    float64 `json:"rtt_seconds,omitempty"`
	// LastHandshake is the Unix time of the last WireGuard handshake.
	LastHandshake int64  `json:"last_handshake,omitempty"`
	RxBytes       uint64 `json:"rx_bytes,omitempty"`
	TxBytes       uint64 `json:"tx_bytes,omitempty"`
}

// Health is a peer's tunnel health report, sent on link changes and
// periodically.
type Health struct {
	Links []LinkHealth `json:"links"`
	// Endpoints are the peer's own candidate addresses (host:port).
	Endpoints []string `json:"endpoints,omitempty"`
}

// Heartbeat is a keepalive. Nonce lets the sender match a reply if it wants.
type Heartbeat struct {
	Nonce int64 `json:"nonce,omitempty"`
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
	// ErrCodeForbidden reports a request the ACLs or the authorizer denied.
	ErrCodeForbidden = "forbidden"
	// ErrCodeRevoked reports that the peer was revoked or deleted; fatal.
	ErrCodeRevoked = "revoked"
	// ErrCodeExpired reports that the peer's credentials expired; fatal.
	ErrCodeExpired = "expired"
	// ErrCodeMoved reports that the peer was moved to another network; fatal.
	ErrCodeMoved = "moved"
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
