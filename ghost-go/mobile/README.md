# Mobile Package

The `mobile` package provides a simplified, gomobile-compatible API for establishing encrypted P2P tunnels on mobile platforms (Android/iOS).

## Overview

This package wraps the core Ghost functionality (ICE NAT traversal + WireGuard encryption) into a JSON-based API suitable for mobile applications. It handles:

- ICE candidate gathering and exchange
- WireGuard key generation and management
- Tunnel establishment and lifecycle
- Event-driven state updates
- HTTP communication over the tunnel

## Resource Management

**CRITICAL: You MUST call `Close()` when done with the client to prevent resource leaks.**

`GhostClient` manages several resources that require cleanup:

| Resource | Created By | Cleanup |
|----------|------------|---------|
| ICE agent | `StartGathering()` | `Close()`, `CancelGathering()`, or re-calling `StartGathering()` |
| ICE connection | `Connect()` | `Close()`, peer disconnect, or re-calling `Connect()` |
| WireGuard device | `StartTunnel()` | `Close()`, peer disconnect, or re-calling `StartTunnel()` |
| HTTP connection pool | Internal | `Close()` |

### Safe Re-entry

All resource-allocating methods are safe to call multiple times:

- **`StartGathering()`**: Cleans up any previous ICE agent before creating a new one
- **`Connect()`**: Cleans up any previous connection (and tunnel) before connecting
- **`StartTunnel()`**: Cleans up any previous tunnel before starting a new one

This design makes reconnection safe without requiring manual cleanup:

```go
// Safe reconnection pattern
client.StartGathering()  // Creates new agent
// ... exchange signaling data ...
client.Connect(true)     // Connects
client.StartTunnel()     // Starts tunnel

// Later, if connection is lost:
client.StartGathering()  // Automatically cleans up old agent
// ... repeat ...
```

### Automatic Cleanup on Disconnect

When the peer disconnects, resources are automatically cleaned up via ICE state change callbacks. The client emits:

1. `disconnected` event with reason
2. `state_change` event with updated states

### Manual Cleanup

Always call `Close()` when done with the client:

```go
client, _ := mobile.NewClient("")
defer client.Close()  // ALWAYS do this

// ... use client ...
```

## Usage

### Complete Example

```go
package main

import (
    "github.com/Imposter/ghost/ghost-go/mobile"
)

func main() {
    // 1. Create client
    client, err := mobile.NewClient("")
    if err != nil {
        panic(err)
    }
    defer client.Close()  // CRITICAL: Always close

    // 2. Set up event callback (optional but recommended)
    client.SetEventCallback(&MyCallback{})

    // 3. Generate WireGuard keys
    keyJSON := client.GenerateWireGuardKey()
    // Parse keyJSON to get publicKey for signaling

    // 4. Start ICE candidate gathering
    if errStr := client.StartGathering(); errStr != "" {
        panic(errStr)
    }

    // 5. Get local signaling data
    signalingJSON := client.GetSignalingData()
    // Send to peer via QR code, WebSocket, etc.

    // 6. Receive peer's signaling data and set it
    if errStr := client.SetSignalingData(peerSignalingJSON); errStr != "" {
        panic(errStr)
    }

    // 7. Connect (true = controlling/initiator, false = controlled/responder)
    if errStr := client.Connect(true); errStr != "" {
        panic(errStr)
    }

    // 8. Start the tunnel
    if errStr := client.StartTunnel(); errStr != "" {
        panic(errStr)
    }

    // 9. Use the tunnel
    resultJSON := client.HTTPGet("http://10.0.0.1:8080/test")
    // Parse resultJSON for response

    // 10. Close when done (handled by defer above)
}
```

### Event Handling

Implement `mobile.EventCallback` to receive real-time events:

```go
type MyCallback struct{}

func (c *MyCallback) OnEvent(jsonStr string) {
    // Parse JSON event
    // event.type can be:
    //   - "state_change": ICE/tunnel state changed
    //   - "candidate": New ICE candidate discovered
    //   - "connected": ICE connection established
    //   - "disconnected": Peer disconnected
    //   - "tunnel_up": Tunnel is active
    //   - "tunnel_down": Tunnel is inactive
    //   - "error": An error occurred
}
```

## Thread Safety

All `GhostClient` methods are thread-safe and can be called from any goroutine. The client uses internal mutexes to protect shared state.

## JSON API

All public methods return JSON strings for gomobile compatibility:

