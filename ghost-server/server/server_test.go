package server_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

var (
	hubRoles  = []proto.Role{proto.RoleHub}
	nodeRoles = []proto.Role{proto.RoleNode}
	exitRoles = []proto.Role{proto.RoleExit, proto.RoleNode} // roles are kept sorted
)

type authKeyResp struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (h *harness) authKey(network string, body map[string]any) authKeyResp {
	h.t.Helper()
	var k authKeyResp
	if body == nil {
		body = map[string]any{}
	}
	h.ctl("POST", "/control/networks/"+network+"/auth-keys", body, &k, http.StatusCreated)
	if !strings.HasPrefix(k.Key, control.AuthKeyPrefix) {
		h.t.Fatalf("auth key %q lacks prefix", k.Key)
	}
	return k
}

func (h *harness) enroll(key string, labels map[string]string) (control.Credentials, int) {
	h.t.Helper()
	var creds control.Credentials
	status := h.do("POST", "/v1/enroll", "", map[string]any{"auth_key": key, "name": "p", "labels": labels}, &creds)
	return creds, status
}

func TestEnrollPreAuthKey(t *testing.T) {
	h := newHarness(t, nil)
	h.defineTags("testnet", "tag:exit")

	// Single use, with roles and pre-applied tags.
	single := h.authKey("testnet", map[string]any{"roles": []string{"exit", "node"}, "tags": []string{"tag:exit"}})
	creds, status := h.enroll(single.Key, nil)
	if status != http.StatusCreated {
		t.Fatalf("enroll: status %d", status)
	}
	if !slices.Equal(creds.Roles, exitRoles) || !slices.Equal(creds.Tags, []string{"tag:exit"}) || creds.Network != "testnet" ||
		!strings.HasPrefix(creds.PeerToken, control.PeerTokenPrefix) || !strings.HasPrefix(creds.PeerID, "peer_") {
		t.Fatalf("credentials: %+v", creds)
	}
	if _, status := h.enroll(single.Key, nil); status != http.StatusUnauthorized {
		t.Fatalf("second use of a single-use key: status %d", status)
	}

	// Reusable, ephemeral, with a peer TTL.
	reusable := h.authKey("testnet", map[string]any{"reusable": true, "ephemeral": true, "peer_ttl_seconds": 60})
	var eph []control.Credentials
	for range 2 {
		c, status := h.enroll(reusable.Key, nil)
		if status != http.StatusCreated || !c.Ephemeral || c.ExpiresAt == nil || !slices.Equal(c.Roles, nodeRoles) {
			t.Fatalf("reusable enroll: status %d %+v", status, c)
		}
		eph = append(eph, c)
	}
	var keys struct {
		AuthKeys []httpapi.AuthKeyView `json:"auth_keys"`
	}
	h.ctl("GET", "/control/networks/testnet/auth-keys", nil, &keys, http.StatusOK)
	if len(keys.AuthKeys) != 2 {
		t.Fatalf("auth keys: %+v", keys.AuthKeys)
	}
	for _, k := range keys.AuthKeys {
		if k.ID == reusable.ID && (k.Uses != 2 || !k.Usable) || k.ID == single.ID && (k.Uses != 1 || k.Usable) || k.Key != "" {
			t.Fatalf("auth key view: %+v", k)
		}
	}

	// A revoked key stops working.
	h.ctl("DELETE", "/control/networks/testnet/auth-keys/"+reusable.ID, nil, nil, http.StatusOK)
	if _, status := h.enroll(reusable.Key, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked key: status %d", status)
	}
	// Tags must be defined.
	if status := h.do("POST", "/control/networks/testnet/auth-keys", serviceToken, map[string]any{"tags": []string{"tag:nope"}}, nil); status != http.StatusBadRequest {
		t.Fatalf("undefined tag: status %d", status)
	}

	// Key expiry, and peer expiry from the key's peer TTL.
	expiring := h.authKey("testnet", map[string]any{"expires_in_seconds": 60})
	h.clock.Advance(2 * time.Minute)
	if _, status := h.enroll(expiring.Key, nil); status != http.StatusUnauthorized {
		t.Fatalf("expired key: status %d", status)
	}
	if e := h.dial(eph[0]).error(proto.ErrCodeExpired); !e.Fatal {
		t.Fatalf("expired peer: %+v", e)
	}

	// The janitor deletes ephemeral peers offline past the grace period.
	h.clock.Advance(10 * time.Minute)
	h.srv.Service.Sweep(context.Background())
	if status := h.do("GET", "/control/peers/"+eph[1].PeerID, serviceToken, nil, nil); status != http.StatusNotFound {
		t.Fatalf("ephemeral peer after sweep: status %d", status)
	}
	if status := h.do("GET", "/control/peers/"+creds.PeerID, serviceToken, nil, nil); status != http.StatusOK {
		t.Fatalf("non-ephemeral peer after sweep: status %d", status)
	}
}

