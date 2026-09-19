package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/ghost/direct"
	"github.com/Imposter/ghost/ghost-go/metrics"
)

// Default tunnel addresses for the two sides of a p2p link.
const (
	defaultInviteAddr = "100.64.0.1/32"
	defaultAcceptAddr = "100.64.0.2/32"
)

func runP2P(ctx context.Context, args []string, e env) error {
	if len(args) == 0 || (args[0] != "invite" && args[0] != "accept") {
		fmt.Fprintln(e.stderr, "Usage: ghost-cli p2p invite [flags]")
		fmt.Fprintln(e.stderr, "       ghost-cli p2p accept [flags] [TOKEN]")
		return errors.New(`p2p needs "invite" or "accept"`)
	}
	inviting := args[0] == "invite"
	fs := newFlagSet("p2p "+args[0], e)
	var mf memberFlags
	mf.register(fs)
	var xf exitFlags
	xf.register(fs, false)
	def := defaultAcceptAddr
	if inviting {
		def = defaultInviteAddr
	}
	addr := fs.String("addr", def, "this member's tunnel address; the two sides need different ones")
	ttl := fs.Duration("token-ttl", direct.DefaultTokenTTL, "how long the token this side creates stays valid")
	fs.Usage = func() {
		o := fs.Output()
		if inviting {
			fmt.Fprintln(o, "Usage: ghost-cli p2p invite [flags]")
			fmt.Fprintln(o, "Prints an invite token, then reads the other side's answer token from stdin.")
		} else {
			fmt.Fprintln(o, "Usage: ghost-cli p2p accept [flags] [TOKEN]")
			fmt.Fprintln(o, "Answers an invite (TOKEN, or a line on stdin) and prints the answer token to paste back.")
		}
		fmt.Fprintln(o, "The member then runs until interrupted, with no control plane: no policy, no")
		fmt.Fprintln(o, "isolation, and any linked peer may reach it. -exit needs -allow.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if inviting && fs.NArg() > 0 || fs.NArg() > 1 {
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
	sig, err := direct.New(direct.Config{Address: *addr, TokenTTL: *ttl})
	if err != nil {
		return err
	}
	// The invited side starts ICE when it accepts, so the answer must come
	// back within the connect timeout.
	cfg := ghost.Config{Signaller: sig, ConnectTimeout: 5 * time.Minute}
	mf.apply(&cfg, log)
	e.hooks.tune(&cfg)
	node, err := ghost.NewNode(cfg)
	if err != nil {
		return err
	}
	col := metrics.NewCollector(metrics.CollectorConfig{})
	x, err := newExit(xf, false, node, col, exit.Config{Logger: log})
	if err != nil {
		return err
	}

	// Exchange tokens once the member has started, then keep running.
	started := make(chan struct{})
	x2 := &lineExchanger{e: e, r: bufio.NewReader(e.stdin)}
	if !inviting && fs.NArg() == 1 {
		x2.first = fs.Arg(0)
	}
	errc := make(chan error, 1)
	go func() {
		select {
		case <-started:
		case <-ctx.Done():
			return
		}
		if inviting {
			errc <- direct.Invite(ctx, sig, x2)
		} else {
			errc <- direct.Answer(ctx, sig, x2)
		}
	}()
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		if err := <-errc; err != nil {
			cancel(err)
		}
	}()
	err = runMember(runCtx, &startSignal{member: node, started: started}, runOptions{
		mode: "p2p", log: log, status: mf.status, forwards: forwards, exit: x, collector: col, hooks: e.hooks,
	})
	if cause := context.Cause(runCtx); err == nil && cause != nil && !errors.Is(cause, context.Canceled) {
		return fmt.Errorf("token exchange: %w", cause)
	}
	return err
}

// startSignal closes started once the member has started.
type startSignal struct {
	member
	started chan struct{}
}

func (s *startSignal) Start(ctx context.Context) error {
	if err := s.member.Start(ctx); err != nil {
		return err
	}
	close(s.started)
	return nil
}

// lineExchanger swaps tokens as lines: it prints ours to stdout and reads
// the other side's from stdin (or takes the first from the command line).
type lineExchanger struct {
	e     env
	r     *bufio.Reader
	first string
}

func (l *lineExchanger) Send(_ context.Context, token string) error {
	fmt.Fprintln(l.e.stderr, "token for the other side:")
	_, err := fmt.Fprintln(l.e.stdout, token)
	return err
}

func (l *lineExchanger) Receive(ctx context.Context) (string, error) {
	if l.first != "" {
		t := l.first
		l.first = ""
		return t, nil
	}
	fmt.Fprintln(l.e.stderr, "paste the other side's token:")
	type line struct {
		s   string
		err error
	}
	ch := make(chan line, 1)
	go func() {
		s, err := l.r.ReadString('\n')
		ch <- line{strings.TrimSpace(s), err}
	}()
	select {
	case got := <-ch:
		if got.s == "" && got.err != nil {
			return "", fmt.Errorf("read token: %w", got.err)
		}
		return got.s, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
