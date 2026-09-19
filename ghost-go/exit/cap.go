package exit

import (
	"context"
	"io"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// capController enforces a per-day byte budget and an optional bytes-per-second
// rate limit, and supports pause/resume. It is safe for concurrent use.
type capController struct {
	mu sync.Mutex

	dailyLimit int64 // 0 = unlimited
	usedToday  int64
	day        int64 // unix day number the counter belongs to

	paused bool

	limiter *rate.Limiter // nil = no rate limit
}

func newCapController(dailyLimit int64, bytesPerSec int64) *capController {
	c := &capController{dailyLimit: dailyLimit, day: unixDay(time.Now())}
	if bytesPerSec > 0 {
		c.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), int(bytesPerSec))
	}
	return c
}

func unixDay(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// SetDailyLimit updates the per-day byte budget (0 = unlimited).
func (c *capController) SetDailyLimit(n int64) {
	c.mu.Lock()
	c.dailyLimit = n
	c.mu.Unlock()
}

// SetRate updates the bytes-per-second limit (0 = unlimited).
func (c *capController) SetRate(bytesPerSec int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if bytesPerSec <= 0 {
		c.limiter = nil
		return
	}
	c.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), int(bytesPerSec))
}

// SetPaused pauses or resumes the exit. While paused, new connections are
// refused.
func (c *capController) SetPaused(p bool) {
	c.mu.Lock()
	c.paused = p
	c.mu.Unlock()
}

// Paused reports whether the exit is paused.
func (c *capController) Paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

// rollover resets the daily counter if the UTC day changed. Caller holds mu.
func (c *capController) rolloverLocked() {
	d := unixDay(time.Now())
	if d != c.day {
		c.day = d
		c.usedToday = 0
	}
}

// Allowed reports whether a new connection may start: not paused and the daily
// budget is not already exhausted.
func (c *capController) Allowed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused {
		return false
	}
	c.rolloverLocked()
	if c.dailyLimit > 0 && c.usedToday >= c.dailyLimit {
		return false
	}
	return true
}

// add records n transferred bytes and reports whether the daily budget is now
// exhausted (true means callers should stop).
func (c *capController) add(n int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rolloverLocked()
	c.usedToday += n
	return c.dailyLimit > 0 && c.usedToday >= c.dailyLimit
}

// wait blocks until the rate limiter allows n bytes (no-op when unlimited).
func (c *capController) wait(ctx context.Context, n int) error {
	c.mu.Lock()
	l := c.limiter
	c.mu.Unlock()
	if l == nil {
		return nil
	}
	// Cap n to the limiter burst to avoid ErrTokenBurst.
	burst := l.Burst()
	for n > 0 {
		k := n
		if k > burst {
			k = burst
		}
		if err := l.WaitN(ctx, k); err != nil {
			return err
		}
		n -= k
	}
	return nil
}

// UsageToday returns the bytes transferred today and the daily limit.
func (c *capController) UsageToday() (used, limit int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rolloverLocked()
	return c.usedToday, c.dailyLimit
}

// capReader wraps a reader to count bytes, apply the rate limit, and stop when
// the daily budget is exhausted.
type capReader struct {
	r       io.Reader
	cap     *capController
	ctx     context.Context
	counter *int64
	onFirst func()
	first   bool
}

func (cr *capReader) Read(p []byte) (int, error) {
	if err := cr.cap.wait(cr.ctx, len(p)); err != nil {
		return 0, err
	}
	n, err := cr.r.Read(p)
	if n > 0 {
		if !cr.first && cr.onFirst != nil {
			cr.first = true
			cr.onFirst()
		}
		*cr.counter += int64(n)
		if cr.cap.add(int64(n)) {
			// Budget exhausted; deliver what we have then signal EOF-like stop.
			return n, errCapExhausted
		}
	}
	return n, err
}
