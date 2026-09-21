// Package config loads ghost-server configuration from an optional JSON file
// and GHOST_* environment variables (environment wins over the file, the file
// wins over defaults).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// AccessMode selects whether an external authorizer takes part in enrolment,
// connection and peer-to-peer decisions.
type AccessMode string

const (
	// AccessOpen relies on credentials and ACLs alone.
	AccessOpen AccessMode = "open"
	// AccessAPI also asks an external authorizer webhook, which can deny and
	// can contribute per-peer policy.
	AccessAPI AccessMode = "api"
)

// Duration is a time.Duration that reads from JSON as a Go duration string
// ("30s") or as whole seconds.
type Duration time.Duration

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration: want a string like \"30s\" or seconds: %w", err)
	}
	*d = Duration(time.Duration(n) * time.Second)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the full server configuration.
type Config struct {
	// Listen is the HTTP listen address for the peer API, signalling and the
	// control API (e.g. ":8080").
	Listen string `json:"listen"`
	// MetricsListen, when set, serves /metrics on a separate listener instead
	// of the main one.
	MetricsListen string `json:"metrics_listen"`
	// OTLP enables the OTLP exporters (configured by OTEL_EXPORTER_OTLP_*).
	OTLP bool `json:"otlp"`
	// LogLevel is debug, info, warn or error.
	LogLevel string `json:"log_level"`

	Database   Database   `json:"database"`
	Access     Access     `json:"access"`
	Control    Control    `json:"control"`
	ICE        ICE        `json:"ice"`
	Heartbeat  Heartbeat  `json:"heartbeat"`
	Enrollment Enrollment `json:"enrollment"`
	Peers      Peers      `json:"peers"`

	// DefaultPool is the address pool for networks created without one.
	DefaultPool string `json:"default_pool"`
	// Networks are created at startup if missing.
	Networks []Network `json:"networks"`
	// TrustedProxies are the addresses or CIDR prefixes of the reverse proxies
	// in front of the server. A peer's client address is read from
	// X-Forwarded-For only when its connection arrives through one of them.
	TrustedProxies []string `json:"trusted_proxies"`
}

// Database selects the storage backend.
type Database struct {
	// Driver is "sqlite" or "postgres".
	Driver string `json:"driver"`
	// DSN is the driver's data source name (a file path for sqlite, a
	// postgres:// URL for postgres).
	DSN string `json:"dsn"`
}

// Access configures access control.
type Access struct {
	Mode AccessMode `json:"mode"`
	// AuthorizerURL is the base URL; requests go to {url}/ghost/authorize.
	AuthorizerURL string `json:"authorizer_url"`
	// AuthorizerSecret is the HMAC-SHA256 key shared with the authorizer.
	AuthorizerSecret string `json:"authorizer_secret"`
	// Timeout bounds one authorizer call.
	Timeout Duration `json:"timeout"`
	// CacheTTL is how long a decision is reused (0 disables caching).
	CacheTTL Duration `json:"cache_ttl"`
}

// Control configures the control API.
type Control struct {
	// ServiceToken is the bearer service token with every scope. Scoped API
	// keys are created through the control API with it. The control API is
	// disabled when empty.
	ServiceToken string `json:"service_token"`
}

// ICE configures the STUN/TURN servers advertised to clients.
type ICE struct {
	STUNURLs []string `json:"stun_urls"`
	TURNURLs []string `json:"turn_urls"`
	// TURNSecret is coturn's static-auth-secret.
	TURNSecret string `json:"turn_secret"`
	// TURNTTL is the lifetime of issued TURN credentials.
	TURNTTL Duration `json:"turn_ttl"`
}

// Heartbeat configures session liveness.
type Heartbeat struct {
	// Interval is advertised to clients in the welcome message.
	Interval Duration `json:"interval"`
	// Timeout closes a session that sent nothing for this long.
	Timeout Duration `json:"timeout"`
}

