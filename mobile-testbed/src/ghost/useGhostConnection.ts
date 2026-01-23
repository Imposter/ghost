/**
 * useGhostConnection Hook
 *
 * Simplified React hook for managing Ghost P2P connections.
 * Uses reducer-based state management and type-safe native bridge.
 */

import { useReducer, useCallback, useEffect, useRef } from 'react';
import { NativeEventEmitter, NativeModules } from 'react-native';
import NetInfo, { NetInfoState } from '@react-native-community/netinfo';

import { GhostBridge } from './bridge';
import { ghostReducer, initialState, isConnected, isConnecting, canReconnect } from './reducer';
import { parseGhostEvent, eventToActions, formatEventForLog } from './events';
import type {
  GhostState,
  ConnectionPhase,
  SignalingDataJSON,
  HTTPResultJSON,
  TunnelStatsJSON,
  UseGhostConnectionResult,
} from './types';

const { GhostModule: NativeGhostModule } = NativeModules;

// =============================================================================
// Hook Implementation
// =============================================================================

/**
 * Hook for managing Ghost P2P connections.
 *
 * Provides a simplified API with:
 * - Unified state via reducer
 * - Type-safe native bridge calls
 * - Automatic event handling
 * - Network state monitoring
 */
export function useGhostConnection(): UseGhostConnectionResult {
  const [state, dispatch] = useReducer(ghostReducer, initialState);
  const clientRef = useRef(false);

  // Track phase in ref for cleanup callbacks
  const phaseRef = useRef(state.phase);
  phaseRef.current = state.phase;

  // Derived state
  const connected = isConnected(state);
  const connecting = isConnecting(state);
  const reconnectable = canReconnect(state);

  // ===========================================================================
  // Sync with Native State on Mount
  // ===========================================================================

  useEffect(() => {
    if (!GhostBridge.isAvailable()) return;

    // Query native module for current connection state
    const connectionResult = GhostBridge.getConnectionState();
    if (connectionResult.success) {
      const connState = connectionResult.data;
      dispatch({ type: 'UPDATE_CONNECTION_STATE', state: connState });

      // If tunnel is active, sync the phase to connected
      // tunnelState values: "inactive", "starting", "active", "error"
      // iceState values: "new", "checking", "connected", "completed", "failed", "disconnected", "closed"
      if (connState.tunnelState === 'active') {
        dispatch({ type: 'TUNNEL_UP' });
        clientRef.current = true;
      } else if (connState.iceState === 'connected' || connState.iceState === 'completed') {
        dispatch({ type: 'ICE_CONNECTED' });
        clientRef.current = true;
      }
    }
  }, []);

  // ===========================================================================
  // Native Event Subscription
  // ===========================================================================

  useEffect(() => {
    if (!GhostBridge.isAvailable()) return;

    const eventEmitter = new NativeEventEmitter(NativeGhostModule);
    const subscription = eventEmitter.addListener('GhostEvent', (eventJSON: string) => {
      const event = parseGhostEvent(eventJSON);
      if (!event) return;

      // Log the event
      console.log(formatEventForLog(event));

      // Dispatch all actions for this event
      const actions = eventToActions(event);
      for (const action of actions) {
        dispatch(action);
      }
    });

    return () => {
      subscription.remove();
    };
  }, []);

  // ===========================================================================
  // Network State Monitoring
  // ===========================================================================

  useEffect(() => {
    const unsubscribe = NetInfo.addEventListener((netState: NetInfoState) => {
      const available = netState.isConnected ?? true;
      dispatch({ type: 'NETWORK_CHANGE', available });
    });

    return () => unsubscribe();
  }, []);

  // ===========================================================================
  // Actions
  // ===========================================================================

  /**
   * Initialize the Ghost client
   */
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

  /**
   * Generate WireGuard keypair
   */
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

  /**
   * Start ICE candidate gathering
   */
  const startGathering = useCallback(async (): Promise<SignalingDataJSON | null> => {
    dispatch({ type: 'GATHER_START' });

    const result = await GhostBridge.startGathering();
    if (!result.success) {
      dispatch({ type: 'ERROR', error: result.error });
      return null;
    }

    // Get signaling data after gathering completes
    const signalingResult = GhostBridge.getSignalingData();
    if (signalingResult.success) {
      dispatch({ type: 'GATHER_COMPLETE', signalingData: signalingResult.data });
      return signalingResult.data;
    } else {
      dispatch({ type: 'ERROR', error: signalingResult.error });
      return null;
    }
  }, []);

  /**
   * Cancel ICE gathering
   */
  const cancelGathering = useCallback(async () => {
    await GhostBridge.cancelGathering();
    dispatch({ type: 'GATHER_CANCELLED' });
  }, []);

  /**
   * Set peer's signaling data
   */
  const setPeerData = useCallback(async (json: string): Promise<boolean> => {
    const result = await GhostBridge.setSignalingData(json);
    if (result.success) {
      dispatch({ type: 'SET_PEER_DATA_SUCCESS' });
      return true;
    } else {
      dispatch({ type: 'ERROR', error: result.error });
      return false;
    }
  }, []);

  /**
   * Establish ICE connection
   */
  const connect = useCallback(async (isControlling: boolean): Promise<boolean> => {
    dispatch({ type: 'CONNECT_START' });

    const result = await GhostBridge.connect(isControlling);
    if (result.success) {
      dispatch({ type: 'ICE_CONNECTED' });
      return true;
    } else {
      dispatch({ type: 'ERROR', error: result.error });
      return false;
    }
  }, []);

  /**
   * Start WireGuard tunnel
   */
  const startTunnel = useCallback(async (): Promise<boolean> => {
    dispatch({ type: 'TUNNEL_START' });

    const result = await GhostBridge.startTunnel();
    if (result.success) {
      dispatch({ type: 'TUNNEL_UP' });
      return true;
    } else {
      dispatch({ type: 'ERROR', error: result.error });
      return false;
    }
  }, []);

  /**
   * Attempt to reconnect (quick reconnect without new session)
   */
  const reconnect = useCallback(async (): Promise<boolean> => {
    dispatch({ type: 'RECONNECT_START' });

    const result = await GhostBridge.reconnect();
    if (result.success) {
      dispatch({ type: 'RECONNECT_SUCCESS' });
      return true;
    } else {
      dispatch({ type: 'RECONNECT_FAILED', error: result.error });
      return false;
    }
  }, []);

  /**
   * Close the client and reset state
   */
  const close = useCallback(async () => {
    if (clientRef.current) {
      await GhostBridge.close();
      clientRef.current = false;
    }
    dispatch({ type: 'RESET' });
  }, []);

  /**
   * Reset state without closing (for UI resets)
   */
  const reset = useCallback(() => {
    dispatch({ type: 'RESET' });
  }, []);

  // ===========================================================================
  // HTTP Methods
  // ===========================================================================

  /**
   * HTTP GET through the tunnel
   */
  const httpGet = useCallback(async (url: string): Promise<HTTPResultJSON> => {
    const result = await GhostBridge.httpGet(url);
    if (result.success) {
      return result.data;
    }
    return { success: false, error: result.error };
  }, []);

  /**
   * HTTP POST through the tunnel
   */
  const httpPost = useCallback(
    async (url: string, contentType: string, body: string): Promise<HTTPResultJSON> => {
      const result = await GhostBridge.httpPost(url, contentType, body);
      if (result.success) {
        return result.data;
      }
      return { success: false, error: result.error };
    },
    []
  );

  // ===========================================================================
  // Utility Methods
  // ===========================================================================

  /**
   * Get current signaling data (synchronous)
   */
  const getSignalingData = useCallback((): SignalingDataJSON | null => {
    const result = GhostBridge.getSignalingData();
    return result.success ? result.data : null;
  }, []);

  /**
   * Get tunnel statistics (synchronous)
   */
  const getTunnelStats = useCallback((): TunnelStatsJSON => {
    return GhostBridge.getTunnelStats();
  }, []);

  /**
   * Update connection state from native (synchronous)
   */
  const updateConnectionState = useCallback(() => {
    const result = GhostBridge.getConnectionState();
    if (result.success) {
      dispatch({ type: 'UPDATE_CONNECTION_STATE', state: result.data });
    }
  }, []);

  // ===========================================================================
  // Cleanup
  // ===========================================================================

  useEffect(() => {
    return () => {
      if (clientRef.current) {
        GhostBridge.close().catch(console.error);
      }
    };
  }, []);

  // ===========================================================================
  // Return Value
  // ===========================================================================

  return {
    // State
    state,
    phase: state.phase,
    isConnected: connected,
    isConnecting: connecting,
    canReconnect: reconnectable,

    // Actions
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

    // HTTP
    httpGet,
    httpPost,

    // Utilities
    getSignalingData,
    getTunnelStats,
    updateConnectionState,
  };
}

export default useGhostConnection;
