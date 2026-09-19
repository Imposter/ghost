// Package bounded holds small, concurrency-safe structures that keep
// cardinality bounded: a first-N label set that folds overflow into a single
// value, and a capped top-N counter (Space-Saving) for heavy hitters.
package bounded

import (
	"sort"
	"sync"
)

// Overflow is the value a Set returns once it is full and sees a new value.
const Overflow = "other"

// Set admits the first Max distinct values it sees. Any later, unseen value is
// reported as Overflow, so a metric label fed through it never exceeds Max+1
// distinct values. The empty string always passes through unchanged.
type Set struct {
	max  int
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewSet creates a Set admitting at most max distinct values (minimum 1).
func NewSet(max int) *Set {
	if max < 1 {
		max = 1
	}
	return &Set{max: max, seen: make(map[string]struct{}, max)}
}

// Admit returns v if it is already admitted or there is room for it, and
// Overflow otherwise.
func (s *Set) Admit(v string) string {
	if v == "" {
		return v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[v]; ok {
		return v
	}
	if len(s.seen) >= s.max {
		return Overflow
	}
	s.seen[v] = struct{}{}
	return v
}

// Count is one TopN entry.
type Count struct {
	Key   string
	Count int64
}

// TopN tracks the approximate top N keys by count in O(N) memory using the
// Space-Saving algorithm: when full, a new key replaces the current minimum and
// inherits its count plus one. Counts of keys that were never evicted are
// exact.
type TopN struct {
	max    int
	mu     sync.Mutex
	counts map[string]int64
}

// NewTopN creates a TopN holding at most max keys (minimum 1).
func NewTopN(max int) *TopN {
	if max < 1 {
		max = 1
	}
	return &TopN{max: max, counts: make(map[string]int64, max)}
}

// Add counts one occurrence of key.
func (t *TopN) Add(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.counts[key]; ok || len(t.counts) < t.max {
		t.counts[key]++
		return
	}
	minKey, minCount := "", int64(-1)
	for k, c := range t.counts {
		if minCount < 0 || c < minCount || (c == minCount && k < minKey) {
			minKey, minCount = k, c
		}
	}
	delete(t.counts, minKey)
	t.counts[key] = minCount + 1
}

// Len returns the number of tracked keys.
func (t *TopN) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.counts)
}

// Top returns the tracked keys ordered by count (descending), then key.
func (t *TopN) Top() []Count {
	t.mu.Lock()
	out := make([]Count, 0, len(t.counts))
	for k, c := range t.counts {
		out = append(out, Count{Key: k, Count: c})
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}
