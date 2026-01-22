#!/usr/bin/env python3
"""
Ghost Mobile Testbed Build and Run Script

This script automates building the gomobile bindings, preparing the React Native
testbed app, and running it for development/testing.

Usage:
    python scripts/mobile-testbed.py build       # Build gomobile bindings
    python scripts/mobile-testbed.py prepare     # Install npm dependencies
    python scripts/mobile-testbed.py prebuild    # Generate native projects (Expo prebuild)
    python scripts/mobile-testbed.py run         # Start Expo dev server
    python scripts/mobile-testbed.py android     # Run on Android device/emulator
    python scripts/mobile-testbed.py ios         # Run on iOS simulator (macOS only)
    python scripts/mobile-testbed.py server      # Run desktop test server
    python scripts/mobile-testbed.py all         # Full workflow: build + prepare + prebuild + android
    python scripts/mobile-testbed.py clean       # Clean build artifacts
"""

import argparse
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path


# Colors for terminal output
class Colors:
    HEADER = '\033[95m'
    BLUE = '\033[94m'
    CYAN = '\033[96m'
    GREEN = '\033[92m'
    WARNING = '\033[93m'
    FAIL = '\033[91m'
    ENDC = '\033[0m'
    BOLD = '\033[1m'


# Use ASCII-compatible symbols for Windows compatibility
SYMBOL_CHECK = "[OK]"
SYMBOL_CROSS = "[X]"
SYMBOL_ARROW = "->"
SYMBOL_WARN = "[!]"


def print_header(msg: str):
    print(f"\n{Colors.HEADER}{Colors.BOLD}{'='*60}{Colors.ENDC}")
    print(f"{Colors.HEADER}{Colors.BOLD}{msg}{Colors.ENDC}")
    print(f"{Colors.HEADER}{Colors.BOLD}{'='*60}{Colors.ENDC}\n")


def print_step(msg: str):
    print(f"{Colors.CYAN}{SYMBOL_ARROW} {msg}{Colors.ENDC}")


def print_success(msg: str):
    print(f"{Colors.GREEN}{SYMBOL_CHECK} {msg}{Colors.ENDC}")


def print_warning(msg: str):
    print(f"{Colors.WARNING}{SYMBOL_WARN} {msg}{Colors.ENDC}")


def print_error(msg: str):
    print(f"{Colors.FAIL}{SYMBOL_CROSS} {msg}{Colors.ENDC}")


def get_project_root() -> Path:
    """Get the project root directory (ghost/)."""
    script_dir = Path(__file__).resolve().parent
    return script_dir.parent


def get_ghost_go_dir() -> Path:
    """Get the ghost-go directory."""
    return get_project_root() / "ghost-go"


def get_testbed_dir() -> Path:
    """Get the mobile-testbed directory."""
    return get_project_root() / "mobile-testbed"


def get_npm_cmd() -> str:
    """Get the correct npm command for the platform."""
    return "npm.cmd" if platform.system() == "Windows" else "npm"


def get_npx_cmd() -> str:
    """Get the correct npx command for the platform."""
    return "npx.cmd" if platform.system() == "Windows" else "npx"


def run_command(cmd: list, cwd: Path = None, env: dict = None, check: bool = True) -> subprocess.CompletedProcess:
    """Run a command and return the result."""
    print_step(f"Running: {' '.join(cmd)}")

    # Merge environment
    full_env = os.environ.copy()
    if env:
        full_env.update(env)

    try:
        result = subprocess.run(
            cmd,
            cwd=cwd,
            env=full_env,
            capture_output=False,
            text=True,
            check=check
        )
        return result
    except subprocess.CalledProcessError as e:
        print_error(f"Command failed with exit code {e.returncode}")
        raise


def check_prerequisites() -> dict:
    """Check that required tools are installed."""
    print_header("Checking Prerequisites")

    tools = {
        "go": ["go", "version"],
        "gomobile": ["gomobile", "version"],
        "node": ["node", "--version"],
        "npm": [get_npm_cmd(), "--version"],
    }

    results = {}
    for name, cmd in tools.items():
        try:
            result = subprocess.run(cmd, capture_output=True, text=True)
            # gomobile version returns non-zero but still works
            if name == "gomobile" or result.returncode == 0:
                version = result.stdout.strip() or result.stderr.strip()
                print_success(f"{name}: {version.split(chr(10))[0]}")
                results[name] = True
            else:
                print_error(f"{name}: not found")
                results[name] = False
        except FileNotFoundError:
            print_error(f"{name}: not found")
            results[name] = False

    return results