func TestEnrollInteractive(t *testing.T) {
	h := newHarness(t, nil)
	start := func(name string) control.StartedEnrollment {
		var st control.StartedEnrollment
		if status := h.do("POST", "/v1/enroll/interactive", "", map[string]any{
			"network": "testnet", "name": name, "public_key": randomKey(t),
		}, &st); status != http.StatusCreated || st.Code == "" || st.PollToken == "" || st.IntervalSeconds <= 0 {
			t.Fatalf("start: status %d %+v", status, st)
		}
		return st
	}
	poll := func(token string) control.PollResult {
		var res control.PollResult
		if status := h.do("POST", "/v1/enroll/poll", "", map[string]any{"poll_token": token}, &res); status != http.StatusOK {
			t.Fatalf("poll: status %d", status)
		}
		return res
	}

	st := start("laptop")
	if res := poll(st.PollToken); res.Status != store.EnrollmentPending || res.Credentials != nil {
		t.Fatalf("pending poll: %+v", res)
	}
	var pending struct {
		Enrollments []control.EnrollmentView `json:"enrollments"`
	}
	h.ctl("GET", "/control/networks/testnet/enrollments?status=pending", nil, &pending, http.StatusOK)
	if len(pending.Enrollments) != 1 || pending.Enrollments[0].Name != "laptop" {
		t.Fatalf("pending enrolments: %+v", pending)
	}
	var view control.EnrollmentView
	// Codes are case- and separator-insensitive.
	h.ctl("GET", "/control/enrollments/"+strings.ToLower(strings.ReplaceAll(st.Code, "-", "")), nil, &view, http.StatusOK)
	h.ctl("POST", "/control/enrollments/"+st.Code+"/approve", map[string]any{"roles": []string{"hub"}}, &view, http.StatusOK)
	if view.Status != store.EnrollmentApproved {
		t.Fatalf("approve: %+v", view)
	}
	res := poll(st.PollToken)
	if res.Status != store.EnrollmentClaimed || res.Credentials == nil || !slices.Equal(res.Credentials.Roles, hubRoles) {
		t.Fatalf("claim: %+v", res)
	}
	if again := poll(st.PollToken); again.Status != store.EnrollmentClaimed || again.Credentials != nil {
		t.Fatalf("second poll must not return credentials again: %+v", again)
	}
	if w := h.dial(*res.Credentials).welcome(); w.PeerID != res.Credentials.PeerID {
		t.Fatalf("welcome: %+v", w)
	}

	denied := start("stranger")
	h.ctl("POST", "/control/enrollments/"+denied.Code+"/deny", map[string]any{"reason": "unknown machine"}, nil, http.StatusOK)
	if res := poll(denied.PollToken); res.Status != store.EnrollmentDenied || res.Reason != "unknown machine" || res.Credentials != nil {
		t.Fatalf("denied poll: %+v", res)
	}
	h.ctl("POST", "/control/enrollments/"+denied.Code+"/approve", map[string]any{}, nil, http.StatusBadRequest)
	if status := h.do("POST", "/v1/enroll/poll", "", map[string]any{"poll_token": "gpl_bogus"}, nil); status != http.StatusUnauthorized {
		t.Fatalf("unknown poll token: status %d", status)
	}

	expiring := start("slow")
	h.clock.Advance(time.Hour)
	if res := poll(expiring.PollToken); res.Status != store.EnrollmentExpired {
		t.Fatalf("expired enrolment: %+v", res)
	}
}

