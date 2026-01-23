# Screens

Screen components for each navigation destination in the app.

## Screen Structure

Each screen is organized in its own directory with components and styles:

```
screens/
├── index.ts              # Barrel export for all screens
├── HomeScreen/           # Landing screen
│   └── index.tsx
├── ConnectionScreen/     # ICE/WireGuard connection setup
│   ├── index.tsx         # Main screen component
│   ├── styles.ts         # StyleSheet definitions
│   └── components/       # Screen-specific components
└── TestScreen/           # Connectivity testing
    └── index.tsx
```

## Screens

### HomeScreen

The landing screen with navigation to Connection and Test screens. Shows basic app information and getting started instructions.

### ConnectionScreen

The main connection workflow screen implementing a multi-step process:

1. **Step 1: Start Gathering** - Generate WireGuard keys and gather ICE candidates
2. **Step 2: Share Your Data** - Display local signaling data for sharing with peer
3. **Step 3: Enter Peer Data** - Accept peer's signaling data (JSON)
4. **Step 4: Connect** - Establish ICE connection and start WireGuard tunnel

**Key Features:**
- Connection persistence across navigation (stays connected when viewing Test screen)
- In-progress state cleanup on navigation away
- Network state indicator
- Reconnection handling for lost connections

### TestScreen

Tests connectivity through the established WireGuard tunnel:
- HTTP GET/POST requests to the desktop peer
- Tunnel statistics display (bytes sent/received)
- Latency measurements
- Connection status monitoring

## Navigation

Screens are connected via React Navigation's stack navigator configured in `App.tsx`:

```typescript
type RootStackParamList = {
  Home: undefined;
  Connection: undefined;
  Test: undefined;
};
```
