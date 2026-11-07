package metrics

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"

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

// Collector is an exit.LiveAccountant that keeps a node's in-process metrics:
// the recent-connections ring buffer, bounded aggregates by destination,
// source (peer and source name; never the job), protocol and result, and a
// capped top-N of denied hosts. Every key space is
// bounded, so memory stays constant however many distinct hosts are
// requested. It is safe for concurrent use.
//
// A connection counts in the aggregates when its tunnel opens, and its bytes
// as they move, so long-lived tunnels show up before they close; its result
// counts once, when it closes.
type Collector struct {
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
	open    map[*conn]struct{}

	// ring holds the most recent connections, open or closed; next is the
	// slot the next one takes and n how many slots are filled.
	ring    []*conn
	next, n int
}

// conn is one connection in the Collector. While it is open, in and out
// count its bytes as they move, and foldedIn and foldedOut are the part of
// them already added to the aggregates.
type conn struct {
	c       *Collector
	dk      destKey
	sk      sourceKey
	in, out atomic.Int64

	// Guarded by c.mu.
	info                exit.ConnInfo
	isOpen              bool
	foldedIn, foldedOut int64
}

var (
	_ exit.LiveAccountant = (*Collector)(nil)
	_ exit.LiveConn       = (*conn)(nil)
)

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
		denied:      bounded.NewTopN(cfg.DeniedTopN),
		sourceNames: bounded.NewSet(cfg.MaxSources),
		dests:       make(map[destKey]*Counts),
		sources:     make(map[sourceKey]*Counts),
		protos:      make(map[string]*Counts),
		results:     make(map[string]int64),
		open:        make(map[*conn]struct{}),
		ring:        make([]*conn, cfg.RingSize),
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

// Record implements exit.Accountant for a connection reported only once it
// finished: it counts, carries its bytes and its result at once.
func (c *Collector) Record(ci exit.ConnInfo) {
	e := c.newConn(ci)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.countLocked(e)
	c.addBytesLocked(e, ci.BytesIn, ci.BytesOut)
	c.resultLocked(ci)
	c.pushLocked(e)
}

// Open implements exit.LiveAccountant: the connection counts now, shows in the
// recent connections with result exit.ResultOpen, and its bytes reach the
// aggregates as they move.
func (c *Collector) Open(ci exit.ConnInfo) exit.LiveConn {
	ci.Result = exit.ResultOpen
	e := c.newConn(ci)
	e.isOpen = true
	c.mu.Lock()
	defer c.mu.Unlock()
	c.countLocked(e)
	c.pushLocked(e)
	c.open[e] = struct{}{}
	return e
}

// AddBytes implements exit.LiveConn. It only touches the connection's own
// counters; the aggregates take them in when read (foldLocked) or on Close.
func (e *conn) AddBytes(in, out int64) {
	e.in.Add(in)
	e.out.Add(out)
}

// Close implements exit.LiveConn: the remaining bytes reach the aggregates
// and the result counts. The connection's recent entry becomes its final
// record. Calls after the first are ignored.
func (e *conn) Close(ci exit.ConnInfo) {
	c := e.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if !e.isOpen {
		return
	}
	e.isOpen = false
	delete(c.open, e)
	ci.BytesIn = max(ci.BytesIn, e.in.Load())
	ci.BytesOut = max(ci.BytesOut, e.out.Load())
	c.addBytesLocked(e, ci.BytesIn-e.foldedIn, ci.BytesOut-e.foldedOut)
	c.resultLocked(ci)
	e.info = ci
}

// newConn resolves a connection's aggregation keys. Only a destination the
// policy permitted becomes a key; the rest fold into exit.DeniedHost.
func (c *Collector) newConn(ci exit.ConnInfo) *conn {
	e := &conn{c: c, info: ci, dk: destKey{host: exit.DeniedHost}}
	if ci.PolicyAllowed {
		e.dk = destKey{host: ci.DestHost, port: ci.DestPort}
	} else {
		c.denied.Add(ci.DestHost)
	}
	src := exit.SourceLabel(ci.Source)
	if src != exit.OverflowSource {
		src = c.sourceNames.Admit(src)
	}
	e.sk = sourceKey{peer: ci.SourcePeer, source: src}
	return e
}

// countLocked counts one connection under each of its keys. Caller holds mu.
func (c *Collector) countLocked(e *conn) {
	for _, k := range c.keyCountsLocked(e) {
		k.Connections++
	}
}

// addBytesLocked adds bytes to every aggregate of e and marks them folded.
// Caller holds mu.
func (c *Collector) addBytesLocked(e *conn, in, out int64) {
	if in == 0 && out == 0 {
		return
	}
	e.foldedIn += in
	e.foldedOut += out
	for _, k := range c.keyCountsLocked(e) {
		k.BytesIn += in
		k.BytesOut += out
	}
}

func (c *Collector) keyCountsLocked(e *conn) [4]*Counts {
	return [4]*Counts{
		&c.totals,
		counts(c.dests, e.dk),
		counts(c.sources, e.sk),
		counts(c.protos, string(e.info.Protocol)),
	}
}

// resultLocked counts a finished connection's result. Caller holds mu.
func (c *Collector) resultLocked(ci exit.ConnInfo) {
	if ci.Result == exit.ResultDenied {
		c.nDenied++
	}
	c.results[string(ci.Result)]++
}

// pushLocked adds e to the ring, evicting the oldest entry when full. An
// evicted open connection still reaches the aggregates. Caller holds mu.
func (c *Collector) pushLocked(e *conn) {
	c.ring[c.next] = e
	c.next = (c.next + 1) % len(c.ring)
	if c.n < len(c.ring) {
		c.n++
	}
}

// foldLocked adds the bytes open connections moved since the last fold to the
// aggregates. Caller holds mu.
func (c *Collector) foldLocked() {
	for e := range c.open {
		c.addBytesLocked(e, e.in.Load()-e.foldedIn, e.out.Load()-e.foldedOut)
	}
}

// recordLocked is e's record as of now: an open connection carries its bytes
// and lifetime so far. Caller holds mu.
func (e *conn) recordLocked() exit.ConnInfo {
	ci := e.info
	if e.isOpen {
		ci.BytesIn = e.in.Load()
		ci.BytesOut = e.out.Load()
		ci.Duration = time.Since(ci.Start)
	}
	return ci
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
// returns all that are held). A connection takes its place when its tunnel
// opens, or when it finishes if it never opened one; while open it shows
// result exit.ResultOpen with its bytes so far, and one entry per connection
// throughout.
func (c *Collector) Recent(limit int) []Connection {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit <= 0 || limit > c.n {
		limit = c.n
	}
	size := len(c.ring)
	out := make([]Connection, limit)
	for i := range out {
		out[i] = ConnectionFrom(c.ring[(c.next-1-i+size)%size].recordLocked())
	}
	return out
}

// RingSize returns the ring buffer capacity.
func (c *Collector) RingSize() int { return len(c.ring) }

// Snapshot returns the exit-side snapshot. Tunnel, PeerID and Address are
// left empty; ghost.Node.Snapshot fills them in.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	c.foldLocked()
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
