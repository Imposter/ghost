# Phase 2: Coordination & Proxy Layer

**Status**: 🔜 Not Started
**Dependencies**: Phase 1 (ghost-go core library)

## Overview

Phase 2 implements the coordination and proxy layers that sit on top of the core P2P library (Phase 1). This phase is split into **two parallel sub-phases** that can be developed independently:

---

### Phase 2a: .NET Coordination Backend (`ghost-coordination/`)

**Repository**: Separate .NET 8 project
**Purpose**: Centralized account-based peer management and signaling relay
**Status**: 🔜 Not Started

**Components**:
- Account management (OAuth2/OIDC integration)
- Peer registry (PostgreSQL + EF Core)
- SignalR hub for real-time signaling
- Same-account authorization enforcement
- REST APIs for peer registration and discovery

**Use Case**: Multi-user, multi-machine scenarios where users want to manage multiple machines under a single account (e.g., "Alice's iPhone", "Alice's MacBook", "Alice's iPad").

---

### Phase 2b: Ghost Proxy Library (`ghost-proxy/`)

**Repository**: Separate Go project
**Purpose**: Wrapper around ghost-go with native bindings + HTTP reverse proxy
**Status**: 🔜 Not Started

**Components**:
- Native module API (gomobile bindings for React Native)
- SignalR client (connects to .NET backend from Phase 2a)
- Signaling coordination (offer/answer/candidate exchange)
- HTTP/WebSocket reverse proxy (routes app traffic through tunnel)
- E2E encryption for WireGuard keys during signaling

**Use Case**: Provides a higher-level API for mobile apps and desktop apps that need P2P tunneling with minimal integration effort.

---

## Architecture Decision: Cloud vs. Local

This architecture supports **two deployment patterns**:

### Option A: Cloud-Coordinated (Implemented in Phase 2a)
- .NET backend in the cloud (Azure, AWS, DigitalOcean, etc.)
- Account-based peer management
- SignalR for real-time signaling
- Works globally, peers can be anywhere
- Requires internet connection for initial pairing and signaling

### Option B: Local Network Pairing (Application-Specific)
- **Out of scope for ghost-go/ghost-proxy core libraries**
- No cloud dependency for pairing
- Keys exchanged locally via QR code + HTTP POST
- Ideal for privacy-conscious users and self-hosted scenarios
- Documented in Phase 5 (see "Alternative Use Case: Local Network Pairing")

**Both options use the same ghost-go core library for P2P tunneling. The difference is only in how peers discover each other and exchange keys.**

## Architecture Diagram

```
┌──────────────────┐
│  React Native    │
│       App        │
└────────┬─────────┘
         │ gomobile (native calls)
         │ StartConnection()
         ▼
┌──────────────────┐
│   ghost-proxy    │
│  - SignalR       │──────► .NET Backend (SignalR hub)
│  - HTTP Proxy    │
└────────┬─────────┘
         │ uses
         ▼
┌──────────────────┐
│    ghost-go      │
│  (Core P2P lib)  │
└──────────────────┘
```

## Architecture Decisions

| Component | Technology | Rationale |
|-----------|------------|-----------|
| **Backend** | .NET 8 Minimal APIs + SignalR | Familiar C#, modern, excellent SignalR support |
| **Identity Provider** | External OAuth2/OIDC | Standard protocol, battle-tested (Auth0, Azure AD, Keycloak) |
| **Authorization** | Backend enforces same-account | Simple, secure, trusted backend validates all messages |
| **Peer Registration** | QR code pairing + OAuth interactive enrollment | Flexible: mobile uses QR, CLI uses interactive enrollment |
| **Signaling Transport** | SignalR over WebSocket | Real-time, bidirectional, built into .NET |
| **App Control** | gomobile native bindings | Direct function calls, no HTTP API for control |
| **HTTP Proxy** | Reverse proxy in ghost-proxy | Forwards app traffic through WireGuard tunnel |

---

## .NET Coordination Backend Architecture

### High-Level Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                     Identity Provider (IdP)                       │
│             Auth0 / Azure AD / Keycloak / etc.                   │
└───────────────────┬──────────────────────────────────────────────┘
                    │ OAuth2/OIDC
                    │
┌───────────────────▼──────────────────────────────────────────────┐
│              .NET Coordination Service                            │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │             Minimal APIs (REST)                             │ │
│  │  • POST /api/peers/register                               │ │
│  │  • GET  /api/peers                                        │ │
│  │  • POST /api/peers/pairing/initiate                       │ │
│  │  • POST /api/peers/pairing/complete                       │ │
│  └────────────────────────────────────────────────────────────┘ │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │             SignalR Hub (WebSocket)                         │ │
│  │  • SignalingHub                                             │ │
│  │  • Handles offer/answer/candidate forwarding               │ │
│  │  • Enforces same-account authorization                     │ │
│  └────────────────────────────────────────────────────────────┘ │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │             Database (PostgreSQL / SQL Server)              │ │
│  │  • Accounts, Peers, Sessions                             │ │
│  └────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────┘
                    │
                    │ WebSocket + REST
                    ▼
