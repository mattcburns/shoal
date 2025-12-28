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
	"testing"
	"time"

	"shoal/internal/database"
	"shoal/pkg/models"
)

func TestEnhanceManagerWithConsole_NoCapabilities(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := &Handler{db: db}

	// Create empty manager data
	managerData := map[string]interface{}{
		"@odata.type": "#Manager.v1_10_0.Manager",
		"@odata.id":   "/redfish/v1/Managers/test",
		"Id":          "test",
		"Name":        "Test Manager",
	}

	// Enhance with empty capabilities
	handler.enhanceManagerWithConsole(managerData, "test", []models.ConsoleCapability{})

	// Check that SerialConsole property exists with defaults
	serialConsole, ok := managerData["SerialConsole"].(map[string]interface{})
	if !ok {
		t.Fatal("SerialConsole property not added")
	}
	if serialConsole["ServiceEnabled"] != false {
		t.Errorf("Expected ServiceEnabled=false, got %v", serialConsole["ServiceEnabled"])
	}
	if serialConsole["MaxConcurrentSessions"] != 0 {
		t.Errorf("Expected MaxConcurrentSessions=0, got %v", serialConsole["MaxConcurrentSessions"])
	}

	// Check that GraphicalConsole property exists with defaults
	graphicalConsole, ok := managerData["GraphicalConsole"].(map[string]interface{})
	if !ok {
		t.Fatal("GraphicalConsole property not added")
	}
	if graphicalConsole["ServiceEnabled"] != false {
		t.Errorf("Expected ServiceEnabled=false, got %v", graphicalConsole["ServiceEnabled"])
	}

	// Check that Oem.Shoal.ConsoleActions exists
	oem, ok := managerData["Oem"].(map[string]interface{})
	if !ok {
		t.Fatal("Oem property not added")
	}
	shoalOEM, ok := oem["Shoal"].(map[string]interface{})
	if !ok {
		t.Fatal("Oem.Shoal property not added")
	}
	consoleActions, ok := shoalOEM["ConsoleActions"].(map[string]interface{})
	if !ok {
		t.Fatal("Oem.Shoal.ConsoleActions property not added")
	}

	// Check ConnectSerialConsole action
	serialAction, ok := consoleActions["#Manager.ConnectSerialConsole"].(map[string]string)
	if !ok {
		t.Fatal("ConnectSerialConsole action not added")
	}
	expectedTarget := "/redfish/v1/Managers/test/Actions/Oem/Shoal.ConnectSerialConsole"
	if serialAction["target"] != expectedTarget {
		t.Errorf("Expected target %s, got %s", expectedTarget, serialAction["target"])
	}

	// Check ConnectGraphicalConsole action
	graphicalAction, ok := consoleActions["#Manager.ConnectGraphicalConsole"].(map[string]string)
	if !ok {
		t.Fatal("ConnectGraphicalConsole action not added")
	}
	expectedTarget = "/redfish/v1/Managers/test/Actions/Oem/Shoal.ConnectGraphicalConsole"
	if graphicalAction["target"] != expectedTarget {
		t.Errorf("Expected target %s, got %s", expectedTarget, graphicalAction["target"])
	}

	// Check ConsoleSessions link
	consoleSessions, ok := shoalOEM["ConsoleSessions"].(map[string]string)
	if !ok {
		t.Fatal("ConsoleSessions property not added")
	}
	expectedODataID := "/redfish/v1/Managers/test/Oem/Shoal/ConsoleSessions"
	if consoleSessions["@odata.id"] != expectedODataID {
		t.Errorf("Expected @odata.id %s, got %s", expectedODataID, consoleSessions["@odata.id"])
	}
}

func TestEnhanceManagerWithConsole_WithCapabilities(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := &Handler{db: db}

	// Create manager data
	managerData := map[string]interface{}{
		"@odata.type": "#Manager.v1_10_0.Manager",
		"@odata.id":   "/redfish/v1/Managers/test",
		"Id":          "test",
		"Name":        "Test Manager",
	}

	// Create console capabilities
	capabilities := []models.ConsoleCapability{
		{
			ID:                   1,
			ConnectionMethodID:   "test-cm",
			ManagerID:            "test",
			ConsoleType:          models.ConsoleTypeSerial,
			ServiceEnabled:       true,
			MaxConcurrentSession: 1,
			ConnectTypes:         `["Oem"]`,
			LastUpdated:          time.Now(),
		},
		{
			ID:                   2,
			ConnectionMethodID:   "test-cm",
			ManagerID:            "test",
			ConsoleType:          models.ConsoleTypeGraphical,
			ServiceEnabled:       true,
			MaxConcurrentSession: 4,
			ConnectTypes:         `["KVMIP", "Oem"]`,
			LastUpdated:          time.Now(),
		},
	}

	// Enhance with capabilities
	handler.enhanceManagerWithConsole(managerData, "test", capabilities)

	// Check SerialConsole property
	serialConsole, ok := managerData["SerialConsole"].(map[string]interface{})
	if !ok {
		t.Fatal("SerialConsole property not added")
	}
	if serialConsole["ServiceEnabled"] != true {
		t.Errorf("Expected ServiceEnabled=true, got %v", serialConsole["ServiceEnabled"])
	}
	if serialConsole["MaxConcurrentSessions"] != 1 {
		t.Errorf("Expected MaxConcurrentSessions=1, got %v", serialConsole["MaxConcurrentSessions"])
	}
	connectTypes, ok := serialConsole["ConnectTypesSupported"].([]string)
	if !ok {
		t.Fatal("ConnectTypesSupported not an array")
	}
	if len(connectTypes) != 1 || connectTypes[0] != "Oem" {
		t.Errorf("Expected ConnectTypesSupported=['Oem'], got %v", connectTypes)
	}

	// Check GraphicalConsole property
	graphicalConsole, ok := managerData["GraphicalConsole"].(map[string]interface{})
	if !ok {
		t.Fatal("GraphicalConsole property not added")
	}
	if graphicalConsole["ServiceEnabled"] != true {
		t.Errorf("Expected ServiceEnabled=true, got %v", graphicalConsole["ServiceEnabled"])
	}
	if graphicalConsole["MaxConcurrentSessions"] != 4 {
		t.Errorf("Expected MaxConcurrentSessions=4, got %v", graphicalConsole["MaxConcurrentSessions"])
	}
	connectTypes, ok = graphicalConsole["ConnectTypesSupported"].([]string)
	if !ok {
		t.Fatal("ConnectTypesSupported not an array")
	}
	if len(connectTypes) != 2 {
		t.Errorf("Expected 2 connect types, got %d", len(connectTypes))
	}
}

