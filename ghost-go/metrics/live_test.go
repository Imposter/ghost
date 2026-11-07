package metrics

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

	"github.com/Imposter/ghost/ghost-go/exit"
)

// opening builds the record the exit hands Open: no result, bytes or duration.
func opening(host, tag string) exit.ConnInfo {
	ci := allowed(host, tag, 0, 0)
	ci.Result, ci.Duration, ci.TTFB = "", 0, 0
	return ci
}

// An open connection counts, carries its bytes so far in every aggregate and
// shows in the recent list as open; closing it adds only the remainder, counts
// its result once and turns the same entry into the closed record.
func TestCollectorOpenConnectionIsLive(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	lc := c.Open(opening("api.example", "source=a&job=1"))
	lc.AddBytes(100, 10)

	s := c.Snapshot()
	if s.Totals.Connections != 1 || s.Totals.BytesIn != 100 || s.Totals.BytesOut != 10 {
		t.Fatalf("open totals=%+v", s.Totals.Counts)
	}
	for name, got := range map[string]Counts{
		"destination": s.Destinations[0].Counts, "source": s.Sources[0].Counts, "protocol": s.Protocols[0].Counts,
	} {
		if got != (Counts{Connections: 1, BytesIn: 100, BytesOut: 10}) {
			t.Errorf("open %s=%+v", name, got)
		}
	}
	if len(s.Results) != 0 {
		t.Errorf("results=%+v want none while open", s.Results)
	}
	recent := c.Recent(0)
	if len(recent) != 1 || recent[0].Result != string(exit.ResultOpen) || recent[0].BytesIn != 100 || recent[0].Job != "1" {
		t.Fatalf("recent while open=%+v", recent)
	}

	lc.AddBytes(50, 5)
	if s := c.Snapshot(); s.Totals.BytesIn != 150 || s.Totals.BytesOut != 15 || s.Totals.Connections != 1 {
		t.Fatalf("second snapshot totals=%+v", s.Totals.Counts)
	}

	final := allowed("api.example", "source=a&job=1", 160, 16)
	lc.AddBytes(10, 1)
	lc.Close(final)
	lc.Close(final) // a second close is ignored

	s = c.Snapshot()
	if s.Totals.Counts != (Counts{Connections: 1, BytesIn: 160, BytesOut: 16}) {
		t.Errorf("closed totals=%+v", s.Totals.Counts)
	}
	if s.Destinations[0].Counts != s.Totals.Counts || s.Sources[0].Counts != s.Totals.Counts {
		t.Errorf("closed splits dest=%+v source=%+v", s.Destinations[0].Counts, s.Sources[0].Counts)
	}
	if len(s.Results) != 1 || s.Results[0] != (ResultStat{Result: "allowed", Count: 1}) {
		t.Errorf("results=%+v", s.Results)
	}
	recent = c.Recent(0)
	if len(recent) != 1 || recent[0].Result != "allowed" || recent[0].BytesIn != 160 || recent[0].DurationSeconds != 2 {
		t.Errorf("recent after close=%+v", recent)
	}
}

// Totals never go backwards across snapshots taken while connections move
// bytes, and end at the exact sums. Run with -race.
func TestCollectorLiveConcurrent(t *testing.T) {
	c := NewCollector(CollectorConfig{RingSize: 4})
	const conns, writes = 8, 200
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lc := c.Open(opening("api.example", fmt.Sprintf("source=s%d", i%3)))
			for range writes {
				lc.AddBytes(3, 1)
			}
			lc.Close(allowed("api.example", "", 3*writes, writes))
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	var last Counts
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		s := c.Snapshot()
		if s.Totals.BytesIn < last.BytesIn || s.Totals.BytesOut < last.BytesOut || s.Totals.Connections < last.Connections {
			t.Fatalf("totals went backwards: %+v after %+v", s.Totals.Counts, last)
		}
		last = s.Totals.Counts
		_ = c.Recent(0)
	}
	want := Counts{Connections: conns, BytesIn: 3 * writes * conns, BytesOut: writes * conns}
	if got := c.Snapshot().Totals.Counts; got != want {
		t.Errorf("final totals=%+v want %+v", got, want)
	}
}

// Through a real exit: a keep-alive tunnel's count, bytes and recent entry
// show before it closes, and closing neither double counts nor adds an entry.
func TestCollectorLiveThroughExit(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "live-body")
	}))
	t.Cleanup(target.Close)
	host, p, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	port, _ := strconv.Atoi(p)

	col := NewCollector(CollectorConfig{})
	allow := exit.NewAllowlist()
	allow.Set([]string{host})
	srv := exit.New(exit.Config{Policy: allow, Accountant: col})
	col.AttachExit(srv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	conn := socks5Dial(t, ln.Addr().String(), host, port)
	br := bufio.NewReader(conn)
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", host)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	var open Snapshot
	waitUntil(t, func() bool { open = col.Snapshot(); return open.Totals.BytesIn > 0 })
	if open.Totals.Connections != 1 || open.Totals.Active != 1 || open.Totals.BytesOut == 0 {
		t.Fatalf("open totals=%+v", open.Totals)
	}
	if r := col.Recent(0); len(r) != 1 || r[0].Result != string(exit.ResultOpen) || r[0].BytesIn != open.Totals.BytesIn {
		t.Fatalf("recent while open=%+v", r)
	}

	_ = conn.Close()
	var closed Snapshot
	waitUntil(t, func() bool { closed = col.Snapshot(); return len(closed.Results) == 1 })
	if closed.Totals.Connections != 1 || closed.Totals.BytesIn < open.Totals.BytesIn || closed.Totals.BytesOut < open.Totals.BytesOut {
		t.Errorf("closed totals=%+v open=%+v", closed.Totals.Counts, open.Totals.Counts)
	}
	r := col.Recent(0)
	if len(r) != 1 || r[0].Result != "allowed" || r[0].BytesIn != closed.Totals.BytesIn || r[0].BytesOut != closed.Totals.BytesOut {
		t.Errorf("recent after close=%+v totals=%+v", r, closed.Totals.Counts)
	}
}

// socks5Dial opens a SOCKS5 CONNECT tunnel with no authentication.
func socks5Dial(t *testing.T, proxy, host string, port int) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	req := []byte{0x05, 0x01, 0x00, 0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2+10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[3] != 0x00 {
		t.Fatalf("socks5 reply code %d", reply[3])
	}
	return c
}

func waitUntil(t *testing.T, cond func() bool) {
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
