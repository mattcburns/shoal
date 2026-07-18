package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/validate"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

// JobReader loads jobs for status polling.
type JobReader interface {
	Get(ctx context.Context, id string) (models.ProvisioningJob, error)
}

// JobCanceler cancels an in-flight job (Orchestrator).
type JobCanceler interface {
	Cancel(ctx context.Context, jobID string) error
}

// JobStarter starts a provisioning job (Orchestrator).
type JobStarter interface {
	Start(ctx context.Context, req models.StartJobRequest) (models.ProvisioningJob, error)
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
	if err := validate.StartJobRequest(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	j, err := s.start.Start(r.Context(), req)
	if err != nil {
		s.log.Warn("start job", "err", err.Error())
		// May still have a job row after partial start.
		if j.ID != "" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "job": j})
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