def find_android_ndk() -> str | None:
    """Find the Android NDK path."""
    # Check environment variable first
    ndk_home = os.environ.get("ANDROID_NDK_HOME")
    if ndk_home and os.path.isdir(ndk_home):
        return ndk_home

    # Try common locations
    if platform.system() == "Windows":
        local_app_data = os.environ.get("LOCALAPPDATA", "")
        sdk_path = Path(local_app_data) / "Android" / "Sdk" / "ndk"
    elif platform.system() == "Darwin":
        sdk_path = Path.home() / "Library" / "Android" / "sdk" / "ndk"
    else:
        sdk_path = Path.home() / "Android" / "Sdk" / "ndk"

    if sdk_path.exists():
        # Get the latest NDK version
        versions = sorted(sdk_path.iterdir(), reverse=True)
        if versions:
            return str(versions[0])

    return None


def build_gomobile(target: str = "android") -> bool:
    """Build gomobile bindings."""
    print_header(f"Building gomobile bindings for {target}")

    ghost_go = get_ghost_go_dir()
    build_dir = ghost_go / "build"

    # Create build directory
    build_dir.mkdir(exist_ok=True)

    # Find Android NDK for Android builds
    env = {}
    if target == "android":
        ndk_path = find_android_ndk()
        if not ndk_path:
            print_error("Android NDK not found!")
            print_warning("Set ANDROID_NDK_HOME environment variable or install NDK via Android Studio")
            return False
        print_success(f"Using Android NDK: {ndk_path}")
        env["ANDROID_NDK_HOME"] = ndk_path

    # Build command
    if target == "android":
        output = build_dir / "ghost.aar"
        cmd = [
            "gomobile", "bind",
            "-v",
            "-ldflags=-checklinkname=0",
            "-target=android",
            "-androidapi", "21",
            "-o", str(output),
            "./mobile"
        ]
    elif target == "ios":
        if platform.system() != "Darwin":
            print_error("iOS builds are only supported on macOS")
            return False
        output = build_dir / "Mobile.xcframework"
        cmd = [
            "gomobile", "bind",
            "-v",
            "-ldflags=-checklinkname=0",
            "-target=ios",
            "-iosversion", "13.0",
            "-o", str(output),
            "./mobile"
        ]
    else:
        print_error(f"Unknown target: {target}")
        return False

    try:
        run_command(cmd, cwd=ghost_go, env=env)

        if output.exists():
            size_mb = output.stat().st_size / (1024 * 1024)
            print_success(f"Build complete: {output} ({size_mb:.1f} MB)")
            return True
        else:
            print_error("Build output not found")
            return False
    except subprocess.CalledProcessError:
        return False


def prepare_testbed() -> bool:
    """Install npm dependencies for the testbed."""
    print_header("Preparing Mobile Testbed")

    testbed = get_testbed_dir()

    if not testbed.exists():
        print_error(f"Testbed directory not found: {testbed}")
        return False

    # Check if node_modules exists
    node_modules = testbed / "node_modules"
    if node_modules.exists():
        print_warning("node_modules already exists, skipping npm install")
        print_step("Run 'npm install' manually to update dependencies")
        return True

    try:
        run_command([get_npm_cmd(), "install"], cwd=testbed)
        print_success("Dependencies installed")
        return True
    except subprocess.CalledProcessError:
        return False


def run_expo_start() -> bool:
    """Start the Expo development server."""
    print_header("Starting Expo Development Server")

    testbed = get_testbed_dir()

    print_step("Starting Expo...")
    print_warning("Press Ctrl+C to stop")
    print()

    try:
        # Use npx to run expo
        run_command([get_npx_cmd(), "expo", "start"], cwd=testbed, check=False)
        return True
    except KeyboardInterrupt:
        print("\nStopped")
        return True


