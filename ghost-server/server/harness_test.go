package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server"
	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

const adminToken = "test-admin-token-0123456789"

// clock is a settable test clock.
type clock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

// harness runs a real ghost-server on a 127.0.0.1:0 listener with a
// per-test SQLite database.
type harness struct {
	t     *testing.T
	srv   *server.Server
	st    *store.SQL
	base  string // http://127.0.0.1:port
	ws    string // ws://127.0.0.1:port/v1/signal
	clock *clock
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Admin.Token = adminToken
	cfg.Networks = []config.Network{{Name: "testnet"}, {Name: "othernet", Pool: "100.96.0.0/24"}}
	cfg.Heartbeat.Interval = config.Duration(time.Second)
	cfg.Heartbeat.Timeout = config.Duration(5 * time.Second)
	return cfg
}

func newHarness(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	cfg := testConfig()
	cfg.Database.DSN = filepath.Join(t.TempDir(), "ghost.db")
	if mutate != nil {
		mutate(&cfg)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite", cfg.Database.DSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	clk := &clock{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(ctx, server.Options{Config: cfg, Store: st, Logger: log, Now: clk.Now})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() {
		_ = hs.Close()
		srv.Close()
		_ = st.Close()
	})
	addr := ln.Addr().String()
	return &harness{t: t, srv: srv, st: st, base: "http://" + addr, ws: "ws://" + addr + "/v1/signal", clock: clk}
}

// do sends a JSON request and decodes a JSON response into out (if non-nil).
// It returns the status code.
func (h *harness) do(method, path, token string, body, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.base+path, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
			h.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// register self-registers a device and fails the test on error.
func (h *harness) register(network string, role proto.Role, labels map[string]string) control.Credentials {
	h.t.Helper()
	var creds control.Credentials
	status := h.do("POST", "/v1/register", "", control.RegisterInput{Network: network, Role: role, Name: string(role), Labels: labels}, &creds)
	if status != http.StatusCreated {
		h.t.Fatalf("register %s in %s: status %d", role, network, status)
	}
	return creds
}

func randomKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// sigClient is a raw signalling client for protocol-level assertions.
type sigClient struct {
	t *testing.T
	c *signal.Client
}

func (h *harness) dial(creds control.Credentials) *sigClient {
	h.t.Helper()
	c := signal.New(signal.Config{
		URL:         h.ws,
		DeviceToken: creds.DeviceToken,
		PublicKey:   randomKey(h.t),
		Role:        creds.Role,
		MinBackoff:  50 * time.Millisecond,
		MaxBackoff:  200 * time.Millisecond,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, signal.Handlers{})
	c.Start(context.Background())
	h.t.Cleanup(func() { _ = c.Close() })
	return &sigClient{t: h.t, c: c}
}

// next waits for the first event matching pred.
func (s *sigClient) next(what string, timeout time.Duration, pred func(signal.Event) bool) signal.Event {
	s.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-s.c.Events():
			if pred(ev) {
				return ev
			}
		case <-deadline:
			s.t.Fatalf("timed out waiting for %s", what)
			return signal.Event{}
		}
	}
}

func (s *sigClient) welcome() proto.Welcome {
	s.t.Helper()
	return *s.next("welcome", 5*time.Second, func(e signal.Event) bool { return e.Welcome != nil }).Welcome
}

func (s *sigClient) join(network string) proto.Joined {
	s.t.Helper()
	s.welcome()
	if err := s.c.Join(context.Background(), network); err != nil {
		s.t.Fatalf("join: %v", err)
	}
	return *s.next("joined", 5*time.Second, func(e signal.Event) bool { return e.Joined != nil }).Joined
}

func (s *sigClient) error(code string) proto.Error {
	s.t.Helper()
	return *s.next("error "+code, 5*time.Second, func(e signal.Event) bool { return e.Err != nil && e.Err.Code == code }).Err
}
