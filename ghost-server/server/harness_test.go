package server_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server"
	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

const serviceToken = "test-service-token-0123456789"

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
	cfg.Control.ServiceToken = serviceToken
	cfg.Networks = []config.Network{{Name: "testnet"}, {Name: "othernet", Pool: "100.96.0.0/24"}}
	cfg.Heartbeat.Interval = config.Duration(time.Second)
	cfg.Heartbeat.Timeout = config.Duration(wait(5 * time.Second))
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

// ctl calls the control API with the service token and fails the test unless
// the status is want.
func (h *harness) ctl(method, path string, body, out any, want int) {
	h.t.Helper()
	if status := h.do(method, path, serviceToken, body, out); status != want {
		h.t.Fatalf("%s %s: status %d, want %d", method, path, status, want)
	}
}

// peer creates a peer through the control API.
func (h *harness) peer(network, name string, roles []proto.Role, tags ...string) control.Credentials {
	h.t.Helper()
	var creds control.Credentials
	h.ctl("POST", "/control/networks/"+network+"/peers",
		map[string]any{"name": name, "roles": roles, "tags": tags}, &creds, http.StatusCreated)
	return creds
}

// defineTags defines tags in a network's policy.
func (h *harness) defineTags(network string, tags ...string) {
	h.t.Helper()
	for _, tag := range tags {
		h.ctl("PUT", "/control/networks/"+network+"/tags/"+tag, map[string]any{}, nil, http.StatusOK)
	}
}

// meshACL lets every peer of a network reach every other peer.
func (h *harness) meshACL(network string) {
	h.t.Helper()
	h.ctl("PUT", "/control/networks/"+network+"/acls", map[string]any{
		"acls": []map[string]any{{"action": "accept", "src": []string{"*"}, "dst": []string{"*"}}},
	}, nil, http.StatusOK)
}

func (h *harness) audit(network, action string) []store.AuditEvent {
	h.t.Helper()
	var out struct {
		Events []store.AuditEvent `json:"events"`
	}
	h.ctl("GET", "/control/audit?network="+network, nil, &out, http.StatusOK)
	var match []store.AuditEvent
	for _, e := range out.Events {
		if e.Action == action {
			match = append(match, e)
		}
	}
	return match
}

func randomKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// sigClient is a raw signalling client for protocol-level assertions. It
// tracks the netmap it has been sent and keeps every frame it received.
type sigClient struct {
	t      *testing.T
	c      *signal.Client
	id     string
	netmap *proto.Netmap
	// snapshots counts full netmaps; everything after the first is a delta.
	snapshots int
	frames    []string // JSON of every netmap, delta, signal and error received
	health    *proto.Health
	mu        sync.Mutex
}

func (h *harness) dial(creds control.Credentials) *sigClient {
	h.t.Helper()
	s := &sigClient{t: h.t, id: creds.PeerID}
	s.c = signal.New(signal.Config{
		URL:        h.ws,
		PeerToken:  creds.PeerToken,
		PublicKey:  randomKey(h.t),
		MinBackoff: 50 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: func() *proto.Health {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.health
		},
	}, signal.Handlers{})
	s.c.Start(context.Background())
	h.t.Cleanup(func() { _ = s.c.Close() })
	return s
}

// joined dials, joins network and waits for the first netmap.
func (h *harness) joined(creds control.Credentials) *sigClient {
	h.t.Helper()
	s := h.dial(creds)
	s.join(creds.Network)
	s.waitNetmap("first netmap", func(proto.Netmap) bool { return true })
	return s
}

// next waits for the first event matching pred, applying netmaps and deltas
// on the way. timeout is what the server should need; the wait is what this
// build and machine are given for it.
func (s *sigClient) next(what string, timeout time.Duration, pred func(signal.Event) bool) signal.Event {
	s.t.Helper()
	timeout = wait(timeout)
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-s.c.Events():
			s.record(ev)
			if pred(ev) {
				return ev
			}
		case <-deadline:
			s.t.Fatalf("%s: timed out waiting for %s", s.id, what)
			return signal.Event{}
		}
	}
}

func (s *sigClient) record(ev signal.Event) {
	var v any
	switch {
	case ev.Netmap != nil:
		nm := *ev.Netmap
		s.netmap = &nm
		s.snapshots++
		v = ev.Netmap
	case ev.Delta != nil:
		if s.netmap != nil {
			nm := s.netmap.Apply(*ev.Delta)
			s.netmap = &nm
		}
		v = ev.Delta
	case ev.Signal != nil:
		v = ev.Signal
	case ev.Err != nil:
		v = ev.Err
	case ev.Joined != nil:
		v = ev.Joined
	case ev.Welcome != nil:
		v = ev.Welcome
	default:
		return
	}
	b, _ := json.Marshal(v)
	s.frames = append(s.frames, string(b))
}

// waitNetmap waits until the tracked netmap satisfies pred.
func (s *sigClient) waitNetmap(what string, pred func(proto.Netmap) bool) proto.Netmap {
	s.t.Helper()
	if s.netmap != nil && pred(*s.netmap) {
		return *s.netmap
	}
	s.next(what, 5*time.Second, func(ev signal.Event) bool {
		return (ev.Netmap != nil || ev.Delta != nil) && s.netmap != nil && pred(*s.netmap)
	})
	return *s.netmap
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

func (s *sigClient) offer(to string) {
	s.t.Helper()
	if err := s.c.SendOffer(context.Background(), proto.Signal{To: to, Ufrag: "uf", Pwd: "pw"}); err != nil {
		s.t.Fatalf("offer: %v", err)
	}
}

func (s *sigClient) setHealth(h *proto.Health) {
	s.mu.Lock()
	s.health = h
	s.mu.Unlock()
}

func netmapIDs(n proto.Netmap) []string {
	out := make([]string, 0, len(n.Peers))
	for _, p := range n.Peers {
		out = append(out, p.PeerID)
	}
	return out
}

func netmapPeer(n proto.Netmap, id string) (proto.PeerInfo, bool) {
	for _, p := range n.Peers {
		if p.PeerID == id {
			return p, true
		}
	}
	return proto.PeerInfo{}, false
}

// ---- watch stream ----

// watcher reads the control API's SSE watch stream.
type watcher struct {
	t      *testing.T
	events chan events.Event
	cancel context.CancelFunc
}

func (h *harness) watch(query string) *watcher {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/control/watch?"+query, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+serviceToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		h.t.Fatalf("watch: status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	w := &watcher{t: h.t, events: make(chan events.Event, 256), cancel: cancel}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		var id, typ string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var e events.Event
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err == nil {
					if fmt.Sprint(e.Seq) != id || e.Type != typ {
						e.Type = "mismatch:" + typ
					}
					w.events <- e
				}
			}
		}
		close(w.events)
	}()
	h.t.Cleanup(cancel)
	return w
}

// next waits for an event of type typ (optionally about peer).
func (w *watcher) next(typ, peer string) events.Event {
	w.t.Helper()
	deadline := time.After(wait(5 * time.Second))
	for {
		select {
		case e, ok := <-w.events:
			if !ok {
				w.t.Fatalf("watch stream closed waiting for %s", typ)
			}
			if e.Type == typ && (peer == "" || e.PeerID == peer) {
				return e
			}
		case <-deadline:
			w.t.Fatalf("watch: timed out waiting for %s %s", typ, peer)
			return events.Event{}
		}
	}
}
