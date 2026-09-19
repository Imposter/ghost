package main

import (
	"context"
	"fmt"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/otelsetup"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

func runNode(ctx context.Context, args []string, e env) error {
	fs := newFlagSet("node", e)
	var cf controlFlags
	cf.register(fs)
	var mf memberFlags
	mf.register(fs)
	var xf exitFlags
	xf.register(fs, true)
	roles := newListFlag("GHOST_ROLES")
	fs.Var(roles, "roles", "roles to ask for, e.g. exit; -exit asks for exit (repeatable; $GHOST_ROLES)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ghost-cli node [flags]")
		fmt.Fprintln(fs.Output(), "Joins the network as a node and runs until interrupted. With -exit it serves an")
		fmt.Fprintln(fs.Output(), "exit on its tunnel IP: the control plane's exit policy applies, -allow narrows it,")
		fmt.Fprintln(fs.Output(), "and only hubs may use it.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return fmt.Errorf("unexpected arguments %v", fs.Args())
	}
	log, err := stderrLogger(e, mf.logLevel)
	if err != nil {
		return err
	}
	forwards, err := parseForwards(*mf.forwards)
	if err != nil {
		return err
	}
	cfg, err := cf.config()
	if err != nil {
		return err
	}
	mf.apply(&cfg, log)
	if cfg.Roles, err = parseRoles(*roles); err != nil {
		return err
	}
	if xf.enabled {
		cfg.Roles = append(cfg.Roles, proto.RoleExit)
	}

	col := metrics.NewCollector(metrics.CollectorConfig{})
	exitCfg := exit.Config{Logger: log}
	if xf.metrics {
		setup, err := otelsetup.New(ctx, otelsetup.Options{ServiceName: "ghost-cli", InstanceID: otelsetup.InstanceIDFromEnv()})
		if err != nil {
			return fmt.Errorf("metrics: %w", err)
		}
		defer func() { _ = setup.Shutdown(context.WithoutCancel(ctx)) }()
		cfg.MeterProvider = setup.MeterProvider
		cfg.TracerProvider = setup.TracerProvider
		cfg.Metrics = &ghost.MetricsConfig{Collector: col, Prometheus: setup.PrometheusHandler}
		exitCfg.MeterProvider = setup.MeterProvider
		exitCfg.TracerProvider = setup.TracerProvider
	}
	e.hooks.tune(&cfg)
	node, err := ghost.NewNode(cfg)
	if err != nil {
		return err
	}
	x, err := newExit(xf, true, node, col, exitCfg)
	if err != nil {
		return err
	}
	return runMember(ctx, node, runOptions{
		mode: "node", log: log, status: mf.status, forwards: forwards, exit: x, collector: col, hooks: e.hooks,
	})
}
