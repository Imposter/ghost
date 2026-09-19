package direct_test

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/ghost/direct"
)

// startMember starts a Node on sig with loopback-only ICE (host candidates,
// no STUN), so the test binds no routable socket.
func startMember(t *testing.T, sig *direct.Signaller, keyPath string) *ghost.Node {
	t.Helper()
	cfg := ghost.Config{Signaller: sig, KeyStorePath: keyPath, ConnectTimeout: 20 * time.Second}
	cfg.UseLoopbackICE()
	n, err := ghost.NewNode(cfg)
	if err != nil {
		t.Fatalf("new node: %v", err)
	}
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n
}

func newSignaller(t *testing.T, cfg direct.Config) *direct.Signaller {
	t.Helper()
	s, err := direct.New(cfg)
	if err != nil {
		t.Fatalf("direct.New: %v", err)
	}
	return s
}

func waitConnected(t *testing.T, n *ghost.Node) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case e := <-n.Events():
			switch e.Kind {
			case ghost.EventPeerConnected:
				return
			case ghost.EventError:
				t.Logf("event error: %v", e.Err)
			}
		case <-deadline:
			t.Fatal("peer not connected in time")
		}
	}
}

// echoRoundTrip serves an echo on server's tunnel address and checks a TCP
// round trip from client.
func echoRoundTrip(t *testing.T, server, client *ghost.Node, serverIP string) {
	t.Helper()
	ln, err := server.Listen("tcp", serverIP+":7000")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := client.DialContext(ctx, "tcp", serverIP+":7000")
	if err != nil {
		t.Fatalf("dial over tunnel: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	msg := []byte("ping over ghost")
	if _, err := c.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(msg) {
		t.Fatalf("echo = %q, want %q", got, msg)
	}
}

func TestTokenPeersConnectWithoutServer(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	sa := newSignaller(t, direct.Config{Address: "100.64.0.1/32"})
	sb := newSignaller(t, direct.Config{Address: "100.64.0.2/32"})
	a := startMember(t, sa, "")
	b := startMember(t, sb, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	xa, xb := direct.Pipe()
	errc := make(chan error, 1)
	go func() { errc <- direct.Answer(ctx, sb, xb) }()
	if err := direct.Invite(ctx, sa, xa); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("answer: %v", err)
	}
	waitConnected(t, a)
	waitConnected(t, b)
	echoRoundTrip(t, a, b, "100.64.0.1")
	echoRoundTrip(t, b, a, "100.64.0.2")
}

func freeUDPPort(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	return addr
}

func TestStaticPeersWithoutICE(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	dir := t.TempDir()
	keyA, keyB := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	ka, err := ghost.LoadOrCreateKeys(keyA)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := ghost.LoadOrCreateKeys(keyB)
	if err != nil {
		t.Fatal(err)
	}
	// A has a fixed endpoint; B knows it and has none of its own.
	endpointA := freeUDPPort(t)
	sa := newSignaller(t, direct.Config{Address: "100.64.1.1/32", Static: []direct.StaticPeer{
		{Name: "b", PublicKey: kb.PublicKey(), Address: "100.64.1.2/32", ListenAddr: endpointA},
	}})
	sb := newSignaller(t, direct.Config{Address: "100.64.1.2/32", Static: []direct.StaticPeer{
		{Name: "a", PublicKey: ka.PublicKey(), Address: "100.64.1.1/32", Endpoint: endpointA, ListenAddr: "127.0.0.1:0"},
	}})
	a := startMember(t, sa, keyA)
	b := startMember(t, sb, keyB)
	waitConnected(t, a)
	waitConnected(t, b)
	echoRoundTrip(t, a, b, "100.64.1.1") // B dials A: the side with the endpoint
	echoRoundTrip(t, b, a, "100.64.1.2") // and back, to the learned address
}

func TestAcceptRefusesAddressCollision(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	sa := newSignaller(t, direct.Config{Address: "100.64.2.1/32"})
	sb := newSignaller(t, direct.Config{Address: "100.64.2.1/32"})
	startMember(t, sa, "")
	startMember(t, sb, "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inv, err := sa.CreateInvite(ctx)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := sb.AcceptInvite(ctx, inv); !errors.Is(err, direct.ErrAddressInUse) {
		t.Fatalf("accept = %v, want ErrAddressInUse", err)
	}
	if _, err := sa.AcceptInvite(ctx, inv); err == nil {
		t.Fatal("accepting its own invite succeeded")
	}
}

func TestNewValidatesStaticPeers(t *testing.T) {
	k, _ := ghost.GenerateKeys()
	for _, cfg := range []direct.Config{
		{Address: "not-an-address"},
		{Address: "100.64.0.0/10"},
		{Address: "100.64.0.1/32", Static: []direct.StaticPeer{{PublicKey: "bad", Address: "100.64.0.2/32", Endpoint: "127.0.0.1:1"}}},
		{Address: "100.64.0.1/32", Static: []direct.StaticPeer{{PublicKey: k.PublicKey(), Address: "100.64.0.1/32", Endpoint: "127.0.0.1:1"}}},
		{Address: "100.64.0.1/32", Static: []direct.StaticPeer{{PublicKey: k.PublicKey(), Address: "100.64.0.2/32"}}},
	} {
		if _, err := direct.New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}