func TestNetmapSnapshotAndDeltas(t *testing.T) {
	h := newHarness(t, nil)
	hubC := h.peer("testnet", "hub", hubRoles)
	nodeC := h.peer("testnet", "node", nodeRoles)

	hub := h.joined(hubC)
	if len(hub.netmap.Peers) != 0 || hub.netmap.Self.PeerID != hubC.PeerID || hub.netmap.Isolation != proto.IsolationNone {
		t.Fatalf("hub's first netmap: %+v", hub.netmap)
	}
	node := h.joined(nodeC)
	nm := *node.netmap
	if ids := netmapIDs(nm); !slices.Equal(ids, []string{hubC.PeerID}) {
		t.Fatalf("node netmap peers: %v", ids)
	}
	hubInfo, _ := netmapPeer(nm, hubC.PeerID)
	if !hubInfo.Online || hubInfo.PublicKey == "" || !strings.HasPrefix(hubInfo.Address, "100.") || !slices.Equal(hubInfo.Roles, hubRoles) {
		t.Fatalf("hub entry: %+v", hubInfo)
	}
	if nm.Policy == nil || nm.Policy.Allow == nil || len(nm.Policy.Allow) != 0 {
		t.Fatalf("default exit policy must deny everything: %+v", nm.Policy)
	}
	if nm.Filter == nil || len(nm.Filter.Rules) != 1 || !slices.Equal(nm.Filter.Rules[0].Src, []string{hubInfo.Address}) {
		t.Fatalf("node filter must admit its hub: %+v", nm.Filter)
	}

	// Deltas: the node comes online, goes offline, then is removed.
	hub.waitNetmap("node online", func(n proto.Netmap) bool {
		p, ok := netmapPeer(n, nodeC.PeerID)
		return ok && p.Online
	})
	seq := hub.netmap.Seq
	_ = node.c.Close()
	hub.waitNetmap("node offline", func(n proto.Netmap) bool {
		p, ok := netmapPeer(n, nodeC.PeerID)
		return ok && !p.Online
	})
	h.ctl("POST", "/control/peers/"+nodeC.PeerID+"/revoke", nil, nil, http.StatusOK)
	hub.waitNetmap("node removed", func(n proto.Netmap) bool {
		_, ok := netmapPeer(n, nodeC.PeerID)
		return !ok
	})
	if hub.netmap.Seq <= seq || hub.snapshots != 1 {
		t.Fatalf("hub got seq %d (from %d) and %d snapshots; want increasing deltas after one snapshot",
			hub.netmap.Seq, seq, hub.snapshots)
	}
}

// TestACL: with the default ACL, hub -> exit is allowed and signals relay;
// node -> node and exit -> exit are denied and audited.
func TestACL(t *testing.T) {
	h := newHarness(t, nil)
	h.defineTags("testnet", "tag:exit")
	hubC := h.peer("testnet", "hub", hubRoles)
	exit1C := h.peer("testnet", "exit1", exitRoles, "tag:exit")
	exit2C := h.peer("testnet", "exit2", exitRoles, "tag:exit")
	nodeC := h.peer("testnet", "node", nodeRoles)
	hub := h.joined(hubC)
	exit1 := h.joined(exit1C)
	exit2 := h.joined(exit2C)
	node := h.joined(nodeC)

	for _, s := range []*sigClient{exit1, exit2, node} {
		if ids := netmapIDs(*s.netmap); !slices.Equal(ids, []string{hubC.PeerID}) {
			t.Fatalf("%s sees %v, want only the hub", s.id, ids)
		}
	}
	hub.waitNetmap("every peer", func(n proto.Netmap) bool { return len(n.Peers) == 3 })

	// hub -> exit relays, and back.
	hub.offer(exit1C.PeerID)
	got := exit1.next("offer from hub", 5*time.Second, func(e signal.Event) bool { return e.Signal != nil })
	if got.Signal.From != hubC.PeerID || got.Signal.Network != "testnet" {
		t.Fatalf("relayed offer: %+v", got.Signal)
	}
	exit1.offer(hubC.PeerID)
	hub.next("offer from exit", 5*time.Second, func(e signal.Event) bool { return e.Signal != nil && e.Signal.From == exit1C.PeerID })

	// exit -> exit and node -> exit are refused and audited once per pair.
	exit1.offer(exit2C.PeerID)
	exit1.error(proto.ErrCodeForbidden)
	exit1.offer(exit2C.PeerID)
	exit1.error(proto.ErrCodeForbidden)
	node.offer(exit1C.PeerID)
	node.error(proto.ErrCodeForbidden)
	denied := h.audit("testnet", "signal.denied")
	if len(denied) != 2 {
		t.Fatalf("signal.denied audit entries: %+v", denied)
	}
	for _, e := range denied {
		if e.Target == exit1C.PeerID && e.Detail["to"] != exit2C.PeerID || e.Target == nodeC.PeerID && e.Detail["to"] != exit1C.PeerID {
			t.Fatalf("audit entry: %+v", e)
		}
	}
}

