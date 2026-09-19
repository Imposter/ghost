package server_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
)

// TestAuthorizerRequestContext: every authorizer request carries the peer's
// WireGuard public key, when known, and how it enrolled (with the pre-auth
// key id for auth_key enrolments).
func TestAuthorizerRequestContext(t *testing.T) {
	fa := newFakeAuthorizer(t, func(access.Request) access.Decision { return access.Decision{Allow: true} })
	h := newHarness(t, apiMode(fa.srv.URL, authzSecret))
	h.meshACL("testnet")

	// Pre-auth key, with a public key at enrolment.
	key := h.authKey("testnet", nil)
	pub := randomKey(t)
	var keyed control.Credentials
	if status := h.do("POST", "/v1/enroll", "", map[string]any{"auth_key": key.Key, "public_key": pub}, &keyed); status != http.StatusCreated {
		t.Fatalf("enroll: status %d", status)
	}
	enrolls := fa.calls(access.ActionEnroll)
	if len(enrolls) != 1 || enrolls[0].PublicKey != pub || enrolls[0].EnrollmentMethod != access.EnrollAuthKey ||
		enrolls[0].AuthKeyID != key.ID {
		t.Fatalf("auth_key enroll request: %+v", enrolls)
	}

	// Direct creation, without a key.
	direct := h.peer("testnet", "direct", nodeRoles)
	enrolls = fa.calls(access.ActionEnroll)
	if last := enrolls[len(enrolls)-1]; last.PublicKey != "" || last.EnrollmentMethod != access.EnrollDirect || last.AuthKeyID != "" {
		t.Fatalf("direct enroll request: %+v", last)
	}

	// Interactive, with the key given at start.
	status, st, msg := h.startInteractive("testnet", "laptop")
	if status != http.StatusCreated {
		t.Fatalf("start interactive: %d %s", status, msg)
	}
	h.ctl("POST", "/control/enrollments/"+st.Code+"/approve", map[string]any{}, nil, http.StatusOK)
	if status, res, msg := h.pollInteractive(st.PollToken); status != http.StatusOK || res.Credentials == nil {
		t.Fatalf("claim: %d %+v %s", status, res, msg)
	}
	enrolls = fa.calls(access.ActionEnroll)
	if last := enrolls[len(enrolls)-1]; last.PublicKey == "" || last.EnrollmentMethod != access.EnrollInteractive || last.AuthKeyID != "" {
		t.Fatalf("interactive enroll request: %+v", last)
	}

	// The method is kept on the peer and shown by the control API.
	var pv httpapi.PeerView
	h.ctl("GET", "/control/peers/"+keyed.PeerID, nil, &pv, http.StatusOK)
	if pv.EnrollmentMethod != "auth_key" || pv.AuthKeyID != key.ID {
		t.Fatalf("peer view: %+v", pv)
	}

	// connect and connect_peer carry the key presented in hello and the method.
	directS := h.joined(direct)
	keyedS := h.joined(keyed)
	var directView, keyedView httpapi.PeerView
	h.ctl("GET", "/control/peers/"+direct.PeerID, nil, &directView, http.StatusOK)
	h.ctl("GET", "/control/peers/"+keyed.PeerID, nil, &keyedView, http.StatusOK)
	connects := fa.calls(access.ActionConnect)
	find := func(reqs []access.Request, peer string) access.Request {
		for _, r := range reqs {
			if r.Peer == peer {
				return r
			}
		}
		t.Fatalf("no request for %s in %+v", peer, reqs)
		return access.Request{}
	}
	if r := find(connects, direct.PeerID); r.PublicKey != directView.PublicKey || r.PublicKey == "" ||
		r.EnrollmentMethod != access.EnrollDirect || r.AuthKeyID != "" {
		t.Fatalf("direct connect request: %+v", r)
	}
	if r := find(connects, keyed.PeerID); r.PublicKey != keyedView.PublicKey || r.EnrollmentMethod != access.EnrollAuthKey ||
		r.AuthKeyID != key.ID {
		t.Fatalf("auth_key connect request: %+v", r)
	}
	directS.waitNetmap("keyed peer", func(n proto.Netmap) bool { _, ok := netmapPeer(n, keyed.PeerID); return ok })
	keyedS.waitNetmap("direct peer", func(n proto.Netmap) bool { _, ok := netmapPeer(n, direct.PeerID); return ok })
	directS.offer(keyed.PeerID)
	keyedS.next("offer", 5*time.Second, func(e signal.Event) bool { return e.Signal != nil })
	if r := find(fa.calls(access.ActionConnectPeer), direct.PeerID); r.PublicKey != directView.PublicKey ||
		r.EnrollmentMethod != access.EnrollDirect || r.Target != keyed.PeerID {
		t.Fatalf("connect_peer request: %+v", r)
	}
}

