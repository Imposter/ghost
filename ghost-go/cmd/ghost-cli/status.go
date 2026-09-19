package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// statusView is what a running member serves on -status and what
// "ghost-cli status" prints.
type statusView struct {
	Mode      string       `json:"mode"`
	PeerID    string       `json:"peer_id"`
	Network   string       `json:"network"`
	Address   string       `json:"address"`
	Roles     []proto.Role `json:"roles"`
	Signal    string       `json:"signal"`
	Isolation string       `json:"isolation,omitempty"`
	Peers     []peerView   `json:"peers"`
	Forwards  []string     `json:"forwards,omitempty"`
	Exit      *exitView    `json:"exit,omitempty"`
}

// peerView is one netmap peer and the tunnel to it.
type peerView struct {
	PeerID        string            `json:"peer_id"`
	Name          string            `json:"name,omitempty"`
	Address       string            `json:"address"`
	Roles         []proto.Role      `json:"roles"`
	Labels        map[string]string `json:"labels,omitempty"`
	Online        bool              `json:"online"`
	Linked        bool              `json:"linked"`
	CandidateType string            `json:"candidate_type,omitempty"`
	RTTSeconds    float64           `json:"rtt_seconds,omitempty"`
	RxBytes       uint64            `json:"rx_bytes,omitempty"`
	TxBytes       uint64            `json:"tx_bytes,omitempty"`
}

// exitView is the member's exit.
type exitView struct {
	Listen     string               `json:"listen,omitempty"`
	Allow      []string             `json:"allow,omitempty"`
	LocalAllow []string             `json:"local_allow,omitempty"`
	Paused     bool                 `json:"paused"`
	UsedBytes  int64                `json:"used_bytes"`
	LimitBytes int64                `json:"limit_bytes"`
	Active     int                  `json:"active"`
	Recent     []metrics.Connection `json:"recent"`
}

// statusRecent is how many exit connections the status carries.
const statusRecent = 10

func buildStatus(m member, o runOptions) statusView {
	st := m.Status()
	v := statusView{
		Mode: o.mode, PeerID: st.PeerID, Network: st.Network, Address: st.Address,
		Roles: st.Roles, Signal: string(st.SignalState), Peers: []peerView{},
	}
	for _, f := range o.forwards {
		v.Forwards = append(v.Forwards, f.local+"="+f.target)
	}
	links := map[string]metrics.PeerStat{}
	for _, t := range m.Snapshot().Tunnel {
		links[t.PeerID] = t
	}
	if nm, ok := m.Netmap(); ok {
		v.Isolation = string(nm.Isolation)
		for _, p := range nm.Peers {
			pv := peerView{PeerID: p.PeerID, Name: p.Name, Address: p.Address, Roles: p.Roles, Labels: p.Labels, Online: p.Online}
			if t, ok := links[p.PeerID]; ok {
				pv.Linked, pv.CandidateType, pv.RTTSeconds, pv.RxBytes, pv.TxBytes = true, t.CandidateType, t.RTTSeconds, t.RxBytes, t.TxBytes
			}
			v.Peers = append(v.Peers, pv)
		}
		sort.Slice(v.Peers, func(i, j int) bool { return v.Peers[i].PeerID < v.Peers[j].PeerID })
	}
	if x := o.exit; x != nil {
		used, limit := x.srv.UsageToday()
		ev := &exitView{
			Listen: x.listening(), Allow: entries(x.policy.server), LocalAllow: entries(x.policy.local),
			Paused: x.srv.Paused(), UsedBytes: used, LimitBytes: limit, Active: x.srv.ActiveConns(),
			Recent: []metrics.Connection{},
		}
		if o.collector != nil {
			ev.Recent = o.collector.Recent(statusRecent)
		}
		v.Exit = ev
	}
	return v
}

// statusHandler serves the status as JSON on GET /.
func statusHandler(m member, o runOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildStatus(m, o))
	})
}

func runStatus(ctx context.Context, args []string, e env) error {
	fs := newFlagSet("status", e)
	addr := fs.String("addr", envOr("GHOST_STATUS", defaultStatusAddr), "the running member's -status address ($GHOST_STATUS)")
	asJSON := fs.Bool("json", false, "print the raw JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ghost-cli status [-addr ADDR] [-json]")
		fmt.Fprintln(fs.Output(), "Shows the status of a node, hub or p2p member running on this host.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return fmt.Errorf("unexpected arguments %v", fs.Args())
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+*addr+"/", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("no member answering on %s: %w", *addr, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s: %s", *addr, resp.Status)
	}
	if *asJSON {
		_, err = e.stdout.Write(b)
		return err
	}
	var v statusView
	if err := json.Unmarshal(b, &v); err != nil {
		return errors.New("status: not a ghost-cli member")
	}
	printStatus(e.stdout, v)
	return nil
}

func printStatus(w io.Writer, v statusView) {
	roles := make([]string, len(v.Roles))
	for i, r := range v.Roles {
		roles[i] = string(r)
	}
	fmt.Fprintf(w, "%s %s in %s, address %s, roles %s, signalling %s\n",
		v.Mode, v.PeerID, v.Network, orDash(v.Address), orDash(strings.Join(roles, ",")), orDash(v.Signal))
	if v.Isolation != "" {
		fmt.Fprintf(w, "isolation %s\n", v.Isolation)
	}
	for _, f := range v.Forwards {
		fmt.Fprintf(w, "forward %s\n", f)
	}
	fmt.Fprintf(w, "\npeers (%d):\n", len(v.Peers))
	if len(v.Peers) > 0 {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "  PEER\tNAME\tADDRESS\tROLES\tONLINE\tLINK\tRTT\tRX/TX\tLABELS")
		for _, p := range v.Peers {
			link := "-"
			if p.Linked {
				link = orDash(p.CandidateType)
			}
			roles := make([]string, len(p.Roles))
			for i, r := range p.Roles {
				roles[i] = string(r)
			}
			labels := make([]string, 0, len(p.Labels))
			for k, val := range p.Labels {
				labels = append(labels, k+"="+val)
			}
			sort.Strings(labels)
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%t\t%s\t%.1fms\t%d/%d\t%s\n", p.PeerID, orDash(p.Name), p.Address,
				strings.Join(roles, ","), p.Online, link, p.RTTSeconds*1000, p.RxBytes, p.TxBytes, orDash(strings.Join(labels, ",")))
		}
		_ = tw.Flush()
	}
	if x := v.Exit; x != nil {
		fmt.Fprintf(w, "\nexit on %s: paused %t, %d active, %d/%d bytes today\n", orDash(x.Listen), x.Paused, x.Active, x.UsedBytes, x.LimitBytes)
		if x.Allow != nil {
			fmt.Fprintf(w, "  control-plane allow: %s\n", orDash(strings.Join(x.Allow, " ")))
		}
		if x.LocalAllow != nil {
			fmt.Fprintf(w, "  local allow: %s\n", strings.Join(x.LocalAllow, " "))
		}
		for _, c := range x.Recent {
			fmt.Fprintf(w, "  %s %s %s:%d %s in=%d out=%d source=%s job=%s\n", c.Start.Format(time.TimeOnly), c.SourcePeer,
				c.Host, c.Port, c.Result, c.BytesIn, c.BytesOut, orDash(c.Source), orDash(c.Job))
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
