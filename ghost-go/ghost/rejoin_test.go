package ghost

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/internal/ice"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
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
	var denials, refusalErrors atomic.Int64
	var reason atomic.Value
	go func() {
		for ev := range node.Events() {
			switch {
			case ev.Kind == EventJoinDenied:
				denials.Add(1)
				reason.Store(ev.Reason)
			case ev.Kind == EventError && ev.Err != nil && strings.Contains(ev.Err.Error(), "join denied"):
				refusalErrors.Add(1)
			}
		}
	}()

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
	// It says why, once: a paused node's log reads "paused", not a
	// refusal every retry.
	if st.JoinDenied != "node paused" {
		t.Errorf("status says the join is refused for %q, want %q", st.JoinDenied, "node paused")
	}
	waitFor(t, 5*time.Second, func() bool { return denials.Load() > 0 })
	if n := denials.Load(); n != 1 {
		t.Errorf("%d join_denied events for %d refusals, want 1", n, asked.Load())
	}
	if r, _ := reason.Load().(string); r != "node paused" {
		t.Errorf("join_denied says %q, want %q", r, "node paused")
	}
	if n := refusalErrors.Load(); n != 0 {
		t.Errorf("a refusal was also reported as %d error events", n)
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
	if st.JoinDenied != "" {
		t.Errorf("a joined node still reports a refusal: %q", st.JoinDenied)
	}
}

// linkCounter is a Signaller that lets the member straight into a network
// with one hub in it, and counts the links the member tries to open. It
// refuses every one, so no ICE runs.
type linkCounter struct {
	events chan signal.Event
	links  atomic.Int64
}

func (c *linkCounter) Start(context.Context, SignalSelf) error {
	c.events <- signal.Event{State: signal.StateConnected}
	return nil
}
func (c *linkCounter) Events() <-chan signal.Event        { return c.events }
func (c *linkCounter) State() signal.State                { return signal.StateConnected }
func (c *linkCounter) ICEServers() []proto.ICEServer      { return nil }
func (c *linkCounter) ReportHealth(context.Context) error { return nil }
func (c *linkCounter) Close() error                       { return nil }
func (c *linkCounter) Send(context.Context, proto.Type, proto.Signal) error {
	return errors.New("no signalling here")
}
func (c *linkCounter) Link(_, _ proto.PeerInfo) (LinkPlan, error) {
	c.links.Add(1)
	return LinkPlan{}, errors.New("not linking in this test")
}
func (c *linkCounter) Join(_ context.Context, network string) error {
	hub, err := GenerateKeys()
	if err != nil {
		return err
	}
	c.events <- signal.Event{Joined: &proto.Joined{Network: network, Address: "100.64.0.2/32", Pool: "100.64.0.0/10"}}
	c.events <- signal.Event{Netmap: &proto.Netmap{
		Network: network,
		Self:    proto.PeerInfo{PeerID: "node", Address: "100.64.0.2/32", Roles: []proto.Role{proto.RoleNode}},
		Peers: []proto.PeerInfo{{
			PeerID: "hub", Address: "100.64.0.1/32", PublicKey: hub.PublicKey(),
			Roles: []proto.Role{proto.RoleHub}, Online: true,
		}},
	}}
	return nil
}

// TestNoLinksWhileUnjoined: out of the network (a paused node, whose joins the
// control plane refuses) a member opens no links. The control plane relays no
// signalling for it, so each attempt would only earn a "join a network
// before signalling" error, every repair round, for as long as the pause.
func TestNoLinksWhileUnjoined(t *testing.T) {
	sig := &linkCounter{events: make(chan signal.Event, 16)}
	node, err := NewNode(Config{Signaller: sig, Network: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return sig.links.Load() > 0 })

	node.setJoined(false)
	before := sig.links.Load()
	node.reconcile()
	node.reconcile()
	if n := sig.links.Load() - before; n != 0 {
		t.Errorf("an unjoined member tried %d links", n)
	}

	node.setJoined(true)
	node.reconcile()
	if sig.links.Load() == before {
		t.Error("a joined member stopped linking to its hub")
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

// TestPausedWhileJoinedIsOneRefusal: the node's owner pauses it while it is in
// the network. The control plane withdraws the session ("connection no
// longer authorized"), then refuses every join the node retries. All of that
// is one fact, the node is paused, and is reported as one join_denied: no
// errors, however long the pause. Resuming lets the node back in by itself.
func TestPausedWhileJoinedIsOneRefusal(t *testing.T) {
	fake := signal.NewFakeServer("100.64.0.0/10")
	var deny atomic.Bool
	var asked atomic.Int64
	fake.JoinFunc = func(_, _ string) error {
		asked.Add(1)
		if deny.Load() {
			return errors.New("node paused")
		}
		return nil
	}
	cfg := Config{SignalDialer: fake.Dialer(), PeerToken: "node-token", Network: "pool"}
	cfg.joinRetryMin, cfg.joinRetryMax = 20*time.Millisecond, 40*time.Millisecond
	loopbackTuner(&cfg)
	node, err := NewNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	var denials, errs atomic.Int64
	var mu sync.Mutex
	var errText []string
	go func() {
		for ev := range node.Events() {
			switch ev.Kind {
			case EventJoinDenied:
				denials.Add(1)
			case EventError:
				errs.Add(1)
				mu.Lock()
				errText = append(errText, ev.Err.Error())
				mu.Unlock()
			}
		}
	}()
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return node.Status().Joined })

	deny.Store(true)
	before := asked.Load()
	fake.Deauthorize(node.Status().PeerID, "node paused")
	waitFor(t, 5*time.Second, func() bool { return asked.Load() >= before+5 })

	if n := denials.Load(); n != 1 {
		t.Errorf("%d join_denied events for one pause, want 1", n)
	}
	if n := errs.Load(); n != 0 {
		mu.Lock()
		t.Errorf("a pause was reported as %d errors: %q", n, errText)
		mu.Unlock()
	}
	if st := node.Status(); st.Joined || st.JoinDenied != "node paused" {
		t.Errorf("paused: joined=%t join_denied=%q", st.Joined, st.JoinDenied)
	}

	deny.Store(false)
	waitFor(t, 10*time.Second, func() bool { return node.Status().Joined })
	if st := node.Status(); st.JoinDenied != "" {
		t.Errorf("a resumed node still reports a refusal: %q", st.JoinDenied)
	}
}