┌───────────────────────────────────────────────────────────────────┐
│                  Go Client Library                                │
│  • internal/coordination/client.go (REST)                        │
│  • pkg/signaling/client.go (SignalR WebSocket)                   │
└───────────────────────────────────────────────────────────────────┘
```

---

### Data Models (.NET)

```csharp
// Models/Account.cs
public class Account
{
    public Guid Id { get; set; }
    public string Email { get; set; } = string.Empty;
    public string IdpSubject { get; set; } = string.Empty; // sub from OAuth token
    public string IdpProvider { get; set; } = string.Empty; // "auth0", "azure-ad", etc.
    public DateTime CreatedAt { get; set; }
    public DateTime? LastSeenAt { get; set; }

    // Navigation
    public ICollection<Peer> Peers { get; set; } = new List<Peer>();
}

// Models/Peer.cs
public class Peer
{
    public Guid Id { get; set; }
    public Guid AccountId { get; set; }
    public string Name { get; set; } = string.Empty;
    public string Platform { get; set; } = string.Empty; // "windows", "android", "ios"
    public string Hostname { get; set; } = string.Empty;

    // Cryptographic identity
    public byte[] PublicKey { get; set; } = Array.Empty<byte>(); // Ed25519 public key

    // Connection info
    public string? ConnectionId { get; set; } // SignalR connection ID when online
    public bool IsOnline { get; set; }
    public DateTime? LastSeenAt { get; set; }

    // Timestamps
    public DateTime CreatedAt { get; set; }
    public DateTime UpdatedAt { get; set; }

    // Navigation
    public Account Account { get; set; } = null!;
}

// Models/PairingSession.cs
public class PairingSession
{
    public Guid Id { get; set; }
    public Guid AccountId { get; set; }
    public string Code { get; set; } = string.Empty; // 6-digit code or token
    public string? QrCodeData { get; set; } // Base64 QR code image
    public DateTime ExpiresAt { get; set; }
    public bool IsUsed { get; set; }
    public DateTime CreatedAt { get; set; }

    // Navigation
    public Account Account { get; set; } = null!;
}
```

---

### Database Schema (Entity Framework Core)

```csharp
// Data/AppDbContext.cs
public class AppDbContext : DbContext
{
    public DbSet<Account> Accounts { get; set; }
    public DbSet<Peer> Peers { get; set; }
    public DbSet<PairingSession> PairingSessions { get; set; }

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        // Accounts
        modelBuilder.Entity<Account>(entity =>
        {
            entity.HasKey(e => e.Id);
            entity.HasIndex(e => e.IdpSubject).IsUnique();
            entity.Property(e => e.Email).HasMaxLength(255).IsRequired();
            entity.Property(e => e.IdpProvider).HasMaxLength(50).IsRequired();
        });

        // Peers
        modelBuilder.Entity<Peer>(entity =>
        {
            entity.HasKey(e => e.Id);
            entity.HasIndex(e => e.AccountId);
            entity.Property(e => e.Name).HasMaxLength(100).IsRequired();
            entity.Property(e => e.PublicKey).IsRequired();

            entity.HasOne(e => e.Account)
                .WithMany(a => a.Peers)
                .HasForeignKey(e => e.AccountId)
                .OnDelete(DeleteBehavior.Cascade);
        });

        // Pairing Sessions
        modelBuilder.Entity<PairingSession>(entity =>
        {
            entity.HasKey(e => e.Id);
            entity.HasIndex(e => e.Code).IsUnique();
            entity.HasIndex(e => e.ExpiresAt);

            entity.HasOne(e => e.Account)
                .WithMany()
                .HasForeignKey(e => e.AccountId)
                .OnDelete(DeleteBehavior.Cascade);
        });
    }
}
```

---

### OAuth2/OIDC Integration (.NET)

```csharp
// Program.cs
var builder = WebApplication.CreateBuilder(args);

// Add authentication
builder.Services.AddAuthentication(JwtBearerDefaults.AuthenticationScheme)
    .AddJwtBearer(options =>
    {
        options.Authority = builder.Configuration["Auth:Authority"]; // IdP URL
        options.Audience = builder.Configuration["Auth:Audience"];
        options.TokenValidationParameters = new TokenValidationParameters
        {
            ValidateIssuer = true,
            ValidateAudience = true,
            ValidateLifetime = true,
            ValidateIssuerSigningKey = true,
        };

        // Support SignalR with token from query string
        options.Events = new JwtBearerEvents
        {
            OnMessageReceived = context =>
            {
                var accessToken = context.Request.Query["access_token"];
                var path = context.HttpContext.Request.Path;

                if (!string.IsNullOrEmpty(accessToken) && path.StartsWithSegments("/hubs"))
                {
                    context.Token = accessToken;
                }
                return Task.CompletedTask;
            }
        };
    });

builder.Services.AddAuthorization();

// Add SignalR
builder.Services.AddSignalR();

// Add database
builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseNpgsql(builder.Configuration.GetConnectionString("DefaultConnection")));

// Add services
builder.Services.AddScoped<IAccountService, AccountService>();
builder.Services.AddScoped<IPeerService, PeerService>();

var app = builder.Build();

app.UseAuthentication();
app.UseAuthorization();

// Map SignalR hub
app.MapHub<SignalingHub>("/hubs/signaling");

// Map APIs (next section)
app.MapPeerApis();
app.MapPairingApis();

