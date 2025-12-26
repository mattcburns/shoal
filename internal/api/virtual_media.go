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
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"shoal/internal/database"
	"shoal/pkg/redfish"
)

// lookupConnectionMethodAndManager finds the connection method and manager ID for a given BMC name or ID
func (h *Handler) lookupConnectionMethodAndManager(r *http.Request, bmcNameOrID string) (connectionMethodID, managerID string, err error) {
	methods, err := h.bmcSvc.GetConnectionMethods(r.Context())
	if err != nil {
		return "", "", fmt.Errorf("failed to get connection methods: %w", err)
	}

	for _, method := range methods {
		if method.Name == bmcNameOrID || method.ID == bmcNameOrID {
			connectionMethodID = method.ID
			if method.AggregatedManagers != "" {
				var managers []map[string]interface{}
				if err := json.Unmarshal([]byte(method.AggregatedManagers), &managers); err == nil && len(managers) > 0 {
					if odataID, ok := managers[0]["@odata.id"].(string); ok {
						parts := strings.Split(strings.Trim(odataID, "/"), "/")
						if len(parts) >= 4 && parts[2] == "Managers" {
							managerID = parts[3]
						}
					}
				}
			}
			return connectionMethodID, managerID, nil
		}
	}

	return "", "", fmt.Errorf("connection method not found")
}

// handleVirtualMediaCollection returns the VirtualMedia collection for a specific manager
// GET /redfish/v1/Managers/{ManagerId}/VirtualMedia
func (h *Handler) handleVirtualMediaCollection(w http.ResponseWriter, r *http.Request, bmcNameOrID string) {
	if r.Method == http.MethodOptions {
		h.writeAllow(w, http.MethodGet)
		return
	}
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	connectionMethodID, managerID, err := h.lookupConnectionMethodAndManager(r, bmcNameOrID)
	if err != nil {
		slog.Error("Failed to lookup connection method", "error", err, "bmc", bmcNameOrID)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Manager not found")
		return
	}

	// Get virtual media resources from database
	resources, err := h.db.GetVirtualMediaResources(r.Context(), connectionMethodID, managerID)
	if err != nil {
		slog.Error("Failed to get virtual media resources", "error", err, "connection_method", connectionMethodID, "manager", managerID)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to retrieve virtual media resources")
		return
	}

	// Build collection members
	var members []redfish.ODataIDRef
	for _, resource := range resources {
		members = append(members, redfish.ODataIDRef{
			ODataID: fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s", bmcNameOrID, resource.ResourceID),
		})
	}

	collection := redfish.Collection{
		ODataContext: "/redfish/v1/$metadata#VirtualMediaCollection.VirtualMediaCollection",
		ODataID:      fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia", bmcNameOrID),
		ODataType:    "#VirtualMediaCollection.VirtualMediaCollection",
		Name:         "Virtual Media Services",
		Members:      members,
		MembersCount: len(members),
	}

	h.writeJSONResponse(w, http.StatusOK, collection)
}

// handleVirtualMedia returns a specific VirtualMedia resource
// GET /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}
func (h *Handler) handleVirtualMedia(w http.ResponseWriter, r *http.Request, bmcNameOrID, mediaID string) {
	if r.Method == http.MethodOptions {
		h.writeAllow(w, http.MethodGet)
		return
	}
	if r.Method != http.MethodGet {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	connectionMethodID, managerID, err := h.lookupConnectionMethodAndManager(r, bmcNameOrID)
	if err != nil {
		slog.Error("Failed to lookup connection method", "error", err, "bmc", bmcNameOrID)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Manager not found")
		return
	}

	// Get virtual media resource from database
	resource, err := h.db.GetVirtualMediaResource(r.Context(), connectionMethodID, managerID, mediaID)
	if err != nil {
		slog.Error("Failed to get virtual media resource", "error", err, "connection_method", connectionMethodID, "manager", managerID, "media", mediaID)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to retrieve virtual media resource")
		return
	}

	if resource == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Virtual media resource not found")
		return
	}

	// Parse media types from JSON
	var mediaTypes []string
	if resource.MediaTypes != "" {
		if err := json.Unmarshal([]byte(resource.MediaTypes), &mediaTypes); err != nil {
			slog.Warn("Failed to parse media types", "error", err, "media_types", resource.MediaTypes)
		}
	}

	// Determine transfer protocol type from current image URL
	transferProtocol := ""
	if resource.CurrentImageURL != "" {
		if strings.HasPrefix(resource.CurrentImageURL, "https://") {
			transferProtocol = "HTTPS"
		} else if strings.HasPrefix(resource.CurrentImageURL, "http://") {
			transferProtocol = "HTTP"
		} else if strings.HasPrefix(resource.CurrentImageURL, "nfs://") {
			transferProtocol = "NFS"
		}
	}

	vmResource := redfish.VirtualMedia{
		ODataContext:         "/redfish/v1/$metadata#VirtualMedia.VirtualMedia",
		ODataID:              fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s", bmcNameOrID, mediaID),
		ODataType:            "#VirtualMedia.v1_6_0.VirtualMedia",
		ID:                   mediaID,
		Name:                 fmt.Sprintf("Virtual Media %s", mediaID),
		MediaTypes:           mediaTypes,
		Image:                resource.CurrentImageURL,
		ImageName:            resource.CurrentImageName,
		Inserted:             resource.IsInserted,
		WriteProtected:       resource.IsWriteProtected,
		ConnectedVia:         resource.ConnectedVia,
		TransferProtocolType: transferProtocol,
		Actions: &redfish.VirtualMediaActions{
			InsertMedia: &redfish.VirtualMediaAction{
				Target: fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s/Actions/VirtualMedia.InsertMedia", bmcNameOrID, mediaID),
			},
			EjectMedia: &redfish.VirtualMediaAction{
				Target: fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s/Actions/VirtualMedia.EjectMedia", bmcNameOrID, mediaID),
			},
		},
	}

	h.writeJSONResponse(w, http.StatusOK, vmResource)
}