// Enrollment configures interactive enrolment codes.
type Enrollment struct {
	// CodeTTL is how long an interactive enrolment code stays usable.
	CodeTTL Duration `json:"code_ttl"`
	// PollInterval is the minimum poll interval advertised to peers.
	PollInterval Duration `json:"poll_interval"`
}

// Peers configures peer lifecycle housekeeping.
type Peers struct {
	// EphemeralGrace is how long an ephemeral peer may stay offline before it
	// is deleted.
	EphemeralGrace Duration `json:"ephemeral_grace"`
	// JanitorInterval is how often expiry and ephemeral cleanup run.
	JanitorInterval Duration `json:"janitor_interval"`
}

// Network is a network to create at startup.
type Network struct {
	Name string `json:"name"`
	Pool string `json:"pool"`
	// Isolation is "none" (default) or "hub-only".
	Isolation string `json:"isolation"`
}

// Default returns the default configuration: SQLite in the working
// directory, open access, and no control API.
func Default() Config {
	return Config{
		Listen:   ":8080",
		LogLevel: "info",
		Database: Database{Driver: "sqlite", DSN: "ghost-server.db"},
		Access: Access{
			Mode:     AccessOpen,
			Timeout:  Duration(3 * time.Second),
			CacheTTL: Duration(30 * time.Second),
		},
		ICE:         ICE{TURNTTL: Duration(time.Hour)},
		Heartbeat:   Heartbeat{Interval: Duration(20 * time.Second), Timeout: Duration(60 * time.Second)},
		Enrollment:  Enrollment{CodeTTL: Duration(10 * time.Minute), PollInterval: Duration(2 * time.Second)},
		Peers:       Peers{EphemeralGrace: Duration(5 * time.Minute), JanitorInterval: Duration(30 * time.Second)},
		DefaultPool: "100.64.0.0/10",
	}
}

