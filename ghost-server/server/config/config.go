// Package config loads ghost-server configuration from an optional JSON file
// and GHOST_* environment variables (environment wins over the file, the file
// wins over defaults).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// AccessMode selects how the server decides who may register, pair, join and
// connect.
type AccessMode string

const (
	// AccessOpen allows every request from an authenticated device.
	AccessOpen AccessMode = "open"
	// AccessAPI asks an external authorizer webhook before each action.
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
	// Listen is the HTTP listen address for the public API, signalling and
	// admin API (e.g. ":8080").
	Listen string `json:"listen"`
	// MetricsListen, when set, serves /metrics on a separate listener instead
	// of the main one.
	MetricsListen string `json:"metrics_listen"`
	// OTLP enables the OTLP exporters (configured by OTEL_EXPORTER_OTLP_*).
	OTLP bool `json:"otlp"`
	// LogLevel is debug, info, warn or error.
	LogLevel string `json:"log_level"`

	Database  Database  `json:"database"`
	Access    Access    `json:"access"`
	Admin     Admin     `json:"admin"`
	ICE       ICE       `json:"ice"`
	Heartbeat Heartbeat `json:"heartbeat"`
	Pairing   Pairing   `json:"pairing"`

	// DefaultPool is the address pool for networks created without one.
	DefaultPool string `json:"default_pool"`
	// Networks are created at startup if missing.
	Networks []Network `json:"networks"`
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

// Admin configures the admin API.
type Admin struct {
	// Token is the bearer service token. The admin API is disabled when empty.
	Token string `json:"token"`
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

// Pairing configures pairing codes.
type Pairing struct {
	// TTL is the default lifetime of a pairing code.
	TTL Duration `json:"ttl"`
}

// Network is a network to create at startup.
type Network struct {
	Name string `json:"name"`
	Pool string `json:"pool"`
}

// Default returns the default configuration: SQLite in the working
// directory, open access, and no admin API.
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
		Pairing:     Pairing{TTL: Duration(10 * time.Minute)},
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
	str("GHOST_ADMIN_TOKEN", &c.Admin.Token)
	list("GHOST_STUN_URLS", &c.ICE.STUNURLs)
	list("GHOST_TURN_URLS", &c.ICE.TURNURLs)
	str("GHOST_TURN_SECRET", &c.ICE.TURNSecret)
	dur("GHOST_TURN_TTL", &c.ICE.TURNTTL)
	dur("GHOST_HEARTBEAT_INTERVAL", &c.Heartbeat.Interval)
	dur("GHOST_HEARTBEAT_TIMEOUT", &c.Heartbeat.Timeout)
	dur("GHOST_PAIRING_TTL", &c.Pairing.TTL)
	str("GHOST_DEFAULT_POOL", &c.DefaultPool)
	if v := getenv("GHOST_NETWORKS"); v != "" {
		// "name" or "name=pool", comma separated.
		c.Networks = nil
		for _, item := range splitList(v) {
			name, pool, _ := strings.Cut(item, "=")
			c.Networks = append(c.Networks, Network{Name: strings.TrimSpace(name), Pool: strings.TrimSpace(pool)})
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
	if c.Admin.Token != "" && len(c.Admin.Token) < 16 {
		errs = append(errs, errors.New("admin.token must be at least 16 bytes"))
	}
	if len(c.ICE.TURNURLs) > 0 && c.ICE.TURNSecret == "" {
		errs = append(errs, errors.New("ice.turn_secret is required when ice.turn_urls is set"))
	}
	if c.Heartbeat.Interval <= 0 || c.Heartbeat.Timeout <= c.Heartbeat.Interval {
		errs = append(errs, errors.New("heartbeat.timeout must exceed a positive heartbeat.interval"))
	}
	if c.Pairing.TTL <= 0 {
		errs = append(errs, errors.New("pairing.ttl must be positive"))
	}
	for _, n := range c.Networks {
		if n.Name == "" {
			errs = append(errs, errors.New("networks: empty name"))
		}
	}
	return errors.Join(errs...)
}
