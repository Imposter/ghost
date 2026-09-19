package server_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal"
	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
)

func TestPairingFlow(t *testing.T) {
	h := newHarness(t, nil)

	var issued control.IssuedCode
	status := h.do("POST", "/admin/pairing-codes", adminToken,
		map[string]any{"network": "testnet", "name": "laptop", "labels": map[string]string{"owner": "u1"}}, &issued)
	if status != http.StatusCreated || len(issued.Code) != 11 || issued.Role != proto.RoleNode {
		t.Fatalf("create code: status %d, %+v", status, issued)
	}

	// Codes are case-insensitive and ignore separators.
	typed := strings.ToLower(strings.ReplaceAll(issued.Code, "-", " "))
	var creds control.Credentials
	if status := h.do("POST", "/v1/pair", "", control.PairInput{Code: typed, Labels: map[string]string{"os": "linux"}}, &creds); status != http.StatusCreated {
		t.Fatalf("pair: status %d", status)
	}
	if creds.DeviceID == "" || !strings.HasPrefix(creds.DeviceToken, "gdt_") || creds.Network != "testnet" {
		t.Fatalf("pair credentials: %+v", creds)
	}

	// One use only.
	if status := h.do("POST", "/v1/pair", "", control.PairInput{Code: issued.Code}, nil); status != http.StatusNotFound {
		t.Fatalf("second redemption: status %d, want 404", status)
	}

	// The device carries the code's name and merged labels; the token hash is
	// not exposed.
	var dev httpapi.DeviceView
	h.do("GET", "/admin/devices/"+creds.DeviceID, adminToken, nil, &dev)
	if dev.Name != "laptop" || dev.Labels["owner"] != "u1" || dev.Labels["os"] != "linux" || dev.Role != proto.RoleNode {
		t.Fatalf("paired device: %+v", dev)
	}

	// The minted token authenticates on the signalling socket.
	w := h.dial(creds).welcome()
	if w.DeviceID != creds.DeviceID || w.HeartbeatInterval != 1 {
		t.Fatalf("welcome: %+v", w)
	}

	// Expired codes are refused.
	var short control.IssuedCode
	h.do("POST", "/admin/pairing-codes", adminToken, map[string]any{"network": "testnet", "ttl_seconds": 60}, &short)
	h.clock.Advance(2 * time.Minute)
	if status := h.do("POST", "/v1/pair", "", control.PairInput{Code: short.Code}, nil); status != http.StatusNotFound {
		t.Fatalf("expired code: status %d, want 404", status)
	}
	// Unknown networks and unauthenticated callers are rejected.
	if status := h.do("POST", "/admin/pairing-codes", adminToken, map[string]any{"network": "nope"}, nil); status != http.StatusNotFound {
		t.Fatalf("code for unknown network: status %d", status)
	}
	if status := h.do("POST", "/admin/pairing-codes", "wrong-token", map[string]any{"network": "testnet"}, nil); status != http.StatusUnauthorized {
		t.Fatalf("admin without token: status %d", status)
	}
}

