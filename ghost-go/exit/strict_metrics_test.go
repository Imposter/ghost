package exit

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fixedResolver maps every address to one peer id.
type fixedResolver string

func (f fixedResolver) PeerForAddr(net.Addr) string { return string(f) }

func loopbackTarget(t *testing.T) (host string, port int) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "strict-metrics-body")
	}))
	t.Cleanup(target.Close)
	h, p, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	port, _ = strconv.Atoi(p)
	return h, port
}

func fetchThrough(t *testing.T, proxy, host string, port int, tag string) {
	t.Helper()
	conn, err := socks5Connect(t, proxy, host, port, tag)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	_, _ = io.ReadAll(conn)
	_ = conn.Close()
}

func collect(t *testing.T, r metric.Reader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

// Each connection increments exactly one ghost.exit.connections series with
// the right labels, and the byte counters match the accounting records.
func TestExitEachConnectionOneSeries(t *testing.T) {
	host, port := loopbackTarget(t)
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	var mu sync.Mutex
	var recs []ConnInfo
	acct := AccountantFunc(func(ci ConnInfo) { mu.Lock(); recs = append(recs, ci); mu.Unlock() })

	allow := NewAllowlist()
	allow.Set([]string{host})
	_, proxy := startExit(t, Config{
		Policy: allow, Accountant: acct, PeerResolver: fixedResolver("hub-1"),
		MeterProvider: mp, TracerProvider: tp,
	})

	fetchThrough(t, proxy, host, port, "source=a&job=1")
	fetchThrough(t, proxy, host, port, "source=b&job=2")
	_, _ = socks5Connect(t, proxy, "not-allowed.example", 443, "source=a&job=3")

	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(recs) == 3 })
	rm := collect(t, reader)

	sum := findMetric(rm, "ghost.exit.connections").Data.(metricdata.Sum[int64])
	if len(sum.DataPoints) != 3 {
		t.Fatalf("series=%d want 3 (one per distinct label set)", len(sum.DataPoints))
	}
	seen := map[string]int64{}
	for _, dp := range sum.DataPoints {
		r, _ := dp.Attributes.Value("result")
		tag, _ := dp.Attributes.Value("ghost.source.name")
		addr, _ := dp.Attributes.Value("server.address")
		seen[r.AsString()+"|"+tag.AsString()+"|"+addr.AsString()] = dp.Value
		assertAttr(t, dp.Attributes, "ghost.source.peer", "hub-1")
		assertAttr(t, dp.Attributes, "network.transport", "tcp")
		assertAttr(t, dp.Attributes, "network.protocol.name", "socks5")
		if r.AsString() == "denied" {
			if _, ok := dp.Attributes.Value("server.port"); ok {
				t.Error("denied series carries server.port")
			}
		}
	}
	for _, k := range []string{"allowed|a|" + host, "allowed|b|" + host, "denied|a|denied"} {
		if seen[k] != 1 {
			t.Errorf("series %q = %d want 1 (all: %v)", k, seen[k], seen)
		}
	}

	mu.Lock()
	var wantIn, wantOut int64
	for _, r := range recs {
		wantIn += r.BytesIn
		wantOut += r.BytesOut
		if r.SourcePeer != "hub-1" {
			t.Errorf("SourcePeer=%q", r.SourcePeer)
		}
	}
	mu.Unlock()
	var gotIn, gotOut int64
	for _, dp := range findMetric(rm, "ghost.exit.bytes").Data.(metricdata.Sum[int64]).DataPoints {
		d, _ := dp.Attributes.Value("direction")
		if d.AsString() == "in" {
			gotIn += dp.Value
		} else {
			gotOut += dp.Value
		}
	}
	if gotIn != wantIn || gotOut != wantOut || wantIn == 0 || wantOut == 0 {
		t.Errorf("bytes in=%d/%d out=%d/%d (metric/record)", gotIn, wantIn, gotOut, wantOut)
	}

	// One span per connection; the denied one keeps its real host on the span.
	spans := sr.Ended()
	if len(spans) != 3 {
		t.Fatalf("spans=%d want 3", len(spans))
	}
	var deniedHost string
	for _, s := range spans {
		m := map[attribute.Key]string{}
		for _, a := range s.Attributes() {
			m[a.Key] = a.Value.Emit()
		}
		if m["result"] == "denied" {
			deniedHost = m["server.address"]
		}
	}
	if deniedHost != "not-allowed.example" {
		t.Errorf("denied span server.address=%q", deniedHost)
	}
}