def run_android() -> bool:
    """Run the app on Android device/emulator."""
    print_header("Running on Android")

    testbed = get_testbed_dir()

    print_step("Starting Android app...")
    print_warning("Make sure an Android device/emulator is connected")
    print()

    try:
        run_command([get_npx_cmd(), "expo", "run:android"], cwd=testbed, check=False)
        return True
    except KeyboardInterrupt:
        print("\nStopped")
        return True


def run_ios() -> bool:
    """Run the app on iOS simulator."""
    print_header("Running on iOS Simulator")

    if platform.system() != "Darwin":
        print_error("iOS simulator is only available on macOS")
        return False

    testbed = get_testbed_dir()

    print_step("Starting iOS simulator...")
    print()

    try:
        run_command([get_npx_cmd(), "expo", "run:ios"], cwd=testbed, check=False)
        return True
    except KeyboardInterrupt:
        print("\nStopped")
        return True


def run_desktop_server() -> bool:
    """Run the desktop test server."""
    print_header("Running Desktop Test Server")

    ghost_go = get_ghost_go_dir()

    print_step("Starting desktop peer (server role)...")
    print_warning("Press Ctrl+C to stop")
    print()

    try:
        run_command(["go", "run", "./cmd/mobile-demo", "-role", "server"], cwd=ghost_go, check=False)
        return True
    except KeyboardInterrupt:
        print("\nStopped")
        return True


def clean_build() -> bool:
    """Clean build artifacts."""
    print_header("Cleaning Build Artifacts")

    ghost_go = get_ghost_go_dir()
    testbed = get_testbed_dir()

    # Clean ghost-go build directory
    build_dir = ghost_go / "build"
    if build_dir.exists():
        print_step(f"Removing {build_dir}")
        shutil.rmtree(build_dir)
        print_success("Removed ghost-go/build/")

    # Clean testbed node_modules (optional)
    node_modules = testbed / "node_modules"
    if node_modules.exists():
        response = input("Remove node_modules? [y/N] ").strip().lower()
        if response == 'y':
            print_step(f"Removing {node_modules}")
            shutil.rmtree(node_modules)
            print_success("Removed node_modules/")

    # Clean Expo cache
    expo_cache = testbed / ".expo"
    if expo_cache.exists():
        print_step(f"Removing {expo_cache}")
        shutil.rmtree(expo_cache)
        print_success("Removed .expo/")

    print_success("Clean complete")
    return True


def run_prebuild() -> bool:
    """Run Expo prebuild to generate native projects."""
    print_header("Running Expo Prebuild")

    testbed = get_testbed_dir()
    ghost_go = get_ghost_go_dir()

    # Check if ghost.aar exists
    aar_path = ghost_go / "build" / "ghost.aar"
    if not aar_path.exists():
        print_error(f"ghost.aar not found at {aar_path}")
        print_warning("Run 'python scripts/mobile-testbed.py build' first")
        return False

    print_success(f"Found ghost.aar: {aar_path}")

    # Run npm install first if needed
    node_modules = testbed / "node_modules"
    if not node_modules.exists():
        print_step("Installing npm dependencies first...")
        try:
            run_command([get_npm_cmd(), "install"], cwd=testbed)
        except subprocess.CalledProcessError:
            return False

    # Run expo prebuild
    print_step("Running expo prebuild...")
    print_warning("This will generate native Android and iOS projects")
    print()

    try:
        # Use --clean to regenerate from scratch
        run_command([get_npx_cmd(), "expo", "prebuild", "--clean"], cwd=testbed)

        # Copy native module files that may have been overwritten
        copy_native_module_files()

        print_success("Prebuild complete")
        print()
        print_step("Next steps:")
        print("  1. Run 'python scripts/mobile-testbed.py android' to build and run on Android")
        print("  2. Or 'python scripts/mobile-testbed.py ios' on macOS for iOS")
        return True
    except subprocess.CalledProcessError:
        print_error("Prebuild failed")
        return False


def copy_native_module_files() -> bool:
    """Copy Ghost native module Kotlin files to the generated Android project."""
    print_step("Copying Ghost native module files...")

    testbed = get_testbed_dir()

    # Source files (our manually created ones)
    src_module = testbed / "android" / "app" / "src" / "main" / "java" / "com" / "ghost" / "testbed"

    # Check if source files exist
    module_kt = src_module / "GhostModule.kt"
    package_kt = src_module / "GhostPackage.kt"

    if not module_kt.exists() or not package_kt.exists():
        print_warning("Native module source files not found - they should be created by the config plugin")
        return False

    print_success("Native module files are in place")
    return True


