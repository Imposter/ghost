package server_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
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

const authzSecret = "authorizer-shared-secret-0123"

// fakeAuthorizer is an authorizer webhook on 127.0.0.1 that verifies every
// request with access.Verifier and answers with decide.
type fakeAuthorizer struct {
	t        *testing.T
	srv      *httptest.Server
	verifier *access.Verifier

	mu       sync.Mutex
	decide   func(access.Request) access.Decision
	accepted []access.Request
	rejected []error
	delay    time.Duration
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
	delay, status, decide := fa.delay, fa.status, fa.decide
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
	if delay > 0 {
		time.Sleep(delay)
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

func apiMode(url, secret string, mutate func(*config.Config)) func(*config.Config) {
	return func(c *config.Config) {
		c.Access.Mode = config.AccessAPI
		c.Access.AuthorizerURL = url
		c.Access.AuthorizerSecret = secret
		c.Access.Timeout = config.Duration(500 * time.Millisecond)
		if mutate != nil {
			mutate(c)
		}
	}
}

func allowAll(access.Request) access.Decision { return access.Decision{Allow: true} }

func TestAuthorizerAllow(t *testing.T) {
	fa := newFakeAuthorizer(t, func(r access.Request) access.Decision {
		switch r.Action {
		case access.ActionRegister:
			return access.Decision{Allow: true, Policy: &access.Policy{Labels: map[string]string{"tier": "gold"}}}
		case access.ActionJoinNetwork:
			if r.Role == proto.RoleNode {
				return access.Decision{Allow: true, Policy: &access.Policy{
					ExitAllowlist: []string{"only.example.com:443"}, Caps: access.Caps{DailyBytes: 1000},
				}}
			}
		}
		return access.Decision{Allow: true}
	})
	h := newHarness(t, apiMode(fa.srv.URL, authzSecret, nil))

	nodeCreds := h.register("testnet", proto.RoleNode, map[string]string{"geo": "ca"})
	reg := fa.calls(access.ActionRegister)
	if len(reg) != 1 || reg[0].Network != "testnet" || reg[0].Role != proto.RoleNode || reg[0].Labels["geo"] != "ca" ||
		reg[0].Device != "" || reg[0].Nonce == "" || reg[0].TS == 0 {
		t.Fatalf("register request: %+v", reg)
	}
	var dev httpapi.DeviceView
	h.do("GET", "/admin/devices/"+nodeCreds.DeviceID, adminToken, nil, &dev)
	if dev.Labels["tier"] != "gold" || dev.Labels["geo"] != "ca" {
		t.Fatalf("authorizer labels not applied: %+v", dev.Labels)
	}

	hubCreds := h.register("testnet", proto.RoleHub, nil)
	hub := h.dial(hubCreds)
	hub.join("testnet")
	node := h.dial(nodeCreds)
	nj := node.join("testnet")
	if nj.Policy == nil || len(nj.Policy.Allow) != 1 || nj.Policy.Allow[0] != "only.example.com:443" || nj.Policy.DailyBytes != 1000 {
		t.Fatalf("authorizer policy override at join: %+v", nj.Policy)
	}
	joins := fa.calls(access.ActionJoinNetwork)
	if len(joins) != 2 || joins[1].Device != nodeCreds.DeviceID {
		t.Fatalf("join_network requests: %+v", joins)
	}

	// connect_peer is asked for relayed signals, then served from the cache.
	for range 3 {
		if err := hub.c.SendCandidate(context.Background(), proto.Signal{To: nodeCreds.DeviceID, Candidate: &proto.Candidate{Type: "host"}}); err != nil {
			t.Fatal(err)
		}
		node.next("candidate", 5*time.Second, func(e signal.Event) bool { return e.Type == proto.TypeCandidate })
	}
	conn := fa.calls(access.ActionConnectPeer)
	if len(conn) != 1 || conn[0].Device != hubCreds.DeviceID || conn[0].Peer != nodeCreds.DeviceID {
		t.Fatalf("connect_peer requests (want 1, cached after): %+v", conn)
	}

	// Pausing the network overrides the device policy's pause flag but keeps
	// its allowlist.
	h.do("PUT", "/admin/networks/testnet/policy", adminToken, control.PolicyInput{Allow: []string{"net.example.com:443"}, Paused: true}, nil)
	p := node.next("policy", 5*time.Second, func(e signal.Event) bool { return e.Policy != nil }).Policy
	if !p.Paused || p.Allow[0] != "only.example.com:443" || p.Revision != 1 {
		t.Fatalf("policy with override: %+v", p)
	}
	hp := hub.next("hub policy", 5*time.Second, func(e signal.Event) bool { return e.Policy != nil }).Policy
	if hp.Allow[0] != "net.example.com:443" {
		t.Fatalf("hub (no override) should get the network policy: %+v", hp)
	}
}

func TestAuthorizerDeny(t *testing.T) {
	fa := newFakeAuthorizer(t, func(r access.Request) access.Decision {
		return access.Decision{Allow: false, Reason: "not invited"}
	})
	// No decision cache, so the authorizer's change of mind applies at once.
	h := newHarness(t, apiMode(fa.srv.URL, authzSecret, func(c *config.Config) { c.Access.CacheTTL = 0 }))

	var body struct {
		Error string `json:"error"`
	}
	if status := h.do("POST", "/v1/register", "", control.RegisterInput{Network: "testnet"}, &body); status != http.StatusForbidden || !strings.Contains(body.Error, "not invited") {
		t.Fatalf("denied register: status %d, %q", status, body.Error)
	}

	// A denied pair leaves the code usable.
	var code control.IssuedCode
	h.do("POST", "/admin/pairing-codes", adminToken, map[string]any{"network": "testnet"}, &code)
	if status := h.do("POST", "/v1/pair", "", control.PairInput{Code: code.Code}, nil); status != http.StatusForbidden {
		t.Fatalf("denied pair: status %d", status)
	}
	fa.set(func(fa *fakeAuthorizer) {
		fa.decide = func(r access.Request) access.Decision {
			return access.Decision{Allow: r.Action != access.ActionJoinNetwork, Reason: "paused"}
		}
	})
	var creds control.Credentials
	if status := h.do("POST", "/v1/pair", "", control.PairInput{Code: code.Code}, &creds); status != http.StatusCreated {
		t.Fatalf("pair after allow: status %d", status)
	}

	// A denied join is a non-fatal forbidden error.
	c := h.dial(creds)
	c.welcome()
	_ = c.c.Join(context.Background(), "testnet")
	if e := c.error(proto.ErrCodeForbidden); e.Fatal || !strings.Contains(e.Message, "paused") {
		t.Fatalf("join denial: %+v", e)
	}
}

func TestAuthorizerBadSignature(t *testing.T) {
	fa := newFakeAuthorizer(t, allowAll)
	h := newHarness(t, apiMode(fa.srv.URL, "a-different-secret-0123456", nil))
	if status := h.do("POST", "/v1/register", "", control.RegisterInput{Network: "testnet"}, nil); status != http.StatusForbidden {
		t.Fatalf("register with mis-signed authorizer call: status %d, want 403", status)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if len(fa.accepted) != 0 || len(fa.rejected) != 1 || fa.rejected[0] != access.ErrBadSignature {
		t.Fatalf("authorizer accepted=%d rejected=%v", len(fa.accepted), fa.rejected)
	}
}

func TestAuthorizerFailClosed(t *testing.T) {
	// Unreachable: a port nothing listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	_ = ln.Close()
	h := newHarness(t, apiMode(dead, authzSecret, nil))
	if status := h.do("POST", "/v1/register", "", control.RegisterInput{Network: "testnet"}, nil); status != http.StatusForbidden {
		t.Fatalf("unreachable authorizer: status %d, want 403", status)
	}

	// Server errors and timeouts deny, and are not cached.
	fa := newFakeAuthorizer(t, allowAll)
	h2 := newHarness(t, apiMode(fa.srv.URL, authzSecret, nil))
	fa.set(func(fa *fakeAuthorizer) { fa.status = http.StatusInternalServerError })
	if status := h2.do("POST", "/v1/register", "", control.RegisterInput{Network: "testnet"}, nil); status != http.StatusForbidden {
		t.Fatalf("authorizer 500: status %d, want 403", status)
	}
	fa.set(func(fa *fakeAuthorizer) { fa.status, fa.delay = 0, 2*time.Second })
	if status := h2.do("POST", "/v1/register", "", control.RegisterInput{Network: "testnet"}, nil); status != http.StatusForbidden {
		t.Fatalf("authorizer timeout: status %d, want 403", status)
	}
	fa.set(func(fa *fakeAuthorizer) { fa.delay = 0 })
	if status := h2.do("POST", "/v1/register", "", control.RegisterInput{Network: "testnet"}, nil); status != http.StatusCreated {
		t.Fatalf("recovered authorizer: status %d, want 201 (errors must not be cached)", status)
	}
}
