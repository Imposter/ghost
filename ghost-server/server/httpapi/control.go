package httpapi

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/policy"
	"github.com/Imposter/ghost/ghost-server/server/signalling"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// ControlAPI serves the control API under /control. Every route needs
// "Authorization: Bearer <token>" where the token is the service token (every
// scope and network) or a scoped API key.
type ControlAPI struct {
	svc          *control.Service
	relay        *signalling.Relay
	serviceToken []byte
	log          *slog.Logger
}

// ControlOptions configures the control API.
type ControlOptions struct {
	Service *control.Service
	Relay   *signalling.Relay
	// ServiceToken is required; the control API is not mounted without it.
	ServiceToken string
	Logger       *slog.Logger
}

// NewControlAPI returns the control API.
func NewControlAPI(opts ControlOptions) *ControlAPI {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &ControlAPI{svc: opts.Service, relay: opts.Relay, serviceToken: []byte(opts.ServiceToken), log: opts.Logger}
}

type controlHandler func(w http.ResponseWriter, r *http.Request, p control.Principal)

// Register mounts the routes on mux.
func (a *ControlAPI) Register(mux *http.ServeMux) {
	h := func(pattern string, scope control.Scope, fn controlHandler) {
		mux.Handle(pattern, a.auth(scope, fn))
	}
	// Networks and isolation.
	h("GET /control/networks", control.ScopeNetworksRead, a.listNetworks)
	h("POST /control/networks", control.ScopeNetworksWrite, a.createNetwork)
	h("GET /control/networks/{net}", control.ScopeNetworksRead, a.getNetwork)
	h("PATCH /control/networks/{net}", control.ScopeNetworksWrite, a.patchNetwork)
	h("DELETE /control/networks/{net}", control.ScopeNetworksWrite, a.deleteNetwork)
	h("POST /control/networks/{net}/reauthorize", control.ScopePeersWrite, a.reauthorizeNetwork)
	// Policy: the whole document, or its ACLs, tags and exit policies.
	h("GET /control/networks/{net}/policy", control.ScopePolicyRead, a.getPolicy)
	h("PUT /control/networks/{net}/policy", control.ScopePolicyWrite, a.putPolicy)
	h("GET /control/networks/{net}/acls", control.ScopePolicyRead, a.getACLs)
	h("PUT /control/networks/{net}/acls", control.ScopePolicyWrite, a.putACLs)
	h("GET /control/networks/{net}/tags", control.ScopePolicyRead, a.getTags)
	h("PUT /control/networks/{net}/tags/{tag}", control.ScopePolicyWrite, a.putTag)
	h("DELETE /control/networks/{net}/tags/{tag}", control.ScopePolicyWrite, a.deleteTag)
	h("GET /control/networks/{net}/exit-policies", control.ScopePolicyRead, a.getExitPolicies)
	h("PUT /control/networks/{net}/exit-policies/{name}", control.ScopePolicyWrite, a.putExitPolicy)
	h("DELETE /control/networks/{net}/exit-policies/{name}", control.ScopePolicyWrite, a.deleteExitPolicy)
	// Enrolment.
	h("GET /control/networks/{net}/auth-keys", control.ScopeKeysRead, a.listAuthKeys)
	h("POST /control/networks/{net}/auth-keys", control.ScopeKeysWrite, a.createAuthKey)
	h("DELETE /control/networks/{net}/auth-keys/{id}", control.ScopeKeysWrite, a.revokeAuthKey)
	h("GET /control/networks/{net}/enrollments", control.ScopeKeysRead, a.listEnrollments)
	h("GET /control/enrollments/{code}", control.ScopeKeysRead, a.getEnrollment)
	h("POST /control/enrollments/{code}/approve", control.ScopeKeysWrite, a.approveEnrollment)
	h("POST /control/enrollments/{code}/deny", control.ScopeKeysWrite, a.denyEnrollment)
	// Peers.
	h("POST /control/networks/{net}/peers", control.ScopePeersWrite, a.createPeer)
	h("GET /control/peers", control.ScopePeersRead, a.listPeers)
	h("GET /control/peers/{id}", control.ScopePeersRead, a.getPeer)
	h("PATCH /control/peers/{id}", control.ScopePeersWrite, a.patchPeer)
	h("DELETE /control/peers/{id}", control.ScopePeersWrite, a.deletePeer)
	h("POST /control/peers/{id}/revoke", control.ScopePeersWrite, a.revokePeer)
	h("POST /control/peers/{id}/expire", control.ScopePeersWrite, a.expirePeer)
	h("POST /control/peers/{id}/move", control.ScopePeersWrite, a.movePeer)
	h("POST /control/peers/{id}/reauthorize", control.ScopePeersWrite, a.reauthorizePeer)
	h("GET /control/peers/{id}/health", control.ScopePeersRead, a.peerHealth)
	// Fleet views.
	h("GET /control/health", control.ScopePeersRead, a.fleetHealth)
	h("GET /control/presence", control.ScopePeersRead, a.presence)
	h("GET /control/stats", control.ScopePeersRead, a.stats)
	h("GET /control/audit", control.ScopeAuditRead, a.audit)
	h("GET /control/watch", control.ScopeWatch, a.watch)
	// API keys.
	h("GET /control/api-keys", control.ScopeAdmin, a.listAPIKeys)
	h("POST /control/api-keys", control.ScopeAdmin, a.createAPIKey)
	h("DELETE /control/api-keys/{id}", control.ScopeAdmin, a.revokeAPIKey)
}