func TestPolicyPush(t *testing.T) {
	h := newHarness(t, nil)
	h.defineTags("testnet", "tag:exit")
	hub := h.joined(h.peer("testnet", "hub", hubRoles))
	exit := h.joined(h.peer("testnet", "exit", exitRoles, "tag:exit"))
	rev := exit.netmap.Policy.Revision

	h.ctl("PUT", "/control/networks/testnet/exit-policies/web", map[string]any{
		"target": []string{"tag:exit"}, "allow": []string{"api.example.com:443"}, "daily_bytes": 1000,
	}, nil, http.StatusOK)
	p := exit.waitNetmap("pushed policy", func(n proto.Netmap) bool {
		return slices.Equal(n.Policy.Allow, []string{"api.example.com:443"})
	}).Policy
	if p.DailyBytes != 1000 || p.Revision <= rev || p.Network != "testnet" {
		t.Fatalf("pushed policy: %+v", p)
	}
	h.ctl("PUT", "/control/networks/testnet/exit-policies/web", map[string]any{
		"target": []string{"tag:exit"}, "allow": []string{"api.example.com:443"}, "paused": true,
	}, nil, http.StatusOK)
	exit.waitNetmap("paused", func(n proto.Netmap) bool { return n.Policy.Paused })
	h.ctl("DELETE", "/control/networks/testnet/exit-policies/web", nil, nil, http.StatusOK)
	exit.waitNetmap("policy removed", func(n proto.Netmap) bool { return len(n.Policy.Allow) == 0 && !n.Policy.Paused })
	if hub.netmap.Policy == nil || len(hub.netmap.Policy.Allow) != 0 {
		t.Fatalf("the hub is not an exit target: %+v", hub.netmap.Policy)
	}
	// An open-proxy allowlist is rejected.
	h.ctl("PUT", "/control/networks/testnet/exit-policies/bad", map[string]any{
		"target": []string{"*"}, "allow": []string{"*:443"},
	}, nil, http.StatusBadRequest)
}

func TestRevokeDisconnects(t *testing.T) {
	h := newHarness(t, nil)
	hubC := h.peer("testnet", "hub", hubRoles)
	nodeC := h.peer("testnet", "node", nodeRoles)
	hub := h.joined(hubC)
	node := h.joined(nodeC)
	hub.waitNetmap("node", func(n proto.Netmap) bool { _, ok := netmapPeer(n, nodeC.PeerID); return ok })
	w := h.watch("network=testnet&types=peer.")

	var view httpapi.PeerView
	h.ctl("POST", "/control/peers/"+nodeC.PeerID+"/revoke", nil, &view, http.StatusOK)
	if view.Status != store.PeerRevoked || view.RevokedAt == nil {
		t.Fatalf("revoke: %+v", view)
	}
	if e := node.error(proto.ErrCodeRevoked); !e.Fatal {
		t.Fatalf("revoke error: %+v", e)
	}
	w.next(events.PeerRevoked, nodeC.PeerID)
	w.next(events.PeerOffline, nodeC.PeerID)
	if h.srv.Relay.Online(nodeC.PeerID) {
		t.Fatal("revoked peer still online")
	}
	hub.waitNetmap("node removed", func(n proto.Netmap) bool { _, ok := netmapPeer(n, nodeC.PeerID); return !ok })
	if e := node.error(proto.ErrCodeRevoked); !e.Fatal { // the reconnect is refused too
		t.Fatalf("reconnect: %+v", e)
	}

	// Expiry disconnects the same way and can be lifted.
	otherC := h.peer("testnet", "other", nodeRoles)
	other := h.joined(otherC)
	h.ctl("POST", "/control/peers/"+otherC.PeerID+"/expire", nil, nil, http.StatusOK)
	other.error(proto.ErrCodeExpired)
	w.next(events.PeerExpired, otherC.PeerID)
	h.ctl("PATCH", "/control/peers/"+otherC.PeerID, map[string]any{"clear_expiry": true}, nil, http.StatusOK)
	other.welcome()
}

