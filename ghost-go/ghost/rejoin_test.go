package ghost

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/signal"
)

// TestNodeRejoinsAfterDeniedJoin: the control plane refuses a join (the node's
// owner paused it, or the authorizer failed closed during an outage) and then
// allows it. The refusal leaves the websocket open, so nothing else brings the
// node back: it must keep asking and rejoin by itself, without its process
// being restarted.
func TestNodeRejoinsAfterDeniedJoin(t *testing.T) {
	fake := signal.NewFakeServer("100.64.0.0/10")
	var deny atomic.Bool
	var asked atomic.Int64
	deny.Store(true)
	fake.JoinFunc = func(_, _ string) error {
		asked.Add(1)
		if deny.Load() {
			return errors.New("node paused")
		}
		return nil
	}

	cfg := Config{SignalDialer: fake.Dialer(), PeerToken: "node-token", Network: "pool"}
	cfg.joinRetryMin, cfg.joinRetryMax = 20*time.Millisecond, 80*time.Millisecond
	loopbackTuner(&cfg)
	node, err := NewNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// While the join is refused the node keeps asking, and says so: the
	// session is up, the network is not joined, and there is no netmap to
	// report.
	waitFor(t, 5*time.Second, func() bool { return asked.Load() >= 3 })
	st := node.Status()
	if st.SignalState != signal.StateConnected {
		t.Fatalf("a refused join must not drop the session, got %q", st.SignalState)
	}
	if st.Joined || st.Connected {
		t.Fatalf("a refused node reports itself in the network: %+v", st)
	}
	if st.Address != "" {
		t.Errorf("a refused node has no tunnel address, got %q", st.Address)
	}
	if _, ok := node.Netmap(); ok {
		t.Error("a refused node must report no netmap")
	}

	// The control plane changes its mind (the owner resumed the node, or the
	// authorizer came back). No restart, no reconnect: the retry carries it.
	deny.Store(false)
	waitFor(t, 10*time.Second, func() bool { return node.Status().Joined })
	st = node.Status()
	if !st.Connected {
		t.Errorf("a joined node is connected: %+v", st)
	}
	if st.Address == "" {
		t.Errorf("a joined node has a tunnel address: %+v", st)
	}
	if _, ok := node.Netmap(); !ok {
		t.Error("a joined node reports its netmap")
	}
}

// TestJoinRetryStopsWhenJoined: the retry exists for refusals only. Once the
// server answers with a joined message the node stops asking, so an accepted
// node never sends a second join (which a real server answers with an error).
func TestJoinRetryStopsWhenJoined(t *testing.T) {
	fake := signal.NewFakeServer("100.64.0.0/10")
	var asked atomic.Int64
	fake.JoinFunc = func(_, _ string) error {
		asked.Add(1)
		return nil
	}
	cfg := Config{SignalDialer: fake.Dialer(), PeerToken: "node-token", Network: "pool"}
	cfg.joinRetryMin, cfg.joinRetryMax = 20*time.Millisecond, 20*time.Millisecond
	loopbackTuner(&cfg)
	node, err := NewNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return node.Status().Joined })
	time.Sleep(200 * time.Millisecond) // ten retry intervals
	if n := asked.Load(); n != 1 {
		t.Errorf("a joined node asked to join %d times, want 1", n)
	}
}

// TestStatusFollowsTheICEPath: a link whose ICE path is gone must stop being
// reported as a live tunnel, so an owner's window does not say the node is
// working while nothing flows. A disconnected path may come back, so it is
// only hidden; a failed one is taken down for the next reconcile to rebuild.
func TestStatusFollowsTheICEPath(t *testing.T) {
	m, err := newMesh(Config{Network: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	m.joined = true
	link := &peerLink{peerID: "peer-1", address: "100.64.0.3/32", candType: "host", added: true}
	m.links[link.peerID] = link

	up := func() (int, bool) {
		t.Helper()
		peers := m.Status().Peers
		linked := false
		for _, s := range m.Snapshot().Tunnel {
			if s.PeerID == link.peerID {
				linked = true
			}
		}
		return peers, linked
	}

	if peers, linked := up(); peers != 1 || !linked {
		t.Fatalf("a wired link is reported: peers=%d linked=%t", peers, linked)
	}

	// ICE lost the path. Nothing is torn down (WireGuard survives a short
	// gap) but the status stops claiming a tunnel.
	m.iceStateChanged(link, ice.ConnectionStateDisconnected)
	if peers, linked := up(); peers != 0 || linked {
		t.Errorf("a disconnected path is still reported as linked: peers=%d linked=%t", peers, linked)
	}
	if h := m.healthSummary(); len(h.Links) != 1 || h.Links[0].State == "connected" {
		t.Errorf("health reports a disconnected path as connected: %+v", h.Links)
	}

	// ICE recovered it.
	m.iceStateChanged(link, ice.ConnectionStateConnected)
	if peers, linked := up(); peers != 1 || !linked {
		t.Errorf("a recovered path is reported again: peers=%d linked=%t", peers, linked)
	}

	// ICE gave up: the link is failed, and stays hidden.
	m.iceStateChanged(link, ice.ConnectionStateFailed)
	if peers, linked := up(); peers != 0 || linked {
		t.Errorf("a failed path is still reported as linked: peers=%d linked=%t", peers, linked)
	}
	m.mu.Lock()
	failed := link.failed
	m.mu.Unlock()
	if !failed {
		t.Error("a failed ICE path marks the link failed, so reconcile rebuilds it")
	}
}

// TestNetmapIsOnlyReportedWhileJoined: the netmap describes the network as of
// the session that carried it. Once the join is lost, the peers in it are no
// longer something this member knows anything about, so it reports none.
func TestNetmapIsOnlyReportedWhileJoined(t *testing.T) {
	fake := signal.NewFakeServer("100.64.0.0/10")
	cfg := Config{SignalDialer: fake.Dialer(), PeerToken: "node-token", Network: "pool"}
	loopbackTuner(&cfg)
	node, err := NewNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		_, ok := node.Netmap()
		return ok
	})

	node.setJoined(false)
	if _, ok := node.Netmap(); ok {
		t.Error("an unjoined member must not report the netmap of a session it has lost")
	}
	if st := node.Status(); st.Connected || st.NetmapPeers != 0 {
		t.Errorf("an unjoined member reports itself connected: %+v", st)
	}
}
