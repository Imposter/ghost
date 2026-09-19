package access

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-server/server/telemetry"
)

// Controller is the server's single access-control entry point. It wraps an
// Authorizer with a short decision cache and fail-closed error handling:
// Check never returns an error, and anything short of an explicit allow is a
// denial.
type Controller struct {
	auth    Authorizer
	ttl     time.Duration
	log     *slog.Logger
	metrics *telemetry.Metrics
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	d       Decision
	device  string
	expires time.Time
}

// ControllerOptions configures a Controller.
type ControllerOptions struct {
	// CacheTTL is how long decisions are reused (0 disables caching).
	CacheTTL time.Duration
	Logger   *slog.Logger
	Metrics  *telemetry.Metrics
	Now      func() time.Time
}

// NewController wraps auth.
func NewController(auth Authorizer, opts ControllerOptions) *Controller {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Controller{
		auth:    auth,
		ttl:     opts.CacheTTL,
		log:     opts.Logger,
		metrics: opts.Metrics,
		now:     opts.Now,
		cache:   map[string]cacheEntry{},
	}
}

// Check decides req. Errors from the authorizer deny (fail closed) and are
// not cached.
func (c *Controller) Check(ctx context.Context, req Request) Decision {
	key := cacheKey(req)
	if c.ttl > 0 {
		c.mu.Lock()
		e, ok := c.cache[key]
		c.mu.Unlock()
		if ok && c.now().Before(e.expires) {
			c.metrics.AuthzDecision(ctx, string(req.Action), result(e.d), true)
			return e.d
		}
	}

	start := time.Now()
	d, err := c.auth.Authorize(ctx, req)
	c.metrics.AuthzLatency(ctx, time.Since(start).Seconds())
	if err != nil {
		c.log.Warn("access: authorizer unavailable, denying", "action", req.Action, "device", req.Device, "error", err)
		c.metrics.AuthzDecision(ctx, string(req.Action), "error", false)
		return Decision{Allow: false, Reason: "authorizer unavailable"}
	}
	c.metrics.AuthzDecision(ctx, string(req.Action), result(d), false)
	if c.ttl > 0 {
		c.mu.Lock()
		c.cache[key] = cacheEntry{d: d, device: req.Device, expires: c.now().Add(c.ttl)}
		c.sweepLocked()
		c.mu.Unlock()
	}
	return d
}

// Forget drops cached decisions made for or about device (on revoke or move).
func (c *Controller) Forget(device string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.cache {
		if e.device == device || strings.Contains(k, "|peer="+device+"|") {
			delete(c.cache, k)
		}
	}
}

func (c *Controller) sweepLocked() {
	if len(c.cache) < 1024 {
		return
	}
	now := c.now()
	for k, e := range c.cache {
		if now.After(e.expires) {
			delete(c.cache, k)
		}
	}
}

func result(d Decision) string {
	if d.Allow {
		return "allow"
	}
	return "deny"
}

// cacheKey identifies a request independent of its ts and nonce.
func cacheKey(r Request) string {
	var b strings.Builder
	b.WriteString(string(r.Action))
	b.WriteString("|net=" + r.Network)
	b.WriteString("|dev=" + r.Device)
	b.WriteString("|peer=" + r.Peer + "|")
	b.WriteString("role=" + string(r.Role))
	keys := make([]string, 0, len(r.Labels))
	for k := range r.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("|" + k + "=" + r.Labels[k])
	}
	return b.String()
}