// TestReauthorize: the reauthorize routes re-ask the authorizer, uncached,
// about live sessions, and a denial disconnects the peer and keeps it out.
func TestReauthorize(t *testing.T) {
	var (
		mu     sync.Mutex
		paused = map[string]bool{}
	)
	fa := newFakeAuthorizer(t, func(r access.Request) access.Decision {
		mu.Lock()
		defer mu.Unlock()
		if r.Action == access.ActionConnect && paused[r.Peer] {
			return access.Decision{Allow: false, Reason: "paused"}
		}
		return access.Decision{Allow: true}
	})
	pause := func(id string, on bool) {
		mu.Lock()
		paused[id] = on
		mu.Unlock()
	}
	// A long cache TTL: the re-check must bypass it, and its denial must
	// replace the cached allow.
	h := newHarness(t, func(c *config.Config) {
		apiMode(fa.srv.URL, authzSecret)(c)
		c.Access.CacheTTL = config.Duration(time.Hour)
	})
	hubC := h.peer("testnet", "hub", hubRoles)
	aC := h.peer("testnet", "a", nodeRoles)
	bC := h.peer("testnet", "b", nodeRoles)
	idleC := h.peer("testnet", "idle", nodeRoles)
	hub := h.joined(hubC)
	a := h.joined(aC)
	b := h.joined(bC)
	hub.waitNetmap("a and b", func(n proto.Netmap) bool {
		pa, okA := netmapPeer(n, aC.PeerID)
		pb, okB := netmapPeer(n, bC.PeerID)
		return okA && okB && pa.Online && pb.Online
	})

	// One peer: a live session the authorizer now denies is closed at once.
	before := len(fa.calls(access.ActionConnect))
	pause(aC.PeerID, true)
	var res control.Reauthorization
	h.ctl("POST", "/control/peers/"+aC.PeerID+"/reauthorize", nil, &res, http.StatusOK)
	if !res.Online || res.Allowed || res.Reason != "paused" || !res.Disconnected || res.Network != "testnet" {
		t.Fatalf("reauthorize a: %+v", res)
	}
	if e := a.error(proto.ErrCodeForbidden); !e.Fatal || !strings.Contains(e.Message, "paused") {
		t.Fatalf("a's disconnect: %+v", e)
	}
	if after := len(fa.calls(access.ActionConnect)); after != before+1 {
		t.Fatalf("the re-check must ask the authorizer once, uncached: %d -> %d", before, after)
	}
	hub.waitNetmap("a offline", func(n proto.Netmap) bool { p, ok := netmapPeer(n, aC.PeerID); return ok && !p.Online })
	if len(h.audit("testnet", "peer.reauthorized")) != 1 {
		t.Fatal("reauthorize not audited")
	}
	denied := h.audit("testnet", "peer.connect_denied")
	if len(denied) != 1 || denied[0].Detail["on"] != "reauthorize" {
		t.Fatalf("connect denial audit: %+v", denied)
	}

	// The denial keeps it out: the client reconnects, and its join is refused.
	a.welcome()
	if err := a.c.Join(t.Context(), "testnet"); err != nil {
		t.Fatal(err)
	}
	if e := a.error(proto.ErrCodeForbidden); !strings.Contains(e.Message, "join denied: paused") {
		t.Fatalf("a's rejoin: %+v", e)
	}

	// An offline peer is not asked about.
	before = len(fa.calls(access.ActionConnect))
	h.ctl("POST", "/control/peers/"+idleC.PeerID+"/reauthorize", nil, &res, http.StatusOK)
	if res.Online || res.Allowed || res.Disconnected {
		t.Fatalf("reauthorize offline: %+v", res)
	}
	if after := len(fa.calls(access.ActionConnect)); after != before {
		t.Fatalf("an offline peer must not be asked about: %d -> %d", before, after)
	}

	// The network: every live peer is re-checked.
	pause(bC.PeerID, true)
	var nres struct {
		Network string                    `json:"network"`
		Peers   []control.Reauthorization `json:"peers"`
	}
	h.ctl("POST", "/control/networks/testnet/reauthorize", nil, &nres, http.StatusOK)
	got := map[string]control.Reauthorization{}
	for _, r := range nres.Peers {
		got[r.PeerID] = r
	}
	if nres.Network != "testnet" || len(got) != 2 || !got[hubC.PeerID].Allowed || got[hubC.PeerID].Disconnected ||
		got[bC.PeerID].Allowed || !got[bC.PeerID].Disconnected {
		t.Fatalf("reauthorize network: %+v", nres)
	}
	if e := b.error(proto.ErrCodeForbidden); !e.Fatal {
		t.Fatalf("b's disconnect: %+v", e)
	}
	if len(h.audit("testnet", "network.reauthorized")) != 1 {
		t.Fatal("network reauthorize not audited")
	}

	// Allowing again: reauthorizing the offline peer drops its cached denial,
	// so its next join is asked afresh.
	pause(aC.PeerID, false)
	h.ctl("POST", "/control/peers/"+aC.PeerID+"/reauthorize", nil, &res, http.StatusOK)
	if err := a.c.Join(t.Context(), "testnet"); err != nil {
		t.Fatal(err)
	}
	a.next("joined", 5*time.Second, func(e signal.Event) bool { return e.Joined != nil })

	// Errors: unknown peer and network, a key without peers:write.
	h.ctl("POST", "/control/peers/peer_nope/reauthorize", nil, nil, http.StatusNotFound)
	h.ctl("POST", "/control/networks/nonet/reauthorize", nil, nil, http.StatusNotFound)
	var key struct {
		Key string `json:"key"`
	}
	h.ctl("POST", "/control/api-keys", map[string]any{"name": "ro", "scopes": []string{"peers:read"}}, &key, http.StatusCreated)
	if status := h.do("POST", "/control/peers/"+aC.PeerID+"/reauthorize", key.Key, nil, nil); status != http.StatusForbidden {
		t.Fatalf("reauthorize without peers:write: status %d", status)
	}
}

// TestReauthorizeOpenMode: with no authorizer there is nothing to ask, so both
// routes answer 409 and nobody is disconnected.
func TestReauthorizeOpenMode(t *testing.T) {
	h := newHarness(t, nil)
	nodeC := h.peer("testnet", "node", nodeRoles)
	h.joined(nodeC)
	h.ctl("POST", "/control/peers/"+nodeC.PeerID+"/reauthorize", nil, nil, http.StatusConflict)
	h.ctl("POST", "/control/networks/testnet/reauthorize", nil, nil, http.StatusConflict)
	h.ctl("POST", "/control/peers/peer_nope/reauthorize", nil, nil, http.StatusNotFound)
	if !h.srv.Relay.Online(nodeC.PeerID) {
		t.Fatal("open-mode reauthorize must not disconnect")
	}
}
