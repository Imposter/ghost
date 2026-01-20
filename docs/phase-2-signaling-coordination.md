# Phase 2: Signaling & Coordination

## Overview

This phase implements the signaling protocol for ICE candidate exchange and secure WireGuard key distribution, plus the coordination service for peer discovery.

---

## Components

### 2.1 Signaling Client (`pkg/signaling/client.go`)

WebSocket-based signaling client for real-time communication with coordination service.

```go
type Client struct {
    conn      *websocket.Conn
    deviceID  string
    publicKey []byte  // For E2E encryption

    // Callbacks
    onOffer     func(*OfferPayload) (*AnswerPayload, error)
    onAnswer    func(*AnswerPayload) error
    onCandidate func(*CandidatePayload) error
}

// Methods
func (c *Client) Connect(ctx context.Context) error
func (c *Client) SendOffer(ctx context.Context, toPeerID string, offer *OfferPayload) error
func (c *Client) SendAnswer(ctx context.Context, toPeerID string, answer *AnswerPayload) error
func (c *Client) TrickleCandidate(ctx context.Context, toPeerID string, candidate *Candidate) error
func (c *Client) Close() error
```

### 2.2 Signaling Protocol (`pkg/signaling/protocol.go`)

Message types for signaling communication.

```go
type MessageType string

const (
    MessageTypeOffer      MessageType = "offer"
    MessageTypeAnswer     MessageType = "answer"
    MessageTypeCandidate  MessageType = "candidate"
    MessageTypeKeyExchange MessageType = "key_exchange"
    MessageTypePing       MessageType = "ping"
    MessageTypePong       MessageType = "pong"
    MessageTypeDisconnect MessageType = "disconnect"
)

type Message struct {
    ID        string      `json:"id"`
    Type      MessageType `json:"type"`
    From      string      `json:"from"`
    To        string      `json:"to"`
    Timestamp time.Time   `json:"timestamp"`
    Payload   []byte      `json:"payload"`    // E2E encrypted
    Signature []byte      `json:"signature"`
}

type OfferPayload struct {
    ICEUfrag    string      `json:"ice_ufrag"`
    ICEPwd      string      `json:"ice_pwd"`
    Candidates  []Candidate `json:"candidates"`
    WGPublicKey []byte      `json:"wg_public_key"` // Encrypted
    TunnelConfig TunnelConfigProposal `json:"tunnel_config"`
}
```

### 2.3 Signaling Server (`pkg/signaling/server.go`)

For coordination service - handles peer registration and message routing.

```go
type Server struct {
    peers      map[string]*ConnectedPeer
    publicKeys map[string][]byte
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request)
func (s *Server) forwardMessage(msg *Message) error
func (s *Server) registerPeer(peer *ConnectedPeer)
func (s *Server) unregisterPeer(peerID string)
```

### 2.4 Secure Key Exchange (`internal/crypto/keyexchange.go`)

End-to-end encrypted key exchange with perfect forward secrecy.

```go
type KeyExchange struct {
    identityKey  ed25519.PrivateKey   // For signing
    signalingKey x25519.PrivateKey    // For encryption
}

func (ke *KeyExchange) CreateOffer(peerID string, wgPublicKey []byte) (*EncryptedKeyPayload, error)
func (ke *KeyExchange) DecryptOffer(peerID string, payload *EncryptedKeyPayload) ([]byte, error)
```

**Security properties:**
- X25519 ECDH for key agreement
- ChaCha20-Poly1305 for encryption
- Ed25519 for signatures
- Ephemeral keys for PFS

### 2.5 Coordination Client (`internal/coordination/client.go`)

Client for peer discovery and registration.

```go
type CoordinationClient struct {
    baseURL   string
    authToken string
    deviceID  string
}

func (c *CoordinationClient) Register(ctx context.Context, info *DeviceInfo) error
func (c *CoordinationClient) DiscoverPeer(ctx context.Context, peerID string) (*PeerInfo, error)
func (c *CoordinationClient) ListPeers(ctx context.Context) ([]*PeerInfo, error)
func (c *CoordinationClient) Heartbeat(ctx context.Context) error
```

---

## Protocol Flow

```
Client                    Coordination Service                   Server
   │                              │                                 │
   │──1. Register────────────────►│◄──────────1. Register───────────│
   │                              │                                 │
   │──2. Discover(serverID)──────►│                                 │
   │◄──2. PeerInfo(publicKey)─────│                                 │
   │                              │                                 │
   │══3. WebSocket Connect════════│════3. WebSocket Connect════════│
   │                              │                                 │
   │──4. Offer(ICE+WGKey)────────►│────4. Forward Offer────────────►│
   │                              │                                 │
   │◄──5. Forward Answer──────────│◄───5. Answer(ICE+WGKey)─────────│
   │                              │                                 │
   │══6. Trickle ICE Candidates══►│══════Forward═══════════════════►│
   │                              │                                 │
```

---

## Configuration

```go
type SignalingConfig struct {
    ServerURL         string        `yaml:"server_url" env:"GHOST_SIGNALING_URL,required"`
    ReconnectInterval time.Duration `yaml:"reconnect_interval" envDefault:"5s"`
    PingInterval      time.Duration `yaml:"ping_interval" envDefault:"30s"`
    MessageTimeout    time.Duration `yaml:"message_timeout" envDefault:"10s"`
}

type CoordinationConfig struct {
    BaseURL   string `yaml:"base_url" env:"GHOST_COORDINATION_URL,required"`
    AuthToken string `yaml:"auth_token" env:"GHOST_AUTH_TOKEN,required"`
}
```

---

## Files to Create

| File | Purpose |
|------|---------|
| `pkg/signaling/client.go` | WebSocket signaling client |
| `pkg/signaling/server.go` | Signaling server (coordination) |
| `pkg/signaling/protocol.go` | Message types |
| `pkg/signaling/crypto.go` | Message encryption |
| `internal/coordination/client.go` | Coordination API client |
| `internal/coordination/discovery.go` | Peer discovery |
| `internal/crypto/keyexchange.go` | Secure key exchange |
| `internal/crypto/noise.go` | Noise protocol helpers |
| `internal/crypto/x25519.go` | Curve25519 operations |

---

## Security Considerations

1. **E2E Encryption**: Signaling payloads encrypted with recipient's public key
2. **Message Signing**: All messages signed with sender's identity key
3. **PFS**: Ephemeral keys for each key exchange
4. **Replay Protection**: Message IDs and timestamps
5. **MITM Protection**: Peer keys verified via coordination service

---

## Testing Strategy

- Unit tests for encryption/decryption
- Mock WebSocket tests for signaling
- Integration tests with local coordination server
- Test message routing and forwarding
- Security tests (invalid signatures, replay attacks)
