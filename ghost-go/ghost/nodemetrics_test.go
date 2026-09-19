package ghost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/otelsetup"
	"github.com/Imposter/ghost/ghost-go/signal"
)

// TestNodeMetricsOverTunnel runs a real loopback Node<->Hub tunnel. The node
// serves an exit and its metrics on its tunnel IP; the hub proxies one request
// through the exit, then fetches the node's snapshot, connections and
// Prometheus text over the tunnel.
func TestNodeMetricsOverTunnel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tunnel integration test in short mode")
	}
	ctx := context.Background()
	fake := signal.NewFakeServer("100.64.0.0/10")

	// Loopback target the exit dials through the host network.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello via exit")
	}))
	defer target.Close()
	tHost, tPortStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	tPort, _ := strconv.Atoi(tPortStr)

	// --- Hub ---
	hubCfg := Config{SignalDialer: fake.Dialer(), DeviceToken: "hub-token", Network: "m", ConnectTimeout: 15 * time.Second}
	loopbackTuner(&hubCfg)
	hub, err := NewHub(hubCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	waitEvent(t, hub.Events(), EventJoined, 10*time.Second)

	// --- Node with metrics and an exit ---
	otel, err := otelsetup.New(ctx, otelsetup.Options{ServiceName: "ghost-node-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer otel.Shutdown(ctx)
	col := metrics.NewCollector(metrics.CollectorConfig{RingSize: 8})
	nodeCfg := Config{
		SignalDialer: fake.Dialer(), DeviceToken: "node-token", Network: "m", ConnectTimeout: 15 * time.Second,
		Metrics: &MetricsConfig{Collector: col, Prometheus: otel.PrometheusHandler},
	}
	loopbackTuner(&nodeCfg)
	node, err := NewNode(nodeCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	joined := waitEvent(t, node.Events(), EventJoined, 10*time.Second)
	nodeIP := tunnelIP(joined.Address)
	waitEvent(t, node.Events(), EventPeerConnected, 25*time.Second)

	allow := exit.NewAllowlist()
	allow.Set([]string{tHost})
	ex := exit.New(exit.Config{
		Policy: allow, Accountant: col, PeerResolver: node, AllowLoopbackForTest: true,
		MeterProvider: otel.MeterProvider,
	})
	defer ex.Close()
	col.AttachExit(ex)
	exLn, err := node.Listen("tcp", net.JoinHostPort(nodeIP, "1080"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ex.Serve(exLn) }()

	nodeID := node.Status().DeviceID
	hubID := hub.Status().DeviceID
	waitFor(t, 10*time.Second, func() bool { return len(hub.Peers()) == 1 })

	// The hub proxies one request through the node's exit (HTTP CONNECT).
	body := connectThrough(t, hub, nodeIP+":1080", fmt.Sprintf("%s:%d", tHost, tPort), "job=metrics")
	if !strings.Contains(body, "hello via exit") {
		t.Fatalf("exit body %q", body)
	}
	waitFor(t, 5*time.Second, func() bool { return col.Snapshot().Totals.Connections == 1 })

	var fetcher NodeMetricsFetcher = hub
	var snap metrics.Snapshot
	deadline := time.Now().Add(15 * time.Second)
	for {
		snap, err = fetcher.NodeSnapshot(ctx, nodeID)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("NodeSnapshot: %v", err)
	}
	if snap.DeviceID != nodeID || snap.Totals.Connections != 1 || snap.Totals.BytesIn == 0 {
		t.Errorf("snapshot=%+v", snap)
	}
	if len(snap.Sources) != 1 || snap.Sources[0].Peer != hubID || snap.Sources[0].Tag != "job=metrics" {
		t.Errorf("sources=%+v want peer %s tag job=metrics", snap.Sources, hubID)
	}
	if len(snap.Tunnel) != 1 || snap.Tunnel[0].PeerID != hubID || snap.Tunnel[0].CandidateType != "host" ||
		snap.Tunnel[0].RxBytes == 0 || snap.Tunnel[0].TxBytes == 0 {
		t.Errorf("tunnel=%+v", snap.Tunnel)
	}

	conns, err := fetcher.NodeConnections(ctx, nodeID, 5)
	if err != nil || len(conns) != 1 || conns[0].Host != tHost || conns[0].Protocol != string(exit.ProtoHTTPConnect) ||
		conns[0].SourcePeer != hubID || conns[0].IP == "" {
		t.Errorf("connections=%+v err=%v", conns, err)
	}

	prom, err := fetcher.NodePrometheus(ctx, nodeID)
	if err != nil || !strings.Contains(string(prom), "ghost_exit_connections") {
		t.Errorf("prometheus err=%v body has ghost_exit_connections=%v", err, strings.Contains(string(prom), "ghost_exit_connections"))
	}

	// The in-process Snapshot matches what the hub fetched.
	if local := node.Snapshot(); local.Totals.Connections != 1 || len(local.Tunnel) != 1 {
		t.Errorf("local snapshot=%+v", local)
	}

	if _, err := fetcher.NodeSnapshot(ctx, "no-such-device"); !errors.Is(err, ErrUnknownPeer) {
		t.Errorf("unknown peer err=%v", err)
	}

	// Anyone but the hub (or an AllowPeers entry) gets 403: route a request
	// from a non-hub tunnel address through the node's real authorizer.
	h := metrics.NewHandler(metrics.HandlerConfig{Source: node, Authorize: node.authorizeMetrics})
	for _, remote := range []string{"100.64.0.200:40000", "192.168.1.10:40000"} {
		req := httptest.NewRequest(http.MethodGet, "/metrics?format=json", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("remote %s: code=%d want 403", remote, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics?format=json", nil)
	hubIP, _ := tunnelAddr(hub.Status().Address)
	req.RemoteAddr = net.JoinHostPort(hubIP.String(), "40000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("hub address: code=%d want 200", rec.Code)
	}
}

// connectThrough opens an HTTP CONNECT tunnel through the exit at proxyAddr
// (dialled over the hub's netstack) and GETs / from target.
func connectThrough(t *testing.T, hub *Hub, proxyAddr, target, tag string) string {
	t.Helper()
	var c net.Conn
	var err error
	deadline := time.Now().Add(15 * time.Second)
	for {
		dctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		c, err = hub.DialContext(dctx, "tcp", proxyAddr)
		cancel()
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial exit over tunnel: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nX-Ghost-Source: %s\r\n\r\n", target, tag)
	br := bufio.NewReader(c)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("CONNECT status %q err=%v", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}
	fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
	b, _ := io.ReadAll(br)
	return string(b)
}