| Method | Returns |
|--------|---------|
| `NewClient()` | `*GhostClient, error` |
| `GenerateWireGuardKey()` | `{"privateKey": "...", "publicKey": "..."}` |
| `GetPublicKey()` | Base64-encoded public key string |
| `StartGathering()` | Empty string on success, `{"error": "..."}` on failure |
| `CancelGathering()` | Empty string on success, `{"error": "..."}` on failure |
| `GetLocalCredentials()` | `{"ufrag": "...", "pwd": "..."}` |
| `GetLocalCandidatesJSON()` | `[{"type": "host", "address": "...", ...}, ...]` |
| `GetSignalingData()` | `{"ufrag": "...", "pwd": "...", "publicKey": "...", "candidates": [...]}` |
| `SetSignalingData()` | Empty string on success, `{"error": "..."}` on failure |
| `Connect()` | Empty string on success, `{"error": "..."}` on failure |
| `StartTunnel()` | Empty string on success, `{"error": "..."}` on failure |
| `GetConnectionState()` | `{"iceState": "...", "tunnelState": "...", ...}` |
| `HTTPGet()` | `{"success": true, "statusCode": 200, "body": "...", ...}` |
| `HTTPPost()` | `{"success": true, "statusCode": 200, "body": "...", ...}` |
| `GetTunnelStats()` | `{"isActive": true, "bytesSent": 123, ...}` |
| `Close()` | `error` |

## Virtual Network

The tunnel uses a virtual subnet for communication:

| Peer | Default IP |
|------|------------|
| Mobile | 10.0.0.2 |
| Desktop | 10.0.0.1 |

The subnet is 10.0.0.0/24. You can override the local IP using `SetLocalIP()`.

## Building for Mobile

### Android

```bash
cd ghost-go
gomobile bind -target=android -o mobile.aar ./mobile
```

### iOS

```bash
cd ghost-go
gomobile bind -target=ios -o Mobile.xcframework ./mobile
```

## Error Handling

Errors are returned as JSON strings with an `error` field:

```json
{"error": "ICE connection failed: timeout"}
```

Check for non-empty return values on methods that return strings:

```go
if errStr := client.StartGathering(); errStr != "" {
    // Parse errStr as JSON to get error message
}
```

## State Machine

### ICE States

```
new → checking → connected → completed
        ↓           ↓           ↓
      failed   disconnected  closed
```

### Tunnel States

```
inactive → starting → active
              ↓         ↓
           error    inactive
```

## Best Practices

1. **Always defer Close()**: Prevents resource leaks
2. **Handle events**: Subscribe to events for real-time state updates
3. **Check return values**: All methods that can fail return error JSON
4. **Use signaling data helpers**: `GetSignalingData()`/`SetSignalingData()` simplify exchange
5. **Handle disconnection**: Listen for `disconnected` events and handle accordingly
6. **Cancel gathering on navigation**: Call `CancelGathering()` when user cancels or navigates away

## Cancelling Gathering

When the user cancels the connection flow or navigates away during ICE gathering, call `CancelGathering()` to properly clean up resources:

```go
// User pressed "Cancel" button during gathering
if errStr := client.CancelGathering(); errStr != "" {
    // Handle error (rare)
}
// State is now reset to "new", can call StartGathering() again later
```

### What CancelGathering Does

1. **Cancels the gathering context**: Stops any in-progress STUN/TURN queries
2. **Closes the ICE agent**: Releases network sockets and goroutines
3. **Resets state**: Sets ICE state back to `new`
4. **Clears candidates**: Removes any gathered candidates
5. **Emits state_change event**: Notifies UI of the reset state

### When to Use CancelGathering

- User presses a "Cancel" button during the connection flow
- User navigates away from the connection screen
- App goes to background and you want to cancel the pending connection
- Timeout occurs during gathering

After `CancelGathering()`, you can safely call `StartGathering()` again to restart the process.

## Reconnection

There is no automatic reconnection. ICE connection states determine what action is required:

### ICE States and Recovery

| State | Description | Recovery |
|-------|-------------|----------|
| `disconnected` | Temporary loss of connectivity (network hiccup) | Try `Connect()` - may recover without new session |
| `failed` | Terminal failure - agent cannot recover | Must call `StartGathering()` for new session |
| `closed` | Agent was explicitly closed | Must call `StartGathering()` for new session |

### Quick Reconnect (Disconnected State)

When the connection is temporarily lost (e.g., brief network interruption), you may be able to reconnect without re-exchanging signaling data:

```go
// Try quick reconnect first
if errStr := client.Connect(isControlling); errStr != "" {
    // Quick reconnect failed - need new session
    // (agent is in failed/closed state)
}
```

### Full Reconnect (Failed/Closed State)

When the ICE agent has failed or been closed, you must start a completely new session:

```go
// Full reconnection pattern
client.StartGathering()  // Creates new agent, cleans up old one
// ... exchange signaling data with peer again ...
client.Connect(isControlling)
client.StartTunnel()
```

### Connect() State Validation

`Connect()` checks the ICE state before attempting connection:
- **Allowed states**: `new`, `checking`, `connected`, `completed`, `disconnected`
- **Blocked states**: `failed`, `closed` (returns error: "ICE agent is not usable")

This prevents using a broken agent and provides a clear error message indicating that `StartGathering()` must be called.

## Limitations

- Single peer per client (P2P, not multi-party)
- No automatic reconnection (application must call StartGathering() again)
- IPv4 only for virtual network
- Userspace networking only (no kernel TUN)
