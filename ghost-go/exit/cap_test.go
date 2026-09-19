package exit

import (
	"context"
	"testing"
	"time"
)

func TestCapDailyBudget(t *testing.T) {
	c := newCapController(100, 0)
	if !c.Allowed() {
		t.Fatal("should allow before any usage")
	}
	if exhausted := c.add(60); exhausted {
		t.Fatal("should not be exhausted at 60/100")
	}
	if exhausted := c.add(40); !exhausted {
		t.Fatal("should be exhausted at 100/100")
	}
	if c.Allowed() {
		t.Fatal("should not allow new connections once budget is exhausted")
	}
	used, limit := c.UsageToday()
	if used != 100 || limit != 100 {
		t.Fatalf("usage=%d/%d want 100/100", used, limit)
	}
}

func TestCapPauseResume(t *testing.T) {
	c := newCapController(0, 0) // unlimited
	if !c.Allowed() {
		t.Fatal("unlimited cap should allow")
	}
	c.SetPaused(true)
	if c.Allowed() {
		t.Fatal("paused cap should refuse")
	}
	if !c.Paused() {
		t.Fatal("Paused() should report true")
	}
	c.SetPaused(false)
	if !c.Allowed() {
		t.Fatal("resumed cap should allow")
	}
}

func TestCapRateLimit(t *testing.T) {
	// 1000 bytes/sec, wait for 500 twice should take ~0.5s total after burst.
	c := newCapController(0, 1000)
	ctx := context.Background()
	// Drain the initial burst.
	if err := c.wait(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := c.wait(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("rate limiter returned too quickly: %v", elapsed)
	}
}
