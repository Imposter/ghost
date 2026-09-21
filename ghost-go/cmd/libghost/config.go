package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Version is the libghost ABI version, returned by ghost_version. It changes
// with the C API or the JSON shapes, not with the library behind them.
const Version = "0.2.0"

// defaultLogLevel is the slog level a node logs at when the start config does
// not name one. A host application embedding the library normally wants its
// own logs, not a node's, so the default is quiet.
const defaultLogLevel = "warn"

// startConfig is the JSON body of ghost_start. Unknown fields are refused:
// a misspelt key_store_path would otherwise silently make the WireGuard key
// ephemeral.
type startConfig struct {
	// Creds are the control-plane credentials, as written by ghost_enroll.
	Creds credsConfig `json:"creds"`

	// Network overrides Creds.Network.
	Network string `json:"network,omitempty"`

	// KeyStorePath is the file the WireGuard key pair is persisted in. Empty
	// means an ephemeral key, which enrols the peer's key afresh on every
	// run; a host application should always pass a path in its own storage.
	KeyStorePath string `json:"key_store_path,omitempty"`

	// PrivateKey is the WireGuard private key (base64), for a host that keeps
	// it in its own secret store: nothing is then read from or written to
	// disk. It excludes KeyStorePath.
	PrivateKey string `json:"private_key,omitempty"`

	// CACertPEM holds PEM certificate authorities trusted, on top of the
	// system roots, for a wss:// control plane behind a private CA.
	CACertPEM string `json:"ca_cert_pem,omitempty"`

	// Roles are the roles this member asks the control plane to grant it
	// ("node", "exit", "hub", "relay"). Exit.Enabled adds "exit".
	Roles []string `json:"roles,omitempty"`

	// STUN are STUN URLs, TURN are TURN servers with credentials. Both are
	// merged with whatever the control plane advertises.
	STUN []string     `json:"stun,omitempty"`
	TURN []turnConfig `json:"turn,omitempty"`

	// PortMin and PortMax bound the local UDP port range ICE binds (0 = any).
	PortMin uint16 `json:"port_min,omitempty"`
	PortMax uint16 `json:"port_max,omitempty"`

	// MTU is the tunnel MTU (0 = ghost.DefaultMTU).
	MTU int `json:"mtu,omitempty"`

	// DNS are DNS servers for the tunnel netstack.
	DNS []string `json:"dns,omitempty"`

	// ConnectTimeoutMS bounds ICE connection establishment per peer
	// (0 = the library default).
	ConnectTimeoutMS int `json:"connect_timeout_ms,omitempty"`

	// LogLevel is "debug", "info", "warn" or "error" (default "warn"). Logs
	// go to the process's standard error.
	LogLevel string `json:"log_level,omitempty"`

	// Exit configures the SOCKS5 / HTTP-CONNECT exit on the tunnel IP.
	Exit *exitConfig `json:"exit,omitempty"`

	// Metrics configures the in-tunnel metrics endpoint. ghost_metrics_json
	// works whether or not it is enabled.
	Metrics *metricsConfig `json:"metrics,omitempty"`
}

// credsConfig is a peer's control-plane credentials: the creds object
// ghost_enroll returns, plus the server it came from.
type credsConfig struct {
	// Server is the control plane's base URL (http(s)://host[:port][/prefix]).
	Server string `json:"server,omitempty"`
	// SignalURL is the signalling WebSocket URL, when it is not the
	// conventional {server}/v1/signal.
	SignalURL string `json:"signal_url,omitempty"`
	// PeerID is this peer's id, when already known.
	PeerID string `json:"peer_id,omitempty"`
	// PeerToken authenticates the peer (gpt_…). Required.
	PeerToken string `json:"peer_token"`
	// Network is the network to join.
	Network string `json:"network,omitempty"`
}

