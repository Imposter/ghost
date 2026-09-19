package ghost

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// startMember starts a Node (or a Hub when hub is true) on the fake server
// and waits for it to join.
func startMember(t *testing.T, fake *signal.FakeServer, token string, hub bool, tune func(*Config)) (*mesh, Event) {
	t.Helper()
	cfg := Config{SignalDialer: fake.Dialer(), PeerToken: token, Network: "iso", ConnectTimeout: 15 * time.Second}
	loopbackTuner(&cfg)
	if tune != nil {
		tune(&cfg)
	}
	var m *mesh
	if hub {
		h, err := NewHub(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m = h.mesh
	} else {
		n, err := NewNode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m = n.mesh
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, waitEvent(t, m.Events(), EventJoined, 10*time.Second)
}

func (m *mesh) linkIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.links))
	for id := range m.links {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// TestHubOnlyNonHubKeyRejected is isolation layer 3: under hub-only, a node
// configures only hub WireGuard keys, each with a /32 AllowedIPs, even when a
// faulty control plane lists other nodes in its netmap and relays their
// offers.
func TestHubOnlyNonHubKeyRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	fake := signal.NewFakeServer("100.64.0.0/10")
	fake.HubOnly = true
	fake.IgnoreIsolation = true

	hub, hubJoined := startMember(t, fake, "hub", true, nil)
	node1, _ := startMember(t, fake, "node1", false, nil)
	waitEvent(t, node1.Events(), EventPeerConnected, 25*time.Second)
	node2, _ := startMember(t, fake, "node2", false, nil)
	waitEvent(t, node2.Events(), EventPeerConnected, 25*time.Second)

	node2ID := node2.Status().PeerID
	waitFor(t, 5*time.Second, func() bool {
		_, ok := node1.netmapPeer(node2ID)
		return ok
	})
	node1.reconcile()

	hubID := hub.Status().PeerID
	if got := node1.linkIDs(); !slices.Equal(got, []string{hubID}) {
		t.Fatalf("node1 links = %v, want only the hub %s", got, hubID)
	}
	node1.mu.Lock()
	wgPeers := node1.wg.GetPeers()
	node1.mu.Unlock()
	if len(wgPeers) != 1 {
		t.Fatalf("node1 configured %d WireGuard peers, want 1 (the hub)", len(wgPeers))
	}
	if key := base64.StdEncoding.EncodeToString(wgPeers[0].PublicKey); key != hub.Keys().PublicKey() {
		t.Fatalf("node1 configured key %s, want the hub's", key)
	}
	if want := []string{tunnelIP(hubJoined.Address) + "/32"}; !slices.Equal(wgPeers[0].AllowedIPs, want) {
		t.Fatalf("AllowedIPs = %v, want %v", wgPeers[0].AllowedIPs, want)
	}

	// A relayed offer from the other node is refused too.
	node1.handleSignal(proto.TypeOffer, proto.Signal{From: node2ID, To: node1.Status().PeerID, Ufrag: "u", Pwd: "p"})
	if node1.RefusedLinks() != 1 {
		t.Fatalf("refused links = %d, want 1", node1.RefusedLinks())
	}
	if got := node1.linkIDs(); !slices.Equal(got, []string{hubID}) {
		t.Fatalf("after a non-hub offer, node1 links = %v", got)
	}
}

// TestHubNeverForwards is isolation layer 4: a node that routes the whole
// pool through the hub (a modified client) and dials another node's address
// gets nothing through; the hub drops the packets instead of forwarding.
func TestHubNeverForwards(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	fake := signal.NewFakeServer("100.64.0.0/10")
	fake.HubOnly = true

	hub, hubJoined := startMember(t, fake, "hub", true, nil)
	victim, victimJoined := startMember(t, fake, "victim", false, nil)
	waitEvent(t, victim.Events(), EventPeerConnected, 25*time.Second)
	attacker, _ := startMember(t, fake, "attacker", false, func(c *Config) {
		c.allowedIPsOverride = func(string, string) []string { return []string{DefaultPool} }
	})
	waitEvent(t, attacker.Events(), EventPeerConnected, 25*time.Second)

	// The victim listens without the packet filter, so any packet that
	// reached it would show up here.
	victimIP := net.ParseIP(tunnelIP(victimJoined.Address))
	raw, err := victim.Netstack().ListenTCP(&net.TCPAddr{IP: victimIP, Port: 7000})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := raw.Accept(); err == nil {
			accepted <- struct{}{}
			_ = c.Close()
		}
	}()

	// The tunnel to the hub itself works.
	hubIP := tunnelIP(hubJoined.Address)
	ln, err := hub.Listen("tcp", net.JoinHostPort(hubIP, "7001"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "hub") }),
		ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: attacker.DialContext}}
	waitFor(t, 15*time.Second, func() bool {
		resp, err := client.Get("http://" + net.JoinHostPort(hubIP, "7001"))
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})

	before := hub.ForwardDrops()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, err := attacker.DialContext(ctx, "tcp", net.JoinHostPort(victimIP.String(), "7000")); err == nil {
		_ = c.Close()
		t.Fatal("attacker reached the victim through the hub")
	}
	select {
	case <-accepted:
		t.Fatal("the victim accepted a connection forwarded by the hub")
	case <-time.After(200 * time.Millisecond):
	}
	if hub.ForwardDrops() <= before {
		t.Fatalf("hub forward drops = %d, want more than %d", hub.ForwardDrops(), before)
	}
}