func TestWatchStream(t *testing.T) {
	h := newHarness(t, nil)
	w := h.watch("network=testnet")
	other := h.watch("network=othernet")

	hubC := h.peer("testnet", "hub", hubRoles)
	w.next(events.Audit, "") // the audit entry is published first
	enrolled := w.next(events.PeerEnrolled, hubC.PeerID)
	hub := h.joined(hubC)
	w.next(events.PeerOnline, hubC.PeerID)
	w.next(events.NetmapUpdated, "")

	hub.setHealth(&proto.Health{
		Links:     []proto.LinkHealth{{PeerID: "peer_x", State: proto.LinkConnected, CandidateType: "srflx", RTTSeconds: 0.02}},
		Endpoints: []string{"203.0.113.7:4242"},
	})
	if err := hub.c.SendHeartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := w.next(events.PeerHealth, hubC.PeerID)
	if !strings.Contains(fmt.Sprint(e.Data), "srflx") {
		t.Fatalf("health event data: %+v", e.Data)
	}

	// Another network's watcher sees only its own network.
	othC := h.peer("othernet", "n", nodeRoles)
	if e := other.next(events.PeerEnrolled, ""); e.PeerID != othC.PeerID || e.Network != "othernet" {
		t.Fatalf("othernet watcher got %+v", e)
	}

	// Resuming replays what the ring still holds after the given sequence.
	resumed := h.watch(fmt.Sprintf("network=testnet&types=peer.online&since=%d", enrolled.Seq))
	resumed.next(events.PeerOnline, hubC.PeerID)

	// A key confined to othernet cannot watch testnet.
	var key httpapi.APIKeyView
	h.ctl("POST", "/control/api-keys", map[string]any{"name": "w", "scopes": []string{"watch"}, "networks": []string{"othernet"}}, &key, http.StatusCreated)
	if status := h.do("GET", "/control/watch?network=testnet", key.Key, nil, nil); status != http.StatusNotFound {
		t.Fatalf("scoped watch of another network: status %d", status)
	}
}

func TestPeerHealth(t *testing.T) {
	h := newHarness(t, nil)
	hubC := h.peer("testnet", "hub", hubRoles)
	hub := h.joined(hubC)
	hub.setHealth(&proto.Health{
		Links: []proto.LinkHealth{
			{PeerID: "peer_a", State: proto.LinkConnected, CandidateType: "host", RTTSeconds: 0.001, HandshakeAgeSeconds: 4, RxBytes: 900, TxBytes: 800},
			{PeerID: "peer_b", State: proto.LinkFailed},
		},
		Exit:      &proto.ExitHealth{CapUsedBytes: 1234, CapLimitBytes: 5000, Paused: true},
		Endpoints: []string{"198.51.100.2:5000"},
	})
	if err := hub.c.SendHeartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	var hv httpapi.HealthView
	for deadline := time.Now().Add(5 * time.Second); hv.Health == nil && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		h.ctl("GET", "/control/peers/"+hubC.PeerID+"/health", nil, &hv, http.StatusOK)
	}
	if hv.Health == nil || !hv.Online || hv.HealthAt == nil || len(hv.Health.Links) != 2 {
		t.Fatalf("health: %+v", hv)
	}
	l := hv.Health.Links[0]
	if l.State != proto.LinkConnected || l.CandidateType != "host" || l.RTTSeconds != 0.001 || l.HandshakeAgeSeconds != 4 ||
		l.RxBytes != 900 || l.TxBytes != 800 || hv.Health.Exit.CapUsedBytes != 1234 || !hv.Health.Exit.Paused {
		t.Fatalf("health fields: %+v %+v", l, hv.Health.Exit)
	}
	var fleet struct {
		Totals httpapi.FleetTotals `json:"totals"`
	}
	h.ctl("GET", "/control/health?network=testnet", nil, &fleet, http.StatusOK)
	ft := fleet.Totals
	if ft.Peers != 1 || ft.Online != 1 || ft.LinksConnected != 1 || ft.LinksFailed != 1 || ft.ByCandidateType["host"] != 1 ||
		ft.ExitsPaused != 1 || ft.CapUsedBytes != 1234 {
		t.Fatalf("fleet totals: %+v", ft)
	}
	var pv httpapi.PeerView
	h.ctl("GET", "/control/peers/"+hubC.PeerID, nil, &pv, http.StatusOK)
	if !slices.Equal(pv.Endpoints, []string{"198.51.100.2:5000"}) || pv.Session == nil || pv.LastSeen == nil {
		t.Fatalf("peer view: %+v", pv)
	}
}