app.Run();
```

---

### REST APIs (Minimal APIs)

```csharp
// Apis/PeerApis.cs
public static class PeerApis
{
    public static void MapPeerApis(this WebApplication app)
    {
        var group = app.MapGroup("/api/peers").RequireAuthorization();

        // Register a new peer
        group.MapPost("/register", async (
            RegisterPeerRequest request,
            IPeerService peerService,
            ClaimsPrincipal user) =>
        {
            var accountId = GetAccountId(user);

            var peer = await peerService.RegisterPeerAsync(new Peer
            {
                AccountId = accountId,
                Name = request.Name,
                Platform = request.Platform,
                Hostname = request.Hostname,
                PublicKey = Convert.FromBase64String(request.PublicKeyBase64)
            });

            return Results.Ok(new RegisterPeerResponse
            {
                PeerId = peer.Id,
                AccountId = peer.AccountId
            });
        });

        // List all peers for authenticated account
        group.MapGet("", async (IPeerService peerService, ClaimsPrincipal user) =>
        {
            var accountId = GetAccountId(user);
            var peers = await peerService.GetPeersAsync(accountId);

            return Results.Ok(peers.Select(d => new PeerInfo
            {
                Id = d.Id,
                Name = d.Name,
                Platform = d.Platform,
                PublicKey = Convert.ToBase64String(d.PublicKey),
                IsOnline = d.IsOnline,
                LastSeenAt = d.LastSeenAt
            }));
        });

        // Get specific peer info (for discovery)
        group.MapGet("/{peerId:guid}", async (
            Guid peerId,
            IPeerService peerService,
            ClaimsPrincipal user) =>
        {
            var accountId = GetAccountId(user);
            var peer = await peerService.GetPeerAsync(peerId);

            if (peer == null)
                return Results.NotFound();

            // Enforce same-account access
            if (peer.AccountId != accountId)
                return Results.Forbid();

            return Results.Ok(new PeerInfo
            {
                Id = peer.Id,
                Name = peer.Name,
                Platform = peer.Platform,
                PublicKey = Convert.ToBase64String(peer.PublicKey),
                IsOnline = peer.IsOnline,
                LastSeenAt = peer.LastSeenAt
            });
        });

        // Heartbeat to update peer status
        group.MapPost("/{peerId:guid}/heartbeat", async (
            Guid peerId,
            IPeerService peerService,
            ClaimsPrincipal user) =>
        {
            var accountId = GetAccountId(user);
            await peerService.UpdateHeartbeatAsync(peerId, accountId);
            return Results.NoContent();
        });
    }

    private static Guid GetAccountId(ClaimsPrincipal user)
    {
        var accountIdClaim = user.FindFirst("account_id")?.Value;
        if (accountIdClaim == null || !Guid.TryParse(accountIdClaim, out var accountId))
        {
            throw new UnauthorizedAccessException("Invalid account claim");
        }
        return accountId;
    }
}

// DTOs
public record RegisterPeerRequest(
    string Name,
    string Platform,
    string Hostname,
    string PublicKeyBase64);

public record RegisterPeerResponse(
    Guid PeerId,
    Guid AccountId);

public record PeerInfo(
    Guid Id,
    string Name,
    string Platform,
    string PublicKeyBase64,
    bool IsOnline,
    DateTime? LastSeenAt);
```

---

### Peer Pairing APIs

```csharp
// Apis/PairingApis.cs
public static class PairingApis
{
    public static void MapPairingApis(this WebApplication app)
    {
        var group = app.MapGroup("/api/pairing").RequireAuthorization();

        // Initiate pairing (for authenticated web session)
        // Returns a code that can be shown as QR or entered manually
        group.MapPost("/initiate", async (
            IPairingService pairingService,
            ClaimsPrincipal user) =>
        {
            var accountId = GetAccountId(user);
            var session = await pairingService.CreatePairingSessionAsync(accountId);

            return Results.Ok(new InitiatePairingResponse
            {
                Code = session.Code,
                QrCodeDataUrl = session.QrCodeData, // data:image/png;base64,...
                ExpiresAt = session.ExpiresAt
            });
        });

        // Complete pairing (called by peer with the code)
        group.MapPost("/complete", async (
            CompletePairingRequest request,
            IPairingService pairingService,
            IPeerService peerService) =>
        {
            // Validate pairing code
            var session = await pairingService.ValidatePairingCodeAsync(request.Code);
            if (session == null)
                return Results.BadRequest(new { error = "Invalid or expired code" });

            // Register peer
            var peer = await peerService.RegisterPeerAsync(new Peer
            {
                AccountId = session.AccountId,
                Name = request.PeerName,
                Platform = request.Platform,
                Hostname = request.Hostname,
                PublicKey = Convert.FromBase64String(request.PublicKeyBase64)
            });

            // Mark session as used
            await pairingService.MarkSessionUsedAsync(session.Id);

            // Generate access token for the peer
            var token = await pairingService.GeneratePeerTokenAsync(peer);

            return Results.Ok(new CompletePairingResponse
            {
                PeerId = peer.Id,
                AccountId = peer.AccountId,
                AccessToken = token
            });
        });
    }
}

public record InitiatePairingResponse(
    string Code,
    string QrCodeDataUrl,
    DateTime ExpiresAt);

public record CompletePairingRequest(
    string Code,
    string PeerName,
    string Platform,
    string Hostname,
    string PublicKeyBase64);

