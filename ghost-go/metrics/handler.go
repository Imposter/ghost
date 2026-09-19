package metrics

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Source provides the data a Handler serves. ghost.Node implements it.
type Source interface {
	// Snapshot returns the current metrics snapshot.
	Snapshot() Snapshot
	// RecentConnections returns up to limit recent connections, newest first.
	RecentConnections(limit int) []Connection
}

// Authorizer decides whether a request may read metrics. It sees the request
// as accepted on the tunnel listener, so r.RemoteAddr is the caller's tunnel
// address.
type Authorizer func(r *http.Request) bool

// HandlerConfig configures NewHandler.
type HandlerConfig struct {
	// Source supplies the JSON snapshot and the ring buffer. Required.
	Source Source
	// Prometheus serves the Prometheus text exposition for GET /metrics, e.g.
	// otelsetup.Setup.PrometheusHandler. When nil, the text format answers
	// 404 and only ?format=json is available.
	Prometheus http.Handler
	// Authorize gates every request; a refused request gets 403. When nil,
	// every request is refused.
	Authorize Authorizer
	// MaxConnections caps the limit a caller may ask for (default: no cap
	// beyond what the ring holds).
	MaxConnections int
}

// NewHandler returns the node metrics HTTP handler. Serve it only on a
// listener bound to the node's tunnel IP.
func NewHandler(cfg HandlerConfig) http.Handler {
	h := &handler{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathMetrics, h.metrics)
	mux.HandleFunc("GET "+PathConnections, h.connections)
	return h.guard(mux)
}

type handler struct {
	cfg HandlerConfig
}

func (h *handler) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.Authorize == nil || !h.cfg.Authorize(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) metrics(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("format") {
	case "json":
		writeJSON(w, h.cfg.Source.Snapshot())
	case "", "prometheus", "text":
		if h.cfg.Prometheus == nil {
			http.Error(w, "prometheus exporter not configured", http.StatusNotFound)
			return
		}
		h.cfg.Prometheus.ServeHTTP(w, r)
	default:
		http.Error(w, "unknown format", http.StatusBadRequest)
	}
}

func (h *handler) connections(w http.ResponseWriter, r *http.Request) {
	limit := DefaultConnections
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
			return
		}
		limit = n
	}
	if h.cfg.MaxConnections > 0 && limit > h.cfg.MaxConnections {
		limit = h.cfg.MaxConnections
	}
	conns := h.cfg.Source.RecentConnections(limit)
	if conns == nil {
		conns = []Connection{}
	}
	writeJSON(w, ConnectionsResponse{Connections: conns})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