func TestRegisterAndJoin(t *testing.T) {
	h := newHarness(t, nil)

	hubCreds := h.register("testnet", proto.RoleHub, nil)
	nodeCreds := h.register("testnet", proto.RoleNode, map[string]string{"geo": "ca"})

	hub := h.dial(hubCreds)
	hj := hub.join("testnet")
	if hj.Address != "100.64.0.1/32" || hj.Pool != "100.64.0.0/10" || hj.Hub != nil || hj.Policy == nil {
		t.Fatalf("hub joined: %+v", hj)
	}

	node := h.dial(nodeCreds)
	nj := node.join("testnet")
	if nj.Address != "100.64.0.2/32" {
		t.Fatalf("node address %q", nj.Address)
	}
	if nj.Hub == nil || nj.Hub.DeviceID != hubCreds.DeviceID || nj.Hub.Role != proto.RoleHub {
		t.Fatalf("node should be pointed at the hub: %+v", nj.Hub)
	}

	// The hub learns about the node.
	on := hub.next("peer_online", 5*time.Second, func(e signal.Event) bool { return e.Peer != nil && e.Type == proto.TypePeerOnline })
	if on.Peer.Peer.DeviceID != nodeCreds.DeviceID || on.Peer.Peer.Address != nj.Address {
		t.Fatalf("peer_online: %+v", on.Peer)
	}

	// Offers are relayed with From rewritten.
	if err := hub.c.SendOffer(context.Background(), proto.Signal{To: nodeCreds.DeviceID, From: "spoofed", Ufrag: "u", Pwd: "p"}); err != nil {
		t.Fatal(err)
	}
	offer := node.next("offer", 5*time.Second, func(e signal.Event) bool { return e.Signal != nil && e.Type == proto.TypeOffer })
	if offer.Signal.From != hubCreds.DeviceID || offer.Signal.Network != "testnet" || offer.Signal.Ufrag != "u" {
		t.Fatalf("relayed offer: %+v", offer.Signal)
	}

	// Node-to-node signalling is refused.
	node2 := h.dial(h.register("testnet", proto.RoleNode, nil))
	node2.join("testnet")
	if err := node2.c.SendOffer(context.Background(), proto.Signal{To: nodeCreds.DeviceID}); err != nil {
		t.Fatal(err)
	}
	node2.error(proto.ErrCodeForbidden)

	// A device cannot join someone else's network.
	other := h.dial(h.register("othernet", proto.RoleNode, nil))
	other.welcome()
	_ = other.c.Join(context.Background(), "testnet")
	other.error(proto.ErrCodeForbidden)

	// Addresses are sticky across sessions.
	_ = node.c.Close()
	again := h.dial(nodeCreds).join("testnet")
	if again.Address != nj.Address {
		t.Fatalf("address changed on rejoin: %s -> %s", nj.Address, again.Address)
	}

	// Presence and stats see the sessions.
	var st httpapi.Stats
	h.do("GET", "/admin/stats", adminToken, nil, &st)
	if st.Devices != 4 || st.OnlineByNet["testnet"]["hub"] != 1 || st.OnlineByNet["testnet"]["node"] < 2 {
		t.Fatalf("stats: %+v", st)
	}
}

func TestHelloRejections(t *testing.T) {
	h := newHarness(t, nil)
	bad := h.dial(control.Credentials{DeviceToken: "gdt_bogus", Role: proto.RoleNode})
	if e := bad.error(proto.ErrCodeUnauthorized); !e.Fatal {
		t.Fatalf("bad token error should be fatal: %+v", e)
	}

	// A node cannot claim the hub role.
	node := h.register("testnet", proto.RoleNode, nil)
	node.Role = proto.RoleHub
	h.dial(node).error(proto.ErrCodeUnauthorized)
}

func TestTURNCredentialsInWelcome(t *testing.T) {
	const secret = "coturn-static-auth-secret"
	h := newHarness(t, func(c *config.Config) {
		c.ICE.STUNURLs = []string{"stun:stun.test:3478"}
		c.ICE.TURNURLs = []string{"turn:turn.test:3478?transport=udp"}
		c.ICE.TURNSecret = secret
		c.ICE.TURNTTL = config.Duration(10 * time.Minute)
	})
	creds := h.register("testnet", proto.RoleNode, nil)
	w := h.dial(creds).welcome()
	if len(w.ICEServers) != 2 || w.ICEServers[0].URLs[0] != "stun:stun.test:3478" || w.ICEServers[0].Username != "" {
		t.Fatalf("ice servers: %+v", w.ICEServers)
	}
	turnSrv := w.ICEServers[1]
	expStr, user, ok := strings.Cut(turnSrv.Username, ":")
	if !ok || user != creds.DeviceID {
		t.Fatalf("turn username %q", turnSrv.Username)
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(time.Unix(exp, 0)); d < 9*time.Minute || d > 11*time.Minute {
		t.Fatalf("turn expiry %v from now, want ~10m", d)
	}
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(turnSrv.Username))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); turnSrv.Credential != want {
		t.Fatalf("turn credential %q, want %q", turnSrv.Credential, want)
	}
}

