package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DialFunc dials a TCP address; ghost.Hub.DialContext has this shape, so a
// Client reaches nodes over the tunnel.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ClientConfig configures NewClient.
type ClientConfig struct {
	// Dial reaches a node's tunnel address. Required.
	Dial DialFunc
	// Port is the node's metrics port (default DefaultPort).
	Port int
	// Timeout bounds each request (default 10s).
	Timeout time.Duration
}

// Client fetches a node's metrics over the tunnel. It is safe for concurrent
// use.
type Client struct {
	http *http.Client
	port int
}

// StatusError is returned when a node answers with a non-200 status, e.g.
// 403 when the caller is not allowed to read its metrics.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("node metrics: HTTP %d: %s", e.Code, e.Body)
}

// maxBody bounds how much of a node's response the client reads.
const maxBody = 16 << 20

// NewClient creates a Client.
func NewClient(cfg ClientConfig) *Client {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Client{
		port: cfg.Port,
		http: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				DialContext:       cfg.Dial,
				Proxy:             nil,
				DisableKeepAlives: true,
			},
		},
	}
}

// Snapshot fetches GET /metrics?format=json from the node at host (its tunnel
// IP).
func (c *Client) Snapshot(ctx context.Context, host string) (Snapshot, error) {
	var s Snapshot
	err := c.getJSON(ctx, host, PathMetrics, url.Values{"format": {"json"}}, &s)
	return s, err
}

// Connections fetches GET /metrics/connections?limit=N (limit <= 0 uses the
// node's default).
func (c *Client) Connections(ctx context.Context, host string, limit int) ([]Connection, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var resp ConnectionsResponse
	if err := c.getJSON(ctx, host, PathConnections, q, &resp); err != nil {
		return nil, err
	}
	return resp.Connections, nil
}

// Prometheus fetches the node's Prometheus text exposition (GET /metrics).
func (c *Client) Prometheus(ctx context.Context, host string) ([]byte, error) {
	resp, err := c.get(ctx, host, PathMetrics, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

func (c *Client) getJSON(ctx context.Context, host, path string, q url.Values, v any) error {
	resp, err := c.get(ctx, host, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(v); err != nil {
		return fmt.Errorf("node metrics: decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, host, path string, q url.Values) (*http.Response, error) {
	u := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort(host, strconv.Itoa(c.port)),
		Path:     path,
		RawQuery: q.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &StatusError{Code: resp.StatusCode, Body: string(b)}
	}
	return resp, nil
}
