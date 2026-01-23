# Phase 1c: React Native Ghost Client Abstraction Layer

## Problem Analysis

The current `useGhostClient.ts` hook has grown organically and exhibits several pain points:

### Identified Issues

1. **Fragmented State (8 independent useState calls)**
   - `isInitialized`, `status`, `publicKey`, `candidates`, `connectionState`, `error`, `networkState`, `isNetworkAvailable`
   - States can diverge, causing inconsistencies

2. **Boilerplate Native Wrappers (18 wrapper functions)**
   - Each follows identical pattern: call native → parse JSON → update state
   - ~300 lines of repetitive code

3. **Scattered JSON Parsing (11+ JSON.parse calls)**
   - No type validation after parsing
   - Inconsistent error handling

4. **Inconsistent Error Handling**
   - Some methods throw, some return error in JSON
   - No unified error type

5. **Complex Event Handler (60+ line switch)**
   - Mixes event parsing with state updates
   - Hard to test in isolation

6. **Navigation Lifecycle Issues**
   - Required workarounds with refs and useFocusEffect
   - Race conditions possible during focus/blur

7. **No Clear Connection Phases**
   - UI tracks `step` separately from hook state
   - Disconnect between UI state machine and underlying state

---

## Solution: Layered Abstraction Architecture

```
┌─────────────────────────────────────────────────────┐
│           ConnectionScreen.tsx                       │
│  (UI logic only - button handlers, display)          │
└────────────────────┬────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────┐
│           useGhostConnection() Hook                  │
│  (Simplified API - state, actions, phase)            │
└────────────────────┬────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────┐
│           ghostReducer + GhostState                  │
│  (Unified state, actions, transitions)               │
└────────────────────┬────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────┐
│           GhostNativeBridge                          │
│  (Type-safe native calls, Result<T> pattern)         │
└────────────────────┬────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────┐
│           NativeGhostModule                          │
│  (Raw RN native module)                              │
└─────────────────────────────────────────────────────┘
```

---

## Implementation

### Phase 1: Core Types and Bridge Layer

#### 1.1 Create `src/ghost/types.ts`

```typescript
// Connection phases (explicit state machine)
export type ConnectionPhase =
  | 'uninitialized'
  | 'idle'
  | 'generating_keys'
  | 'gathering'
  | 'awaiting_peer'
  | 'peer_configured'
  | 'connecting'
  | 'ice_connected'
  | 'tunnel_starting'
  | 'connected'
  | 'reconnecting'
  | 'disconnected'
  | 'error';

// Result type for consistent error handling
export type Result<T> =
  | { success: true; data: T }
  | { success: false; error: string };

// Native response types (from Go JSON)
export interface ConnectionStateJSON {
  iceState: string;
  tunnelState: string;
  hasWireGuardKey: boolean;
  hasPeerKey: boolean;
  localIP: string;
  peerIP: string;
}

export interface WireGuardKeyJSON {
  privateKey: string;
  publicKey: string;
}

export interface SignalingDataJSON {
  ufrag: string;
  pwd: string;
  publicKey: string;
  candidates: CandidateJSON[];
}

export interface CandidateJSON {
  type: string;
  address: string;
  port: number;
  protocol: string;
  priority: number;
}

// Event types (discriminated union)
export type GhostEvent =
  | { type: 'state_change'; iceState: string; tunnelState: string }
  | { type: 'candidate'; candidate: CandidateJSON }
  | { type: 'connected' }
  | { type: 'disconnected'; reason?: string }
  | { type: 'tunnel_up' }
  | { type: 'tunnel_down' }
  | { type: 'error'; message: string };

// Unified state shape
export interface GhostState {
  phase: ConnectionPhase;
  publicKey: string | null;
  candidates: CandidateJSON[];
  localSignalingData: SignalingDataJSON | null;
  connectionState: ConnectionStateJSON | null;
  error: string | null;
  networkAvailable: boolean;
}
```

#### 1.2 Create `src/ghost/bridge.ts`

