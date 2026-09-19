package exit

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingAccountant captures connection records.
type recordingAccountant struct {
	mu      sync.Mutex
	records []ConnInfo
}

func (r *recordingAccountant) Record(ci ConnInfo) {
	r.mu.Lock()
	r.records = append(r.records, ci)
	r.mu.Unlock()
}

func (r *recordingAccountant) last() (ConnInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) == 0 {
		return ConnInfo{}, false
	}
	return r.records[len(r.records)-1], true
}

// startExit starts an exit server on a loopback listener and returns its addr.
func startExit(t *testing.T, cfg Config) (*Server, string) {
	t.Helper()
	s := New(cfg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Close() })
	return s, ln.Addr().String()
}

// socks5Connect performs a SOCKS5 handshake+CONNECT and returns the ready conn.
func socks5Connect(t *testing.T, proxyAddr, host string, port int, user string) (net.Conn, error) {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	// Method negotiation: offer user/pass + none.
	if _, err := c.Write([]byte{0x05, 0x02, socks5AuthUserPass, socks5AuthNone}); err != nil {
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil {
		return nil, err
	}
	if reply[1] == socks5AuthUserPass {
		buf := []byte{0x01, byte(len(user))}
		buf = append(buf, user...)
		buf = append(buf, 0x00) // empty password
		if _, err := c.Write(buf); err != nil {
			return nil, err
		}
		ar := make([]byte, 2)
		if _, err := io.ReadFull(c, ar); err != nil {
			return nil, err
		}
	}
	// CONNECT request with domain name.
	req := []byte{0x05, socks5CmdConnect, 0x00, socks5AtypDomain, byte(len(host))}
	req = append(req, host...)
	p := make([]byte, 2)
	binary.BigEndian.PutUint16(p, uint16(port))
	req = append(req, p...)
	if _, err := c.Write(req); err != nil {
		return nil, err
	}
	resp := make([]byte, 10)
	if _, err := io.ReadFull(c, resp); err != nil {
		return nil, err
	}
	if resp[1] != socks5RepSuccess {
		c.Close()
		return nil, fmt.Errorf("socks5 reply code %d", resp[1])
	}
	return c, nil
}

func TestExitSOCKS5Allow(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from target")
	}))
	defer target.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	acct := &recordingAccountant{}
	allow := NewAllowlist()
	allow.Set([]string{host}) // allow the loopback literal explicitly
	_, proxy := startExit(t, Config{Policy: allow, Accountant: acct})

	conn, err := socks5Connect(t, proxy, host, port, "source=tesla-ca")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "hello from target") {
		t.Fatalf("unexpected response: %q", string(body))
	}
	_ = conn.Close() // let the exit tear down and emit its record

	waitFor(t, func() bool {
		ci, ok := acct.last()
		return ok && ci.Result == ResultAllowed
	})
	ci, _ := acct.last()
	if ci.SourceTag != "source=tesla-ca" {
		t.Errorf("source tag=%q", ci.SourceTag)
	}
	if ci.Protocol != ProtoSOCKS5 {
		t.Errorf("protocol=%q", ci.Protocol)
	}
	if ci.BytesOut == 0 || ci.BytesIn == 0 {
		t.Errorf("byte counts not recorded: in=%d out=%d", ci.BytesIn, ci.BytesOut)
	}
}

func TestExitSOCKS5DenyLoopback(t *testing.T) {
	acct := &recordingAccountant{}
	allow := NewAllowlist()
	allow.Set([]string{"example.com:443"}) // does NOT include loopback
	_, proxy := startExit(t, Config{Policy: allow, Accountant: acct})

	_, err := socks5Connect(t, proxy, "127.0.0.1", 9, "")
	if err == nil {
		t.Fatal("expected loopback destination to be denied")
	}
	waitFor(t, func() bool {
		ci, ok := acct.last()
		return ok && ci.Result == ResultDenied
	})
}

func TestExitHTTPConnectAllow(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	acct := &recordingAccountant{}
	allow := NewAllowlist()
	allow.Set([]string{host})
	_, proxy := startExit(t, Config{Policy: allow, Accountant: acct, HTTPSourceHeader: "X-Ghost-Source"})

	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s:%d HTTP/1.1\r\nX-Ghost-Source: job=42\r\n\r\n", host, port)
	br := bufio.NewReader(c)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("expected 200, got %q", status)
	}
	// consume rest of headers
	for {
		line, _ := br.ReadString('\n')
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	body, _ := io.ReadAll(br)
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("unexpected body: %q", string(body))
	}
	_ = c.Close()
	waitFor(t, func() bool {
		ci, ok := acct.last()
		return ok && ci.Result == ResultAllowed && ci.SourceTag == "job=42" && ci.Protocol == ProtoHTTPConnect
	})
}

func TestExitPauseRefuses(t *testing.T) {
	acct := &recordingAccountant{}
	allow := NewAllowlist()
	allow.Set([]string{"127.0.0.1"})
	s, proxy := startExit(t, Config{Policy: allow, Accountant: acct})
	s.SetPaused(true)

	_, err := socks5Connect(t, proxy, "127.0.0.1", 80, "")
	if err == nil {
		t.Fatal("expected paused exit to refuse")
	}
	waitFor(t, func() bool {
		ci, ok := acct.last()
		return ok && ci.Result == ResultCapped
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
