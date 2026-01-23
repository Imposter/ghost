# Expo Config Plugins

Custom Expo config plugins for integrating the ghost-go native module.

## Files

### withGhostModule.js

The main config plugin that configures both Android and iOS projects to use the ghost native module.

**What it does:**

#### Android
- Copies `native-src/android/` files to `android/app/src/main/java/`
- Adds `ghost.aar` to project dependencies
- Configures JNI library loading
- Sets up required permissions (INTERNET, ACCESS_NETWORK_STATE)

#### iOS
- Copies `native-src/ios/` files to the iOS project
- Configures framework search paths for `Mobile.xcframework`
- Sets up bridging header for Swift/Objective-C interop
- Adds required Info.plist entries

## Usage

The plugin is configured in `app.json`:

```json
{
  "expo": {
    "plugins": [
      "./plugins/withGhostModule"
    ]
  }
}
```

## Running Prebuild

To apply plugin changes:

```bash
# Clean prebuild (recommended after plugin changes)
npx expo prebuild --clean

# Then build the app
npx expo run:android
# or
npx expo run:ios
```

## Debugging

If the plugin isn't working correctly:

1. Check `npx expo prebuild` output for errors
2. Verify native-src files exist and are valid
3. Check the generated native projects in `android/` and `ios/`
4. Review Expo's config plugin documentation for API changes

## Development

To modify the plugin:

1. Edit `withGhostModule.js`
2. Run `npx expo prebuild --clean`
3. Test the build on both platforms
4. Commit changes to both the plugin and any native-src updates