// Without a resolver the source peer is the client's IP, never IP:port.
func TestExitSourcePeerDefaultsToIP(t *testing.T) {
	var got ConnInfo
	done := make(chan struct{})
	_, proxy := startExit(t, Config{Accountant: AccountantFunc(func(ci ConnInfo) { got = ci; close(done) })})
	_, _ = socks5Connect(t, proxy, "x.example", 443, "")
	<-done
	if got.SourcePeer != "127.0.0.1" {
		t.Errorf("SourcePeer=%q want 127.0.0.1", got.SourcePeer)
	}
}

// Only a tag's source part becomes a label: jobs never add series, and
// sources beyond MaxSources fold into OverflowSource. The record keeps the raw
// tag, the source and the job.
func TestExitSourceNameCardinalityCap(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	var mu sync.Mutex
	var recs []ConnInfo
	_, proxy := startExit(t, Config{
		MaxSources: 2, MeterProvider: mp,
		Accountant: AccountantFunc(func(ci ConnInfo) { mu.Lock(); recs = append(recs, ci); mu.Unlock() }),
	})
	tags := []string{
		"source=a&job=1", "source=a&job=2", "source=a&job=3", // one source, many jobs
		"source=b&job=4",
		"source=c&job=5", "d", // beyond the bound (a bare tag is a source name)
		"job=6", // no source: no source label at all
	}
	for i, tag := range tags {
		_, _ = socks5Connect(t, proxy, "x.example", 443, tag)
		waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(recs) == i+1 })
	}
	distinct := map[string]int64{}
	for _, dp := range findMetric(collect(t, reader), "ghost.exit.connections").Data.(metricdata.Sum[int64]).DataPoints {
		v, ok := dp.Attributes.Value("ghost.source.name")
		if !ok {
			distinct["<none>"] += dp.Value
			continue
		}
		distinct[v.AsString()] += dp.Value
	}
	want := map[string]int64{"a": 3, "b": 1, OverflowSource: 2, "<none>": 1}
	if len(distinct) != len(want) {
		t.Fatalf("source series=%v want %v", distinct, want)
	}
	for k, v := range want {
		if distinct[k] != v {
			t.Errorf("source series[%q]=%d want %d (all %v)", k, distinct[k], v, distinct)
		}
	}
	if r := recs[4]; r.SourceTag != "source=c&job=5" || r.Source != "c" || r.Job != "5" {
		t.Errorf("record lost the tag: %+v", r)
	}
	if r := recs[6]; r.Source != "" || r.Job != "6" {
		t.Errorf("job-only record: %+v", r)
	}
}

// The cap and pause gauges report the controller state.
func TestExitCapGauges(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	s := New(Config{MeterProvider: mp, DailyCapBytes: 1000})
	defer s.Close()
	s.SetPaused(true)
	rm := collect(t, reader)
	gauge := func(name string) int64 {
		m := findMetric(rm, name)
		if m == nil {
			t.Fatalf("%s missing", name)
		}
		return m.Data.(metricdata.Gauge[int64]).DataPoints[0].Value
	}
	if gauge("ghost.exit.cap.limit") != 1000 || gauge("ghost.exit.cap.used") != 0 || gauge("ghost.exit.paused") != 1 {
		t.Errorf("cap gauges limit=%d used=%d paused=%d", gauge("ghost.exit.cap.limit"),
			gauge("ghost.exit.cap.used"), gauge("ghost.exit.paused"))
	}
}
