package api

import (
	"errors"
	"net/http"

	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

// WithJobStore attaches a job store for GET /v1/jobs/{id}.
func (s *Server) WithJobStore(store jobstore.Store) *Server {
	s.jobs = store
	return s
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