// auth authenticates the caller and checks scope.
func (a *ControlAPI) auth(scope control.Scope, next controlHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.principal(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-control"`)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !p.Has(scope) {
			writeError(w, http.StatusForbidden, "missing scope "+string(scope))
			return
		}
		next(w, r.WithContext(control.WithActor(r.Context(), p.Actor)), p)
	})
}

func (a *ControlAPI) principal(r *http.Request) (control.Principal, error) {
	tok, ok := bearer(r)
	if !ok {
		return control.Principal{}, control.ErrUnauthorized
	}
	if len(a.serviceToken) > 0 && subtle.ConstantTimeCompare([]byte(tok), a.serviceToken) == 1 {
		return control.ServicePrincipal(), nil
	}
	if strings.HasPrefix(tok, control.APIKeyPrefix) {
		return a.svc.AuthenticateAPIKey(r.Context(), tok)
	}
	return control.Principal{}, control.ErrUnauthorized
}

// network checks the caller may act on the {net} path value and returns it.
func (a *ControlAPI) network(w http.ResponseWriter, r *http.Request, p control.Principal) (string, bool) {
	name := r.PathValue("net")
	if !p.CanAccess(name) {
		writeError(w, http.StatusNotFound, "not found")
		return "", false
	}
	return name, true
}

// peer loads the {id} peer and checks the caller may see its network.
func (a *ControlAPI) peer(w http.ResponseWriter, r *http.Request, p control.Principal) (store.Peer, bool) {
	peer, err := a.svc.Peer(r.Context(), r.PathValue("id"))
	if err != nil || !p.CanAccess(peer.Network) {
		writeError(w, http.StatusNotFound, "not found")
		return peer, false
	}
	return peer, true
}

// ---- views ----

// NetworkView is the control API form of a network.
type NetworkView struct {
	Name                  string           `json:"name"`
	Pool                  string           `json:"pool"`
	Isolation             policy.Isolation `json:"isolation"`
	InteractiveEnrollment bool             `json:"interactive_enrollment"`
	PolicyRevision        int64            `json:"policy_revision"`
	CreatedAt             time.Time        `json:"created_at"`
	Online                int              `json:"online"`
}

func (a *ControlAPI) networkView(n store.Network) NetworkView {
	online := 0
	for _, s := range a.relay.Presence(n.Name) {
		if s.Joined {
			online++
		}
	}
	return NetworkView{Name: n.Name, Pool: n.Pool, Isolation: n.Isolation, InteractiveEnrollment: n.InteractiveEnrollment,
		PolicyRevision: n.PolicyRevision, CreatedAt: n.CreatedAt, Online: online}
}

