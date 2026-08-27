package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DevicePollRequest/DevicePollResult/DevicePoll mirror internal/api/poll.go's
// types of the same names exactly, so a "Poll BMC" submission from either
// the Sensors or Firmware tab runs the same in-process Redfish SEL+sensor
// poll the HTTP API's POST /v1/devices/{id}/poll uses -- not a second HTTP
// round trip. Kept as separate types here (rather than importing
// internal/api, which is owned by a sibling unit in this batch) to avoid a
// ui -> api package dependency; reconcile with the shell unit at merge time
// if a single shared type is preferred (e.g. by moving DevicePoll to a
// neutral package both import).
type DevicePollRequest struct {
	BMCEndpoint string
	BMCUsername string
	BMCPassword string
	SystemID    string
}

// DevicePollResult is the on-demand poll outcome. Password is never
// included (mirrors internal/api/poll.go's DevicePollResult).
type DevicePollResult struct {
	DeviceID        string
	SELNew          int
	SensorsWritten  int
	FirmwareWritten int
	PowerState      string
}

// DevicePoll runs one Redfish SEL+sensor poll into telemetry. Any type
// implementing internal/api's equivalent interface (e.g.
// internal/cli/poll.go's devicePoll) can be adapted to satisfy this one.
type DevicePoll interface {
	Poll(ctx context.Context, deviceID string, req DevicePollRequest) (DevicePollResult, error)
}

// pollTimeoutFloor mirrors the ~120s timeout floor documented in
// extras/netbox-plugin-shoal/netbox_shoal/client.go's poll_device: "iDRAC
// ListSEL/ListSensors can exceed the default 30s plugin timeout" so that
// client forces its HTTP timeout up to at least 120s. This in-process call
// has no HTTP client timeout to raise; instead this bounds worst-case
// handler hang time against an unresponsive BMC to the same floor.
const pollTimeoutFloor = 120 * time.Second

// handlePollForm processes the "Poll BMC" form shared by the Sensors and
// Firmware tabs (identical fields on both), then redirects back to the tab
// it was submitted from (POST/redirect/GET) with the outcome encoded in the
// query string for the GET handler to render as a banner.
func (s *Server) handlePollForm(w http.ResponseWriter, r *http.Request, tab string) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing device id", http.StatusBadRequest)
		return
	}
	backTo := "/ui/devices/" + url.PathEscape(id) + "/" + tab

	if s.Poll == nil {
		redirectWithPollResult(w, r, backTo, "", true, "Poll BMC is not configured on this server.")
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithPollResult(w, r, backTo, "", true, "Invalid form submission.")
		return
	}
	req := DevicePollRequest{
		BMCEndpoint: strings.TrimSpace(r.PostForm.Get("bmc_endpoint")),
		BMCUsername: strings.TrimSpace(r.PostForm.Get("bmc_username")),
		BMCPassword: r.PostForm.Get("bmc_password"),
		SystemID:    strings.TrimSpace(r.PostForm.Get("system_id")),
	}
	if req.BMCEndpoint == "" {
		redirectWithPollResult(w, r, backTo, req.BMCEndpoint, true, "BMC endpoint is required to poll.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), pollTimeoutFloor)
	defer cancel()
	res, err := s.Poll.Poll(ctx, id, req)
	if err != nil {
		s.logErr("ui device poll", err, "device_id", id)
		redirectWithPollResult(w, r, backTo, req.BMCEndpoint, true, "Poll BMC failed (see server logs).")
		return
	}
	msg := fmt.Sprintf("Polled BMC: %d sensor(s), %d firmware, power=%s, %d new SEL.",
		res.SensorsWritten, res.FirmwareWritten, orDash(res.PowerState), res.SELNew)
	redirectWithPollResult(w, r, backTo, req.BMCEndpoint, false, msg)
}

// redirectWithPollResult 303s back to path, carrying the poll outcome (and
// the submitted BMC endpoint, so the form re-populates it) in the query
// string. The password is deliberately never round-tripped this way.
func redirectWithPollResult(w http.ResponseWriter, r *http.Request, path, bmcEndpoint string, isErr bool, msg string) {
	q := url.Values{}
	if bmcEndpoint != "" {
		q.Set("bmc_endpoint", bmcEndpoint)
	}
	q.Set("poll_msg", msg)
	if isErr {
		q.Set("poll_err", "1")
	}
	http.Redirect(w, r, path+"?"+q.Encode(), http.StatusSeeOther)
}

// applyPollFeedback reads the poll outcome encoded by redirectWithPollResult
// back out of the GET request's query string.
func applyPollFeedback(msg *string, isErr *bool, q url.Values) {
	*msg = q.Get("poll_msg")
	*isErr = q.Get("poll_err") == "1"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
