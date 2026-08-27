package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

// registerStatusRoutes wires the Status tab (this unit's whole deliverable)
// onto the shared /ui mux. The real "UI shell" PR should call this from its
// own routes() instead of relying on this package's placeholder Server/routes
// in server.go — see that file's package doc.
func (s *Server) registerStatusRoutes() {
	s.mux.HandleFunc("GET /ui/devices/{id}", s.handleDeviceStatusPage)
	s.mux.HandleFunc("POST /ui/devices/{id}", s.handleDeviceStatusPost)
}

// startDefaults prefills the Provision/Deprovision/Power forms, mirroring
// netbox_shoal/views.py's _start_defaults (bmc_endpoint from the stored
// BMC IP, serial_target/system_id from the device name, profile_ref
// defaulting to "spike").
type startDefaults struct {
	BMCEndpoint   string
	ProfileRef    string
	SerialTarget  string
	SystemID      string
	CredentialRef string
}

// statusPageData is the template data for templates/status.html.
type statusPageData struct {
	DeviceID string
	Device   *models.DeviceIdentity

	Status    *models.DeviceStatus
	StatusErr string

	Jobs      []models.Job
	JobsErr   string
	ActiveJob *models.Job

	LogLines []telemetry.JobLogLine
	LogErr   string

	Profiles    []profile.Record
	ProfilesErr string

	Creds    api.DeviceCredentialsView
	CredsErr string

	Provisioning bool
	AutoRefresh  bool

	Defaults startDefaults

	Flash      string
	FlashLevel string
}

func (s *Server) handleDeviceStatusPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		http.Error(w, "missing device id", http.StatusBadRequest)
		return
	}
	data := s.buildStatusPage(r.Context(), id)
	data.Flash = r.URL.Query().Get("msg")
	data.FlashLevel = r.URL.Query().Get("level")
	s.renderPage(w, r, "status.html", data)
}

func (s *Server) buildStatusPage(ctx context.Context, id string) statusPageData {
	data := statusPageData{DeviceID: id}

	var dev models.DeviceIdentity
	if s.Directory != nil {
		if d, err := s.Directory.GetDevice(ctx, id); err == nil {
			dev = d
			data.Device = &d
		}
		// A lookup error (including "not found") is not fatal: an operator
		// may be starting a job for a device Shoal hasn't seen via the
		// directory backend yet, matching the NetBox plugin's "no status yet"
		// empty state rather than a hard failure.
	}

	if s.Observe != nil {
		if st, err := s.Observe.Status(ctx, id); err != nil {
			data.StatusErr = err.Error()
		} else {
			data.Status = &st
		}
	}

	if s.Jobs != nil {
		if jobs, err := s.Jobs.ListByDevice(ctx, id, "", 5); err != nil {
			data.JobsErr = err.Error()
		} else {
			data.Jobs = jobs
		}
	}
	data.ActiveJob = activeOrLatestJob(data.Jobs)

	if data.ActiveJob != nil && data.ActiveJob.ID != "" && s.Observe != nil {
		if lines, err := s.Observe.ListJobLog(ctx, data.ActiveJob.ID, time.Time{}, 40); err != nil {
			data.LogErr = err.Error()
		} else {
			data.LogLines = lines
		}
	}

	if s.Profiles != nil {
		if list, err := s.Profiles.List(ctx); err != nil {
			data.ProfilesErr = err.Error()
		} else {
			data.Profiles = list
		}
	}

	if s.Credentials != nil {
		if view, err := s.Credentials.Get(ctx, id, dev.CredentialRef); err != nil {
			data.CredsErr = err.Error()
		} else {
			data.Creds = view
		}
	}

	data.Provisioning = data.Status != nil &&
		(data.Status.LifecycleState == models.StateProvisioning || data.Status.ActiveJobID != "")
	for _, j := range data.Jobs {
		if j.State == models.StateProvisioning {
			data.Provisioning = true
		}
	}
	data.AutoRefresh = data.Provisioning

	bmcEndpoint := endpointFromBMCIP(data.Creds.BMCIP)
	if bmcEndpoint == "" {
		bmcEndpoint = endpointFromBMCIP(dev.BMCIP)
	}
	profileRef := "spike"
	data.Defaults = startDefaults{
		BMCEndpoint:   bmcEndpoint,
		ProfileRef:    profileRef,
		SerialTarget:  dev.Name,
		SystemID:      dev.Name,
		CredentialRef: dev.CredentialRef,
	}
	return data
}

