/**
 * Ghost Connection Library
 *
 * Simplified abstraction layer for Ghost P2P connections in React Native.
 *
 * Usage:
 * ```typescript
 * import { useGhostConnection } from '../ghost';
 *
 * function ConnectionScreen() {
 *   const {
 *     state,
 *     phase,
 *     isConnected,
 *     initialize,
 *     startGathering,
 *     connect,
 *     startTunnel,
 *     close,
 *   } = useGhostConnection();
 *
 *   // ...
 * }
 * ```
 */

// Main hook
export { useGhostConnection, default } from './useGhostConnection';

// Types
export type {
  // Connection phases
  ConnectionPhase,

  // Result type
  Result,

  // Native response types
  ConnectionStateJSON,
  WireGuardKeyJSON,
  SignalingDataJSON,
  CandidateJSON,
  HTTPResultJSON,
  TunnelStatsJSON,

  // Event types
  GhostEvent,

  // State types
  GhostState,
  UseGhostConnectionResult,
} from './types';

// Bridge (for advanced usage)
export { GhostBridge, isNativeAvailable } from './bridge';

// Reducer (for testing and advanced usage)
export {
  ghostReducer,
  initialState,
  isConnected,
  isConnecting,
  canReconnect,
  hasError,
  getStatusText,
} from './reducer';
export type { GhostAction } from './reducer';

// Events (for testing and advanced usage)
export {
  parseGhostEvent,
  eventToAction,
  eventToActions,
  formatEventForLog,
} from './events';
