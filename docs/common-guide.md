# GhostBridge Common Guide

This document defines standards, best practices, and requirements that apply across all phases of the GhostBridge project. **Read this before implementing any phase.**

---

## Table of Contents

1. [Project Principles](#project-principles)
2. [Code Standards](#code-standards)
3. [Security Requirements](#security-requirements)
4. [Error Handling](#error-handling)
5. [Logging & Observability](#logging--observability)
6. [Testing Requirements](#testing-requirements)
7. [Documentation Standards](#documentation-standards)
8. [Git & Version Control](#git--version-control)
9. [Environment Configuration](#environment-configuration)
10. [Dependency Management](#dependency-management)

---

## Project Principles

### Core Values

| Principle | Description |
|-----------|-------------|
| **Security First** | Never compromise security for convenience. Assume hostile networks. |
| **User Privacy** | No data leaves the mesh unless explicitly requested. No analytics without consent. |
| **Fail Safe** | When errors occur, fail closed (deny access) rather than fail open. |
| **Simplicity** | Prefer simple solutions. Complexity is a liability. |
| **Observability** | Everything that can fail should be logged and monitored. |

### Follow Technology Best Practices

**Every technology, language, and framework we use must follow its established best practices.** This is non-negotiable.

When implementing any component:

1. **Research first** - Before writing code, understand the idiomatic patterns for that technology
2. **Use official guidelines** - Follow the official style guides and conventions
3. **Leverage the ecosystem** - Use established libraries and tools, not custom solutions
4. **Stay current** - Follow modern practices, not legacy patterns

| Technology | Best Practices Source |
|------------|----------------------|
| **Go** | [Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments), [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md) |
| **TypeScript** | [TypeScript Handbook](https://www.typescriptlang.org/docs/handbook/), [Google TS Style Guide](https://google.github.io/styleguide/tsguide.html) |
| **React Native** | [React Native Docs](https://reactnative.dev/docs/getting-started), [React Patterns](https://reactpatterns.com/) |
| **Kotlin** | [Kotlin Coding Conventions](https://kotlinlang.org/docs/coding-conventions.html), [Android Kotlin Guides](https://developer.android.com/kotlin/style-guide) |
| **Swift** | [Swift API Design Guidelines](https://www.swift.org/documentation/api-design-guidelines/), [Google Swift Style Guide](https://google.github.io/swift/) |
| **Python** | [PEP 8](https://peps.python.org/pep-0008/), [Google Python Style Guide](https://google.github.io/styleguide/pyguide.html) |
| **Docker** | [Dockerfile Best Practices](https://docs.docker.com/develop/develop-images/dockerfile_best-practices/) |
| **Shell/Bash** | [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html) |
| **SQL** | [SQL Style Guide](https://www.sqlstyle.guide/) |

**Examples of what this means in practice:**

```go
// Go: Use error wrapping (Go 1.13+), not string concatenation
// Good
return fmt.Errorf("failed to connect: %w", err)

// Bad
return errors.New("failed to connect: " + err.Error())
```

```typescript
// TypeScript: Use strict mode and proper typing
// Good
function getUser(id: string): Promise<User | null>

// Bad
function getUser(id: any): Promise<any>
```

```kotlin
// Kotlin: Use coroutines for async, not callbacks
// Good
suspend fun fetchData(): Result<Data>

// Bad
fun fetchData(callback: (Data?, Error?) -> Unit)
```

```swift
// Swift: Use Result type and async/await (Swift 5.5+)
// Good
func connect() async throws -> Connection

// Bad
func connect(completion: @escaping (Connection?, Error?) -> Void)
```

**When in doubt:**
- Check the official documentation
- Look at popular open-source projects in that ecosystem
- Ask: "Is this how an experienced developer in this language would write it?"

### Architecture Decisions Record (ADR)

When making significant decisions, document them:

```markdown
## ADR-XXX: [Title]

**Status:** Proposed | Accepted | Deprecated | Superseded

**Context:** Why is this decision needed?

**Decision:** What did we decide?

**Consequences:** What are the trade-offs?
```

Keep ADRs in `docs/adr/` directory.

---

## Code Standards

### General

- **No hardcoded secrets** - Use environment variables or secure vaults
- **No hardcoded IPs/URLs** - Always use configuration
- **No TODO comments in production code** - Track in issue tracker instead
- **No commented-out code** - Delete it; git has history
- **No magic numbers** - Use named constants

### Go (Phase 3)

```go
// Package naming: lowercase, single word
package GhostBridge

// Exported functions: PascalCase with doc comments
// Initialize configures the bridge with the given settings.
// It must be called before Start().
func Initialize(config *BridgeConfig) error {
    // Validate inputs first
    if config == nil {
        return errors.New("config cannot be nil")
    }

    // Use explicit error handling
    if err := validateConfig(config); err != nil {
        return fmt.Errorf("invalid config: %w", err)
    }

    // ...
}

// Private functions: camelCase
func validateConfig(c *BridgeConfig) error {
    // ...
}

// Constants: ALL_CAPS for public, camelCase for private
const (
    DefaultTimeout = 30 * time.Second
    maxRetries     = 3
)

// Errors: use sentinel errors or wrap with context
var (
    ErrNotInitialized = errors.New("bridge not initialized")
    ErrAlreadyRunning = errors.New("bridge already running")
)
```

**Go Linting:**
```bash
# Required linters
golangci-lint run

# golangci.yml config
linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gosec        # Security
    - bodyclose    # HTTP body close
    - contextcheck # Context handling
```

### TypeScript/JavaScript (Phase 4, 5)

```typescript
// Use strict TypeScript
// tsconfig.json: "strict": true

// Interfaces over types for objects
interface BridgeStatus {
  state: 'disconnected' | 'connecting' | 'connected' | 'error';
  proxyPort: number;
  meshIP: string;
  error?: string;  // Optional fields with ?
}

// Async/await over .then() chains
async function connect(): Promise<number> {
  try {
    const port = await GhostBridge.connect();
    return port;
  } catch (error) {
    // Always type-check errors
    if (error instanceof Error) {
      throw new ConnectionError(error.message);
    }
    throw new ConnectionError('Unknown error');
  }
}

// Named exports over default exports
export { GhostBridge, BridgeStatus, BridgeConfig };

// Avoid any - use unknown if type is truly unknown
function handleResponse(data: unknown): void {
  if (typeof data === 'object' && data !== null) {
    // Type guard
  }
}
```

**ESLint Config:**
```json
{
  "extends": [
    "eslint:recommended",
    "@react-native",
    "plugin:@typescript-eslint/recommended"
  ],
  "rules": {
    "@typescript-eslint/no-explicit-any": "error",
    "@typescript-eslint/explicit-function-return-type": "warn",
    "no-console": ["warn", { "allow": ["warn", "error"] }]
  }
}
```

### Kotlin (Phase 4 - Android)

```kotlin
// Use data classes for DTOs
data class BridgeStatus(
    val state: String,
    val proxyPort: Int,
    val meshIP: String,
    val error: String?
)

// Null safety - use !! sparingly, prefer ?. and ?:
fun getPort(): Int {
    return bridge?.status?.proxyPort ?: 0
}

// Coroutines for async work
suspend fun connect(): Int = withContext(Dispatchers.IO) {
    // IO-bound work
}

// Use when instead of if-else chains
fun handleState(state: String) = when (state) {
    "connected" -> onConnected()
    "error" -> onError()
    else -> onUnknown()
}
```

### Swift (Phase 4 - iOS)

```swift
// Use structs for value types
struct BridgeStatus {
    let state: State
    let proxyPort: Int
    let meshIP: String
    let error: String?

    enum State: String {
        case disconnected
        case connecting
        case connected
        case error
    }
}

// Guard for early returns
func connect() throws -> Int {
    guard isInitialized else {
        throw BridgeError.notInitialized
    }

    guard let config = self.config else {
        throw BridgeError.missingConfig
    }

    // Continue with valid state...
}

// Use Result type for async operations
func fetchStatus(completion: @escaping (Result<BridgeStatus, Error>) -> Void) {
    // ...
}
```

### Python (Phase 2)

```python
"""Module docstring: Brief description of the module."""

from typing import Optional, Dict, Any
from dataclasses import dataclass

@dataclass
class BridgeConfig:
    """Configuration for the bridge."""
    control_url: str
    auth_key: str
    storage_dir: str
    hostname: str = "mobile-client"
    verbose: bool = False


def connect(config: BridgeConfig) -> int:
    """
    Connect to the mesh network.

    Args:
        config: Bridge configuration

    Returns:
        The proxy port number

    Raises:
        ConnectionError: If connection fails
    """
    if not config.control_url:
        raise ValueError("control_url is required")

    # ...
```

**Python Tooling:**
```bash
# Formatting
black .

# Linting
ruff check .

# Type checking
mypy --strict .
```

### Shell Scripts (All Phases)

```bash
#!/bin/bash
# Always use bash explicitly, not sh

# Strict mode - REQUIRED for all scripts
set -euo pipefail

# Constants at the top
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly LOG_FILE="/tmp/GhostBridge-$(date +%Y%m%d).log"

# Functions before main logic
log_info() {
    echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $1" | tee -a "$LOG_FILE"
}

log_error() {
    echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $1" >&2 | tee -a "$LOG_FILE"
}

# Cleanup trap
cleanup() {
    # Always clean up temp files
    rm -f "$TEMP_FILE" 2>/dev/null || true
}
trap cleanup EXIT

# Main function
main() {
    log_info "Starting..."
    # ...
}

# Call main
main "$@"
```

---

## Security Requirements

### Secrets Management

| Do | Don't |
|----|-------|
| Use environment variables | Hardcode in source |
| Use secure vaults (HashiCorp Vault, AWS Secrets Manager) | Store in git |
| Rotate keys regularly | Use same key forever |
| Log key usage, not key values | Log secrets |

### Pre-Auth Key Handling

```go
// Good: Short-lived, single-use keys
key := headscale.CreatePreAuthKey(
    user,
    reusable: false,        // Single use
    expiration: 15*time.Minute,  // Short lived
)

// Bad: Long-lived, reusable keys
key := headscale.CreatePreAuthKey(
    user,
    reusable: true,         // Can be reused!
    expiration: 365*24*time.Hour,  // Year-long!
)
```

### Input Validation

**Always validate:**
- User input
- API responses
- Configuration values
- File paths
- Network addresses

```go
// Validate IP:port format
func validateTarget(target string) error {
    host, port, err := net.SplitHostPort(target)
    if err != nil {
        return fmt.Errorf("invalid target format: %w", err)
    }

    if ip := net.ParseIP(host); ip == nil {
        return fmt.Errorf("invalid IP address: %s", host)
    }

    if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
        return fmt.Errorf("invalid port: %s", port)
    }

    return nil
}
```

### Network Security

- **Always use HTTPS** for Headscale communication
- **Validate TLS certificates** (no `InsecureSkipVerify: true` in production)
- **Implement timeouts** on all network operations
- **Rate limit** API endpoints

### Mobile Security

- Store auth tokens in Keychain (iOS) / Keystore (Android)
- Enable certificate pinning for production
- Obfuscate release builds (ProGuard for Android)
- Never log sensitive data

---

## Error Handling

### Error Types

```go
// Define domain-specific errors
var (
    ErrNotInitialized  = errors.New("bridge not initialized")
    ErrConnectionFailed = errors.New("connection failed")
    ErrTimeout         = errors.New("operation timed out")
)

// Wrap errors with context
func Connect() error {
    if err := mesh.Dial(); err != nil {
        return fmt.Errorf("connect: %w", err)
    }
    return nil
}

// Check error types
if errors.Is(err, ErrTimeout) {
    // Handle timeout specifically
}
```

### Error Messages

| Good | Bad |
|------|-----|
| "Connection to headscale.example.com:443 timed out after 30s" | "Connection failed" |
| "Invalid auth key format: expected 32 characters, got 16" | "Bad key" |
| "Failed to create storage directory /app/data: permission denied" | "Storage error" |

### User-Facing Errors

```typescript
// Map internal errors to user-friendly messages
function getUserMessage(error: Error): string {
  const messages: Record<string, string> = {
    'NETWORK_ERROR': 'Unable to connect. Please check your internet connection.',
    'AUTH_FAILED': 'Authentication failed. Please sign in again.',
    'TIMEOUT': 'The request took too long. Please try again.',
  };

  return messages[error.code] || 'Something went wrong. Please try again.';
}
```

---

## Logging & Observability

### Log Levels

| Level | Use For |
|-------|---------|
| ERROR | Errors requiring immediate attention |
| WARN | Potential issues, degraded operation |
| INFO | Significant events (startup, connections) |
| DEBUG | Detailed diagnostic information |

### Structured Logging

```go
// Use structured logging
logger.Info("connection established",
    "mesh_ip", status.IP,
    "latency_ms", latency.Milliseconds(),
    "peer_count", len(peers),
)

// Output: {"level":"info","msg":"connection established","mesh_ip":"100.64.0.1","latency_ms":127,"peer_count":3}
```

### What to Log

**Do Log:**
- Application startup/shutdown
- Connection state changes
- Authentication events (success/failure)
- Error details with context
- Performance metrics

**Don't Log:**
- Secrets, tokens, or keys
- Personal data (PII)
- Full request/response bodies
- High-frequency events in production

### Metrics

```go
// Define metrics
var (
    connectionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "GhostBridge_connections_total",
            Help: "Total number of mesh connections",
        },
        []string{"status"},  // "success" or "failure"
    )

    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "GhostBridge_request_duration_seconds",
            Help:    "Request duration in seconds",
            Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5},
        },
        []string{"endpoint"},
    )
)
```

---

## Testing Requirements

### Test Coverage Targets

| Component | Minimum Coverage |
|-----------|------------------|
| Go Bridge | 80% |
| Backend API | 80% |
| React Native Hooks | 70% |
| Native Modules | 60% |

### Test Types

```
Unit Tests        → Test individual functions
Integration Tests → Test component interactions
E2E Tests         → Test full user flows
```

### Test Naming

```go
// Go: TestFunctionName_Scenario_ExpectedBehavior
func TestConnect_WithValidConfig_ReturnsPort(t *testing.T) {}
func TestConnect_WithMissingURL_ReturnsError(t *testing.T) {}
func TestConnect_WithTimeout_RetriesThreeTimes(t *testing.T) {}
```

```typescript
// TypeScript: describe/it pattern
describe('GhostBridge', () => {
  describe('connect', () => {
    it('returns port when connection succeeds', async () => {});
    it('throws error when not initialized', async () => {});
    it('retries on temporary failure', async () => {});
  });
});
```

### Test Data

- Use fixtures for complex test data
- Never use production data in tests
- Reset state between tests

---

## Documentation Standards

### Code Comments

```go
// Single-line: Explain WHY, not WHAT
// Using retry because the mesh occasionally drops first connection attempt
for i := 0; i < maxRetries; i++ {

// Multi-line: For complex logic
/*
 * The proxy uses a two-phase lookup:
 * 1. Check X-Target-IP header for explicit target
 * 2. Fall back to path-based routing (/mesh/{target}/...)
 *
 * This allows both programmatic access (header) and
 * browser-friendly URLs (path).
 */
```

### README Requirements

Every component must have a README with:
- Purpose and overview
- Prerequisites
- Quick start
- Configuration options
- Common issues

### API Documentation

```go
// Document all public APIs with godoc format
// Connect establishes a connection to the mesh network.
//
// It initializes the tsnet server, authenticates with Headscale,
// and starts the local HTTP proxy.
//
// Returns the proxy port on success, or an error if:
//   - Bridge is not initialized (call Initialize first)
//   - Headscale is unreachable
//   - Authentication fails
//
// Example:
//
//     port, err := GhostBridge.Connect()
//     if err != nil {
//         log.Fatal(err)
//     }
//     fmt.Printf("Proxy running on port %d\n", port)
func Connect() (int, error) {
```

---

## Git & Version Control

### Branch Strategy

```
main           → Production-ready code
develop        → Integration branch
feature/*      → New features
bugfix/*       → Bug fixes
release/*      → Release preparation
hotfix/*       → Production hotfixes
```

### Commit Messages

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation
- `refactor`: Code change that neither fixes a bug nor adds a feature
- `test`: Adding tests
- `chore`: Maintenance tasks

**Examples:**
```
feat(bridge): add path-based routing support

Adds support for /mesh/{target}/{path} URL pattern as an
alternative to X-Target-IP header routing.

Closes #123
```

```
fix(proxy): handle connection timeout correctly

Previously, timeouts would cause a panic. Now returns
a proper 504 Gateway Timeout response.

Fixes #456
```

### Code Review Checklist

- [ ] Code follows style guidelines
- [ ] Tests are included and passing
- [ ] No secrets or sensitive data
- [ ] Error handling is appropriate
- [ ] Documentation is updated
- [ ] No unnecessary dependencies added

---

## Environment Configuration

### Configuration Hierarchy

```
1. Command-line flags (highest priority)
2. Environment variables (from .env files)
3. Config file
4. Default values (lowest priority)
```

### Using .env Files

**.env files are the standard way to manage environment-specific configuration.** Use them everywhere possible.

#### File Structure

Every component should have:

```
component/
├── .env.example     # Template with all variables (committed to git)
├── .env             # Local development values (NEVER committed)
├── .env.test        # Test environment values (NEVER committed)
├── .env.production  # Production values (NEVER committed)
└── ...
```

#### .env.example Template

**Always provide a `.env.example` file** that documents all required variables:

```bash
# .env.example
# Copy this file to .env and fill in the values
# NEVER commit .env files with real values

# =============================================================================
# Headscale Configuration
# =============================================================================
GhostBridge_HEADSCALE_URL=https://your-headscale-server.com
GhostBridge_HEADSCALE_API_KEY=your-api-key-here

# =============================================================================
# Bridge Configuration
# =============================================================================
GhostBridge_BRIDGE_HOSTNAME=mobile-client
GhostBridge_BRIDGE_TIMEOUT=30s
GhostBridge_BRIDGE_VERBOSE=false

# =============================================================================
# Database (Phase 5)
# =============================================================================
DATABASE_URL=postgresql://user:password@localhost:5432/GhostBridge

# =============================================================================
# Redis (Phase 5)
# =============================================================================
REDIS_URL=redis://localhost:6379

# =============================================================================
# Logging
# =============================================================================
LOG_LEVEL=info
LOG_FORMAT=json
```

#### Loading .env Files by Technology

**Node.js / TypeScript:**
```typescript
// Install: npm install dotenv
import 'dotenv/config';
// Or load explicitly:
import dotenv from 'dotenv';
dotenv.config({ path: '.env.local' });

// Access variables
const headscaleUrl = process.env.GhostBridge_HEADSCALE_URL;
if (!headscaleUrl) {
  throw new Error('GhostBridge_HEADSCALE_URL is required');
}
```

**Go:**
```go
// Install: go get github.com/joho/godotenv
import "github.com/joho/godotenv"

func init() {
    // Load .env file (optional in production where env vars are set externally)
    if err := godotenv.Load(); err != nil {
        log.Println("No .env file found, using environment variables")
    }
}

func main() {
    headscaleURL := os.Getenv("GhostBridge_HEADSCALE_URL")
    if headscaleURL == "" {
        log.Fatal("GhostBridge_HEADSCALE_URL is required")
    }
}
```

**Python:**
```python
# Install: pip install python-dotenv
from dotenv import load_dotenv
import os

load_dotenv()  # Loads from .env by default

headscale_url = os.getenv("GhostBridge_HEADSCALE_URL")
if not headscale_url:
    raise ValueError("GhostBridge_HEADSCALE_URL is required")
```

**Docker Compose:**
```yaml
# docker-compose.yml
services:
  headscale:
    image: headscale/headscale:latest
    env_file:
      - .env  # Load all variables from .env
    environment:
      # Or specify individually (overrides .env)
      - HEADSCALE_SERVER_URL=${GhostBridge_HEADSCALE_URL}
```

**Shell Scripts:**
```bash
#!/bin/bash
set -euo pipefail

# Load .env file if it exists
if [[ -f .env ]]; then
    set -a  # Auto-export all variables
    source .env
    set +a
fi

# Validate required variables
: "${GhostBridge_HEADSCALE_URL:?GhostBridge_HEADSCALE_URL is required}"
: "${GhostBridge_HEADSCALE_API_KEY:?GhostBridge_HEADSCALE_API_KEY is required}"
```

**React Native:**
```typescript
// Install: npm install react-native-dotenv
// babel.config.js:
module.exports = {
  plugins: [
    ['module:react-native-dotenv', {
      envName: 'APP_ENV',
      moduleName: '@env',
      path: '.env',
    }]
  ]
};

// Usage:
import { GhostBridge_HEADSCALE_URL } from '@env';
```

#### .env Security Rules

| Rule | Description |
|------|-------------|
| **Never commit .env** | Add to `.gitignore` immediately |
| **Always commit .env.example** | Documents required variables |
| **No secrets in .env.example** | Use placeholder values only |
| **Validate on startup** | Fail fast if required vars missing |
| **Use different files per environment** | `.env.development`, `.env.production` |
| **Rotate secrets regularly** | Especially after team changes |

#### Variable Naming Convention

```bash
# Format: PROJECTNAME_COMPONENT_SETTING
GhostBridge_HEADSCALE_URL=...      # Headscale server URL
GhostBridge_HEADSCALE_API_KEY=...  # Headscale API key
GhostBridge_BRIDGE_TIMEOUT=...     # Bridge timeout setting
GhostBridge_DB_HOST=...            # Database host
GhostBridge_DB_PORT=...            # Database port
GhostBridge_REDIS_URL=...          # Redis connection URL
```

#### Validation Helper

Create a validation utility for each component:

```typescript
// src/config/env.ts
import { z } from 'zod';

const envSchema = z.object({
  GhostBridge_HEADSCALE_URL: z.string().url(),
  GhostBridge_HEADSCALE_API_KEY: z.string().min(1),
  GhostBridge_BRIDGE_TIMEOUT: z.string().default('30s'),
  GhostBridge_BRIDGE_VERBOSE: z.enum(['true', 'false']).default('false'),
  NODE_ENV: z.enum(['development', 'test', 'production']).default('development'),
});

export const env = envSchema.parse(process.env);
```

```go
// config/env.go
type Config struct {
    HeadscaleURL    string `env:"GhostBridge_HEADSCALE_URL,required"`
    HeadscaleAPIKey string `env:"GhostBridge_HEADSCALE_API_KEY,required"`
    BridgeTimeout   string `env:"GhostBridge_BRIDGE_TIMEOUT" envDefault:"30s"`
    BridgeVerbose   bool   `env:"GhostBridge_BRIDGE_VERBOSE" envDefault:"false"`
}

// Use: github.com/caarlos0/env/v9
func LoadConfig() (*Config, error) {
    cfg := &Config{}
    if err := env.Parse(cfg); err != nil {
        return nil, fmt.Errorf("failed to parse config: %w", err)
    }
    return cfg, nil
}
```

### Environment Variables

```bash
# Naming convention: GhostBridge_<COMPONENT>_<SETTING>
GhostBridge_HEADSCALE_URL=https://headscale.example.com
GhostBridge_HEADSCALE_API_KEY=secret-key
GhostBridge_BRIDGE_VERBOSE=true
GhostBridge_BRIDGE_TIMEOUT=30s
```

### Config File Format

For complex configuration, use YAML files that reference environment variables:

```yaml
# config.yaml
headscale:
  url: ${GhostBridge_HEADSCALE_URL}
  api_key: ${GhostBridge_HEADSCALE_API_KEY}

bridge:
  hostname: ${GhostBridge_BRIDGE_HOSTNAME:-mobile-client}  # Default value
  timeout: ${GhostBridge_BRIDGE_TIMEOUT:-30s}
  verbose: ${GhostBridge_BRIDGE_VERBOSE:-false}

logging:
  level: ${LOG_LEVEL:-info}
  format: ${LOG_FORMAT:-json}
```

---

## Dependency Management

### Version Pinning

**Always pin versions:**
```go
// go.mod
require (
    tailscale.com/tsnet v1.56.1  // Pin exact version
)
```

```json
// package.json
{
  "dependencies": {
    "react-native": "0.73.2"  // Pin exact version
  }
}
```

### Dependency Updates

- Review changelogs before updating
- Run full test suite after updates
- Update one dependency at a time
- Document breaking changes

### Prohibited Dependencies

- No dependencies with known vulnerabilities
- No dependencies with incompatible licenses
- No dependencies that haven't been updated in 2+ years
- No dependencies with < 100 GitHub stars (unless necessary)

### Security Scanning

```bash
# Go
govulncheck ./...

# Node.js
npm audit

# Python
pip-audit
```

---

## Quick Reference

### Before Starting Any Phase

1. Read this guide
2. Set up linting and formatting tools
3. Configure pre-commit hooks
4. Review security requirements

### Before Committing

1. Run tests: `make test`
2. Run linters: `make lint`
3. Check for secrets: `git secrets --scan`
4. Review diff for debug code

### Before Deploying

1. All tests passing
2. Security scan clean
3. Documentation updated
4. Changelog updated
5. Version bumped

---

## Appendix: Tool Installation

```bash
# Go tools
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Node.js tools
npm install -g eslint prettier

# Python tools
pip install black ruff mypy pip-audit

# Git hooks
pip install pre-commit
pre-commit install
```