// activeOrLatestJob prefers a provisioning job; else the newest (Jobs.ListByDevice
// returns newest-updated first), mirroring netbox_shoal/views.py's
// _active_or_latest_job.
func activeOrLatestJob(jobs []models.Job) *models.Job {
	if len(jobs) == 0 {
		return nil
	}
	for i := range jobs {
		if jobs[i].State == models.StateProvisioning {
			return &jobs[i]
		}
	}
	return &jobs[0]
}

// endpointFromBMCIP mirrors internal/api/power.go's unexported helper of the
// same name (bare host/IP -> https://, scheme already present -> unchanged).
func endpointFromBMCIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	if strings.HasPrefix(ip, "http://") || strings.HasPrefix(ip, "https://") {
		return ip
	}
	return "https://" + ip
}

func (s *Server) redirectFlash(w http.ResponseWriter, r *http.Request, id, level, msg string) {
	v := url.Values{}
	v.Set("msg", msg)
	v.Set("level", level)
	http.Redirect(w, r, "/ui/devices/"+url.PathEscape(id)+"?"+v.Encode(), http.StatusSeeOther)
}

// redirectUpstreamError logs the full err server-side and redirects with a
// generic flash message, mirroring internal/api/errors.go's
// writeUpstreamError: upstream error text (BMC responses, secrets-backend or
// NetBox failures, ...) must never reach the browser verbatim, since it can
// carry raw response bodies. extra, when non-empty, is safe local context
// (e.g. a job id this handler itself generated) appended to the message.
func (s *Server) redirectUpstreamError(w http.ResponseWriter, r *http.Request, id, action string, err error, extra string) {
	if s.Log != nil {
		s.Log.Error("ui upstream error", "action", action, "device_id", id, "err", err.Error())
	}
	msg := action + " failed: upstream request failed"
	if extra != "" {
		msg += " (" + extra + ")"
	}
	s.redirectFlash(w, r, id, "error", msg)
}

func (s *Server) handleDeviceStatusPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		http.Error(w, "missing device id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.redirectFlash(w, r, id, "error", "invalid form")
		return
	}
	switch strings.TrimSpace(r.PostForm.Get("action")) {
	case "start":
		s.handleStartAction(w, r, id)
	case "cancel":
		s.handleCancelAction(w, r, id)
	case "deprovision":
		s.handleDeprovisionAction(w, r, id)
	case "credentials":
		s.handleCredentialsAction(w, r, id)
	case "power":
		s.handlePowerAction(w, r, id)
	default:
		s.redirectFlash(w, r, id, "error", "unknown action")
	}
}

// deviceFor is a best-effort directory lookup used by the write actions below
// to fill defaults (bmc_endpoint, serial_target, credential_ref) exactly like
// the read side, without failing the whole action when the directory is
// unavailable or the device is unknown to it.
func (s *Server) deviceFor(ctx context.Context, id string) models.DeviceIdentity {
	if s.Directory == nil {
		return models.DeviceIdentity{}
	}
	dev, err := s.Directory.GetDevice(ctx, id)
	if err != nil {
		return models.DeviceIdentity{}
	}
	return dev
}

