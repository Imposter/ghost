// Package control holds ghost-server's domain operations: networks, device
// registration and pairing, device authentication, address assignment,
// revocation, moves, and exit policies. HTTP handlers and the signalling relay
// both call into it; it applies access control and keeps live sessions in
// step through the Sessions interface.
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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/ipam"
	"github.com/Imposter/ghost/ghost-server/server/store"
	"github.com/Imposter/ghost/ghost-server/server/telemetry"
)

// Errors returned by Service. Handlers map them to HTTP statuses and
// signalling error codes.
var (
	ErrInvalid      = errors.New("invalid request")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("already exists")
	ErrUnauthorized = errors.New("unauthorized")
)

// DeniedError reports an access-control denial.
type DeniedError struct{ Reason string }

func (e *DeniedError) Error() string {
	if e.Reason == "" {
		return "denied by access control"
	}
	return "denied by access control: " + e.Reason
}

// Sessions is implemented by the signalling relay so the service can act on
// live sessions.
type Sessions interface {
	// Disconnect sends a fatal error with code/message to the device's live
	// session, if any, and closes it. It reports whether one was closed.
	Disconnect(deviceID, code, message string) bool
	// PushNetworkPolicy sends p to every session in its network that has no
	// per-device override. It returns the number of sessions reached.
	PushNetworkPolicy(p proto.ExitPolicy) int
}

// Options configures a Service.
type Options struct {
	Store       store.Store
	Access      *access.Controller
	DefaultPool string
	PairingTTL  time.Duration
	Logger      *slog.Logger
	Metrics     *telemetry.Metrics
	Now         func() time.Time
}

// Service implements the domain operations.
type Service struct {
	st          store.Store
	access      *access.Controller
	defaultPool string
	pairingTTL  time.Duration
	log         *slog.Logger
	metrics     *telemetry.Metrics
	now         func() time.Time

	allocMu  sync.Mutex // serialises address allocation
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
	if opts.DefaultPool == "" {
		opts.DefaultPool = "100.64.0.0/10"
	}
	if opts.PairingTTL <= 0 {
		opts.PairingTTL = 10 * time.Minute
	}
	return &Service{
		st:          opts.Store,
		access:      opts.Access,
		defaultPool: opts.DefaultPool,
		pairingTTL:  opts.PairingTTL,
		log:         opts.Logger,
		metrics:     opts.Metrics,
		now:         opts.Now,
	}
}

// SetSessions attaches the signalling relay. Until set, revocations and
// policy changes only touch storage.
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

// Access returns the access controller.
func (s *Service) Access() *access.Controller { return s.access }

// Store returns the underlying store (read access for handlers).
func (s *Service) Store() store.Store { return s.st }

// Now returns the service clock's current time.
func (s *Service) Now() time.Time { return s.now() }

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	}
	return err
}