// PolicyView is a network's policy document with its revision.
type PolicyView struct {
	Network   string           `json:"network"`
	Isolation policy.Isolation `json:"isolation"`
	Revision  int64            `json:"revision"`
	Policy    policy.Document  `json:"policy"`
}

// PeerView is the control API form of a peer. The token hash is never
// exposed.
type PeerView struct {
	ID        string            `json:"id"`
	Network   string            `json:"network"`
	Name      string            `json:"name,omitempty"`
	PublicKey string            `json:"public_key,omitempty"`
	Address   string            `json:"address,omitempty"`
	Roles     []proto.Role      `json:"roles"`
	Tags      []string          `json:"tags"`
	Labels    map[string]string `json:"labels"`
	Endpoints []string          `json:"endpoints,omitempty"`
	Ephemeral bool              `json:"ephemeral"`
	// EnrollmentMethod is "auth_key", "interactive" or "direct" (empty when
	// unknown); AuthKeyID is the pre-auth key an auth_key enrolment used.
	EnrollmentMethod string               `json:"enrollment_method,omitempty"`
	AuthKeyID        string               `json:"auth_key_id,omitempty"`
	Status           store.PeerStatus     `json:"status"`
	CreatedAt        time.Time            `json:"created_at"`
	LastSeen         *time.Time           `json:"last_seen,omitempty"`
	ExpiresAt        *time.Time           `json:"expires_at,omitempty"`
	RevokedAt        *time.Time           `json:"revoked_at,omitempty"`
	Online           bool                 `json:"online"`
	Session          *signalling.Presence `json:"session,omitempty"`
	Health           *proto.Health        `json:"health,omitempty"`
	HealthAt         *time.Time           `json:"health_at,omitempty"`
}

