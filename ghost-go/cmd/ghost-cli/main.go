// Command ghost-cli is a small command-line member for ghost networks, built
// on the public ghost-go API. It enrols a peer, runs a node (optionally with
// an exit and in-tunnel metrics) or a hub, links two members peer to peer
// with no server, and reports a running member's status.
//
//	ghost-cli enroll  -server URL -auth-key gak_…      enrol with a pre-auth key
//	ghost-cli node    [-exit -allow host:port] [-metrics]
//	ghost-cli hub     [-forward LOCAL=PEER:PORT]
//	ghost-cli hub     dial PEER PORT                   pipe stdin/stdout to a peer
//	ghost-cli hub     curl [-source S -job J] PEER URL fetch URL through PEER's exit
//	ghost-cli hub     metrics PEER                     read PEER's in-tunnel metrics
//	ghost-cli p2p     invite | accept [TOKEN]          link two members with no server
//	ghost-cli status  [-addr 127.0.0.1:9465]           a running member's status
//
// Run "ghost-cli <command> -h" for the flags of each command.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const usage = `ghost-cli: a command-line ghost member

Usage:
  ghost-cli enroll  -server URL -auth-key KEY [-name N] [-label k=v]   enrol with a pre-auth key
  ghost-cli node    [flags]                          run a node (-exit, -allow, -metrics, -forward)
  ghost-cli hub     [flags]                          run a hub (-forward)
  ghost-cli hub     [flags] dial PEER PORT           pipe stdin/stdout to PEER:PORT over the tunnel
  ghost-cli hub     [flags] curl [-source S] [-job J] PEER URL
                                                     fetch URL through PEER's exit
  ghost-cli hub     [flags] metrics [-connections N | -prometheus] PEER
                                                     read PEER's in-tunnel metrics
  ghost-cli p2p     invite [flags]                   create an invite; paste the answer back
  ghost-cli p2p     accept [flags] [TOKEN]           answer an invite (TOKEN or stdin)
  ghost-cli status  [-addr ADDR] [-json]             show a running member's status

PEER is a peer id, a peer name or a tunnel IP. Run "ghost-cli <command> -h"
for the flags of a command. Most flags also read an environment variable,
shown in the flag help.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := run(ctx, os.Args[1:], env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr})
	switch {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "ghost-cli:", err)
		os.Exit(1)
	}
}

// env is the process environment a command runs in; tests replace it.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// hooks are test seams (see testHooks).
	hooks *testHooks
}

// run dispatches one command.
func run(ctx context.Context, args []string, e env) error {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return flag.ErrHelp
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "enroll":
		return runEnroll(ctx, rest, e)
	case "node":
		return runNode(ctx, rest, e)
	case "hub":
		return runHub(ctx, rest, e)
	case "p2p":
		return runP2P(ctx, rest, e)
	case "status":
		return runStatus(ctx, rest, e)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(e.stdout, usage)
		return nil
	}
	fmt.Fprint(e.stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

// newFlagSet returns a flag set that reports errors to e.stderr.
func newFlagSet(name string, e env) *flag.FlagSet {
	fs := flag.NewFlagSet("ghost-cli "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	return fs
}

// envOr returns the environment variable name, or def when it is unset.
func envOr(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

// listFlag is a repeatable flag whose values may also be comma-separated.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

// newListFlag returns a listFlag preset from a comma-separated environment
// variable.
func newListFlag(envName string) *listFlag {
	l := &listFlag{}
	_ = l.Set(os.Getenv(envName))
	return l
}

// newLogger returns a text logger on w at the named level.
func newLogger(w io.Writer, level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", level, err)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l})), nil
}
