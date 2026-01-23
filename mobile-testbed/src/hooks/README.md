# React Hooks

Custom React hooks for the Ghost mobile testbed.

## Hooks

### useGhostClient.ts (Deprecated)

> **Note**: This hook is deprecated. Use `useGhostConnection` from `../ghost` instead.

The original hook for managing ghost client state. It has been superseded by the more robust `useGhostConnection` hook which provides better state management and type safety.

### useNetworkState.ts

Monitors device network connectivity status using React Native's NetInfo.

```typescript
import { useNetworkState } from './useNetworkState';

function MyComponent() {
  const { networkState, isOnline, isWifi, isCellular } = useNetworkState();

  if (!isOnline) {
    return <Text>No network connection</Text>;
  }

  return <Text>Connected via {isWifi ? 'WiFi' : 'Cellular'}</Text>;
}
```

**Returns:**
- `networkState` - Full NetInfo state object
- `isOnline` - Boolean indicating network availability
- `isWifi` - Boolean indicating WiFi connection
- `isCellular` - Boolean indicating cellular connection

## Migration Guide

If you're using `useGhostClient`, migrate to `useGhostConnection`:

```typescript
// Old (deprecated)
import { useGhostClient } from '../hooks/useGhostClient';

// New (recommended)
import { useGhostConnection } from '../ghost';
```

The new hook provides:
- Cleaner state management with `useReducer`
- Better TypeScript types
- Automatic state sync with native module
- Improved reconnection handling
