package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type collectorSource struct{ *Collector }

func (s collectorSource) RecentConnections(limit int) []Connection { return s.Recent(limit) }

func newTestHandler(allow bool) http.Handler {
	c := NewCollector(CollectorConfig{RingSize: 4})
	for range 6 {
		c.Record(allowed("api.example", "", 1, 1))
	}
	prom := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# prometheus text\n"))
	})
	return NewHandler(HandlerConfig{
		Source:         collectorSource{c},
		Prometheus:     prom,
		Authorize:      func(*http.Request) bool { return allow },
		MaxConnections: c.RingSize(),
	})
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestHandlerRefusesUnauthorized(t *testing.T) {
	h := newTestHandler(false)
	for _, p := range []string{"/metrics", "/metrics?format=json", "/metrics/connections"} {
		if rec := get(h, p); rec.Code != http.StatusForbidden {
			t.Errorf("%s: code=%d want 403", p, rec.Code)
		}
	}
}

func TestHandlerRoutes(t *testing.T) {
	h := newTestHandler(true)
	if rec := get(h, "/metrics"); rec.Code != 200 || rec.Body.String() != "# prometheus text\n" {
		t.Errorf("/metrics: %d %q", rec.Code, rec.Body.String())
	}
	rec := get(h, "/metrics?format=json")
	var s Snapshot
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &s) != nil || s.Totals.Connections != 6 {
		t.Errorf("json: %d %s", rec.Code, rec.Body.String())
	}
	var cr ConnectionsResponse
	rec = get(h, "/metrics/connections?limit=2")
	if json.Unmarshal(rec.Body.Bytes(), &cr) != nil || len(cr.Connections) != 2 {
		t.Errorf("limit=2: %s", rec.Body.String())
	}
	rec = get(h, "/metrics/connections?limit=500")
	if json.Unmarshal(rec.Body.Bytes(), &cr) != nil || len(cr.Connections) != 4 {
		t.Errorf("limit capped to ring: got %d", len(cr.Connections))
	}
	if rec := get(h, "/metrics/connections?limit=-1"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit code=%d", rec.Code)
	}
	if rec := get(h, "/metrics?format=xml"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad format code=%d", rec.Code)
	}
}
