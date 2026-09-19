package direct

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// TokenVersion is the token format version this package writes and accepts.
const TokenVersion = 1

// Token size and content limits. ParseToken rejects anything larger.
const (
	MaxTokenSize  = 8 << 10 // encoded bytes
	MaxCandidates = 32
	maxField      = 256
)

// Kind says which half of the exchange a token is.
type Kind string

const (
	// KindInvite is the first token, created by the inviting side.
	KindInvite Kind = "invite"
	// KindAnswer is the reply to an invite, created by the invited side.
	KindAnswer Kind = "answer"
)

// Token errors.
var (
	ErrTokenSize    = errors.New("direct: token too large")
	ErrTokenVersion = errors.New("direct: unsupported token version")
	ErrTokenExpired = errors.New("direct: token expired")
	ErrPrivateKey   = errors.New("direct: token carries private key material")
	ErrTokenInvalid = errors.New("direct: invalid token")
)

// Token is the public half of one side of a peer-to-peer exchange: who the
// peer is (WireGuard public key, tunnel address) and how to reach it (ICE
// credentials and gathered candidates). It never holds a private key. It is
// carried as base64url JSON (see Encode and ParseToken).
type Token struct {
	// V is the format version (TokenVersion).
	V int `json:"v"`
	// Kind is invite or answer.
	Kind Kind `json:"kind"`
	// Session pairs an answer with its invite; both sides use it as the
	// other peer's id.
	Session string `json:"session"`
	// PublicKey is the sender's WireGuard public key (base64).
	PublicKey string `json:"public_key"`
	// Address is the sender's tunnel address (CIDR, a single host).
	Address string `json:"address"`
	// AllowedIPs are the tunnel addresses the sender answers for. A member
	// never routes for another peer, so this is always its own address.
	AllowedIPs []string `json:"allowed_ips,omitempty"`
	// Ufrag and Pwd are the sender's ICE credentials for this session.
	Ufrag string `json:"ufrag"`
	Pwd   string `json:"pwd"`
	// Candidates are the sender's gathered ICE candidates.
	Candidates []proto.Candidate `json:"candidates,omitempty"`
	// Expires is the Unix time after which the token is refused (0: never).
	Expires int64 `json:"expires,omitempty"`
}

// Encode returns the token as base64url (unpadded) JSON.
func (t Token) Encode() (string, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	s := base64.RawURLEncoding.EncodeToString(data)
	if len(s) > MaxTokenSize {
		return "", ErrTokenSize
	}
	return s, nil
}

// ParseToken decodes and validates a token: its size, version, expiry and
// every field. A token holding any field that is not part of the format
// (for example a private key) is refused.
func ParseToken(s string) (Token, error) {
	return parseToken(s, time.Now())
}

func parseToken(s string, now time.Time) (Token, error) {
	var t Token
	s = strings.TrimSpace(s)
	if len(s) > MaxTokenSize {
		return t, ErrTokenSize
	}
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return t, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return t, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	for k := range fields {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "priv") || strings.Contains(lk, "secret") {
			return t, ErrPrivateKey
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return t, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	if t.V != TokenVersion {
		return t, fmt.Errorf("%w: %d", ErrTokenVersion, t.V)
	}
	if t.Expires != 0 && now.Unix() > t.Expires {
		return t, ErrTokenExpired
	}
	if err := t.validate(); err != nil {
		return t, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}
	return t, nil
}

func (t Token) validate() error {
	if t.Kind != KindInvite && t.Kind != KindAnswer {
		return fmt.Errorf("kind %q", t.Kind)
	}
	if !validID(t.Session) {
		return fmt.Errorf("session %q", t.Session)
	}
	if err := validPublicKey(t.PublicKey); err != nil {
		return err
	}
	addr, err := hostPrefix(t.Address)
	if err != nil {
		return err
	}
	for _, a := range t.AllowedIPs {
		p, err := netip.ParsePrefix(a)
		if err != nil || !p.IsSingleIP() || p.Addr() != addr.Addr() {
			return fmt.Errorf("allowed ip %q is not the peer's own address", a)
		}
	}
	if t.Ufrag == "" || t.Pwd == "" || len(t.Ufrag) > maxField || len(t.Pwd) > maxField {
		return errors.New("ice credentials")
	}
	if len(t.Candidates) > MaxCandidates {
		return fmt.Errorf("%d candidates (max %d)", len(t.Candidates), MaxCandidates)
	}
	for _, c := range t.Candidates {
		if err := validCandidate(c); err != nil {
			return err
		}
	}
	return nil
}

func validCandidate(c proto.Candidate) error {
	switch c.Type {
	case "host", "srflx", "prflx", "relay":
	default:
		return fmt.Errorf("candidate type %q", c.Type)
	}
	if c.Protocol != "udp" && c.Protocol != "tcp" {
		return fmt.Errorf("candidate protocol %q", c.Protocol)
	}
	if c.Address == "" || len(c.Address) > maxField || len(c.Foundation) > maxField || len(c.RelatedAddress) > maxField {
		return errors.New("candidate address")
	}
	if c.Port <= 0 || c.Port > 65535 || c.RelatedPort < 0 || c.RelatedPort > 65535 {
		return errors.New("candidate port")
	}
	return nil
}

// validID accepts 1-64 characters of [A-Za-z0-9_-].
func validID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

func validPublicKey(k string) error {
	raw, err := base64.StdEncoding.DecodeString(k)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("public key %q", k)
	}
	return nil
}

// hostPrefix parses a tunnel address that names a single host.
func hostPrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return p, fmt.Errorf("address %q: %w", s, err)
	}
	if !p.IsSingleIP() {
		return p, fmt.Errorf("address %q is not a single host (/32 or /128)", s)
	}
	return p, nil
}
