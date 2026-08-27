package ui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/api"
)

// logErr logs a handler-side failure with s.log (falling back to
// slog.Default if unset), the same pattern status.go's
// redirectUpstreamError uses -- upstream error detail is logged
// server-side only, never echoed back into a response.
func (s *Server) logErr(msg string, err error, args ...any) {
	log := s.log
	if log == nil {
		log = slog.Default()
	}
	all := make([]any, 0, len(args)+2)
	all = append(all, "err", err.Error())
	all = append(all, args...)
	log.Error(msg, all...)
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
	if !s.verifyCSRF(r) {
		redirectWithPollResult(w, r, backTo, "", true, "Your session expired; please retry.")
		return
	}
	req := api.DevicePollRequest{
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
