package access

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/telemetry"
)

// Controller is the server's single access-control entry point. It wraps an
// Authorizer with a short decision cache and fail-closed error handling:
// Check never returns an error, and anything short of an explicit allow is a
// denial.
type Controller struct {
	auth    Authorizer
	open    bool
	ttl     time.Duration
	log     *slog.Logger
	metrics *telemetry.Metrics
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	d       Decision
	peer    string
	target  string
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
	_, open := auth.(Open)
	return &Controller{
		auth:    auth,
		open:    open,
		ttl:     opts.CacheTTL,
		log:     opts.Logger,
		metrics: opts.Metrics,
		now:     opts.Now,
		cache:   map[string]cacheEntry{},
	}
}

// Consulted reports whether an external authorizer takes part in decisions.
// It is false in open mode, where every request is allowed.
func (c *Controller) Consulted() bool { return !c.open }

// Check decides req. Errors from the authorizer deny (fail closed) and are
// not cached.
func (c *Controller) Check(ctx context.Context, req Request) Decision {
	return c.check(ctx, req, true)
}

// CheckFresh decides req without consulting the cache (the result still
// refreshes it). Policy changes use it so the authorizer sees every change.
func (c *Controller) CheckFresh(ctx context.Context, req Request) Decision {
	return c.check(ctx, req, false)
}

func (c *Controller) check(ctx context.Context, req Request, useCache bool) Decision {
	key := cacheKey(req)
	if c.ttl > 0 && useCache {
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
		c.log.Warn("access: authorizer unavailable, denying", "action", req.Action, "peer", req.Peer, "error", err)
		c.metrics.AuthzDecision(ctx, string(req.Action), "error", false)
		return Decision{Allow: false, Reason: "authorizer unavailable", Unavailable: true}
	}
	c.metrics.AuthzDecision(ctx, string(req.Action), result(d), false)
	if c.ttl > 0 {
		c.mu.Lock()
		c.cache[key] = cacheEntry{d: d, peer: req.Peer, target: req.Target, expires: c.now().Add(c.ttl)}
		c.sweepLocked()
		c.mu.Unlock()
	}
	return d
}

// Forget drops cached decisions made for or about a peer (on revoke, expiry,
// move or deletion).
func (c *Controller) Forget(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.cache {
		if e.peer == peer || e.target == peer {
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

// cacheKey identifies a request independent of its ts and nonce. The fields
// are JSON-encoded so no value can collide with a separator.
func cacheKey(r Request) string {
	b, _ := json.Marshal(cacheKeyFields{
		Action: r.Action, Network: r.Network, Peer: r.Peer, Target: r.Target,
		Roles: sortedRoles(r.Roles), Tags: sortedStrings(r.Tags), Labels: r.Labels,
		PublicKey: r.PublicKey, EnrollmentMethod: r.EnrollmentMethod, AuthKeyID: r.AuthKeyID,
	})
	return string(b)
}

type cacheKeyFields struct {
	Action  Action            `json:"a"`
	Network string            `json:"n"`
	Peer    string            `json:"p"`
	Target  string            `json:"t"`
	Roles   []string          `json:"r"`
	Tags    []string          `json:"g"`
	Labels  map[string]string `json:"l"`
	// The remaining fields rarely change for a peer, but a new key or method
	// must not reuse a decision made for the old one.
	PublicKey        string           `json:"k"`
	EnrollmentMethod EnrollmentMethod `json:"m"`
	AuthKeyID        string           `json:"i"`
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sortedRoles(in []proto.Role) []string {
	out := make([]string, len(in))
	for i, r := range in {
		out[i] = string(r)
	}
	sort.Strings(out)
	return out
}
