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
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestExitMetricsAndSpan(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "payload-body-data")
	}))
	defer target.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	allow := NewAllowlist()
	allow.Set([]string{host})
	_, proxy := startExit(t, Config{Policy: allow, MeterProvider: mp, TracerProvider: tp})

	conn, err := socks5Connect(t, proxy, host, port, "source=metrics-test&job=j-17")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	_, _ = io.ReadAll(conn)
	_ = conn.Close()

	// Wait until the connection metric appears.
	var rm metricdata.ResourceMetrics
	waitFor(t, func() bool {
		rm = metricdata.ResourceMetrics{}
		if err := reader.Collect(context.Background(), &rm); err != nil {
			return false
		}
		return findMetric(rm, "ghost.exit.connections") != nil
	})

	conns := findMetric(rm, "ghost.exit.connections")
	sum, ok := conns.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) == 0 {
		t.Fatalf("no data points for connections")
	}
	dp := sum.DataPoints[0]
	if dp.Value != 1 {
		t.Errorf("connections=%d want 1", dp.Value)
	}
	assertAttr(t, dp.Attributes, "result", "allowed")
	assertAttr(t, dp.Attributes, "network.transport", "tcp")
	assertAttr(t, dp.Attributes, "network.protocol.name", "socks5")
	assertAttr(t, dp.Attributes, "server.address", host)
	assertAttr(t, dp.Attributes, "ghost.source.name", "metrics-test")
	for _, k := range []attribute.Key{"ghost.source.tag", "ghost.source.job"} {
		if _, ok := dp.Attributes.Value(k); ok {
			t.Errorf("metric carries the high-cardinality %s attribute", k)
		}
	}

	// Byte counters should sum to what the accountant saw.
	bytes := findMetric(rm, "ghost.exit.bytes")
	if bytes == nil {
		t.Fatal("no ghost.exit.bytes metric")
	}
	bsum := bytes.Data.(metricdata.Sum[int64])
	var totalBytes int64
	for _, p := range bsum.DataPoints {
		totalBytes += p.Value
	}
	if totalBytes == 0 {
		t.Error("byte counter is zero")
	}

	// One span per connection with the semconv attributes.
	spans := sr.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	span := spans[len(spans)-1]
	if span.Name() != "exit.connection" {
		t.Errorf("span name=%q", span.Name())
	}
	spanAttrs := map[string]string{}
	for _, a := range span.Attributes() {
		spanAttrs[string(a.Key)] = a.Value.String()
	}
	if spanAttrs["result"] != "allowed" {
		t.Errorf("span result=%q", spanAttrs["result"])
	}
	// The resolved IP must be on the span, not in the metric attributes.
	if spanAttrs["server.socket.address"] == "" {
		t.Error("expected resolved IP on the span")
	}
	// The raw tag and both its parts are on the span.
	if spanAttrs["ghost.source.tag"] != "source=metrics-test&job=j-17" || spanAttrs["ghost.source.name"] != "metrics-test" ||
		spanAttrs["ghost.source.job"] != "j-17" {
		t.Errorf("span source attributes=%v", spanAttrs)
	}
}

func TestExitMetricsDeniedHostCollapsed(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	allow := NewAllowlist()
	allow.Set([]string{"example.com:443"})
	_, proxy := startExit(t, Config{Policy: allow, MeterProvider: mp})

	// Try an arbitrary disallowed host; server.address must collapse to "denied".
	_, _ = socks5Connect(t, proxy, "arbitrary-evil-host.example.net", 8443, "")

	var rm metricdata.ResourceMetrics
	waitFor(t, func() bool {
		rm = metricdata.ResourceMetrics{}
		_ = reader.Collect(context.Background(), &rm)
		m := findMetric(rm, "ghost.exit.connections")
		return m != nil
	})
	conns := findMetric(rm, "ghost.exit.connections")
	sum := conns.Data.(metricdata.Sum[int64])
	found := false
	for _, dp := range sum.DataPoints {
		v, _ := dp.Attributes.Value("server.address")
		if v.AsString() == "arbitrary-evil-host.example.net" {
			t.Error("disallowed host leaked into server.address (cardinality risk)")
		}
		r, _ := dp.Attributes.Value("result")
		if r.AsString() == "denied" {
			found = true
			if v.AsString() != "denied" {
				t.Errorf("denied connection server.address=%q want denied", v.AsString())
			}
		}
	}
	if !found {
		t.Fatal("no denied connection recorded")
	}
}

func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

func assertAttr(t *testing.T, set attribute.Set, key, want string) {
	t.Helper()
	v, ok := set.Value(attribute.Key(key))
	if !ok {
		t.Errorf("attribute %q missing", key)
		return
	}
	if v.AsString() != want {
		t.Errorf("attribute %q=%q want %q", key, v.AsString(), want)
	}
}
