/*
Shoal is a Redfish aggregator service.
Copyright (C) 2025  Matthew Burns

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"shoal/internal/ctxkeys"
)

// handleBMCProxy proxies requests to managed BMCs.
// Extracted to proxy.go per design 019. Behavior is unchanged.
func (h *Handler) handleBMCProxy(w http.ResponseWriter, r *http.Request, path string) {
	// Extract BMC name from path (e.g., /v1/Managers/bmc1/...)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Resource not found")
		return
	}

	var bmcName string
	var bmcPath string

	// Handle different proxy patterns
	if parts[1] == "Managers" && len(parts) >= 3 {
		bmcName = parts[2]

		// Check if this is a VirtualMedia request
		if len(parts) >= 4 && parts[3] == "VirtualMedia" {
			// VirtualMedia collection: /v1/Managers/{id}/VirtualMedia
			if len(parts) == 4 {
				h.handleVirtualMediaCollection(w, r, bmcName)
				return
			}
			// VirtualMedia resource: /v1/Managers/{id}/VirtualMedia/{mediaId}
			if len(parts) == 5 {
				h.handleVirtualMedia(w, r, bmcName, parts[4])
				return
			}
			// VirtualMedia actions: /v1/Managers/{id}/VirtualMedia/{mediaId}/Actions/VirtualMedia.{Action}
			if len(parts) == 7 && parts[5] == "Actions" {
				mediaID := parts[4]
				if parts[6] == "VirtualMedia.InsertMedia" {
					h.handleInsertMedia(w, r, bmcName, mediaID)
					return
				}
				if parts[6] == "VirtualMedia.EjectMedia" {
					h.handleEjectMedia(w, r, bmcName, mediaID)
					return
				}
			}
		}

		// Get the actual manager ID from the BMC
		managerID, err := h.bmcSvc.GetFirstManagerID(r.Context(), bmcName)
		if err != nil {
			slog.Error("Failed to get manager ID", "bmc", bmcName, "error", err)
			// Fall back to common defaults
			managerID = "1" // Common default
		}

		if len(parts) == 3 {
			// Request for the manager root
			bmcPath = fmt.Sprintf("/redfish/v1/Managers/%s", managerID)
		} else {
			// Sub-resource request
			bmcPath = fmt.Sprintf("/redfish/v1/Managers/%s/%s", managerID, strings.Join(parts[3:], "/"))
		}
	} else if parts[1] == "Systems" && len(parts) >= 3 {
		// Extract BMC name and system path
		bmcName = parts[2]
		// Get the actual system ID from the BMC
		systemID, err := h.bmcSvc.GetFirstSystemID(r.Context(), bmcName)
		if err != nil {
			slog.Error("Failed to get system ID", "bmc", bmcName, "error", err)
			// Fall back to common defaults
			systemID = "1" // Common default
		}

		if len(parts) == 3 {
			// Request for the system root
			bmcPath = fmt.Sprintf("/redfish/v1/Systems/%s", systemID)
		} else {
			// Sub-resource request
			bmcPath = fmt.Sprintf("/redfish/v1/Systems/%s/%s", systemID, strings.Join(parts[3:], "/"))
		}
	} else {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Resource not found")
		return
	}

	// Ensure authenticated user is present in context so audits can include it
	user, _ := h.auth.AuthenticateRequest(r)
	ctx := r.Context()
	if user != nil {
		ctx = context.WithValue(ctx, ctxkeys.User, user)
	}

	// Proxy request to BMC
	resp, err := h.bmcSvc.ProxyRequest(ctx, bmcName, bmcPath, r)
	if err != nil {
		slog.Error("Failed to proxy request to BMC", "bmc", bmcName, "path", bmcPath, "error", err)
		h.writeErrorResponse(w, http.StatusBadGateway, "Base.1.0.InternalError", fmt.Sprintf("Failed to communicate with BMC: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code and body
	w.WriteHeader(resp.StatusCode)
	if _, cerr := io.Copy(w, resp.Body); cerr != nil {
		slog.Warn("proxy response copy error", "error", cerr)
	}
}

// isBMCProxyRequest checks if the request should be proxied to a BMC.
// Extracted to proxy.go per design 019. Behavior is unchanged.
func (h *Handler) isBMCProxyRequest(path string) bool {
	// Proxy requests for Managers and Systems endpoints with BMC names
	managerPattern := regexp.MustCompile(`^/v1/Managers/[^/]+(/.*)?$`)
	systemPattern := regexp.MustCompile(`^/v1/Systems/[^/]+(/.*)?$`)

	return managerPattern.MatchString(path) || systemPattern.MatchString(path)
}
