# Peer to peer

A member gets its identity, peers and ICE descriptions from a
`ghost.Signaller`. By default that is the control-plane client. Package
`ghost/direct` is a standalone signaller: two members connect with no server.
It supports token peers (ICE and WireGuard) and static peers (WireGuard only).

A direct member has no control plane behind it: no netmap policy, no
isolation, no packet filter, no health reporting and no revocation. Anyone you
exchange tokens with can reach every port you `Listen` on.

## Token peers

Each side makes one token. The inviter's token is the *invite*; the other
side's reply is the *answer*. A token is versioned base64url JSON:

| Field | Meaning |
| ----- | ------- |
| `v`, `kind` | format version (1); `invite` or `answer` |
| `session` | pairs an answer with its invite; both sides use it as the peer id |
| `public_key` | the sender's WireGuard public key |
| `address`, `allowed_ips` | the sender's tunnel address (a single host) |
| `ufrag`, `pwd`, `candidates` | the sender's ICE credentials and gathered candidates |
| `expires` | Unix time after which it is refused (default: 10 minutes) |

Tokens never carry a private key. `ParseToken` refuses a token that is larger
than 8 KiB, has another version, has expired, carries any unknown field, or
has malformed fields. Accepting a token also fails if its key or address is
already in use by this member or another peer.

```go
sig, _ := direct.New(direct.Config{Address: "100.64.0.1/32"})
node, _ := ghost.NewNode(ghost.Config{
    Signaller:      sig,
    KeyStorePath:   "keys.json",
    ConnectTimeout: 5 * time.Minute, // time allowed to apply the answer
    STUNServers:    []ghost.STUNServer{{URL: "stun:stun.l.google.com:19302"}}, // optional
})
_ = node.Start(ctx)

// Inviting side:
invite, _ := sig.CreateInvite(ctx) // send it to the other side
// ... receive the answer ...
_ = sig.AcceptAnswer(ctx, answer)

// Invited side (its own signaller, with Address "100.64.0.2/32"):
answer, _ := sig.AcceptInvite(ctx, invite) // send it back
```

Once `EventPeerConnected` arrives, `DialContext` and `Listen` work on the
tunnel addresses as usual. To carry tokens over your own channel, implement
`direct.Exchanger` (`Send` and `Receive` a token) and call
`direct.Invite(ctx, sig, x)` or `direct.Answer(ctx, sig, x)`. `direct.Pipe`
is an in-memory pair for tests. [`examples/p2p`](../ghost-go/examples/p2p)
swaps tokens over stdin and stdout.

The invited side starts ICE as soon as it accepts, so the answer must be
applied within `ConnectTimeout`. A token pair links once: if the link fails,
exchange new tokens. Without STUN only host candidates are exchanged, which
works on one LAN; across NATs, add STUN, or TURN if both ends are behind
symmetric NATs.

## Static peers

When one side has a fixed, reachable UDP endpoint, list the peers up front and
skip ICE:

```go
// Server, reachable at 203.0.113.7:51820.
direct.New(direct.Config{Address: "100.64.0.1/32", Static: []direct.StaticPeer{
    {Name: "laptop", PublicKey: laptopKey, Address: "100.64.0.2/32", ListenAddr: ":51820"},
}})

// Laptop, no fixed endpoint.
direct.New(direct.Config{Address: "100.64.0.2/32", Static: []direct.StaticPeer{
    {Name: "server", PublicKey: serverKey, Address: "100.64.0.1/32", Endpoint: "203.0.113.7:51820"},
}})
```

Get each side's public key from `ghost.LoadOrCreateKeys(path).PublicKey()` and
pass the same path as `KeyStorePath`. Each static peer uses its own UDP
socket. A peer with an `Endpoint` is talked to only at that address. A peer
without one replies to wherever its last packet came from; WireGuard drops
anything unauthenticated, so a spoofed packet can at most divert replies until
the peer's next packet.

## Writing a Signaller

`ghost.Signaller` is small: `Start`, `Events`, `State`, `Join`, `ICEServers`,
`Link`, `Send`, `ReportHealth` and `Close`. `Events` delivers the same
`signal.Event` values the control-plane client does: a connected state and a
welcome, then after `Join` a `joined` and a netmap, then deltas and the
remote peer's offer, answer and candidates. `Link` decides, per peer, which
side controls ICE or hands over a ready connection. `Send` receives the
member's own offer, answer and candidates; a candidate with no `Candidate`
marks the end of gathering. `ghost/direct` is a complete example.
