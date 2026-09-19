// Package server assembles ghost-server, the ghost peer control plane, from
// its parts: storage, the policy engine, the external authorizer, the control
// service, the event bus, the signalling relay, and the HTTP APIs.
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
	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
	"github.com/Imposter/ghost/ghost-server/server/policy"
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
	// Now overrides the clock (optional).
	Now func() time.Time
}

// Server is a running ghost-server instance (without its listener).
type Server struct {
	Service *control.Service
	Relay   *signalling.Relay
	Bus     *events.Bus
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

	bus := events.NewBus(4096, now)
	svc := control.New(control.Options{
		Store: opts.Store, Access: ac, Bus: bus, DefaultPool: cfg.DefaultPool,
		EnrollmentTTL: cfg.Enrollment.CodeTTL.Std(), PollInterval: cfg.Enrollment.PollInterval.Std(),
		EphemeralGrace: cfg.Peers.EphemeralGrace.Std(), Logger: log, Metrics: metrics, Now: now,
	})
	sysCtx := control.WithActor(ctx, "system")
	for _, n := range cfg.Networks {
		if err := svc.EnsureNetwork(sysCtx, control.NetworkInput{Name: n.Name, Pool: n.Pool, Isolation: policy.Isolation(n.Isolation)}); err != nil {
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
	httpapi.NewPeerAPI(svc, relay, log).Register(mux)
	if cfg.Control.ServiceToken != "" {
		httpapi.NewControlAPI(httpapi.ControlOptions{
			Service: svc, Relay: relay, ServiceToken: cfg.Control.ServiceToken, Logger: log,
		}).Register(mux)
	} else {
		log.Warn("control API disabled: no service token configured")
	}

	jctx, stop := context.WithCancel(context.Background())
	s := &Server{Service: svc, Relay: relay, Bus: bus, handler: mux, log: log, stop: stop}
	s.janitor.Add(1)
	go s.housekeeping(jctx, cfg.Peers.JanitorInterval.Std())
	return s, nil
}

// Handler returns the HTTP handler for the peer API, signalling and the
// control API.
func (s *Server) Handler() http.Handler { return s.handler }

// Close disconnects every session and stops background work.
func (s *Server) Close() {
	s.stop()
	s.Relay.Close()
	s.janitor.Wait()
}

func (s *Server) housekeeping(ctx context.Context, every time.Duration) {
	defer s.janitor.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Service.Sweep(ctx)
		}
	}
}