// startJobBoundaryProbe mirrors internal/api/jobs.go's unexported helper of
// the same name: it patches a copy of req (never the request actually sent
// to JobStarter.StartAsync) so job.StartJobRequest's structural validation
// doesn't reject a request solely because a fill Orchestrator.prepareStart
// still performs (credential_ref from the directory, serial_transport
// auto-detected for an https bmc_endpoint) hasn't happened yet.
func startJobBoundaryProbe(req models.StartJobRequest) models.StartJobRequest {
	probe := req
	if strings.TrimSpace(probe.CredentialRef) == "" &&
		strings.TrimSpace(probe.BMCUsername) == "" &&
		strings.TrimSpace(probe.BMCPassword) == "" {
		probe.CredentialRef = "boundary-probe-placeholder"
	}
	if strings.TrimSpace(probe.SerialTransport) == "" && job.LooksLikeHTTPSBMC(probe.BMCEndpoint) {
		probe.SerialTransport = "redfish_sol"
	}
	return probe
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *Server) handleStartAction(w http.ResponseWriter, r *http.Request, id string) {
	if s.JobStarter == nil {
		s.redirectFlash(w, r, id, "error", "job start not configured")
		return
	}
	form := r.PostForm
	dev := s.deviceFor(r.Context(), id)

	req := models.StartJobRequest{
		DeviceID:     id,
		BMCEndpoint:  firstNonEmpty(strings.TrimSpace(form.Get("bmc_endpoint")), endpointFromBMCIP(dev.BMCIP)),
		ISOURL:       strings.TrimSpace(form.Get("iso_url")),
		ProfileRef:   strings.TrimSpace(form.Get("profile_ref")),
		SerialTarget: firstNonEmpty(strings.TrimSpace(form.Get("serial_target")), dev.Name),
		BMCUsername:  strings.TrimSpace(form.Get("bmc_username")),
		BMCPassword:  form.Get("bmc_password"),
	}
	if v := strings.TrimSpace(form.Get("system_id")); v != "" {
		req.SystemID = v
	} else {
		req.SystemID = dev.Name
	}
	if req.BMCUsername == "" && req.BMCPassword == "" && dev.CredentialRef != "" {
		req.CredentialRef = dev.CredentialRef
	}
	if form.Get("approve_destruct") == "on" {
		req.ApproveDestruct = true
	}

	if err := job.StartJobRequest(startJobBoundaryProbe(req)); err != nil {
		s.redirectFlash(w, r, id, "error", err.Error())
		return
	}

	// Detached like handleStartJob: BMC bring-up continues after this request
	// returns; a client giving up must not cancel work already touching the BMC.
	j, err := s.JobStarter.StartAsync(context.WithoutCancel(r.Context()), req)
	if err != nil {
		extra := ""
		if j.ID != "" {
			extra = "job " + short(j.ID) + " was created"
		}
		s.redirectUpstreamError(w, r, id, "start", err, extra)
		return
	}
	s.redirectFlash(w, r, id, "success", fmt.Sprintf("Provisioning job %s started (state=%s)", short(j.ID), j.State))
}

func (s *Server) handleCancelAction(w http.ResponseWriter, r *http.Request, id string) {
	if s.JobCanceler == nil {
		s.redirectFlash(w, r, id, "error", "job cancel not configured")
		return
	}
	jobID := strings.TrimSpace(r.PostForm.Get("job_id"))
	if jobID == "" {
		s.redirectFlash(w, r, id, "error", "missing job id to cancel")
		return
	}
	if err := s.JobCanceler.Cancel(r.Context(), jobID); err != nil {
		if errors.Is(err, jobstore.ErrNotFound) {
			s.redirectFlash(w, r, id, "error", "job not found")
			return
		}
		s.redirectUpstreamError(w, r, id, "cancel", err, "")
		return
	}
	s.redirectFlash(w, r, id, "success", fmt.Sprintf("Cancel requested for job %s", short(jobID)))
}

func (s *Server) handleDeprovisionAction(w http.ResponseWriter, r *http.Request, id string) {
	if s.JobStarter == nil {
		s.redirectFlash(w, r, id, "error", "job start not configured")
		return
	}
	form := r.PostForm
	dev := s.deviceFor(r.Context(), id)

	wipeLevel := strings.TrimSpace(form.Get("wipe_level"))
	// Server-side confirmation gate (documented choice, see status.html): the
	// form also carries an onclick confirm() dialog, but that only guards
	// against a stray click in a real browser -- it is not trusted as the
	// safety check. A checkbox posted without "on" (JS disabled, scripted
	// POST, tampered client) must still be rejected here.
	approve := form.Get("approve_destruct") == "on"
	if wipeLevel == "" {
		s.redirectFlash(w, r, id, "error", "wipe level (discard or zero) is required to deprovision")
		return
	}
	if !approve {
		s.redirectFlash(w, r, id, "error", "confirmation is required: deprovision permanently wipes the boot disk")
		return
	}

	req := models.StartJobRequest{
		DeviceID:        id,
		Kind:            models.JobKindDeprovision,
		Prep:            "wipe_only",
		WipeLevel:       wipeLevel,
		BMCEndpoint:     firstNonEmpty(strings.TrimSpace(form.Get("bmc_endpoint")), endpointFromBMCIP(dev.BMCIP)),
		SerialTarget:    firstNonEmpty(strings.TrimSpace(form.Get("serial_target")), dev.Name),
		BMCUsername:     strings.TrimSpace(form.Get("bmc_username")),
		BMCPassword:     form.Get("bmc_password"),
		ApproveDestruct: approve,
	}
	if v := strings.TrimSpace(form.Get("system_id")); v != "" {
		req.SystemID = v
	} else {
		req.SystemID = dev.Name
	}
	if req.BMCUsername == "" && req.BMCPassword == "" && dev.CredentialRef != "" {
		req.CredentialRef = dev.CredentialRef
	}

	if err := job.StartJobRequest(startJobBoundaryProbe(req)); err != nil {
		s.redirectFlash(w, r, id, "error", err.Error())
		return
	}

	j, err := s.JobStarter.StartAsync(context.WithoutCancel(r.Context()), req)
	if err != nil {
		extra := ""
		if j.ID != "" {
			extra = "job " + short(j.ID) + " was created"
		}
		s.redirectUpstreamError(w, r, id, "deprovision", err, extra)
		return
	}
	s.redirectFlash(w, r, id, "success", fmt.Sprintf("Deprovision job %s started (state=%s)", short(j.ID), j.State))
}