def run_tests() -> bool:
    """Run Go tests for the mobile package."""
    print_header("Running Tests")

    ghost_go = get_ghost_go_dir()

    try:
        print_step("Running mobile package tests...")
        run_command(["go", "test", "-v", "./mobile/..."], cwd=ghost_go)

        print_step("Running integration tests...")
        run_command(["go", "test", "-v", "./tests/..."], cwd=ghost_go)

        print_success("All tests passed")
        return True
    except subprocess.CalledProcessError:
        print_error("Tests failed")
        return False


def main():
    parser = argparse.ArgumentParser(
        description="Ghost Mobile Testbed Build and Run Script",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Commands:
  build [android|ios]  Build gomobile bindings (default: android)
  prepare              Install npm dependencies
  prebuild             Run Expo prebuild to generate native projects
  run                  Start Expo development server
  android              Run on Android device/emulator
  ios                  Run on iOS simulator (macOS only)
  server               Run desktop test server
  test                 Run Go tests
  all                  Build + prepare + prebuild + android
  clean                Clean build artifacts

Examples:
  python scripts/mobile-testbed.py build      # Build gomobile bindings
  python scripts/mobile-testbed.py prebuild   # Generate native projects
  python scripts/mobile-testbed.py android    # Build and run on Android
  python scripts/mobile-testbed.py all        # Full workflow
  python scripts/mobile-testbed.py server     # Run desktop test server
"""
    )

    parser.add_argument("command", nargs="?", default="all",
                        choices=["build", "prepare", "prebuild", "run", "android", "ios", "server", "test", "all", "clean"],
                        help="Command to run")
    parser.add_argument("target", nargs="?", default="android",
                        help="Build target (android or ios)")
    parser.add_argument("--skip-checks", action="store_true",
                        help="Skip prerequisite checks")

    args = parser.parse_args()

    # Print banner
    banner = r"""
   _____ _               _     __  __       _     _ _
  / ____| |             | |   |  \/  |     | |   (_) |
 | |  __| |__   ___  ___| |_  | \  / | ___ | |__  _| | ___
 | | |_ | '_ \ / _ \/ __| __| | |\/| |/ _ \| '_ \| | |/ _ \
 | |__| | | | | (_) \__ \ |_  | |  | | (_) | |_) | | |  __/
  \_____|_| |_|\___/|___/\__| |_|  |_|\___/|_.__/|_|_|\___|
"""
    print(f"{Colors.BOLD}{Colors.CYAN}{banner}{Colors.ENDC}")

    # Check prerequisites (unless skipped)
    if not args.skip_checks and args.command not in ["clean"]:
        prereqs = check_prerequisites()

        # Check required tools based on command
        if args.command in ["build", "all", "test", "server"]:
            if not prereqs.get("go"):
                print_error("Go is required but not found")
                sys.exit(1)
            if args.command == "build" and not prereqs.get("gomobile"):
                print_error("gomobile is required for building")
                print_step("Install with: go install golang.org/x/mobile/cmd/gomobile@latest")
                sys.exit(1)

        if args.command in ["prepare", "prebuild", "run", "android", "ios", "all"]:
            if not prereqs.get("node") or not prereqs.get("npm"):
                print_error("Node.js and npm are required")
                sys.exit(1)

    # Run command
    success = True

    if args.command == "build":
        success = build_gomobile(args.target)

    elif args.command == "prepare":
        success = prepare_testbed()

    elif args.command == "prebuild":
        success = run_prebuild()

    elif args.command == "run":
        success = run_expo_start()

    elif args.command == "android":
        success = run_android()

    elif args.command == "ios":
        success = run_ios()

    elif args.command == "server":
        success = run_desktop_server()

    elif args.command == "test":
        success = run_tests()

    elif args.command == "all":
        success = build_gomobile(args.target)
        if success:
            success = prepare_testbed()
        if success:
            success = run_prebuild()
        if success:
            success = run_android()

    elif args.command == "clean":
        success = clean_build()

    # Exit with appropriate code
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