// TestHubOnlyIsolation covers the control plane's isolation layers: a non-hub
// peer's netmap and deltas name only hubs (layer 1), and signalling refuses
// and audits any other pair (layer 2). The mesh ACL would otherwise let every
// peer see every other.
func TestHubOnlyIsolation(t *testing.T) {
	h := newHarness(t, nil)
	h.ctl("POST", "/control/networks", map[string]any{"name": "iso"}, nil, http.StatusCreated)
	h.meshACL("iso")
	h.defineTags("iso", "tag:exit")
	hubC := h.peer("iso", "hub", hubRoles)
	exit1C := h.peer("iso", "exit1", exitRoles, "tag:exit")
	nodeC := h.peer("iso", "node", nodeRoles)
	hub := h.joined(hubC)
	exit1 := h.joined(exit1C)
	node := h.joined(nodeC)
	exit1.waitNetmap("mesh", func(n proto.Netmap) bool { return len(n.Peers) == 2 })

	h.ctl("PATCH", "/control/networks/iso", map[string]any{"isolation": "bogus"}, nil, http.StatusBadRequest)
	h.ctl("PATCH", "/control/networks/iso", map[string]any{"isolation": "hub-only"}, nil, http.StatusOK)
	for _, s := range []*sigClient{exit1, node} {
		n := s.waitNetmap("hub-only", func(n proto.Netmap) bool {
			return n.Isolation == proto.IsolationHubOnly && slices.Equal(netmapIDs(n), []string{hubC.PeerID})
		})
		if len(n.Filter.Rules) != 1 || !slices.Equal(n.Filter.Rules[0].Src, []string{hub.netmap.Self.Address}) {
			t.Fatalf("%s filter under hub-only: %+v", s.id, n.Filter)
		}
	}
	mark := len(exit1.frames)

	// A peer joining under hub-only never learns of the other non-hub peers,
	// and they never learn of it.
	exit2C := h.peer("iso", "exit2", exitRoles, "tag:exit")
	exit2 := h.joined(exit2C)
	hub.waitNetmap("all", func(n proto.Netmap) bool { return len(n.Peers) == 3 })
	exit2.waitNetmap("hub", func(n proto.Netmap) bool { return slices.Equal(netmapIDs(n), []string{hubC.PeerID}) })

	// Layer 2: exit -> exit is refused and audited; hub -> exit relays.
	exit2.offer(exit1C.PeerID)
	exit2.error(proto.ErrCodeForbidden)
	exit1.offer(exit2C.PeerID)
	exit1.error(proto.ErrCodeForbidden)
	hub.offer(exit2C.PeerID)
	exit2.next("offer from hub", 5*time.Second, func(e signal.Event) bool { return e.Signal != nil && e.Signal.From == hubC.PeerID })
	denied := h.audit("iso", "signal.denied")
	if len(denied) != 2 {
		t.Fatalf("signal.denied audit entries: %+v", denied)
	}

	// Layer 1, checked on the wire: no frame names another non-hub peer.
	for _, f := range exit2.frames {
		if strings.Contains(f, exit1C.PeerID) && !strings.Contains(f, `"code":"forbidden"`) || strings.Contains(f, nodeC.PeerID) {
			t.Fatalf("exit2 received a frame naming another non-hub peer: %s", f)
		}
	}
	for _, f := range exit1.frames[mark:] {
		if strings.Contains(f, exit2C.PeerID) && !strings.Contains(f, `"code":"forbidden"`) {
			t.Fatalf("exit1 received a frame naming exit2: %s", f)
		}
	}
	// The control API still sees the whole network.
	var peers struct {
		Peers []httpapi.PeerView `json:"peers"`
	}
	h.ctl("GET", "/control/peers?network=iso", nil, &peers, http.StatusOK)
	if len(peers.Peers) != 4 {
		t.Fatalf("control API peers: %d", len(peers.Peers))
	}
}