func (s *Server) handleCredentialsAction(w http.ResponseWriter, r *http.Request, id string) {
	if s.Credentials == nil {
		s.redirectFlash(w, r, id, "error", "credentials store not configured")
		return
	}
	form := r.PostForm
	username := strings.TrimSpace(form.Get("bmc_username"))
	password := form.Get("bmc_password") // blank = keep existing, per handleDeviceCredentialsPut/Put contract
	if username == "" {
		s.redirectFlash(w, r, id, "error", "username is required")
		return
	}
	req := api.DeviceCredentialsPut{
		Username: username,
		Password: password,
		BMCIP:    strings.TrimSpace(form.Get("bmc_ip")),
	}
	view, err := s.Credentials.Put(r.Context(), id, req)
	if err != nil {
		// Exact-match against the same handful of client-facing messages
		// handleDeviceCredentialsPut itself crafts (internal/api/credentials.go).
		// Substring matching would be unsafe: a wrapped upstream error could
		// contain "is required" or "not configured" as raw response body text.
		msg := err.Error()
		switch msg {
		case "username is required", "password is required for new credentials",
			"secrets backend not configured (set SHOAL_SECRETS_DIR)":
			s.redirectFlash(w, r, id, "error", msg)
			return
		}
		if strings.Contains(msg, "not found") || strings.Contains(msg, "status 404") {
			s.redirectFlash(w, r, id, "error", "not found")
			return
		}
		s.redirectUpstreamError(w, r, id, "save credentials", err, "")
		return
	}
	s.redirectFlash(w, r, id, "success", fmt.Sprintf("BMC credentials saved (ref=%s)", view.CredentialRef))
}

func (s *Server) handlePowerAction(w http.ResponseWriter, r *http.Request, id string) {
	if s.Power == nil {
		s.redirectFlash(w, r, id, "error", "device power not configured")
		return
	}
	form := r.PostForm
	resetType := strings.TrimSpace(form.Get("reset_type"))
	bmcEndpoint := strings.TrimSpace(form.Get("bmc_endpoint"))
	username := strings.TrimSpace(form.Get("bmc_username"))
	password := form.Get("bmc_password")

	// Mirrors handleDevicePower's resolution order: request fields, then the
	// stored per-device secret, then orchestrator-wide defaults.
	if (username == "" || password == "") && s.Credentials != nil {
		if u, p, err := s.Credentials.Resolve(r.Context(), id); err == nil {
			if username == "" {
				username = u
			}
			if password == "" {
				password = p
			}
		}
	}
	if username == "" {
		username = s.DefaultBMCUsername
	}
	if password == "" {
		password = s.DefaultBMCPassword
	}
	if bmcEndpoint == "" && s.Credentials != nil {
		if view, err := s.Credentials.Get(r.Context(), id, ""); err == nil {
			bmcEndpoint = endpointFromBMCIP(view.BMCIP)
		}
	}
	if err := api.ValidateDevicePower(resetType, bmcEndpoint); err != nil {
		s.redirectFlash(w, r, id, "error", err.Error())
		return
	}
	req := api.DevicePowerRequest{
		ResetType:   resetType,
		BMCEndpoint: bmcEndpoint,
		BMCUsername: username,
		BMCPassword: password,
		SystemID:    strings.TrimSpace(form.Get("system_id")),
	}
	out, err := s.Power.Power(r.Context(), id, req)
	if err != nil {
		s.redirectUpstreamError(w, r, id, "power "+resetType, err, "")
		return
	}
	s.redirectFlash(w, r, id, "success", fmt.Sprintf("Power %s sent. power_state=%s", resetType, firstNonEmpty(out.PowerState, "?")))
}

func short(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}
