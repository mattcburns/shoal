package api

import (
	"encoding/json"
	"net/http"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/discover"
)

// WithDiscover attaches the hybrid ingest service (Phase 3).
func (s *Server) WithDiscover(svc *discover.Service) *Server {
	s.discover = svc
	return s
}

func (s *Server) handleDiscoverIngest(w http.ResponseWriter, r *http.Request) {
	if s.discover == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "discover not configured (set SHOAL_AI_* and optionally NetBox)",
		})
		return
	}
	var in models.RawAssetInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return
	}
	if err := discover.RawAssetInput(in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	got, err := s.discover.Ingest(r.Context(), in)
	if err != nil {
		s.log.Error("discover ingest failed", "err", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDiscoverConfirm(w http.ResponseWriter, r *http.Request) {
	if s.discover == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "discover not configured",
		})
		return
	}
	var req discover.ConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return
	}
	got, err := s.discover.Confirm(r.Context(), req)
	if err != nil {
		s.log.Error("discover confirm failed", "err", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, got)
}
