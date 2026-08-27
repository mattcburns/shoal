package ui

import (
	"net/http"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// eventsPageData is the data passed to templates/events.html.
type eventsPageData struct {
	DeviceID string
	Events   []models.NormalizedEvent
	// Error is a client-safe message only (never a raw upstream error, same
	// rule as internal/api/errors.go's writeUpstreamError).
	Error string
}

// handleDeviceEvents renders GET /ui/devices/{id}/events: a read-only table
// of normalized events for the device, same pagination convention as
// internal/api/devices.go's handleDeviceEvents (?limit=, default 50, capped
// at maxListLimit).
func (s *Server) handleDeviceEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := eventsPageData{DeviceID: id}
	if s.Observe == nil {
		data.Error = "observe not configured"
		s.renderPage(w, r, "events", data)
		return
	}

	limit := parseLimit(r, 50, maxListLimit)
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}

	evs, err := s.Observe.ListEvents(r.Context(), id, since, limit)
	if err != nil {
		if isNotConfiguredErr(err) {
			data.Error = "observe not configured"
		} else {
			s.Log.Error("ui device events", "device_id", id, "err", err.Error())
			data.Error = "upstream request failed"
		}
		s.renderPage(w, r, "events", data)
		return
	}
	data.Events = evs
	s.renderPage(w, r, "events", data)
}
