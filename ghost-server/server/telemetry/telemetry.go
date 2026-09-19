// Package telemetry defines ghost-server's OpenTelemetry instruments. Like the
// ghost-go libraries, it depends only on the OTel API: the binary owns the SDK
// and the Prometheus exporter and passes a MeterProvider in.
package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/Imposter/ghost/ghost-server"

// Metrics holds the server's instruments. A nil *Metrics is valid and records
// nothing, so packages can be used without telemetry in tests.
type Metrics struct {
	sessions       metric.Int64UpDownCounter
	messages       metric.Int64Counter
	authzDecisions metric.Int64Counter
	authzLatency   metric.Float64Histogram
	registrations  metric.Int64Counter
	revocations    metric.Int64Counter
	heartbeatDrops metric.Int64Counter
}

// New creates the instruments from mp (the global provider when nil).
func New(mp metric.MeterProvider) (*Metrics, error) {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	m := mp.Meter(meterName)
	var (
		out Metrics
		err error
	)
	if out.sessions, err = m.Int64UpDownCounter("ghost_server.sessions.active",
		metric.WithDescription("Live signalling sessions."), metric.WithUnit("{session}")); err != nil {
		return nil, err
	}
	if out.messages, err = m.Int64Counter("ghost_server.signal.messages",
		metric.WithDescription("Signalling frames by type and direction."), metric.WithUnit("{message}")); err != nil {
		return nil, err
	}
	if out.authzDecisions, err = m.Int64Counter("ghost_server.authz.decisions",
		metric.WithDescription("Access-control decisions by action and result."), metric.WithUnit("{decision}")); err != nil {
		return nil, err
	}
	if out.authzLatency, err = m.Float64Histogram("ghost_server.authz.duration",
		metric.WithDescription("Authorizer webhook round-trip time."), metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if out.registrations, err = m.Int64Counter("ghost_server.enrollments",
		metric.WithDescription("Peer enrolments by result."), metric.WithUnit("{peer}")); err != nil {
		return nil, err
	}
	if out.revocations, err = m.Int64Counter("ghost_server.peers.revoked",
		metric.WithDescription("Peers revoked through the control API."), metric.WithUnit("{peer}")); err != nil {
		return nil, err
	}
	if out.heartbeatDrops, err = m.Int64Counter("ghost_server.heartbeat.timeouts",
		metric.WithDescription("Sessions closed for missing heartbeats."), metric.WithUnit("{session}")); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionOpened / SessionClosed track live sessions by role.
func (m *Metrics) SessionOpened(ctx context.Context, role string) {
	if m != nil {
		m.sessions.Add(ctx, 1, metric.WithAttributes(attribute.String("role", role)))
	}
}

func (m *Metrics) SessionClosed(ctx context.Context, role string) {
	if m != nil {
		m.sessions.Add(ctx, -1, metric.WithAttributes(attribute.String("role", role)))
	}
}

// Message counts one signalling frame; direction is "in" or "out".
func (m *Metrics) Message(ctx context.Context, msgType, direction string) {
	if m != nil {
		m.messages.Add(ctx, 1, metric.WithAttributes(
			attribute.String("type", msgType), attribute.String("direction", direction)))
	}
}

// AuthzDecision counts one access decision. result is allow, deny or error;
// cached reports whether it came from the decision cache.
func (m *Metrics) AuthzDecision(ctx context.Context, action, result string, cached bool) {
	if m != nil {
		m.authzDecisions.Add(ctx, 1, metric.WithAttributes(
			attribute.String("action", action), attribute.String("result", result), attribute.Bool("cached", cached)))
	}
}

// AuthzLatency records one authorizer round trip in seconds.
func (m *Metrics) AuthzLatency(ctx context.Context, seconds float64) {
	if m != nil {
		m.authzLatency.Record(ctx, seconds)
	}
}

// Registration counts a peer enrolment; result is ok or denied.
func (m *Metrics) Registration(ctx context.Context, result string) {
	if m != nil {
		m.registrations.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
}

// Revoked counts one peer revocation.
func (m *Metrics) Revoked(ctx context.Context) {
	if m != nil {
		m.revocations.Add(ctx, 1)
	}
}

// HeartbeatTimeout counts one session dropped for silence.
func (m *Metrics) HeartbeatTimeout(ctx context.Context) {
	if m != nil {
		m.heartbeatDrops.Add(ctx, 1)
	}
}
