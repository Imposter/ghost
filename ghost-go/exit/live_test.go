package exit

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// liveAccountant records Open, AddBytes and Close calls.
type liveAccountant struct {
	recordingAccountant
	opened  atomic.Int32
	in, out atomic.Int64

	mu     sync.Mutex
	open   []ConnInfo
	closed []ConnInfo
}

func (a *liveAccountant) Open(ci ConnInfo) LiveConn {
	a.mu.Lock()
	a.open = append(a.open, ci)
	a.mu.Unlock()
	a.opened.Add(1)
	return a
}

func (a *liveAccountant) AddBytes(in, out int64) {
	a.in.Add(in)
	a.out.Add(out)
}

func (a *liveAccountant) Close(ci ConnInfo) {
	a.mu.Lock()
	a.closed = append(a.closed, ci)
	a.mu.Unlock()
}

func (a *liveAccountant) closedRecords() []ConnInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ConnInfo(nil), a.closed...)
}

// A keep-alive tunnel reaches a LiveAccountant when it opens and its bytes
// arrive as they move; it is closed once with matching totals and never
// reported through Record.
func TestExitLiveAccountantSeesOpenTunnel(t *testing.T) {
	host, port := loopbackTarget(t)
	acct := &liveAccountant{}
	allow := NewAllowlist()
	allow.Set([]string{host})
	_, proxy := startExit(t, Config{Policy: allow, Accountant: acct})

	conn, err := socks5Connect(t, proxy, host, port, "source=a&job=1")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	br := bufio.NewReader(conn)
	for range 2 {
		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", host)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("response: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// The tunnel is still open: it was opened once and its bytes are in.
	waitFor(t, func() bool { return acct.in.Load() > 0 && acct.out.Load() > 0 })
	if n := acct.opened.Load(); n != 1 {
		t.Fatalf("opened=%d want 1", n)
	}
	acct.mu.Lock()
	opened := acct.open[0]
	acct.mu.Unlock()
	if !opened.PolicyAllowed || opened.Source != "a" || opened.DestIP == "" || opened.Result != "" {
		t.Errorf("open record=%+v", opened)
	}
	if len(acct.closedRecords()) != 0 {
		t.Fatal("closed before the client hung up")
	}

	_ = conn.Close()
	waitFor(t, func() bool { return len(acct.closedRecords()) == 1 })
	ci := acct.closedRecords()[0]
	if ci.Result != ResultAllowed {
		t.Errorf("result=%q", ci.Result)
	}
	if ci.BytesIn != acct.in.Load() || ci.BytesOut != acct.out.Load() {
		t.Errorf("final bytes in=%d out=%d, live in=%d out=%d", ci.BytesIn, ci.BytesOut, acct.in.Load(), acct.out.Load())
	}
	if _, ok := acct.last(); ok {
		t.Error("an opened connection was also reported through Record")
	}
}

// A connection that never opens a tunnel is reported through Record alone.
func TestExitLiveAccountantDeniedUsesRecord(t *testing.T) {
	acct := &liveAccountant{}
	_, proxy := startExit(t, Config{Accountant: acct})
	_, _ = socks5Connect(t, proxy, "not-allowed.example", 443, "")
	waitFor(t, func() bool { ci, ok := acct.last(); return ok && ci.Result == ResultDenied })
	if n := acct.opened.Load(); n != 0 {
		t.Errorf("opened=%d want 0", n)
	}
}
