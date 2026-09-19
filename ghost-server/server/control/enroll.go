package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// Credentials are returned once, when a peer is created. Only the token's hash
// is stored.
type Credentials struct {
	PeerID    string       `json:"peer_id"`
	PeerToken string       `json:"peer_token"`
	Network   string       `json:"network"`
	Roles     []proto.Role `json:"roles"`
	Tags      []string     `json:"tags"`
	Ephemeral bool         `json:"ephemeral,omitempty"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
}

// newPeer is everything needed to create a peer.
type newPeer struct {
	network   string
	name      string
	publicKey string
	roles     []proto.Role
	tags      []string
	labels    map[string]string
	ephemeral bool
	authKeyID string
	ttl       time.Duration
	via       string // "auth_key", "interactive" or "control", for the audit log
}

// authorizeEnroll consults the authorizer; the decision's tags and labels are
// merged into np.
func (s *Service) authorizeEnroll(ctx context.Context, np *newPeer) error {
	d := s.access.Check(ctx, access.Request{
		Action: access.ActionEnroll, Network: np.network, Roles: np.roles, Tags: np.tags, Labels: np.labels,
	})
	if !d.Allow {
		s.metrics.Registration(ctx, "denied")
		s.Audit(ctx, np.network, "peer.enroll_denied", "", map[string]any{"via": np.via, "reason": d.Reason})
		return &DeniedError{Reason: d.Reason, Unavailable: d.Unavailable}
	}
	if d.Policy != nil {
		if err := s.checkTags(ctx, np.network, d.Policy.Tags); err != nil {
			return err
		}
		np.tags = unionTags(np.tags, d.Policy.Tags)
		np.labels = mergeLabels(np.labels, d.Policy.Labels)
	}
	return nil
}

func (s *Service) createPeer(ctx context.Context, np newPeer) (Credentials, error) {
	if np.publicKey != "" && !ValidWireGuardKey(np.publicKey) {
		return Credentials{}, invalidf("public_key must be a base64 32-byte key")
	}
	now := s.now().UTC()
	token := newSecret(PeerTokenPrefix)
	p := store.Peer{
		ID: newID("peer_"), TokenHash: HashSecret(token), Network: np.network, Name: np.name,
		PublicKey: np.publicKey, Roles: np.roles, Tags: unionTags(np.tags), Labels: np.labels,
		Ephemeral: np.ephemeral, AuthKeyID: np.authKeyID, CreatedAt: now,
	}
	if np.ttl > 0 {
		exp := now.Add(np.ttl)
		p.ExpiresAt = &exp
	}
	if err := s.st.CreatePeer(ctx, p); err != nil {
		return Credentials{}, mapStoreErr(err)
	}
	s.metrics.Registration(ctx, "ok")
	s.Audit(ctx, p.Network, "peer.enrolled", p.ID, map[string]any{
		"via": np.via, "roles": p.Roles, "tags": p.Tags, "ephemeral": p.Ephemeral, "auth_key": np.authKeyID,
	})
	s.publishPeer(events.PeerEnrolled, p, map[string]any{"via": np.via})
	s.networkChanged(ctx, p.Network)
	return Credentials{
		PeerID: p.ID, PeerToken: token, Network: p.Network, Roles: p.Roles, Tags: p.Tags,
		Ephemeral: p.Ephemeral, ExpiresAt: p.ExpiresAt,
	}, nil
}

// PeerInput creates a peer directly through the control API (no pre-auth key
// or interactive approval).
type PeerInput struct {
	Name      string            `json:"name"`
	PublicKey string            `json:"public_key"`
	Roles     []proto.Role      `json:"roles"`
	Tags      []string          `json:"tags"`
	Labels    map[string]string `json:"labels"`
	Ephemeral bool              `json:"ephemeral"`
	// TTL, when positive, expires the peer after this long.
	TTL time.Duration `json:"-"`
}

// CreatePeer creates a peer in network and returns its credentials. The
// authorizer is consulted as for any enrolment.
func (s *Service) CreatePeer(ctx context.Context, network string, in PeerInput) (Credentials, error) {
	roles, err := normalizeRoles(in.Roles)
	if err != nil {
		return Credentials{}, err
	}
	if err := validLabels(in.Labels); err != nil {
		return Credentials{}, err
	}
	if in.TTL < 0 {
		return Credentials{}, invalidf("ttl must not be negative")
	}
	if _, err := s.Network(ctx, network); err != nil {
		return Credentials{}, err
	}
	if err := s.checkTags(ctx, network, in.Tags); err != nil {
		return Credentials{}, err
	}
	np := newPeer{
		network: network, name: in.Name, publicKey: in.PublicKey, roles: roles, tags: in.Tags,
		labels: in.Labels, ephemeral: in.Ephemeral, ttl: in.TTL, via: "control",
	}
	if err := s.authorizeEnroll(ctx, &np); err != nil {
		return Credentials{}, err
	}
	return s.createPeer(ctx, np)
}

// ---- pre-auth keys ----

// AuthKeyInput creates a pre-auth key.
type AuthKeyInput struct {
	// Reusable keys enrol any number of peers until they expire; otherwise
	// the key is single-use.
	Reusable bool `json:"reusable"`
	// Ephemeral peers are deleted after staying offline past the grace
	// period.
	Ephemeral bool              `json:"ephemeral"`
	Roles     []proto.Role      `json:"roles"`
	Tags      []string          `json:"tags"`
	Labels    map[string]string `json:"labels"`
	// ExpiresIn is the key's lifetime (default 1h, at most 90 days).
	ExpiresIn time.Duration `json:"-"`
	// PeerTTL, when positive, expires enrolled peers after this long.
	PeerTTL time.Duration `json:"-"`
}

// IssuedAuthKey is a newly created pre-auth key. The plaintext key is only
// available here.
type IssuedAuthKey struct {
	Key     string        `json:"key"`
	AuthKey store.AuthKey `json:"-"`
}

// CreateAuthKey issues a pre-auth key for a network.
func (s *Service) CreateAuthKey(ctx context.Context, network string, in AuthKeyInput) (IssuedAuthKey, error) {
	roles, err := normalizeRoles(in.Roles)
	if err != nil {
		return IssuedAuthKey{}, err
	}
	if err := validLabels(in.Labels); err != nil {
		return IssuedAuthKey{}, err
	}
	if err := s.checkTags(ctx, network, in.Tags); err != nil {
		return IssuedAuthKey{}, err
	}
	if in.ExpiresIn <= 0 {
		in.ExpiresIn = time.Hour
	}
	if in.ExpiresIn > 90*24*time.Hour {
		return IssuedAuthKey{}, invalidf("expires_in exceeds 90 days")
	}
	if in.PeerTTL < 0 {
		return IssuedAuthKey{}, invalidf("peer_ttl must not be negative")
	}
	now := s.now().UTC()
	key := newSecret(AuthKeyPrefix)
	k := store.AuthKey{
		ID: newID("key_"), KeyHash: HashSecret(key), Network: network, Reusable: in.Reusable,
		Ephemeral: in.Ephemeral, Roles: roles, Tags: unionTags(in.Tags), Labels: in.Labels,
		PeerTTL: in.PeerTTL, CreatedAt: now, ExpiresAt: now.Add(in.ExpiresIn),
	}
	if err := s.st.CreateAuthKey(ctx, k); err != nil {
		return IssuedAuthKey{}, mapStoreErr(err)
	}
	s.Audit(ctx, network, "auth_key.created", k.ID, map[string]any{
		"reusable": k.Reusable, "ephemeral": k.Ephemeral, "roles": k.Roles, "tags": k.Tags, "expires_at": k.ExpiresAt,
	})
	return IssuedAuthKey{Key: key, AuthKey: k}, nil
}

// ListAuthKeys returns a network's pre-auth keys (hashes only).
func (s *Service) ListAuthKeys(ctx context.Context, network string) ([]store.AuthKey, error) {
	if _, err := s.Network(ctx, network); err != nil {
		return nil, err
	}
	return s.st.ListAuthKeys(ctx, network)
}

// RevokeAuthKey revokes a pre-auth key. Peers it enrolled are unaffected.
func (s *Service) RevokeAuthKey(ctx context.Context, id string) (store.AuthKey, error) {
	k, err := s.st.RevokeAuthKey(ctx, id, s.now().UTC())
	if err != nil {
		return k, mapStoreErr(err)
	}
	s.Audit(ctx, k.Network, "auth_key.revoked", id, nil)
	return k, nil
}

// EnrollInput enrols a peer with a pre-auth key.
type EnrollInput struct {
	AuthKey   string            `json:"auth_key"`
	Name      string            `json:"name"`
	PublicKey string            `json:"public_key"`
	Labels    map[string]string `json:"labels"`
}

var errBadAuthKey = errors.New("pre-auth key is invalid, spent, revoked or expired")

// Enroll creates a peer from a pre-auth key: the key's network, roles, tags
// and labels apply. The authorizer is consulted before the key is spent.
func (s *Service) Enroll(ctx context.Context, in EnrollInput) (Credentials, error) {
	if err := validLabels(in.Labels); err != nil {
		return Credentials{}, err
	}
	k, err := s.st.GetAuthKeyByHash(ctx, HashSecret(in.AuthKey))
	if err != nil || !k.Usable(s.now()) {
		return Credentials{}, errors.Join(ErrUnauthorized, errBadAuthKey)
	}
	np := newPeer{
		network: k.Network, name: in.Name, publicKey: in.PublicKey, roles: k.Roles, tags: k.Tags,
		labels: mergeLabels(in.Labels, k.Labels), ephemeral: k.Ephemeral, authKeyID: k.ID, ttl: k.PeerTTL, via: "auth_key",
	}
	if err := s.authorizeEnroll(ctx, &np); err != nil {
		return Credentials{}, err
	}
	if _, err := s.st.UseAuthKey(ctx, k.ID, s.now()); err != nil {
		return Credentials{}, errors.Join(ErrUnauthorized, errBadAuthKey)
	}
	return s.createPeer(ctx, np)
}

// ---- interactive enrolment ----

// enrollmentAlphabet is Crockford base32 without I, L, O, U: easy to read
// aloud and to type.
const enrollmentAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NormalizeCode upper-cases an enrolment code and strips separators.
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r != '-' && r != ' ' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hashCode(code string) string { return HashSecret("ghost-enrollment:" + NormalizeCode(code)) }

func newEnrollmentCode() string {
	raw := randomBytes(10)
	var b strings.Builder
	for i, v := range raw {
		if i == 5 {
			b.WriteByte('-')
		}
		b.WriteByte(enrollmentAlphabet[int(v)%len(enrollmentAlphabet)])
	}
	return b.String()
}

// interactiveAllowed returns ErrNotFound for an unknown network and
// ErrInteractiveEnrollmentDisabled when the network's switch is off.
func (s *Service) interactiveAllowed(ctx context.Context, network string) error {
	n, err := s.Network(ctx, network)
	if err != nil {
		return err
	}
	if !n.InteractiveEnrollment {
		return fmt.Errorf("%w for network %s", ErrInteractiveEnrollmentDisabled, network)
	}
	return nil
}

// StartEnrollmentInput begins an interactive enrolment.
type StartEnrollmentInput struct {
	Network   string            `json:"network"`
	Name      string            `json:"name"`
	PublicKey string            `json:"public_key"`
	Labels    map[string]string `json:"labels"`
}

// StartedEnrollment is returned to the enrolling peer. It shows Code to its
// user and polls with PollToken.
type StartedEnrollment struct {
	Code            string    `json:"code"`
	PollToken       string    `json:"poll_token"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval_seconds"`
}