func TestControlAPI(t *testing.T) {
	h := newHarness(t, nil)
	if status := h.do("GET", "/control/networks", "", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", status)
	}
	if status := h.do("GET", "/control/networks", "gck_wrong", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("bad token: status %d", status)
	}

	// Networks.
	var nv httpapi.NetworkView
	h.ctl("POST", "/control/networks", map[string]any{"name": "lab", "pool": "100.100.0.0/24", "isolation": "hub-only"}, &nv, http.StatusCreated)
	if nv.Isolation != "hub-only" || nv.Pool != "100.100.0.0/24" {
		t.Fatalf("network: %+v", nv)
	}
	h.ctl("POST", "/control/networks", map[string]any{"name": "lab"}, nil, http.StatusConflict)
	h.ctl("POST", "/control/networks", map[string]any{"name": "Bad Name"}, nil, http.StatusBadRequest)
	h.ctl("PATCH", "/control/networks/lab", map[string]any{"isolation": "none"}, &nv, http.StatusOK)
	if nv.Isolation != "none" {
		t.Fatalf("patch network: %+v", nv)
	}
	var list struct {
		Networks []httpapi.NetworkView `json:"networks"`
	}
	h.ctl("GET", "/control/networks", nil, &list, http.StatusOK)
	if len(list.Networks) != 3 {
		t.Fatalf("networks: %+v", list)
	}

	// Tags, ACLs and exit policies.
	h.defineTags("lab", "tag:exit")
	var pol httpapi.PolicyView
	h.ctl("GET", "/control/networks/lab/policy", nil, &pol, http.StatusOK)
	if _, ok := pol.Policy.Tags["tag:exit"]; !ok || len(pol.Policy.ACLs) != 1 {
		t.Fatalf("policy: %+v", pol)
	}
	h.ctl("PUT", "/control/networks/lab/acls", map[string]any{
		"acls": []map[string]any{{"action": "accept", "src": []string{"everyone"}, "dst": []string{"*"}}},
	}, nil, http.StatusBadRequest)
	h.ctl("PUT", "/control/networks/lab/acls", map[string]any{
		"acls": []map[string]any{{"action": "accept", "src": []string{"role:hub"}, "dst": []string{"tag:exit"}, "ports": []int{1080}}},
	}, &pol, http.StatusOK)
	if len(pol.Policy.ACLs) != 1 || pol.Policy.ACLs[0].Ports[0] != 1080 {
		t.Fatalf("acls: %+v", pol.Policy.ACLs)
	}
	h.ctl("PUT", "/control/networks/lab/exit-policies/web", map[string]any{"target": []string{"tag:exit"}, "allow": []string{"a.example:443"}}, nil, http.StatusOK)
	var exits struct {
		Exit []map[string]any `json:"exit"`
	}
	h.ctl("GET", "/control/networks/lab/exit-policies", nil, &exits, http.StatusOK)
	if len(exits.Exit) != 1 {
		t.Fatalf("exit policies: %+v", exits)
	}

	// Peers.
	creds := h.peer("lab", "e", exitRoles, "tag:exit")
	h.ctl("DELETE", "/control/networks/lab/tags/tag:exit", nil, nil, http.StatusBadRequest) // an ACL still references it
	h.ctl("DELETE", "/control/networks/lab", nil, nil, http.StatusConflict)                 // still has peers
	var pv httpapi.PeerView
	h.ctl("PATCH", "/control/peers/"+creds.PeerID, map[string]any{"name": "renamed", "roles": []string{"hub"}, "tags": []string{}}, &pv, http.StatusOK)
	if pv.Name != "renamed" || !slices.Equal(pv.Roles, hubRoles) || len(pv.Tags) != 0 {
		t.Fatalf("patch peer: %+v", pv)
	}
	var peers struct {
		Peers []httpapi.PeerView `json:"peers"`
	}
	h.ctl("GET", "/control/peers?network=lab&role=hub", nil, &peers, http.StatusOK)
	if len(peers.Peers) != 1 {
		t.Fatalf("list peers: %+v", peers)
	}
	h.ctl("POST", "/control/peers/"+creds.PeerID+"/move", map[string]any{"network": "othernet"}, &pv, http.StatusOK)
	if pv.Network != "othernet" || pv.Address != "" {
		t.Fatalf("move: %+v", pv)
	}
	h.ctl("DELETE", "/control/peers/"+creds.PeerID, nil, nil, http.StatusNoContent)
	h.ctl("GET", "/control/peers/"+creds.PeerID, nil, nil, http.StatusNotFound)
	h.ctl("DELETE", "/control/networks/lab/exit-policies/web", nil, nil, http.StatusOK)
	h.meshACL("lab")
	h.ctl("DELETE", "/control/networks/lab/tags/tag:exit", nil, nil, http.StatusOK)
	h.ctl("DELETE", "/control/networks/lab", nil, nil, http.StatusNoContent)
	h.ctl("GET", "/control/networks/lab", nil, nil, http.StatusNotFound)

	// A scoped API key sees only its network and scopes.
	testPeer := h.peer("testnet", "t", nodeRoles)
	othPeer := h.peer("othernet", "o", nodeRoles)
	var key httpapi.APIKeyView
	h.ctl("POST", "/control/api-keys", map[string]any{"name": "ro", "scopes": []string{"peers:read"}, "networks": []string{"othernet"}}, &key, http.StatusCreated)
	if !strings.HasPrefix(key.Key, control.APIKeyPrefix) {
		t.Fatalf("api key: %+v", key)
	}
	peers.Peers = nil
	if status := h.do("GET", "/control/peers", key.Key, nil, &peers); status != http.StatusOK || len(peers.Peers) != 1 || peers.Peers[0].ID != othPeer.PeerID {
		t.Fatalf("scoped list: status %d %+v", status, peers)
	}
	if status := h.do("GET", "/control/peers/"+testPeer.PeerID, key.Key, nil, nil); status != http.StatusNotFound {
		t.Fatalf("scoped get of another network's peer: status %d", status)
	}
	if status := h.do("POST", "/control/peers/"+othPeer.PeerID+"/revoke", key.Key, nil, nil); status != http.StatusForbidden {
		t.Fatalf("revoke without peers:write: status %d", status)
	}
	if status := h.do("GET", "/control/api-keys", key.Key, nil, nil); status != http.StatusForbidden {
		t.Fatalf("api keys without admin: status %d", status)
	}
	h.ctl("DELETE", "/control/api-keys/"+key.ID, nil, nil, http.StatusOK)
	if status := h.do("GET", "/control/peers", key.Key, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked api key: status %d", status)
	}

	var stats httpapi.Stats
	h.ctl("GET", "/control/stats", nil, &stats, http.StatusOK)
	if stats.Networks != 2 || stats.Peers != 2 {
		t.Fatalf("stats: %+v", stats)
	}
	if len(h.audit("othernet", "peer.moved")) != 1 {
		t.Fatal("peer.moved not audited")
	}
}