func (a *ControlAPI) peerView(p store.Peer) PeerView {
	v := PeerView{
		ID: p.ID, Network: p.Network, Name: p.Name, PublicKey: p.PublicKey, Address: p.Address,
		Roles: p.Roles, Tags: p.Tags, Labels: p.Labels, Endpoints: p.Endpoints, Ephemeral: p.Ephemeral,
		EnrollmentMethod: p.EnrollmentMethod, AuthKeyID: p.AuthKeyID, Status: p.Status(a.svc.Now()), CreatedAt: p.CreatedAt, LastSeen: p.LastSeen, ExpiresAt: p.ExpiresAt,
		RevokedAt: p.RevokedAt, Health: p.Health, HealthAt: p.HealthAt,
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	if s, ok := a.relay.Session(p.ID); ok {
		v.Online, v.Session = s.Joined, &s
	}
	return v
}

// AuthKeyView is the control API form of a pre-auth key (never the key).
type AuthKeyView struct {
	ID             string            `json:"id"`
	Network        string            `json:"network"`
	Reusable       bool              `json:"reusable"`
	Ephemeral      bool              `json:"ephemeral"`
	Roles          []proto.Role      `json:"roles"`
	Tags           []string          `json:"tags"`
	Labels         map[string]string `json:"labels,omitempty"`
	PeerTTLSeconds int64             `json:"peer_ttl_seconds,omitempty"`
	Uses           int64             `json:"uses"`
	Usable         bool              `json:"usable"`
	CreatedAt      time.Time         `json:"created_at"`
	ExpiresAt      time.Time         `json:"expires_at"`
	LastUsedAt     *time.Time        `json:"last_used_at,omitempty"`
	RevokedAt      *time.Time        `json:"revoked_at,omitempty"`
	// Key is the plaintext key, present only in the create response.
	Key string `json:"key,omitempty"`
}

func (a *ControlAPI) authKeyView(k store.AuthKey) AuthKeyView {
	v := AuthKeyView{ID: k.ID, Network: k.Network, Reusable: k.Reusable, Ephemeral: k.Ephemeral, Roles: k.Roles,
		Tags: k.Tags, Labels: k.Labels, PeerTTLSeconds: int64(k.PeerTTL / time.Second), Uses: k.Uses,
		Usable: k.Usable(a.svc.Now()), CreatedAt: k.CreatedAt, ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
		RevokedAt: k.RevokedAt}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	return v
}

// APIKeyView is the control API form of an API key (never the key).
type APIKeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Networks   []string   `json:"networks"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	// Key is the plaintext key, present only in the create response.
	Key string `json:"key,omitempty"`
}

func apiKeyView(k store.APIKey) APIKeyView {
	v := APIKeyView{ID: k.ID, Name: k.Name, Scopes: k.Scopes, Networks: k.Networks, CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt}
	if v.Networks == nil {
		v.Networks = []string{}
	}
	return v
}

// ---- networks ----

func (a *ControlAPI) listNetworks(w http.ResponseWriter, r *http.Request, p control.Principal) {
	ns, err := a.svc.ListNetworks(r.Context())
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := []NetworkView{}
	for _, n := range ns {
		if p.CanAccess(n.Name) {
			out = append(out, a.networkView(n))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": out})
}

func (a *ControlAPI) createNetwork(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if p.Networks != nil {
		writeError(w, http.StatusForbidden, "network-scoped keys cannot create networks")
		return
	}
	var in control.NetworkInput
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := a.svc.CreateNetwork(r.Context(), in)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, a.networkView(n))
}

func (a *ControlAPI) getNetwork(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	n, err := a.svc.Network(r.Context(), name)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.networkView(n))
}

func (a *ControlAPI) patchNetwork(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var patch control.NetworkPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	n, err := a.svc.UpdateNetwork(r.Context(), name, patch)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.networkView(n))
}

func (a *ControlAPI) deleteNetwork(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	if err := a.svc.DeleteNetwork(r.Context(), name); err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- policy ----

func (a *ControlAPI) writePolicy(w http.ResponseWriter, n store.Network) {
	writeJSON(w, http.StatusOK, PolicyView{Network: n.Name, Isolation: n.Isolation, Revision: n.PolicyRevision, Policy: n.Policy})
}

func (a *ControlAPI) loadNetwork(w http.ResponseWriter, r *http.Request, p control.Principal) (store.Network, bool) {
	name, ok := a.network(w, r, p)
	if !ok {
		return store.Network{}, false
	}
	n, err := a.svc.Network(r.Context(), name)
	if err != nil {
		writeServiceError(w, a.log, err)
		return n, false
	}
	return n, true
}

func (a *ControlAPI) getPolicy(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if n, ok := a.loadNetwork(w, r, p); ok {
		a.writePolicy(w, n)
	}
}

func (a *ControlAPI) putPolicy(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var doc policy.Document
	if !decodeJSON(w, r, &doc) {
		return
	}
	a.policyResult(w)(a.svc.SetPolicy(r.Context(), name, doc))
}

// policyResult writes the outcome of a policy edit.
func (a *ControlAPI) policyResult(w http.ResponseWriter) func(store.Network, error) {
	return func(n store.Network, err error) {
		if err != nil {
			writeServiceError(w, a.log, err)
			return
		}
		a.writePolicy(w, n)
	}
}

func (a *ControlAPI) getACLs(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if n, ok := a.loadNetwork(w, r, p); ok {
		writeJSON(w, http.StatusOK, map[string]any{"acls": n.Policy.ACLs, "revision": n.PolicyRevision})
	}
}

func (a *ControlAPI) putACLs(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var in struct {
		ACLs []policy.ACLRule `json:"acls"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	a.policyResult(w)(a.svc.SetACLs(r.Context(), name, in.ACLs))
}

func (a *ControlAPI) getTags(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if n, ok := a.loadNetwork(w, r, p); ok {
		writeJSON(w, http.StatusOK, map[string]any{"tags": n.Policy.Tags, "revision": n.PolicyRevision})
	}
}

func (a *ControlAPI) putTag(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var def policy.TagDef
	if !decodeJSON(w, r, &def) {
		return
	}
	a.policyResult(w)(a.svc.PutTag(r.Context(), name, r.PathValue("tag"), def))
}

func (a *ControlAPI) deleteTag(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if name, ok := a.network(w, r, p); ok {
		a.policyResult(w)(a.svc.DeleteTag(r.Context(), name, r.PathValue("tag")))
	}
}