// StartEnrollment creates a pending interactive enrolment.
func (s *Service) StartEnrollment(ctx context.Context, in StartEnrollmentInput) (StartedEnrollment, error) {
	if err := validLabels(in.Labels); err != nil {
		return StartedEnrollment{}, err
	}
	if in.PublicKey != "" && !ValidWireGuardKey(in.PublicKey) {
		return StartedEnrollment{}, invalidf("public_key must be a base64 32-byte key")
	}
	if err := s.interactiveAllowed(ctx, in.Network); err != nil {
		return StartedEnrollment{}, err
	}
	now := s.now().UTC()
	for range 5 {
		code := newEnrollmentCode()
		poll := newSecret(PollTokenPrefix)
		e := store.Enrollment{
			CodeHash: hashCode(code), PollHash: HashSecret(poll), Network: in.Network, Name: in.Name,
			PublicKey: in.PublicKey, Labels: in.Labels, Status: store.EnrollmentPending,
			CreatedAt: now, ExpiresAt: now.Add(s.enrollmentTTL),
		}
		err := s.st.CreateEnrollment(ctx, e)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return StartedEnrollment{}, err
		}
		s.Audit(ctx, in.Network, "enrollment.started", "", map[string]any{"name": in.Name})
		s.bus.Publish(events.Event{Type: events.EnrollmentPending, Network: in.Network, Data: map[string]any{
			"name": in.Name, "labels": in.Labels, "expires_at": e.ExpiresAt,
		}})
		return StartedEnrollment{Code: code, PollToken: poll, ExpiresAt: e.ExpiresAt,
			IntervalSeconds: int(s.pollInterval / time.Second)}, nil
	}
	return StartedEnrollment{}, errors.New("control: could not generate a unique enrolment code")
}

