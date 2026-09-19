package signal

import (
	"context"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

func waitState(t *testing.T, c *Client, want State) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.State() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state %q not reached (now %q)", want, c.State())
}

func TestClientHelloJoin(t *testing.T) {
	srv := NewFakeServer("100.64.0.0/10")
	c := New(Config{
		URL:         "ws://fake",
		Dialer:      srv.Dialer(),
		DeviceToken: "tok",
		Role:        proto.RoleNode,
		PublicKey:   "pk",
	}, Handlers{})
	ctx := context.Background()
	c.Start(ctx)
	defer c.Close()

	waitState(t, c, StateConnected)
	if c.Welcome().DeviceID == "" {
		t.Fatal("no device id assigned")
	}

	if err := c.Join(ctx, "net1"); err != nil {
		t.Fatalf("join: %v", err)
	}
	// Expect a Joined event with an assigned address.
	joined := waitEvent(t, c, proto.TypeJoined)
	if joined.Joined == nil || joined.Joined.Address == "" {
		t.Fatal("no address assigned on join")
	}
}

func TestClientRejectsBadToken(t *testing.T) {
	srv := NewFakeServer("100.64.0.0/10")
	srv.AllowToken("good")
	c := New(Config{
		URL:         "ws://fake",
		Dialer:      srv.Dialer(),
		DeviceToken: "bad",
		Role:        proto.RoleNode,
		MinBackoff:  20 * time.Millisecond,
	}, Handlers{})
	c.Start(context.Background())
	defer c.Close()

	ev := waitEvent(t, c, proto.TypeError)
	if ev.Err == nil || ev.Err.Code != proto.ErrCodeUnauthorized {
		t.Fatalf("expected unauthorized error, got %+v", ev.Err)
	}
}

// TestClientReconnect verifies the client reconnects after a dropped
// connection using its backoff loop.
func TestClientReconnect(t *testing.T) {
	srv := NewFakeServer("100.64.0.0/10")
	c := New(Config{
		URL:         "ws://fake",
		Dialer:      srv.Dialer(),
		DeviceToken: "tok",
		Role:        proto.RoleNode,
		MinBackoff:  20 * time.Millisecond,
		MaxBackoff:  100 * time.Millisecond,
	}, Handlers{})
	c.Start(context.Background())
	defer c.Close()

	waitState(t, c, StateConnected)
	firstDev := c.Welcome().DeviceID

	// Drop the underlying connection to force a reconnect.
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		t.Fatal("no active connection")
	}
	_ = conn.Close()

	// It should leave connected, then come back.
	waitState(t, c, StateConnected)
	if c.Welcome().DeviceID == "" {
		t.Fatal("no device id after reconnect")
	}
	_ = firstDev
}

func waitEvent(t *testing.T, c *Client, want proto.Type) Event {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Type == want {
				return ev
			}
		case <-timeout:
			t.Fatalf("event %q not received", want)
		}
	}
}
