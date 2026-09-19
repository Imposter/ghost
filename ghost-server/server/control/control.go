// Package control is ghost-server's peer control plane: networks and their
// policy documents, peers, enrolment (pre-auth keys and interactive codes),
// credential rotation and expiry, scoped API keys, health, and the audit log.
// The HTTP APIs and the signalling relay call into it; it keeps live sessions
// in step through the Sessions interface and publishes every change on the
// event bus.
package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/store"
	"github.com/Imposter/ghost/ghost-server/server/telemetry"
)

// Errors returned by Service. Handlers map them to HTTP statuses and
// signalling error codes.
var (
	ErrInvalid      = errors.New("invalid request")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden refuses an operation the network's settings do not allow.
	ErrForbidden = errors.New("forbidden")
	// ErrInteractiveEnrollmentDisabled refines ErrForbidden for a network
	// whose interactive_enrollment switch is off.
	ErrInteractiveEnrollmentDisabled = fmt.Errorf("%w: interactive enrolment is disabled", ErrForbidden)
	// ErrPeerRevoked and ErrPeerExpired refine ErrUnauthorized for peers.
	ErrPeerRevoked = fmt.Errorf("%w: peer revoked", ErrUnauthorized)
	ErrPeerExpired = fmt.Errorf("%w: peer credentials expired", ErrUnauthorized)
	// ErrAuthorizerOpen refines ErrConflict for a reauthorize call while the
	// access mode is open: there is no authorizer to ask.
	ErrAuthorizerOpen = fmt.Errorf("%w: access mode is open, there is no authorizer to ask", ErrConflict)
)

// DeniedError reports a denial by the external authorizer. Unavailable marks
// the fail-closed denial of an unreachable authorizer.
type DeniedError struct {
	Reason      string
	Unavailable bool
}

func (e *DeniedError) Error() string {
	if e.Reason == "" {
		return "denied by the authorizer"
	}
	return "denied by the authorizer: " + e.Reason
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func mapStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}

// Sessions is implemented by the signalling relay so the service can act on
// live sessions.
type Sessions interface {
	// Disconnect sends a fatal error with code/message to the peer's live
	// session, if any, and closes it. It reports whether one was closed.
	Disconnect(peerID, code, message string) bool
	// Online reports whether the peer has a live, joined session.
	Online(peerID string) bool
	// NetworkChanged recomputes the netmaps of the network's live sessions
	// and sends each the resulting delta.
	NetworkChanged(ctx context.Context, network string)
	// PolicyChanged re-consults the authorizer for every live session in the
	// network, then behaves like NetworkChanged.
	PolicyChanged(ctx context.Context, network string)
	// Reauthorize re-asks the authorizer, uncached, whether the peer's joined
	// session may stay, closing it on a denial as PolicyChanged does, then
	// pushes the network's netmaps. It reports false, without asking, when
	// the peer has no joined session.
	Reauthorize(ctx context.Context, peerID string) (access.Decision, bool)
	// ReauthorizeNetwork does the same for every joined session in the
	// network and returns each decision by peer id.
	ReauthorizeNetwork(ctx context.Context, network string) map[string]access.Decision
}

// Options configures a Service.
type Options struct {
	Store          store.Store
	Access         *access.Controller
	Bus            *events.Bus
	DefaultPool    string
	EnrollmentTTL  time.Duration
	PollInterval   time.Duration
	EphemeralGrace time.Duration
	Logger         *slog.Logger
	Metrics        *telemetry.Metrics
	Now            func() time.Time
}