// ---- networks ----

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ValidName reports whether s is a valid network name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// CreateNetwork creates a network. pool may be empty for the default pool.
func (s *Service) CreateNetwork(ctx context.Context, name, pool string) (store.Network, error) {
	if !ValidName(name) {
		return store.Network{}, fmt.Errorf("%w: network name must match %s", ErrInvalid, nameRE)
	}
	if pool == "" {
		pool = s.defaultPool
	}
	p, err := ipam.ParsePool(pool)
	if err != nil {
		return store.Network{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	n := store.Network{
		Name:      name,
		Pool:      p.String(),
		Policy:    proto.ExitPolicy{Network: name, Allow: []string{}},
		CreatedAt: s.now().UTC(),
	}
	if err := s.st.CreateNetwork(ctx, n); err != nil {
		return store.Network{}, mapStoreErr(err)
	}
	return n, nil
}

// EnsureNetwork creates a network if it does not exist.
func (s *Service) EnsureNetwork(ctx context.Context, name, pool string) error {
	if _, err := s.st.GetNetwork(ctx, name); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	_, err := s.CreateNetwork(ctx, name, pool)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// Network returns a network.
func (s *Service) Network(ctx context.Context, name string) (store.Network, error) {
	n, err := s.st.GetNetwork(ctx, name)
	return n, mapStoreErr(err)
}

// PolicyInput is an admin-supplied exit policy.
type PolicyInput struct {
	Allow          []string          `json:"allow"`
	DailyBytes     int64             `json:"daily_bytes"`
	BytesPerSecond int64             `json:"bytes_per_second"`
	Paused         bool              `json:"paused"`
	Labels         map[string]string `json:"labels"`
}

// SetNetworkPolicy stores a network's exit policy and pushes it to the live
// sessions in that network. It returns the stored policy and how many sessions
// received it.
func (s *Service) SetNetworkPolicy(ctx context.Context, network string, in PolicyInput) (proto.ExitPolicy, int, error) {
	if in.DailyBytes < 0 || in.BytesPerSecond < 0 {
		return proto.ExitPolicy{}, 0, fmt.Errorf("%w: caps must not be negative", ErrInvalid)
	}
	allow := make([]string, 0, len(in.Allow))
	for _, a := range in.Allow {
		a = strings.TrimSpace(a)
		if a == "" || a == "*" || strings.HasPrefix(a, "*:") {
			return proto.ExitPolicy{}, 0, fmt.Errorf("%w: allow entry %q would open the exit to every host", ErrInvalid, a)
		}
		allow = append(allow, a)
	}
	p, err := s.st.SetNetworkPolicy(ctx, network, proto.ExitPolicy{
		Allow:          allow,
		DailyBytes:     in.DailyBytes,
		BytesPerSecond: in.BytesPerSecond,
		Paused:         in.Paused,
		Labels:         in.Labels,
	})
	if err != nil {
		return p, 0, mapStoreErr(err)
	}
	pushed := 0
	if live := s.live(); live != nil {
		pushed = live.PushNetworkPolicy(p)
	}
	s.metrics.PolicyPushed(ctx, pushed)
	return p, pushed, nil
}

// ---- credentials ----

// Credentials are returned once, when a device is created. Only the token's
// hash is stored.
type Credentials struct {
	DeviceID    string     `json:"device_id"`
	DeviceToken string     `json:"device_token"`
	Network     string     `json:"network"`
	Role        proto.Role `json:"role"`
}

// HashToken returns the stored hash of a device token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("control: crypto/rand: %v", err))
	}
	return b
}

func newDeviceID() string {
	return "dev_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomBytes(10)))
}

func newDeviceToken() string {
	return "gdt_" + base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

func validRole(r proto.Role) bool { return r == proto.RoleNode || r == proto.RoleHub }

func validLabels(l map[string]string) error {
	if len(l) > 32 {
		return fmt.Errorf("%w: at most 32 labels", ErrInvalid)
	}
	for k, v := range l {
		if k == "" || len(k) > 64 || len(v) > 256 {
			return fmt.Errorf("%w: label %q is empty or too long", ErrInvalid, k)
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

// ---- register ----

// RegisterInput is a self-registration request.
type RegisterInput struct {
	Network   string            `json:"network"`
	Name      string            `json:"name"`
	Role      proto.Role        `json:"role"`
	Labels    map[string]string `json:"labels"`
	PublicKey string            `json:"public_key"`
}

// Register creates a device directly in a network, subject to access control
// (action register).
func (s *Service) Register(ctx context.Context, in RegisterInput) (Credentials, error) {
	if in.Role == "" {
		in.Role = proto.RoleNode
	}
	if !validRole(in.Role) {
		return Credentials{}, fmt.Errorf("%w: role must be node or hub", ErrInvalid)
	}
	if err := validLabels(in.Labels); err != nil {
		return Credentials{}, err
	}
	if _, err := s.st.GetNetwork(ctx, in.Network); err != nil {
		return Credentials{}, mapStoreErr(err)
	}
	d := s.access.Check(ctx, access.Request{
		Action: access.ActionRegister, Network: in.Network, Role: in.Role, Labels: in.Labels,
	})
	if !d.Allow {
		s.metrics.Registration(ctx, "denied")
		return Credentials{}, &DeniedError{Reason: d.Reason}
	}
	var granted map[string]string
	if d.Policy != nil {
		granted = d.Policy.Labels
	}
	creds, err := s.createDevice(ctx, in.Network, in.Role, in.Name, mergeLabels(in.Labels, granted), in.PublicKey)
	if err == nil {
		s.metrics.Registration(ctx, "ok")
	}
	return creds, err
}

func (s *Service) createDevice(ctx context.Context, network string, role proto.Role, name string, labels map[string]string, pubKey string) (Credentials, error) {
	token := newDeviceToken()
	dev := store.Device{
		ID:        newDeviceID(),
		TokenHash: HashToken(token),
		Network:   network,
		Role:      role,
		Name:      name,
		Labels:    labels,
		PublicKey: pubKey,
		CreatedAt: s.now().UTC(),
	}
	if err := s.st.CreateDevice(ctx, dev); err != nil {
		return Credentials{}, mapStoreErr(err)
	}
	s.log.Info("device created", "device", dev.ID, "network", network, "role", role)
	return Credentials{DeviceID: dev.ID, DeviceToken: token, Network: network, Role: role}, nil
}

// ---- pairing ----

// pairingAlphabet is Crockford base32 without I, L, O, U: easy to read aloud.
const pairingAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NormalizeCode upper-cases a code and strips separators and whitespace.
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte("ghost-pairing:" + NormalizeCode(code)))
	return hex.EncodeToString(sum[:])
}

func newPairingCode() string {
	raw := randomBytes(10)
	var b strings.Builder
	for i, v := range raw {
		if i == 5 {
			b.WriteByte('-')
		}
		b.WriteByte(pairingAlphabet[int(v)%len(pairingAlphabet)])
	}
	return b.String()
}

// PairingInput is an admin request for a pairing code.
type PairingInput struct {
	Network string            `json:"network"`
	Role    proto.Role        `json:"role"`
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	// TTL overrides the default lifetime (bounded to one day).
	TTL time.Duration `json:"-"`
}

// IssuedCode is a newly created pairing code. The plaintext code is only
// available here.
type IssuedCode struct {
	Code      string     `json:"code"`
	Network   string     `json:"network"`
	Role      proto.Role `json:"role"`
	ExpiresAt time.Time  `json:"expires_at"`
}

// CreatePairingCode issues a short-lived, single-use pairing code.
func (s *Service) CreatePairingCode(ctx context.Context, in PairingInput) (IssuedCode, error) {
	if in.Role == "" {
		in.Role = proto.RoleNode
	}
	if !validRole(in.Role) {
		return IssuedCode{}, fmt.Errorf("%w: role must be node or hub", ErrInvalid)
	}
	if err := validLabels(in.Labels); err != nil {
		return IssuedCode{}, err
	}
	if _, err := s.st.GetNetwork(ctx, in.Network); err != nil {
		return IssuedCode{}, mapStoreErr(err)
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = s.pairingTTL
	}
	if ttl > 24*time.Hour {
		return IssuedCode{}, fmt.Errorf("%w: ttl exceeds 24h", ErrInvalid)
	}
	now := s.now().UTC()
	for range 5 {
		code := newPairingCode()
		pc := store.PairingCode{
			CodeHash: hashCode(code), Network: in.Network, Role: in.Role, Name: in.Name,
			Labels: in.Labels, CreatedAt: now, ExpiresAt: now.Add(ttl),
		}
		err := s.st.CreatePairingCode(ctx, pc)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return IssuedCode{}, err
		}
		return IssuedCode{Code: code, Network: in.Network, Role: in.Role, ExpiresAt: pc.ExpiresAt}, nil
	}
	return IssuedCode{}, errors.New("control: could not generate a unique pairing code")
}

// PairInput redeems a pairing code.
type PairInput struct {
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	PublicKey string            `json:"public_key"`
}

// Pair redeems a pairing code and creates the device, subject to access
// control (action pair). The code is consumed only once access control
// allows, so a transient authorizer outage does not burn it.
func (s *Service) Pair(ctx context.Context, in PairInput) (Credentials, error) {
	if err := validLabels(in.Labels); err != nil {
		return Credentials{}, err
	}
	hash := hashCode(in.Code)
	now := s.now()
	pc, err := s.st.GetPairingCode(ctx, hash)
	if err != nil || pc.UsedAt != nil || !now.Before(pc.ExpiresAt) {
		s.metrics.Pairing(ctx, "invalid")
		return Credentials{}, fmt.Errorf("%w: pairing code is invalid, used, or expired", ErrNotFound)
	}
	labels := mergeLabels(in.Labels, pc.Labels)
	d := s.access.Check(ctx, access.Request{
		Action: access.ActionPair, Network: pc.Network, Role: pc.Role, Labels: labels,
	})
	if !d.Allow {
		s.metrics.Pairing(ctx, "denied")
		return Credentials{}, &DeniedError{Reason: d.Reason}
	}
	if _, err := s.st.RedeemPairingCode(ctx, hash, now); err != nil {
		s.metrics.Pairing(ctx, "invalid")
		return Credentials{}, fmt.Errorf("%w: pairing code is invalid, used, or expired", ErrNotFound)
	}
	if d.Policy != nil {
		labels = mergeLabels(labels, d.Policy.Labels)
	}
	name := in.Name
	if pc.Name != "" {
		name = pc.Name
	}
	creds, err := s.createDevice(ctx, pc.Network, pc.Role, name, labels, in.PublicKey)
	if err != nil {
		return creds, err
	}
	if err := s.st.BindPairingCode(ctx, hash, creds.DeviceID); err != nil {
		s.log.Warn("pairing: bind code to device", "device", creds.DeviceID, "error", err)
	}
	s.metrics.Pairing(ctx, "ok")
	return creds, nil
}

// ---- devices ----

// Authenticate resolves a device token to a live (unrevoked) device.
func (s *Service) Authenticate(ctx context.Context, token string) (store.Device, error) {
	if token == "" {
		return store.Device{}, ErrUnauthorized
	}
	d, err := s.st.GetDeviceByTokenHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return d, ErrUnauthorized
		}
		return d, err
	}
	if d.Revoked() {
		return d, ErrUnauthorized
	}
	return d, nil
}