func TestHelloRejections(t *testing.T) {
	h := newHarness(t, nil)
	if e := h.dial(control.Credentials{PeerToken: "gpt_bogus"}).error(proto.ErrCodeUnauthorized); !e.Fatal {
		t.Fatalf("bad token: %+v", e)
	}
	nodeC := h.peer("testnet", "n", nodeRoles)
	s := h.dial(nodeC)
	s.welcome()
	if err := s.c.Join(context.Background(), "othernet"); err != nil {
		t.Fatal(err)
	}
	s.error(proto.ErrCodeForbidden)
	s.offer("peer_x")
	s.error(proto.ErrCodeBadRequest) // not joined yet
}

func TestTURNCredentialsInWelcome(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.ICE.STUNURLs = []string{"stun:127.0.0.1:3478"}
		c.ICE.TURNURLs = []string{"turn:127.0.0.1:3478"}
		c.ICE.TURNSecret = "coturn-secret"
	})
	w := h.dial(h.peer("testnet", "n", nodeRoles)).welcome()
	if len(w.ICEServers) != 2 || w.ICEServers[1].Username == "" || w.ICEServers[1].Credential == "" {
		t.Fatalf("ice servers: %+v", w.ICEServers)
	}
	if !strings.HasSuffix(w.ICEServers[1].Username, ":"+w.PeerID) {
		t.Fatalf("TURN username %q must end with the peer id", w.ICEServers[1].Username)
	}
}
