package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/Imposter/ghost/ghost-server/server/control"
)

// Public serves the device-facing API:
//
//	GET  /healthz
//	POST /v1/register   self-registration (access action "register")
//	POST /v1/pair       redeem a pairing code (access action "pair")
//	GET  /v1/signal     signalling WebSocket (signal/proto v1)
type Public struct {
	svc    *control.Service
	signal http.Handler
	log    *slog.Logger
}

// NewPublic returns the public API. signal is the signalling WebSocket
// handler.
func NewPublic(svc *control.Service, signal http.Handler, log *slog.Logger) *Public {
	if log == nil {
		log = slog.Default()
	}
	return &Public{svc: svc, signal: signal, log: log}
}

// Register mounts the routes on mux.
func (p *Public) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/register", p.register)
	mux.HandleFunc("POST /v1/pair", p.pair)
	mux.Handle("GET /v1/signal", p.signal)
}

func (p *Public) register(w http.ResponseWriter, r *http.Request) {
	var in control.RegisterInput
	if !decodeJSON(w, r, &in) {
		return
	}
	creds, err := p.svc.Register(r.Context(), in)
	if err != nil {
		writeServiceError(w, p.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, creds)
}

func (p *Public) pair(w http.ResponseWriter, r *http.Request) {
	var in control.PairInput
	if !decodeJSON(w, r, &in) {
		return
	}
	creds, err := p.svc.Pair(r.Context(), in)
	if err != nil {
		writeServiceError(w, p.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, creds)
}