public record CompletePairingResponse(
    Guid PeerId,
    Guid AccountId,
    string AccessToken);
```

---

### SignalR Hub (Real-time Signaling)

```csharp
// Hubs/SignalingHub.cs
[Authorize]
public class SignalingHub : Hub
{
    private readonly IPeerService _peerService;
    private readonly ILogger<SignalingHub> _logger;

    // In-memory connection tracking (use Redis for production)
    private static readonly ConcurrentDictionary<Guid, string> _peerConnections = new();

    public SignalingHub(IPeerService peerService, ILogger<SignalingHub> logger)
    {
        _peerService = peerService;
        _logger = logger;
    }

    public override async Task OnConnectedAsync()
    {
        var peerId = GetPeerId();
        var accountId = GetAccountId();

        // Update peer connection status
        _peerConnections[peerId] = Context.ConnectionId;
        await _peerService.UpdateConnectionStatusAsync(peerId, Context.ConnectionId, true);

        _logger.LogInformation("Peer {PeerId} connected from account {AccountId}",
            peerId, accountId);

        await base.OnConnectedAsync();
    }

    public override async Task OnDisconnectedAsync(Exception? exception)
    {
        var peerId = GetPeerId();

        _peerConnections.TryRemove(peerId, out _);
        await _peerService.UpdateConnectionStatusAsync(peerId, null, false);

        _logger.LogInformation("Peer {PeerId} disconnected", peerId);

        await base.OnDisconnectedAsync(exception);
    }

    // Client calls: SendOffer
    public async Task<SendMessageResult> SendOffer(Guid toPeerId, SignalingMessage message)
    {
        return await SendMessageToPeer(toPeerId, "ReceiveOffer", message);
    }

    // Client calls: SendAnswer
    public async Task<SendMessageResult> SendAnswer(Guid toPeerId, SignalingMessage message)
    {
        return await SendMessageToPeer(toPeerId, "ReceiveAnswer", message);
    }

    // Client calls: SendCandidate (trickle ICE)
    public async Task<SendMessageResult> SendCandidate(Guid toPeerId, SignalingMessage message)
    {
        return await SendMessageToPeer(toPeerId, "ReceiveCandidate", message);
    }

    private async Task<SendMessageResult> SendMessageToPeer(
        Guid toPeerId,
        string method,
        SignalingMessage message)
    {
        var fromPeerId = GetPeerId();
        var accountId = GetAccountId();

        // Validate target peer exists and is in same account
        var targetPeer = await _peerService.GetPeerAsync(toPeerId);
        if (targetPeer == null)
        {
            return new SendMessageResult { Success = false, Error = "Peer not found" };
        }

        // CRITICAL: Enforce same-account authorization
        if (targetPeer.AccountId != accountId)
        {
            _logger.LogWarning("Peer {From} attempted to message peer {To} in different account",
                fromPeerId, toPeerId);
            return new SendMessageResult { Success = false, Error = "Forbidden" };
        }

        // Check if target is online
        if (!_peerConnections.TryGetValue(toPeerId, out var connectionId))
        {
            return new SendMessageResult { Success = false, Error = "Peer offline" };
        }

        // Add sender info
        message.FromPeerId = fromPeerId;
        message.ToPeerId = toPeerId;
        message.Timestamp = DateTime.UtcNow;

        // Forward message to target peer
        await Clients.Client(connectionId).SendAsync(method, message);

        _logger.LogDebug("Forwarded {Method} from {From} to {To}", method, fromPeerId, toPeerId);

        return new SendMessageResult { Success = true };
    }

    private Guid GetPeerId()
    {
        var claim = Context.User?.FindFirst("peer_id")?.Value;
        if (claim == null || !Guid.TryParse(claim, out var peerId))
            throw new HubException("Invalid peer_id claim");
        return peerId;
    }

    private Guid GetAccountId()
    {
        var claim = Context.User?.FindFirst("account_id")?.Value;
        if (claim == null || !Guid.TryParse(claim, out var accountId))
            throw new HubException("Invalid account_id claim");
        return accountId;
    }
}

// SignalR message DTOs
public class SignalingMessage
{
    public Guid FromPeerId { get; set; }
    public Guid ToPeerId { get; set; }
    public string Type { get; set; } = string.Empty; // "offer", "answer", "candidate"
    public string Payload { get; set; } = string.Empty; // JSON payload (E2E encrypted)
    public DateTime Timestamp { get; set; }
}

public class SendMessageResult
{
    public bool Success { get; set; }
    public string? Error { get; set; }
}
```

---

### JWT Token Generation with Peer Claims

```csharp
// Services/PairingService.cs
public class PairingService : IPairingService
{
    private readonly IConfiguration _configuration;
    private readonly AppDbContext _db;

