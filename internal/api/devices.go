package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/observe"
)

// WithObserve attaches the Observe service for device status/events.
func (s *Server) WithObserve(obs *observe.Service) *Server {
	s.observe = obs
	return s
}

func (s *Server) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	if s.observe == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "observe not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	st, err := s.observe.Status(r.Context(), id)
	if err != nil {
		s.log.Error("device status", "err", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDeviceEvents(w http.ResponseWriter, r *http.Request) {
	if s.observe == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "observe not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	evs, err := s.observe.ListEvents(r.Context(), id, since, limit)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if evs == nil {
		evs = []models.NormalizedEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id": id,
		"events":    evs,
	})
}