```typescript
import { NativeModules } from 'react-native';
import type { Result, ConnectionStateJSON, SignalingDataJSON, WireGuardKeyJSON } from './types';

const { GhostModule } = NativeModules;

// Parse JSON with error handling
function parseJSON<T>(json: string): Result<T> {
  try {
    const parsed = JSON.parse(json);
    if (parsed.error) {
      return { success: false, error: parsed.error };
    }
    return { success: true, data: parsed as T };
  } catch (e) {
    return { success: false, error: `JSON parse error: ${e}` };
  }
}

// Type-safe bridge to native module
export const GhostBridge = {
  async newClient(stunServers: string): Promise<Result<void>> {
    try {
      await GhostModule.newClient(stunServers);
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  async close(): Promise<Result<void>> {
    try {
      await GhostModule.close();
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  async generateWireGuardKey(): Promise<Result<WireGuardKeyJSON>> {
    try {
      const json = await GhostModule.generateWireGuardKey();
      return parseJSON<WireGuardKeyJSON>(json);
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  getPublicKey(): string {
    return GhostModule.getPublicKey();
  },

  async startGathering(): Promise<Result<void>> {
    try {
      await GhostModule.startGathering();
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  async cancelGathering(): Promise<Result<void>> {
    try {
      await GhostModule.cancelGathering();
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  getSignalingData(): Result<SignalingDataJSON> {
    const json = GhostModule.getSignalingData();
    return parseJSON<SignalingDataJSON>(json);
  },

  async setSignalingData(data: string): Promise<Result<void>> {
    try {
      await GhostModule.setSignalingData(data);
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  async connect(isControlling: boolean): Promise<Result<void>> {
    try {
      await GhostModule.connect(isControlling);
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  async startTunnel(): Promise<Result<void>> {
    try {
      await GhostModule.startTunnel();
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  getConnectionState(): Result<ConnectionStateJSON> {
    const json = GhostModule.getConnectionState();
    return parseJSON<ConnectionStateJSON>(json);
  },

  async reconnect(): Promise<Result<void>> {
    try {
      await GhostModule.reconnect();
      return { success: true, data: undefined };
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  // HTTP methods
  async httpGet(url: string): Promise<Result<{ statusCode: number; body: string }>> {
    try {
      const json = await GhostModule.httpGet(url);
      return parseJSON(json);
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },

  async httpPost(url: string, contentType: string, body: string): Promise<Result<{ statusCode: number; body: string }>> {
    try {
      const json = await GhostModule.httpPost(url, contentType, body);
      return parseJSON(json);
    } catch (e: any) {
      return { success: false, error: e.message || String(e) };
    }
  },
};
```

### Phase 2: State Management

#### 2.1 Create `src/ghost/reducer.ts`

```typescript
import type { GhostState, ConnectionPhase, GhostEvent, CandidateJSON, SignalingDataJSON, ConnectionStateJSON } from './types';

export type GhostAction =
  | { type: 'INITIALIZE_START' }
  | { type: 'INITIALIZE_SUCCESS' }
  | { type: 'INITIALIZE_ERROR'; error: string }
  | { type: 'GENERATE_KEY_START' }
  | { type: 'GENERATE_KEY_SUCCESS'; publicKey: string }
  | { type: 'GATHER_START' }
  | { type: 'GATHER_COMPLETE'; signalingData: SignalingDataJSON }
  | { type: 'ADD_CANDIDATE'; candidate: CandidateJSON }
  | { type: 'SET_PEER_DATA_SUCCESS' }
  | { type: 'CONNECT_START' }
  | { type: 'ICE_CONNECTED' }
  | { type: 'TUNNEL_START' }
  | { type: 'TUNNEL_UP' }
  | { type: 'DISCONNECTED'; reason?: string }
  | { type: 'RECONNECT_START' }
  | { type: 'ERROR'; error: string }
  | { type: 'RESET' }
  | { type: 'UPDATE_CONNECTION_STATE'; state: ConnectionStateJSON }
  | { type: 'NETWORK_CHANGE'; available: boolean };

export const initialState: GhostState = {
  phase: 'uninitialized',
  publicKey: null,
  candidates: [],
  localSignalingData: null,
  connectionState: null,
  error: null,
  networkAvailable: true,
};

export function ghostReducer(state: GhostState, action: GhostAction): GhostState {
  switch (action.type) {
    case 'INITIALIZE_START':
      return { ...initialState, phase: 'idle' };

    case 'INITIALIZE_SUCCESS':
      return { ...state, phase: 'idle', error: null };

    case 'INITIALIZE_ERROR':
      return { ...state, phase: 'error', error: action.error };

    case 'GENERATE_KEY_START':
      return { ...state, phase: 'generating_keys' };

    case 'GENERATE_KEY_SUCCESS':
      return { ...state, phase: 'idle', publicKey: action.publicKey };

    case 'GATHER_START':
      return { ...state, phase: 'gathering', candidates: [] };

    case 'ADD_CANDIDATE':
      return { ...state, candidates: [...state.candidates, action.candidate] };

    case 'GATHER_COMPLETE':
      return { ...state, phase: 'awaiting_peer', localSignalingData: action.signalingData };

    case 'SET_PEER_DATA_SUCCESS':
      return { ...state, phase: 'peer_configured' };

    case 'CONNECT_START':
      return { ...state, phase: 'connecting' };

    case 'ICE_CONNECTED':
      return { ...state, phase: 'ice_connected' };

    case 'TUNNEL_START':
      return { ...state, phase: 'tunnel_starting' };

    case 'TUNNEL_UP':
      return { ...state, phase: 'connected', error: null };

    case 'DISCONNECTED':
      return { ...state, phase: 'disconnected', error: action.reason || 'Connection lost' };

    case 'RECONNECT_START':
      return { ...state, phase: 'reconnecting', error: null };

    case 'ERROR':
      return { ...state, phase: 'error', error: action.error };

    case 'RESET':
      return initialState;

    case 'UPDATE_CONNECTION_STATE':
      return { ...state, connectionState: action.state };

    case 'NETWORK_CHANGE':
      return { ...state, networkAvailable: action.available };

    default:
      return state;
  }
}
```

