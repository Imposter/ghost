// Package server assembles ghost-server from its parts: storage, access
// control, the control service, the signalling relay, and the HTTP APIs.
// cmd/ghost-server wires it to configuration, OpenTelemetry and a listener;
// tests use it directly.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
	"github.com/Imposter/ghost/ghost-server/server/signalling"
	"github.com/Imposter/ghost/ghost-server/server/store"
	"github.com/Imposter/ghost/ghost-server/server/telemetry"
	"github.com/Imposter/ghost/ghost-server/server/turn"
)

// Options configures a Server.
type Options struct {
	Config config.Config
	// Store is required; the caller owns it and closes it after the Server.
	Store  store.Store
	Logger *slog.Logger
	// MeterProvider supplies OTel instruments (the global provider when nil).
	MeterProvider metric.MeterProvider
	// AuthorizerClient is the HTTP client for the authorizer webhook
	// (optional).
	AuthorizerClient *http.Client
	// DeviceMetrics plugs in the admin device-metrics proxy (optional).
	DeviceMetrics httpapi.DeviceMetricsProxy
	// Now overrides the clock (optional).
	Now func() time.Time
}

// Server is a running ghost-server instance (without its listener).
type Server struct {
	Service *control.Service
	Relay   *signalling.Relay
	handler http.Handler
	log     *slog.Logger

	stop    context.CancelFunc
	janitor sync.WaitGroup
}

// New builds a Server and creates the configured networks.
func New(ctx context.Context, opts Options) (*Server, error) {
	if opts.Store == nil {
		return nil, errors.New("server: Store is required")
	}
	cfg := opts.Config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("server: config: %w", err)
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	metrics, err := telemetry.New(opts.MeterProvider)
	if err != nil {
		return nil, fmt.Errorf("server: metrics: %w", err)
	}

	var authorizer access.Authorizer = access.Open{}
	cacheTTL := time.Duration(0)
	if cfg.Access.Mode == config.AccessAPI {
		authorizer = access.NewWebhook(cfg.Access.AuthorizerURL, []byte(cfg.Access.AuthorizerSecret),
			cfg.Access.Timeout.Std(), opts.AuthorizerClient, now)
		cacheTTL = cfg.Access.CacheTTL.Std()
	}
	ac := access.NewController(authorizer, access.ControllerOptions{
		CacheTTL: cacheTTL, Logger: log, Metrics: metrics, Now: now,
	})

	svc := control.New(control.Options{
		Store: opts.Store, Access: ac, DefaultPool: cfg.DefaultPool,
		PairingTTL: cfg.Pairing.TTL.Std(), Logger: log, Metrics: metrics, Now: now,
	})
	for _, n := range cfg.Networks {
		if err := svc.EnsureNetwork(ctx, n.Name, n.Pool); err != nil {
			return nil, fmt.Errorf("server: network %s: %w", n.Name, err)
		}
	}

	var turnIssuer *turn.Issuer
	if cfg.ICE.TURNSecret != "" {
		turnIssuer = turn.NewIssuer(cfg.ICE.TURNSecret, cfg.ICE.TURNTTL.Std())
	}
	relay := signalling.New(signalling.Options{
		Service: svc, STUNURLs: cfg.ICE.STUNURLs, TURNURLs: cfg.ICE.TURNURLs, TURN: turnIssuer,
		HeartbeatInterval: cfg.Heartbeat.Interval.Std(), HeartbeatTimeout: cfg.Heartbeat.Timeout.Std(),
		Logger: log, Metrics: metrics,
	})

	mux := http.NewServeMux()
	httpapi.NewPublic(svc, relay, log).Register(mux)
	if cfg.Admin.Token != "" {
		httpapi.NewAdmin(httpapi.AdminOptions{
			Service: svc, Relay: relay, Token: cfg.Admin.Token, Metrics: opts.DeviceMetrics, Logger: log,
		}).Register(mux)
	} else {
		log.Warn("admin API disabled: no admin token configured")
	}

	jctx, stop := context.WithCancel(context.Background())
	s := &Server{Service: svc, Relay: relay, handler: mux, log: log, stop: stop}
	s.janitor.Add(1)
	go s.sweepPairingCodes(jctx, opts.Store, now)
	return s, nil
}

// Handler returns the HTTP handler for the public, signalling and admin APIs.
func (s *Server) Handler() http.Handler { return s.handler }

// Close disconnects every session and stops background work.
func (s *Server) Close() {
	s.stop()
	s.Relay.Close()
	s.janitor.Wait()
}

func (s *Server) sweepPairingCodes(ctx context.Context, st store.Store, now func() time.Time) {
	defer s.janitor.Done()
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.DeleteExpiredPairingCodes(ctx, now()); err != nil {
				s.log.Warn("pairing: sweep expired codes", "error", err)
			} else if n > 0 {
				s.log.Debug("pairing: swept expired codes", "count", n)
			}
		}
	}
}
