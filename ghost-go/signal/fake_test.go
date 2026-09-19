package signal

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

func fakeClient(t *testing.T, srv *FakeServer, token string, roles ...proto.Role) *Client {
	t.Helper()
	c := New(Config{
		URL: "ws://fake", Dialer: srv.Dialer(), PeerToken: token, PublicKey: "pk-" + token, Roles: roles,
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	}, Handlers{})
	c.Start(context.Background())
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// A registered peer gets its enrolled roles, tags and labels, whatever its
// hello asks for, and netmaps carry them to the peers that see it.
func TestFakeServerRegisteredPeer(t *testing.T) {
	srv := NewFakeServer("100.64.0.0/10")
	labels := map[string]string{"geo": "ca-on", "asn": "577"}
	srv.AddPeer("exit-tok", FakePeer{ID: "exit-1", Name: "exit", Roles: []proto.Role{proto.RoleExit, proto.RoleNode}, Tags: []string{"tag:exit"}, Labels: labels})
	srv.AddPeer("hub-tok", FakePeer{ID: "hub-1", Roles: []proto.Role{proto.RoleHub}})

	hub := fakeClient(t, srv, "hub-tok", proto.RoleHub)
	waitState(t, hub, StateConnected)
	if err := hub.Join(context.Background(), "n"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, hub, proto.TypeNetmap)

	exit := fakeClient(t, srv, "exit-tok", proto.RoleExit)
	waitState(t, exit, StateConnected)
	if id := exit.Welcome().PeerID; id != "exit-1" {
		t.Fatalf("peer id=%q want exit-1", id)
	}
	if err := exit.Join(context.Background(), "n"); err != nil {
		t.Fatal(err)
	}
	nm := waitEvent(t, exit, proto.TypeNetmap).Netmap
	if !slices.Equal(nm.Self.Roles, []proto.Role{proto.RoleExit, proto.RoleNode}) || !maps.Equal(nm.Self.Labels, labels) ||
		!slices.Equal(nm.Self.Tags, []string{"tag:exit"}) {
		t.Fatalf("self=%+v", nm.Self)
	}
	d := waitEvent(t, hub, proto.TypeNetmapDelta).Delta
	if len(d.Upsert) != 1 || d.Upsert[0].PeerID != "exit-1" || d.Upsert[0].Name != "exit" || !proto.HasRole(d.Upsert[0].Roles, proto.RoleExit) ||
		!maps.Equal(d.Upsert[0].Labels, labels) {
		t.Fatalf("hub delta=%+v", d)
	}
}

// A hello asking for a role the registered peer does not hold is refused, as
// ghost-server refuses it.
func TestFakeServerRefusesUnheldRole(t *testing.T) {
	srv := NewFakeServer("100.64.0.0/10")
	srv.AddPeer("node-tok", FakePeer{Roles: []proto.Role{proto.RoleNode}})
	c := fakeClient(t, srv, "node-tok", proto.RoleExit)
	ev := waitEvent(t, c, proto.TypeError)
	if ev.Err.Code != proto.ErrCodeUnauthorized || !ev.Err.Fatal {
		t.Fatalf("error=%+v want fatal unauthorized", ev.Err)
	}

	// Registering restricts tokens, as AllowToken does.
	other := fakeClient(t, srv, "unknown-tok")
	if ev := waitEvent(t, other, proto.TypeError); ev.Err.Code != proto.ErrCodeUnauthorized {
		t.Fatalf("unregistered token: %+v", ev.Err)
	}
}