func (a *ControlAPI) getExitPolicies(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if n, ok := a.loadNetwork(w, r, p); ok {
		writeJSON(w, http.StatusOK, map[string]any{"exit": n.Policy.Exit, "revision": n.PolicyRevision})
	}
}

func (a *ControlAPI) putExitPolicy(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var rule policy.ExitRule
	if !decodeJSON(w, r, &rule) {
		return
	}
	rule.Name = r.PathValue("name")
	a.policyResult(w)(a.svc.PutExitRule(r.Context(), name, rule))
}

func (a *ControlAPI) deleteExitPolicy(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if name, ok := a.network(w, r, p); ok {
		a.policyResult(w)(a.svc.DeleteExitRule(r.Context(), name, r.PathValue("name")))
	}
}

// ---- enrolment ----

func (a *ControlAPI) listAuthKeys(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	ks, err := a.svc.ListAuthKeys(r.Context(), name)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := []AuthKeyView{}
	for _, k := range ks {
		out = append(out, a.authKeyView(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"auth_keys": out})
}

func (a *ControlAPI) createAuthKey(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var in struct {
		control.AuthKeyInput
		ExpiresInSeconds int64 `json:"expires_in_seconds"`
		PeerTTLSeconds   int64 `json:"peer_ttl_seconds"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	ki := in.AuthKeyInput
	ki.ExpiresIn = time.Duration(in.ExpiresInSeconds) * time.Second
	ki.PeerTTL = time.Duration(in.PeerTTLSeconds) * time.Second
	issued, err := a.svc.CreateAuthKey(r.Context(), name, ki)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	v := a.authKeyView(issued.AuthKey)
	v.Key = issued.Key
	writeJSON(w, http.StatusCreated, v)
}

func (a *ControlAPI) revokeAuthKey(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	k, err := a.svc.RevokeAuthKey(r.Context(), r.PathValue("id"))
	if err == nil && k.Network != name {
		err = control.ErrNotFound
	}
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.authKeyView(k))
}

func (a *ControlAPI) listEnrollments(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	es, err := a.svc.ListEnrollments(r.Context(), name, store.EnrollmentStatus(r.URL.Query().Get("status")))
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollments": es})
}

func (a *ControlAPI) enrollment(w http.ResponseWriter, r *http.Request, p control.Principal) (control.EnrollmentView, bool) {
	e, err := a.svc.Enrollment(r.Context(), r.PathValue("code"))
	if err != nil || !p.CanAccess(e.Network) {
		writeError(w, http.StatusNotFound, "not found")
		return e, false
	}
	return e, true
}

func (a *ControlAPI) getEnrollment(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if e, ok := a.enrollment(w, r, p); ok {
		writeJSON(w, http.StatusOK, e)
	}
}

func (a *ControlAPI) approveEnrollment(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if _, ok := a.enrollment(w, r, p); !ok {
		return
	}
	var in control.ApproveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	e, err := a.svc.ApproveEnrollment(r.Context(), r.PathValue("code"), in)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *ControlAPI) denyEnrollment(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if _, ok := a.enrollment(w, r, p); !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	e, err := a.svc.DenyEnrollment(r.Context(), r.PathValue("code"), in.Reason)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// ---- peers ----

func (a *ControlAPI) listPeers(w http.ResponseWriter, r *http.Request, p control.Principal) {
	q := r.URL.Query()
	network := q.Get("network")
	if network != "" && !p.CanAccess(network) {
		writeJSON(w, http.StatusOK, map[string]any{"peers": []PeerView{}})
		return
	}
	includeRevoked, _ := strconv.ParseBool(q.Get("include_revoked"))
	peers, err := a.svc.ListPeers(r.Context(), control.PeerQuery{
		Network: network, Tag: q.Get("tag"), Role: proto.Role(q.Get("role")), IncludeRevoked: includeRevoked,
	})
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := []PeerView{}
	for _, peer := range peers {
		if p.CanAccess(peer.Network) {
			out = append(out, a.peerView(peer))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": out})
}

func (a *ControlAPI) createPeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	var in struct {
		control.PeerInput
		TTLSeconds int64 `json:"ttl_seconds"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pi := in.PeerInput
	pi.TTL = time.Duration(in.TTLSeconds) * time.Second
	creds, err := a.svc.CreatePeer(r.Context(), name, pi)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, creds)
}

func (a *ControlAPI) getPeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if peer, ok := a.peer(w, r, p); ok {
		writeJSON(w, http.StatusOK, a.peerView(peer))
	}
}