func TestEnhanceManagerWithConsole_PreservesExistingOEM(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := &Handler{db: db}

	// Create manager data with existing OEM properties
	managerData := map[string]interface{}{
		"@odata.type": "#Manager.v1_10_0.Manager",
		"@odata.id":   "/redfish/v1/Managers/test",
		"Id":          "test",
		"Name":        "Test Manager",
		"Oem": map[string]interface{}{
			"Dell": map[string]interface{}{
				"DellAttributes": map[string]string{
					"FirmwareVersion": "1.2.3",
				},
			},
		},
	}

	// Enhance with console
	handler.enhanceManagerWithConsole(managerData, "test", []models.ConsoleCapability{})

	// Check that existing OEM properties are preserved
	oem, ok := managerData["Oem"].(map[string]interface{})
	if !ok {
		t.Fatal("Oem property removed")
	}
	
	dellOEM, ok := oem["Dell"].(map[string]interface{})
	if !ok {
		t.Fatal("Dell OEM property removed")
	}
	
	dellAttrs, ok := dellOEM["DellAttributes"].(map[string]string)
	if !ok {
		t.Fatal("DellAttributes removed")
	}
	
	if dellAttrs["FirmwareVersion"] != "1.2.3" {
		t.Errorf("Expected FirmwareVersion=1.2.3, got %v", dellAttrs["FirmwareVersion"])
	}

	// Check that Shoal OEM was added alongside existing OEM
	shoalOEM, ok := oem["Shoal"].(map[string]interface{})
	if !ok {
		t.Fatal("Shoal OEM property not added")
	}
	
	if shoalOEM["@odata.type"] != "#ShoalManager.v1_0_0.ShoalManager" {
		t.Error("Shoal OEM @odata.type not set correctly")
	}
}

func TestParseConsolePath(t *testing.T) {
	testCases := []struct {
		name              string
		path              string
		expectedManager   string
		expectedSession   string
		expectedAction    string
	}{
		{
			name:            "ConnectSerialConsole action",
			path:            "/redfish/v1/Managers/test-mgr/Actions/Oem/Shoal.ConnectSerialConsole",
			expectedManager: "test-mgr",
			expectedSession: "",
			expectedAction:  "connect",
		},
		{
			name:            "ConsoleSessions collection",
			path:            "/redfish/v1/Managers/test-mgr/Oem/Shoal/ConsoleSessions",
			expectedManager: "test-mgr",
			expectedSession: "",
			expectedAction:  "collection",
		},
		{
			name:            "ConsoleSession resource",
			path:            "/redfish/v1/Managers/test-mgr/Oem/Shoal/ConsoleSessions/session-123",
			expectedManager: "test-mgr",
			expectedSession: "session-123",
			expectedAction:  "session",
		},
		{
			name:            "Disconnect action",
			path:            "/redfish/v1/Managers/test-mgr/Oem/Shoal/ConsoleSessions/session-123/Actions/Disconnect",
			expectedManager: "test-mgr",
			expectedSession: "session-123",
			expectedAction:  "disconnect",
		},
		{
			name:            "Invalid path",
			path:            "/redfish/v1/Systems/sys1",
			expectedManager: "",
			expectedSession: "",
			expectedAction:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager, session, action := parseConsolePath(tc.path)
			
			if manager != tc.expectedManager {
				t.Errorf("Expected manager %q, got %q", tc.expectedManager, manager)
			}
			if session != tc.expectedSession {
				t.Errorf("Expected session %q, got %q", tc.expectedSession, session)
			}
			if action != tc.expectedAction {
				t.Errorf("Expected action %q, got %q", tc.expectedAction, action)
			}
		})
	}
}

func setupTestConsoleCapability(t *testing.T, db *database.DB, cmID string) {
	t.Helper()
	ctx := context.Background()

	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cmID,
		ManagerID:            "",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       true,
		MaxConcurrentSession: 1,
		ConnectTypes:         `["Oem"]`,
	}

	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}
}
