package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Imposter/ghost/ghost-server/server/control"
	"github.com/Imposter/ghost/ghost-server/server/httpapi"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// startInteractive begins an interactive enrolment and returns the status,
// the started enrolment and the error message, if any.
func (h *harness) startInteractive(network, name string) (int, control.StartedEnrollment, string) {
	h.t.Helper()
	var out struct {
		control.StartedEnrollment
		Error string `json:"error"`
	}
	status := h.do("POST", "/v1/enroll/interactive", "", map[string]any{
		"network": network, "name": name, "public_key": randomKey(h.t),
	}, &out)
	return status, out.StartedEnrollment, out.Error
}

func (h *harness) pollInteractive(token string) (int, control.PollResult, string) {
	h.t.Helper()
	var out struct {
		control.PollResult
		Error string `json:"error"`
	}
	status := h.do("POST", "/v1/enroll/poll", "", map[string]any{"poll_token": token}, &out)
	return status, out.PollResult, out.Error
}

func TestNetworkInteractiveEnrollmentSetting(t *testing.T) {
	h := newHarness(t, nil)

	tests := []struct {
		name string
		body map[string]any
		want bool
	}{
		{"default", map[string]any{"name": "dflt"}, true},
		{"on", map[string]any{"name": "on", "interactive_enrollment": true}, true},
		{"off", map[string]any{"name": "off", "interactive_enrollment": false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v httpapi.NetworkView
			h.ctl("POST", "/control/networks", tt.body, &v, http.StatusCreated)
			if v.InteractiveEnrollment != tt.want {
				t.Fatalf("create: interactive_enrollment=%v, want %v", v.InteractiveEnrollment, tt.want)
			}
			h.ctl("GET", "/control/networks/"+v.Name, nil, &v, http.StatusOK)
			if v.InteractiveEnrollment != tt.want {
				t.Fatalf("get: interactive_enrollment=%v, want %v", v.InteractiveEnrollment, tt.want)
			}
		})
	}

	// Networks from the configuration keep today's behaviour.
	var list struct {
		Networks []httpapi.NetworkView `json:"networks"`
	}
	h.ctl("GET", "/control/networks", nil, &list, http.StatusOK)
	got := map[string]bool{}
	for _, n := range list.Networks {
		got[n.Name] = n.InteractiveEnrollment
	}
	if !got["testnet"] || !got["dflt"] || got["off"] {
		t.Fatalf("list: %v", got)
	}

	// PATCH toggles the switch without bumping the policy revision, and still
	// changes isolation alongside it.
	var before, v httpapi.NetworkView
	h.ctl("GET", "/control/networks/testnet", nil, &before, http.StatusOK)
	h.ctl("PATCH", "/control/networks/testnet", map[string]any{"interactive_enrollment": false}, &v, http.StatusOK)
	if v.InteractiveEnrollment || v.PolicyRevision != before.PolicyRevision || v.Isolation != before.Isolation {
		t.Fatalf("disable: %+v (before %+v)", v, before)
	}
	h.ctl("PATCH", "/control/networks/testnet", map[string]any{"interactive_enrollment": true, "isolation": "hub-only"}, &v, http.StatusOK)
	if !v.InteractiveEnrollment || v.Isolation != "hub-only" || v.PolicyRevision != before.PolicyRevision+1 {
		t.Fatalf("enable with isolation: %+v", v)
	}
	h.ctl("PATCH", "/control/networks/testnet", map[string]any{}, nil, http.StatusBadRequest)
	h.ctl("PATCH", "/control/networks/testnet", map[string]any{"interactive_enrollment": "no"}, nil, http.StatusBadRequest)
	h.ctl("PATCH", "/control/networks/missing", map[string]any{"interactive_enrollment": false}, nil, http.StatusNotFound)

	if evs := h.audit("testnet", "network.interactive_enrollment"); len(evs) != 2 ||
		evs[0].Detail["enabled"] != false || evs[1].Detail["enabled"] != true {
		t.Fatalf("audit: %+v", evs)
	}
}

