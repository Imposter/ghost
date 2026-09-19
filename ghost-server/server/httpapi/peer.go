package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/Imposter/ghost/ghost-server/server/control"
)

// PeerAPI serves the peer-facing API:
//
//	GET  /healthz
//	POST /v1/enroll               enrol with a pre-auth key
//	POST /v1/enroll/interactive   start an interactive enrolment (code + poll token)
//	POST /v1/enroll/poll          poll an interactive enrolment; credentials once approved
//	POST /v1/peer/rotate          rotate the peer token (and WireGuard key); Bearer peer token
//	GET  /v1/signal               signalling WebSocket (signal/proto v1)
type PeerAPI struct {
	svc    *control.Service
	signal http.Handler
	log    *slog.Logger
}

// NewPeerAPI returns the peer API. signal is the signalling WebSocket
// handler.
func NewPeerAPI(svc *control.Service, signal http.Handler, log *slog.Logger) *PeerAPI {
	if log == nil {
		log = slog.Default()
	}
	return &PeerAPI{svc: svc, signal: signal, log: log}
}

// Register mounts the routes on mux.
func (a *PeerAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/enroll", a.enroll)
	mux.HandleFunc("POST /v1/enroll/interactive", a.startEnrollment)
	mux.HandleFunc("POST /v1/enroll/poll", a.pollEnrollment)
	mux.HandleFunc("POST /v1/peer/rotate", a.rotate)
	mux.Handle("GET /v1/signal", a.signal)
}

func (a *PeerAPI) enroll(w http.ResponseWriter, r *http.Request) {
	var in control.EnrollInput
	if !decodeJSON(w, r, &in) {
		return
	}
	creds, err := a.svc.Enroll(control.WithActor(r.Context(), "enrollment"), in)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, creds)
}

func (a *PeerAPI) startEnrollment(w http.ResponseWriter, r *http.Request) {
	var in control.StartEnrollmentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	started, err := a.svc.StartEnrollment(control.WithActor(r.Context(), "enrollment"), in)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, started)
}

func (a *PeerAPI) pollEnrollment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PollToken string `json:"poll_token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := a.svc.PollEnrollment(control.WithActor(r.Context(), "enrollment"), in.PollToken)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *PeerAPI) rotate(w http.ResponseWriter, r *http.Request) {
	token, ok := bearer(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "peer token required")
		return
	}
	peer, err := a.svc.Authenticate(r.Context(), token)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	var in struct {
		PublicKey string `json:"public_key"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	rot, err := a.svc.RotateCredentials(control.WithActor(r.Context(), "peer:"+peer.ID), peer, in.PublicKey)
	if err != nil {
		writeServiceError(w, a.log, err)
		return
	}
	writeJSON(w, http.StatusOK, rot)
}
