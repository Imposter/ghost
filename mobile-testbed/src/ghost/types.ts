/**
 * Ghost Client Type Definitions
 *
 * This file contains all TypeScript types for the Ghost connection abstraction layer.
 * Types are organized into: Connection phases, Result types, Native response types,
 * Event types, and Unified state shape.
 */

// =============================================================================
// Connection Phases (Explicit State Machine)
// =============================================================================

/**
 * Represents the current phase of the Ghost connection lifecycle.
 * This is an explicit state machine that prevents invalid state combinations.
 */
export type ConnectionPhase =
  | 'uninitialized' // Initial state, no client created
  | 'idle' // Client initialized, ready to start
  | 'generating_keys' // Generating WireGuard keypair
  | 'gathering' // ICE candidate gathering in progress
  | 'awaiting_peer' // Gathering complete, waiting for peer data
  | 'peer_configured' // Peer data received and configured
  | 'connecting' // ICE connection in progress
  | 'ice_connected' // ICE connected, ready for tunnel
  | 'tunnel_starting' // Starting WireGuard tunnel
  | 'connected' // Fully connected with active tunnel
  | 'reconnecting' // Attempting to reconnect
  | 'disconnected' // Connection lost
  | 'error'; // Error state

// =============================================================================
// Result Type (Consistent Error Handling)
// =============================================================================

/**
 * Result type for consistent error handling across all operations.
 * All bridge operations return this type.
 */
export type Result<T> = { success: true; data: T } | { success: false; error: string };

// =============================================================================
// Native Response Types (from Go JSON API)
// =============================================================================

/**
 * Connection state returned by GetConnectionState()
 */
export interface ConnectionStateJSON {
  iceState: string;
  tunnelState: string;
  isConnected: boolean;
  isTunnelActive: boolean;
  hasWireGuardKey?: boolean;
  hasPeerKey?: boolean;
  localIP: string;
  peerIP: string;
}

/**
 * WireGuard key pair returned by GenerateWireGuardKey()
 */
export interface WireGuardKeyJSON {
  privateKey: string;
  publicKey: string;
}

/**
 * Complete signaling data for peer exchange
 */
export interface SignalingDataJSON {
  ufrag: string;
  pwd: string;
  publicKey: string;
  candidates: CandidateJSON[];
}

/**
 * ICE candidate
 */
export interface CandidateJSON {
  type: string;
  address: string;
  port: number;
  protocol: string;
  priority: number;
  foundation?: string;
}

/**
 * HTTP response from tunnel requests
 */
export interface HTTPResultJSON {
  success: boolean;
  statusCode?: number;
  body?: string;
  headers?: Record<string, string>;
  error?: string;
  latencyMs?: number;
}

/**
 * Tunnel statistics
 */
export interface TunnelStatsJSON {
  bytesSent: number;
  bytesReceived: number;
  packetsSent: number;
  packetsReceived: number;
  lastHandshake: number;
  isActive: boolean;
}

// =============================================================================
// Event Types (Discriminated Union)
// =============================================================================

/**
 * Events emitted by the native module.
 * Uses discriminated union for type-safe event handling.
 */
export type GhostEvent =
  | { type: 'state_change'; data: string } // Contains ConnectionStateJSON as string
  | { type: 'candidate'; data: string } // Contains CandidateJSON as string
  | { type: 'connected' }
  | { type: 'disconnected'; reason?: string }
  | { type: 'tunnel_up' }
  | { type: 'tunnel_down' }
  | { type: 'error'; message: string };

// =============================================================================
// Unified State Shape
// =============================================================================

/**
 * Unified state managed by the Ghost reducer.
 * Single source of truth for all connection-related state.
 */
export interface GhostState {
  /** Current connection phase */
  phase: ConnectionPhase;

  /** Local WireGuard public key */
  publicKey: string | null;

  /** Gathered ICE candidates */
  candidates: CandidateJSON[];

  /** Local signaling data for peer exchange */
  localSignalingData: SignalingDataJSON | null;

  /** Current connection state from native module */
  connectionState: ConnectionStateJSON | null;

  /** Current error message, if any */
  error: string | null;

  /** Whether network is available */
  networkAvailable: boolean;

  /** Whether a network-triggered reconnect is in progress */
  isReconnecting: boolean;
}

// =============================================================================
// Hook Return Type
// =============================================================================

/**
 * Return type for useGhostConnection hook
 */
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
  httpGet: (url: string) => Promise<HTTPResultJSON>;
  httpPost: (url: string, contentType: string, body: string) => Promise<HTTPResultJSON>;

  // Utilities
  getSignalingData: () => SignalingDataJSON | null;
  getTunnelStats: () => TunnelStatsJSON;
  updateConnectionState: () => void;
}