// EnrollmentView is an enrolment as the control API shows it.
type EnrollmentView struct {
	Network   string                 `json:"network"`
	Name      string                 `json:"name"`
	Labels    map[string]string      `json:"labels,omitempty"`
	Status    store.EnrollmentStatus `json:"status"`
	Roles     []proto.Role           `json:"roles,omitempty"`
	Tags      []string               `json:"tags,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	PeerID    string                 `json:"peer_id,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	ExpiresAt time.Time              `json:"expires_at"`
}

func (s *Service) enrollmentView(e store.Enrollment) EnrollmentView {
	status := e.Status
	if status == store.EnrollmentPending && !s.now().Before(e.ExpiresAt) {
		status = store.EnrollmentExpired
	}
	return EnrollmentView{Network: e.Network, Name: e.Name, Labels: e.Labels, Status: status, Roles: e.Roles,
		Tags: e.Tags, Reason: e.Reason, PeerID: e.PeerID, CreatedAt: e.CreatedAt, ExpiresAt: e.ExpiresAt}
}

// Enrollment looks an enrolment up by its user-facing code.
func (s *Service) Enrollment(ctx context.Context, code string) (EnrollmentView, error) {
	e, err := s.st.GetEnrollmentByCode(ctx, hashCode(code))
	if err != nil {
		return EnrollmentView{}, mapStoreErr(err)
	}
	return s.enrollmentView(e), nil
}