// Service implements the control plane's operations.
type Service struct {
	st             store.Store
	access         *access.Controller
	bus            *events.Bus
	defaultPool    string
	enrollmentTTL  time.Duration
	pollInterval   time.Duration
	ephemeralGrace time.Duration
	log            *slog.Logger
	metrics        *telemetry.Metrics
	now            func() time.Time

	allocMu  sync.Mutex // serialises address allocation
	policyMu sync.Mutex // serialises read-modify-write edits of policy documents
	sessMu   sync.RWMutex
	sessions Sessions
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Bus == nil {
		opts.Bus = events.NewBus(0, opts.Now)
	}
	if opts.DefaultPool == "" {
		opts.DefaultPool = "100.64.0.0/10"
	}
	if opts.EnrollmentTTL <= 0 {
		opts.EnrollmentTTL = 10 * time.Minute
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.EphemeralGrace < 0 {
		opts.EphemeralGrace = 0
	}
	return &Service{
		st:             opts.Store,
		access:         opts.Access,
		bus:            opts.Bus,
		defaultPool:    opts.DefaultPool,
		enrollmentTTL:  opts.EnrollmentTTL,
		pollInterval:   opts.PollInterval,
		ephemeralGrace: opts.EphemeralGrace,
		log:            opts.Logger,
		metrics:        opts.Metrics,
		now:            opts.Now,
	}
}

// SetSessions attaches the signalling relay. Until set, changes only touch
// storage.
func (s *Service) SetSessions(sess Sessions) {
	s.sessMu.Lock()
	s.sessions = sess
	s.sessMu.Unlock()
}

func (s *Service) live() Sessions {
	s.sessMu.RLock()
	defer s.sessMu.RUnlock()
	return s.sessions
}

func (s *Service) networkChanged(ctx context.Context, network string) {
	if l := s.live(); l != nil {
		l.NetworkChanged(ctx, network)
	}
}

func (s *Service) disconnect(peerID, code, msg string) {
	if l := s.live(); l != nil {
		l.Disconnect(peerID, code, msg)
	}
}

// Access returns the authorizer controller.
func (s *Service) Access() *access.Controller { return s.access }

// Bus returns the event bus.
func (s *Service) Bus() *events.Bus { return s.bus }

// Store returns the underlying store (read access for handlers).
func (s *Service) Store() store.Store { return s.st }

// Now returns the service clock's current time.
func (s *Service) Now() time.Time { return s.now() }

// ---- actors and audit ----

type actorKey struct{}

// WithActor records who is acting (for the audit log): "service",
// "apikey:<id>", "peer:<id>" or "system".
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor recorded by WithActor ("system" if none).
func ActorFrom(ctx context.Context) string {
	if a, ok := ctx.Value(actorKey{}).(string); ok && a != "" {
		return a
	}
	return "system"
}

// Audit appends an audit entry and publishes it on the bus. Failures to
// persist are logged, never returned: auditing must not break the operation
// it records.
func (s *Service) Audit(ctx context.Context, network, action, target string, detail map[string]any) {
	e := store.AuditEvent{
		ID:      newAuditID(s.now()),
		Time:    s.now().UTC(),
		Network: network,
		Actor:   ActorFrom(ctx),
		Action:  action,
		Target:  target,
		Detail:  detail,
	}
	if err := s.st.AppendAudit(context.WithoutCancel(ctx), e); err != nil {
		s.log.Error("audit: append", "action", action, "error", err)
	}
	s.bus.Publish(events.Event{Type: events.Audit, Network: network, Time: e.Time, Data: e})
}

// ListAudit returns audit entries.
func (s *Service) ListAudit(ctx context.Context, f store.AuditFilter) ([]store.AuditEvent, error) {
	return s.st.ListAudit(ctx, f)
}

// ---- identifiers and secrets ----

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("control: crypto/rand: %v", err))
	}
	return b
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func newID(prefix string) string { return prefix + idEncoding.EncodeToString(randomBytes(10)) }

// newAuditID sorts by time, then randomly within a millisecond.
func newAuditID(t time.Time) string {
	return fmt.Sprintf("%013d-%s", t.UnixMilli(), hex.EncodeToString(randomBytes(4)))
}

func newSecret(prefix string) string {
	return prefix + base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

// HashSecret returns the stored hash of a bearer secret (peer token, pre-auth
// key, API key, enrolment code or poll token).
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Secret prefixes identify what a bearer secret is.
const (
	PeerTokenPrefix = "gpt_"
	AuthKeyPrefix   = "gak_"
	APIKeyPrefix    = "gck_"
	PollTokenPrefix = "gpl_"
)

// ---- validation helpers ----

func normalizeRoles(roles []proto.Role) ([]proto.Role, error) {
	if len(roles) == 0 {
		return []proto.Role{proto.RoleNode}, nil
	}
	out := make([]proto.Role, 0, len(roles))
	for _, r := range roles {
		if !r.Valid() {
			return nil, invalidf("unknown role %q", r)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out, nil
}

func validLabels(l map[string]string) error {
	if len(l) > 32 {
		return invalidf("at most 32 labels")
	}
	for k, v := range l {
		if k == "" || len(k) > 64 || len(v) > 256 {
			return invalidf("label %q is empty or too long", k)
		}
	}
	return nil
}

func mergeLabels(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range layers {
		maps.Copy(out, l)
	}
	return out
}

func unionTags(layers ...[]string) []string {
	var out []string
	for _, l := range layers {
		for _, t := range l {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	slices.Sort(out)
	return out
}

// checkTags verifies every tag is defined in the network's policy.
func (s *Service) checkTags(ctx context.Context, network string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	n, err := s.st.GetNetwork(ctx, network)
	if err != nil {
		return mapStoreErr(err)
	}
	if undefined := n.Policy.UndefinedTags(tags); len(undefined) > 0 {
		return invalidf("tags not defined in network %s: %s", network, strings.Join(undefined, ", "))
	}
	return nil
}
