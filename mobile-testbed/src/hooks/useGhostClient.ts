import { useState, useEffect, useCallback, useRef } from 'react';

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

// Native module interface (to be implemented in native code)
interface GhostNativeModule {
  newClient(stunServers: string): Promise<void>;
  close(): Promise<void>;
  generateWireGuardKey(): Promise<string>;
  getPublicKey(): string;
  startGathering(): Promise<string>;
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

// Placeholder for native module - will be replaced by actual native bridge
const GhostModule: GhostNativeModule = {
  newClient: async () => { throw new Error('Native module not available'); },
  close: async () => { throw new Error('Native module not available'); },
  generateWireGuardKey: async () => { throw new Error('Native module not available'); },
  getPublicKey: () => '',
  startGathering: async () => '',
  getLocalCredentials: () => '{}',
  getLocalCandidatesJSON: () => '[]',
  setRemoteCredentials: async () => '',
  addRemoteCandidate: async () => '',
  connect: async () => '',
  getConnectionState: () => '{"iceState":"new","tunnelState":"inactive","isConnected":false,"isTunnelActive":false}',
  setPeerPublicKey: async () => '',
  setLocalIP: async () => '',
  startTunnel: async () => '',
  httpGet: async () => '{"success":false,"error":"Native module not available"}',
  httpPost: async () => '{"success":false,"error":"Native module not available"}',
  getTunnelStats: () => '{"isActive":false,"bytesSent":0,"bytesReceived":0}',
  getSignalingData: () => '',
  setSignalingData: async () => '',
};

export function useGhostClient() {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected');
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [connectionState, setConnectionState] = useState<ConnectionState | null>(null);
  const [publicKey, setPublicKey] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [isInitialized, setIsInitialized] = useState(false);

  const clientRef = useRef<boolean>(false);

  // Initialize client
  const initialize = useCallback(async (stunServers: string = '') => {
    try {
      setError(null);
      await GhostModule.newClient(stunServers);
      clientRef.current = true;
      setIsInitialized(true);
      updateConnectionState();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to initialize client');
    }
  }, []);

  // Close client
  const close = useCallback(async () => {
    if (!clientRef.current) return;
    try {
      await GhostModule.close();
      clientRef.current = false;
      setIsInitialized(false);
      setStatus('disconnected');
      setPublicKey('');
      setCandidates([]);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to close client');
    }
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

  return {
    // State
    status,
    candidates,
    connectionState,
    publicKey,
    error,
    isInitialized,

    // Actions
    initialize,
    close,
    generateKeys,
    startGathering,
    pollCandidates,
    getSignalingData,
    setSignalingData,
    connect,
    startTunnel,
    httpGet,
    httpPost,
    getTunnelStats,
    updateConnectionState,
  };
}
