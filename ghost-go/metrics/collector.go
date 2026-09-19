package metrics

import (
	"cmp"
	"slices"
	"sync"

	"github.com/Imposter/ghost/ghost-go/exit"
	"github.com/Imposter/ghost/ghost-go/internal/bounded"
)

// Collector defaults.
const (
	DefaultRingSize    = 256
	DefaultDeniedTopN  = 32
	DefaultMaxSources  = 64
	DefaultConnections = 100 // default limit for /metrics/connections
)

// ExitState is the live exit state a Collector reports in its totals.
// *exit.Server implements it.
type ExitState interface {
	ActiveConns() int
	UsageToday() (used, limit int64)
	Paused() bool
}

// CollectorConfig configures a Collector. Zero values take the defaults.
type CollectorConfig struct {
	// RingSize is how many recent connections are kept (DefaultRingSize).
	RingSize int
	// DeniedTopN caps the denied-host tracker (DefaultDeniedTopN).
	DeniedTopN int
	// MaxSources caps the distinct source names aggregated (the source part
	// of each connection's tag); later names fold into exit.OverflowSource
	// (DefaultMaxSources).
	MaxSources int
}

type destKey struct {
	host string
	port int
}

type sourceKey struct {
	peer, source string
}

// Collector is an exit.Accountant that keeps a node's in-process metrics: the
// recent-connections ring buffer, bounded aggregates by destination, source
// (peer and source name; never the job), protocol and result, and a capped
// top-N of denied hosts. Every key space is
// bounded, so memory stays constant however many distinct hosts are
// requested. It is safe for concurrent use.
type Collector struct {
	ring        *exit.ConnRing
	denied      *bounded.TopN
	sourceNames *bounded.Set

	mu      sync.Mutex
	exit    ExitState
	totals  Counts
	nDenied int64
	dests   map[destKey]*Counts
	sources map[sourceKey]*Counts
	protos  map[string]*Counts
	results map[string]int64
}

var _ exit.Accountant = (*Collector)(nil)

// NewCollector creates a Collector.
func NewCollector(cfg CollectorConfig) *Collector {
	if cfg.RingSize <= 0 {
		cfg.RingSize = DefaultRingSize
	}
	if cfg.DeniedTopN <= 0 {
		cfg.DeniedTopN = DefaultDeniedTopN
	}
	if cfg.MaxSources <= 0 {
		cfg.MaxSources = DefaultMaxSources
	}
	return &Collector{
		ring:        exit.NewConnRing(cfg.RingSize),
		denied:      bounded.NewTopN(cfg.DeniedTopN),
		sourceNames: bounded.NewSet(cfg.MaxSources),
		dests:       make(map[destKey]*Counts),
		sources:     make(map[sourceKey]*Counts),
		protos:      make(map[string]*Counts),
		results:     make(map[string]int64),
	}
}

// AttachExit sets the exit whose live state (active connections, cap usage,
// pause) the totals report. The exit is usually created with this Collector
// as its Accountant, so it is attached afterwards.
func (c *Collector) AttachExit(s ExitState) {
	c.mu.Lock()
	c.exit = s
	c.mu.Unlock()
}

// Record implements exit.Accountant.
func (c *Collector) Record(ci exit.ConnInfo) {
	c.ring.Record(ci)

	dk := destKey{host: exit.DeniedHost}
	if ci.PolicyAllowed {
		dk = destKey{host: ci.DestHost, port: ci.DestPort}
	} else {
		c.denied.Add(ci.DestHost)
	}
	src := exit.SourceLabel(ci.Source)
	if src != exit.OverflowSource {
		src = c.sourceNames.Admit(src)
	}
	sk := sourceKey{peer: ci.SourcePeer, source: src}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.totals.add(ci)
	if ci.Result == exit.ResultDenied {
		c.nDenied++
	}
	counts(c.dests, dk).add(ci)
	counts(c.sources, sk).add(ci)
	counts(c.protos, string(ci.Protocol)).add(ci)
	c.results[string(ci.Result)]++
}

func counts[K comparable](m map[K]*Counts, k K) *Counts {
	v, ok := m[k]
	if !ok {
		v = &Counts{}
		m[k] = v
	}
	return v
}

// Recent returns up to limit recent connections, newest first (limit <= 0
// returns all that are held).
func (c *Collector) Recent(limit int) []Connection {
	recs := c.ring.Recent(limit)
	out := make([]Connection, len(recs))
	for i, r := range recs {
		out[i] = ConnectionFrom(r)
	}
	return out
}

// RingSize returns the ring buffer capacity.
func (c *Collector) RingSize() int { return c.ring.Cap() }

// Snapshot returns the exit-side snapshot. Tunnel, PeerID and Address are
// left empty; ghost.Node.Snapshot fills them in.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	s := EmptySnapshot()
	s.Totals = Totals{Counts: c.totals, Denied: c.nDenied}
	for k, v := range c.dests {
		s.Destinations = append(s.Destinations, DestinationStat{Host: k.host, Port: k.port, Counts: *v})
	}
	for k, v := range c.sources {
		s.Sources = append(s.Sources, SourceStat{Peer: k.peer, Source: k.source, Counts: *v})
	}
	for k, v := range c.protos {
		s.Protocols = append(s.Protocols, ProtocolStat{Protocol: k, Transport: transportTCP, Counts: *v})
	}
	for k, v := range c.results {
		s.Results = append(s.Results, ResultStat{Result: k, Count: v})
	}
	ex := c.exit
	c.mu.Unlock()

	if ex != nil {
		s.Totals.Active = ex.ActiveConns()
		s.Totals.CapUsedBytes, s.Totals.CapLimitBytes = ex.UsageToday()
		s.Totals.Paused = ex.Paused()
	}
	top := c.denied.Top()
	s.DeniedHosts = make([]HostCount, len(top))
	for i, t := range top {
		s.DeniedHosts[i] = HostCount{Host: t.Key, Count: t.Count}
	}

	slices.SortFunc(s.Destinations, func(a, b DestinationStat) int {
		return cmp.Or(byTraffic(a.Counts, b.Counts), cmp.Compare(a.Host, b.Host), cmp.Compare(a.Port, b.Port))
	})
	slices.SortFunc(s.Sources, func(a, b SourceStat) int {
		return cmp.Or(byTraffic(a.Counts, b.Counts), cmp.Compare(a.Peer, b.Peer), cmp.Compare(a.Source, b.Source))
	})
	slices.SortFunc(s.Protocols, func(a, b ProtocolStat) int {
		return cmp.Or(byTraffic(a.Counts, b.Counts), cmp.Compare(a.Protocol, b.Protocol))
	})
	slices.SortFunc(s.Results, func(a, b ResultStat) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Result, b.Result))
	})
	return s
}

// byTraffic orders by connections, then total bytes, descending.
func byTraffic(a, b Counts) int {
	return cmp.Or(
		cmp.Compare(b.Connections, a.Connections),
		cmp.Compare(b.BytesIn+b.BytesOut, a.BytesIn+a.BytesOut),
	)
}
