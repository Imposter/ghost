package access

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

var secret = []byte("unit-test-secret-0123456789")

func signed(t *testing.T, req Request) ([]byte, string) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return body, Sign(secret, body)
}

func TestVerifier(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	v := NewVerifier(secret, 5*time.Minute, func() time.Time { return now })
	req := Request{Action: ActionConnect, Network: "n", Peer: "p", TS: now.Unix(), Nonce: NewNonce()}
	body, sig := signed(t, req)

	got, err := v.Verify(body, sig)
	if err != nil || got.Peer != "p" {
		t.Fatalf("valid request: %v %+v", err, got)
	}
	if _, err := v.Verify(body, sig); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: %v", err)
	}

	tampered := append([]byte{}, body...)
	tampered[len(tampered)-2] ^= 1
	if _, err := v.Verify(tampered, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered body: %v", err)
	}
	if _, err := v.Verify(body, "sha256=00"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("bad signature: %v", err)
	}
	if _, err := NewVerifier([]byte("other-secret-0123456789"), 0, nil).Verify(body, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong secret: %v", err)
	}

	stale := Request{Action: ActionEnroll, TS: now.Add(-10 * time.Minute).Unix(), Nonce: NewNonce()}
	b, s := signed(t, stale)
	if _, err := v.Verify(b, s); !errors.Is(err, ErrStale) {
		t.Fatalf("stale: %v", err)
	}
	b, s = signed(t, Request{Action: ActionEnroll, TS: now.Unix()})
	if _, err := v.Verify(b, s); !errors.Is(err, ErrMalformed) {
		t.Fatalf("missing nonce: %v", err)
	}
}

// TestWebhookSignsVerifiably checks the client and the verifier agree, and
// that a captured request replayed verbatim is rejected.
func TestWebhookSignsVerifiably(t *testing.T) {
	v := NewVerifier(secret, time.Minute, nil)
	var capturedBody []byte
	var capturedSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		capturedSig = r.Header.Get(SignatureHeader)
		if _, err := v.Verify(capturedBody, capturedSig); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Decision{Allow: true, Policy: &Policy{ExitAllowlist: []string{}}})
	}))
	defer srv.Close()

	d, err := NewWebhook(srv.URL, secret, time.Second, nil, nil).Authorize(context.Background(), Request{Action: ActionEnroll, Network: "n"})
	if err != nil || !d.Allow {
		t.Fatalf("authorize: %v %+v", err, d)
	}
	if p := d.Policy.ExitPolicy("n"); p == nil || p.Allow == nil || len(p.Allow) != 0 {
		t.Fatalf("an empty allowlist must override (deny all): %+v", p)
	}
	if _, err := v.Verify(capturedBody, capturedSig); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed capture: %v", err)
	}
}

type countingAuth struct {
	n   atomic.Int32
	err error
}

func (c *countingAuth) Authorize(context.Context, Request) (Decision, error) {
	c.n.Add(1)
	return Decision{Allow: c.err == nil}, c.err
}

func TestControllerCacheAndFailClosed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	auth := &countingAuth{}
	c := NewController(auth, ControllerOptions{CacheTTL: 30 * time.Second, Now: clock})
	req := Request{Action: ActionConnectPeer, Network: "n", Peer: "a", Target: "b"}

	for range 3 {
		if !c.Check(context.Background(), req).Allow {
			t.Fatal("expected allow")
		}
	}
	if auth.n.Load() != 1 {
		t.Fatalf("authorizer calls = %d, want 1 (cached)", auth.n.Load())
	}
	c.Forget("b") // the target was revoked
	c.Check(context.Background(), req)
	if auth.n.Load() != 2 {
		t.Fatalf("Forget(peer) should drop the entry; calls = %d", auth.n.Load())
	}
	now = now.Add(time.Minute)
	c.Check(context.Background(), req)
	if auth.n.Load() != 3 {
		t.Fatalf("an expired entry should be refreshed; calls = %d", auth.n.Load())
	}

	failing := &countingAuth{err: errors.New("down")}
	fc := NewController(failing, ControllerOptions{CacheTTL: time.Minute, Now: clock})
	for range 2 {
		if d := fc.Check(context.Background(), req); d.Allow {
			t.Fatal("an authorizer error must deny")
		}
	}
	if failing.n.Load() != 2 {
		t.Fatalf("errors must not be cached; calls = %d", failing.n.Load())
	}
}

// TestControllerKeysOnPeerContext: a new WireGuard key or enrolment method is
// a different request, so it never reuses a decision made for the old one.
func TestControllerKeysOnPeerContext(t *testing.T) {
	auth := &countingAuth{}
	c := NewController(auth, ControllerOptions{CacheTTL: time.Minute})
	base := Request{Action: ActionConnect, Network: "n", Peer: "a", PublicKey: "k1", EnrollmentMethod: EnrollAuthKey, AuthKeyID: "key_1"}
	variants := []func(*Request){
		func(*Request) {},
		func(r *Request) { r.PublicKey = "k2" },
		func(r *Request) { r.EnrollmentMethod = EnrollDirect },
		func(r *Request) { r.AuthKeyID = "key_2" },
	}
	for i, mutate := range variants {
		req := base
		mutate(&req)
		c.Check(context.Background(), req)
		c.Check(context.Background(), req)
		if got := auth.n.Load(); got != int32(i+1) {
			t.Fatalf("variant %d: authorizer calls = %d, want %d", i, got, i+1)
		}
	}
}

func TestControllerConsulted(t *testing.T) {
	tests := []struct {
		name string
		auth Authorizer
		want bool
	}{
		{"open", Open{}, false},
		{"webhook", NewWebhook("http://127.0.0.1:1", secret, time.Second, nil, nil), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewController(tt.auth, ControllerOptions{}).Consulted(); got != tt.want {
				t.Fatalf("Consulted() = %v, want %v", got, tt.want)
			}
		})
	}
}
