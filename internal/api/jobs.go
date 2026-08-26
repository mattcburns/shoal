package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

// JobReader loads jobs for status polling.
type JobReader interface {
	Get(ctx context.Context, id string) (models.Job, error)
}

// JobCanceler cancels an in-flight job (Orchestrator).
type JobCanceler interface {
	Cancel(ctx context.Context, jobID string) error
}

// JobStarter starts a provisioning job (Orchestrator).
//
// StartAsync returns once the job row is durable, leaving BMC bring-up running
// in the background; bring-up failures surface as a terminal job state via
// GET /v1/jobs/{id} rather than as an error here. Callers poll for progress.
type JobStarter interface {
	StartAsync(ctx context.Context, req models.StartJobRequest) (models.Job, error)
}

// WithJobStore attaches a job store for GET /v1/jobs/{id}.
func (s *Server) WithJobStore(store jobstore.Store) *Server {
	s.jobs = store
	return s
}

// WithJobCanceler attaches cancel support for POST /v1/jobs/{id}/cancel.
func (s *Server) WithJobCanceler(c JobCanceler) *Server {
	s.cancel = c
	return s
}

// WithJobStarter attaches start support for POST /v1/jobs.
func (s *Server) WithJobStarter(st JobStarter) *Server {
	s.start = st
	return s
}

// startJobBoundaryProbe returns a copy of req patched, for validation
// purposes only, with the two fields Orchestrator.prepareStart may fill in
// from state this handler cannot see (a NetBox device record, orchestrator-
// wide SHOAL_BMC_* env defaults, or an https bmc_endpoint's implied
// transport) before its own job.StartJobRequest call. The original req
// sent on to StartAsync is never modified -- only this probe, used solely to
// decide whether the boundary check in handleStartJob should reject the
// request outright.
func startJobBoundaryProbe(req models.StartJobRequest) models.StartJobRequest {
	probe := req
	if strings.TrimSpace(probe.CredentialRef) == "" &&
		strings.TrimSpace(probe.BMCUsername) == "" &&
		strings.TrimSpace(probe.BMCPassword) == "" {
		// Orchestrator.applyStartBindings/applyDefaultCredentials may still
		// resolve real credentials from NetBox or env defaults; a placeholder
		// here just satisfies the boundary check's "some credential material
		// is present" rule so it defers the real answer to that later pass.
		probe.CredentialRef = "boundary-probe-placeholder"
	}
	if strings.TrimSpace(probe.SerialTransport) == "" && job.LooksLikeHTTPSBMC(probe.BMCEndpoint) {
		// Mirrors Orchestrator.applyStartBindings' auto-detect exactly (same
		// exported helper, not a copy) so an https bmc_endpoint without an
		// explicit serial_target isn't rejected here for a serial_target
		// requirement that redfish_sol doesn't have.
		probe.SerialTransport = "redfish_sol"
	}
	return probe
}

func (s *Server) handleStartJob(w http.ResponseWriter, r *http.Request) {
	if s.start == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "job start not configured",
		})
		return
	}
	var req models.StartJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	// Boundary validation, matching every other write handler: reject a
	// structurally-invalid request (bad install_strategy, missing device_id /
	// bmc_endpoint, unknown enums, ...) before it reaches business logic.
	//
	// This runs on startJobBoundaryProbe(req), not req itself: job.StartJobRequest
	// also requires resolvable BMC credentials and (absent an explicit
	// serial_transport) a serial_target, but those can legitimately come from
	// fields this handler cannot see -- Orchestrator.prepareStart's
	// applyStartBindings/applyDefaultCredentials fill credential_ref from a
	// NetBox device record or BMC username/password from orchestrator-wide
	// SHOAL_BMC_* defaults (the documented "NetBox start-job need not post
	// passwords" path), and auto-derive serial_transport=redfish_sol for an
	// https bmc_endpoint. The probe copy patches over exactly those two
	// defaultable gaps so this boundary check can never reject a request
	// solely because a fill that hasn't happened yet is missing, while still
	// catching everything else (bad enums, missing device_id/bmc_endpoint,
	// inconsistent prep/seed/os_family combinations, ...). req itself is
	// untouched, so Orchestrator.prepareStart's own job.StartJobRequest
	// call -- unchanged below in internal/deploy/job/orchestrator.go -- remains
	// the sole authority on whether credentials/serial_transport actually end
	// up resolved, for both this handler and the CLI's direct
	// orch.Start/StartAsync callers in internal/cli/deploy.go, which never
	// reach this handler at all.
	if err := job.StartJobRequest(startJobBoundaryProbe(req)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Returns as soon as the job row is durable; BMC bring-up (SOL attach, media
	// insert, boot override, power cycle -- ~40s on a Dell R750/iDRAC9) continues
	// in the background and is observed via GET /v1/jobs/{id}. Blocking here
	// instead made clients time out on jobs that were starting fine.
	//
	// Cancellation is still detached (not r.Context()) because the request-scoped
	// work that remains -- resolveDeviceID, secrets, probeCDCount, store.Insert,
	// syncNetBoxLifecycle -- must not be abortable by a client that gives up
	// mid-flight: that is how a real deprovision died with "jobstore: insert:
	// context canceled" after the BMC had already been touched.
	j, err := s.start.StartAsync(context.WithoutCancel(r.Context()), req)
	if err != nil {
		s.log.Warn("start job", "err", err.Error())
		// May still have a job row after partial start.
		if j.ID != "" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "job": j})
			return
		}
		// Validation / approval errors are client faults.
		msg := err.Error()
		if strings.Contains(msg, "validate:") || strings.Contains(msg, "requires approval") || strings.Contains(msg, "load profile") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	metricJobsStarted.Add(1)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "job store not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing job id"})
		return
	}
	j, err := s.jobs.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, jobstore.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		s.log.Error("get job", "err", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request) {
	if s.observe == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "observe not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing job id"})
		return
	}
	limit := parseLimit(r, 50, maxListLimit)
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	lines, err := s.observe.ListJobLog(r.Context(), id, since, limit)
	if err != nil {
		if isNotConfiguredErr(err) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "observe not configured"})
			return
		}
		writeUpstreamError(w, s.log, "job log", err, "job_id", id)
		return
	}
	if lines == nil {
		lines = []telemetry.JobLogLine{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": id,
		"lines":  lines,
	})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if s.cancel == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "job cancel not configured",
		})
		return
	}
	if s.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "job store not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing job id"})
		return
	}
	if err := s.cancel.Cancel(r.Context(), id); err != nil {
		// Not found vs other errors
		if errors.Is(err, jobstore.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		// Cancel of non-provisioning jobs returns a plain error from Orchestrator
		s.log.Warn("cancel job", "job_id", id, "err", err.Error())
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	metricJobsCancel.Add(1)
	// Async terminal — poll briefly for updated state
	j, err := s.jobs.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"job_id": id,
			"status": "cancel_requested",
		})
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}
