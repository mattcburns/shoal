package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
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

func (s *Server) handleDeviceJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "job store not configured",
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
	if limit > maxListLimit {
		limit = maxListLimit
	}
	// state is not validated against the LifecycleState enum: an unrecognized
	// value simply matches no rows, mirroring how an unparseable `since`
	// elsewhere falls back to "no filter" rather than 400.
	state := models.LifecycleState(r.URL.Query().Get("state"))
	jobs, err := s.jobs.ListByDevice(r.Context(), id, state, limit)
	if err != nil {
		s.log.Error("list device jobs", "err", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	if jobs == nil {
		jobs = []models.ProvisioningJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id": id,
		"jobs":      jobs,
	})
}

func (s *Server) handleDeviceSensors(w http.ResponseWriter, r *http.Request) {
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
	limit := maxListLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	readings, err := s.observe.ListSensors(r.Context(), id, since, limit)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if readings == nil {
		readings = []telemetry.SensorReading{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id": id,
		"readings":  readings,
	})
}

func (s *Server) handleDeviceFirmware(w http.ResponseWriter, r *http.Request) {
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
	limit := maxListLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	comps, err := s.observe.ListFirmware(r.Context(), id, limit)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if comps == nil {
		comps = []telemetry.FirmwareComponent{}
	}
	var ts time.Time
	if len(comps) > 0 {
		ts = comps[0].TS
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":  id,
		"ts":         ts,
		"components": comps,
	})
}
