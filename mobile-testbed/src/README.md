# Source Directory

This directory contains the React Native TypeScript source code for the Ghost mobile testbed app.

## Structure

```
src/
├── App.tsx              # Root component with navigation setup
├── ghost/               # Ghost client library integration
├── hooks/               # Custom React hooks
├── screens/             # Screen components (pages)
└── components/          # Shared/reusable components
```

## Key Files

- **App.tsx** - Sets up React Navigation with a stack navigator containing Home, Connection, and Test screens.

## Architecture

The app follows a clean separation of concerns:

1. **ghost/** - Low-level native module bridge and state management
2. **hooks/** - React hooks that wrap ghost functionality for components
3. **screens/** - Full-page components for each navigation destination
4. **components/** - Reusable UI components shared across screens
