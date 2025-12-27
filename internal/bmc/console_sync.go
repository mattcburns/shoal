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

package bmc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"shoal/pkg/models"
)

// SyncConsoleCapabilities discovers and caches console capabilities from a connection method
// NOTE: Part of console pass-through feature (design/021_Console_Pass_Through.md, Milestone 1).
// Currently only exercised in tests; will be called periodically by connection method sync in Milestone 2.
func (s *Service) SyncConsoleCapabilities(ctx context.Context, connMethodID string) error {
	// Get connection method details
	cm, err := s.db.GetConnectionMethod(ctx, connMethodID)
	if err != nil {
		return fmt.Errorf("failed to get connection method: %w", err)
	}
	if cm == nil {
		return fmt.Errorf("connection method not found: %s", connMethodID)
	}

	// Parse aggregated managers
	var managers []map[string]interface{}
	if cm.AggregatedManagers != "" {
		if err := json.Unmarshal([]byte(cm.AggregatedManagers), &managers); err != nil {
			return fmt.Errorf("failed to parse aggregated managers: %w", err)
		}
	}

	// Sync console capabilities for each manager
	for _, mgr := range managers {
		managerID, ok := mgr["Id"].(string)
		if !ok {
			continue
		}

		if err := s.syncManagerConsoleCapabilities(ctx, cm, managerID); err != nil {
			slog.Debug("Failed to sync manager console capabilities",
				"connection_method", cm.Name, "manager", managerID, "error", err)
			// Continue with other managers
		}
	}

	return nil
}

// syncManagerConsoleCapabilities discovers console capabilities from a specific manager
func (s *Service) syncManagerConsoleCapabilities(ctx context.Context, cm *models.ConnectionMethod, managerID string) error {
	// Query Manager resource
	managerPath := fmt.Sprintf("/redfish/v1/Managers/%s", managerID)

	req, err := http.NewRequestWithContext(ctx, "GET", managerPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.ProxyRequestToConnectionMethod(ctx, cm.ID, managerPath, req)
	if err != nil {
		return fmt.Errorf("failed to query manager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse Manager response to extract console properties
	var manager map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&manager); err != nil {
		return fmt.Errorf("failed to decode manager response: %w", err)
	}

	// Detect vendor from Manager resource
	vendor := DetectVendor(manager)
	slog.Debug("Detected BMC vendor",
		"connection_method", cm.Name,
		"manager", managerID,
		"vendor", vendor)

	// Extract vendor-specific capabilities
	vendorCapability := ExtractVendorCapability(vendor, manager)

	// Extract SerialConsole capability
	if serialConsole, ok := manager["SerialConsole"].(map[string]interface{}); ok {
		if err := s.processConsoleCapability(ctx, cm.ID, managerID, models.ConsoleTypeSerial, serialConsole, vendorCapability); err != nil {
			slog.Warn("Failed to process serial console capability", "error", err)
		}
	}

	// Extract GraphicalConsole capability
	if graphicalConsole, ok := manager["GraphicalConsole"].(map[string]interface{}); ok {
		if err := s.processConsoleCapability(ctx, cm.ID, managerID, models.ConsoleTypeGraphical, graphicalConsole, vendorCapability); err != nil {
			slog.Warn("Failed to process graphical console capability", "error", err)
		}
	}

	return nil
}

// processConsoleCapability processes and stores a console capability with vendor-specific enhancements
func (s *Service) processConsoleCapability(ctx context.Context, connMethodID, managerID string, consoleType models.ConsoleType, consoleData map[string]interface{}, vendorCapability *VendorCapability) error {
	capability := &models.ConsoleCapability{
		ConnectionMethodID: connMethodID,
		ManagerID:          managerID,
		ConsoleType:        consoleType,
	}

	// Extract ServiceEnabled
	if enabled, ok := consoleData["ServiceEnabled"].(bool); ok {
		capability.ServiceEnabled = enabled
	}

	// Extract MaxConcurrentSessions
	if maxSessions, ok := consoleData["MaxConcurrentSessions"].(float64); ok {
		capability.MaxConcurrentSession = int(maxSessions)
	}

	// Extract ConnectTypesSupported
	if connectTypes, ok := consoleData["ConnectTypesSupported"].([]interface{}); ok {
		connectTypesJSON, err := json.Marshal(connectTypes)
		if err == nil {
			capability.ConnectTypes = string(connectTypesJSON)
		}
	}

	// Build enhanced vendor data with both console OEM and vendor capability info
	vendorData := make(map[string]interface{})

	// Include console-specific OEM data
	if oem, ok := consoleData["Oem"].(map[string]interface{}); ok {
		vendorData["console_oem"] = oem
	}

	// Include vendor capability information
	if vendorCapability != nil {
		vendorData["vendor"] = vendorCapability.Vendor
		vendorData["model"] = vendorCapability.Model
		vendorData["firmware_version"] = vendorCapability.FirmwareVersion
		vendorData["supports_websocket"] = vendorCapability.SupportsWebSocket
		vendorData["supports_html5_console"] = vendorCapability.SupportsHTML5Console

		// Add console-type-specific OEM endpoints
		if consoleType == models.ConsoleTypeSerial && vendorCapability.SerialConsoleOEM != nil {
			vendorData["serial_console_oem"] = vendorCapability.SerialConsoleOEM
		}
		if consoleType == models.ConsoleTypeGraphical && vendorCapability.GraphicalConsoleOEM != nil {
			vendorData["graphical_console_oem"] = vendorCapability.GraphicalConsoleOEM
		}

		// Include additional vendor-specific capabilities
		if len(vendorCapability.AdditionalCapabilities) > 0 {
			vendorData["additional_capabilities"] = vendorCapability.AdditionalCapabilities
		}
	}

	// Marshal vendor data to JSON
	if len(vendorData) > 0 {
		vendorDataJSON, err := json.Marshal(vendorData)
		if err == nil {
			capability.VendorData = string(vendorDataJSON)
		}
	}

	// Store capability in database
	if err := s.db.UpsertConsoleCapability(ctx, capability); err != nil {
		return fmt.Errorf("failed to upsert console capability: %w", err)
	}

	slog.Debug("Synced console capability",
		"connection_method", connMethodID,
		"manager", managerID,
		"console_type", consoleType,
		"enabled", capability.ServiceEnabled,
		"vendor", vendorCapability.Vendor)

	return nil
}
