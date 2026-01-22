# Ghost-GO iOS Test App

Minimal iOS app for validating gomobile bindings.

## Prerequisites

1. macOS with Xcode 15+
2. gomobile installed: `go install golang.org/x/mobile/cmd/gomobile@latest`
3. gomobile init: `gomobile init`

## Building the XCFramework

From the ghost-go directory:

```bash
gomobile bind -target=ios -o=./tests/ios/GhostTestbed/Mobile.xcframework ./mobile
```

## Setup in Xcode

1. Open `tests/ios/GhostTestbed/GhostTestbed.xcodeproj` (create via Xcode)
2. Drag `Mobile.xcframework` into the project
3. Add to "Frameworks, Libraries, and Embedded Content" in target settings
4. Set "Embed" to "Embed & Sign"

## Running Tests

```bash
xcodebuild test \
  -scheme GhostTestbed \
  -destination 'platform=iOS Simulator,name=iPhone 15'
```

Or run tests from Xcode using Cmd+U.

## Test Coverage

The XCTest tests validate:
- Client creation
- Key generation
- Connection state management
- Input validation
- Error handling
- Resource cleanup

## Creating the Xcode Project

If starting fresh:

1. Open Xcode → File → New → Project
2. Select iOS → App
3. Product Name: GhostTestbed
4. Interface: SwiftUI
5. Language: Swift
6. Add XCFramework to project
7. Copy ContentView.swift and GhostTestbedApp.swift
8. Add GhostClientTests.swift to Tests target

## Project Structure

```
tests/ios/
├── GhostTestbed/
│   ├── Mobile.xcframework/      # gomobile output (add here)
│   ├── GhostTestbed/
│   │   ├── GhostTestbedApp.swift
│   │   └── ContentView.swift
│   └── GhostTestbedTests/
│       └── GhostClientTests.swift
└── README.md
```
