// Command p2p links two ghost members with no control plane. Run one side
// with -invite and the other without; paste each printed token into the other
// process. The invited side then serves an echo on its tunnel address and the
// inviting side sends a line through it.
//
//	go run ./examples/p2p -invite -addr 100.64.0.1/32
//	go run ./examples/p2p -addr 100.64.0.2/32
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/ghost/direct"
)

// lines exchanges tokens as lines on stdin and stdout.
type lines struct{ r *bufio.Reader }

func (lines) Send(_ context.Context, token string) error {
	_, err := fmt.Printf("token (paste into the other side):\n%s\n", token)
	return err
}

func (l lines) Receive(context.Context) (string, error) {
	fmt.Println("paste the other side's token:")
	s, err := l.r.ReadString('\n')
	return strings.TrimSpace(s), err
}

func main() {
	invite := flag.Bool("invite", false, "create the invite (the other side answers)")
	addr := flag.String("addr", "", "this member's tunnel address, e.g. 100.64.0.1/32")
	stun := flag.String("stun", "", "optional STUN URL, e.g. stun:stun.l.google.com:19302")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sig, err := direct.New(direct.Config{Address: *addr})
	if err != nil {
		log.Fatal(err)
	}
	cfg := ghost.Config{Signaller: sig, ConnectTimeout: 5 * time.Minute}
	if *stun != "" {
		cfg.STUNServers = []ghost.STUNServer{{URL: *stun}}
	}
	node, err := ghost.NewNode(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer node.Close()
	if err := node.Start(ctx); err != nil {
		log.Fatal(err)
	}

	x := lines{r: bufio.NewReader(os.Stdin)}
	if *invite {
		err = direct.Invite(ctx, sig, x)
	} else {
		err = direct.Answer(ctx, sig, x)
	}
	if err != nil {
		log.Fatal(err)
	}

	var peer ghost.Event
	for peer.Kind != ghost.EventPeerConnected {
		select {
		case peer = <-node.Events():
		case <-ctx.Done():
			return
		}
	}
	peerIP := netip.MustParsePrefix(peer.Address).Addr()
	log.Printf("connected to %s (%s)", peer.PeerID, peerIP)

	if !*invite {
		self := netip.MustParsePrefix(*addr).Addr()
		ln, err := node.Listen("tcp", netip.AddrPortFrom(self, 7000).String())
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("echo on %s:7000; Ctrl-C to stop", self)
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
			}
		}()
		<-ctx.Done()
		return
	}

	c, err := node.DialContext(ctx, "tcp", netip.AddrPortFrom(peerIP, 7000).String())
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintln(c, "hello over ghost")
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("echo: %s", strings.TrimSpace(line))
}