// ListEnrollments lists a network's enrolments, optionally by status.
func (s *Service) ListEnrollments(ctx context.Context, network string, status store.EnrollmentStatus) ([]EnrollmentView, error) {
	es, err := s.st.ListEnrollments(ctx, network, status)
	if err != nil {
		return nil, err
	}
	out := make([]EnrollmentView, 0, len(es))
	for _, e := range es {
		out = append(out, s.enrollmentView(e))
	}
	return out, nil
}

// ApproveInput approves an interactive enrolment.
type ApproveInput struct {
	Roles  []proto.Role      `json:"roles"`
	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels"`
	Name   string            `json:"name"`
}

// ApproveEnrollment approves a pending enrolment. The peer is created when the
// enrolling side next polls. It is refused while the network's interactive
// enrolment is disabled.
func (s *Service) ApproveEnrollment(ctx context.Context, code string, in ApproveInput) (EnrollmentView, error) {
	roles, err := normalizeRoles(in.Roles)
	if err != nil {
		return EnrollmentView{}, err
	}
	if err := validLabels(in.Labels); err != nil {
		return EnrollmentView{}, err
	}
	// The network checks read the store, so they run before decide's
	// transaction (SQLite has a single connection). An enrolment never changes
	// network, and PollEnrollment re-checks the switch before minting a peer.
	pending, err := s.st.GetEnrollmentByCode(ctx, hashCode(code))
	if err != nil {
		return EnrollmentView{}, mapStoreErr(err)
	}
	if err := s.interactiveAllowed(ctx, pending.Network); err != nil {
		return EnrollmentView{}, err
	}
	if err := s.checkTags(ctx, pending.Network, in.Tags); err != nil {
		return EnrollmentView{}, err
	}
	e, err := s.decide(ctx, code, func(e *store.Enrollment) error {
		e.Status, e.Roles, e.Tags = store.EnrollmentApproved, roles, unionTags(in.Tags)
		e.Labels = mergeLabels(e.Labels, in.Labels)
		if in.Name != "" {
			e.Name = in.Name
		}
		return nil
	})
	if err != nil {
		return EnrollmentView{}, err
	}
	s.Audit(ctx, e.Network, "enrollment.approved", "", map[string]any{"roles": e.Roles, "tags": e.Tags, "name": e.Name})
	return s.enrollmentView(e), nil
}

