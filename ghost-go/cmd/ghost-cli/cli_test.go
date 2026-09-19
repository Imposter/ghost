package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// syncBuffer is a goroutine-safe bytes.Buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// listens collects the host addresses the members report.
type listens struct {
	mu sync.Mutex
	m  map[string]chan string
}

func (l *listens) ch(what string) chan string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]chan string{}
	}
	if l.m[what] == nil {
		l.m[what] = make(chan string, 4)
	}
	return l.m[what]
}

func (l *listens) hook(prefix string) func(what, addr string) {
	return func(what, addr string) { l.ch(prefix + what) <- addr }
}

func (l *listens) wait(t *testing.T, what string) string {
	t.Helper()
	select {
	case a := <-l.ch(what):
		return a
	case <-time.After(10 * time.Second):
		t.Fatalf("no %s listener", what)
		return ""
	}
}

// background runs a command until the test ends.
func background(t *testing.T, args []string, e env) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, args, e) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%v: %v", args, err)
			}
		case <-time.After(15 * time.Second):
			t.Errorf("%v did not stop", args)
		}
	})
}

func testTarget(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello through the exit")
	}))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "http://")
}

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

func TestParseForwards(t *testing.T) {
	got, err := parseForwards([]string{"127.0.0.1:1080=exit-1:1080", ":9000=100.64.0.2:22"})
	if err != nil || len(got) != 2 || got[0].local != "127.0.0.1:1080" || got[0].target != "exit-1:1080" || got[1].target != "100.64.0.2:22" {
		t.Fatalf("parseForwards=%+v, %v", got, err)
	}
	for _, bad := range []string{"127.0.0.1:1080", "=peer:1", "127.0.0.1:1=", "127.0.0.1:1=peer"} {
		if _, err := parseForwards([]string{bad}); err == nil {
			t.Errorf("parseForwards(%q) accepted", bad)
		}
	}
}

func TestExitPolicyNeedsEveryAllowlist(t *testing.T) {
	server, local := exit.NewAllowlist(), exit.NewAllowlist()
	server.Set([]string{"a.example:443", "b.example:443"})
	local.Set([]string{"b.example:443", "c.example:443"})
	tests := []struct {
		name string
		p    exitPolicy
		host string
		want bool
	}{
		{"neither denies", exitPolicy{}, "a.example", false},
		{"server only", exitPolicy{server: server}, "a.example", true},
		{"local only", exitPolicy{local: local}, "c.example", true},
		{"both: in both", exitPolicy{server: server, local: local}, "b.example", true},
		{"both: server only", exitPolicy{server: server, local: local}, "a.example", false},
		{"both: local only", exitPolicy{server: server, local: local}, "c.example", false},
	}
	for _, tt := range tests {
		if got := tt.p.Allow(tt.host, 443); got != tt.want {
			t.Errorf("%s: Allow(%s)=%v want %v", tt.name, tt.host, got, tt.want)
		}
	}
}

