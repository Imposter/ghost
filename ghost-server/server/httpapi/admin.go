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
	"github.com/Imposter/ghost/ghost-server/server/signalling"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// DeviceMetricsProxy serves a device's node metrics, reached over the tunnel
// through a hub. It is the plug-in point for the node /metrics work (A3b);
// subpath is "" for GET /admin/devices/{id}/metrics and "connections" for
// GET /admin/devices/{id}/metrics/connections.
type DeviceMetricsProxy interface {
	ServeDeviceMetrics(w http.ResponseWriter, r *http.Request, device store.Device, subpath string)
}

// Admin serves the admin API. Every route requires
// "Authorization: Bearer <service token>".
type Admin struct {
	svc     *control.Service
	relay   *signalling.Relay
	token   []byte
	metrics DeviceMetricsProxy
	log     *slog.Logger
}

// AdminOptions configures the admin API.
type AdminOptions struct {
	Service *control.Service
	Relay   *signalling.Relay
	// Token is the bearer service token (required).
	Token string
	// Metrics is optional; without it the device metrics routes answer 501.
	Metrics DeviceMetricsProxy
	Logger  *slog.Logger
}

// NewAdmin returns the admin API.
func NewAdmin(opts AdminOptions) *Admin {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Admin{svc: opts.Service, relay: opts.Relay, token: []byte(opts.Token), metrics: opts.Metrics, log: opts.Logger}
}

// Register mounts the admin routes on mux.
func (a *Admin) Register(mux *http.ServeMux) {
	h := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, a.auth(fn)) }
	h("GET /admin/networks", a.listNetworks)
	h("POST /admin/networks", a.createNetwork)
	h("GET /admin/networks/{name}", a.getNetwork)
	h("PUT /admin/networks/{name}/policy", a.setPolicy)
	h("GET /admin/devices", a.listDevices)
	h("GET /admin/devices/{id}", a.getDevice)
	h("POST /admin/devices/{id}/revoke", a.revokeDevice)
	h("POST /admin/devices/{id}/move", a.moveDevice)
	h("GET /admin/devices/{id}/metrics", a.deviceMetrics(""))
	h("GET /admin/devices/{id}/metrics/connections", a.deviceMetrics("connections"))
	h("POST /admin/pairing-codes", a.createPairingCode)
	h("GET /admin/presence", a.presence)
	h("GET /admin/stats", a.stats)
}

func (a *Admin) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(a.token) == 0 || subtle.ConstantTimeCompare([]byte(got), a.token) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-admin"`)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- views ----

// NetworkView is the admin JSON form of a network.
type NetworkView struct {
	Name      string           `json:"name"`
	Pool      string           `json:"pool"`
	Policy    proto.ExitPolicy `json:"policy"`
	CreatedAt time.Time        `json:"created_at"`
	Online    int              `json:"online"`
}

// DeviceView is the admin JSON form of a device. The token hash is never
// exposed.
type DeviceView struct {
	ID        string               `json:"id"`
	Network   string               `json:"network"`
	Role      proto.Role           `json:"role"`
	Name      string               `json:"name"`
	Labels    map[string]string    `json:"labels"`
	PublicKey string               `json:"public_key,omitempty"`
	Address   string               `json:"address,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	LastSeen  *time.Time           `json:"last_seen,omitempty"`
	RevokedAt *time.Time           `json:"revoked_at,omitempty"`
	Online    bool                 `json:"online"`
	Session   *signalling.Presence `json:"session,omitempty"`
}

func (a *Admin) deviceView(d store.Device) DeviceView {
	v := DeviceView{
		ID: d.ID, Network: d.Network, Role: d.Role, Name: d.Name, Labels: d.Labels,
		PublicKey: d.PublicKey, Address: d.Address, CreatedAt: d.CreatedAt,
		LastSeen: d.LastSeen, RevokedAt: d.RevokedAt,
	}
	if p, ok := a.relay.Online(d.ID); ok {
		v.Online, v.Session = true, &p
	}
	return v
}

func (a *Admin) networkView(n store.Network) NetworkView {
	return NetworkView{Name: n.Name, Pool: n.Pool, Policy: n.Policy, CreatedAt: n.CreatedAt, Online: len(a.relay.Presence(n.Name))}
}

// ---- networks ----

func (a *Admin) listNetworks(w http.ResponseWriter, r *http.Request) {
	ns, err := a.svc.Store().ListNetworks(r.Context())
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := make([]NetworkView, 0, len(ns))
	for _, n := range ns {
		out = append(out, a.networkView(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": out})
}

func (a *Admin) createNetwork(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Pool string `json:"pool"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := a.svc.CreateNetwork(r.Context(), in.Name, in.Pool)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, a.networkView(n))
}