// DenyEnrollment denies a pending enrolment.
func (s *Service) DenyEnrollment(ctx context.Context, code, reason string) (EnrollmentView, error) {
	e, err := s.decide(ctx, code, func(e *store.Enrollment) error {
		e.Status, e.Reason = store.EnrollmentDenied, reason
		return nil
	})
	if err != nil {
		return EnrollmentView{}, err
	}
	s.Audit(ctx, e.Network, "enrollment.denied", "", map[string]any{"reason": reason})
	return s.enrollmentView(e), nil
}

// decide applies fn to a pending, unexpired enrolment.
func (s *Service) decide(ctx context.Context, code string, fn func(*store.Enrollment) error) (store.Enrollment, error) {
	e, err := s.st.UpdateEnrollment(ctx, hashCode(code), func(e *store.Enrollment) error {
		if e.Status != store.EnrollmentPending {
			return invalidf("enrolment is %s", e.Status)
		}
		if !s.now().Before(e.ExpiresAt) {
			return invalidf("enrolment code expired")
		}
		if err := fn(e); err != nil {
			return err
		}
		t := s.now().UTC()
		e.DecidedAt = &t
		return nil
	})
	return e, mapStoreErr(err)
}

// PollResult is the enrolling peer's view of its enrolment. Credentials are
// present exactly once: on the poll that claims an approved enrolment.
type PollResult struct {
	Status      store.EnrollmentStatus `json:"status"`
	Reason      string                 `json:"reason,omitempty"`
	Credentials *Credentials           `json:"credentials,omitempty"`
}

// PollEnrollment reports an enrolment's status. The first poll after approval
// consults the authorizer, creates the peer and returns its credentials. An
// approved enrolment is not claimed while the network's interactive enrolment
// is disabled; it stays approved until the switch is back on or it is swept.
func (s *Service) PollEnrollment(ctx context.Context, pollToken string) (PollResult, error) {
	e, err := s.st.GetEnrollmentByPoll(ctx, HashSecret(pollToken))
	if err != nil {
		return PollResult{}, errors.Join(ErrUnauthorized, errors.New("unknown poll token"))
	}
	view := s.enrollmentView(e)
	if view.Status != store.EnrollmentApproved {
		return PollResult{Status: view.Status, Reason: view.Reason}, nil
	}
	if err := s.interactiveAllowed(ctx, e.Network); err != nil {
		return PollResult{}, err
	}
	// Claim atomically so two concurrent polls cannot both mint a peer.
	claimed, err := s.st.UpdateEnrollment(ctx, e.CodeHash, func(e *store.Enrollment) error {
		if e.Status != store.EnrollmentApproved {
			return ErrConflict
		}
		e.Status = store.EnrollmentClaimed
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return PollResult{Status: store.EnrollmentClaimed}, nil
		}
		return PollResult{}, mapStoreErr(err)
	}
	np := newPeer{
		network: claimed.Network, name: claimed.Name, publicKey: claimed.PublicKey, roles: claimed.Roles,
		tags: claimed.Tags, labels: claimed.Labels, via: "interactive",
	}
	if err := s.authorizeEnroll(ctx, &np); err != nil {
		// A decision denies for good; an outage (or any other failure) puts the
		// enrolment back so a later poll retries.
		var denied *DeniedError
		final := errors.As(err, &denied) && !denied.Unavailable
		_, _ = s.st.UpdateEnrollment(ctx, e.CodeHash, func(e *store.Enrollment) error {
			if final {
				e.Status, e.Reason = store.EnrollmentDenied, denied.Error()
			} else {
				e.Status = store.EnrollmentApproved
			}
			return nil
		})
		if final {
			return PollResult{Status: store.EnrollmentDenied, Reason: denied.Error()}, nil
		}
		return PollResult{}, err
	}
	creds, err := s.createPeer(ctx, np)
	if err != nil {
		return PollResult{}, err
	}
	_, _ = s.st.UpdateEnrollment(ctx, e.CodeHash, func(e *store.Enrollment) error {
		e.PeerID = creds.PeerID
		return nil
	})
	return PollResult{Status: store.EnrollmentClaimed, Credentials: &creds}, nil
}
