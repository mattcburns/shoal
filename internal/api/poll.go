package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// DevicePollRequest is POST /v1/devices/{id}/poll (on-demand SEL + sensors).
type DevicePollRequest struct {
	BMCEndpoint string `json:"bmc_endpoint,omitempty"`
	BMCUsername string `json:"bmc_username,omitempty"`
	BMCPassword string `json:"bmc_password,omitempty"`
	SystemID    string `json:"system_id,omitempty"`
}

// DevicePollResult is the on-demand poll outcome. Password is never included.
type DevicePollResult struct {
	DeviceID        string `json:"device_id"`
	SELNew          int    `json:"sel_new"`
	SensorsWritten  int    `json:"sensors_written"`
	FirmwareWritten int    `json:"firmware_written"`
	PowerState      string `json:"power_state,omitempty"`
}

// DevicePoll runs one Redfish SEL+sensor poll into telemetry.
type DevicePoll interface {
	Poll(ctx context.Context, deviceID string, req DevicePollRequest) (DevicePollResult, error)
}

// WithDevicePoll attaches POST /v1/devices/{id}/poll.
func (s *Server) WithDevicePoll(p DevicePoll) *Server {
	s.poll = p
	return s
}

func (s *Server) handleDevicePoll(w http.ResponseWriter, r *http.Request) {
	if s.poll == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "device poll not configured",
		})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	var req DevicePollRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
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
	if err := validateDevicePoll(req.BMCEndpoint); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	out, err := s.poll.Poll(r.Context(), id, req)
	if err != nil {
		s.log.Warn("device poll", "device_id", id, "err", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":            err.Error(),
			"device_id":        id,
			"sel_new":          out.SELNew,
			"sensors_written":  out.SensorsWritten,
			"firmware_written": out.FirmwareWritten,
			"power_state":      out.PowerState,
		})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
