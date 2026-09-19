package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Defaults shared by the commands.
const (
	defaultKeys  = "ghost-keys.json"
	defaultCreds = "ghost-creds.json"
)

// credentials are a peer's control-plane credentials: the body ghost-server
// returns from POST /v1/enroll and POST /control/networks/{net}/peers, plus
// the server they came from.
type credentials struct {
	PeerID    string       `json:"peer_id"`
	PeerToken string       `json:"peer_token"`
	Network   string       `json:"network"`
	Roles     []proto.Role `json:"roles,omitempty"`
	Tags      []string     `json:"tags,omitempty"`
	Ephemeral bool         `json:"ephemeral,omitempty"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
	// Server is the control plane's base URL. ghost-cli enroll records it;
	// the server does not return it.
	Server string `json:"server,omitempty"`
}

func loadCredentials(path string) (credentials, error) {
	var c credentials
	b, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read credentials: %w", err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse credentials %s: %w", path, err)
	}
	if c.PeerToken == "" {
		return c, fmt.Errorf("credentials %s have no peer_token", path)
	}
	return c, nil
}

func saveCredentials(path string, c credentials) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create credentials directory: %w", err)
		}
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// signalURL turns a control-plane base URL into its signalling WebSocket URL:
// http(s)://host[:port][/prefix] becomes ws(s)://host[:port][/prefix]/v1/signal.
// A ws(s) URL with a path is taken as the signalling URL itself.
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

// enrollRequest is the body of POST /v1/enroll.
type enrollRequest struct {
	AuthKey   string            `json:"auth_key"`
	Name      string            `json:"name,omitempty"`
	PublicKey string            `json:"public_key,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

func runEnroll(ctx context.Context, args []string, e env) error {
	fs := newFlagSet("enroll", e)
	server := fs.String("server", envOr("GHOST_SERVER", ""), "control plane base URL, e.g. http://localhost:8080 ($GHOST_SERVER)")
	authKey := fs.String("auth-key", envOr("GHOST_AUTH_KEY", ""), "pre-auth key (gak_…) ($GHOST_AUTH_KEY)")
	name := fs.String("name", envOr("GHOST_NAME", ""), "peer name ($GHOST_NAME)")
	keys := fs.String("keys", envOr("GHOST_KEYS", defaultKeys), "WireGuard key file, created if missing ($GHOST_KEYS)")
	credsPath := fs.String("creds", envOr("GHOST_CREDS", defaultCreds), "where to write the credentials ($GHOST_CREDS)")
	force := fs.Bool("force", false, "overwrite existing credentials")
	var labels listFlag
	fs.Var(&labels, "label", "peer label key=value (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ghost-cli enroll -server URL -auth-key KEY [flags]")
		fmt.Fprintln(fs.Output(), "Enrols this machine as a peer and writes its credentials. The WireGuard public key is registered at enrolment.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *authKey == "" {
		fs.Usage()
		return errors.New("enroll needs -server and -auth-key")
	}
	if _, err := os.Stat(*credsPath); err == nil && !*force {
		return fmt.Errorf("%s already exists (use -force to enrol again)", *credsPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	req := enrollRequest{AuthKey: *authKey, Name: *name}
	if len(labels) > 0 {
		req.Labels = map[string]string{}
		for _, l := range labels {
			k, v, ok := strings.Cut(l, "=")
			if !ok || k == "" {
				return fmt.Errorf("label %q: want key=value", l)
			}
			req.Labels[k] = v
		}
	}
	k, err := ghost.LoadOrCreateKeys(*keys)
	if err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	req.PublicKey = k.PublicKey()

	creds, err := enroll(ctx, *server, req)
	if err != nil {
		return err
	}
	creds.Server = strings.TrimSuffix(*server, "/")
	if err := saveCredentials(*credsPath, creds); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "enrolled %s in %s (roles %v); credentials in %s\n", creds.PeerID, creds.Network, creds.Roles, *credsPath)
	return nil
}

// enroll calls POST {server}/v1/enroll.
func enroll(ctx context.Context, server string, req enrollRequest) (credentials, error) {
	var creds credentials
	body, err := json.Marshal(req)
	if err != nil {
		return creds, err
	}
	endpoint, err := url.JoinPath(server, "/v1/enroll")
	if err != nil {
		return creds, fmt.Errorf("server URL %q: %w", server, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return creds, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(hreq)
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &apiErr) == nil && apiErr.Error != "" {
			return creds, fmt.Errorf("enroll: %s: %s", resp.Status, apiErr.Error)
		}
		return creds, fmt.Errorf("enroll: %s", resp.Status)
	}
	if err := json.Unmarshal(b, &creds); err != nil {
		return creds, fmt.Errorf("enroll: decode response: %w", err)
	}
	if creds.PeerToken == "" {
		return creds, errors.New("enroll: response carries no peer_token")
	}
	return creds, nil
}