// TestHubOnlySourceChecks is isolation layer 5: under hub-only, a node's
// packet filter, exit and metrics admit only hub source addresses, whatever
// the filter rules or MetricsConfig.AllowPeers say.
func TestHubOnlySourceChecks(t *testing.T) {
	m, err := newMesh(Config{Metrics: &MetricsConfig{AllowPeers: []string{"exit1"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const hubIP, exitIP = "100.64.0.1", "100.64.0.3"
	m.netmap = &proto.Netmap{
		Network:   "iso",
		Isolation: proto.IsolationHubOnly,
		Self:      proto.PeerInfo{PeerID: "node1", Address: "100.64.0.2/32", Roles: []proto.Role{proto.RoleNode, proto.RoleExit}},
		Peers: []proto.PeerInfo{
			{PeerID: "hub1", Address: hubIP + "/32", Roles: []proto.Role{proto.RoleHub}},
			{PeerID: "exit1", Address: exitIP + "/32", Roles: []proto.Role{proto.RoleExit}},
		},
		// A filter that (wrongly) admits the other exit.
		Filter: &proto.PacketFilter{Rules: []proto.FilterRule{{Src: []string{hubIP + "/32", exitIP + "/32"}}}},
	}
	m.links["hub1"] = &peerLink{peerID: "hub1", address: hubIP + "/32"}
	m.links["exit1"] = &peerLink{peerID: "exit1", address: exitIP + "/32"}

	hubAddr := &net.TCPAddr{IP: net.ParseIP(hubIP), Port: 40000}
	exitAddr := &net.TCPAddr{IP: net.ParseIP(exitIP), Port: 40000}
	if !m.IsHubSource(hubAddr) || m.IsHubSource(exitAddr) {
		t.Fatalf("IsHubSource: hub=%v exit=%v", m.IsHubSource(hubAddr), m.IsHubSource(exitAddr))
	}
	hub, _ := addrIP(hubAddr)
	other, _ := addrIP(exitAddr)
	if !m.admits(hub, 8080) || m.admits(other, 8080) {
		t.Fatalf("packet filter: hub=%v exit=%v", m.admits(hub, 8080), m.admits(other, 8080))
	}
	req := func(addr *net.TCPAddr) *http.Request { return &http.Request{RemoteAddr: addr.String()} }
	if !m.authorizeMetrics(req(hubAddr)) || m.authorizeMetrics(req(exitAddr)) {
		t.Fatal("metrics must admit the hub and refuse the non-hub AllowPeers entry")
	}

	// The exit closes a non-hub client before reading a byte, and serves the hub.
	allow := exit.NewAllowlist()
	ex := exit.New(exit.Config{Policy: allow, AllowSource: m.IsHubSource})
	defer ex.Close()
	for _, tc := range []struct {
		from  *net.TCPAddr
		serve bool
	}{{exitAddr, false}, {hubAddr, true}} {
		ln := newOneConnListener(tc.from)
		go func() { _ = ex.Serve(ln) }()
		client := ln.client
		_ = client.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = client.Write([]byte{5, 1, 0}) // SOCKS5 greeting, no auth
		reply := make([]byte, 2)
		_, err := io.ReadFull(bufio.NewReader(client), reply)
		switch {
		case tc.serve && err != nil:
			t.Fatalf("exit refused the hub: %v", err)
		case !tc.serve && err == nil:
			t.Fatalf("exit answered a non-hub source: %v", reply)
		case !tc.serve && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe):
			t.Fatalf("non-hub source: %v", err)
		}
		_ = client.Close()
	}
}

// oneConnListener yields one in-memory connection whose server side reports
// a chosen remote address.
type oneConnListener struct {
	ch     chan net.Conn
	client net.Conn
	done   chan struct{}
}

type remoteConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteConn) RemoteAddr() net.Addr { return c.remote }

func newOneConnListener(remote net.Addr) *oneConnListener {
	server, client := net.Pipe()
	l := &oneConnListener{ch: make(chan net.Conn, 1), client: client, done: make(chan struct{})}
	l.ch <- remoteConn{Conn: server, remote: remote}
	return l
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConnListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *oneConnListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(100, 64, 0, 2), Port: 1080}
}