func TestAdminRevokeKillsSession(t *testing.T) {
	h := newHarness(t, nil)
	hubCreds := h.register("testnet", proto.RoleHub, nil)
	nodeCreds := h.register("testnet", proto.RoleNode, nil)
	hub := h.dial(hubCreds)
	hub.join("testnet")
	node := h.dial(nodeCreds)
	node.join("testnet")

	var dev httpapi.DeviceView
	h.do("GET", "/admin/devices/"+nodeCreds.DeviceID, adminToken, nil, &dev)
	if !dev.Online {
		t.Fatal("node should be online before revoke")
	}

	start := time.Now()
	if status := h.do("POST", "/admin/devices/"+nodeCreds.DeviceID+"/revoke", adminToken, nil, &dev); status != http.StatusOK || dev.RevokedAt == nil {
		t.Fatalf("revoke: status %d, %+v", status, dev)
	}
	if e := node.error(proto.ErrCodeRevoked); !e.Fatal {
		t.Fatalf("revoke error should be fatal: %+v", e)
	}
	node.next("disconnect", 5*time.Second, func(e signal.Event) bool { return e.State == signal.StateDisconnected })
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("session took %v to close", el)
	}
	off := hub.next("peer_offline", 5*time.Second, func(e signal.Event) bool { return e.Peer != nil && e.Type == proto.TypePeerOffline })
	if off.Peer.Peer.DeviceID != nodeCreds.DeviceID {
		t.Fatalf("peer_offline for %s", off.Peer.Peer.DeviceID)
	}
	// The client's reconnect attempt is refused.
	node.error(proto.ErrCodeUnauthorized)

	var list struct {
		Devices []httpapi.DeviceView `json:"devices"`
	}
	h.do("GET", "/admin/devices?network=testnet", adminToken, nil, &list)
	for _, d := range list.Devices {
		if d.ID == nodeCreds.DeviceID {
			t.Fatal("revoked device listed without include_revoked")
		}
	}
}

func TestAdminMoveDevice(t *testing.T) {
	h := newHarness(t, nil)
	creds := h.register("testnet", proto.RoleNode, nil)
	c := h.dial(creds)
	c.join("testnet")

	var dev httpapi.DeviceView
	if status := h.do("POST", "/admin/devices/"+creds.DeviceID+"/move", adminToken, map[string]string{"network": "othernet"}, &dev); status != http.StatusOK {
		t.Fatalf("move: status %d", status)
	}
	if dev.Network != "othernet" || dev.Address != "" {
		t.Fatalf("moved device: %+v", dev)
	}
	c.error(proto.ErrCodeForbidden)

	moved := h.dial(creds).join("othernet")
	if moved.Address != "100.96.0.1/32" {
		t.Fatalf("address in new pool: %s", moved.Address)
	}
}

func TestPolicyPushReachesNode(t *testing.T) {
	h := newHarness(t, nil)
	node := h.dial(h.register("testnet", proto.RoleNode, nil))
	j := node.join("testnet")
	if j.Policy == nil || len(j.Policy.Allow) != 0 || j.Policy.Revision != 0 {
		t.Fatalf("initial policy should deny all: %+v", j.Policy)
	}
	// A node in another network must not receive it.
	other := h.dial(h.register("othernet", proto.RoleNode, nil))
	other.join("othernet")

	var res struct {
		Policy proto.ExitPolicy `json:"policy"`
		Pushed int              `json:"pushed"`
	}
	in := control.PolicyInput{Allow: []string{"example.com:443", "*.example.org:443"}, DailyBytes: 1 << 30}
	if status := h.do("PUT", "/admin/networks/testnet/policy", adminToken, in, &res); status != http.StatusOK {
		t.Fatalf("set policy: status %d", status)
	}
	if res.Pushed != 1 || res.Policy.Revision != 1 {
		t.Fatalf("set policy result: %+v", res)
	}
	got := node.next("policy", 5*time.Second, func(e signal.Event) bool { return e.Policy != nil }).Policy
	if got.Network != "testnet" || got.Revision != 1 || got.DailyBytes != 1<<30 || len(got.Allow) != 2 || got.Allow[0] != "example.com:443" {
		t.Fatalf("pushed policy: %+v", got)
	}

	// Open-proxy entries are rejected.
	if status := h.do("PUT", "/admin/networks/testnet/policy", adminToken, control.PolicyInput{Allow: []string{"*:443"}}, nil); status != http.StatusBadRequest {
		t.Fatalf("wildcard policy: status %d, want 400", status)
	}

	// A later join carries the stored policy.
	rejoin := h.dial(h.register("testnet", proto.RoleNode, nil)).join("testnet")
	if rejoin.Policy.Revision != 1 || len(rejoin.Policy.Allow) != 2 {
		t.Fatalf("policy at join: %+v", rejoin.Policy)
	}

	for drain := time.After(300 * time.Millisecond); ; {
		select {
		case ev := <-other.c.Events():
			if ev.Policy != nil {
				t.Fatal("policy leaked to another network")
			}
			continue
		case <-drain:
		}
		break
	}
}

