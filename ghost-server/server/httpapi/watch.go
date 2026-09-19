package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/events"
)

// watchKeepalive is the interval between SSE comment lines on an idle stream.
const watchKeepalive = 15 * time.Second

// watch streams bus events as Server-Sent Events:
//
//	GET /control/watch?network=a,b&types=peer.,audit&since=42
//
// Each event is "id: <seq>\nevent: <type>\ndata: <json>\n\n". A client
// resumes after a disconnect with ?since=<last id> or the Last-Event-ID
// header; events still in the server's ring buffer are replayed. A client too
// slow to keep up is disconnected and should resume the same way.
func (a *ControlAPI) watch(w http.ResponseWriter, r *http.Request, p control.Principal) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	q := r.URL.Query()
	f := events.Filter{Types: splitCSV(q.Get("types"))}
	if nets := splitCSV(q.Get("network")); nets != nil {
		for _, n := range nets {
			if !p.CanAccess(n) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
		}
		f.Networks = nets
	} else if p.Networks != nil {
		f.Networks = slices.Clone(p.Networks)
	}
	// Keys confined to networks never see network-less (global) events.
	f.Strict = p.Networks != nil

	since := uint64(0)
	if v := q.Get("since"); v != "" {
		since, _ = strconv.ParseUint(v, 10, 64)
	} else if v := r.Header.Get("Last-Event-ID"); v != "" {
		since, _ = strconv.ParseUint(v, 10, 64)
	}

	sub := a.svc.Bus().Subscribe(f, since, 512)
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// An initial comment tells the client the stream is open.
	fmt.Fprintf(w, ": watching from seq %d\n\n", a.svc.Bus().Seq())
	flusher.Flush()

	keepalive := time.NewTicker(watchKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e, open := <-sub.C:
			if !open {
				return // dropped for being slow; the client resumes from its last id
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
