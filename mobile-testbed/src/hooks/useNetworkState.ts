import { useState, useEffect, useCallback, useRef } from 'react';
import NetInfo, { NetInfoState, NetInfoStateType } from '@react-native-community/netinfo';

// Network connection types
export type NetworkType = 'wifi' | 'cellular' | 'ethernet' | 'none' | 'unknown';

// Network state with change detection
export interface NetworkState {
  type: NetworkType;
  isConnected: boolean;
  isInternetReachable: boolean | null;
  details: {
    ssid?: string | null;
    strength?: number | null;
    cellularGeneration?: string | null;
    carrier?: string | null;
  };
}

// Network change event for detecting transitions
export interface NetworkChangeEvent {
  previousType: NetworkType | null;
  currentType: NetworkType;
  previousConnected: boolean;
  currentConnected: boolean;
  isNetworkSwitch: boolean; // WiFi <-> Cellular switch
  isReconnect: boolean; // Was disconnected, now connected
  isDisconnect: boolean; // Was connected, now disconnected
}

// Hook return type
export interface UseNetworkStateReturn {
  networkState: NetworkState;
  isOnline: boolean;
  isWifi: boolean;
  isCellular: boolean;
  lastChangeEvent: NetworkChangeEvent | null;
  refresh: () => Promise<NetworkState>;
}

// Convert NetInfo type to our NetworkType
function mapNetInfoType(type: NetInfoStateType): NetworkType {
  switch (type) {
    case 'wifi':
      return 'wifi';
    case 'cellular':
      return 'cellular';
    case 'ethernet':
      return 'ethernet';
    case 'none':
      return 'none';
    default:
      return 'unknown';
  }
}

// Convert NetInfoState to our NetworkState
function mapNetInfoState(state: NetInfoState): NetworkState {
  const type = mapNetInfoType(state.type);

  // Extract relevant details based on connection type
  const details: NetworkState['details'] = {};

  if (state.type === 'wifi' && state.details) {
    details.ssid = state.details.ssid;
    details.strength = state.details.strength;
  } else if (state.type === 'cellular' && state.details) {
    details.cellularGeneration = state.details.cellularGeneration;
    details.carrier = state.details.carrier;
  }

  return {
    type,
    isConnected: state.isConnected ?? false,
    isInternetReachable: state.isInternetReachable,
    details,
  };
}

/**
 * Hook for monitoring network state changes.
 *
 * Detects:
 * - WiFi to cellular switches
 * - Cellular to WiFi switches
 * - Network disconnections
 * - Network reconnections
 * - Internet reachability changes
 *
 * @param onNetworkChange Optional callback when network state changes
 */
export function useNetworkState(
  onNetworkChange?: (event: NetworkChangeEvent, state: NetworkState) => void
): UseNetworkStateReturn {
  const [networkState, setNetworkState] = useState<NetworkState>({
    type: 'unknown',
    isConnected: false,
    isInternetReachable: null,
    details: {},
  });

  const [lastChangeEvent, setLastChangeEvent] = useState<NetworkChangeEvent | null>(null);

  // Track previous state for change detection
  const previousStateRef = useRef<NetworkState | null>(null);
  const onNetworkChangeRef = useRef(onNetworkChange);

  // Keep callback ref up to date
  useEffect(() => {
    onNetworkChangeRef.current = onNetworkChange;
  }, [onNetworkChange]);

  // Handle network state updates
  const handleNetworkChange = useCallback((netInfoState: NetInfoState) => {
    const newState = mapNetInfoState(netInfoState);
    const prevState = previousStateRef.current;

    // Detect what kind of change this is
    const changeEvent: NetworkChangeEvent = {
      previousType: prevState?.type ?? null,
      currentType: newState.type,
      previousConnected: prevState?.isConnected ?? false,
      currentConnected: newState.isConnected,
      isNetworkSwitch: false,
      isReconnect: false,
      isDisconnect: false,
    };

    if (prevState) {
      // Detect network type switch (WiFi <-> Cellular)
      const wasWifiOrCellular = prevState.type === 'wifi' || prevState.type === 'cellular';
      const isWifiOrCellular = newState.type === 'wifi' || newState.type === 'cellular';
      changeEvent.isNetworkSwitch = wasWifiOrCellular && isWifiOrCellular && prevState.type !== newState.type;

      // Detect disconnect (was connected, now not)
      changeEvent.isDisconnect = prevState.isConnected && !newState.isConnected;

      // Detect reconnect (was not connected, now connected)
      changeEvent.isReconnect = !prevState.isConnected && newState.isConnected;
    }

    // Update refs and state
    previousStateRef.current = newState;
    setNetworkState(newState);
    setLastChangeEvent(changeEvent);

    // Log network changes for debugging
    const timestamp = new Date().toISOString().substring(11, 23);
    if (changeEvent.isNetworkSwitch) {
      console.log(`[${timestamp}] [Network] Switch: ${changeEvent.previousType} -> ${changeEvent.currentType}`);
    } else if (changeEvent.isDisconnect) {
      console.log(`[${timestamp}] [Network] Disconnected (was ${changeEvent.previousType})`);
    } else if (changeEvent.isReconnect) {
      console.log(`[${timestamp}] [Network] Reconnected via ${changeEvent.currentType}`);
    }

    // Call user callback if provided
    if (onNetworkChangeRef.current && prevState) {
      onNetworkChangeRef.current(changeEvent, newState);
    }
  }, []);

  // Subscribe to network state changes
  useEffect(() => {
    // Get initial state
    NetInfo.fetch().then(handleNetworkChange);

    // Subscribe to changes
    const unsubscribe = NetInfo.addEventListener(handleNetworkChange);

    return () => {
      unsubscribe();
    };
  }, [handleNetworkChange]);

  // Manual refresh function
  const refresh = useCallback(async (): Promise<NetworkState> => {
    const state = await NetInfo.fetch();
    const mappedState = mapNetInfoState(state);
    setNetworkState(mappedState);
    return mappedState;
  }, []);

  return {
    networkState,
    isOnline: networkState.isConnected && networkState.isInternetReachable !== false,
    isWifi: networkState.type === 'wifi',
    isCellular: networkState.type === 'cellular',
    lastChangeEvent,
    refresh,
  };
}

export default useNetworkState;
