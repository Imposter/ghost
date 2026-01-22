# Ghost-Go Mobile Build Makefile
# For Windows, run with: make <target>
# Requires: Go 1.21+, gomobile, Android NDK (for Android builds)

# Configuration
MOBILE_PKG := ./mobile
BUILD_DIR := ./build
ANDROID_API := 21
IOS_MIN_VERSION := 13.0

# Detect Android NDK (Windows paths)
ANDROID_NDK_HOME ?= $(shell echo $$ANDROID_NDK_HOME)
ifeq ($(ANDROID_NDK_HOME),)
    # Try common Windows locations
    ANDROID_SDK := $(shell echo $$LOCALAPPDATA)/Android/Sdk
    NDK_VERSION := $(shell ls "$(ANDROID_SDK)/ndk" 2>/dev/null | tail -1)
    ifneq ($(NDK_VERSION),)
        ANDROID_NDK_HOME := $(ANDROID_SDK)/ndk/$(NDK_VERSION)
    endif
endif

# Build flags to work around gomobile compatibility issues
# -checklinkname=0 disables the Go 1.23+ linker check for internal symbol references
# This is needed because github.com/wlynxg/anet references net.zoneCache
LDFLAGS := -checklinkname=0

.PHONY: all android ios clean test help check-deps check-android-ndk

# Default target
all: android

# Help target
help:
	@echo "Ghost-Go Mobile Build System"
	@echo ""
	@echo "Targets:"
	@echo "  android          Build Android AAR library"
	@echo "  ios              Build iOS XCFramework (macOS only)"
	@echo "  all              Build for all platforms"
	@echo "  test             Run Go tests"
	@echo "  test-mobile      Run mobile package tests"
	@echo "  test-integration Run integration tests"
	@echo "  clean            Remove build artifacts"
	@echo "  check-deps       Verify build dependencies"
	@echo ""
	@echo "Environment Variables:"
	@echo "  ANDROID_NDK_HOME  Path to Android NDK (auto-detected if not set)"
	@echo ""
	@echo "Current Configuration:"
	@echo "  BUILD_DIR        = $(BUILD_DIR)"
	@echo "  ANDROID_NDK_HOME = $(ANDROID_NDK_HOME)"
	@echo "  ANDROID_API      = $(ANDROID_API)"
	@echo "  IOS_MIN_VERSION  = $(IOS_MIN_VERSION)"

# Check dependencies
check-deps:
	@echo "Checking dependencies..."
	@go version || (echo "ERROR: Go not found" && exit 1)
	@gomobile version 2>/dev/null || (echo "WARNING: gomobile version check failed, but may still work")
	@echo "Dependencies OK"

# Check Android NDK
check-android-ndk:
	@if [ -z "$(ANDROID_NDK_HOME)" ]; then \
		echo "ERROR: ANDROID_NDK_HOME not set and could not be auto-detected"; \
		echo "Please set ANDROID_NDK_HOME environment variable"; \
		echo "Example: export ANDROID_NDK_HOME=/path/to/android-ndk"; \
		exit 1; \
	fi
	@if [ ! -d "$(ANDROID_NDK_HOME)" ]; then \
		echo "ERROR: ANDROID_NDK_HOME directory does not exist: $(ANDROID_NDK_HOME)"; \
		exit 1; \
	fi
	@echo "Using Android NDK: $(ANDROID_NDK_HOME)"

# Build Android AAR
android: check-deps check-android-ndk
	@echo "Building Android AAR..."
	@echo "  Target API: $(ANDROID_API)"
	@echo "  Output: $(BUILD_DIR)/ghost.aar"
	@mkdir -p $(BUILD_DIR)
	ANDROID_NDK_HOME="$(ANDROID_NDK_HOME)" gomobile bind \
		-v \
		-ldflags="$(LDFLAGS)" \
		-target=android \
		-androidapi $(ANDROID_API) \
		-o $(BUILD_DIR)/ghost.aar \
		$(MOBILE_PKG)
	@echo ""
	@echo "Build complete: $(BUILD_DIR)/ghost.aar"
	@ls -lh $(BUILD_DIR)/ghost.aar

# Build iOS XCFramework (macOS only)
ios: check-deps
	@echo "Building iOS XCFramework..."
	@echo "  Minimum iOS: $(IOS_MIN_VERSION)"
	@echo "  Output: $(BUILD_DIR)/Mobile.xcframework"
	@mkdir -p $(BUILD_DIR)
	gomobile bind \
		-v \
		-ldflags="$(LDFLAGS)" \
		-target=ios \
		-iosversion $(IOS_MIN_VERSION) \
		-o $(BUILD_DIR)/Mobile.xcframework \
		$(MOBILE_PKG)
	@echo ""
	@echo "Build complete: $(BUILD_DIR)/Mobile.xcframework"

# Run all tests
test: test-mobile test-integration

# Run mobile package unit tests
test-mobile:
	@echo "Running mobile package tests..."
	go test -v $(MOBILE_PKG)/...

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	go test -v ./tests/...

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	go test -v -race $(MOBILE_PKG)/... ./tests/...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out $(MOBILE_PKG)/... ./tests/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Install gomobile (run once)
install-gomobile:
	@echo "Installing gomobile..."
	go install golang.org/x/mobile/cmd/gomobile@latest
	go install golang.org/x/mobile/cmd/gobind@latest
	gomobile init
	@echo "gomobile installed"

# Verify the AAR contents
verify-aar:
	@echo "Verifying AAR contents..."
	@if [ -f "$(BUILD_DIR)/ghost.aar" ]; then \
		unzip -l $(BUILD_DIR)/ghost.aar | head -30; \
	else \
		echo "ERROR: ghost.aar not found. Run 'make android' first."; \
		exit 1; \
	fi