    public async Task<string> GeneratePeerTokenAsync(Peer peer)
    {
        var tokenHandler = new JwtSecurityTokenHandler();
        var key = Encoding.ASCII.GetBytes(_configuration["Jwt:Secret"]!);

        var tokenDescriptor = new SecurityTokenDescriptor
        {
            Subject = new ClaimsIdentity(new[]
            {
                new Claim("peer_id", peer.Id.ToString()),
                new Claim("account_id", peer.AccountId.ToString()),
                new Claim("peer_name", peer.Name),
                new Claim(ClaimTypes.Role, "peer")
            }),
            Expires = DateTime.UtcNow.AddYears(1), // Long-lived for peers
            SigningCredentials = new SigningCredentials(
                new SymmetricSecurityKey(key),
                SecurityAlgorithms.HmacSha256Signature)
        };

        var token = tokenHandler.CreateToken(tokenDescriptor);
        return tokenHandler.WriteToken(token);
    }
}
```

---

## Go Client Updates

Now update the Go client to work with the account-based backend:

### Updated Coordination Client

```go
// internal/coordination/client.go
type Client struct {
    baseURL    string
    httpClient *http.Client

    // Authentication
    accessToken string  // JWT from peer registration
    peerID    string  // Our peer ID
    accountID   string  // Our account ID

    logger *slog.Logger
}

// Register registers this peer with the coordination service
// using a pairing code from QR or interactive enrollment.
func (c *Client) RegisterWithPairingCode(ctx context.Context, code string, peerInfo *PeerInfo) error {
    req := struct {
        Code            string `json:"code"`
        PeerName      string `json:"peerName"`
        Platform        string `json:"platform"`
        Hostname        string `json:"hostname"`
        PublicKeyBase64 string `json:"publicKeyBase64"`
    }{
        Code:            code,
        PeerName:      peerInfo.Name,
        Platform:        peerInfo.Platform,
        Hostname:        peerInfo.Hostname,
        PublicKeyBase64: base64.StdEncoding.EncodeToString(peerInfo.PublicKey),
    }

    resp := struct {
        PeerId    string `json:"peerId"`
        AccountId   string `json:"accountId"`
        AccessToken string `json:"accessToken"`
    }{}

    if err := c.post(ctx, "/api/pairing/complete", req, &resp); err != nil {
        return fmt.Errorf("complete pairing: %w", err)
    }

    // Store credentials
    c.peerID = resp.PeerId
    c.accountID = resp.AccountId
    c.accessToken = resp.AccessToken

    c.logger.Info("peer registered",
        "peer_id", c.peerID,
        "account_id", c.accountID)

    return nil
}

// DiscoverPeer fetches info about another peer in the same account.
func (c *Client) DiscoverPeer(ctx context.Context, peerID string) (*PeerInfo, error) {
    var info PeerInfo
    err := c.get(ctx, fmt.Sprintf("/api/peers/%s", peerID), &info)
    if err != nil {
        return nil, fmt.Errorf("discover peer: %w", err)
    }
    return &info, nil
}

// ListPeers returns all peers in the account.
func (c *Client) ListPeers(ctx context.Context) ([]*PeerInfo, error) {
    var peers []*PeerInfo
    err := c.get(ctx, "/api/peers", &peers)
    if err != nil {
        return nil, fmt.Errorf("list peers: %w", err)
    }
    return peers, nil
}

func (c *Client) get(ctx context.Context, path string, result interface{}) error {
    req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
    if err != nil {
        return err
    }
    req.Header.Set("Authorization", "Bearer "+c.accessToken)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    return json.NewDecoder(resp.Body).Decode(result)
}
```

### Updated SignalR Signaling Client

```go
// pkg/signaling/client.go
type Client struct {
    hub *signalr.Client

    peerID  string
    accountID string

    // Callbacks
    onOffer     func(*SignalingMessage) error
    onAnswer    func(*SignalingMessage) error
    onCandidate func(*SignalingMessage) error

    logger *slog.Logger
}

// Connect establishes SignalR connection to the hub.
func (c *Client) Connect(ctx context.Context, coordinationURL, accessToken string) error {
    hubURL := coordinationURL + "/hubs/signaling"

    // Create SignalR client with JWT auth
    conn := signalr.WithConnection(
        signalr.NewHTTPConnection(hubURL, signalr.WithHTTPQuery(
            map[string]string{"access_token": accessToken},
        )),
    )

    c.hub = signalr.NewClient(ctx, conn)

    // Register server-to-client handlers
    c.hub.On("ReceiveOffer", c.handleReceiveOffer)
    c.hub.On("ReceiveAnswer", c.handleReceiveAnswer)
    c.hub.On("ReceiveCandidate", c.handleReceiveCandidate)

    // Start connection
    if err := c.hub.Start(); err != nil {
        return fmt.Errorf("start SignalR: %w", err)
    }

    c.logger.Info("connected to signaling hub")
    return nil
}

// SendOffer sends an ICE offer to another peer.
func (c *Client) SendOffer(ctx context.Context, toPeerID string, offer *OfferPayload) error {
    payload, err := json.Marshal(offer)
    if err != nil {
        return fmt.Errorf("marshal offer: %w", err)
    }

    msg := &SignalingMessage{
        Type:    "offer",
        Payload: string(payload),
    }

    // Call SignalR method
    result := struct {
        Success bool   `json:"success"`
        Error   string `json:"error"`
    }{}

    if err := c.hub.Invoke(ctx, "SendOffer", toPeerID, msg).Await(ctx, &result); err != nil {
        return fmt.Errorf("send offer: %w", err)
    }

    if !result.Success {
        return fmt.Errorf("send offer failed: %s", result.Error)
    }

    return nil
}