func TestEnroll(t *testing.T) {
	var got enrollRequest
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
		fmt.Fprint(w, `{"peer_id":"peer_1","peer_token":"gpt_x","network":"lab","roles":["exit","node"],"tags":["tag:exit"]}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	keys, creds := filepath.Join(dir, "keys.json"), filepath.Join(dir, "sub", "creds.json")
	var out syncBuffer
	e := env{stdin: strings.NewReader(""), stdout: &out, stderr: io.Discard}
	args := []string{"enroll", "-server", srv.URL, "-auth-key", "gak_good", "-name", "exit-1",
		"-label", "geo=ca-on", "-label", "asn=577", "-keys", keys, "-creds", creds}
	if err := run(context.Background(), args, e); err != nil {
		t.Fatal(err)
	}
	k, err := ghost.LoadKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "exit-1" || got.PublicKey != k.PublicKey() || got.Labels["geo"] != "ca-on" || got.Labels["asn"] != "577" {
		t.Errorf("request=%+v", got)
	}
	c, err := loadCredentials(creds)
	if err != nil {
		t.Fatal(err)
	}
	if c.PeerID != "peer_1" || c.PeerToken != "gpt_x" || c.Network != "lab" || c.Server != srv.URL || len(c.Roles) != 2 {
		t.Errorf("credentials=%+v", c)
	}
	if fi, err := os.Stat(creds); err == nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("credentials mode %v", fi.Mode().Perm())
	}
	if !strings.Contains(out.String(), "enrolled peer_1") {
		t.Errorf("output %q", out.String())
	}

	// Enrolling again needs -force; a refused key reports the server's error.
	if err := run(context.Background(), args, e); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second enrol: %v", err)
	}
	bad := append([]string{}, args...)
	bad[4] = "gak_bad"
	bad = append(bad, "-force")
	if err := run(context.Background(), bad, e); err == nil || !strings.Contains(err.Error(), "invalid auth key") {
		t.Errorf("bad key: %v", err)
	}
}

// TestNodeExitAndHub runs a node serving an exit and a hub through an
// in-memory control plane, on loopback: the hub fetches a page through the
// node's exit with "hub curl" and through a -forward, the node's exit records
// the source and job, and "status" shows both.
func TestNodeExitAndHub(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel test")
	}
	_, target := testTarget(t)
	fake := signal.NewFakeServer("100.64.0.0/10")
	fake.AddPeer("exit-tok", signal.FakePeer{ID: "exit-1", Name: "exit", Roles: []proto.Role{proto.RoleExit, proto.RoleNode},
		Labels: map[string]string{"geo": "ca-on"}})
	fake.AddPeer("hub-tok", signal.FakePeer{ID: "hub-1", Roles: []proto.Role{proto.RoleHub}})
	fake.AddPeer("hub-cli-tok", signal.FakePeer{ID: "hub-2", Roles: []proto.Role{proto.RoleHub}})
	fake.SetPolicy(proto.ExitPolicy{Network: "m", Allow: []string{target}, Revision: 1})

	dir := t.TempDir()
	var ls listens
	common := func(token, keys string) []string {
		return []string{"-server", "http://fake", "-token", token, "-network", "m", "-keys", filepath.Join(dir, keys),
			"-connect-timeout", "20s"}
	}
	hooks := func(prefix string) *testHooks {
		return &testHooks{dialer: fake.Dialer(), loopbackICE: true, onListen: ls.hook(prefix)}
	}
	nodeLog := &syncBuffer{}
	background(t, append(append([]string{"node"}, common("exit-tok", "node.json")...), "-exit", "-status", "127.0.0.1:0"),
		env{stdin: strings.NewReader(""), stdout: io.Discard, stderr: nodeLog, hooks: hooks("node-")})
	background(t, append(append([]string{"hub"}, common("hub-tok", "hub.json")...), "-status", "127.0.0.1:0",
		"-forward", "127.0.0.1:0=exit:"+fmt.Sprint(exit.DefaultPort)),
		env{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard, hooks: hooks("hub-")})
	nodeStatus := ls.wait(t, "node-status")
	hubStatus := ls.wait(t, "hub-status")
	fwd := ls.wait(t, "hub-forward")

	// A one-shot hub fetches through the node's exit.
	var body, errOut syncBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := run(ctx, append(append([]string{"hub"}, common("hub-cli-tok", "hub2.json")...), "-status", "",
		"curl", "-source", "cli", "-job", "j-1", "exit", "http://"+target+"/"),
		env{stdin: strings.NewReader(""), stdout: &body, stderr: &errOut, hooks: hooks("cli-")})
	if err != nil || body.String() != "hello through the exit" {
		t.Fatalf("hub curl: %v body=%q stderr=%s\nnode log:\n%s", err, body.String(), errOut.String(), nodeLog.String())
	}

	// The forward reaches the same exit: CONNECT through it by hand.
	c, err := net.DialTimeout("tcp", fwd, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	tc, err := httpConnect(context.Background(), c, target, exit.SourceTag{Source: "fwd", Job: "j-2"}.String())
	if err != nil {
		t.Fatalf("CONNECT through the forward: %v\nnode log:\n%s", err, nodeLog.String())
	}
	fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "hello through the exit" {
		t.Fatalf("forward body %q", b)
	}
	_ = c.Close() // the exit records a connection when it ends

	// Anything off the control plane's allowlist is refused.
	c2, err := net.DialTimeout("tcp", fwd, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_ = c2.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := httpConnect(context.Background(), c2, "example.com:443", ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("CONNECT off the allowlist: %v", err)
	}

	// The node's status shows the hubs, its exit and the two tagged fetches.
	var st statusView
	deadline := time.Now().Add(10 * time.Second)
	for {
		var raw syncBuffer
		if err := run(context.Background(), []string{"status", "-addr", nodeStatus, "-json"}, env{stdout: &raw, stderr: io.Discard}); err != nil {
			t.Fatal(err)
		}
		st = statusView{}
		if err := json.Unmarshal([]byte(raw.String()), &st); err != nil {
			t.Fatal(err)
		}
		if st.Exit != nil && len(st.Exit.Recent) >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st.Mode != "node" || st.PeerID != "exit-1" || !proto.HasRole(st.Roles, proto.RoleExit) || st.Exit == nil ||
		!strings.HasSuffix(st.Exit.Listen, fmt.Sprintf(":%d", exit.DefaultPort)) {
		t.Fatalf("node status=%+v", st)
	}
	sources := map[string]string{}
	for _, r := range st.Exit.Recent {
		if r.Result == string(exit.ResultAllowed) {
			sources[r.Source] = r.Job
		}
	}
	if sources["cli"] != "j-1" || sources["fwd"] != "j-2" {
		t.Errorf("exit connections=%+v", st.Exit.Recent)
	}

	// The human-readable status of the hub lists the labelled exit.
	var text syncBuffer
	if err := run(context.Background(), []string{"status", "-addr", hubStatus}, env{stdout: &text, stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if out := text.String(); !strings.Contains(out, "hub hub-1 in m") || !strings.Contains(out, "exit-1") || !strings.Contains(out, "geo=ca-on") {
		t.Errorf("hub status:\n%s", out)
	}
}

// TestP2PInviteAccept links two members with no control plane by swapping
// tokens over stdin and stdout; the accepting side serves an exit that the
// inviting side reaches through a -forward.
func TestP2PInviteAccept(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel test")
	}
	_, target := testTarget(t)
	dir := t.TempDir()
	var ls listens

	inviteIn, inviteInW := io.Pipe()
	inviteOutR, inviteOut := io.Pipe()
	t.Cleanup(func() { _ = inviteInW.Close(); _ = inviteOutR.Close() })
	background(t, []string{"p2p", "invite", "-keys", filepath.Join(dir, "a.json"), "-status", "",
		"-forward", "127.0.0.1:0=100.64.0.2:" + fmt.Sprint(exit.DefaultPort)},
		env{stdin: inviteIn, stdout: inviteOut, stderr: io.Discard, hooks: &testHooks{loopbackICE: true, onListen: ls.hook("a-")}})
	invite, err := bufio.NewReader(inviteOutR).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	acceptOutR, acceptOut := io.Pipe()
	t.Cleanup(func() { _ = acceptOutR.Close() })
	background(t, []string{"p2p", "accept", "-keys", filepath.Join(dir, "b.json"), "-status", "127.0.0.1:0",
		"-exit", "-allow", target, strings.TrimSpace(invite)},
		env{stdin: strings.NewReader(""), stdout: acceptOut, stderr: io.Discard, hooks: &testHooks{loopbackICE: true, onListen: ls.hook("b-")}})
	answer, err := bufio.NewReader(acceptOutR).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(inviteInW, answer); err != nil {
		t.Fatal(err)
	}

	fwd := ls.wait(t, "a-forward")
	var body string
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, err = fetchVia(fwd, target, "source=p2p")
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil || body != "hello through the exit" {
		t.Fatalf("fetch through the p2p exit: %q, %v", body, err)
	}

	var raw syncBuffer
	if err := run(context.Background(), []string{"status", "-addr", ls.wait(t, "b-status"), "-json"}, env{stdout: &raw, stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	var st statusView
	if err := json.Unmarshal([]byte(raw.String()), &st); err != nil {
		t.Fatal(err)
	}
	if st.Mode != "p2p" || st.Address != "100.64.0.2/32" || len(st.Peers) != 1 || !st.Peers[0].Linked || st.Exit == nil {
		t.Fatalf("accepting side status=%+v", st)
	}
}

// fetchVia GETs / from target through the exit behind proxy.
func fetchVia(proxy, target, tag string) (string, error) {
	c, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	tc, err := httpConnect(context.Background(), c, target, tag)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
