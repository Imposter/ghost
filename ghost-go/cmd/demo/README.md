# Ghost-GO Demo

This demo application shows the complete integration of ICE (NAT traversal) with WireGuard (encrypted tunneling).

## What It Does

The demo establishes a peer-to-peer encrypted tunnel between two machines:
1. Creates ICE agents for NAT traversal
2. Gathers local candidates (STUN discovery)
3. Manually exchanges signaling data (copy/paste)
4. Establishes ICE connection
5. Creates WireGuard tunnel over ICE
6. Provides interactive shell for monitoring

## Requirements

### Privileges
- **Linux**: Requires `root` or `CAP_NET_ADMIN` capability for TUN device creation
- **Windows**: Requires Administrator privileges
- **macOS**: Requires `root` or administrator privileges

### Network
- UDP connectivity (for ICE/STUN)
- Firewall must allow outbound UDP traffic
- Access to STUN server (default: stun.l.google.com:19302)

## Usage

### Running Two Peers

#### Terminal 1 (Peer A)
```bash
# Linux/macOS (requires sudo)
sudo go run ./cmd/demo -role a

# Windows (run as Administrator)
go run ./cmd/demo -role a
```

#### Terminal 2 (Peer B)
```bash
# On same or different machine
sudo go run ./cmd/demo -role b
```

### Step-by-Step Process

1. **Start both peers** with different roles (a and b)

2. **Each peer will:**
   - Create ICE agent
   - Gather candidates
   - Generate WireGuard keys
   - Display signaling data (JSON)

3. **Manual Signaling:**
   - Copy the JSON output from Peer A
   - Paste it into Peer B's terminal
   - Press Enter twice
   - Repeat for Peer B → Peer A

4. **Connection Establishment:**
   - Both peers will establish ICE connection
   - WireGuard tunnel will be created
   - Success message displayed

5. **Interactive Mode:**
   - Type commands to interact with tunnel
   - `status` - Show device status
   - `peer` - Show peer information
   - `quit` - Exit cleanly

## Example Session

### Peer A
```
╔════════════════════════════════════════════════════════╗
║            Ghost-GO Phase 1 Demo                      ║
║    ICE + WireGuard P2P Connection Demo                ║
╚════════════════════════════════════════════════════════╝

=== Running as Peer A ===

Step 1: Creating ICE agent...
✓ ICE agent created

Step 2: Getting local ICE credentials...
✓ Local credentials:
   ufrag: abc123
   pwd:   def456xyz...

Step 3: Generating WireGuard keys...
✓ Public key: abc123base64encoded...

Step 4: Gathering ICE candidates...
   (This may take 10-15 seconds...)
   • Found host candidate: 192.168.1.100:54321
   • Found srflx candidate: 203.0.113.42:54321
✓ Gathered 2 candidates

Step 5: Preparing signaling data...
============================================================
YOUR SIGNALING DATA (copy this and send to peer):
============================================================
{
  "ufrag": "abc123",
  "pwd": "def456xyz...",
  "candidates": [
    {
      "type": "host",
      "address": "192.168.1.100",
      "port": 54321,
      ...
    }
  ],
  "public_key": "abc123base64encoded..."
}
============================================================

Step 6: Waiting for remote peer's signaling data...
Paste the peer's JSON data below, then press Enter twice:

[User pastes Peer B's JSON here]

✓ Received remote data (ufrag: xyz789, 2 candidates)

Step 7: Adding remote candidates...
✓ Added 2 remote candidates

Step 8: Setting remote credentials...
✓ Remote credentials set

Step 9: Establishing ICE connection...
   (This may take 5-30 seconds...)
✓ ICE connection established!
   Local:  host 192.168.1.100:54321
   Remote: host 192.168.1.200:54322

Step 10: Creating ICEBind adapter...
✓ ICEBind created

Step 11: Creating TUN device...
   (May require administrator/root privileges)
✓ TUN device created: ghost0

Step 12: Creating WireGuard device...
✓ WireGuard device created

Step 13: Configuring WireGuard...
✓ Device configured

Step 14: Adding WireGuard peer...
✓ Peer added (allowed IPs: 10.0.0.0/24)

Step 15: Bringing device up...
✓ Device is UP

============================================================
🎉 SUCCESS! Encrypted tunnel established!
============================================================

Tunnel Information:
  TUN device:  ghost0
  MTU:         1280
  Allowed IPs: 10.0.0.0/24
  Keepalive:   25s

Entering interactive mode...
Commands: status, peer, quit

> status

=== Device Status ===
private_key=(hidden)
public_key=abc123base64encoded...
listen_port=0
peer=xyz789base64encoded...
  endpoint=192.168.1.200:54322
  allowed_ip=10.0.0.0/24
  persistent_keepalive_interval=25

> quit
Shutting down...
Demo completed successfully
```