func TestInteractiveEnrollmentDisabled(t *testing.T) {
	h := newHarness(t, nil)
	h.defineTags("testnet", "tag:laptop")

	// Enrolments started while the switch is on.
	status, pending, _ := h.startInteractive("testnet", "pending")
	if status != http.StatusCreated {
		t.Fatalf("start pending: %d", status)
	}
	status, approved, _ := h.startInteractive("testnet", "approved")
	if status != http.StatusCreated {
		t.Fatalf("start approved: %d", status)
	}
	var view control.EnrollmentView
	h.ctl("POST", "/control/enrollments/"+approved.Code+"/approve",
		map[string]any{"roles": []string{"node"}, "tags": []string{"tag:laptop"}}, &view, http.StatusOK)
	if view.Status != store.EnrollmentApproved || len(view.Tags) != 1 {
		t.Fatalf("approve with tags: %+v", view)
	}

	h.ctl("PATCH", "/control/networks/testnet", map[string]any{"interactive_enrollment": false}, nil, http.StatusOK)

	// Starting is refused with a clear error.
	status, started, msg := h.startInteractive("testnet", "late")
	if status != http.StatusForbidden || started.Code != "" || !strings.Contains(msg, "interactive enrolment is disabled for network testnet") {
		t.Fatalf("start while disabled: %d %+v %q", status, started, msg)
	}
	// An unknown network is still a 404, not a 403.
	if status, _, _ := h.startInteractive("nosuchnet", "x"); status != http.StatusNotFound {
		t.Fatalf("unknown network: %d", status)
	}

	// Approving an already-pending code is refused and leaves it pending.
	var errBody struct {
		Error string `json:"error"`
	}
	if status := h.do("POST", "/control/enrollments/"+pending.Code+"/approve", serviceToken, map[string]any{}, &errBody); status != http.StatusForbidden ||
		!strings.Contains(errBody.Error, "interactive enrolment is disabled") {
		t.Fatalf("approve while disabled: %d %q", status, errBody.Error)
	}
	if status, res, _ := h.pollInteractive(pending.PollToken); status != http.StatusOK || res.Status != store.EnrollmentPending {
		t.Fatalf("pending poll while disabled: %d %+v", status, res)
	}

	// An approval granted before the switch went off is not claimed.
	status, res, msg := h.pollInteractive(approved.PollToken)
	if status != http.StatusForbidden || res.Credentials != nil || !strings.Contains(msg, "interactive enrolment is disabled") {
		t.Fatalf("claim while disabled: %d %+v %q", status, res, msg)
	}

	// Pre-auth key enrolment is unaffected.
	if creds, status := h.enroll(h.authKey("testnet", nil).Key, nil); status != http.StatusCreated || creds.PeerID == "" {
		t.Fatalf("pre-auth key while disabled: %d %+v", status, creds)
	}

	// Denying still works, so operators can clear the queue.
	h.ctl("POST", "/control/enrollments/"+pending.Code+"/deny", map[string]any{"reason": "closed"}, &view, http.StatusOK)
	if view.Status != store.EnrollmentDenied {
		t.Fatalf("deny while disabled: %+v", view)
	}

	// Other networks are unaffected.
	if status, _, _ := h.startInteractive("othernet", "other"); status != http.StatusCreated {
		t.Fatalf("othernet: %d", status)
	}

	// Switching back on lets the approved enrolment be claimed.
	h.ctl("PATCH", "/control/networks/testnet", map[string]any{"interactive_enrollment": true}, nil, http.StatusOK)
	status, res, _ = h.pollInteractive(approved.PollToken)
	if status != http.StatusOK || res.Status != store.EnrollmentClaimed || res.Credentials == nil || len(res.Credentials.Tags) != 1 {
		t.Fatalf("claim after re-enable: %d %+v", status, res)
	}
	if status, _, _ := h.startInteractive("testnet", "again"); status != http.StatusCreated {
		t.Fatalf("start after re-enable: %d", status)
	}
}