func (a *Admin) getNetwork(w http.ResponseWriter, r *http.Request) {
	n, err := a.svc.Network(r.Context(), r.PathValue("name"))
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.networkView(n))
}

func (a *Admin) setPolicy(w http.ResponseWriter, r *http.Request) {
	var in control.PolicyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p, pushed, err := a.svc.SetNetworkPolicy(r.Context(), r.PathValue("name"), in)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": p, "pushed": pushed})
}

// ---- devices ----

func (a *Admin) listDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeRevoked, _ := strconv.ParseBool(q.Get("include_revoked"))
	ds, err := a.svc.Store().ListDevices(r.Context(), store.DeviceFilter{Network: q.Get("network"), IncludeRevoked: includeRevoked})
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := make([]DeviceView, 0, len(ds))
	for _, d := range ds {
		out = append(out, a.deviceView(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (a *Admin) getDevice(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.Device(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.deviceView(d))
}

func (a *Admin) revokeDevice(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.Revoke(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.deviceView(d))
}

func (a *Admin) moveDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Network string `json:"network"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	d, err := a.svc.Move(r.Context(), r.PathValue("id"), in.Network)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a.deviceView(d))
}

func (a *Admin) deviceMetrics(subpath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := a.svc.Device(r.Context(), r.PathValue("id"))
		if err != nil {
			writeServiceError(w, a.log, err)
			return
		}
		if a.metrics == nil {
			writeError(w, http.StatusNotImplemented, "device metrics proxy is not configured")
			return
		}
		a.metrics.ServeDeviceMetrics(w, r, d, subpath)
	}
}

// ---- pairing ----

func (a *Admin) createPairingCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		control.PairingInput
		TTLSeconds int64 `json:"ttl_seconds"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pi := in.PairingInput
	pi.TTL = time.Duration(in.TTLSeconds) * time.Second
	code, err := a.svc.CreatePairingCode(r.Context(), pi)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, code)
}

// ---- presence and stats ----

func (a *Admin) presence(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": a.relay.Presence(r.URL.Query().Get("network"))})
}

// Stats is the admin stats document.
type Stats struct {
	Networks       int                       `json:"networks"`
	Devices        int                       `json:"devices"`
	RevokedDevices int                       `json:"revoked_devices"`
	Online         int                       `json:"online"`
	OnlineByNet    map[string]map[string]int `json:"online_by_network"`
}

func (a *Admin) stats(w http.ResponseWriter, r *http.Request) {
	st, err := a.svc.Store().Stats(r.Context())
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	out := Stats{Networks: st.Networks, Devices: st.Devices, RevokedDevices: st.RevokedDevices, OnlineByNet: map[string]map[string]int{}}
	for _, p := range a.relay.Presence("") {
		out.Online++
		if !p.Joined {
			continue
		}
		byRole := out.OnlineByNet[p.Network]
		if byRole == nil {
			byRole = map[string]int{}
			out.OnlineByNet[p.Network] = byRole
		}
		byRole[string(p.Role)]++
	}
	writeJSON(w, http.StatusOK, out)
}
