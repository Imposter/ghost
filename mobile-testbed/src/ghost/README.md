# Ghost Client Library

This directory contains the TypeScript wrapper for the native Ghost module, providing a clean API for interacting with the Go-based ghost-go library.

## Files

| File | Purpose |
|------|---------|
| `index.ts` | Public exports for the ghost module |
| `bridge.ts` | Native module bridge - communicates with Kotlin/Swift native code |
| `events.ts` | Event handling for native callbacks (candidates, state changes) |
| `reducer.ts` | State reducer for managing ghost client state |
| `types.ts` | TypeScript type definitions for the ghost API |
| `useGhostConnection.ts` | High-level React hook for connection management |

## Architecture

```
┌─────────────────────────────────────────────┐
│           useGhostConnection Hook           │
│  (High-level API for React components)      │
└────────────────────┬────────────────────────┘
                     │
┌────────────────────▼────────────────────────┐
│              reducer.ts                      │
│  (State management with useReducer)          │
└────────────────────┬────────────────────────┘
                     │
┌────────────────────▼────────────────────────┐
│     bridge.ts + events.ts                    │
│  (Native module communication)               │
└────────────────────┬────────────────────────┘
                     │
┌────────────────────▼────────────────────────┐
│        Native Module (Kotlin/Swift)          │
│  (Calls into ghost-go via JNI/cgo)          │
└─────────────────────────────────────────────┘
```

## Usage

```typescript
import { useGhostConnection } from '../ghost';

function MyComponent() {
  const {
    state,
    phase,
    isConnected,
    initialize,
    generateKey,
    startGathering,
    setPeerData,
    connect,
    startTunnel,
  } = useGhostConnection();

  // Use the hook to manage connection lifecycle
}
```

## State Phases

The connection goes through these phases:
- `uninitialized` - Client not yet created
- `initialized` - Client ready, waiting to start
- `gathering` - ICE candidate gathering in progress
- `awaiting_peer` - Gathering complete, waiting for peer data
- `connecting` - ICE connection in progress
- `tunnel_starting` - WireGuard tunnel being established
- `connected` - Tunnel active and working
- `reconnecting` - Attempting to restore lost connection
- `disconnected` - Connection closed
- `error` - An error occurred
