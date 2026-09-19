// Package otelsetup wires the OpenTelemetry SDK and exporters for ghost
// binaries (a node, a hub, the signalling server, or a CLI). Library packages
// (ghost, exit, signal) depend only on the OTel API and take a MeterProvider /
// TracerProvider; this package is where the SDK, the Prometheus exporter, and
// the optional OTLP exporters actually live, which keeps the libraries free of
// the SDK.
//
// The Prometheus HTTP handler returned here is meant to be served on the
// node's tunnel IP only (over the netstack), so /metrics is never reachable
// from the owner's LAN or the internet.
package otelsetup

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexp "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Options configures the OTel setup.
type Options struct {
	// ServiceName, ServiceVersion and InstanceID populate the OTel resource.
	ServiceName    string
	ServiceVersion string
	InstanceID     string

	// EnableOTLP turns on the OTLP metric and trace exporters, configured from
	// the standard OTEL_EXPORTER_OTLP_* environment variables. When false (the
	// default) only the in-process Prometheus exporter is wired.
	EnableOTLP bool

	// SetGlobals installs the created providers as the OTel globals so library
	// code using otel.GetMeterProvider()/GetTracerProvider() picks them up.
	SetGlobals bool
}

// Setup holds the created providers and the Prometheus HTTP handler.
type Setup struct {
	MeterProvider  *sdkmetric.MeterProvider
	TracerProvider *sdktrace.TracerProvider
	// PrometheusHandler serves the Prometheus text/JSON exposition. Mount it at
	// /metrics on a listener bound to the tunnel IP.
	PrometheusHandler http.Handler

	shutdowns []func(context.Context) error
}

// New builds a MeterProvider (with a Prometheus exporter, and optionally
// OTLP) and a TracerProvider (OTLP when enabled). Call Shutdown to flush.
func New(ctx context.Context, opts Options) (*Setup, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(opts.ServiceName),
			semconv.ServiceVersion(opts.ServiceVersion),
			semconv.ServiceInstanceID(opts.InstanceID),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	reg := prometheus.NewRegistry()
	promExporter, err := promexp.New(promexp.WithRegisterer(reg))
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}

	metricOpts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExporter),
	}

	s := &Setup{
		PrometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	}

	if opts.EnableOTLP {
		mExp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("otlp metric exporter: %w", err)
		}
		metricOpts = append(metricOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(mExp)))
		s.shutdowns = append(s.shutdowns, mExp.Shutdown)

		tExp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("otlp trace exporter: %w", err)
		}
		s.TracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(tExp),
		)
	} else {
		s.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
	}

	s.MeterProvider = sdkmetric.NewMeterProvider(metricOpts...)
	s.shutdowns = append(s.shutdowns, s.MeterProvider.Shutdown, s.TracerProvider.Shutdown)

	if opts.SetGlobals {
		otel.SetMeterProvider(s.MeterProvider)
		otel.SetTracerProvider(s.TracerProvider)
	}
	return s, nil
}

// Shutdown flushes and stops all exporters and providers.
func (s *Setup) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, fn := range s.shutdowns {
		if err := fn(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// InstanceIDFromEnv returns a stable-ish instance id: OTEL_SERVICE_INSTANCE_ID
// if set, else the hostname.
func InstanceIDFromEnv() string {
	if v := os.Getenv("OTEL_SERVICE_INSTANCE_ID"); v != "" {
		return v
	}
	h, _ := os.Hostname()
	return h
}