// handleInsertMedia processes InsertMedia action requests
// POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.InsertMedia
func (h *Handler) handleInsertMedia(w http.ResponseWriter, r *http.Request, bmcNameOrID, mediaID string) {
	if r.Method == http.MethodOptions {
		h.writeAllow(w, http.MethodPost)
		return
	}
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	// Parse request body
	var req redfish.InsertMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode InsertMedia request", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.MalformedJSON", "Invalid JSON in request body")
		return
	}

	// Validate required fields
	if req.Image == "" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.PropertyMissing", "Image property is required")
		return
	}

	// Get connection method and manager ID
	connectionMethodID, managerID, err := h.lookupConnectionMethodAndManager(r, bmcNameOrID)
	if err != nil {
		slog.Error("Failed to lookup connection method", "error", err, "bmc", bmcNameOrID)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Manager not found")
		return
	}

	// Get virtual media resource from database
	resource, err := h.db.GetVirtualMediaResource(r.Context(), connectionMethodID, managerID, mediaID)
	if err != nil {
		slog.Error("Failed to get virtual media resource", "error", err, "connection_method", connectionMethodID, "manager", managerID, "media", mediaID)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to retrieve virtual media resource")
		return
	}

	if resource == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Virtual media resource not found")
		return
	}

	// Get authenticated user for audit trail
	user, err := h.auth.AuthenticateRequest(r)
	if err != nil {
		slog.Error("Failed to authenticate request", "error", err)
		h.writeErrorResponse(w, http.StatusUnauthorized, "Base.1.0.Unauthorized", "Authentication required")
		return
	}

	// Record operation in database
	op := &database.VirtualMediaOperation{
		VirtualMediaResourceID: resource.ID,
		Operation:              "insert",
		ImageURL:               req.Image,
		RequestedBy:            user.Username,
		Status:                 "pending",
	}
	if err := h.db.CreateVirtualMediaOperation(r.Context(), op); err != nil {
		slog.Error("Failed to create operation record", "error", err)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to record operation")
		return
	}

	// Forward request to downstream BMC
	// Build the action path on the downstream BMC
	actionPath := fmt.Sprintf("%s/Actions/VirtualMedia.InsertMedia", resource.ODataID)

	// Create request to downstream BMC
	reqBody, err := json.Marshal(req)
	if err != nil {
		slog.Error("Failed to marshal request body", "error", err)
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", "Failed to prepare request")
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to prepare request")
		return
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, actionPath, strings.NewReader(string(reqBody)))
	if err != nil {
		slog.Error("Failed to create proxy request", "error", err)
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", "Failed to create proxy request")
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to create proxy request")
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	// Proxy request to BMC
	resp, err := h.bmcSvc.ProxyRequestToConnectionMethod(r.Context(), connectionMethodID, actionPath, proxyReq)
	if err != nil {
		slog.Error("Failed to proxy InsertMedia to BMC", "error", err, "connection_method", connectionMethodID, "path", actionPath)
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", fmt.Sprintf("BMC communication failed: %v", err))
		h.writeErrorResponse(w, http.StatusBadGateway, "Base.1.0.InternalError", fmt.Sprintf("Failed to communicate with BMC: %v", err))
		return
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("BMC returned error for InsertMedia", "status", resp.StatusCode, "body", string(bodyBytes))
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", fmt.Sprintf("BMC returned status %d", resp.StatusCode))
		
		// Forward the error response from BMC
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		return
	}

	// Update operation status to success
	if err := h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "success", ""); err != nil {
		slog.Warn("Failed to update operation status", "error", err)
	}

	// Update resource state in database
	imageName := req.Image
	// Extract filename from URL if possible
	if lastSlash := strings.LastIndex(req.Image, "/"); lastSlash != -1 {
		imageName = req.Image[lastSlash+1:]
	}

	inserted := true
	if req.Inserted != nil {
		inserted = *req.Inserted
	}

	writeProtected := false
	if req.WriteProtected != nil {
		writeProtected = *req.WriteProtected
	}

	connectedVia := "URI"
	if err := h.db.UpsertVirtualMediaResource(r.Context(), connectionMethodID, managerID, mediaID,
		resource.ODataID, resource.MediaTypes, resource.SupportedProtocols,
		&req.Image, &imageName, inserted, writeProtected, connectedVia); err != nil {
		slog.Warn("Failed to update resource state", "error", err)
	}

	// Return success (204 No Content per DMTF spec)
	w.WriteHeader(http.StatusNoContent)
}

