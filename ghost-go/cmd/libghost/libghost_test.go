package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// The tests drive the Go implementation behind the C entry points, in
// process: the wrappers in export.go do nothing but convert strings, and
// building a DLL to test them would need a C toolchain on every platform CI
// runs on. Everything runs against signal.NewFakeServer on loopback with
// loopback-only ICE, so no packet leaves the machine.

// decode unmarshals a JSON document a call returned.
func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

// startNode starts a node against fake and stops it when the test ends.
func startNode(t *testing.T, fake *signal.FakeServer, cfg string) int64 {
	t.Helper()
	h, err := apiStart(cfg, &testHooks{dialer: fake.Dialer(), loopbackICE: true})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = apiStop(h) })
	return h
}

// waitEvent reads events until one of kind arrives, or the test times out.
func waitEvent(t *testing.T, h int64, kind string) eventJSON {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		s, ok := apiNextEvent(h, 500)
		if !ok {
			continue
		}
		ev := decode[eventJSON](t, s)
		if ev.Kind == kind {
			return ev
		}
	}
	t.Fatalf("no %q event", kind)
	return eventJSON{}
}

// TestNodeLifecycle drives the whole exported surface against an in-memory
// control plane: start a node with an exit, read its status and events, set a
// policy, read metrics, and stop.
func TestNodeLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel test")
	}
	if got := apiVersion(); got != Version {
		t.Errorf("version=%q want %q", got, Version)
	}

	fake := signal.NewFakeServer("100.64.0.0/10")
	fake.AddPeer("exit-tok", signal.FakePeer{ID: "exit-1", Name: "exit", Roles: []proto.Role{proto.RoleExit, proto.RoleNode}})
	fake.AddPeer("hub-tok", signal.FakePeer{ID: "hub-1", Name: "hub", Roles: []proto.Role{proto.RoleHub}})
	fake.SetPolicy(proto.ExitPolicy{Network: "lab", Allow: []string{"a.example:443"}, DailyBytes: 4096, Revision: 7})

	dir := t.TempDir()
	cfg := func(token, keys string, exitOn bool) string {
		b, err := json.Marshal(startConfig{
			Creds:            credsConfig{Server: "http://fake", PeerToken: token, Network: "lab"},
			KeyStorePath:     filepath.Join(dir, keys),
			LogLevel:         "error",
			ConnectTimeoutMS: 20000,
			Exit: &exitConfig{
				Enabled: exitOn, Port: exit.DefaultPort, Allow: []string{"a.example:443", "b.example:443"},
			},
			Metrics: &metricsConfig{Enabled: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	node := startNode(t, fake, cfg("exit-tok", "node.json", true))
	if node <= 0 {
		t.Fatalf("handle %d", node)
	}

	// The node joins, is given a tunnel address, and serves its exit there.
	joined := waitEvent(t, node, "joined")
	if !strings.HasPrefix(joined.Address, "100.64.") {
		t.Errorf("joined address %q", joined.Address)
	}
	if joined.Seq == 0 || joined.Time.IsZero() {
		t.Errorf("joined event %+v", joined)
	}

	st := decode[statusView](t, apiStatus(node))
	if !st.OK || st.Handle != node || st.PeerID != "exit-1" || st.Network != "lab" {
		t.Fatalf("status %+v", st)
	}
	if st.Address != joined.Address || st.TunnelAddress+"/32" != st.Address {
		t.Errorf("status address %q / %q", st.Address, st.TunnelAddress)
	}
	if !st.Connected || !st.Joined || st.SignalState != "connected" {
		t.Errorf("status signalling %v joined %v %q", st.Connected, st.Joined, st.SignalState)
	}
	if st.Exit == nil || !st.Exit.Enabled {
		t.Fatalf("status exit %+v", st.Exit)
	}
	if want := st.TunnelAddress + fmt.Sprintf(":%d", exit.DefaultPort); st.Exit.Listen != want {
		t.Errorf("exit listen %q want %q", st.Exit.Listen, want)
	}
	// The control plane's allowlist and caps arrived with the netmap, and the
	// local list narrows them.
	if len(st.Exit.Allow) != 1 || st.Exit.Allow[0] != "a.example:443" {
		t.Errorf("server allow %v", st.Exit.Allow)
	}
	if len(st.Exit.LocalAllow) != 2 {
		t.Errorf("local allow %v", st.Exit.LocalAllow)
	}
	if st.Exit.Effective.DailyBytes != 4096 || st.Exit.Effective.Paused {
		t.Errorf("effective policy %+v", st.Exit.Effective)
	}

	// A hub joins: the node sees the netmap change and links to it.
	hub := startNode(t, fake, cfg("hub-tok", "hub.json", false))
	hubJoined := waitEvent(t, hub, "joined")
	connected := waitEvent(t, node, "peer_connected")
	if connected.PeerID != "hub-1" {
		t.Errorf("peer_connected from %q", connected.PeerID)
	}
	if connected.CandidateType != "host" {
		t.Errorf("candidate type %q, want host (loopback ICE)", connected.CandidateType)
	}

	st = decode[statusView](t, apiStatus(node))
	if st.PeerCount != 1 || st.NetmapPeers != 1 || len(st.Peers) != 1 {
		t.Fatalf("status peers %+v", st)
	}
	p := st.Peers[0]
	if p.PeerID != "hub-1" || p.Name != "hub" || !p.Linked || p.CandidateType != "host" {
		t.Errorf("peer %+v", p)
	}
	if p.Address != hubJoined.Address {
		t.Errorf("peer address %q want %q", p.Address, hubJoined.Address)
	}

	// The host pauses the exit and narrows the allowlist.
	if err := apiSetPolicy(node, `{"paused":true,"allow":["a.example:443"],"bytes_per_second":1024}`); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	st = decode[statusView](t, apiStatus(node))
	if !st.Exit.Local.Paused || !st.Exit.Effective.Paused {
		t.Errorf("after pause %+v", st.Exit)
	}
	if st.Exit.Effective.BytesPerSecond != 1024 || len(st.Exit.LocalAllow) != 1 {
		t.Errorf("after policy %+v", st.Exit)
	}
	if err := apiSetPolicy(node, `{"paused":false}`); err != nil {
		t.Fatalf("resume: %v", err)
	}
	st = decode[statusView](t, apiStatus(node))
	if st.Exit.Effective.Paused || st.Exit.Effective.BytesPerSecond != 1024 {
		t.Errorf("resume left %+v", st.Exit)
	}

	// Metrics report the node's identity, the live tunnel and the exit state.
	mv := decode[metricsView](t, apiMetrics(node))
	if !mv.OK || mv.Snapshot.PeerID != "exit-1" || mv.Snapshot.Address != st.Address {
		t.Fatalf("metrics %+v", mv.Snapshot)
	}
	if len(mv.Snapshot.Tunnel) != 1 || mv.Snapshot.Tunnel[0].PeerID != "hub-1" {
		t.Errorf("metrics tunnel %+v", mv.Snapshot.Tunnel)
	}
	if mv.Snapshot.Totals.CapLimitBytes != 4096 || mv.Connections == nil {
		t.Errorf("metrics totals %+v", mv.Snapshot.Totals)
	}

	// A reader waiting on events is released by the stop, with a final
	// "stopped" event, and the handle is gone afterwards.
	for {
		if _, ok := apiNextEvent(node, 200); !ok {
			break // the buffer is empty; the reader below will block
		}
	}
	kinds := make(chan string, 8)
	go func() {
		defer close(kinds)
		for {
			s, ok := apiNextEvent(node, 10000)
			if !ok {
				return
			}
			var ev eventJSON
			if err := json.Unmarshal([]byte(s), &ev); err != nil {
				return
			}
			kinds <- ev.Kind
		}
	}()
	time.Sleep(50 * time.Millisecond) // let the reader block in next
	if err := apiStop(node); err != nil {
		t.Fatalf("stop: %v", err)
	}
	last := ""
	for k := range kinds {
		last = k
	}
	if last != kindStopped {
		t.Errorf("last event %q want %q", last, kindStopped)
	}
	if s := apiStatus(node); !strings.Contains(s, "no such ghost handle") {
		t.Errorf("status after stop: %s", s)
	}
	if err := apiStop(node); err == nil {
		t.Error("second stop succeeded")
	}
}

// TestStartRejectsBadConfig checks the config validation a host application
// meets first.
func TestStartRejectsBadConfig(t *testing.T) {
	tests := []struct{ name, cfg, want string }{
		{"not json", `{`, "config:"},
		{"unknown field", `{"creds":{"peer_token":"t","server":"http://x","network":"n"},"keystore":"x"}`, "unknown field"},
		{"no token", `{"creds":{"server":"http://x","network":"n"}}`, "peer_token"},
		{"no server", `{"creds":{"peer_token":"t","network":"n"}}`, "creds.server"},
		{"no network", `{"creds":{"peer_token":"t","server":"http://x"}}`, "no network"},
		{"bad scheme", `{"creds":{"peer_token":"t","server":"ftp://x","network":"n"}}`, "scheme must be"},
		{"bad role", `{"creds":{"peer_token":"t","server":"http://x","network":"n"},"roles":["boss"]}`, "unknown role"},
		{"bad exit port", `{"creds":{"peer_token":"t","server":"http://x","network":"n"},"exit":{"enabled":true,"port":70000}}`, "out of range"},
		{"bad log level", `{"creds":{"peer_token":"t","server":"http://x","network":"n"},"log_level":"loud"}`, "log level"},
		{"turn without urls", `{"creds":{"peer_token":"t","server":"http://x","network":"n"},"turn":[{"username":"u"}]}`, "no urls"},
		{"trailing data", `{"creds":{"peer_token":"t","server":"http://x","network":"n"}} {}`, "trailing data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := apiStart(tt.cfg, nil)
			if h != 0 {
				_ = apiStop(h)
				t.Fatalf("start accepted %s", tt.cfg)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
		})
	}
}

// TestSignalURL covers the control-plane URL a start config is given.
func TestSignalURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://localhost:8080", "ws://localhost:8080/v1/signal"},
		{"https://ghost.example.com/", "wss://ghost.example.com/v1/signal"},
		{"https://example.com/ghost", "wss://example.com/ghost/v1/signal"},
		{"ws://localhost:8080", "ws://localhost:8080/v1/signal"},
		{"wss://example.com/custom/signal", "wss://example.com/custom/signal"},
		{"ftp://example.com", ""},
		{"localhost:8080", ""},
	}
	for _, tt := range tests {
		got, err := signalURL(tt.in)
		if tt.want == "" {
			if err == nil {
				t.Errorf("signalURL(%q)=%q, want an error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("signalURL(%q)=%q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

// TestUnknownHandle checks that every call is safe on a handle that was never
// issued.
func TestUnknownHandle(t *testing.T) {
	const h = 999999
	for name, got := range map[string]string{"status": apiStatus(h), "metrics": apiMetrics(h)} {
		var e errorResponse
		if err := json.Unmarshal([]byte(got), &e); err != nil || e.OK || e.Error == "" {
			t.Errorf("%s(%d)=%s", name, h, got)
		}
	}
	if _, ok := apiNextEvent(h, 0); ok {
		t.Error("next event on an unknown handle")
	}
	if err := apiSetPolicy(h, `{}`); err == nil {
		t.Error("set policy on an unknown handle")
	}
	if err := apiStop(h); err == nil {
		t.Error("stop on an unknown handle")
	}
}

// TestEventBufferDropsOldest checks that a slow reader never blocks the node:
// the buffer keeps the newest events and says how many it dropped.
func TestEventBufferDropsOldest(t *testing.T) {
	b := newEventBuffer(4)
	for i := 1; i <= 10; i++ {
		b.push(eventJSON{Kind: "netmap", PeerID: fmt.Sprint(i)})
	}
	first, ok := b.next(0)
	if !ok {
		t.Fatal("no event")
	}
	if first.PeerID != "7" || first.Seq != 7 {
		t.Errorf("first kept event %+v, want the 7th", first)
	}
	if first.Dropped != 6 {
		t.Errorf("dropped=%d want 6", first.Dropped)
	}
	next, ok := b.next(0)
	if !ok || next.PeerID != "8" || next.Dropped != 0 {
		t.Errorf("second event %+v", next)
	}
	// Three are left; then the buffer reports no more.
	for range 2 {
		if _, ok := b.next(0); !ok {
			t.Fatal("missing buffered event")
		}
	}
	if _, ok := b.next(0); ok {
		t.Error("buffer was not empty")
	}
}

// TestEventBufferWaits checks the timeout and the close path.
func TestEventBufferWaits(t *testing.T) {
	b := newEventBuffer(2)
	start := time.Now()
	if _, ok := b.next(50 * time.Millisecond); ok {
		t.Error("an empty buffer returned an event")
	}
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Errorf("next returned after %v, want a ~50ms wait", d)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		b.push(eventJSON{Kind: "joined"})
	}()
	ev, ok := b.next(5 * time.Second)
	if !ok || ev.Kind != "joined" {
		t.Fatalf("waited event %+v %v", ev, ok)
	}
	// A closed buffer still yields what it holds, then reports no more.
	b.push(eventJSON{Kind: kindStopped})
	b.close()
	if ev, ok := b.next(time.Second); !ok || ev.Kind != kindStopped {
		t.Errorf("event after close %+v %v", ev, ok)
	}
	if _, ok := b.next(time.Second); ok {
		t.Error("a drained closed buffer returned an event")
	}
	// push after close is a no-op rather than a panic.
	b.push(eventJSON{Kind: "netmap"})
}

// TestMinCap covers combining two limits in which 0 means unlimited.
func TestMinCap(t *testing.T) {
	tests := []struct{ a, b, want int64 }{
		{0, 0, 0}, {0, 5, 5}, {5, 0, 5}, {5, 9, 5}, {9, 5, 5}, {-1, 7, 7}, {7, -1, 7},
	}
	for _, tt := range tests {
		if got := minCap(tt.a, tt.b); got != tt.want {
			t.Errorf("minCap(%d,%d)=%d want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestEnroll drives ghost_enroll against a stub control plane, including the
// key the peer registers.
func TestEnroll(t *testing.T) {
	var got enrollBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/enroll" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got.AuthKey != "gak_good" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid auth key"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"peer_id":"peer_1","peer_token":"gpt_x","network":"lab","roles":["exit","node"]}`)
	}))
	defer srv.Close()

	keys := filepath.Join(t.TempDir(), "keys.json")
	req, err := json.Marshal(enrollRequestJSON{
		Server: srv.URL, AuthKey: "gak_good", Name: "exit-1",
		Labels: map[string]string{"geo": "ca-on"}, KeyStorePath: keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := decode[enrollResponse](t, apiEnroll(string(req)))
	if !res.OK || res.Creds == nil {
		t.Fatalf("enroll: %+v", res)
	}
	if res.Creds.PeerToken != "gpt_x" || res.Creds.Network != "lab" || res.Creds.Server != srv.URL {
		t.Errorf("creds %+v", res.Creds)
	}
	if got.Name != "exit-1" || got.Labels["geo"] != "ca-on" || got.PublicKey == "" {
		t.Errorf("request %+v", got)
	}
	if res.Creds.PublicKey != got.PublicKey {
		t.Errorf("creds public key %q want %q", res.Creds.PublicKey, got.PublicKey)
	}
	// The creds feed a start config unchanged.
	if _, _, err := (&startConfig{Creds: credsConfig{
		Server: res.Creds.Server, PeerID: res.Creds.PeerID, PeerToken: res.Creds.PeerToken, Network: res.Creds.Network,
	}}).parse(); err != nil {
		t.Errorf("creds do not make a start config: %v", err)
	}

	// A refused key, a missing field and a malformed body all report.
	bad := strings.Replace(string(req), "gak_good", "gak_bad", 1)
	if res := decode[enrollResponse](t, apiEnroll(bad)); res.OK || !strings.Contains(res.Error, "invalid auth key") {
		t.Errorf("bad key: %+v", res)
	}
	if res := decode[enrollResponse](t, apiEnroll(`{"server":"`+srv.URL+`"}`)); res.OK || !strings.Contains(res.Error, "auth_key") {
		t.Errorf("no auth key: %+v", res)
	}
	if res := decode[enrollResponse](t, apiEnroll(`{`)); res.OK || res.Error == "" {
		t.Errorf("malformed: %+v", res)
	}
}
