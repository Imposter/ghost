package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
)

const authzSecret = "authorizer-shared-secret-0123"

// fakeAuthorizer is an authorizer webhook on 127.0.0.1 that verifies every
// request with access.Verifier (signature, timestamp, single-use nonce) and
// answers with decide.
type fakeAuthorizer struct {
	t        *testing.T
	srv      *httptest.Server
	verifier *access.Verifier

	mu       sync.Mutex
	decide   func(access.Request) access.Decision
	accepted []access.Request
	rejected []error
	status   int
}

func newFakeAuthorizer(t *testing.T, decide func(access.Request) access.Decision) *fakeAuthorizer {
	t.Helper()
	fa := &fakeAuthorizer{t: t, decide: decide, verifier: access.NewVerifier([]byte(authzSecret), time.Minute, nil)}
	fa.srv = httptest.NewServer(http.HandlerFunc(fa.serve)) // binds 127.0.0.1:0
	t.Cleanup(fa.srv.Close)
	return fa
}

func (fa *fakeAuthorizer) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != access.AuthorizePath || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	req, err := fa.verifier.VerifyHTTP(r)
	fa.mu.Lock()
	status, decide := fa.status, fa.decide
	if err != nil {
		fa.rejected = append(fa.rejected, err)
	} else {
		fa.accepted = append(fa.accepted, req)
	}
	fa.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(decide(req))
}

func (fa *fakeAuthorizer) set(fn func(*fakeAuthorizer)) {
	fa.mu.Lock()
	fn(fa)
	fa.mu.Unlock()
}

