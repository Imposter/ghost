// Command ghost-server is the ghost peer control plane: networks and their
// policies, peers and their enrolment, netmap distribution over the v1
// signalling WebSocket, TURN credentials, the optional external authorizer,
// and the control API with its watch stream.
//
// Configuration comes from an optional JSON file (-config) and GHOST_*
// environment variables; see docs/ghost-server.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Imposter/ghost/ghost-go/otelsetup"

	"github.com/Imposter/ghost/ghost-server/server"
	"github.com/Imposter/ghost/ghost-server/server/config"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ghost-server:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv("GHOST_CONFIG"), "path to a JSON config file (optional)")
	flag.Parse()

	cfg, err := config.Load(*configPath, nil)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	tel, err := otelsetup.New(ctx, otelsetup.Options{
		ServiceName:    "ghost-server",
		ServiceVersion: version,
		InstanceID:     otelsetup.InstanceIDFromEnv(),
		EnableOTLP:     cfg.OTLP,
		SetGlobals:     true,
	})
	if err != nil {
		return err
	}
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = tel.Shutdown(sctx)
	}()

	st, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	srv, err := server.New(ctx, server.Options{Config: cfg, Store: st, Logger: log, MeterProvider: tel.MeterProvider})
	if err != nil {
		return err
	}
	defer srv.Close()

	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())
	servers := []*http.Server{{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}}
	if cfg.MetricsListen != "" {
		mm := http.NewServeMux()
		mm.Handle("GET /metrics", tel.PrometheusHandler)
		servers = append(servers, &http.Server{Addr: cfg.MetricsListen, Handler: mm, ReadHeaderTimeout: 10 * time.Second})
	} else {
		mux.Handle("GET /metrics", tel.PrometheusHandler)
	}

	errc := make(chan error, len(servers))
	for _, hs := range servers {
		log.Info("ghost-server listening", "addr", hs.Addr, "version", version, "access_mode", cfg.Access.Mode, "db", cfg.Database.Driver)
		go func() {
			if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		log.Info("ghost-server shutting down")
	case err = <-errc:
	}
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	for _, hs := range servers {
		_ = hs.Shutdown(sctx)
	}
	return err
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