// Load builds a Config from defaults, then the JSON file at path (skipped
// when path is empty), then environment variables read through getenv
// (os.Getenv when nil). The result is validated.
func Load(path string, getenv func(string) string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("config: %w", err)
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("config %s: %w", path, err)
		}
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if err := applyEnv(&cfg, getenv); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func applyEnv(c *Config, getenv func(string) string) error {
	var errs []error
	str := func(key string, dst *string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	list := func(key string, dst *[]string) {
		if v := getenv(key); v != "" {
			*dst = splitList(v)
		}
	}
	dur := func(key string, dst *Duration) {
		if v := getenv(key); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
				return
			}
			*dst = Duration(d)
		}
	}
	boolean := func(key string, dst *bool) {
		if v := getenv(key); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
				return
			}
			*dst = b
		}
	}

	str("GHOST_LISTEN", &c.Listen)
	str("GHOST_METRICS_LISTEN", &c.MetricsListen)
	boolean("GHOST_OTLP", &c.OTLP)
	str("GHOST_LOG_LEVEL", &c.LogLevel)
	str("GHOST_DB_DRIVER", &c.Database.Driver)
	str("GHOST_DB_DSN", &c.Database.DSN)
	if v := getenv("GHOST_ACCESS_MODE"); v != "" {
		c.Access.Mode = AccessMode(v)
	}
	str("GHOST_AUTHORIZER_URL", &c.Access.AuthorizerURL)
	str("GHOST_AUTHORIZER_SECRET", &c.Access.AuthorizerSecret)
	dur("GHOST_AUTHORIZER_TIMEOUT", &c.Access.Timeout)
	dur("GHOST_AUTHORIZER_CACHE_TTL", &c.Access.CacheTTL)
	str("GHOST_CONTROL_TOKEN", &c.Control.ServiceToken)
	list("GHOST_STUN_URLS", &c.ICE.STUNURLs)
	list("GHOST_TURN_URLS", &c.ICE.TURNURLs)
	list("GHOST_TRUSTED_PROXIES", &c.TrustedProxies)
	str("GHOST_TURN_SECRET", &c.ICE.TURNSecret)
	dur("GHOST_TURN_TTL", &c.ICE.TURNTTL)
	dur("GHOST_HEARTBEAT_INTERVAL", &c.Heartbeat.Interval)
	dur("GHOST_HEARTBEAT_TIMEOUT", &c.Heartbeat.Timeout)
	dur("GHOST_ENROLLMENT_CODE_TTL", &c.Enrollment.CodeTTL)
	dur("GHOST_ENROLLMENT_POLL_INTERVAL", &c.Enrollment.PollInterval)
	dur("GHOST_EPHEMERAL_GRACE", &c.Peers.EphemeralGrace)
	dur("GHOST_JANITOR_INTERVAL", &c.Peers.JanitorInterval)
	str("GHOST_DEFAULT_POOL", &c.DefaultPool)
	if v := getenv("GHOST_NETWORKS"); v != "" {
		// "name", "name=pool" or "name=pool=isolation", comma separated.
		c.Networks = nil
		for _, item := range splitList(v) {
			parts := strings.SplitN(item, "=", 3)
			n := Network{Name: strings.TrimSpace(parts[0])}
			if len(parts) > 1 {
				n.Pool = strings.TrimSpace(parts[1])
			}
			if len(parts) > 2 {
				n.Isolation = strings.TrimSpace(parts[2])
			}
			c.Networks = append(c.Networks, n)
		}
	}
	return errors.Join(errs...)
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Validate checks the configuration for contradictions.
func (c Config) Validate() error {
	var errs []error
	for _, entry := range c.TrustedProxies {
		if !validProxyEntry(entry) {
			errs = append(errs, fmt.Errorf("trusted_proxies: %q is neither an address nor a CIDR prefix", entry))
		}
	}
	switch c.Database.Driver {
	case "sqlite", "postgres":
	default:
		errs = append(errs, fmt.Errorf("database.driver must be sqlite or postgres, got %q", c.Database.Driver))
	}
	if c.Database.DSN == "" {
		errs = append(errs, errors.New("database.dsn is required"))
	}
	switch c.Access.Mode {
	case AccessOpen:
	case AccessAPI:
		if c.Access.AuthorizerURL == "" {
			errs = append(errs, errors.New("access.authorizer_url is required in api mode"))
		}
		if len(c.Access.AuthorizerSecret) < 16 {
			errs = append(errs, errors.New("access.authorizer_secret must be at least 16 bytes in api mode"))
		}
	default:
		errs = append(errs, fmt.Errorf("access.mode must be open or api, got %q", c.Access.Mode))
	}
	if c.Control.ServiceToken != "" && len(c.Control.ServiceToken) < 16 {
		errs = append(errs, errors.New("control.service_token must be at least 16 bytes"))
	}
	if len(c.ICE.TURNURLs) > 0 && c.ICE.TURNSecret == "" {
		errs = append(errs, errors.New("ice.turn_secret is required when ice.turn_urls is set"))
	}
	if c.Heartbeat.Interval <= 0 || c.Heartbeat.Timeout <= c.Heartbeat.Interval {
		errs = append(errs, errors.New("heartbeat.timeout must exceed a positive heartbeat.interval"))
	}
	if c.Enrollment.CodeTTL <= 0 || c.Enrollment.PollInterval <= 0 {
		errs = append(errs, errors.New("enrollment.code_ttl and enrollment.poll_interval must be positive"))
	}
	if c.Peers.EphemeralGrace < 0 || c.Peers.JanitorInterval <= 0 {
		errs = append(errs, errors.New("peers.ephemeral_grace must not be negative and peers.janitor_interval must be positive"))
	}
	for _, n := range c.Networks {
		if n.Name == "" {
			errs = append(errs, errors.New("networks: empty name"))
		}
	}
	return errors.Join(errs...)
}

// validProxyEntry reports whether entry is an address or a CIDR prefix.
func validProxyEntry(entry string) bool {
	entry = strings.TrimSpace(entry)
	if _, err := netip.ParsePrefix(entry); err == nil {
		return true
	}
	_, err := netip.ParseAddr(entry)
	return err == nil
}
