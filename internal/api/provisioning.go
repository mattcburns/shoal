// Shoal is a Redfish aggregator service.
// Copyright (C) 2025  Matthew Burns
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package api

import (
	"log/slog"
	"net/http"
	"strings"
)

// handleProvisioning routes provisioning requests to the appropriate handler
func (h *Handler) handleProvisioning(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Route to kickstart handler
	if strings.HasPrefix(path, "/provision/kickstart/") {
		systemID := strings.TrimPrefix(path, "/provision/kickstart/")
		systemID = strings.TrimSuffix(systemID, "/")
		if systemID == "" {
			h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.PropertyMissing", "System ID is required")
			return
		}
		h.handleProvisioningKickstart(w, r, systemID)
		return
	}

	// Route to preseed handler
	if strings.HasPrefix(path, "/provision/preseed/") {
		systemID := strings.TrimPrefix(path, "/provision/preseed/")
		systemID = strings.TrimSuffix(systemID, "/")
		if systemID == "" {
			h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.PropertyMissing", "System ID is required")
			return
		}
		h.handleProvisioningPreseed(w, r, systemID)
		return
	}

	// Unknown provisioning endpoint
	h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Provisioning resource not found")
}

// handleProvisioningKickstart serves kickstart configuration files for system installations
// GET /provision/kickstart/{system-id}
func (h *Handler) handleProvisioningKickstart(w http.ResponseWriter, r *http.Request, systemID string) {
	if r.Method == http.MethodOptions {
		h.writeAllow(w, http.MethodGet)
		return
	}
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	// Retrieve kickstart template from database
	template, err := h.db.GetProvisioningTemplate(r.Context(), systemID, "kickstart")
	if err != nil {
		slog.Error("Failed to get kickstart template", "error", err, "system_id", systemID)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to retrieve kickstart template")
		return
	}

	if template == nil {
		slog.Warn("Kickstart template not found", "system_id", systemID)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Kickstart template not found for system")
		return
	}

	// Render template with system-specific variables
	renderedContent := h.renderProvisioningTemplate(template.Content, systemID)

	// Log access for audit trail
	slog.Info("Serving kickstart template", "system_id", systemID)

	// Serve the kickstart file as plain text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderedContent))
}

// handleProvisioningPreseed serves preseed configuration files for Debian/Ubuntu installations
// GET /provision/preseed/{system-id}
func (h *Handler) handleProvisioningPreseed(w http.ResponseWriter, r *http.Request, systemID string) {
	if r.Method == http.MethodOptions {
		h.writeAllow(w, http.MethodGet)
		return
	}
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	// Retrieve preseed template from database
	template, err := h.db.GetProvisioningTemplate(r.Context(), systemID, "preseed")
	if err != nil {
		slog.Error("Failed to get preseed template", "error", err, "system_id", systemID)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to retrieve preseed template")
		return
	}

	if template == nil {
		slog.Warn("Preseed template not found", "system_id", systemID)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Preseed template not found for system")
		return
	}

	// Render template with system-specific variables
	renderedContent := h.renderProvisioningTemplate(template.Content, systemID)

	// Log access for audit trail
	slog.Info("Serving preseed template", "system_id", systemID)

	// Serve the preseed file as plain text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderedContent))
}

// renderProvisioningTemplate performs simple variable substitution in provisioning templates
// Supports {{system_id}} placeholder for now, can be extended for more variables
func (h *Handler) renderProvisioningTemplate(content, systemID string) string {
	// Simple variable substitution - replace {{system_id}} with actual system ID
	rendered := strings.ReplaceAll(content, "{{system_id}}", systemID)
	
	// Future: Add more variable substitutions as needed
	// - {{hostname}}
	// - {{ip_address}}
	// - {{gateway}}
	// etc.
	
	return rendered
}