// peerResult writes the outcome of a peer operation.
func (a *ControlAPI) peerResult(w http.ResponseWriter) func(store.Peer, error) {
	return func(peer store.Peer, err error) {
		if err != nil {
			writeServiceError(w, a.log, err)
			return
		}
		writeJSON(w, http.StatusOK, a.peerView(peer))
	}
}

func (a *ControlAPI) patchPeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	peer, ok := a.peer(w, r, p)
	if !ok {
		return
	}
	var patch control.PeerPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	a.peerResult(w)(a.svc.UpdatePeer(r.Context(), peer.ID, patch))
}

func (a *ControlAPI) deletePeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	peer, ok := a.peer(w, r, p)
	if !ok {
		return
	}
	if err := a.svc.DeletePeer(r.Context(), peer.ID); err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *ControlAPI) revokePeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if peer, ok := a.peer(w, r, p); ok {
		a.peerResult(w)(a.svc.RevokePeer(r.Context(), peer.ID))
	}
}

func (a *ControlAPI) expirePeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if peer, ok := a.peer(w, r, p); ok {
		a.peerResult(w)(a.svc.ExpirePeer(r.Context(), peer.ID))
	}
}

func (a *ControlAPI) movePeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	peer, ok := a.peer(w, r, p)
	if !ok {
		return
	}
	var in struct {
		Network string `json:"network"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !p.CanAccess(in.Network) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	a.peerResult(w)(a.svc.MovePeer(r.Context(), peer.ID, in.Network))
}

// reauthorizePeer re-asks the authorizer about the peer's live session and
// disconnects it on a denial. 409 in open mode.
func (a *ControlAPI) reauthorizePeer(w http.ResponseWriter, r *http.Request, p control.Principal) {
	peer, ok := a.peer(w, r, p)
	if !ok {
		return
	}
	res, err := a.svc.ReauthorizePeer(r.Context(), peer.ID)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// reauthorizeNetwork does the same for every live peer in the network.
func (a *ControlAPI) reauthorizeNetwork(w http.ResponseWriter, r *http.Request, p control.Principal) {
	name, ok := a.network(w, r, p)
	if !ok {
		return
	}
	res, err := a.svc.ReauthorizeNetwork(r.Context(), name)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"network": name, "peers": res})
}

// HealthView is one peer's health.
type HealthView struct {
	PeerID   string        `json:"peer_id"`
	Network  string        `json:"network"`
	Online   bool          `json:"online"`
	LastSeen *time.Time    `json:"last_seen,omitempty"`
	Health   *proto.Health `json:"health,omitempty"`
	HealthAt *time.Time    `json:"health_at,omitempty"`
}

func (a *ControlAPI) healthView(p store.Peer) HealthView {
	return HealthView{PeerID: p.ID, Network: p.Network, Online: a.relay.Online(p.ID), LastSeen: p.LastSeen,
		Health: p.Health, HealthAt: p.HealthAt}
}

func (a *ControlAPI) peerHealth(w http.ResponseWriter, r *http.Request, p control.Principal) {
	if peer, ok := a.peer(w, r, p); ok {
		writeJSON(w, http.StatusOK, a.healthView(peer))
	}
}

// FleetTotals summarises health across peers.
type FleetTotals struct {
	Peers           int            `json:"peers"`
	Online          int            `json:"online"`
	LinksConnected  int            `json:"links_connected"`
	LinksFailed     int            `json:"links_failed"`
	ByCandidateType map[string]int `json:"by_candidate_type"`
	ExitsPaused     int            `json:"exits_paused"`
	CapUsedBytes    int64          `json:"cap_used_bytes"`
}

func (a *ControlAPI) fleetHealth(w http.ResponseWriter, r *http.Request, p control.Principal) {
	network := r.URL.Query().Get("network")
	if network != "" && !p.CanAccess(network) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	peers, err := a.svc.ListPeers(r.Context(), control.PeerQuery{Network: network})
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	totals := FleetTotals{ByCandidateType: map[string]int{}}
	out := []HealthView{}
	for _, peer := range peers {
		if !p.CanAccess(peer.Network) {
			continue
		}
		v := a.healthView(peer)
		out = append(out, v)
		totals.Peers++
		if v.Online {
			totals.Online++
		}
		if h := peer.Health; h != nil {
			for _, l := range h.Links {
				switch l.State {
				case proto.LinkConnected:
					totals.LinksConnected++
					if l.CandidateType != "" {
						totals.ByCandidateType[l.CandidateType]++
					}
				case proto.LinkFailed:
					totals.LinksFailed++
				}
			}
			if h.Exit != nil {
				totals.CapUsedBytes += h.Exit.CapUsedBytes
				if h.Exit.Paused {
					totals.ExitsPaused++
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": out, "totals": totals})
}

func (a *ControlAPI) presence(w http.ResponseWriter, r *http.Request, p control.Principal) {
	network := r.URL.Query().Get("network")
	out := []signalling.Presence{}
	for _, s := range a.relay.Presence(network) {
		if p.CanAccess(s.Network) {
			out = append(out, s)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// Stats is the control API stats document.
type Stats struct {
	Networks     int                       `json:"networks"`
	Peers        int                       `json:"peers"`
	RevokedPeers int                       `json:"revoked_peers"`
	Online       int                       `json:"online"`
	OnlineByNet  map[string]map[string]int `json:"online_by_network"`
}

func (a *ControlAPI) stats(w http.ResponseWriter, r *http.Request, p control.Principal) {
	out := Stats{OnlineByNet: map[string]map[string]int{}}
	if p.Networks == nil {
		st, err := a.svc.Store().Stats(r.Context())
		if err != nil {
			writeServiceError(w, a.log, err)
			return
		}
		out.Networks, out.Peers, out.RevokedPeers = st.Networks, st.Peers, st.RevokedPeers
	}
	for _, s := range a.relay.Presence("") {
		if !s.Joined || !p.CanAccess(s.Network) {
			continue
		}
		out.Online++
		byRole := out.OnlineByNet[s.Network]
		if byRole == nil {
			byRole = map[string]int{}
			out.OnlineByNet[s.Network] = byRole
		}
		for _, role := range s.Roles {
			byRole[string(role)]++
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *ControlAPI) audit(w http.ResponseWriter, r *http.Request, p control.Principal) {
	q := r.URL.Query()
	network := q.Get("network")
	if p.Networks != nil && (network == "" || !p.CanAccess(network)) {
		writeError(w, http.StatusForbidden, "network-scoped keys must name one of their networks")
		return
	}
	f := store.AuditFilter{Network: network}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC 3339")
			return
		}
		f.Since = t
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be a number")
			return
		}
		f.Limit = n
	}
	evs, err := a.svc.ListAudit(r.Context(), f)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	if evs == nil {
		evs = []store.AuditEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// ---- API keys ----

func (a *ControlAPI) listAPIKeys(w http.ResponseWriter, r *http.Request, _ control.Principal) {
	ks, err := a.svc.ListAPIKeys(r.Context())
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := []APIKeyView{}
	for _, k := range ks {
		out = append(out, apiKeyView(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": out})
}

func (a *ControlAPI) createAPIKey(w http.ResponseWriter, r *http.Request, _ control.Principal) {
	var in struct {
		control.APIKeyInput
		ExpiresInSeconds int64 `json:"expires_in_seconds"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	ki := in.APIKeyInput
	ki.ExpiresIn = time.Duration(in.ExpiresInSeconds) * time.Second
	issued, err := a.svc.CreateAPIKey(r.Context(), ki)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	v := apiKeyView(issued.APIKey)
	v.Key = issued.Key
	writeJSON(w, http.StatusCreated, v)
}

func (a *ControlAPI) revokeAPIKey(w http.ResponseWriter, r *http.Request, _ control.Principal) {
	k, err := a.svc.RevokeAPIKey(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, apiKeyView(k))
}