func TestDeviceMetricsStub(t *testing.T) {
	h := newHarness(t, nil)
	creds := h.register("testnet", proto.RoleNode, nil)
	if status := h.do("GET", "/admin/devices/"+creds.DeviceID+"/metrics", adminToken, nil, nil); status != http.StatusNotImplemented {
		t.Fatalf("metrics stub: status %d, want 501", status)
	}
	if status := h.do("GET", "/admin/devices/dev_missing/metrics", adminToken, nil, nil); status != http.StatusNotFound {
		t.Fatalf("metrics for unknown device: status %d, want 404", status)
	}
}

// TestNodeHubThroughServer runs a real ghost.Hub and ghost.Node through the
// real server and moves HTTP over the WireGuard tunnel. ICE is restricted to
// loopback host candidates, so nothing binds a routable address.
func TestNodeHubThroughServer(t *testing.T) {
	if testing.Short() {
		t.Skip("tunnel integration test")
	}
	h := newHarness(t, nil)
	hubCreds := h.register("testnet", proto.RoleHub, nil)
	nodeCreds := h.register("testnet", proto.RoleNode, nil)
	ctx := context.Background()

	cfg := func(c control.Credentials) ghost.Config {
		gc := ghost.Config{SignalURL: h.ws, DeviceToken: c.DeviceToken, Network: "testnet", ConnectTimeout: 15 * time.Second,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		gc.UseLoopbackICE()
		return gc
	}

	hub, err := ghost.NewHub(cfg(hubCreds))
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	hubIP := strings.TrimSuffix(waitMesh(t, hub.Events(), ghost.EventJoined, 10*time.Second).Address, "/32")

	ln, err := hub.Listen("tcp", net.JoinHostPort(hubIP, "8080"))
	if err != nil {
		t.Fatal(err)
	}
	hsrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello over ghost")
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hsrv.Serve(ln) }()
	defer hsrv.Close()

	node, err := ghost.NewNode(cfg(nodeCreds))
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	waitMesh(t, node.Events(), ghost.EventPeerConnected, 30*time.Second)

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: node.DialContext}}
	var lastErr error
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		resp, err := client.Get("http://" + net.JoinHostPort(hubIP, "8080") + "/")
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "hello over ghost" {
			t.Fatalf("body %q", body)
		}
		// The server sees both members online in the network.
		if n := len(h.srv.Relay.Presence("testnet")); n != 2 {
			t.Fatalf("presence: %d sessions", n)
		}
		return
	}
	t.Fatalf("GET over tunnel: %v", lastErr)
}

func waitMesh(t *testing.T, ev <-chan ghost.Event, kind ghost.EventKind, timeout time.Duration) ghost.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ev:
			if e.Kind == kind {
				return e
			}
			if e.Kind == ghost.EventError {
				t.Logf("mesh error: %v", e.Err)
			}
		case <-deadline:
			t.Fatalf("mesh event %q not received within %s", kind, timeout)
			return ghost.Event{}
		}
	}
}
