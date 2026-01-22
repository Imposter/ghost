# Ghost-GO Android Test App

Minimal Android app for validating gomobile bindings.

## Prerequisites

1. Android Studio or Android SDK
2. gomobile installed: `go install golang.org/x/mobile/cmd/gomobile@latest`
3. gomobile init: `gomobile init`

## Building the AAR

From the ghost-go directory:

```bash
gomobile bind -target=android -o=./tests/android/app/libs/ghost.aar ./mobile
```

## Running Tests

```bash
# Start an Android emulator or connect a device

# Run instrumented tests
cd tests/android
./gradlew connectedAndroidTest
```

## Test Coverage

The instrumented tests validate:
- Client creation
- Key generation
- Connection state management
- Input validation
- Error handling
- Resource cleanup

## Project Structure

```
tests/android/
├── app/
│   ├── build.gradle.kts
│   ├── libs/                    # Place ghost.aar here
│   └── src/
│       ├── main/
│       │   ├── AndroidManifest.xml
│       │   ├── java/com/ghost/testbed/
│       │   │   └── MainActivity.kt
│       │   └── res/
│       └── androidTest/
│           └── java/com/ghost/testbed/
│               └── GhostClientTest.kt
├── build.gradle.kts
└── settings.gradle.kts
```
