package exit

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// meterName is the instrumentation scope name (the package import path).
const meterName = "github.com/Imposter/ghost/ghost-go/exit"

// meterVersion is the instrumentation scope version.
const meterVersion = "0.1.0"

// exitMetrics holds the OTel instruments for the exit server. They are created
// once in the server constructor, follow the OTel semantic conventions, and
// carry units. Attribute cardinality is kept bounded: only allowlisted hosts
// appear as server.address; everything else collapses to "denied". The
// resolved IP never enters a metric attribute — it lives in spans and the
// connections ring buffer only.
type exitMetrics struct {
	tracer trace.Tracer

	connections metric.Int64Counter     // ghost.exit.connections {result}
	bytes       metric.Int64Counter     // ghost.exit.bytes {direction}
	duration    metric.Float64Histogram // ghost.exit.duration (s)
	ttfb        metric.Float64Histogram // ghost.exit.ttfb (s)
	active      metric.Int64UpDownCounter
}

func newExitMetrics(mp metric.MeterProvider, tp trace.TracerProvider) (*exitMetrics, error) {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	meter := mp.Meter(meterName, metric.WithInstrumentationVersion(meterVersion))
	m := &exitMetrics{tracer: tp.Tracer(meterName, trace.WithInstrumentationVersion(meterVersion))}

	var err error
	if m.connections, err = meter.Int64Counter("ghost.exit.connections",
		metric.WithDescription("Exit connection attempts by result"),
		metric.WithUnit("{connection}")); err != nil {
		return nil, err
	}
	if m.bytes, err = meter.Int64Counter("ghost.exit.bytes",
		metric.WithDescription("Bytes proxied through the exit by direction"),
		metric.WithUnit("By")); err != nil {
		return nil, err
	}
	if m.duration, err = meter.Float64Histogram("ghost.exit.duration",
		metric.WithDescription("Exit connection lifetime"),
		metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if m.ttfb, err = meter.Float64Histogram("ghost.exit.ttfb",
		metric.WithDescription("Time to first byte from the target"),
		metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if m.active, err = meter.Int64UpDownCounter("ghost.exit.active_connections",
		metric.WithDescription("Currently active proxied connections"),
		metric.WithUnit("{connection}")); err != nil {
		return nil, err
	}
	return m, nil
}

// protocolName maps a Protocol to its network.protocol.name value.
func protocolName(p Protocol) string {
	switch p {
	case ProtoSOCKS5:
		return "socks5"
	case ProtoHTTPConnect:
		return "http-connect"
	default:
		return string(p)
	}
}

// baseAttrs builds the low-cardinality semconv attribute set for a connection.
// server.address is the host only when it was allowed by policy; otherwise it
// is "denied" so a rejected arbitrary host cannot explode cardinality.
func baseAttrs(ci ConnInfo, allowed bool) []attribute.KeyValue {
	host := "denied"
	if allowed {
		host = ci.DestHost
	}
	attrs := []attribute.KeyValue{
		semconv.NetworkTransportTCP,
		attribute.String("network.protocol.name", protocolName(ci.Protocol)),
		attribute.String("server.address", host),
		attribute.Int("server.port", ci.DestPort),
	}
	if ci.SNI != "" && allowed {
		attrs = append(attrs, attribute.String("tls.server.name", ci.SNI))
	}
	if ci.SourcePeer != "" {
		attrs = append(attrs, attribute.String("ghost.source.peer", ci.SourcePeer))
	}
	if ci.SourceTag != "" {
		attrs = append(attrs, attribute.String("ghost.source.tag", ci.SourceTag))
	}
	return attrs
}

// record emits metrics for a finished connection.
func (m *exitMetrics) record(ctx context.Context, ci ConnInfo) {
	if m == nil {
		return
	}
	allowed := ci.Result == ResultAllowed
	attrs := baseAttrs(ci, allowed)
	resultAttrs := append([]attribute.KeyValue{attribute.String("result", string(ci.Result))}, attrs...)
	m.connections.Add(ctx, 1, metric.WithAttributes(resultAttrs...))
	if ci.BytesIn > 0 {
		m.bytes.Add(ctx, ci.BytesIn, metric.WithAttributes(append([]attribute.KeyValue{attribute.String("direction", "in")}, attrs...)...))
	}
	if ci.BytesOut > 0 {
		m.bytes.Add(ctx, ci.BytesOut, metric.WithAttributes(append([]attribute.KeyValue{attribute.String("direction", "out")}, attrs...)...))
	}
	m.duration.Record(ctx, ci.Duration.Seconds(), metric.WithAttributes(attrs...))
	if allowed && ci.TTFB > 0 {
		m.ttfb.Record(ctx, ci.TTFB.Seconds(), metric.WithAttributes(attrs...))
	}
}