#### 2.2 Create `src/ghost/events.ts`

```typescript
import type { GhostEvent, GhostAction } from './types';

export function parseGhostEvent(json: string): GhostEvent | null {
  try {
    const parsed = JSON.parse(json);
    // Validate event structure
    if (!parsed.type) return null;
    return parsed as GhostEvent;
  } catch {
    return null;
  }
}

export function eventToAction(event: GhostEvent): GhostAction | null {
  switch (event.type) {
    case 'connected':
      return { type: 'ICE_CONNECTED' };
    case 'tunnel_up':
      return { type: 'TUNNEL_UP' };
    case 'tunnel_down':
    case 'disconnected':
      return { type: 'DISCONNECTED', reason: event.reason };
    case 'error':
      return { type: 'ERROR', error: event.message };
    case 'candidate':
      return { type: 'ADD_CANDIDATE', candidate: event.candidate };
    case 'state_change':
      // State changes handled separately via polling or dedicated action
      return null;
    default:
      return null;
  }
}
```

### Phase 3: Main Hook

#### 3.1 Create `src/ghost/useGhostConnection.ts`

```typescript
import { useReducer, useCallback, useEffect, useRef } from 'react';
import { NativeEventEmitter, NativeModules } from 'react-native';
import NetInfo from '@react-native-community/netinfo';
import { useFocusEffect } from '@react-navigation/native';
import { GhostBridge } from './bridge';
import { ghostReducer, initialState, GhostAction } from './reducer';
import { parseGhostEvent, eventToAction } from './events';
import type { GhostState, ConnectionPhase, SignalingDataJSON } from './types';

const { GhostModule } = NativeModules;

export interface UseGhostConnectionResult {
  // State
  state: GhostState;
  phase: ConnectionPhase;
  isConnected: boolean;
  isConnecting: boolean;
  canReconnect: boolean;

  // Actions
  initialize: (stunServers: string) => Promise<void>;
  generateKey: () => Promise<string | null>;
  startGathering: () => Promise<SignalingDataJSON | null>;
  cancelGathering: () => Promise<void>;
  setPeerData: (json: string) => Promise<boolean>;
  connect: (isControlling: boolean) => Promise<boolean>;
  startTunnel: () => Promise<boolean>;
  reconnect: () => Promise<boolean>;
  close: () => Promise<void>;
  reset: () => void;

  // HTTP
  httpGet: (url: string) => Promise<{ success: boolean; statusCode?: number; body?: string; error?: string }>;
  httpPost: (url: string, contentType: string, body: string) => Promise<{ success: boolean; statusCode?: number; body?: string; error?: string }>;
}

export function useGhostConnection(): UseGhostConnectionResult {
  const [state, dispatch] = useReducer(ghostReducer, initialState);
  const clientRef = useRef(false);
  const phaseRef = useRef(state.phase);
  phaseRef.current = state.phase;

  // Derived state
  const isConnected = state.phase === 'connected';
  const isConnecting = ['gathering', 'connecting', 'tunnel_starting', 'reconnecting'].includes(state.phase);
  const canReconnect = ['disconnected', 'error'].includes(state.phase);

  // Initialize client
  const initialize = useCallback(async (stunServers: string) => {
    dispatch({ type: 'INITIALIZE_START' });
    const result = await GhostBridge.newClient(stunServers);
    if (result.success) {
      clientRef.current = true;
      dispatch({ type: 'INITIALIZE_SUCCESS' });
    } else {
      dispatch({ type: 'INITIALIZE_ERROR', error: result.error });
    }
  }, []);

  // Generate WireGuard key
  const generateKey = useCallback(async (): Promise<string | null> => {
    dispatch({ type: 'GENERATE_KEY_START' });
    const result = await GhostBridge.generateWireGuardKey();
    if (result.success) {
      dispatch({ type: 'GENERATE_KEY_SUCCESS', publicKey: result.data.publicKey });
      return result.data.publicKey;
    } else {
      dispatch({ type: 'ERROR', error: result.error });
      return null;
    }
  }, []);

  // Start ICE gathering
  const startGathering = useCallback(async (): Promise<SignalingDataJSON | null> => {
    dispatch({ type: 'GATHER_START' });
    const result = await GhostBridge.startGathering();
    if (result.success) {
      const signalingResult = GhostBridge.getSignalingData();
      if (signalingResult.success) {
        dispatch({ type: 'GATHER_COMPLETE', signalingData: signalingResult.data });
        return signalingResult.data;
      }
    }
    dispatch({ type: 'ERROR', error: result.success ? 'Failed to get signaling data' : result.error });
    return null;
  }, []);

  // Cancel gathering
  const cancelGathering = useCallback(async () => {
    await GhostBridge.cancelGathering();
  }, []);

  // Set peer signaling data
  const setPeerData = useCallback(async (json: string): Promise<boolean> => {
    const result = await GhostBridge.setSignalingData(json);
    if (result.success) {
      dispatch({ type: 'SET_PEER_DATA_SUCCESS' });
      return true;
    }
    dispatch({ type: 'ERROR', error: result.error });
    return false;
  }, []);

  // Connect ICE
  const connect = useCallback(async (isControlling: boolean): Promise<boolean> => {
    dispatch({ type: 'CONNECT_START' });
    const result = await GhostBridge.connect(isControlling);
    if (result.success) {
      dispatch({ type: 'ICE_CONNECTED' });
      return true;
    }
    dispatch({ type: 'ERROR', error: result.error });
    return false;
  }, []);

  // Start tunnel
  const startTunnel = useCallback(async (): Promise<boolean> => {
    dispatch({ type: 'TUNNEL_START' });
    const result = await GhostBridge.startTunnel();
    if (result.success) {
      dispatch({ type: 'TUNNEL_UP' });
      return true;
    }
    dispatch({ type: 'ERROR', error: result.error });
    return false;
  }, []);

  // Reconnect
  const reconnect = useCallback(async (): Promise<boolean> => {
    dispatch({ type: 'RECONNECT_START' });
    const result = await GhostBridge.reconnect();
    if (result.success) {
      dispatch({ type: 'TUNNEL_UP' });
      return true;
    }
    dispatch({ type: 'ERROR', error: result.error });
    return false;
  }, []);

  // Close and reset
  const close = useCallback(async () => {
    if (clientRef.current) {
      await GhostBridge.close();
      clientRef.current = false;
    }
    dispatch({ type: 'RESET' });
  }, []);

  const reset = useCallback(() => {
    dispatch({ type: 'RESET' });
  }, []);

  // HTTP methods
  const httpGet = useCallback(async (url: string) => {
    const result = await GhostBridge.httpGet(url);
    if (result.success) {
      return { success: true, statusCode: result.data.statusCode, body: result.data.body };
    }
    return { success: false, error: result.error };
  }, []);

  const httpPost = useCallback(async (url: string, contentType: string, body: string) => {
    const result = await GhostBridge.httpPost(url, contentType, body);
    if (result.success) {
      return { success: true, statusCode: result.data.statusCode, body: result.data.body };
    }
    return { success: false, error: result.error };
  }, []);

  // Event listener
  useEffect(() => {
    const emitter = new NativeEventEmitter(GhostModule);
    const subscription = emitter.addListener('GhostEvent', (json: string) => {
      const event = parseGhostEvent(json);
      if (event) {
        const action = eventToAction(event);
        if (action) {
          dispatch(action);
        }
      }
    });
    return () => subscription.remove();
  }, []);

  // Network state listener
  useEffect(() => {
    const unsubscribe = NetInfo.addEventListener((netState) => {
      dispatch({ type: 'NETWORK_CHANGE', available: netState.isConnected ?? true });
    });
    return () => unsubscribe();
  }, []);

  return {
    state,
    phase: state.phase,
    isConnected,
    isConnecting,
    canReconnect,
    initialize,
    generateKey,
    startGathering,
    cancelGathering,
    setPeerData,
    connect,
    startTunnel,
    reconnect,
    close,
    reset,
    httpGet,
    httpPost,
  };
}
```

