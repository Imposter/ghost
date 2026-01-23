# Shared Components

Reusable UI components shared across the application.

## Structure

```
components/
├── common/              # Generic reusable components
├── ConnectionScreen.tsx # Legacy: Old connection screen (use screens/ConnectionScreen)
├── HomeScreen.tsx       # Legacy: Old home screen (use screens/HomeScreen)
└── TestScreen.tsx       # Legacy: Old test screen (use screens/TestScreen)
```

## Component Categories

### Common Components (`common/`)

Generic, reusable components that can be used across multiple screens:
- Loading indicators
- Error displays
- Network status indicators

### Legacy Components

The root-level `.tsx` files are legacy components from an earlier architecture. The app now uses the screen-based structure in `screens/`. These files are kept for reference but may be removed in future cleanup.

## Screen-Specific Components

Screen-specific components live within their respective screen directories:

```
screens/
└── ConnectionScreen/
    └── components/
        ├── index.ts           # Barrel export
        ├── NetworkIndicator.tsx
        ├── StatusBanner.tsx
        ├── CandidatesList.tsx
        ├── ConnectionStateDisplay.tsx
        ├── PeerDataInput.tsx
        ├── DisconnectedPanel.tsx
        └── SignalingDataActions.tsx
```

This keeps components close to where they're used while maintaining clean screen code.
