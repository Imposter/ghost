package main

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/Imposter/ghost/ghost-go/metrics"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// statusView is the JSON ghost_status_json returns: what the node's Status
// reports, its tunnel address, every peer in the netmap with the state of the
// tunnel to it, and the exit.
type statusView struct {
	// OK is false only on the error form, which carries Error instead.
	OK bool `json:"ok"`
	// Handle is the node's handle, echoed back.
	Handle int64 `json:"handle"`
	// Connected is true while the node is usable: the signalling session is
	// up and the control plane has the node in its network.
	Connected bool `json:"connected"`
	// SignalState is disconnected, connecting, connected or closed. It says
	// nothing about the join: a node whose join the control plane refused
	// (because its owner paused it, say) stays connected and unjoined while
	// it keeps asking.
	SignalState string `json:"signal_state"`
	// Joined is whether the node is in its network. While it is false the
	// node carries no traffic and lists no peers.
	Joined bool `json:"joined"`
	// PeerID is the control plane's id for this peer.
	PeerID string `json:"peer_id"`
	// Network is the joined network.
	Network string `json:"network"`
	// Address is this node's tunnel address in CIDR form
	// ("100.64.0.5/32"); TunnelAddress is the bare IP ("100.64.0.5").
	Address       string `json:"address"`
	TunnelAddress string `json:"tunnel_address"`
	// Roles are this peer's roles, as the netmap reports them (the requested
	// roles until the first netmap arrives).
	Roles []proto.Role `json:"roles"`
	// Isolation is the network's isolation mode ("none" or "hub-only").
	Isolation string `json:"isolation,omitempty"`
	// PeerCount is the number of linked tunnel peers, NetmapPeers the number
	// the netmap lists.
	PeerCount   int `json:"peer_count"`
	NetmapPeers int `json:"netmap_peers"`
	// Peers is every netmap peer, sorted by peer id.
	Peers []peerView `json:"peers"`
	// Exit is the node's exit, or null when it serves none.
	Exit *exitView `json:"exit"`
}

// peerView is one netmap peer and the tunnel to it.
type peerView struct {
	PeerID  string            `json:"peer_id"`
	Name    string            `json:"name,omitempty"`
	Address string            `json:"address"`
	Roles   []proto.Role      `json:"roles"`
	Labels  map[string]string `json:"labels,omitempty"`
	// Online is what the control plane says; Linked is whether this node has
	// a live WireGuard tunnel to the peer.
	Online bool `json:"online"`
	Linked bool `json:"linked"`
	// CandidateType is the selected local ICE candidate type: host, srflx,
	// prflx or relay. It is empty until the link is up.
	CandidateType string  `json:"candidate_type,omitempty"`
	RTTSeconds    float64 `json:"rtt_seconds"`
	RxBytes       uint64  `json:"rx_bytes"`
	TxBytes       uint64  `json:"tx_bytes"`
}

// exitView is the node's exit in a status snapshot.
type exitView struct {
	Enabled bool `json:"enabled"`
	// Listen is the tunnel-side address the exit serves on, empty until the
	// node has joined; Port is the port it will use.
	Listen string `json:"listen"`
	Port   int    `json:"port"`
	// Allow is the control plane's allowlist, LocalAllow the one
	// ghost_set_policy sets (null when the host has set none).
	Allow      []string `json:"allow"`
	LocalAllow []string `json:"local_allow"`
	// Local is the host application's policy, Effective the combination of it
	// and the control plane's that the exit enforces.
	Local     localPolicy `json:"local"`
	Effective localPolicy `json:"effective"`
	// UsedBytes and LimitBytes are today's usage against the effective daily
	// cap (a limit of 0 means unlimited).
	UsedBytes  int64 `json:"used_bytes"`
	LimitBytes int64 `json:"limit_bytes"`
	// Active is the number of live proxied connections.
	Active int `json:"active"`
}

// apiStatus is the implementation behind ghost_status_json.
func apiStatus(h int64) string {
	in, err := nodes.get(h)
	if err != nil {
		return encodeError(err)
	}
	return encodeJSON(in.status())
}

func (in *instance) status() statusView {
	st := in.node.Status()
	v := statusView{
		OK:            true,
		Handle:        in.handle,
		Connected:     st.Connected,
		SignalState:   string(st.SignalState),
		Joined:        st.Joined,
		PeerID:        st.PeerID,
		Network:       st.Network,
		Address:       st.Address,
		TunnelAddress: tunnelIP(st.Address),
		Roles:         st.Roles,
		PeerCount:     st.Peers,
		NetmapPeers:   st.NetmapPeers,
		Peers:         []peerView{},
	}
	if v.Roles == nil {
		v.Roles = []proto.Role{}
	}
	links := map[string]metrics.PeerStat{}
	for _, t := range in.node.Snapshot().Tunnel {
		links[t.PeerID] = t
	}
	if nm, ok := in.node.Netmap(); ok {
		v.Isolation = string(nm.Isolation)
		for _, p := range nm.Peers {
			pv := peerView{
				PeerID: p.PeerID, Name: p.Name, Address: p.Address,
				Roles: p.Roles, Labels: p.Labels, Online: p.Online,
			}
			if pv.Roles == nil {
				pv.Roles = []proto.Role{}
			}
			if t, ok := links[p.PeerID]; ok {
				pv.Linked = true
				pv.CandidateType, pv.RTTSeconds = t.CandidateType, t.RTTSeconds
				pv.RxBytes, pv.TxBytes = t.RxBytes, t.TxBytes
			}
			v.Peers = append(v.Peers, pv)
		}
		slices.SortFunc(v.Peers, func(a, b peerView) int { return strings.Compare(a.PeerID, b.PeerID) })
	}
	if x := in.exit; x != nil {
		used, limit := x.srv.UsageToday()
		v.Exit = &exitView{
			Enabled:    true,
			Listen:     x.listening(),
			Port:       x.port,
			Allow:      entries(x.policy.server),
			LocalAllow: entries(x.policy.local.Load()),
			Local:      x.localView(),
			Effective:  x.effective(),
			UsedBytes:  used,
			LimitBytes: limit,
			Active:     x.srv.ActiveConns(),
		}
	}
	return v
}

// tunnelIP is the bare address of a CIDR, or "" when there is none yet.
func tunnelIP(cidr string) string {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return ""
	}
	return p.Addr().String()
}

// metricsView is the JSON ghost_metrics_json returns: the node's metrics
// snapshot and its recent exit connections, newest first.
type metricsView struct {
	OK       bool             `json:"ok"`
	Handle   int64            `json:"handle"`
	Snapshot metrics.Snapshot `json:"snapshot"`
	// Connections holds up to metrics.DefaultConnections records.
	Connections []metrics.Connection `json:"connections"`
}

// apiMetrics is the implementation behind ghost_metrics_json.
func apiMetrics(h int64) string {
	in, err := nodes.get(h)
	if err != nil {
		return encodeError(err)
	}
	return encodeJSON(in.metrics())
}

func (in *instance) metrics() metricsView {
	// The collector holds the exit-side aggregates whether or not the node
	// serves them in the tunnel; the node adds its identity and the live
	// per-peer tunnel stats.
	s := in.col.Snapshot()
	ns := in.node.Snapshot()
	s.PeerID, s.Address, s.Tunnel = ns.PeerID, ns.Address, ns.Tunnel
	conns := in.col.Recent(metrics.DefaultConnections)
	if conns == nil {
		conns = []metrics.Connection{}
	}
	return metricsView{OK: true, Handle: in.handle, Snapshot: s, Connections: conns}
}
