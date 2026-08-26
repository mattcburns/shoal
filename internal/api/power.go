package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// DevicePowerRequest is POST /v1/devices/{id}/power.
type DevicePowerRequest struct {
	ResetType   string `json:"reset_type"`
	BMCEndpoint string `json:"bmc_endpoint"`
	BMCUsername string `json:"bmc_username,omitempty"`
	BMCPassword string `json:"bmc_password,omitempty"`
	SystemID    string `json:"system_id,omitempty"`
}

// DevicePowerResult is the BMC power action outcome.
type DevicePowerResult struct {
	DeviceID   string `json:"device_id"`
	ResetType  string `json:"reset_type"`
	PowerState string `json:"power_state,omitempty"`
	SystemID   string `json:"system_id,omitempty"`
}

// DevicePower applies a Redfish reset for operator power control (not a Deploy job).
type DevicePower interface {
	Power(ctx context.Context, deviceID string, req DevicePowerRequest) (DevicePowerResult, error)
}

// WithDevicePower attaches POST /v1/devices/{id}/power.
func (s *Server) WithDevicePower(p DevicePower) *Server {
	s.power = p
	return s
}

func (s *Server) handleDevicePower(w http.ResponseWriter, r *http.Request) {
	if s.power == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "device power not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	var req DevicePowerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if (strings.TrimSpace(req.BMCUsername) == "" || req.BMCPassword == "") && s.creds != nil {
		if u, p, err := s.creds.Resolve(r.Context(), id); err == nil {
			if strings.TrimSpace(req.BMCUsername) == "" {
				req.BMCUsername = u
			}
			if req.BMCPassword == "" {
				req.BMCPassword = p
			}
		}
	}
	if strings.TrimSpace(req.BMCUsername) == "" {
		req.BMCUsername = s.cfg.BMCUsername
	}
	if req.BMCPassword == "" {
		req.BMCPassword = s.cfg.BMCPassword
	}
	if strings.TrimSpace(req.BMCEndpoint) == "" && s.creds != nil {
		if view, err := s.creds.Get(r.Context(), id, ""); err == nil {
			req.BMCEndpoint = endpointFromBMCIP(view.BMCIP)
		}
	}
	if err := validateDevicePower(req.ResetType, req.BMCEndpoint); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	out, err := s.power.Power(r.Context(), id, req)
	if err != nil {
		writeUpstreamError(w, s.log, "device power", err, "device_id", id, "reset_type", req.ResetType)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func endpointFromBMCIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	if strings.HasPrefix(ip, "http://") || strings.HasPrefix(ip, "https://") {
		return ip
	}
	return "https://" + ip
}