func (fa *fakeAuthorizer) calls(action access.Action) []access.Request {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var out []access.Request
	for _, r := range fa.accepted {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

// TestAuthorizerJudgesBothSidesOfAPair: an authorizer is asked about a pair,
// so it is told what both sides are. Here it refuses any pair without a hub
// in it -- the pool's promise -- and the server enforces that even though the
// network's own ACLs and netmaps would let the two nodes reach each other.
// It is the layer that still holds when a mis-set isolation or an ACL lets a
// pair through.
func TestAuthorizerJudgesBothSidesOfAPair(t *testing.T) {
	fa := newFakeAuthorizer(t, func(r access.Request) access.Decision {
		if r.Action == access.ActionConnectPeer &&
			!proto.HasRole(r.Roles, proto.RoleHub) && !proto.HasRole(r.TargetRoles, proto.RoleHub) {
			return access.Decision{Allow: false, Reason: "the pool is hub-only"}
		}
		return access.Decision{Allow: true}
	})
	h := newHarness(t, apiMode(fa.srv.URL, authzSecret))
	h.ctl("POST", "/control/networks", map[string]any{"name": "pair"}, nil, http.StatusCreated)
	h.meshACL("pair") // every peer may see and signal every other
	hubC := h.peer("pair", "hub", hubRoles)
	node1C := h.peer("pair", "node1", nodeRoles)
	node2C := h.peer("pair", "node2", nodeRoles)
	hub := h.joined(hubC)
	node1 := h.joined(node1C)
	h.joined(node2C)
	node1.waitNetmap("mesh", func(n proto.Netmap) bool { return len(n.Peers) == 2 })

	// node -> node: in each other's netmap, and still refused.
	node1.offer(node2C.PeerID)
	if e := node1.error(proto.ErrCodeForbidden); !strings.Contains(e.Message, "hub-only") {
		t.Fatalf("node to node must be refused by the authorizer: %+v", e)
	}
	if len(h.audit("pair", "signal.denied")) != 1 {
		t.Error("a refused pair is audited")
	}

	// node -> hub: the same authorizer allows it, and it relays.
	node1.offer(hubC.PeerID)
	hub.next("offer from node1", 5*time.Second, func(e signal.Event) bool {
		return e.Signal != nil && e.Signal.From == node1C.PeerID
	})

	// Both requests named both sides.
	byTarget := map[string]access.Request{}
	for _, r := range fa.calls(access.ActionConnectPeer) {
		byTarget[r.Target] = r
	}
	toNode, ok := byTarget[node2C.PeerID]
	if !ok || !slices.Equal(toNode.Roles, nodeRoles) || !slices.Equal(toNode.TargetRoles, nodeRoles) {
		t.Errorf("connect_peer to a node: %+v", toNode)
	}
	toHub, ok := byTarget[hubC.PeerID]
	if !ok || !slices.Equal(toHub.Roles, nodeRoles) || !proto.HasRole(toHub.TargetRoles, proto.RoleHub) {
		t.Errorf("connect_peer to the hub: %+v", toHub)
	}
}

func apiMode(url, secret string) func(*config.Config) {
	return func(c *config.Config) {
		c.Access.Mode = config.AccessAPI
		c.Access.AuthorizerURL = url
		c.Access.AuthorizerSecret = secret
		c.Access.Timeout = config.Duration(500 * time.Millisecond)
	}
}

// TestAuthorizerPolicySource: in api mode the authorizer decides enrolment
// (adding tags and labels), connection (replacing the exit allowlist and
// caps) and peer-to-peer signalling, and is re-asked when policy changes.
func TestAuthorizerPolicySource(t *testing.T) {
	fa := newFakeAuthorizer(t, func(r access.Request) access.Decision {
		switch r.Action {
		case access.ActionEnroll:
			if r.Labels["user"] == "mallory" {
				return access.Decision{Allow: false, Reason: "not invited"}
			}
			return access.Decision{Allow: true, Policy: &access.Policy{Tags: []string{"tag:vetted"}, Labels: map[string]string{"owner": "alice"}}}
		case access.ActionConnect:
			return access.Decision{Allow: true, Policy: &access.Policy{
				ExitAllowlist: []string{"only.example:443"}, Caps: access.Caps{DailyBytes: 5}, Labels: map[string]string{"src": "authz"},
			}}
		case access.ActionConnectPeer:
			return access.Decision{Allow: false, Reason: "no direct links today"}
		}
		return access.Decision{}
	})
	h := newHarness(t, apiMode(fa.srv.URL, authzSecret))
	h.defineTags("testnet", "tag:vetted", "tag:exit")
	// The network policy would give exits another allowlist; the authorizer's wins.
	h.ctl("PUT", "/control/networks/testnet/exit-policies/web", map[string]any{
		"target": []string{"tag:exit"}, "allow": []string{"network.example:443"},
	}, nil, http.StatusOK)

	key := h.authKey("testnet", map[string]any{"reusable": true, "roles": []string{"exit", "node"}, "tags": []string{"tag:exit"}})
	exitC, status := h.enroll(key.Key, map[string]string{"user": "alice"})
	if status != http.StatusCreated || !slices.Equal(exitC.Tags, []string{"tag:exit", "tag:vetted"}) {
		t.Fatalf("enroll: status %d %+v", status, exitC)
	}
	var pv httpapi.PeerView
	h.ctl("GET", "/control/peers/"+exitC.PeerID, nil, &pv, http.StatusOK)
	if pv.Labels["owner"] != "alice" || pv.Labels["user"] != "alice" {
		t.Fatalf("labels: %+v", pv.Labels)
	}
	enrolls := fa.calls(access.ActionEnroll)
	if len(enrolls) != 1 || enrolls[0].Network != "testnet" || enrolls[0].Nonce == "" || enrolls[0].TS == 0 ||
		!slices.Contains(enrolls[0].Tags, "tag:exit") || enrolls[0].Labels["user"] != "alice" {
		t.Fatalf("enroll request: %+v", enrolls)
	}
	if _, status := h.enroll(key.Key, map[string]string{"user": "mallory"}); status != http.StatusForbidden {
		t.Fatalf("denied enroll: status %d", status)
	}
	if len(h.audit("testnet", "peer.enroll_denied")) != 1 {
		t.Fatal("denied enrolment not audited")
	}

	hub := h.joined(h.peer("testnet", "hub", hubRoles))
	exit := h.joined(exitC)
	p := exit.netmap.Policy
	if !slices.Equal(p.Allow, []string{"only.example:443"}) || p.DailyBytes != 5 || p.Labels["src"] != "authz" {
		t.Fatalf("exit policy must come from the authorizer: %+v", p)
	}
	if c := fa.calls(access.ActionConnect); len(c) < 2 || !slices.ContainsFunc(c, func(r access.Request) bool { return r.Peer == exitC.PeerID }) {
		t.Fatalf("connect requests: %+v", c)
	}

	// connect_peer denials are enforced and audited.
	hub.offer(exitC.PeerID)
	if e := hub.error(proto.ErrCodeForbidden); !strings.Contains(e.Message, "no direct links today") {
		t.Fatalf("connect_peer denial: %+v", e)
	}
	if cp := fa.calls(access.ActionConnectPeer); len(cp) != 1 || cp[0].Target != exitC.PeerID {
		t.Fatalf("connect_peer requests: %+v", cp)
	}

	// A policy change re-asks the authorizer, uncached; a denial disconnects.
	fa.set(func(fa *fakeAuthorizer) {
		prev := fa.decide
		fa.decide = func(r access.Request) access.Decision {
			if r.Action == access.ActionConnect && r.Peer == exitC.PeerID {
				return access.Decision{Allow: false, Reason: "suspended"}
			}
			return prev(r)
		}
	})
	h.ctl("PUT", "/control/networks/testnet/tags/tag:other", map[string]any{}, nil, http.StatusOK)
	if e := exit.error(proto.ErrCodeForbidden); !e.Fatal || !strings.Contains(e.Message, "suspended") {
		t.Fatalf("policy-change denial: %+v", e)
	}
	if len(h.audit("testnet", "peer.connect_denied")) == 0 {
		t.Fatal("connect denial not audited")
	}
}

// TestAuthorizerFailClosed: an authorizer that errors, is unreachable, or
// rejects the signature (wrong secret) denies everything.
func TestAuthorizerFailClosed(t *testing.T) {
	fa := newFakeAuthorizer(t, func(access.Request) access.Decision { return access.Decision{Allow: true} })
	h := newHarness(t, apiMode(fa.srv.URL, authzSecret))
	key := h.authKey("testnet", map[string]any{"reusable": true})
	creds, status := h.enroll(key.Key, nil)
	if status != http.StatusCreated {
		t.Fatalf("enroll: status %d", status)
	}

	fa.set(func(fa *fakeAuthorizer) { fa.status = http.StatusInternalServerError })
	// Distinct labels, so the cached allow for the first request does not apply.
	if _, status := h.enroll(key.Key, map[string]string{"attempt": "2"}); status != http.StatusServiceUnavailable {
		t.Fatalf("authorizer 500: status %d", status)
	}
	s := h.dial(creds)
	s.welcome()
	if err := s.c.Join(t.Context(), "testnet"); err != nil {
		t.Fatal(err)
	}
	if e := s.error(proto.ErrCodeForbidden); !strings.Contains(e.Message, "authorizer unavailable") {
		t.Fatalf("join with the authorizer down: %+v", e)
	}

	// Wrong shared secret: the authorizer rejects every signature.
	bad := newHarness(t, apiMode(fa.srv.URL, "a-different-secret-0123456789"))
	fa.set(func(fa *fakeAuthorizer) { fa.status = 0 })
	badKey := bad.authKey("testnet", nil)
	if _, status := bad.enroll(badKey.Key, nil); status != http.StatusServiceUnavailable {
		t.Fatalf("bad signature: status %d", status)
	}
	fa.mu.Lock()
	rejected := len(fa.rejected)
	fa.mu.Unlock()
	if rejected == 0 {
		t.Fatal("the authorizer should have rejected the signature")
	}

	// Unreachable authorizer.
	fa.srv.Close()
	if _, status := h.enroll(key.Key, map[string]string{"attempt": "3"}); status != http.StatusServiceUnavailable {
		t.Fatalf("unreachable authorizer: status %d", status)
	}
}
