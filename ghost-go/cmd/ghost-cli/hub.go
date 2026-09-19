package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// oneShot is a hub subcommand that runs once the hub has linked to its peer.
type oneShot struct {
	peer string
	run  func(ctx context.Context, hub *ghost.Hub, peer proto.PeerInfo) error
}

func runHub(ctx context.Context, args []string, e env) error {
	fs := newFlagSet("hub", e)
	var cf controlFlags
	cf.register(fs)
	var mf memberFlags
	mf.register(fs)
	wait := fs.Duration("wait", time.Minute, "how long dial, curl and metrics wait for the peer to connect")
	fs.Usage = func() {
		o := fs.Output()
		fmt.Fprintln(o, "Usage: ghost-cli hub [flags]                                    run a hub until interrupted")
		fmt.Fprintln(o, "       ghost-cli hub [flags] dial PEER PORT                     pipe stdin/stdout to PEER:PORT")
		fmt.Fprintln(o, "       ghost-cli hub [flags] curl [-source S] [-job J] PEER URL  fetch URL through PEER's exit")
		fmt.Fprintln(o, "       ghost-cli hub [flags] metrics [-connections N | -prometheus] PEER")
		fmt.Fprintln(o, "PEER is a peer id, name or tunnel IP. The peer must hold the hub role.")
		fmt.Fprintln(o, "A one-shot command opens its own session, which replaces a running hub's session for the same peer.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	var shot *oneShot
	if fs.NArg() > 0 {
		var err error
		if shot, err = parseOneShot(fs.Args(), e); err != nil {
			return err
		}
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
	e.hooks.tune(&cfg)
	hub, err := ghost.NewHub(cfg)
	if err != nil {
		return err
	}
	if shot == nil {
		return runMember(ctx, hub, runOptions{mode: "hub", log: log, status: mf.status, forwards: forwards, hooks: e.hooks})
	}

	if err := hub.Start(ctx); err != nil {
		return err
	}
	defer hub.Close()
	peer, err := waitPeer(ctx, hub, shot.peer, *wait)
	if err != nil {
		return err
	}
	return shot.run(ctx, hub, peer)
}

// parseOneShot parses a hub subcommand and its flags.
func parseOneShot(args []string, e env) (*oneShot, error) {
	switch args[0] {
	case "dial":
		fs := newFlagSet("hub dial", e)
		fs.Usage = func() { fmt.Fprintln(fs.Output(), "Usage: ghost-cli hub [flags] dial PEER PORT") }
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		if fs.NArg() != 2 {
			fs.Usage()
			return nil, errors.New("dial needs PEER and PORT")
		}
		port, err := strconv.Atoi(fs.Arg(1))
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("port %q: want 1-65535", fs.Arg(1))
		}
		return &oneShot{peer: fs.Arg(0), run: func(ctx context.Context, hub *ghost.Hub, p proto.PeerInfo) error {
			return hubDial(ctx, hub, p, port, e)
		}}, nil

	case "curl":
		fs := newFlagSet("hub curl", e)
		source := fs.String("source", "", "source name for the exit's metrics (the source part of the tag)")
		job := fs.String("job", "", "job id, kept in the exit's connection log only")
		port := fs.Int("exit-port", exit.DefaultPort, "the node's tunnel-side exit port")
		include := fs.Bool("i", false, "print the response status and headers to stdout before the body")
		fs.Usage = func() {
			fmt.Fprintln(fs.Output(), "Usage: ghost-cli hub [flags] curl [-source S] [-job J] [-exit-port P] [-i] PEER URL")
			fmt.Fprintln(fs.Output(), "GETs URL through PEER's exit (HTTP CONNECT over the tunnel). The body goes to stdout.")
			fs.PrintDefaults()
		}
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		if fs.NArg() != 2 {
			fs.Usage()
			return nil, errors.New("curl needs PEER and URL")
		}
		target, err := url.Parse(fs.Arg(1))
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			return nil, fmt.Errorf("URL %q: want http(s)://host/...", fs.Arg(1))
		}
		tag := exit.SourceTag{Source: *source, Job: *job}.String()
		return &oneShot{peer: fs.Arg(0), run: func(ctx context.Context, hub *ghost.Hub, p proto.PeerInfo) error {
			return hubCurl(ctx, hub, p, *port, tag, target, *include, e)
		}}, nil

	case "metrics":
		fs := newFlagSet("hub metrics", e)
		conns := fs.Int("connections", 0, "print the last N exit connections instead of the snapshot")
		prom := fs.Bool("prometheus", false, "print the Prometheus text instead of the snapshot")
		fs.Usage = func() {
			fmt.Fprintln(fs.Output(), "Usage: ghost-cli hub [flags] metrics [-connections N | -prometheus] PEER")
			fs.PrintDefaults()
		}
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		if fs.NArg() != 1 {
			fs.Usage()
			return nil, errors.New("metrics needs PEER")
		}
		return &oneShot{peer: fs.Arg(0), run: func(ctx context.Context, hub *ghost.Hub, p proto.PeerInfo) error {
			return hubMetrics(ctx, hub, p, *conns, *prom, e)
		}}, nil
	}
	return nil, fmt.Errorf("unknown hub command %q (want dial, curl or metrics)", args[0])
}

// waitPeer waits until ref names a netmap peer the hub has a working link to.
func waitPeer(ctx context.Context, hub *ghost.Hub, ref string, timeout time.Duration) (proto.PeerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		p, known := resolvePeer(hub, ref)
		if known && slices.Contains(hub.Peers(), p.PeerID) {
			return p, nil
		}
		select {
		case <-ctx.Done():
			switch {
			case !known:
				return p, fmt.Errorf("peer %q is not in the hub's netmap (after %s)", ref, timeout)
			case !p.Online:
				return p, fmt.Errorf("peer %q (%s) is offline", ref, p.PeerID)
			}
			return p, fmt.Errorf("no tunnel to peer %q (%s) after %s", ref, p.PeerID, timeout)
		case <-hub.Events():
			// Drained so the hub never blocks on its event channel.
		case <-tick.C:
		}
	}
}