### Phase 4: Refactor ConnectionScreen

#### 4.1 Update `src/components/ConnectionScreen.tsx`

The screen becomes much simpler - it only handles UI logic:

```typescript
import React, { useCallback } from 'react';
import { useFocusEffect } from '@react-navigation/native';
import { useGhostConnection } from '../ghost/useGhostConnection';

export function ConnectionScreen() {
  const {
    state,
    phase,
    isConnected,
    isConnecting,
    canReconnect,
    initialize,
    generateKey,
    startGathering,
    cancelGathering,
    setPeerData,
    connect,
    startTunnel,
    reconnect,
    close,
  } = useGhostConnection();

  // Focus/blur lifecycle
  useFocusEffect(
    useCallback(() => {
      initialize('stun:stun.l.google.com:19302');
      return () => {
        if (isConnecting) {
          cancelGathering();
          close();
        }
      };
    }, [])
  );

  // UI rendering based on phase
  // ...
}
```

---

## File Structure

```
mobile-testbed/src/
├── ghost/
│   ├── index.ts              # Re-exports
│   ├── types.ts              # All TypeScript types
│   ├── bridge.ts             # Native module bridge
│   ├── reducer.ts            # State reducer and actions
│   ├── events.ts             # Event parsing and mapping
│   └── useGhostConnection.ts # Main hook
├── hooks/
│   └── useGhostClient.ts     # (DEPRECATED - keep for reference)
└── components/
    └── ConnectionScreen.tsx  # Simplified UI component
```

