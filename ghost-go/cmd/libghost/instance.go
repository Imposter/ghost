package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/signal"
)

// errNoSuchHandle is returned for a handle that was never issued or has
// already been stopped.
var errNoSuchHandle = errors.New("no such ghost handle")

// instance is one running node behind a handle. Nothing in it ever crosses
// the C boundary; callers hold only the integer handle.
type instance struct {
	handle int64
	node   *ghost.Node
	col    *metrics.Collector
	exit   *exitRuntime
	events *eventBuffer
	log    *slog.Logger

	cancel context.CancelFunc
	done   chan struct{} // closed when the event pump has stopped

	stopOnce sync.Once
}

// registry maps handles to instances. A Go pointer never leaves the process,
// so the host application cannot hold a stale or forged one: a handle is just
// a number this table knows or does not.
type registry struct {
	mu   sync.Mutex
	next int64
	m    map[int64]*instance
}

var nodes = &registry{next: 1, m: make(map[int64]*instance)}

// add stores in under a fresh handle.
func (r *registry) add(in *instance) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.next
	r.next++
	in.handle = h
	r.m[h] = in
	return h
}

// get looks up a handle.
func (r *registry) get(h int64) (*instance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if in, ok := r.m[h]; ok {
		return in, nil
	}
	return nil, errNoSuchHandle
}

// remove takes a handle out of the table, reporting whether it was there.
func (r *registry) remove(h int64) (*instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.m[h]
	delete(r.m, h)
	return in, ok
}

// testHooks are the seams the in-process tests use: an in-memory signalling
// server and loopback-only ICE, so the suite needs no network, no STUN and no
// firewall exemption. The C entry points never set them.
type testHooks struct {
	dialer      signal.Dialer
	loopbackICE bool
	eventBuffer int
}

func (h *testHooks) tune(cfg *ghost.Config) {
	if h == nil {
		return
	}
	if h.dialer != nil {
		cfg.SignalDialer = h.dialer
	}
	if h.loopbackICE {
		cfg.UseLoopbackICE()
	}
}

func (h *testHooks) bufferSize() int {
	if h == nil {
		return defaultEventBuffer
	}
	return h.eventBuffer
}

// apiStart is the implementation behind ghost_start: it builds a node from
// the JSON config, starts it, and returns its handle.
func apiStart(configJSON string, hooks *testHooks) (int64, error) {
	var sc startConfig
	if err := decodeJSON(configJSON, &sc); err != nil {
		return 0, fmt.Errorf("config: %w", err)
	}
	cfg, log, err := sc.parse()
	if err != nil {
		return 0, err
	}

	// The collector always backs ghost_metrics_json; metrics.enabled only
	// decides whether the node also serves those metrics inside the tunnel,
	// where the netmap's hubs can read them.
	col := metrics.NewCollector(metrics.CollectorConfig{})
	if sc.Metrics != nil && sc.Metrics.Enabled {
		cfg.Metrics = &ghost.MetricsConfig{Collector: col, Port: sc.Metrics.Port}
	}
	hooks.tune(&cfg)

	node, err := ghost.NewNode(cfg)
	if err != nil {
		return 0, err
	}
	in := &instance{
		node:   node,
		col:    col,
		events: newEventBuffer(hooks.bufferSize()),
		log:    log,
		done:   make(chan struct{}),
	}
	if sc.Exit != nil && sc.Exit.Enabled {
		in.exit = newExitRuntime(sc.Exit, exit.Config{
			Logger:         log,
			Accountant:     col,
			PeerResolver:   node,
			AllowSource:    node.IsHubSource,
			MeterProvider:  cfg.MeterProvider,
			TracerProvider: cfg.TracerProvider,
		})
		col.AttachExit(in.exit.srv)
	}

	ctx, cancel := context.WithCancel(context.Background())
	in.cancel = cancel
	if err := node.Start(ctx); err != nil {
		cancel()
		if in.exit != nil {
			_ = in.exit.srv.Close()
		}
		_ = node.Close()
		return 0, err
	}
	go in.pump(ctx)
	return nodes.add(in), nil
}

// pump moves node events into the bounded buffer and keeps the exit in step
// with the control plane. It never blocks on a slow host application.
func (in *instance) pump(ctx context.Context) {
	defer close(in.done)
	events := in.node.Events()
	for {
		select {
		case <-ctx.Done():
			in.events.push(eventJSON{Kind: kindStopped})
			in.events.close()
			return
		case ev, ok := <-events:
			if !ok {
				in.events.push(eventJSON{Kind: kindStopped})
				in.events.close()
				return
			}
			in.onEvent(ev)
		}
	}
}

// onEvent reacts to one node event, then buffers it for the host.
func (in *instance) onEvent(ev ghost.Event) {
	switch ev.Kind {
	case ghost.EventJoined:
		if in.exit != nil && in.exit.listening() == "" {
			if err := in.exit.serve(in.node, ev.Address); err != nil {
				in.log.Error("libghost: exit", "error", err)
				in.events.push(eventJSON{Kind: string(ghost.EventError), Error: err.Error()})
			} else if pol, ok := in.node.Policy(); ok {
				in.exit.setServerPolicy(pol)
			}
		}
	case ghost.EventPolicy:
		if in.exit != nil && ev.Policy != nil {
			in.exit.setServerPolicy(*ev.Policy)
		}
	}
	in.events.push(eventFrom(ev))
}

// stop shuts the node down and releases its resources. It is idempotent.
func (in *instance) stop() error {
	var err error
	in.stopOnce.Do(func() {
		in.cancel()
		if in.exit != nil {
			err = in.exit.srv.Close()
		}
		if cerr := in.node.Close(); err == nil {
			err = cerr
		}
		<-in.done
	})
	return err
}

// apiStop is the implementation behind ghost_stop.
func apiStop(h int64) error {
	in, ok := nodes.remove(h)
	if !ok {
		return errNoSuchHandle
	}
	return in.stop()
}

// apiSetPolicy is the implementation behind ghost_set_policy.
func apiSetPolicy(h int64, policyJSON string) error {
	in, err := nodes.get(h)
	if err != nil {
		return err
	}
	var req policyRequest
	if err := decodeJSON(policyJSON, &req); err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if in.exit == nil {
		return errors.New("policy: this node serves no exit")
	}
	in.exit.setLocalPolicy(req)
	return nil
}

// apiNextEvent is the implementation behind ghost_next_event_json. It waits
// up to timeoutMS for the next event (a negative timeout waits until one
// arrives or the node stops) and reports false on timeout, on an unknown
// handle, and once a stopped node's events have run out.
func apiNextEvent(h int64, timeoutMS int) (string, bool) {
	in, err := nodes.get(h)
	if err != nil {
		return "", false
	}
	ev, ok := in.events.next(time.Duration(timeoutMS) * time.Millisecond)
	if !ok {
		return "", false
	}
	return encodeJSON(ev), true
}

// apiVersion is the implementation behind ghost_version.
func apiVersion() string { return Version }

// A node's tunnel netstack is where its exit listens.
var _ tunnelListener = (*ghost.Node)(nil)
