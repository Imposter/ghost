# Native Source Code

Platform-specific native module implementations that bridge React Native to the ghost-go library.

## Structure

```
native-src/
├── android/          # Android native module (Kotlin)
└── ios/              # iOS native module (Swift)
```

## Android (`android/`)

Contains the Kotlin native module that:
- Wraps the `ghost.aar` library (built via gomobile)
- Exposes Go functions to React Native via NativeModule
- Handles event callbacks from Go and emits them to JavaScript
- Manages the GhostClient lifecycle

Key files:
- `GhostModule.kt` - React Native module implementation
- `GhostPackage.kt` - Module registration

## iOS (`ios/`)

Contains the Swift native module that:
- Wraps the `Mobile.xcframework` (built via gomobile)
- Exposes Go functions to React Native via RCTBridgeModule
- Handles event callbacks from Go and emits them to JavaScript
- Manages the GhostClient lifecycle

Key files:
- `GhostModule.swift` - React Native module implementation
- `GhostModule.m` - Objective-C bridge header

## Build Integration

These native modules are automatically linked during `expo prebuild`. The Expo config plugin in `plugins/withGhostModule.js` handles:
- Copying native source files to the correct locations
- Configuring build settings (linking ghost.aar, framework search paths)
- Setting up required permissions

## Updating Native Code

After modifying native source:

1. Run `npx expo prebuild --clean` to regenerate native projects
2. Rebuild the app: `npx expo run:android` or `npx expo run:ios`

The config plugin will copy updated source files during prebuild.
