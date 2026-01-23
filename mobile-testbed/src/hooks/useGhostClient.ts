import { useState, useEffect, useCallback, useRef } from 'react';
import { NativeModules, Platform, NativeEventEmitter } from 'react-native';
import { useNetworkState, NetworkChangeEvent, NetworkState } from './useNetworkState';

// Types matching the Go mobile API
export interface Candidate {
  type: string;
  address: string;
  port: number;
  protocol: string;
  priority: number;
  foundation: string;
}

export interface SignalingData {
  ufrag: string;
  pwd: string;
  publicKey: string;
  candidates: Candidate[];
}

export interface ConnectionState {
  iceState: string;
  tunnelState: string;
  isConnected: boolean;
  isTunnelActive: boolean;
  localIP: string;
  peerIP: string;
}

export interface HTTPResult {
  success: boolean;
  statusCode?: number;
  body?: string;
  headers?: Record<string, string>;
  error?: string;
  latencyMs: number;
}

export interface TunnelStats {
  bytesSent: number;
  bytesReceived: number;
  packetsSent: number;
  packetsReceived: number;
  lastHandshake: number;
  isActive: boolean;
}

export type ConnectionStatus = 'disconnected' | 'gathering' | 'connecting' | 'connected' | 'error';

// Re-export network types for convenience
export type { NetworkChangeEvent, NetworkState } from './useNetworkState';

// Network-triggered reconnection result
export interface ReconnectionResult {
  success: boolean;
  requiresNewSession: boolean;
  error?: string;
}

// Event types from Go mobile API
export interface GhostEvent {
  type: 'candidate' | 'state_change' | 'error' | 'connected' | 'disconnected' | 'tunnel_up' | 'tunnel_down';
  data?: string;
  message?: string;
}

// Native module interface (to be implemented in native code)
interface GhostNativeModule {
  newClient(stunServers: string): Promise<void>;
  close(): Promise<void>;
  generateWireGuardKey(): Promise<string>;
  getPublicKey(): string;
  startGathering(): Promise<string>;
  cancelGathering(): Promise<string>;
  getLocalCredentials(): string;
  getLocalCandidatesJSON(): string;
  setRemoteCredentials(json: string): Promise<string>;
  addRemoteCandidate(json: string): Promise<string>;
  connect(isControlling: boolean): Promise<string>;
  getConnectionState(): string;
  setPeerPublicKey(base64Key: string): Promise<string>;
  setLocalIP(cidr: string): Promise<string>;
  startTunnel(): Promise<string>;
  httpGet(url: string): Promise<string>;
  httpPost(url: string, contentType: string, body: string): Promise<string>;
  getTunnelStats(): string;
  getSignalingData(): string;
  setSignalingData(json: string): Promise<string>;
}

// Get native module - will be available after prebuild and proper setup
const { GhostModule: NativeGhostModule } = NativeModules;

// Check if native module is available
const isNativeAvailable = (): boolean => {
  return NativeGhostModule != null;
};

// Wrapper that provides fallbacks when native module isn't available
const GhostModule: GhostNativeModule = {
  newClient: async (stunServers: string) => {
    if (!isNativeAvailable()) throw new Error('Native module not available. Run "npx expo prebuild" and rebuild the app.');
    return NativeGhostModule.newClient(stunServers);
  },
  close: async () => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.close();
  },
  generateWireGuardKey: async () => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.generateWireGuardKey();
  },
  getPublicKey: () => {
    if (!isNativeAvailable()) return '';
    return NativeGhostModule.getPublicKey();
  },
  startGathering: async () => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.startGathering();
  },
  cancelGathering: async () => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.cancelGathering();
  },
  getLocalCredentials: () => {
    if (!isNativeAvailable()) return '{}';
    return NativeGhostModule.getLocalCredentials();
  },
  getLocalCandidatesJSON: () => {
    if (!isNativeAvailable()) return '[]';
    return NativeGhostModule.getLocalCandidatesJSON();
  },
  setRemoteCredentials: async (json: string) => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.setRemoteCredentials(json);
  },
  addRemoteCandidate: async (json: string) => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.addRemoteCandidate(json);
  },
  connect: async (isControlling: boolean) => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.connect(isControlling);
  },
  getConnectionState: () => {
    if (!isNativeAvailable()) return '{"iceState":"new","tunnelState":"inactive","isConnected":false,"isTunnelActive":false}';
    return NativeGhostModule.getConnectionState();
  },
  setPeerPublicKey: async (base64Key: string) => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.setPeerPublicKey(base64Key);
  },
  setLocalIP: async (cidr: string) => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.setLocalIP(cidr);
  },
  startTunnel: async () => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.startTunnel();
  },
  httpGet: async (url: string) => {
    if (!isNativeAvailable()) return '{"success":false,"error":"Native module not available"}';
    return NativeGhostModule.httpGet(url);
  },
  httpPost: async (url: string, contentType: string, body: string) => {
    if (!isNativeAvailable()) return '{"success":false,"error":"Native module not available"}';
    return NativeGhostModule.httpPost(url, contentType, body);
  },
  getTunnelStats: () => {
    if (!isNativeAvailable()) return '{"isActive":false,"bytesSent":0,"bytesReceived":0}';
    return NativeGhostModule.getTunnelStats();
  },
  getSignalingData: () => {
    if (!isNativeAvailable()) return '';
    return NativeGhostModule.getSignalingData();
  },
  setSignalingData: async (json: string) => {
    if (!isNativeAvailable()) throw new Error('Native module not available');
    return NativeGhostModule.setSignalingData(json);
  },
};

