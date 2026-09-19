package ghost

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/internal/nettest"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// loopbackTuner restricts a mesh's ICE to the loopback interface (host
// candidates only, no STUN/TURN), so the test binds no routable socket and
// triggers no Windows firewall prompt.
func loopbackTuner(cfg *Config) {
	lb := nettest.LoopbackICEConfig(0)
	cfg.iceTuner = func(c *iceConfig) {
		c.STUNServers = nil
		c.TURNServers = nil
		c.CandidateTypes = lb.CandidateTypes
		c.IPFilter = lb.IPFilter
	}
}

func tunnelIP(addrCIDR string) string {
	if i := strings.IndexByte(addrCIDR, '/'); i >= 0 {
		return addrCIDR[:i]
	}
	return addrCIDR
}

func waitEvent(t *testing.T, ev <-chan Event, kind EventKind, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ev:
			if e.Kind == kind {
				return e
			}
			if e.Kind == EventError {
				t.Logf("mesh error event: %v", e.Err)
			}
		case <-deadline:
			t.Fatalf("event %q not received within %s", kind, timeout)
		}
	}
}

func TestNodeHubTwoNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tunnel integration test in short mode")
	}
	fake := signal.NewFakeServer("100.64.0.0/10")
	dialer := fake.Dialer()
	ctx := context.Background()

	// --- Hub ---
	hubCfg := Config{
		SignalDialer:   dialer,
		DeviceToken:    "hub-token",
		Network:        "testnet",
		ConnectTimeout: 15 * time.Second,
	}
	loopbackTuner(&hubCfg)
	hub, err := NewHub(hubCfg)
	if err != nil {
		t.Fatalf("new hub: %v", err)
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	hubJoined := waitEvent(t, hub.Events(), EventJoined, 10*time.Second)
	hubIP := tunnelIP(hubJoined.Address)

	// Serve a small HTTP endpoint on the hub's tunnel IP (inside the netstack,
	// so no OS port is bound).
	const svcPort = 8080
	ln, err := hub.Listen("tcp", fmt.Sprintf("%s:%d", hubIP, svcPort))
	if err != nil {
		t.Fatalf("hub listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello from hub")
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// --- Two nodes ---
	startNode := func(token string) *Node {
		cfg := Config{
			SignalDialer:   dialer,
			DeviceToken:    token,
			Network:        "testnet",
			ConnectTimeout: 15 * time.Second,
		}
		loopbackTuner(&cfg)
		n, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("new node: %v", err)
		}
		if err := n.Start(ctx); err != nil {
			t.Fatalf("start node: %v", err)
		}
		waitEvent(t, n.Events(), EventPeerConnected, 25*time.Second)
		return n
	}

	node1 := startNode("node1-token")
	defer node1.Close()
	node2 := startNode("node2-token")
	defer node2.Close()

	// The hub should report two connected peers.
	waitFor(t, 10*time.Second, func() bool { return len(hub.Peers()) >= 2 })

	// Each node reaches the hub's in-tunnel HTTP endpoint.
	for i, n := range []*Node{node1, node2} {
		body := httpGetOverTunnel(t, n, hubIP, svcPort)
		if !strings.Contains(body, "hello from hub") {
			t.Fatalf("node %d: unexpected body %q", i+1, body)
		}
	}
}

func httpGetOverTunnel(t *testing.T, n *Node, ip string, port int) string {
	t.Helper()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return n.DialContext(ctx, network, addr)
			},
		},
		Timeout: 10 * time.Second,
	}
	var lastErr error
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://%s:%d/", ip, port))
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	t.Fatalf("tunnel HTTP GET failed: %v", lastErr)
	return ""
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestNodeStatusBeforeJoin(t *testing.T) {
	fake := signal.NewFakeServer("100.64.0.0/10")
	cfg := Config{SignalDialer: fake.Dialer(), DeviceToken: "t", Network: "n"}
	loopbackTuner(&cfg)
	n, err := NewNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	st := n.Status()
	if st.Role != proto.RoleNode {
		t.Errorf("role=%q", st.Role)
	}
	if st.Peers != 0 {
		t.Errorf("expected 0 peers before start")
	}
}
