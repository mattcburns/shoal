package api

import (
	"net/http"

	"github.com/mattcburns/shoal/internal/core/profile"
)

// WithProfiles attaches a profile store for GET /v1/profiles. Nil is safe
// (the route reports 503 rather than a nil-pointer panic) so callers that
// don't configure SHOAL_PROFILE_DIR can wire it unconditionally.
func (s *Server) WithProfiles(store profile.Store) *Server {
	s.profiles = store
	return s
}

// handleListProfiles lists saved provisioning profiles (NetBox's Provision
// form dropdown, and any other caller that wants to browse profile_ref
// options instead of typing a name).
func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	if s.profiles == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "profile store not configured (set SHOAL_PROFILE_DIR)",
		})
		return
	}
	list, err := s.profiles.List(r.Context())
	if err != nil {
		s.log.Error("list profiles", "err", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	if list == nil {
		list = []profile.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}