## Configuration Options

```bash
# Custom STUN server
go run ./cmd/demo -role a -stun "stun:stun.example.com:3478"

# Custom timeouts
go run ./cmd/demo -role a \
  -gather-timeout 20s \
  -conn-timeout 60s

# Custom TUN device name and MTU
go run ./cmd/demo -role a \
  -tun ghost1 \
  -mtu 1420

# Custom allowed IPs
go run ./cmd/demo -role a \
  -allowed-ips "10.1.0.0/16"

# Custom keepalive interval
go run ./cmd/demo -role a \
  -keepalive 30s
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-role` | *required* | Peer role: 'a' or 'b' |
| `-stun` | `stun:stun.l.google.com:19302` | STUN server URL |
| `-gather-timeout` | `15s` | Candidate gathering timeout |
| `-conn-timeout` | `30s` | ICE connection timeout |
| `-tun` | `ghost0` | TUN device name |
| `-mtu` | `1280` | MTU size |
| `-keepalive` | `25s` | WireGuard keepalive interval |
| `-allowed-ips` | `10.0.0.0/24` | Allowed IP ranges (CIDR) |

## Troubleshooting

### "Permission denied" creating TUN device
**Solution**: Run with `sudo` (Linux/macOS) or as Administrator (Windows)

### ICE connection fails
**Possible causes**:
- Firewall blocking UDP
- STUN server unreachable
- Symmetric NAT (may need TURN server)

**Solution**: 
- Check firewall settings
- Try different STUN server
- Verify network connectivity

### "No candidates gathered"
**Possible causes**:
- No network interfaces available
- STUN server unreachable

**Solution**:
- Check network connectivity
- Verify STUN server is accessible
- Try `-stun stun:stun1.l.google.com:19302`

### WireGuard handshake fails
**Possible causes**:
- Keys don't match
- MTU too large
- System clocks out of sync

**Solution**:
- Verify JSON was copied correctly
- Try `-mtu 1280` (safe default)
- Check system time on both peers

### Connection established but no data transfer
**Note**: This demo doesn't configure IP addresses or routing. It only demonstrates the tunnel establishment. For actual data transfer, you would need to:
1. Assign IP addresses to TUN interfaces
2. Configure routing
3. Send actual packets

This is intentionally left out to keep the demo focused on core integration.

## What This Demonstrates

✅ **ICE NAT Traversal**
- STUN server integration
- Candidate gathering (host, srflx)
- ICE connectivity checks
- Connection establishment

✅ **WireGuard Integration**
- Key generation and management
- TUN device creation (cross-platform)
- ICEBind adapter usage
- Peer configuration
- Device lifecycle

✅ **Manual Signaling**
- Credential exchange
- Candidate exchange
- Public key exchange
- JSON-based signaling format

✅ **Error Handling**
- Proper cleanup on errors
- Signal handling (Ctrl+C)
- Resource management

## Next Steps

After running the demo successfully:

1. **Phase 2**: Implement automated signaling server
2. **Phase 3**: Add automatic reconnection
3. **Phase 4**: Add HTTP proxy on top of tunnel
4. **Phase 5**: Add mobile platform support

## Notes

- This demo uses **manual signaling** (copy/paste) to demonstrate the core integration without requiring a signaling server
- Phase 2 will add a WebSocket-based signaling server for automatic candidate exchange
- The tunnel is encrypted but doesn't configure IP addresses or routing in this demo
- Both peers must be running simultaneously for connection establishment

## Architecture

```
Peer A                                    Peer B
┌──────────────┐                      ┌──────────────┐
│ ICE Agent    │◄────────────────────►│ ICE Agent    │
│ (STUN/NAT)   │   Manual Signaling   │ (STUN/NAT)   │
└──────┬───────┘   (Copy/Paste JSON)  └──────┬───────┘
       │                                      │
       │         ICE Connection               │
       └──────────────────────────────────────┘
       │                                      │
┌──────▼───────┐                      ┌──────▼───────┐
│ ICEBind      │                      │ ICEBind      │
│ (Adapter)    │                      │ (Adapter)    │
└──────┬───────┘                      └──────┬───────┘
       │                                      │
┌──────▼───────┐                      ┌──────▼───────┐
│ WireGuard    │◄────────────────────►│ WireGuard    │
│ Device       │  Encrypted Tunnel    │ Device       │
└──────┬───────┘                      └──────┬───────┘
       │                                      │
┌──────▼───────┐                      ┌──────▼───────┐
│ TUN Device   │                      │ TUN Device   │
│ (ghost0)     │                      │ (ghost0)     │
└──────────────┘                      └──────────────┘
```

---

**Phase 1 Demo - Ghost-GO Project**
