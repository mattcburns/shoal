package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mattcburns/shoal/internal/common/validate"
)

// DeviceCredentialsView is a non-secret view of stored BMC credentials.
type DeviceCredentialsView struct {
	DeviceID      string `json:"device_id"`
	CredentialRef string `json:"credential_ref"`
	Username      string `json:"username,omitempty"`
	HasPassword   bool   `json:"has_password"`
	BMCIP         string `json:"bmc_ip,omitempty"`
}

// DeviceCredentialsPut updates stored BMC credentials. Password is never returned.
// Empty password keeps the existing secret when a credential already exists.
type DeviceCredentialsPut struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	BMCIP    string `json:"bmc_ip,omitempty"`
}

// DeviceCredentials stores BMC user/pass in the secrets backend (not NetBox).
type DeviceCredentials interface {
	// Get returns a non-secret view. credentialRef, when set, skips NetBox lookup
	// (plugin already has the custom field; calling NetBox from a Status render can stall).
	Get(ctx context.Context, deviceID, credentialRef string) (DeviceCredentialsView, error)
	Put(ctx context.Context, deviceID string, req DeviceCredentialsPut) (DeviceCredentialsView, error)
	// Resolve returns username/password for power/jobs. Never log the result.
	Resolve(ctx context.Context, deviceID string) (username, password string, err error)
}

// WithDeviceCredentials attaches GET/PUT /v1/devices/{id}/credentials.
func (s *Server) WithDeviceCredentials(c DeviceCredentials) *Server {
	s.creds = c
	return s
}

func (s *Server) handleDeviceCredentialsGet(w http.ResponseWriter, r *http.Request) {
	if s.creds == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "credentials store not configured"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	view, err := s.creds.Get(r.Context(), id, r.URL.Query().Get("credential_ref"))
	if err != nil {
		s.log.Warn("device credentials get", "device_id", id, "err", err.Error())
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeviceCredentialsPut(w http.ResponseWriter, r *http.Request) {
	if s.creds == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "credentials store not configured"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	var req DeviceCredentialsPut
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if err := validate.DeviceCredentials(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	view, err := s.creds.Put(r.Context(), id, req)
	if err != nil {
		s.log.Warn("device credentials put", "device_id", id, "err", err.Error())
		msg := err.Error()
		code := http.StatusBadRequest
		if strings.Contains(msg, "not found") || strings.Contains(msg, "status 404") {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
