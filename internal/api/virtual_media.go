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
	"log/slog"
	"net/http"
	"strings"

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
