/**
 * Ghost State Reducer
 *
 * Centralized state management for Ghost connection lifecycle.
 * Uses explicit action types for predictable state transitions.
 */

import type {
  GhostState,
  ConnectionPhase,
  CandidateJSON,
  SignalingDataJSON,
  ConnectionStateJSON,
} from './types';

// =============================================================================
// Action Types
// =============================================================================

export type GhostAction =
  // Initialization
  | { type: 'INITIALIZE_START' }
  | { type: 'INITIALIZE_SUCCESS' }
  | { type: 'INITIALIZE_ERROR'; error: string }

  // Key generation
  | { type: 'GENERATE_KEY_START' }
  | { type: 'GENERATE_KEY_SUCCESS'; publicKey: string }

  // ICE gathering
  | { type: 'GATHER_START' }
  | { type: 'GATHER_COMPLETE'; signalingData: SignalingDataJSON }
  | { type: 'GATHER_CANCELLED' }
  | { type: 'ADD_CANDIDATE'; candidate: CandidateJSON }

  // Peer configuration
  | { type: 'SET_PEER_DATA_SUCCESS' }

  // Connection
  | { type: 'CONNECT_START' }
  | { type: 'ICE_CONNECTED' }

  // Tunnel
  | { type: 'TUNNEL_START' }
  | { type: 'TUNNEL_UP' }
  | { type: 'TUNNEL_DOWN' }

  // Disconnection and reconnection
  | { type: 'DISCONNECTED'; reason?: string }
  | { type: 'RECONNECT_START' }
  | { type: 'RECONNECT_SUCCESS' }
  | { type: 'RECONNECT_FAILED'; error: string }

  // Error handling
  | { type: 'ERROR'; error: string }
  | { type: 'CLEAR_ERROR' }

  // State updates
  | { type: 'UPDATE_CONNECTION_STATE'; state: ConnectionStateJSON }
  | { type: 'NETWORK_CHANGE'; available: boolean }

  // Reset
  | { type: 'RESET' };

// =============================================================================
// Initial State
// =============================================================================

export const initialState: GhostState = {
  phase: 'uninitialized',
  publicKey: null,
  candidates: [],
  localSignalingData: null,
  connectionState: null,
  error: null,
  networkAvailable: true,
  isReconnecting: false,
};

// =============================================================================
// Reducer
// =============================================================================

/**
 * Ghost state reducer.
 * Handles all state transitions for the connection lifecycle.
 */