func (c *Client) handleReceiveOffer(msg *SignalingMessage) {
    if c.onOffer != nil {
        if err := c.onOffer(msg); err != nil {
            c.logger.Error("handle offer failed", "error", err)
        }
    }
}
```

---

## Components

### 2.1 Signaling Client (`pkg/signaling/client.go`)

WebSocket-based signaling client for real-time communication with coordination service.

```go
type Client struct {
    conn      *websocket.Conn
    peerID  string
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
    peerID  string
}

func (c *CoordinationClient) Register(ctx context.Context, info *PeerInfo) error
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

---

## Phase 2b: Ghost Proxy Library

### Repository Structure

```
ghost-proxy/                          # New Go project
├── go.mod
├── pkg/
│   └── proxy/                       # HTTP/WebSocket reverse proxy
│       ├── proxy.go                 # Reverse proxy implementation
│       ├── handler.go               # HTTP handlers
│       └── middleware.go            # Logging, metrics
├── internal/
│   ├── signaling/                   # SignalR client
│   │   ├── client.go                # SignalR WebSocket client
│   │   ├── coordinator.go           # ICE negotiation coordinator
│   │   └── messages.go              # Message types (match .NET DTOs)
│   └── crypto/                      # Key exchange encryption
│       ├── keyexchange.go           # E2E encryption for WG keys
│       └── x25519.go                # ECDH operations
├── cmd/
│   └── ghost-mobile/                # gomobile entry point
│       └── main.go                  # Exported functions for mobile
└── bindings/                        # Generated by gomobile
    ├── android/                     # .aar file
    └── ios/                         # .framework

Total: Separate repository from ghost-go
```

### Native Module API (gomobile)

```go
// cmd/ghost-mobile/main.go
package main

import (
    "C"
    "context"
    "encoding/json"
    "fmt"

    "ghost-proxy/internal/signaling"
    "ghost-proxy/pkg/proxy"
    ghostgo "ghost-go/pkg/tunnel"  // Import core library
)

var (
    currentTunnel  *ghostgo.Tunnel
    currentProxy   *proxy.ReverseProxy
    signalingClient *signaling.Client
)

// Initialize sets up the proxy with coordination server URL and peer JWT
//export Initialize
func Initialize(coordinationURL, peerJWT string) error {
    signalingClient = signaling.NewClient(coordinationURL, peerJWT)
    return signalingClient.Connect(context.Background())
}

// StartConnection initiates P2P tunnel and returns local proxy URL
// iceServersJSON: JSON array of {"urls": "stun:..."} objects
//export StartConnection
func StartConnection(peerID, peerPublicKeyB64, iceServersJSON string) (string, error) {
    ctx := context.Background()

    // 1. Parse ICE servers
    var iceServers []ICEServer
    if err := json.Unmarshal([]byte(iceServersJSON), &iceServers); err != nil {
        return "", fmt.Errorf("parse ICE servers: %w", err)
    }

    // 2. Generate local WireGuard keys
    wgPrivateKey, wgPublicKey, err := generateWGKeyPair()
    if err != nil {
        return "", fmt.Errorf("generate WG keys: %w", err)
    }

    // 3. Create ICE credentials
    localICECreds := generateICECredentials()

    // 4. Gather local ICE candidates via ghost-go
    candidates, err := gatherLocalCandidates(ctx, localICECreds, iceServers)
    if err != nil {
        return "", fmt.Errorf("gather candidates: %w", err)
    }

    // 5. E2E encrypt our WG public key with peer's signaling public key
    peerSignalingPubKey := getPeerSignalingKey(peerID) // From peer registry
    encryptedWGKey, err := encryptWGKeyForPeer(wgPublicKey, peerSignalingPubKey)
    if err != nil {
        return "", fmt.Errorf("encrypt WG key: %w", err)
    }

    // 6. Send offer via SignalR
    offer := &signaling.OfferMessage{
        ICEUfrag:       localICECreds.Ufrag,
        ICEPwd:         localICECreds.Pwd,
        Candidates:     candidates,
        EncryptedWGKey: encryptedWGKey,
    }

    answer, err := signalingClient.SendOfferAndWaitForAnswer(ctx, peerID, offer)
    if err != nil {
        return "", fmt.Errorf("signaling: %w", err)
    }

    // 7. Decrypt peer's WG key
    peerWGKey, err := decryptWGKeyFromPeer(answer.EncryptedWGKey)
    if err != nil {
        return "", fmt.Errorf("decrypt peer WG key: %w", err)
    }

    // 8. Connect via ghost-go
    tunnel, err := ghostgo.Connect(ctx, &ghostgo.Config{
        LocalICECreds:    localICECreds,
        RemoteICECreds:   answer.ICECredentials,
        RemoteCandidates: answer.Candidates,
        WGPrivateKey:     wgPrivateKey,
        WGPeerPublicKey:  peerWGKey,
        STUNServers:      extractSTUNServers(iceServers),
        TURNServers:      extractTURNServers(iceServers),
    })
    if err != nil {
        return "", fmt.Errorf("establish tunnel: %w", err)
    }

    currentTunnel = tunnel

    // 9. Start HTTP reverse proxy
    proxyServer := proxy.NewReverseProxy(tunnel)
    if err := proxyServer.Start("127.0.0.1:0"); err != nil {
        tunnel.Close()
        return "", fmt.Errorf("start proxy: %w", err)
    }

    currentProxy = proxyServer
    proxyURL := fmt.Sprintf("http://%s", proxyServer.Addr())

    return proxyURL, nil
}

// StopConnection tears down the tunnel
//export StopConnection
func StopConnection() error {
    if currentProxy != nil {
        currentProxy.Stop()
        currentProxy = nil
    }
    if currentTunnel != nil {
        currentTunnel.Close()
        currentTunnel = nil
    }
    return nil
}

// GetConnectionStatus returns JSON status
//export GetConnectionStatus
func GetConnectionStatus() string {
    if currentTunnel == nil {
        return `{"state":"disconnected"}`
    }

    metrics := currentTunnel.Metrics()
    status := map[string]interface{}{
        "state":            "connected",
        "latency_ms":       metrics.RTT.Milliseconds(),
        "bytes_sent":       metrics.BytesSent,
        "bytes_received":   metrics.BytesReceived,
        "local_addr":       currentTunnel.LocalAddr().String(),
        "remote_addr":      currentTunnel.RemoteAddr().String(),
    }

    data, _ := json.Marshal(status)
    return string(data)
}

// Callbacks (for future use)
var (
    onStateChangeCallback func(state string)
    onErrorCallback       func(error string)
)

//export SetOnStateChange
func SetOnStateChange(callback func(string)) {
    onStateChangeCallback = callback
}

//export SetOnError
func SetOnError(callback func(string)) {
    onErrorCallback = callback
}

func main() {} // Required for gomobile
```

