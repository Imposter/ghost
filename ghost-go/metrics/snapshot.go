// Package metrics serves and fetches a ghost node's strict metrics.
//
// A node owns a Collector (an exit.Accountant) that keeps bounded in-process
// aggregates and the recent-connections ring buffer. The node serves them on
// its tunnel IP only, through Handler:
//
//	GET /metrics                      Prometheus text (from otelsetup)
//	GET /metrics?format=json          a JSON Snapshot
//	GET /metrics/connections?limit=N  the most recent connections, newest first
//
// A hub reads them over the tunnel with Client, and an in-process reader gets
// the same Snapshot from ghost.Node.Snapshot, so the JSON shape here is the
// single contract for every consumer.
//
// The package depends only on the exit package and the standard library; the
// OpenTelemetry SDK stays in binaries (otelsetup).
package metrics

import (
	"time"

	"github.com/Imposter/ghost/ghost-go/exit"
)

// DefaultPort is the tunnel-side TCP port a node serves its metrics on. It is
// the conventional OpenTelemetry Prometheus exporter port.
const DefaultPort = 9464

// Paths served by Handler.
const (
	PathMetrics     = "/metrics"
	PathConnections = "/metrics/connections"
)

// Counts are connection and byte totals for one aggregation key.
type Counts struct {
	Connections int64 `json:"connections"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
}

func (c *Counts) add(ci exit.ConnInfo) {
	c.Connections++
	c.BytesIn += ci.BytesIn
	c.BytesOut += ci.BytesOut
}

// Totals are node-wide figures.
type Totals struct {
	Counts
	// Active is the number of live proxied connections.
	Active int `json:"active"`
	// Denied counts connections refused by policy or the safety guard.
	Denied int64 `json:"denied"`
	// CapUsedBytes and CapLimitBytes are today's usage against the daily cap
	// (a limit of 0 means unlimited).
	CapUsedBytes  int64 `json:"cap_used_bytes"`
	CapLimitBytes int64 `json:"cap_limit_bytes"`
	// Paused is true while the owner has paused the exit.
	Paused bool `json:"paused"`
}

// DestinationStat aggregates one destination. Host is an allowlisted host, or
// exit.DeniedHost (with port 0) for everything the policy refused.
type DestinationStat struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Counts
}

// SourceStat aggregates one source: the requesting peer and the source part
// of the tag it sent (see exit.ParseSourceTag). Jobs are never aggregated.
// Names that are not valid labels, and names beyond the collector's bound,
// fold into exit.OverflowSource.
type SourceStat struct {
	Peer   string `json:"peer"`
	Source string `json:"source,omitempty"`
	Counts
}

// ProtocolStat aggregates one proxy protocol ("socks5" or "http_connect")
// over one transport ("tcp").
type ProtocolStat struct {
	Protocol  string `json:"protocol"`
	Transport string `json:"transport"`
	Counts
}

// ResultStat counts connections with one result.
type ResultStat struct {
	Result string `json:"result"`
	Count  int64  `json:"count"`
}

// HostCount is one entry of the capped top-N denied hosts.
type HostCount struct {
	Host  string `json:"host"`
	Count int64  `json:"count"`
}

// PeerStat is the tunnel state of one peer.
type PeerStat struct {
	PeerID string `json:"peer_id"`
	// Address is the peer's tunnel address (CIDR).
	Address string `json:"address,omitempty"`
	// CandidateType is the selected local ICE candidate type (host, srflx,
	// prflx or relay).
	CandidateType string `json:"candidate_type,omitempty"`
	// RTTSeconds is the ICE round-trip time (0 when not yet measured).
	RTTSeconds float64 `json:"rtt_seconds"`
	// LastHandshake is the last WireGuard handshake (zero when none yet), and
	// HandshakeAgeSeconds its age at snapshot time.
	LastHandshake       time.Time `json:"last_handshake,omitzero"`
	HandshakeAgeSeconds float64   `json:"handshake_age_seconds,omitempty"`
	// RxBytes and TxBytes are transport bytes received from / sent to the peer.
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// Snapshot is a point-in-time view of a node's metrics. Every slice is sorted
// deterministically (by traffic, then key) and never nil.
type Snapshot struct {
	Time time.Time `json:"time"`
	// PeerID and Address identify the node and its tunnel address, when
	// known.
	PeerID  string `json:"peer_id,omitempty"`
	Address string `json:"address,omitempty"`

	Totals       Totals            `json:"totals"`
	Destinations []DestinationStat `json:"destinations"`
	Sources      []SourceStat      `json:"sources"`
	Protocols    []ProtocolStat    `json:"protocols"`
	Results      []ResultStat      `json:"results"`
	DeniedHosts  []HostCount       `json:"denied_hosts"`
	Tunnel       []PeerStat        `json:"tunnel"`
}

// EmptySnapshot returns a Snapshot taken now with every slice empty (not
// nil), so its JSON form always carries arrays.
func EmptySnapshot() Snapshot {
	return Snapshot{
		Time:         time.Now(),
		Destinations: []DestinationStat{},
		Sources:      []SourceStat{},
		Protocols:    []ProtocolStat{},
		Results:      []ResultStat{},
		DeniedHosts:  []HostCount{},
		Tunnel:       []PeerStat{},
	}
}

// Connection is one entry of the recent-connections ring buffer: the full
// per-connection record, including the requested host even when denied and
// the resolved IP, which never appear as metric labels.
type Connection struct {
	Start           time.Time `json:"start"`
	SourcePeer      string    `json:"source_peer,omitempty"`
	SourceTag       string    `json:"source_tag,omitempty"`
	Source          string    `json:"source,omitempty"`
	Job             string    `json:"job,omitempty"`
	Protocol        string    `json:"protocol"`
	Transport       string    `json:"transport"`
	SNI             string    `json:"sni,omitempty"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	IP              string    `json:"ip,omitempty"`
	PolicyAllowed   bool      `json:"policy_allowed"`
	Result          string    `json:"result"`
	Error           string    `json:"error,omitempty"`
	BytesIn         int64     `json:"bytes_in"`
	BytesOut        int64     `json:"bytes_out"`
	DurationSeconds float64   `json:"duration_seconds"`
	TTFBSeconds     float64   `json:"ttfb_seconds,omitempty"`
}

// ConnectionsResponse is the JSON body of GET /metrics/connections.
type ConnectionsResponse struct {
	Connections []Connection `json:"connections"`
}

// transportTCP is the only transport the exit carries today.
const transportTCP = "tcp"

// ConnectionFrom converts an exit record to its wire form.
func ConnectionFrom(ci exit.ConnInfo) Connection {
	return Connection{
		Start:           ci.Start,
		SourcePeer:      ci.SourcePeer,
		SourceTag:       ci.SourceTag,
		Source:          ci.Source,
		Job:             ci.Job,
		Protocol:        string(ci.Protocol),
		Transport:       transportTCP,
		SNI:             ci.SNI,
		Host:            ci.DestHost,
		Port:            ci.DestPort,
		IP:              ci.DestIP,
		PolicyAllowed:   ci.PolicyAllowed,
		Result:          string(ci.Result),
		Error:           ci.Err,
		BytesIn:         ci.BytesIn,
		BytesOut:        ci.BytesOut,
		DurationSeconds: ci.Duration.Seconds(),
		TTFBSeconds:     ci.TTFB.Seconds(),
	}
}