// turnConfig is one TURN server.
type turnConfig struct {
	URLs     []string `json:"urls"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
}

// exitConfig configures a node's exit. Allow, DailyBytes, BytesPerSecond and
// Paused are the local policy: they narrow the control plane's, they never
// widen it, and ghost_set_policy replaces them later.
type exitConfig struct {
	Enabled bool `json:"enabled"`
	// Port is the tunnel-side TCP port (0 = exit.DefaultPort).
	Port int `json:"port,omitempty"`
	// Allow is the local allowlist ("host", "host:port", "*.example.com:443").
	// Absent leaves the control plane's allowlist alone; an empty array denies
	// everything.
	Allow []string `json:"allow,omitempty"`
	// DailyBytes and BytesPerSecond are local caps (0 = no local cap).
	DailyBytes     int64 `json:"daily_bytes,omitempty"`
	BytesPerSecond int64 `json:"bytes_per_second,omitempty"`
	// Paused refuses new exit connections while true.
	Paused bool `json:"paused,omitempty"`
}

func (x *exitConfig) exitPort() int {
	if x.Port > 0 {
		return x.Port
	}
	return exit.DefaultPort
}

// metricsConfig turns the in-tunnel metrics endpoint on. It listens inside the
// netstack on the node's tunnel IP, so only the netmap's hubs can reach it.
type metricsConfig struct {
	Enabled bool `json:"enabled"`
	// Port is the tunnel-side TCP port (0 = metrics.DefaultPort).
	Port int `json:"port,omitempty"`
}

// decodeJSON decodes a single strict JSON document into v.
func decodeJSON(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the JSON document")
	}
	return nil
}

// encodeJSON renders v compactly. It never fails for the types here, so a
// marshalling error is reported in band rather than swallowed.
func encodeJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return encodeError(fmt.Errorf("encode: %w", err))
	}
	return strings.TrimRight(buf.String(), "\n")
}

// errorResponse is what a call that returns JSON reports a failure with.
type errorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func encodeError(err error) string {
	b, _ := json.Marshal(errorResponse{Error: err.Error()})
	return string(b)
}

// parse validates the start config and turns it into a ghost.Config.
func (c *startConfig) parse() (ghost.Config, *slog.Logger, error) {
	var cfg ghost.Config
	if c.Creds.PeerToken == "" {
		return cfg, nil, errors.New("creds.peer_token is required")
	}
	if c.PrivateKey != "" && c.KeyStorePath != "" {
		return cfg, nil, errors.New("set private_key or key_store_path, not both")
	}
	tlsCfg, err := tlsWithCA(c.CACertPEM)
	if err != nil {
		return cfg, nil, err
	}
	u, err := c.signalURL()
	if err != nil {
		return cfg, nil, err
	}
	network := c.Network
	if network == "" {
		network = c.Creds.Network
	}
	if network == "" {
		return cfg, nil, errors.New("no network: set network or creds.network")
	}
	roles, err := parseRoles(c.Roles)
	if err != nil {
		return cfg, nil, err
	}
	if c.Exit != nil && c.Exit.Enabled {
		if p := c.Exit.exitPort(); p <= 0 || p > 65535 {
			return cfg, nil, fmt.Errorf("exit.port %d out of range", p)
		}
		if !slices.Contains(roles, proto.RoleExit) {
			roles = append(roles, proto.RoleExit)
		}
	}
	if c.Metrics != nil && c.Metrics.Enabled && (c.Metrics.Port < 0 || c.Metrics.Port > 65535) {
		return cfg, nil, fmt.Errorf("metrics.port %d out of range", c.Metrics.Port)
	}
	level := c.LogLevel
	if level == "" {
		level = defaultLogLevel
	}
	log, err := newLogger(os.Stderr, level)
	if err != nil {
		return cfg, nil, err
	}

	cfg = ghost.Config{
		SignalURL:      u,
		PeerToken:      c.Creds.PeerToken,
		PeerID:         c.Creds.PeerID,
		Network:        network,
		Roles:          roles,
		KeyStorePath:   c.KeyStorePath,
		PrivateKey:     c.PrivateKey,
		SignalTLS:      tlsCfg,
		PortMin:        c.PortMin,
		PortMax:        c.PortMax,
		MTU:            c.MTU,
		DNSServers:     slices.Clone(c.DNS),
		Logger:         log,
		ConnectTimeout: time.Duration(c.ConnectTimeoutMS) * time.Millisecond,
	}
	for _, s := range c.STUN {
		cfg.STUNServers = append(cfg.STUNServers, ghost.STUNServer{URL: s})
	}
	for _, t := range c.TURN {
		if len(t.URLs) == 0 {
			return cfg, nil, errors.New("turn entry has no urls")
		}
		cfg.TURNServers = append(cfg.TURNServers, ghost.TURNServer{
			URLs: slices.Clone(t.URLs), Username: t.Username, Password: t.Password,
		})
	}
	return cfg, log, nil
}

// tlsWithCA is a TLS config trusting the system roots plus the authorities
// in pem, or nil when pem is empty (the defaults apply).
func tlsWithCA(pem string) (*tls.Config, error) {
	if strings.TrimSpace(pem) == "" {
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		return nil, errors.New("ca_cert_pem holds no PEM certificate")
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// signalURL is the signalling WebSocket URL the config asks for.
func (c *startConfig) signalURL() (string, error) {
	if c.Creds.SignalURL != "" {
		return c.Creds.SignalURL, nil
	}
	if c.Creds.Server == "" {
		return "", errors.New("no control plane: set creds.server or creds.signal_url")
	}
	return signalURL(c.Creds.Server)
}

// signalURL turns a control-plane base URL into its signalling WebSocket URL:
// http(s)://host[:port][/prefix] becomes ws(s)://host[:port][/prefix]/v1/signal.
// A ws(s) URL that already has a path is taken as the signalling URL itself.
func signalURL(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", fmt.Errorf("server URL %q: %w", server, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("server URL %q has no host", server)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		if strings.Trim(u.Path, "/") != "" {
			return u.String(), nil
		}
	default:
		return "", fmt.Errorf("server URL %q: scheme must be http, https, ws or wss", server)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/signal"
	return u.String(), nil
}

// parseRoles parses role names, rejecting unknown ones and dropping repeats.
func parseRoles(names []string) ([]proto.Role, error) {
	out := make([]proto.Role, 0, len(names))
	for _, n := range names {
		r := proto.Role(n)
		if !r.Valid() {
			return nil, fmt.Errorf("unknown role %q (want hub, node, exit or relay)", n)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// newLogger returns a text logger on w at the named level.
func newLogger(w io.Writer, level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", level, err)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l})), nil
}
