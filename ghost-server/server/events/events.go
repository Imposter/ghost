// Package events is ghost-server's in-process event bus. Control-plane changes
// (peers, netmaps, policy), peer health and audit entries are published here
// and streamed to integrators through the control API's watch endpoint. A
// bounded ring buffer lets a reconnecting watcher resume from the last
// sequence number it saw.
package events

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// Event types.
const (
	PeerEnrolled   = "peer.enrolled"
	PeerUpdated    = "peer.updated"
	PeerOnline     = "peer.online"
	PeerOffline    = "peer.offline"
	PeerRevoked    = "peer.revoked"
	PeerExpired    = "peer.expired"
	PeerDeleted    = "peer.deleted"
	PeerKeyRotated = "peer.key_rotated"
	PeerHealth     = "peer.health"
	NetmapUpdated  = "netmap.updated"
	PolicyUpdated  = "policy.updated"
	NetworkUpdated = "network.updated"
	Audit          = "audit"
)

// Event is one published event.
type Event struct {
	// Seq increases by one per published event (process-local).
	Seq     uint64    `json:"seq"`
	Type    string    `json:"type"`
	Time    time.Time `json:"time"`
	Network string    `json:"network,omitempty"`
	PeerID  string    `json:"peer_id,omitempty"`
	Data    any       `json:"data,omitempty"`
}

// Filter selects events for a subscriber.
type Filter struct {
	// Networks limits events to these networks (nil: every network; events
	// without a network always pass).
	Networks []string
	// Types limits events to these types or type prefixes ending in "." (for
	// example "peer."); nil passes every type.
	Types []string
	// Strict drops events without a network when Networks is set (for
	// subscribers confined to some networks).
	Strict bool
}

// Match reports whether e passes the filter.
func (f Filter) Match(e Event) bool {
	if f.Networks != nil {
		if e.Network == "" && f.Strict {
			return false
		}
		if e.Network != "" && !slices.Contains(f.Networks, e.Network) {
			return false
		}
	}
	if f.Types == nil {
		return true
	}
	for _, t := range f.Types {
		if t == e.Type || (strings.HasSuffix(t, ".") && strings.HasPrefix(e.Type, t)) {
			return true
		}
	}
	return false
}

// Subscription receives matching events on C until Close. A subscriber that
// falls more than its buffer behind is dropped: C is closed and Dropped
// reports true, so the watcher can reconnect with the last Seq it saw.
type Subscription struct {
	C       <-chan Event
	ch      chan Event
	filter  Filter
	bus     *Bus
	once    sync.Once
	dropped bool
}

// Dropped reports whether the bus dropped this subscriber for being slow.
func (s *Subscription) Dropped() bool {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.dropped
}

// Close unsubscribes. It is idempotent.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.closeLocked()
}

func (s *Subscription) closeLocked() {
	s.once.Do(func() {
		delete(s.bus.subs, s)
		close(s.ch)
	})
}

// Bus fans events out to subscribers. It is safe for concurrent use.
type Bus struct {
	mu   sync.Mutex
	seq  uint64
	ring []Event
	next int
	full bool
	subs map[*Subscription]struct{}
	now  func() time.Time
}

// NewBus returns a bus keeping the last ringSize events for resumption.
func NewBus(ringSize int, now func() time.Time) *Bus {
	if ringSize <= 0 {
		ringSize = 1024
	}
	if now == nil {
		now = time.Now
	}
	return &Bus{ring: make([]Event, ringSize), subs: map[*Subscription]struct{}{}, now: now}
}

// Publish assigns e a sequence number and time and delivers it.
func (b *Bus) Publish(e Event) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e.Seq = b.seq
	if e.Time.IsZero() {
		e.Time = b.now().UTC()
	}
	b.ring[b.next] = e
	b.next = (b.next + 1) % len(b.ring)
	if b.next == 0 {
		b.full = true
	}
	for s := range b.subs {
		if !s.filter.Match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			s.dropped = true
			s.closeLocked()
		}
	}
	return e
}

// Subscribe returns a subscription. When since > 0, buffered events with a
// greater sequence number are replayed first (as many as the ring still
// holds).
func (b *Bus) Subscribe(f Filter, since uint64, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 256
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var replay []Event
	if since > 0 {
		for _, e := range b.bufferedLocked() {
			if e.Seq > since && f.Match(e) {
				replay = append(replay, e)
			}
		}
	}
	ch := make(chan Event, buffer+len(replay))
	for _, e := range replay {
		ch <- e
	}
	s := &Subscription{C: ch, ch: ch, filter: f, bus: b}
	b.subs[s] = struct{}{}
	return s
}

// bufferedLocked returns the ring's events in sequence order.
func (b *Bus) bufferedLocked() []Event {
	if !b.full {
		return slices.Clone(b.ring[:b.next])
	}
	return append(slices.Clone(b.ring[b.next:]), b.ring[:b.next]...)
}

// Seq returns the last assigned sequence number.
func (b *Bus) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}
