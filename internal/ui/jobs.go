package ui

import (
	"errors"
	"net/http"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

// jobLogLimit is how many job_log lines the job-log panel fetches, matching
// internal/api/jobs.go's handleJobLog default-ish size used by the NetBox
// plugin's jobs tab (views.py's ShoalJobsView uses limit=80).
const jobLogLimit = 80

// jobsPageData is the data passed to templates/jobs.html.
type jobsPageData struct {
	DeviceID string
	Jobs     []models.Job
	// Error is a client-safe message only (never a raw upstream error).
	Error string

	ActiveJob   *models.Job
	JobLog      []telemetry.JobLogLine
	JobLogError string

	// CancelError is set (from the ?cancel_error= redirect param) when the
	// most recent cancel POST failed, so the operator sees why the job is
	// still shown as provisioning instead of silently landing back on the
	// same page. Never the raw error text (same rule as Error/JobLogError).
	CancelError string

	// AutoRefresh mirrors the NetBox plugin's shoal_auto_refresh: true when
	// any listed job is still provisioning.
	AutoRefresh bool
	// CanCancel reports whether a JobCanceler is wired up at all; the
	// cancel button itself is only shown when there is also an active
	// (state == provisioning) job.
	CanCancel bool
}

// activeOrLatestJob mirrors the NetBox plugin's _active_or_latest_job in
// extras/netbox-plugin-shoal/netbox_shoal/views.py: prefer a provisioning
// job, else the first row (ListByDevice returns newest-updated first).
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

// handleDeviceJobs renders GET /ui/devices/{id}/jobs: a table of jobs for
// this device (internal/api/devices.go's handleDeviceJobs / jobstore.Store's
// ListByDevice), plus a job-log panel for the active-or-latest job
// (internal/api/jobs.go's handleJobLog) and a cancel button when a job is
// actively provisioning.
func (s *Server) handleDeviceJobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := jobsPageData{DeviceID: id, CanCancel: s.Canceler != nil}
	switch r.URL.Query().Get("cancel_error") {
	case "":
		// no-op
	case "not_found":
		data.CancelError = "Cancel failed: job not found."
	default:
		data.CancelError = "Cancel failed. The job may already be in a terminal state."
	}
	if s.Jobs == nil {
		data.Error = "job store not configured"
		s.renderPage(w, r, "jobs", data)
		return
	}

	limit := parseLimit(r, 50, maxListLimit)
	// state is not validated against the LifecycleState enum: an
	// unrecognized value simply matches no rows, mirroring
	// handleDeviceJobs's own behavior.
	state := models.LifecycleState(r.URL.Query().Get("state"))
	jobs, err := s.Jobs.ListByDevice(r.Context(), id, state, limit)
	if err != nil {
		s.Log.Error("ui device jobs", "device_id", id, "err", err.Error())
		data.Error = "upstream request failed"
		s.renderPage(w, r, "jobs", data)
		return
	}
	data.Jobs = jobs
	for _, j := range jobs {
		if j.State == models.StateProvisioning {
			data.AutoRefresh = true
			break
		}
	}

	active := activeOrLatestJob(jobs)
	data.ActiveJob = active
	if active != nil && active.ID != "" {
		if s.Observe == nil {
			data.JobLogError = "observe not configured"
		} else {
			lines, logErr := s.Observe.ListJobLog(r.Context(), active.ID, time.Time{}, jobLogLimit)
			if logErr != nil {
				if isNotConfiguredErr(logErr) {
					data.JobLogError = "observe not configured"
				} else {
					s.Log.Error("ui job log", "job_id", active.ID, "err", logErr.Error())
					data.JobLogError = "upstream request failed"
				}
			} else {
				data.JobLog = lines
			}
		}
	}

	s.renderPage(w, r, "jobs", data)
}

// handleCancelJob handles POST /ui/devices/{id}/jobs (the cancel-active-job
// form on the jobs tab): calls the same JobCanceler.Cancel path as
// internal/api/jobs.go's handleCancelJob, then redirects back to the jobs
// tab so the table/log panel re-render with the new state. Unlike the JSON
// API handler, there is no response body to return a failure in -- a failed
// cancel is reported via a ?cancel_error= redirect param that
// handleDeviceJobs turns back into a banner (never the raw error text, same
// rule as every other client-facing message in this package).
//
// NOTE: like every other /ui/* write, this form relies entirely on the
// shell's cookie-based auth; the shell also owns any CSRF protection for
// /ui/* POSTs (a same-origin check, double-submit token, etc.) since that
// is cross-cutting across every write form the shell renders, not specific
// to this tab. This placeholder does not implement one itself.
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	redirectTo := "/ui/devices/" + id + "/jobs"
	if s.Canceler == nil {
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
		return
	}
	jobID := r.PostForm.Get("job_id")
	if jobID == "" {
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
		return
	}
	if err := s.Canceler.Cancel(r.Context(), jobID); err != nil {
		s.Log.Warn("ui cancel job", "job_id", jobID, "err", err.Error())
		if errors.Is(err, jobstore.ErrNotFound) {
			http.Redirect(w, r, redirectTo+"?cancel_error=not_found", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, redirectTo+"?cancel_error=1", http.StatusSeeOther)
		}
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}
