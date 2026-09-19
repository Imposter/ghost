package exit

import "sync"

// ConnRing is a fixed-size ring buffer of the most recent connection records.
// It powers the owner's activity log and the admin "what's happening" view. It
// is a plain in-process structure, deliberately separate from OpenTelemetry,
// and is fed from the same accounting hook. It is safe for concurrent use and
// itself implements Accountant.
type ConnRing struct {
	mu   sync.Mutex
	buf  []ConnInfo
	next int
	size int
	n    int
}

// NewConnRing creates a ring holding the last size records (minimum 1).
func NewConnRing(size int) *ConnRing {
	if size < 1 {
		size = 1
	}
	return &ConnRing{buf: make([]ConnInfo, size), size: size}
}

// Record adds a record, evicting the oldest when full.
func (r *ConnRing) Record(ci ConnInfo) {
	r.mu.Lock()
	r.buf[r.next] = ci
	r.next = (r.next + 1) % r.size
	if r.n < r.size {
		r.n++
	}
	r.mu.Unlock()
}

// Recent returns up to the last n records, newest first. Pass n <= 0 for all.
func (r *ConnRing) Recent(n int) []ConnInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > r.n {
		n = r.n
	}
	out := make([]ConnInfo, 0, n)
	for i := 0; i < n; i++ {
		idx := (r.next - 1 - i + r.size*2) % r.size
		out = append(out, r.buf[idx])
	}
	return out
}

// Len returns the number of records currently held.
func (r *ConnRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// Cap returns the ring's capacity.
func (r *ConnRing) Cap() int { return r.size }
