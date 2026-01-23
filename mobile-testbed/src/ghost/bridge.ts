/**
 * Ghost Native Bridge
 *
 * Type-safe wrapper around the native Ghost module.
 * All operations return Result<T> for consistent error handling.
 */

import { NativeModules } from 'react-native';
import type {
  Result,
  ConnectionStateJSON,
  SignalingDataJSON,
  WireGuardKeyJSON,
  HTTPResultJSON,
  TunnelStatsJSON,
} from './types';

const { GhostModule: NativeGhostModule } = NativeModules;

// =============================================================================
// Availability Check
// =============================================================================

/**
 * Check if the native Ghost module is available.
 * Returns false when running in Expo Go or before prebuild.
 */
export function isNativeAvailable(): boolean {
  return NativeGhostModule != null;
}

// =============================================================================
// JSON Parsing Utilities
// =============================================================================

/**
 * Parse JSON string with error handling.
 * Checks for error field in parsed object.
 */
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

/**
 * Check if a result string contains an error.
 */
function checkResultError(result: string | undefined): string | null {
  if (!result) return null;
  try {
    if (result.includes('error')) {
      const parsed = JSON.parse(result);
      if (parsed.error) return parsed.error;
    }
  } catch {
    // Not JSON, not an error
  }
  return null;
}

// =============================================================================
// Ghost Native Bridge
// =============================================================================

/**
 * Type-safe bridge to the native Ghost module.
 * All async operations return Result<T> for consistent error handling.
 */
export const GhostBridge = {
  /**
   * Check if native module is available
   */
  isAvailable(): boolean {
    return isNativeAvailable();
  },

  /**
   * Create a new Ghost client
   */
  async newClient(stunServers: string): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return {
        success: false,
        error: 'Native module not available. Run "npx expo prebuild" and rebuild the app.',
      };
    }
    try {
      await NativeGhostModule.newClient(stunServers);
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Close the Ghost client and release resources
   */
  async close(): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      await NativeGhostModule.close();
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Generate a new WireGuard keypair
   */
  async generateWireGuardKey(): Promise<Result<WireGuardKeyJSON>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      const json = await NativeGhostModule.generateWireGuardKey();
      return parseJSON<WireGuardKeyJSON>(json);
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Get the current public key (synchronous)
   */
  getPublicKey(): string {
    if (!isNativeAvailable()) return '';
    return NativeGhostModule.getPublicKey() || '';
  },

  /**
   * Start ICE candidate gathering
   */
  async startGathering(): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      const result = await NativeGhostModule.startGathering();
      const error = checkResultError(result);
      if (error) return { success: false, error };
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Cancel ICE candidate gathering
   */
  async cancelGathering(): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      const result = await NativeGhostModule.cancelGathering();
      const error = checkResultError(result);
      if (error) return { success: false, error };
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Get local signaling data (synchronous)
   */
  getSignalingData(): Result<SignalingDataJSON> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    const json = NativeGhostModule.getSignalingData();
    if (!json) {
      return { success: false, error: 'No signaling data available' };
    }
    return parseJSON<SignalingDataJSON>(json);
  },

  /**
   * Set peer's signaling data
   */
  async setSignalingData(data: string): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      const result = await NativeGhostModule.setSignalingData(data);
      const error = checkResultError(result);
      if (error) return { success: false, error };
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Establish ICE connection
   */
  async connect(isControlling: boolean): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      const result = await NativeGhostModule.connect(isControlling);
      const error = checkResultError(result);
      if (error) return { success: false, error };
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Start the WireGuard tunnel
   */
  async startTunnel(): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      const result = await NativeGhostModule.startTunnel();
      const error = checkResultError(result);
      if (error) return { success: false, error };
      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Attempt to reconnect (quick reconnect without new session)
   */
  async reconnect(): Promise<Result<void>> {
    if (!isNativeAvailable()) {
      return { success: false, error: 'Native module not available' };
    }
    try {
      // First try to reconnect ICE
      const connectResult = await NativeGhostModule.connect(false);
      const connectError = checkResultError(connectResult);
      if (connectError) {
        // Check if ICE agent is not usable (need new session)
        if (connectError.includes('not usable') || connectError.includes('failed')) {
          return { success: false, error: `ICE agent not usable: ${connectError}` };
        }
        return { success: false, error: connectError };
      }

      // Then restart tunnel
      const tunnelResult = await NativeGhostModule.startTunnel();
      const tunnelError = checkResultError(tunnelResult);
      if (tunnelError) return { success: false, error: tunnelError };

      return { success: true, data: undefined };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return { success: false, error: message };
    }
  },

  /**
   * Get current connection state (synchronous)
   */
  getConnectionState(): Result<ConnectionStateJSON> {
    if (!isNativeAvailable()) {
      return {
        success: true,
        data: {
          iceState: 'new',
          tunnelState: 'inactive',
          isConnected: false,
          isTunnelActive: false,
          localIP: '',
          peerIP: '',
        },
      };
    }
    const json = NativeGhostModule.getConnectionState();
    return parseJSON<ConnectionStateJSON>(json);
  },

  /**
   * HTTP GET request through the tunnel
   */
  async httpGet(url: string): Promise<Result<HTTPResultJSON>> {
    if (!isNativeAvailable()) {
      return {
        success: true,
        data: { success: false, error: 'Native module not available' },
      };
    }
    try {
      const json = await NativeGhostModule.httpGet(url);
      return parseJSON<HTTPResultJSON>(json);
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return {
        success: true,
        data: { success: false, error: message },
      };
    }
  },

  /**
   * HTTP POST request through the tunnel
   */
  async httpPost(
    url: string,
    contentType: string,
    body: string
  ): Promise<Result<HTTPResultJSON>> {
    if (!isNativeAvailable()) {
      return {
        success: true,
        data: { success: false, error: 'Native module not available' },
      };
    }
    try {
      const json = await NativeGhostModule.httpPost(url, contentType, body);
      return parseJSON<HTTPResultJSON>(json);
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      return {
        success: true,
        data: { success: false, error: message },
      };
    }
  },

  /**
   * Get tunnel statistics (synchronous)
   */
  getTunnelStats(): TunnelStatsJSON {
    if (!isNativeAvailable()) {
      return {
        bytesSent: 0,
        bytesReceived: 0,
        packetsSent: 0,
        packetsReceived: 0,
        lastHandshake: 0,
        isActive: false,
      };
    }
    try {
      const json = NativeGhostModule.getTunnelStats();
      return JSON.parse(json);
    } catch {
      return {
        bytesSent: 0,
        bytesReceived: 0,
        packetsSent: 0,
        packetsReceived: 0,
        lastHandshake: 0,
        isActive: false,
      };
    }
  },

  /**
   * Get local ICE candidates (synchronous)
   */
  getLocalCandidates(): Result<import('./types').CandidateJSON[]> {
    if (!isNativeAvailable()) {
      return { success: true, data: [] };
    }
    try {
      const json = NativeGhostModule.getLocalCandidatesJSON();
      return parseJSON<import('./types').CandidateJSON[]>(json);
    } catch {
      return { success: true, data: [] };
    }
  },
};

export default GhostBridge;