// handleEjectMedia processes EjectMedia action requests
// POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.EjectMedia
func (h *Handler) handleEjectMedia(w http.ResponseWriter, r *http.Request, bmcNameOrID, mediaID string) {
	if r.Method == http.MethodOptions {
		h.writeAllow(w, http.MethodPost)
		return
	}
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	// Parse request body (should be empty, but validate it's valid JSON)
	var req redfish.EjectMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body
		if err != io.EOF {
			slog.Error("Failed to decode EjectMedia request", "error", err)
			h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.MalformedJSON", "Invalid JSON in request body")
			return
		}
	}

	// Get connection method and manager ID
	connectionMethodID, managerID, err := h.lookupConnectionMethodAndManager(r, bmcNameOrID)
	if err != nil {
		slog.Error("Failed to lookup connection method", "error", err, "bmc", bmcNameOrID)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Manager not found")
		return
	}

	// Get virtual media resource from database
	resource, err := h.db.GetVirtualMediaResource(r.Context(), connectionMethodID, managerID, mediaID)
	if err != nil {
		slog.Error("Failed to get virtual media resource", "error", err, "connection_method", connectionMethodID, "manager", managerID, "media", mediaID)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to retrieve virtual media resource")
		return
	}

	if resource == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Virtual media resource not found")
		return
	}

	// Get authenticated user for audit trail
	user, err := h.auth.AuthenticateRequest(r)
	if err != nil {
		slog.Error("Failed to authenticate request", "error", err)
		h.writeErrorResponse(w, http.StatusUnauthorized, "Base.1.0.Unauthorized", "Authentication required")
		return
	}

	// Record operation in database
	op := &database.VirtualMediaOperation{
		VirtualMediaResourceID: resource.ID,
		Operation:              "eject",
		ImageURL:               "", // No image URL for eject
		RequestedBy:            user.Username,
		Status:                 "pending",
	}
	if err := h.db.CreateVirtualMediaOperation(r.Context(), op); err != nil {
		slog.Error("Failed to create operation record", "error", err)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to record operation")
		return
	}

	// Forward request to downstream BMC
	// Build the action path on the downstream BMC
	actionPath := fmt.Sprintf("%s/Actions/VirtualMedia.EjectMedia", resource.ODataID)

	// Create request to downstream BMC with empty body
	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, actionPath, strings.NewReader("{}"))
	if err != nil {
		slog.Error("Failed to create proxy request", "error", err)
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", "Failed to create proxy request")
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to create proxy request")
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	// Proxy request to BMC
	resp, err := h.bmcSvc.ProxyRequestToConnectionMethod(r.Context(), connectionMethodID, actionPath, proxyReq)
	if err != nil {
		slog.Error("Failed to proxy EjectMedia to BMC", "error", err, "connection_method", connectionMethodID, "path", actionPath)
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", fmt.Sprintf("BMC communication failed: %v", err))
		h.writeErrorResponse(w, http.StatusBadGateway, "Base.1.0.InternalError", fmt.Sprintf("Failed to communicate with BMC: %v", err))
		return
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("BMC returned error for EjectMedia", "status", resp.StatusCode, "body", string(bodyBytes))
		_ = h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "failed", fmt.Sprintf("BMC returned status %d", resp.StatusCode))
		
		// Forward the error response from BMC
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		return
	}

	// Update operation status to success
	if err := h.db.UpdateVirtualMediaOperationStatus(r.Context(), op.ID, "success", ""); err != nil {
		slog.Warn("Failed to update operation status", "error", err)
	}

	// Update resource state in database - clear image info
	var emptyImageURL *string
	var emptyImageName *string
	connectedVia := "NotConnected"
	if err := h.db.UpsertVirtualMediaResource(r.Context(), connectionMethodID, managerID, mediaID,
		resource.ODataID, resource.MediaTypes, resource.SupportedProtocols,
		emptyImageURL, emptyImageName, false, false, connectedVia); err != nil {
		slog.Warn("Failed to update resource state", "error", err)
	}

	// Return success (204 No Content per DMTF spec)
	w.WriteHeader(http.StatusNoContent)
}
