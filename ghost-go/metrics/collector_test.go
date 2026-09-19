package metrics

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
)

type fakeExit struct{}

func (fakeExit) ActiveConns() int                { return 2 }
func (fakeExit) UsageToday() (used, limit int64) { return 300, 1000 }
func (fakeExit) Paused() bool                    { return false }

func allowed(host string, tag string, in, out int64) exit.ConnInfo {
	return exit.ConnInfo{
		SourcePeer: "hub-1", SourceTag: tag, Protocol: exit.ProtoSOCKS5,
		DestHost: host, DestPort: 443, DestIP: "203.0.113.7", PolicyAllowed: true,
		Result: exit.ResultAllowed, BytesIn: in, BytesOut: out,
		Duration: 2 * time.Second, TTFB: 50 * time.Millisecond, Start: time.Now(),
	}
}

func denied(host string) exit.ConnInfo {
	return exit.ConnInfo{
		SourcePeer: "hub-1", Protocol: exit.ProtoHTTPConnect, DestHost: host, DestPort: 8443,
		Result: exit.ResultDenied, Start: time.Now(),
	}
}

func TestCollectorDeniedHostCardinalityCap(t *testing.T) {
	c := NewCollector(CollectorConfig{DeniedTopN: 3})
	// A heavy hitter interleaved with a long tail of one-off hosts: the tail
	// never grows the tracker past its cap, and the hitter keeps its count.
	for i := range 40 {
		c.Record(denied("persistent.example"))
		c.Record(denied(fmt.Sprintf("random-%d.example", i)))
	}
	s := c.Snapshot()
	if len(s.DeniedHosts) != 3 {
		t.Fatalf("denied hosts=%d want 3", len(s.DeniedHosts))
	}
	if s.DeniedHosts[0].Host != "persistent.example" || s.DeniedHosts[0].Count != 40 {
		t.Errorf("top denied=%+v", s.DeniedHosts[0])
	}
	if len(s.Destinations) != 1 || s.Destinations[0].Host != exit.DeniedHost || s.Destinations[0].Port != 0 {
		t.Errorf("destinations=%+v want a single %q entry", s.Destinations, exit.DeniedHost)
	}
	if s.Totals.Denied != 80 || s.Totals.Connections != 80 {
		t.Errorf("totals=%+v", s.Totals)
	}
}

func TestCollectorSnapshotJSONShape(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	c.AttachExit(fakeExit{})
	c.Record(allowed("api.example", "job=1", 100, 10))
	c.Record(allowed("api.example", "job=2", 50, 5))
	c.Record(denied("evil.example"))

	b, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"time", "totals", "destinations", "sources", "protocols", "results", "denied_hosts", "tunnel"} {
		if _, ok := m[k]; !ok {
			t.Errorf("snapshot JSON missing %q: %s", k, b)
		}
	}
	if m["tunnel"] == nil {
		t.Error("tunnel must be an empty array, not null")
	}
	totals := m["totals"].(map[string]any)
	want := map[string]float64{
		"connections": 3, "bytes_in": 150, "bytes_out": 15, "active": 2,
		"denied": 1, "cap_used_bytes": 300, "cap_limit_bytes": 1000,
	}
	for k, v := range want {
		if totals[k] != v {
			t.Errorf("totals.%s=%v want %v", k, totals[k], v)
		}
	}
	dest := m["destinations"].([]any)[0].(map[string]any)
	if dest["host"] != "api.example" || dest["port"] != float64(443) || dest["connections"] != float64(2) {
		t.Errorf("destinations[0]=%v", dest)
	}
	src := m["sources"].([]any)
	if len(src) != 3 {
		t.Errorf("sources=%v want 3 (two tags + untagged)", src)
	}
	proto := m["protocols"].([]any)[0].(map[string]any)
	if proto["protocol"] != "socks5" || proto["transport"] != "tcp" {
		t.Errorf("protocols[0]=%v", proto)
	}
	res := m["results"].([]any)[0].(map[string]any)
	if res["result"] != "allowed" || res["count"] != float64(2) {
		t.Errorf("results[0]=%v", res)
	}
}

func TestCollectorRingBufferLimit(t *testing.T) {
	c := NewCollector(CollectorConfig{RingSize: 5})
	for i := range 8 {
		c.Record(allowed(fmt.Sprintf("h%d.example", i), "", 1, 1))
	}
	all := c.Recent(0)
	if len(all) != 5 {
		t.Fatalf("recent=%d want 5", len(all))
	}
	if all[0].Host != "h7.example" || all[4].Host != "h3.example" {
		t.Errorf("order: first=%s last=%s want h7..h3", all[0].Host, all[4].Host)
	}
	if got := c.Recent(2); len(got) != 2 {
		t.Errorf("Recent(2)=%d", len(got))
	}
	if all[0].IP != "203.0.113.7" || all[0].DurationSeconds != 2 || all[0].TTFBSeconds != 0.05 {
		t.Errorf("connection fields=%+v", all[0])
	}
}