### Building Mobile Bindings

```bash
# Android
gomobile bind -target=android \
    -o bindings/android/ghostproxy.aar \
    -javapkg=com.ghost.proxy \
    ./cmd/ghost-mobile

# iOS
gomobile bind -target=ios \
    -o bindings/ios/GhostProxy.framework \
    ./cmd/ghost-mobile
```

### SignalR Client Implementation

```go
// internal/signaling/client.go
package signaling

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/philippseith/signalr"
)

type Client struct {
    hub *signalr.Client
    peerID string

    // Pending offers/answers
    pendingAnswers map[string]chan *AnswerMessage

    logger *slog.Logger
}

func NewClient(coordinationURL, peerJWT string) *Client {
    return &Client{
        peerID:       extractPeerIDFromJWT(peerJWT),
        pendingAnswers: make(map[string]chan *AnswerMessage),
    }
}

func (c *Client) Connect(ctx context.Context) error {
    hubURL := coordinationURL + "/hubs/signaling"

    conn := signalr.WithConnection(
        signalr.NewHTTPConnection(hubURL, signalr.WithHTTPQuery(
            map[string]string{"access_token": c.peerJWT},
        )),
    )

    c.hub = signalr.NewClient(ctx, conn)

    // Register callbacks
    c.hub.On("ReceiveOffer", c.handleReceiveOffer)
    c.hub.On("ReceiveAnswer", c.handleReceiveAnswer)
    c.hub.On("ReceiveCandidate", c.handleReceiveCandidate)

    return c.hub.Start()
}

func (c *Client) SendOfferAndWaitForAnswer(
    ctx context.Context,
    toPeerID string,
    offer *OfferMessage,
) (*AnswerMessage, error) {
    // Create channel for answer
    answerChan := make(chan *AnswerMessage, 1)
    c.pendingAnswers[toPeerID] = answerChan
    defer delete(c.pendingAnswers, toPeerID)

    // Send offer via SignalR
    msg := &SignalingMessage{
        Type:    "offer",
        Payload: mustMarshal(offer),
    }

    result := struct {
        Success bool
        Error   string
    }{}

    if err := c.hub.Invoke(ctx, "SendOffer", toPeerID, msg).Await(ctx, &result); err != nil {
        return nil, fmt.Errorf("invoke SendOffer: %w", err)
    }

    if !result.Success {
        return nil, fmt.Errorf("signaling error: %s", result.Error)
    }

    // Wait for answer
    select {
    case answer := <-answerChan:
        return answer, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (c *Client) handleReceiveAnswer(msg *SignalingMessage) {
    var answer AnswerMessage
    if err := json.Unmarshal([]byte(msg.Payload), &answer); err != nil {
        c.logger.Error("unmarshal answer", "error", err)
        return
    }

    // Send to waiting goroutine
    if ch, exists := c.pendingAnswers[msg.FromPeerID]; exists {
        ch <- &answer
    }
}
```

### HTTP Reverse Proxy

