/**
 * Ghost Event Handling
 *
 * Parses native events and maps them to reducer actions.
 * Separates event parsing logic from state management.
 */

import type { GhostEvent, CandidateJSON, ConnectionStateJSON } from './types';
import type { GhostAction } from './reducer';

// =============================================================================
// Event Parsing
// =============================================================================

/**
 * Parse a JSON event string from the native module.
 * Returns null if parsing fails or event structure is invalid.
 */
export function parseGhostEvent(json: string): GhostEvent | null {
  try {
    const parsed = JSON.parse(json);

    // Validate required type field
    if (!parsed.type || typeof parsed.type !== 'string') {
      console.warn('[GhostEvent] Invalid event: missing type field');
      return null;
    }

    // Validate based on event type
    switch (parsed.type) {
      case 'state_change':
      case 'candidate':
        // These events should have a data field
        if (typeof parsed.data !== 'string') {
          console.warn(`[GhostEvent] Invalid ${parsed.type} event: missing data field`);
          return null;
        }
        return parsed as GhostEvent;

      case 'connected':
      case 'tunnel_up':
      case 'tunnel_down':
        // These events have no additional required fields
        return parsed as GhostEvent;

      case 'disconnected':
        // Optional reason field
        return parsed as GhostEvent;

      case 'error':
        // Should have a message field
        if (typeof parsed.message !== 'string') {
          console.warn('[GhostEvent] Invalid error event: missing message field');
          return null;
        }
        return parsed as GhostEvent;

      default:
        console.warn(`[GhostEvent] Unknown event type: ${parsed.type}`);
        return null;
    }
  } catch (e) {
    console.error('[GhostEvent] Failed to parse event JSON:', e);
    return null;
  }
}

/**
 * Parse candidate data from event
 */
export function parseCandidateData(data: string): CandidateJSON | null {
  try {
    return JSON.parse(data) as CandidateJSON;
  } catch {
    console.error('[GhostEvent] Failed to parse candidate data');
    return null;
  }
}

/**
 * Parse connection state data from event
 */
export function parseConnectionStateData(data: string): ConnectionStateJSON | null {
  try {
    return JSON.parse(data) as ConnectionStateJSON;
  } catch {
    console.error('[GhostEvent] Failed to parse connection state data');
    return null;
  }
}

// =============================================================================
// Event to Action Mapping
// =============================================================================

/**
 * Convert a Ghost event to a reducer action.
 * Returns null if the event doesn't map to an action.
 */
export function eventToAction(event: GhostEvent): GhostAction | null {
  switch (event.type) {
    case 'connected':
      return { type: 'ICE_CONNECTED' };

    case 'tunnel_up':
      return { type: 'TUNNEL_UP' };

    case 'tunnel_down':
      return { type: 'TUNNEL_DOWN' };

    case 'disconnected':
      return { type: 'DISCONNECTED', reason: event.reason };

    case 'error':
      return { type: 'ERROR', error: event.message };

    case 'candidate': {
      const candidate = parseCandidateData(event.data);
      if (candidate) {
        return { type: 'ADD_CANDIDATE', candidate };
      }
      return null;
    }

    case 'state_change': {
      const state = parseConnectionStateData(event.data);
      if (state) {
        return { type: 'UPDATE_CONNECTION_STATE', state };
      }
      return null;
    }

    default:
      return null;
  }
}

/**
 * Convert a Ghost event to multiple actions if needed.
 * Some events may trigger additional state updates.
 */
export function eventToActions(event: GhostEvent): GhostAction[] {
  const actions: GhostAction[] = [];

  // Get primary action
  const primaryAction = eventToAction(event);
  if (primaryAction) {
    actions.push(primaryAction);
  }

  // Handle state_change events that may also trigger phase changes
  if (event.type === 'state_change') {
    const state = parseConnectionStateData(event.data);
    if (state) {
      // Check if tunnel became active
      if (state.isTunnelActive) {
        actions.push({ type: 'TUNNEL_UP' });
      }
      // Check if connection was lost
      else if (state.iceState === 'failed' || state.iceState === 'closed') {
        actions.push({ type: 'DISCONNECTED', reason: `ICE state: ${state.iceState}` });
      }
      // Check if disconnected
      else if (state.iceState === 'disconnected' && !state.isConnected) {
        actions.push({ type: 'DISCONNECTED', reason: 'ICE disconnected' });
      }
    }
  }

  return actions;
}

// =============================================================================
// Event Logging
// =============================================================================

/**
 * Format an event for logging
 */
export function formatEventForLog(event: GhostEvent): string {
  const timestamp = new Date().toISOString().substring(11, 23); // HH:mm:ss.SSS

  switch (event.type) {
    case 'state_change':
      try {
        const state = JSON.parse(event.data);
        return `[${timestamp}] [GhostEvent] state_change: ICE=${state.iceState}, Tunnel=${state.tunnelState}`;
      } catch {
        return `[${timestamp}] [GhostEvent] state_change: ${event.data.substring(0, 50)}...`;
      }

    case 'candidate':
      try {
        const cand = JSON.parse(event.data);
        return `[${timestamp}] [GhostEvent] candidate: ${cand.type} ${cand.address}:${cand.port}`;
      } catch {
        return `[${timestamp}] [GhostEvent] candidate: ${event.data.substring(0, 50)}...`;
      }

    case 'error':
      return `[${timestamp}] [GhostEvent] error: ${event.message}`;

    case 'disconnected':
      return `[${timestamp}] [GhostEvent] disconnected${event.reason ? `: ${event.reason}` : ''}`;

    default:
      return `[${timestamp}] [GhostEvent] ${event.type}`;
  }
}