// Export the availability check for UI components
export const isGhostNativeModuleAvailable = isNativeAvailable;

export function useGhostClient() {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected');
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [connectionState, setConnectionState] = useState<ConnectionState | null>(null);
  const [publicKey, setPublicKey] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [isInitialized, setIsInitialized] = useState(false);

  // Network state monitoring
  const [networkTriggeredReconnect, setNetworkTriggeredReconnect] = useState(false);
  const [lastNetworkEvent, setLastNetworkEvent] = useState<NetworkChangeEvent | null>(null);

  const clientRef = useRef<boolean>(false);
  const wasConnectedRef = useRef<boolean>(false);
  const reconnectAttemptsRef = useRef<number>(0);
  const maxReconnectAttempts = 3;

  // Callback refs for network-triggered reconnection
  const onNetworkReconnectRef = useRef<((result: ReconnectionResult) => void) | null>(null);
  const onNetworkDisconnectRef = useRef<(() => void) | null>(null);

  // Subscribe to native events
  useEffect(() => {
    if (!isNativeAvailable()) return;

    const eventEmitter = new NativeEventEmitter(NativeGhostModule);
    const subscription = eventEmitter.addListener('GhostEvent', (eventJSON: string) => {
      try {
        const event: GhostEvent = JSON.parse(eventJSON);
        const timestamp = new Date().toISOString().substring(11, 23); // HH:mm:ss.SSS
        console.log(`[${timestamp}] [GhostEvent]`, event.type, event.message || event.data?.substring(0, 50));

        switch (event.type) {
          case 'state_change':
            if (event.data) {
              const newState: ConnectionState = JSON.parse(event.data);
              setConnectionState(newState);

              // Update status based on state
              if (newState.isTunnelActive) {
                setStatus('connected');
              } else if (newState.isConnected) {
                setStatus('connecting');
              } else if (newState.iceState === 'failed' || newState.iceState === 'disconnected') {
                setStatus('disconnected');
              }
            }
            break;

          case 'error':
            setError(event.message || 'Unknown error');
            setStatus('error');
            break;

          case 'disconnected':
            setStatus('disconnected');
            setConnectionState((prev) =>
              prev ? { ...prev, isConnected: false, isTunnelActive: false, iceState: 'disconnected' } : null
            );
            break;

          case 'connected':
            // ICE connection established, waiting for tunnel
            setStatus('connecting');
            break;

          case 'tunnel_up':
            setStatus('connected');
            setConnectionState((prev) =>
              prev ? { ...prev, isTunnelActive: true, tunnelState: 'active' } : null
            );
            break;

          case 'tunnel_down':
            setStatus('disconnected');
            setConnectionState((prev) =>
              prev ? { ...prev, isTunnelActive: false, tunnelState: 'inactive' } : null
            );
            break;

          case 'candidate':
            // New candidate gathered - update candidates list
            if (event.data) {
              const newCandidate: Candidate = JSON.parse(event.data);
              setCandidates((prev) => [...prev, newCandidate]);
            }
            break;
        }
      } catch (err) {
        const timestamp = new Date().toISOString().substring(11, 23);
        console.error(`[${timestamp}] [GhostEvent] Failed to parse event:`, err);
      }
    });

    return () => {
      subscription.remove();
    };
  }, []);

  // Track connection state for network-triggered reconnection
  useEffect(() => {
    if (status === 'connected') {
      wasConnectedRef.current = true;
      reconnectAttemptsRef.current = 0;
    } else if (status === 'disconnected' || status === 'error') {
      // Don't reset wasConnectedRef here - we need it to know if reconnection makes sense
    }
  }, [status]);

  // Handle network changes and attempt reconnection
  const handleNetworkChange = useCallback(
    async (event: NetworkChangeEvent, networkState: NetworkState) => {
      setLastNetworkEvent(event);

      const timestamp = new Date().toISOString().substring(11, 23);

      // Case 1: Network switch (WiFi <-> Cellular) while connected or recently connected
      if (event.isNetworkSwitch && wasConnectedRef.current) {
        console.log(`[${timestamp}] [Ghost] Network switch detected, attempting reconnection...`);
        setNetworkTriggeredReconnect(true);

        // Wait a moment for the new network to stabilize
        await new Promise((resolve) => setTimeout(resolve, 1000));

        // Attempt quick reconnect
        await attemptNetworkReconnect('network_switch');
      }

      // Case 2: Network reconnected after being disconnected
      if (event.isReconnect && wasConnectedRef.current && status === 'disconnected') {
        console.log(`[${timestamp}] [Ghost] Network reconnected, attempting tunnel reconnection...`);
        setNetworkTriggeredReconnect(true);

        // Wait a moment for the network to stabilize
        await new Promise((resolve) => setTimeout(resolve, 1500));

        // Attempt quick reconnect
        await attemptNetworkReconnect('network_reconnect');
      }

      // Case 3: Network disconnected
      if (event.isDisconnect && status === 'connected') {
        console.log(`[${timestamp}] [Ghost] Network disconnected`);
        if (onNetworkDisconnectRef.current) {
          onNetworkDisconnectRef.current();
        }
      }
    },
    [status]
  );

  // Attempt reconnection after network change
  const attemptNetworkReconnect = useCallback(
    async (reason: 'network_switch' | 'network_reconnect'): Promise<ReconnectionResult> => {
      const timestamp = new Date().toISOString().substring(11, 23);

      if (reconnectAttemptsRef.current >= maxReconnectAttempts) {
        console.log(`[${timestamp}] [Ghost] Max reconnection attempts reached`);
        setNetworkTriggeredReconnect(false);
        const result: ReconnectionResult = {
          success: false,
          requiresNewSession: true,
          error: 'Max reconnection attempts reached',
        };
        if (onNetworkReconnectRef.current) {
          onNetworkReconnectRef.current(result);
        }
        return result;
      }

      reconnectAttemptsRef.current++;
      console.log(
        `[${timestamp}] [Ghost] Reconnection attempt ${reconnectAttemptsRef.current}/${maxReconnectAttempts} (${reason})`
      );

      try {
        // Try to reconnect using existing ICE agent
        setStatus('connecting');
        const connectResult = await GhostModule.connect(false);

        if (connectResult && connectResult.includes('error')) {
          const parsed = JSON.parse(connectResult);

          // Check if we need a new session (ICE agent in terminal state)
          if (parsed.error.includes('not usable') || parsed.error.includes('failed')) {
            console.log(`[${timestamp}] [Ghost] ICE agent not usable, new session required`);
            setNetworkTriggeredReconnect(false);
            setStatus('disconnected');

            const result: ReconnectionResult = {
              success: false,
              requiresNewSession: true,
              error: parsed.error,
            };
            if (onNetworkReconnectRef.current) {
              onNetworkReconnectRef.current(result);
            }
            return result;
          }

          // Other error - retry
          console.log(`[${timestamp}] [Ghost] Reconnect failed: ${parsed.error}`);
          setError(parsed.error);
          setStatus('error');

          const result: ReconnectionResult = {
            success: false,
            requiresNewSession: false,
            error: parsed.error,
          };
          setNetworkTriggeredReconnect(false);
          if (onNetworkReconnectRef.current) {
            onNetworkReconnectRef.current(result);
          }
          return result;
        }

        // ICE reconnected, now restart tunnel
        console.log(`[${timestamp}] [Ghost] ICE reconnected, starting tunnel...`);
        const tunnelResult = await GhostModule.startTunnel();

        if (tunnelResult && tunnelResult.includes('error')) {
          const parsed = JSON.parse(tunnelResult);
          console.log(`[${timestamp}] [Ghost] Tunnel restart failed: ${parsed.error}`);
          setError(parsed.error);
          setStatus('error');
          setNetworkTriggeredReconnect(false);

          const result: ReconnectionResult = {
            success: false,
            requiresNewSession: false,
            error: parsed.error,
          };
          if (onNetworkReconnectRef.current) {
            onNetworkReconnectRef.current(result);
          }
          return result;
        }

        // Success!
        console.log(`[${timestamp}] [Ghost] Tunnel reconnected successfully!`);
        setStatus('connected');
        setError(null);
        setNetworkTriggeredReconnect(false);
        reconnectAttemptsRef.current = 0;

        const result: ReconnectionResult = {
          success: true,
          requiresNewSession: false,
        };
        if (onNetworkReconnectRef.current) {
          onNetworkReconnectRef.current(result);
        }
        return result;
      } catch (err) {
        const errorMessage = err instanceof Error ? err.message : 'Reconnection failed';
        console.log(`[${timestamp}] [Ghost] Reconnection error: ${errorMessage}`);
        setError(errorMessage);
        setStatus('error');
        setNetworkTriggeredReconnect(false);

        const result: ReconnectionResult = {
          success: false,
          requiresNewSession: false,
          error: errorMessage,
        };
        if (onNetworkReconnectRef.current) {
          onNetworkReconnectRef.current(result);
        }
        return result;
      }
    },
    []
  );

  // Use network state hook with our handler
  const {
    networkState,
    isOnline,
    isWifi,
    isCellular,
    lastChangeEvent: networkChangeEvent,
  } = useNetworkState(handleNetworkChange);

  // Initialize client
  const initialize = useCallback(async (stunServers: string = '') => {
    try {
      // Reset all state before initializing
      setError(null);
      setStatus('disconnected');
      setPublicKey('');
      setCandidates([]);
      setConnectionState(null);

      await GhostModule.newClient(stunServers);
      clientRef.current = true;
      setIsInitialized(true);
      updateConnectionState();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to initialize client');
    }
  }, [updateConnectionState]);

  // Close client and reset all state
  const close = useCallback(async () => {
    if (!clientRef.current) return;
    try {
      await GhostModule.close();
    } catch (err) {
      console.error('Error closing client:', err);
    }
    // Always reset state, even if close() throws
    clientRef.current = false;
    setIsInitialized(false);
    setStatus('disconnected');
    setPublicKey('');
    setCandidates([]);
    setConnectionState(null);
    setError(null);
  }, []);

  // Update connection state
  const updateConnectionState = useCallback(() => {
    try {
      const stateJson = GhostModule.getConnectionState();
      const state: ConnectionState = JSON.parse(stateJson);
      setConnectionState(state);

      // Update status based on state
      if (state.isTunnelActive) {
        setStatus('connected');
      } else if (state.isConnected) {
        setStatus('connecting');
      }
    } catch (err) {
      console.error('Failed to get connection state:', err);
    }
  }, []);

  // Generate WireGuard keys
  const generateKeys = useCallback(async () => {
    try {
      setError(null);
      const result = await GhostModule.generateWireGuardKey();
      const parsed = JSON.parse(result);
      if (parsed.error) {
        setError(parsed.error);
        return null;
      }
      setPublicKey(parsed.publicKey);
      return parsed;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to generate keys');
      return null;
    }
  }, []);

  // Start gathering candidates
  const startGathering = useCallback(async () => {
    try {
      setError(null);
      setStatus('gathering');
      setCandidates([]);
      const result = await GhostModule.startGathering();
      if (result && result.includes('error')) {
        const parsed = JSON.parse(result);
        setError(parsed.error);
        setStatus('error');
        return false;
      }
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start gathering');
      setStatus('error');
      return false;
    }
  }, []);

  // Cancel gathering - call when user cancels or navigates away
  const cancelGathering = useCallback(async () => {
    try {
      const result = await GhostModule.cancelGathering();
      if (result && result.includes('error')) {
        const parsed = JSON.parse(result);
        console.error('Failed to cancel gathering:', parsed.error);
        return false;
      }
      setStatus('disconnected');
      setCandidates([]);
      return true;
    } catch (err) {
      console.error('Failed to cancel gathering:', err);
      return false;
    }
  }, []);

  // Poll for candidates (in a real implementation, this would use native events)
  const pollCandidates = useCallback(() => {
    try {
      const candsJson = GhostModule.getLocalCandidatesJSON();
      const cands: Candidate[] = JSON.parse(candsJson);
      setCandidates(cands);
      return cands;
    } catch (err) {
      console.error('Failed to get candidates:', err);
      return [];
    }
  }, []);

  // Get signaling data
  const getSignalingData = useCallback((): SignalingData | null => {
    try {
      const dataJson = GhostModule.getSignalingData();
      if (dataJson.includes('error')) {
        const parsed = JSON.parse(dataJson);
        setError(parsed.error);
        return null;
      }
      return JSON.parse(dataJson);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to get signaling data');
      return null;
    }
  }, []);

  // Set signaling data from peer
  const setSignalingData = useCallback(async (data: SignalingData) => {
    try {
      setError(null);
      const result = await GhostModule.setSignalingData(JSON.stringify(data));
      if (result && result.includes('error')) {
        const parsed = JSON.parse(result);
        setError(parsed.error);
        return false;
      }
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to set signaling data');
      return false;
    }
  }, []);

  // Connect to peer
  const connect = useCallback(async (isControlling: boolean) => {
    try {
      setError(null);
      setStatus('connecting');
      const result = await GhostModule.connect(isControlling);
      if (result && result.includes('error')) {
        const parsed = JSON.parse(result);
        setError(parsed.error);
        setStatus('error');
        return false;
      }
      updateConnectionState();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to connect');
      setStatus('error');
      return false;
    }
  }, [updateConnectionState]);

  // Start tunnel
  const startTunnel = useCallback(async () => {
    try {
      setError(null);
      const result = await GhostModule.startTunnel();
      if (result && result.includes('error')) {
        const parsed = JSON.parse(result);
        setError(parsed.error);
        setStatus('error');
        return false;
      }
      setStatus('connected');
      updateConnectionState();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start tunnel');
      setStatus('error');
      return false;
    }
  }, [updateConnectionState]);

  // HTTP GET through tunnel
  const httpGet = useCallback(async (url: string): Promise<HTTPResult> => {
    try {
      const result = await GhostModule.httpGet(url);
      return JSON.parse(result);
    } catch (err) {
      return {
        success: false,
        error: err instanceof Error ? err.message : 'HTTP request failed',
        latencyMs: 0,
      };
    }
  }, []);

  // HTTP POST through tunnel
  const httpPost = useCallback(async (url: string, contentType: string, body: string): Promise<HTTPResult> => {
    try {
      const result = await GhostModule.httpPost(url, contentType, body);
      return JSON.parse(result);
    } catch (err) {
      return {
        success: false,
        error: err instanceof Error ? err.message : 'HTTP request failed',
        latencyMs: 0,
      };
    }
  }, []);

  // Get tunnel stats
  const getTunnelStats = useCallback((): TunnelStats => {
    try {
      const statsJson = GhostModule.getTunnelStats();
      return JSON.parse(statsJson);
    } catch (err) {
      return {
        bytesSent: 0,
        bytesReceived: 0,
        packetsSent: 0,
        packetsReceived: 0,
        lastHandshake: 0,
        isActive: false,
      };
    }
  }, []);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (clientRef.current) {
        GhostModule.close().catch(console.error);
      }
    };
  }, []);

  // Set callback for network-triggered reconnection results
  const setOnNetworkReconnect = useCallback((callback: (result: ReconnectionResult) => void) => {
    onNetworkReconnectRef.current = callback;
  }, []);

  // Set callback for network disconnect
  const setOnNetworkDisconnect = useCallback((callback: () => void) => {
    onNetworkDisconnectRef.current = callback;
  }, []);

  // Reset reconnection tracking (call when starting a new session)
  const resetReconnectionState = useCallback(() => {
    wasConnectedRef.current = false;
    reconnectAttemptsRef.current = 0;
    setNetworkTriggeredReconnect(false);
    setLastNetworkEvent(null);
  }, []);

  return {
    // Connection State
    status,
    candidates,
    connectionState,
    publicKey,
    error,
    isInitialized,

    // Network State
    networkState,
    isOnline,
    isWifi,
    isCellular,
    networkTriggeredReconnect,
    lastNetworkEvent,

    // Connection Actions
    initialize,
    close,
    generateKeys,
    startGathering,
    cancelGathering,
    pollCandidates,
    getSignalingData,
    setSignalingData,
    connect,
    startTunnel,
    httpGet,
    httpPost,
    getTunnelStats,
    updateConnectionState,

    // Network Callbacks
    setOnNetworkReconnect,
    setOnNetworkDisconnect,
    resetReconnectionState,
  };
}
