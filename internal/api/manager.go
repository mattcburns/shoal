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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"shoal/pkg/models"
)

// handleManagerResource handles GET requests for individual Manager resources
// and enhances the response with console capabilities and OEM actions
func (h *Handler) handleManagerResource(w http.ResponseWriter, r *http.Request, bmcName, bmcPath string) {
	if r.Method != http.MethodGet {
		// For non-GET requests, just proxy through
		h.proxyToBMC(w, r, bmcName, bmcPath)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Proxy the request to get the base Manager resource
	resp, err := h.bmcSvc.ProxyRequest(ctx, bmcName, bmcPath, r)
	if err != nil {
		slog.Error("Failed to proxy Manager request", "bmc", bmcName, "path", bmcPath, "error", err)
		h.writeErrorResponse(w, http.StatusBadGateway, "Base.1.0.InternalError", fmt.Sprintf("Failed to communicate with BMC: %v", err))
		return
	}
	defer resp.Body.Close()

	// If the response is not 200 OK, just pass it through
	if resp.StatusCode != http.StatusOK {
		h.copyResponse(w, resp)
		return
	}

	// Read and parse the Manager response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("Failed to read Manager response", "error", err)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to read BMC response")
		return
	}

	var managerData map[string]interface{}
	if err := json.Unmarshal(body, &managerData); err != nil {
		slog.Error("Failed to parse Manager response", "error", err)
		// Return the original response if we can't parse it
		h.copyResponseWithBody(w, resp, body)
		return
	}

	// Get connection method for this BMC to query console capabilities
	cm, err := h.db.GetConnectionMethod(ctx, bmcName)
	if err != nil || cm == nil {
		// If we can't get connection method, just return the original response
		slog.Debug("Connection method not found for BMC", "bmc", bmcName)
		h.copyResponseWithBody(w, resp, body)
		return
	}

	// Get console capabilities from database
	capabilities, err := h.db.GetConsoleCapabilities(ctx, cm.ID, "")
	if err != nil {
		slog.Debug("Failed to get console capabilities", "error", err)
		// Continue without console capabilities
		capabilities = []models.ConsoleCapability{}
	}

	// Enhance Manager resource with console properties
	h.enhanceManagerWithConsole(managerData, bmcName, capabilities)

	// Return enhanced Manager resource
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(managerData); err != nil {
		slog.Error("Failed to encode enhanced Manager response", "error", err)
	}
}

// enhanceManagerWithConsole adds SerialConsole, GraphicalConsole properties and OEM console actions
func (h *Handler) enhanceManagerWithConsole(managerData map[string]interface{}, bmcName string, capabilities []models.ConsoleCapability) {
	// Build console properties from capabilities
	var serialConsole, graphicalConsole map[string]interface{}

	for _, cap := range capabilities {
		consoleProperty := map[string]interface{}{
			"ServiceEnabled":        cap.ServiceEnabled,
			"MaxConcurrentSessions": cap.MaxConcurrentSession,
		}

		// Parse ConnectTypes
		var connectTypes []string
		if err := json.Unmarshal([]byte(cap.ConnectTypes), &connectTypes); err == nil {
			consoleProperty["ConnectTypesSupported"] = connectTypes
		} else {
			consoleProperty["ConnectTypesSupported"] = []string{"Oem"}
		}

		if cap.ConsoleType == models.ConsoleTypeSerial {
			serialConsole = consoleProperty
		} else if cap.ConsoleType == models.ConsoleTypeGraphical {
			graphicalConsole = consoleProperty
		}
	}

	// If no capabilities were found, set defaults for discovery
	if serialConsole == nil {
		serialConsole = map[string]interface{}{
			"ServiceEnabled":           false,
			"MaxConcurrentSessions":    0,
			"ConnectTypesSupported":    []string{},
		}
	}
	if graphicalConsole == nil {
		graphicalConsole = map[string]interface{}{
			"ServiceEnabled":           false,
			"MaxConcurrentSessions":    0,
			"ConnectTypesSupported":    []string{},
		}
	}

	// Add console properties to Manager resource
	managerData["SerialConsole"] = serialConsole
	managerData["GraphicalConsole"] = graphicalConsole

	// Add OEM Shoal console actions
	oem, ok := managerData["Oem"].(map[string]interface{})
	if !ok {
		oem = make(map[string]interface{})
		managerData["Oem"] = oem
	}

	shoalOEM := map[string]interface{}{
		"@odata.type": "#ShoalManager.v1_0_0.ShoalManager",
		"ConsoleActions": map[string]interface{}{
			"#Manager.ConnectSerialConsole": map[string]string{
				"target": fmt.Sprintf("/redfish/v1/Managers/%s/Actions/Oem/Shoal.ConnectSerialConsole", bmcName),
			},
			"#Manager.ConnectGraphicalConsole": map[string]string{
				"target": fmt.Sprintf("/redfish/v1/Managers/%s/Actions/Oem/Shoal.ConnectGraphicalConsole", bmcName),
			},
		},
		"ConsoleSessions": map[string]string{
			"@odata.id": fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions", bmcName),
		},
	}

	oem["Shoal"] = shoalOEM
}

// proxyToBMC is a helper to proxy requests to the BMC
func (h *Handler) proxyToBMC(w http.ResponseWriter, r *http.Request, bmcName, bmcPath string) {
	resp, err := h.bmcSvc.ProxyRequest(r.Context(), bmcName, bmcPath, r)
	if err != nil {
		slog.Error("Failed to proxy request to BMC", "bmc", bmcName, "path", bmcPath, "error", err)
		h.writeErrorResponse(w, http.StatusBadGateway, "Base.1.0.InternalError", fmt.Sprintf("Failed to communicate with BMC: %v", err))
		return
	}
	defer resp.Body.Close()

	h.copyResponse(w, resp)
}

// copyResponse copies HTTP response to the ResponseWriter
func (h *Handler) copyResponse(w http.ResponseWriter, resp *http.Response) {
	// Copy headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status and body
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Warn("Response copy error", "error", err)
	}
}

// copyResponseWithBody copies HTTP response with pre-read body
func (h *Handler) copyResponseWithBody(w http.ResponseWriter, resp *http.Response, body []byte) {
	// Copy headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status and body
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil {
		slog.Warn("Response write error", "error", err)
	}
}