// peerAddr returns a peer's tunnel address with port.
func peerAddr(p proto.PeerInfo, port int) (string, error) {
	pfx, err := netip.ParsePrefix(p.Address)
	if err != nil {
		return "", fmt.Errorf("peer %s address %q: %w", p.PeerID, p.Address, err)
	}
	return net.JoinHostPort(pfx.Addr().String(), strconv.Itoa(port)), nil
}

func hubDial(ctx context.Context, hub *ghost.Hub, p proto.PeerInfo, port int, e env) error {
	addr, err := peerAddr(p, port)
	if err != nil {
		return err
	}
	c, err := hub.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer c.Close()
	go func() {
		_, _ = io.Copy(c, e.stdin)
		closeWrite(c)
	}()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(e.stdout, c)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return nil
	}
}

func hubCurl(ctx context.Context, hub *ghost.Hub, p proto.PeerInfo, port int, tag string, target *url.URL, include bool, e env) error {
	proxy, err := peerAddr(p, port)
	if err != nil {
		return err
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			c, err := hub.DialContext(ctx, "tcp", proxy)
			if err != nil {
				return nil, fmt.Errorf("dial exit %s: %w", proxy, err)
			}
			tc, err := httpConnect(ctx, c, addr, tag)
			if err != nil {
				_ = c.Close()
				return nil, err
			}
			return tc, nil
		},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if include {
		fmt.Fprintf(e.stdout, "%s %s\r\n", resp.Proto, resp.Status)
		_ = resp.Header.Write(e.stdout)
		fmt.Fprint(e.stdout, "\r\n")
	} else {
		fmt.Fprintf(e.stderr, "%s %s\n", resp.Proto, resp.Status)
	}
	_, err = io.Copy(e.stdout, resp.Body)
	return err
}

// httpConnect asks the exit on c to open a tunnel to target (host:port),
// sending tag in the source header. It returns the tunnelled connection.
func httpConnect(ctx context.Context, c net.Conn, target, tag string) (net.Conn, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
		defer func() { _ = c.SetDeadline(time.Time{}) }()
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if tag != "" {
		req += exit.DefaultSourceHeader + ": " + tag + "\r\n"
	}
	if _, err := io.WriteString(c, req+"\r\n"); err != nil {
		return nil, fmt.Errorf("CONNECT %s: %w", target, err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("CONNECT %s: %w", target, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CONNECT %s: exit answered %s", target, resp.Status)
	}
	return &bufferedConn{Conn: c, r: br}, nil
}

// bufferedConn reads through the reader that parsed the CONNECT response.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *bufferedConn) CloseWrite() error {
	if cw, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return b.Conn.Close()
}

func hubMetrics(ctx context.Context, hub *ghost.Hub, p proto.PeerInfo, conns int, prom bool, e env) error {
	var v any
	switch {
	case prom:
		b, err := hub.NodePrometheus(ctx, p.PeerID)
		if err != nil {
			return err
		}
		_, err = e.stdout.Write(b)
		return err
	case conns > 0:
		c, err := hub.NodeConnections(ctx, p.PeerID, conns)
		if err != nil {
			return err
		}
		v = c
	default:
		s, err := hub.NodeSnapshot(ctx, p.PeerID)
		if err != nil {
			return err
		}
		v = s
	}
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