```go
// pkg/proxy/proxy.go
package proxy

import (
    "context"
    "fmt"
    "net"
    "net/http"
    "net/http/httputil"

    ghostgo "ghost-go/pkg/tunnel"
)

type ReverseProxy struct {
    tunnel   *ghostgo.Tunnel
    server   *http.Server
    listener net.Listener
}

func NewReverseProxy(tunnel *ghostgo.Tunnel) *ReverseProxy {
    return &ReverseProxy{
        tunnel: tunnel,
    }
}

func (rp *ReverseProxy) Start(addr string) error {
    listener, err := net.Listen("tcp", addr)
    if err != nil {
        return fmt.Errorf("listen: %w", err)
    }

    rp.listener = listener

    proxy := &httputil.ReverseProxy{
        Director: rp.director,
        Transport: &http.Transport{
            DialContext: rp.dialThroughTunnel,
        },
    }

    rp.server = &http.Server{
        Handler: proxy,
    }

    go rp.server.Serve(listener)

    return nil
}

func (rp *ReverseProxy) director(req *http.Request) {
    // App can specify target via header or use tunnel remote addr
    targetHost := req.Header.Get("X-Ghost-Target")
    if targetHost == "" {
        targetHost = rp.tunnel.RemoteAddr().String()
    }

    req.URL.Scheme = "http"
    req.URL.Host = targetHost
    req.Host = targetHost
}

func (rp *ReverseProxy) dialThroughTunnel(ctx context.Context, network, addr string) (net.Conn, error) {
    // All traffic goes through WireGuard tunnel
    // The tunnel IP routes to the peer
    return net.Dial(network, addr)
}

func (rp *ReverseProxy) Addr() string {
    return rp.listener.Addr().String()
}

func (rp *ReverseProxy) Stop() error {
    return rp.server.Shutdown(context.Background())
}
```

---

## .NET Project Structure

```
ghost-coordination/                  # New .NET project
├── GhostCoordination.sln
├── src/
│   └── GhostCoordination/
│       ├── Program.cs               # App entry, DI setup
│       ├── appsettings.json
│       ├── appsettings.Development.json
│       │
│       ├── Models/                  # Data models
│       │   ├── Account.cs
│       │   ├── Peer.cs
│       │   └── PairingSession.cs
│       │
│       ├── Data/                    # EF Core
│       │   ├── AppDbContext.cs
│       │   └── Migrations/
│       │
│       ├── Apis/                    # Minimal API endpoints
│       │   ├── PeerApis.cs
│       │   └── PairingApis.cs
│       │
│       ├── Hubs/                    # SignalR hubs
│       │   └── SignalingHub.cs
│       │
│       ├── Services/                # Business logic
│       │   ├── IAccountService.cs
│       │   ├── AccountService.cs
│       │   ├── IPeerService.cs
│       │   ├── PeerService.cs
│       │   ├── IPairingService.cs
│       │   └── PairingService.cs
│       │
│       └── Middleware/              # Custom middleware
│           └── ExceptionHandler.cs
│
├── tests/
│   └── GhostCoordination.Tests/
│       ├── PeerApiTests.cs
│       ├── PairingApiTests.cs
│       └── SignalingHubTests.cs
│
├── docker/
│   ├── Dockerfile
│   └── docker-compose.yml
│
└── README.md
```

### Configuration (appsettings.json)

```json
{
  "ConnectionStrings": {
    "DefaultConnection": "Host=localhost;Database=ghost;Username=ghost;Password=..."
  },
  "Auth": {
    "Authority": "https://your-idp.auth0.com/",
    "Audience": "ghost-coordination-api"
  },
  "Jwt": {
    "Secret": "your-jwt-secret-for-peer-tokens",
    "Issuer": "ghost-coordination",
    "Audience": "ghost-peers"
  },
  "Pairing": {
    "CodeLength": 6,
    "ExpirationMinutes": 15
  },
  "Logging": {
    "LogLevel": {
      "Default": "Information",
      "Microsoft.AspNetCore": "Warning",
      "Microsoft.AspNetCore.SignalR": "Debug"
    }
  }
}
```

### NuGet Packages Required

```xml
<ItemGroup>
  <!-- ASP.NET Core -->
  <PackageReference Include="Microsoft.AspNetCore.Authentication.JwtBearer" Version="8.0.0" />
  <PackageReference Include="Microsoft.AspNetCore.SignalR" Version="8.0.0" />

  <!-- Entity Framework Core -->
  <PackageReference Include="Microsoft.EntityFrameworkCore" Version="8.0.0" />
  <PackageReference Include="Microsoft.EntityFrameworkCore.Design" Version="8.0.0" />
  <PackageReference Include="Npgsql.EntityFrameworkCore.PostgreSQL" Version="8.0.0" />

  <!-- JWT -->
  <PackageReference Include="System.IdentityModel.Tokens.Jwt" Version="7.0.0" />

  <!-- QR Code generation -->
  <PackageReference Include="QRCoder" Version="1.4.3" />

  <!-- Testing -->
  <PackageReference Include="xunit" Version="2.6.0" />
  <PackageReference Include="Moq" Version="4.20.0" />
</ItemGroup>
```

---

## Files to Create

### Go Library Files

| File | Purpose |
|------|---------|
| `pkg/signaling/client.go` | SignalR client (connects to .NET hub) |
| `pkg/signaling/protocol.go` | Message types matching .NET DTOs |
| `internal/coordination/client.go` | REST API client for peer management |
| `internal/coordination/pairing.go` | Pairing flow implementation |
| `internal/crypto/keyexchange.go` | Secure key exchange |
| `internal/crypto/x25519.go` | Curve25519 operations |

### .NET Backend Files

| File | Purpose |
|------|---------|
| `Program.cs` | Application entry, DI, middleware |
| `Models/*.cs` | Account, Peer, PairingSession entities |
| `Data/AppDbContext.cs` | EF Core context |
| `Apis/PeerApis.cs` | Peer registration/discovery endpoints |
| `Apis/PairingApis.cs` | Pairing initiation/completion endpoints |
| `Hubs/SignalingHub.cs` | SignalR hub for real-time signaling |
| `Services/*.cs` | Business logic services |

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
