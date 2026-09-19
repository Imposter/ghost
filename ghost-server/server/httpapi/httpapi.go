// Package httpapi serves ghost-server's HTTP surface: the public device API
// (register, pair, the signalling WebSocket) and the admin API protected by a
// bearer service token.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Imposter/ghost/ghost-server/server/control"
)

// errorBody is the JSON shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// writeServiceError maps control errors to HTTP statuses.
func writeServiceError(w http.ResponseWriter, log *slog.Logger, err error) {
	var denied *control.DeniedError
	switch {
	case errors.As(err, &denied):
		writeError(w, http.StatusForbidden, denied.Error())
	case errors.Is(err, control.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, control.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, control.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, control.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	default:
		log.Error("httpapi: internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// decodeJSON reads a bounded JSON body, rejecting unknown fields.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
