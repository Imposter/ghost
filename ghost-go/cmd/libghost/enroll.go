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
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// enrollTimeout bounds the whole enrolment call.
const enrollTimeout = 30 * time.Second

// enrollRequestJSON is the JSON body of ghost_enroll.
type enrollRequestJSON struct {
	// Server is the control plane's base URL, e.g. "https://ghost.example.com".
	Server string `json:"server"`
	// AuthKey is a pre-auth key (gak_…).
	AuthKey string `json:"auth_key"`
	// Name is the peer's display name in netmaps.
	Name string `json:"name,omitempty"`
	// Labels are free-form key/value labels the control plane records.
	Labels map[string]string `json:"labels,omitempty"`
	// KeyStorePath is where the WireGuard key pair lives. The public key is
	// registered at enrolment, so this should be the same path the start
	// config later uses; the file is created when it is missing.
	KeyStorePath string `json:"key_store_path,omitempty"`
	// PublicKey registers a key the caller already holds, instead of
	// KeyStorePath. Leaving both empty enrols without a key.
	PublicKey string `json:"public_key,omitempty"`
	// PrivateKey registers the public half of a private key the caller
	// keeps in its own secret store; nothing is written to disk. When
	// PublicKey is also given the two must be a pair.
	PrivateKey string `json:"private_key,omitempty"`
	// CACertPEM holds PEM certificate authorities trusted, on top of the
	// system roots, for a control plane behind a private CA.
	CACertPEM string `json:"ca_cert_pem,omitempty"`
}

// enrollBody is the wire body of POST /v1/enroll.
type enrollBody struct {
	AuthKey   string            `json:"auth_key"`
	Name      string            `json:"name,omitempty"`
	PublicKey string            `json:"public_key,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// enrollCreds is what the control plane returns, plus the server it came
// from. It is exactly the creds object a start config takes.
type enrollCreds struct {
	PeerID    string       `json:"peer_id"`
	PeerToken string       `json:"peer_token"`
	Network   string       `json:"network"`
	Roles     []proto.Role `json:"roles,omitempty"`
	Tags      []string     `json:"tags,omitempty"`
	Ephemeral bool         `json:"ephemeral,omitempty"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
	// Server is the control plane's base URL. The server does not return it;
	// ghost_enroll records it so the creds are usable on their own.
	Server string `json:"server,omitempty"`
	// PublicKey is the WireGuard key registered for the peer, when one was.
	PublicKey string `json:"public_key,omitempty"`
}

// enrollResponse is the JSON ghost_enroll returns.
type enrollResponse struct {
	OK    bool         `json:"ok"`
	Creds *enrollCreds `json:"creds,omitempty"`
	Error string       `json:"error,omitempty"`
}

// apiEnroll is the implementation behind ghost_enroll. It always returns a
// JSON document: {"ok":true,"creds":{…}} or {"ok":false,"error":"…"}.
func apiEnroll(requestJSON string) string {
	var req enrollRequestJSON
	if err := decodeJSON(requestJSON, &req); err != nil {
		return encodeJSON(enrollResponse{Error: fmt.Sprintf("enroll: %v", err)})
	}
	creds, err := enroll(context.Background(), req)
	if err != nil {
		return encodeJSON(enrollResponse{Error: err.Error()})
	}
	return encodeJSON(enrollResponse{OK: true, Creds: &creds})
}

// enroll calls POST {server}/v1/enroll with the peer's public key.
func enroll(ctx context.Context, req enrollRequestJSON) (enrollCreds, error) {
	var creds enrollCreds
	if req.Server == "" {
		return creds, errors.New("enroll: server is required")
	}
	if req.AuthKey == "" {
		return creds, errors.New("enroll: auth_key is required")
	}
	body := enrollBody{AuthKey: req.AuthKey, Name: req.Name, Labels: req.Labels, PublicKey: req.PublicKey}
	if req.PrivateKey != "" {
		if req.KeyStorePath != "" {
			return creds, errors.New("enroll: set private_key or key_store_path, not both")
		}
		k, err := ghost.KeysFromPrivateKey(req.PrivateKey)
		if err != nil {
			return creds, fmt.Errorf("enroll: %w", err)
		}
		if body.PublicKey != "" && body.PublicKey != k.PublicKey() {
			return creds, errors.New("enroll: public_key is not the public half of private_key")
		}
		body.PublicKey = k.PublicKey()
	}
	if body.PublicKey == "" && req.KeyStorePath != "" {
		k, err := ghost.LoadOrCreateKeys(req.KeyStorePath)
		if err != nil {
			return creds, fmt.Errorf("enroll: keys: %w", err)
		}
		body.PublicKey = k.PublicKey()
	}

	b, err := json.Marshal(body)
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	endpoint, err := url.JoinPath(req.Server, "/v1/enroll")
	if err != nil {
		return creds, fmt.Errorf("enroll: server URL %q: %w", req.Server, err)
	}
	ctx, cancel := context.WithTimeout(ctx, enrollTimeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	tlsCfg, err := tlsWithCA(req.CACertPEM)
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	client := http.DefaultClient
	if tlsCfg != nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = tlsCfg
		client = &http.Client{Transport: tr}
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return creds, fmt.Errorf("enroll: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rb, &apiErr) == nil && apiErr.Error != "" {
			return creds, fmt.Errorf("enroll: %s: %s", resp.Status, apiErr.Error)
		}
		return creds, fmt.Errorf("enroll: %s", resp.Status)
	}
	if err := json.Unmarshal(rb, &creds); err != nil {
		return creds, fmt.Errorf("enroll: decode response: %w", err)
	}
	if creds.PeerToken == "" {
		return creds, errors.New("enroll: the response carries no peer_token")
	}
	creds.Server = strings.TrimSuffix(req.Server, "/")
	creds.PublicKey = body.PublicKey
	return creds, nil
}