export function ghostReducer(state: GhostState, action: GhostAction): GhostState {
  switch (action.type) {
    // -------------------------------------------------------------------------
    // Initialization
    // -------------------------------------------------------------------------
    case 'INITIALIZE_START':
      return {
        ...initialState,
        phase: 'idle',
        networkAvailable: state.networkAvailable, // Preserve network state
      };

    case 'INITIALIZE_SUCCESS':
      return {
        ...state,
        phase: 'idle',
        error: null,
      };

    case 'INITIALIZE_ERROR':
      return {
        ...state,
        phase: 'error',
        error: action.error,
      };

    // -------------------------------------------------------------------------
    // Key Generation
    // -------------------------------------------------------------------------
    case 'GENERATE_KEY_START':
      return {
        ...state,
        phase: 'generating_keys',
      };

    case 'GENERATE_KEY_SUCCESS':
      return {
        ...state,
        phase: 'idle',
        publicKey: action.publicKey,
        error: null,
      };

    // -------------------------------------------------------------------------
    // ICE Gathering
    // -------------------------------------------------------------------------
    case 'GATHER_START':
      return {
        ...state,
        phase: 'gathering',
        candidates: [],
        localSignalingData: null,
        error: null,
      };

    case 'ADD_CANDIDATE':
      return {
        ...state,
        candidates: [...state.candidates, action.candidate],
      };

    case 'GATHER_COMPLETE':
      return {
        ...state,
        phase: 'awaiting_peer',
        localSignalingData: action.signalingData,
        candidates: action.signalingData.candidates,
      };

    case 'GATHER_CANCELLED':
      return {
        ...state,
        phase: 'idle',
        candidates: [],
        localSignalingData: null,
      };

    // -------------------------------------------------------------------------
    // Peer Configuration
    // -------------------------------------------------------------------------
    case 'SET_PEER_DATA_SUCCESS':
      return {
        ...state,
        phase: 'peer_configured',
        error: null,
      };

    // -------------------------------------------------------------------------
    // Connection
    // -------------------------------------------------------------------------
    case 'CONNECT_START':
      return {
        ...state,
        phase: 'connecting',
        error: null,
      };

    case 'ICE_CONNECTED':
      return {
        ...state,
        phase: 'ice_connected',
        error: null,
      };

    // -------------------------------------------------------------------------
    // Tunnel
    // -------------------------------------------------------------------------
    case 'TUNNEL_START':
      return {
        ...state,
        phase: 'tunnel_starting',
      };

    case 'TUNNEL_UP':
      return {
        ...state,
        phase: 'connected',
        error: null,
        isReconnecting: false,
      };

    case 'TUNNEL_DOWN':
      return {
        ...state,
        phase: 'disconnected',
        connectionState: state.connectionState
          ? { ...state.connectionState, isTunnelActive: false, tunnelState: 'inactive' }
          : null,
      };

    // -------------------------------------------------------------------------
    // Disconnection and Reconnection
    // -------------------------------------------------------------------------
    case 'DISCONNECTED':
      return {
        ...state,
        phase: 'disconnected',
        error: action.reason || null,
        connectionState: state.connectionState
          ? {
              ...state.connectionState,
              isConnected: false,
              isTunnelActive: false,
              iceState: 'disconnected',
            }
          : null,
      };

    case 'RECONNECT_START':
      return {
        ...state,
        phase: 'reconnecting',
        error: null,
        isReconnecting: true,
      };

    case 'RECONNECT_SUCCESS':
      return {
        ...state,
        phase: 'connected',
        error: null,
        isReconnecting: false,
      };

    case 'RECONNECT_FAILED':
      return {
        ...state,
        phase: 'error',
        error: action.error,
        isReconnecting: false,
      };

    // -------------------------------------------------------------------------
    // Error Handling
    // -------------------------------------------------------------------------
    case 'ERROR':
      return {
        ...state,
        phase: 'error',
        error: action.error,
        isReconnecting: false,
      };

    case 'CLEAR_ERROR':
      return {
        ...state,
        error: null,
        // Only change phase if currently in error state
        phase: state.phase === 'error' ? 'idle' : state.phase,
      };

    // -------------------------------------------------------------------------
    // State Updates
    // -------------------------------------------------------------------------
    case 'UPDATE_CONNECTION_STATE':
      return {
        ...state,
        connectionState: action.state,
      };

    case 'NETWORK_CHANGE':
      return {
        ...state,
        networkAvailable: action.available,
      };

    // -------------------------------------------------------------------------
    // Reset
    // -------------------------------------------------------------------------
    case 'RESET':
      return {
        ...initialState,
        networkAvailable: state.networkAvailable, // Preserve network state
      };

    default:
      return state;
  }
}

// =============================================================================
// Selectors
// =============================================================================

/**
 * Check if currently connected (tunnel active)
 */
export function isConnected(state: GhostState): boolean {
  return state.phase === 'connected';
}

/**
 * Check if currently in a connecting phase
 */
export function isConnecting(state: GhostState): boolean {
  return ['gathering', 'connecting', 'tunnel_starting', 'reconnecting'].includes(state.phase);
}

/**
 * Check if reconnection is possible
 */
export function canReconnect(state: GhostState): boolean {
  return ['disconnected', 'error'].includes(state.phase);
}

/**
 * Check if in an error state
 */
export function hasError(state: GhostState): boolean {
  return state.phase === 'error' || state.error !== null;
}

/**
 * Get a human-readable status string
 */
export function getStatusText(state: GhostState): string {
  switch (state.phase) {
    case 'uninitialized':
      return 'Not initialized';
    case 'idle':
      return 'Ready';
    case 'generating_keys':
      return 'Generating keys...';
    case 'gathering':
      return 'Gathering candidates...';
    case 'awaiting_peer':
      return 'Waiting for peer data';
    case 'peer_configured':
      return 'Peer configured';
    case 'connecting':
      return 'Connecting...';
    case 'ice_connected':
      return 'ICE connected';
    case 'tunnel_starting':
      return 'Starting tunnel...';
    case 'connected':
      return 'Connected';
    case 'reconnecting':
      return 'Reconnecting...';
    case 'disconnected':
      return 'Disconnected';
    case 'error':
      return state.error || 'Error';
    default:
      return 'Unknown';
  }
}