---

## Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `src/ghost/types.ts` | Create | Type definitions for state, events, results |
| `src/ghost/bridge.ts` | Create | Type-safe native module wrapper |
| `src/ghost/reducer.ts` | Create | State reducer with explicit phases |
| `src/ghost/events.ts` | Create | Event parsing and action mapping |
| `src/ghost/useGhostConnection.ts` | Create | Main simplified hook |
| `src/ghost/index.ts` | Create | Re-exports for clean imports |
| `src/components/ConnectionScreen.tsx` | Modify | Use new hook, simplify logic |
| `src/hooks/useGhostClient.ts` | Keep | Mark deprecated, keep for reference |

---

## Benefits

1. **Type Safety**: Discriminated unions and Result types catch errors at compile time
2. **Testability**: Each layer (bridge, reducer, events) can be unit tested independently
3. **Explicit State Machine**: Connection phases prevent invalid state combinations
4. **Single Source of Truth**: One reducer holds all connection state
5. **Simplified UI**: ConnectionScreen only handles rendering and user actions
6. **Consistent Error Handling**: Result<T> pattern everywhere
7. **Event-Driven Updates**: Native events automatically update state

---

## Verification

1. **Unit Tests**:
   - Test reducer transitions with mock actions
   - Test event parsing with sample JSON
   - Test bridge error handling

2. **Integration**:
   ```bash
   cd mobile-testbed
   npx expo prebuild --clean
   npx expo run:android
   ```

3. **Manual Testing**:
   - Full connection flow (init → gather → exchange → connect → tunnel)
   - Disconnect handling (kill peer, verify UI updates)
   - Navigation (leave during gathering, return, verify clean state)
   - Error display (invalid peer data, connection timeout)
