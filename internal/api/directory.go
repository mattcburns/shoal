package api

import (
	"encoding/json"
	"net/http"

	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/models"
)

// WithDirectory attaches a device directory store for GET/POST /v1/devices.
// Mirrors WithProfiles's shape (profiles.go): nil is safe -- the routes
// report 503 rather than a nil-pointer panic -- so callers that haven't
// configured a directory backend can wire it unconditionally.
func (s *Server) WithDirectory(store directory.Store) *Server {
	s.directory = store
	return s
}

// handleListDevices lists every device known to the configured directory
// backend (local file store or NetBox).
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if s.directory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "device directory not configured",
		})
		return
	}
	list, err := s.directory.ListDevices(r.Context())
	if err != nil {
		writeUpstreamError(w, s.log, "list devices", err)
		return
	}
	// No natural "since" cursor for a full device list, so -- like the
	// sensors/firmware endpoints in devices.go -- this defaults to the
	// server-side maximum rather than a smaller page size.
	limit := parseLimit(r, maxListLimit, maxListLimit)
	if len(list) > limit {
		list = list[:limit]
	}
	if list == nil {
		list = []models.DeviceIdentity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": list})
}

// createDeviceRequest is the POST /v1/devices request body. lifecycle_state
// is intentionally absent: a client can never set it on create -- every new
// device starts at models.StateDiscovered (set server-side in
// handleCreateDevice), matching how Discover ingest establishes new device
// records elsewhere in the system.
type createDeviceRequest struct {
	Name          string `json:"name"`
	Serial        string `json:"serial"`
	Vendor        string `json:"vendor"`
	Model         string `json:"model"`
	BMCIP         string `json:"bmc_ip"`
	CredentialRef string `json:"credential_ref"`
}

// handleCreateDevice registers a new device identity record.
func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	if s.directory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "device directory not configured",
		})
		return
	}
	var req createDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if err := validateCreateDevice(req.Name, req.Serial); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	d := models.DeviceIdentity{
		Name:           req.Name,
		Serial:         req.Serial,
		Vendor:         req.Vendor,
		Model:          req.Model,
		BMCIP:          req.BMCIP,
		CredentialRef:  req.CredentialRef,
		LifecycleState: models.StateDiscovered,
	}
	id, err := s.directory.UpsertDevice(r.Context(), d)
	if err != nil {
		writeUpstreamError(w, s.log, "create device", err)
		return
	}
	d.ID = id
	writeJSON(w, http.StatusCreated, d)
}
