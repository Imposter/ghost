package main

import (
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/ghost"
	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// defaultEventBuffer is how many events a node holds for a host application
// that has not read them yet.
const defaultEventBuffer = 256

// eventJSON is one entry of the event stream, the JSON ghost_next_event_json
// returns. Fields that do not apply to a kind are omitted.
type eventJSON struct {
	// Seq numbers events per node, from 1, and counts the dropped ones, so a
	// gap in Seq is exactly what Dropped reports.
	Seq uint64 `json:"seq"`
	// Kind is one of signal_state, joined, netmap, policy, peer_connected,
	// peer_disconnected, error, stopped.
	Kind string `json:"kind"`
	// Time is when the event was buffered (RFC 3339, UTC).
	Time time.Time `json:"time"`
	// Dropped is how many events were dropped, oldest first, between the
	// previous delivered event and this one. It is 0 in the normal case.
	Dropped uint64 `json:"dropped"`

	// SignalState is set on signal_state: disconnected, connecting,
	// connected or closed.
	SignalState string `json:"signal_state,omitempty"`
	// PeerID is the peer an event concerns (peer_connected,
	// peer_disconnected, and some errors).
	PeerID string `json:"peer_id,omitempty"`
	// Address is a tunnel address in CIDR form: this node's on joined, the
	// peer's on peer_connected.
	Address string `json:"address,omitempty"`
	// CandidateType is the selected local ICE candidate type on
	// peer_connected: host, srflx, prflx or relay.
	CandidateType string `json:"candidate_type,omitempty"`
	// Policy is the control plane's new exit policy on policy.
	Policy *proto.ExitPolicy `json:"policy,omitempty"`
	// Error is the message on error.
	Error string `json:"error,omitempty"`
}

// kindStopped is emitted once, last, when the node is stopped. It is the only
// kind libghost adds to the ones ghost.Event carries.
const kindStopped = "stopped"

// eventBuffer is a bounded FIFO between a node's event channel and the host
// application. A host that reads slowly never blocks the node: the buffer
// drops the oldest events and reports how many on the next one delivered.
// It is safe for concurrent use.
type eventBuffer struct {
	mu      sync.Mutex
	buf     []eventJSON
	head    int // index of the oldest entry
	n       int // entries held
	seq     uint64
	dropped uint64
	closed  bool
	// notify wakes one waiting next; it is a signal, not a queue.
	notify chan struct{}
}

func newEventBuffer(size int) *eventBuffer {
	if size <= 0 {
		size = defaultEventBuffer
	}
	return &eventBuffer{buf: make([]eventJSON, size), notify: make(chan struct{}, 1)}
}

// push appends ev, dropping the oldest entry when the buffer is full. It
// never blocks.
func (b *eventBuffer) push(ev eventJSON) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.seq++
	ev.Seq = b.seq
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	if b.n == len(b.buf) {
		b.head = (b.head + 1) % len(b.buf)
		b.n--
		b.dropped++
	}
	b.buf[(b.head+b.n)%len(b.buf)] = ev
	b.n++
	b.mu.Unlock()
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

// close stops the buffer. Entries already held are still delivered; next
// reports no more once they run out.
func (b *eventBuffer) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

// pop takes the oldest entry, if there is one.
func (b *eventBuffer) pop() (eventJSON, bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.n == 0 {
		return eventJSON{}, false, b.closed
	}
	ev := b.buf[b.head]
	b.buf[b.head] = eventJSON{}
	b.head = (b.head + 1) % len(b.buf)
	b.n--
	ev.Dropped = b.dropped
	b.dropped = 0
	return ev, true, false
}

// next returns the oldest event, waiting up to timeout for one. It reports
// false on timeout and once a closed buffer has run dry. A timeout of zero
// polls; a negative timeout waits until an event arrives or the buffer
// closes.
func (b *eventBuffer) next(timeout time.Duration) (eventJSON, bool) {
	var deadline time.Time
	if timeout >= 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		ev, ok, done := b.pop()
		if ok {
			return ev, true
		}
		if done {
			return eventJSON{}, false
		}
		var wait <-chan time.Time
		var timer *time.Timer
		if timeout >= 0 {
			left := time.Until(deadline)
			if left <= 0 {
				return eventJSON{}, false
			}
			timer = time.NewTimer(left)
			wait = timer.C
		}
		select {
		case <-b.notify:
		case <-wait:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// eventFrom converts a node event to its wire form.
func eventFrom(ev ghost.Event) eventJSON {
	out := eventJSON{
		Kind:          string(ev.Kind),
		Time:          time.Now().UTC(),
		SignalState:   string(ev.SignalState),
		PeerID:        ev.PeerID,
		Address:       ev.Address,
		CandidateType: ev.CandidateType,
		Policy:        ev.Policy,
	}
	if ev.Err != nil {
		out.Error = ev.Err.Error()
	}
	return out
}
