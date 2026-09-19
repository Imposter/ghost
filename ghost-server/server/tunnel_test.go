package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
)

// member is a real ghost.Node or ghost.Hub connected to the harness server.
// ICE is restricted to loopback host candidates: nothing binds a routable
// address, no STUN is contacted, and no firewall prompt appears.
type member interface {
	Start(context.Context) error
	Close() error
	Events() <-chan ghost.Event
	Listen(network, addr string) (net.Listener, error)
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	Status() ghost.Status
}

func (h *harness) member(c control.Credentials, hub bool) (member, string) {
	h.t.Helper()
	cfg := ghost.Config{SignalURL: h.ws, PeerToken: c.PeerToken, Network: c.Network, ConnectTimeout: 15 * time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	cfg.UseLoopbackICE()
	var m member
	var err error
	if hub {
		m, err = ghost.NewHub(cfg)
	} else {
		m, err = ghost.NewNode(cfg)
	}
	if err != nil {
		h.t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = m.Close() })
	ip := strings.TrimSuffix(waitMesh(h.t, m.Events(), ghost.EventJoined, 10*time.Second).Address, "/32")
	return m, ip
}

// serveHTTP serves "hello from <name>" on ip:port inside m's netstack.
func serveHTTP(t *testing.T, m member, ip, name string) string {
	t.Helper()
	addr := net.JoinHostPort(ip, "8080")
	ln, err := m.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello from "+name)
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return addr
}

// get fetches http://addr/ over from's tunnel, retrying until timeout.
func get(from member, addr string, timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: from.DialContext}}
	var lastErr error
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		resp, err := client.Get("http://" + addr + "/")
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body), nil
	}
	return "", lastErr
}

func waitMesh(t *testing.T, ev <-chan ghost.Event, kind ghost.EventKind, timeout time.Duration) ghost.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ev:
			if e.Kind == kind {
				return e
			}
		case <-deadline:
			t.Fatalf("mesh event %q not received within %s", kind, timeout)
			return ghost.Event{}
		}
	}
}

// TestNodeHubThroughServer runs a real hub and two real nodes through the
// real server in a hub-only network: each node reaches the hub over
// WireGuard, sees only the hub, cannot reach the other node, and reports
// its link health to the server.
func TestNodeHubThroughServer(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	h := newHarness(t, nil)
	h.ctl("POST", "/control/networks", map[string]any{"name": "iso", "isolation": "hub-only"}, nil, http.StatusCreated)
	hubC := h.peer("iso", "hub", hubRoles)
	node1C := h.peer("iso", "node1", nodeRoles)
	node2C := h.peer("iso", "node2", nodeRoles)

	hub, hubIP := h.member(hubC, true)
	hubAddr := serveHTTP(t, hub, hubIP, "hub")
	node1, _ := h.member(node1C, false)
	node2, node2IP := h.member(node2C, false)
	node2Addr := serveHTTP(t, node2, node2IP, "node2")
	waitMesh(t, node1.Events(), ghost.EventPeerConnected, 30*time.Second)
	waitMesh(t, node2.Events(), ghost.EventPeerConnected, 30*time.Second)

	if body, err := get(node1, hubAddr, 15*time.Second); err != nil || body != "hello from hub" {
		t.Fatalf("node1 -> hub over the tunnel: %q %v", body, err)
	}
	if st := node1.Status(); st.NetmapPeers != 1 || st.Peers != 1 {
		t.Fatalf("node1 status: %+v", st)
	}
	if body, err := get(node1, node2Addr, 2*time.Second); err == nil {
		t.Fatalf("node1 reached node2 in a hub-only network: %q", body)
	}
	// The hub reaches both nodes' services it is allowed to (node2's HTTP).
	if body, err := get(hub, node2Addr, 15*time.Second); err != nil || body != "hello from node2" {
		t.Fatalf("hub -> node2 over the tunnel: %q %v", body, err)
	}

	// Link health arrives in heartbeats.
	var hv httpapi.HealthView
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		h.ctl("GET", "/control/peers/"+node1C.PeerID+"/health", nil, &hv, http.StatusOK)
		if hv.Health != nil && len(hv.Health.Links) == 1 && hv.Health.Links[0].State == proto.LinkConnected {
			break
		}
	}
	if hv.Health == nil || len(hv.Health.Links) != 1 || hv.Health.Links[0].PeerID != hubC.PeerID ||
		hv.Health.Links[0].State != proto.LinkConnected || hv.Health.Links[0].CandidateType != "host" {
		t.Fatalf("node1 health: %+v", hv.Health)
	}
	if n := len(h.srv.Relay.Presence("iso")); n != 3 {
		t.Fatalf("presence: %d sessions", n)
	}
}

// TestMeshIsolationNone: with isolation "none" and a mesh ACL, two nodes and
// no hub connect directly through the server's signalling.
func TestMeshIsolationNone(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	h := newHarness(t, nil)
	h.meshACL("testnet")
	aC := h.peer("testnet", "a", nodeRoles)
	bC := h.peer("testnet", "b", nodeRoles)
	a, _ := h.member(aC, false)
	b, bIP := h.member(bC, false)
	bAddr := serveHTTP(t, b, bIP, "b")
	waitMesh(t, a.Events(), ghost.EventPeerConnected, 30*time.Second)

	if body, err := get(a, bAddr, 15*time.Second); err != nil || body != "hello from b" {
		t.Fatalf("a -> b over the mesh: %q %v", body, err)
	}
}