// Device returns one device.
func (s *Service) Device(ctx context.Context, id string) (store.Device, error) {
	d, err := s.st.GetDevice(ctx, id)
	return d, mapStoreErr(err)
}

// EnsureAddress returns the device's address in its network, assigning one
// from the pool on first use.
func (s *Service) EnsureAddress(ctx context.Context, dev store.Device) (string, error) {
	s.allocMu.Lock()
	defer s.allocMu.Unlock()
	current, err := s.st.GetDevice(ctx, dev.ID)
	if err != nil {
		return "", mapStoreErr(err)
	}
	n, err := s.st.GetNetwork(ctx, current.Network)
	if err != nil {
		return "", mapStoreErr(err)
	}
	pool, err := ipam.ParsePool(n.Pool)
	if err != nil {
		return "", err
	}
	if current.Address != "" && ipam.Contains(pool, current.Address) {
		return current.Address, nil
	}
	used, err := s.st.UsedAddresses(ctx, current.Network)
	if err != nil {
		return "", err
	}
	addr, err := ipam.Allocate(pool, used)
	if err != nil {
		return "", err
	}
	if err := s.st.SetDeviceAddress(ctx, current.ID, addr); err != nil {
		return "", mapStoreErr(err)
	}
	return addr, nil
}

// Revoke revokes a device and immediately disconnects its live session.
func (s *Service) Revoke(ctx context.Context, id string) (store.Device, error) {
	if err := s.st.RevokeDevice(ctx, id, s.now().UTC()); err != nil {
		return store.Device{}, mapStoreErr(err)
	}
	s.access.Forget(id)
	if live := s.live(); live != nil {
		live.Disconnect(id, proto.ErrCodeRevoked, "device revoked")
	}
	s.metrics.Revoked(ctx)
	s.log.Info("device revoked", "device", id)
	return s.Device(ctx, id)
}

// Move moves a device to another network. Its address is released (a new one
// is assigned on its next join) and its live session is closed so it rejoins
// under the new network.
func (s *Service) Move(ctx context.Context, id, network string) (store.Device, error) {
	if _, err := s.st.GetNetwork(ctx, network); err != nil {
		return store.Device{}, mapStoreErr(err)
	}
	s.allocMu.Lock()
	err := s.st.MoveDevice(ctx, id, network)
	s.allocMu.Unlock()
	if err != nil {
		return store.Device{}, mapStoreErr(err)
	}
	s.access.Forget(id)
	if live := s.live(); live != nil {
		live.Disconnect(id, proto.ErrCodeForbidden, "device moved to network "+network)
	}
	s.log.Info("device moved", "device", id, "network", network)
	return s.Device(ctx, id)
}
