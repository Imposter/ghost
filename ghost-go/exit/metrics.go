package exit

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/Imposter/ghost/ghost-go/internal/bounded"
)

// meterName is the instrumentation scope name (the package import path).
const meterName = "github.com/Imposter/ghost/ghost-go/exit"

// meterVersion is the instrumentation scope version.
const meterVersion = "0.3.0"

// DeniedHost is the server.address value every destination refused by policy
// collapses to, so an arbitrary requested host can never become a label.
const DeniedHost = "denied"

// DefaultMaxSources bounds the distinct ghost.source.name label values.
const DefaultMaxSources = 64

// capState is the part of the cap controller the metrics observe.
type capState interface {
	UsageToday() (used, limit int64)
	Paused() bool
}

// exitMetrics holds the OTel instruments for the exit server. They are created
// once in the server constructor, follow the OTel semantic conventions, and
// carry units. Attribute cardinality is bounded:
//   - server.address is the requested host only when the policy permitted it,
//     otherwise DeniedHost; server.port is set only for permitted hosts;
//   - tls.server.name is set only when the SNI itself is permitted by policy;
//   - ghost.source.name is the source part of the tag only (never the job or
//     the raw tag), passed through SourceLabel, and admits the first
//     MaxSources values, then OverflowSource.
//
// The resolved IP, the raw tag and the job never enter a metric attribute;
// they live in spans and the connections ring buffer only.
type exitMetrics struct {
	tracer  trace.Tracer
	policy  Policy
	sources *bounded.Set

	connections metric.Int64Counter     // ghost.exit.connections {result,...}
	bytes       metric.Int64Counter     // ghost.exit.bytes {direction,...}
	duration    metric.Float64Histogram // ghost.exit.duration (s)
	ttfb        metric.Float64Histogram // ghost.exit.ttfb (s)
	active      metric.Int64UpDownCounter

	reg metric.Registration // cap gauges callback
}

func newExitMetrics(cfg Config, caps capState) (*exitMetrics, error) {
	mp := cfg.MeterProvider
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	tp := cfg.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	maxSources := cfg.MaxSources
	if maxSources <= 0 {
		maxSources = DefaultMaxSources
	}
	meter := mp.Meter(meterName, metric.WithInstrumentationVersion(meterVersion))
	m := &exitMetrics{
		tracer:  tp.Tracer(meterName, trace.WithInstrumentationVersion(meterVersion)),
		policy:  cfg.Policy,
		sources: bounded.NewSet(maxSources),
	}

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

	capUsed, err := meter.Int64ObservableGauge("ghost.exit.cap.used",
		metric.WithDescription("Bytes transferred today against the daily cap"),
		metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	capLimit, err := meter.Int64ObservableGauge("ghost.exit.cap.limit",
		metric.WithDescription("Daily byte cap (0 = unlimited)"),
		metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	paused, err := meter.Int64ObservableGauge("ghost.exit.paused",
		metric.WithDescription("1 when the exit is paused by its owner"),
		metric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	m.reg, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		used, limit := caps.UsageToday()
		o.ObserveInt64(capUsed, used)
		o.ObserveInt64(capLimit, limit)
		p := int64(0)
		if caps.Paused() {
			p = 1
		}
		o.ObserveInt64(paused, p)
		return nil
	}, capUsed, capLimit, paused)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (m *exitMetrics) close() {
	if m != nil && m.reg != nil {
		_ = m.reg.Unregister()
	}
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

// labelAttrs builds the bounded-cardinality attribute set for a connection's
// metrics. Only a destination the policy permitted becomes a label.
func (m *exitMetrics) labelAttrs(ci ConnInfo) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.NetworkTransportTCP,
		attribute.String("network.protocol.name", protocolName(ci.Protocol)),
	}
	if ci.PolicyAllowed {
		attrs = append(attrs,
			attribute.String("server.address", ci.DestHost),
			attribute.Int("server.port", ci.DestPort))
		if ci.SNI != "" && m.policy.Allow(ci.SNI, ci.DestPort) {
			attrs = append(attrs, attribute.String("tls.server.name", ci.SNI))
		}
	} else {
		attrs = append(attrs, attribute.String("server.address", DeniedHost))
	}
	if ci.SourcePeer != "" {
		attrs = append(attrs, attribute.String("ghost.source.peer", ci.SourcePeer))
	}
	if src := SourceLabel(ci.Source); src != "" {
		if src != OverflowSource {
			src = m.sources.Admit(src)
		}
		attrs = append(attrs, attribute.String("ghost.source.name", src))
	}
	return attrs
}

// spanAttrs builds the full-detail attribute set for a connection's span.
// The unbounded values (the requested host even when denied, the resolved IP,
// the raw tag and its job) belong on the span, never in metric attributes.
func spanAttrs(ci ConnInfo) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.NetworkTransportTCP,
		attribute.String("network.protocol.name", protocolName(ci.Protocol)),
		attribute.String("server.address", ci.DestHost),
		attribute.Int("server.port", ci.DestPort),
		attribute.String("result", string(ci.Result)),
		attribute.Int64("ghost.exit.bytes_in", ci.BytesIn),
		attribute.Int64("ghost.exit.bytes_out", ci.BytesOut),
	}
	if ci.DestIP != "" {
		attrs = append(attrs, attribute.String("server.socket.address", ci.DestIP))
	}
	if ci.SNI != "" {
		attrs = append(attrs, attribute.String("tls.server.name", ci.SNI))
	}
	if ci.SourcePeer != "" {
		attrs = append(attrs, attribute.String("ghost.source.peer", ci.SourcePeer))
	}
	if ci.SourceTag != "" {
		attrs = append(attrs, attribute.String("ghost.source.tag", ci.SourceTag))
	}
	if ci.Source != "" {
		attrs = append(attrs, attribute.String("ghost.source.name", ci.Source))
	}
	if ci.Job != "" {
		attrs = append(attrs, attribute.String("ghost.source.job", ci.Job))
	}
	return attrs
}

// record emits metrics for a finished connection: exactly one increment of
// ghost.exit.connections, plus bytes, duration and (when a byte arrived) TTFB.
func (m *exitMetrics) record(ctx context.Context, ci ConnInfo) {
	if m == nil {
		return
	}
	attrs := m.labelAttrs(ci)
	withResult := append([]attribute.KeyValue{attribute.String("result", string(ci.Result))}, attrs...)
	m.connections.Add(ctx, 1, metric.WithAttributes(withResult...))
	if ci.BytesIn > 0 {
		m.bytes.Add(ctx, ci.BytesIn, metric.WithAttributes(append([]attribute.KeyValue{attribute.String("direction", "in")}, attrs...)...))
	}
	if ci.BytesOut > 0 {
		m.bytes.Add(ctx, ci.BytesOut, metric.WithAttributes(append([]attribute.KeyValue{attribute.String("direction", "out")}, attrs...)...))
	}
	m.duration.Record(ctx, ci.Duration.Seconds(), metric.WithAttributes(withResult...))
	if ci.Result == ResultAllowed && ci.TTFB > 0 {
		m.ttfb.Record(ctx, ci.TTFB.Seconds(), metric.WithAttributes(attrs...))
	}
}
