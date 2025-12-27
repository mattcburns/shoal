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
	"net/http"
	"net/http/httptest"
	"testing"

	"shoal/internal/database"
	"shoal/pkg/models"
)

func TestSyncConsoleCapabilities(t *testing.T) {
	// Create test database
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create mock BMC server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Managers/BMC":
			// Return manager with console capabilities and Dell vendor info
			manager := map[string]interface{}{
				"@odata.id":       "/redfish/v1/Managers/BMC",
				"@odata.type":     "#Manager.v1_10_0.Manager",
				"Id":              "BMC",
				"Name":            "Manager",
				"Manufacturer":    "Dell Inc.",
				"Model":           "iDRAC9",
				"FirmwareVersion": "4.40.00.00",
				"SerialConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 1,
					"ConnectTypesSupported": []string{"Oem"},
					"Oem": map[string]interface{}{
						"Dell": map[string]interface{}{
							"WebSocketEndpoint": "/redfish/v1/Dell/Managers/BMC/SerialInterfaces/1/WebSocket",
						},
					},
				},
				"GraphicalConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 4,
					"ConnectTypesSupported": []string{"KVMIP", "Oem"},
					"Oem": map[string]interface{}{
						"Dell": map[string]interface{}{
							"vKVMEndpoint": "/redfish/v1/Dell/Managers/BMC/DellvKVM",
						},
					},
				},
				"Oem": map[string]interface{}{
					"Dell": map[string]interface{}{
						"WebSocketEndpoint": "/redfish/v1/Dell/Managers/BMC/SerialInterfaces/1/WebSocket",
						"vKVMEndpoint":      "/redfish/v1/Dell/Managers/BMC/DellvKVM",
					},
				},
			}
			json.NewEncoder(w).Encode(manager)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create connection method
	managers := []map[string]interface{}{
		{"Id": "BMC"},
	}
	managersJSON, _ := json.Marshal(managers)

	cm := &models.ConnectionMethod{
		ID:                   "test-cm",
		Name:                 "Test BMC",
		ConnectionMethodType: "Redfish",
		Address:              server.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   string(managersJSON),
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create service
	service := New(db)

	t.Run("SyncConsoleCapabilities", func(t *testing.T) {
		err := service.SyncConsoleCapabilities(ctx, "test-cm")
		if err != nil {
			t.Errorf("SyncConsoleCapabilities failed: %v", err)
		}

		// Verify serial console capability was stored
		serialCap, err := db.GetConsoleCapability(ctx, "test-cm", "BMC", models.ConsoleTypeSerial)
		if err != nil {
			t.Errorf("Failed to get serial capability: %v", err)
		}
		if serialCap == nil {
			t.Fatal("Expected serial console capability to be stored")
		}
		if !serialCap.ServiceEnabled {
			t.Error("Expected serial console to be enabled")
		}
		if serialCap.MaxConcurrentSession != 1 {
			t.Errorf("Expected MaxConcurrentSession=1, got %d", serialCap.MaxConcurrentSession)
		}

		// Verify vendor data is stored
		var vendorData map[string]interface{}
		if err := json.Unmarshal([]byte(serialCap.VendorData), &vendorData); err != nil {
			t.Errorf("Failed to parse vendor data: %v", err)
		}
		if vendor, ok := vendorData["vendor"].(string); !ok || vendor != "Dell" {
			t.Errorf("Expected vendor Dell, got %v", vendorData["vendor"])
		}
		if model, ok := vendorData["model"].(string); !ok || model != "iDRAC9" {
			t.Errorf("Expected model iDRAC9, got %v", vendorData["model"])
		}
		if wsSupport, ok := vendorData["supports_websocket"].(bool); !ok || !wsSupport {
			t.Error("Expected supports_websocket to be true")
		}

		// Verify graphical console capability was stored
		graphicalCap, err := db.GetConsoleCapability(ctx, "test-cm", "BMC", models.ConsoleTypeGraphical)
		if err != nil {
			t.Errorf("Failed to get graphical capability: %v", err)
		}
		if graphicalCap == nil {
			t.Fatal("Expected graphical console capability to be stored")
		}
		if !graphicalCap.ServiceEnabled {
			t.Error("Expected graphical console to be enabled")
		}
		if graphicalCap.MaxConcurrentSession != 4 {
			t.Errorf("Expected MaxConcurrentSession=4, got %d", graphicalCap.MaxConcurrentSession)
		}

		// Verify vendor data for graphical console
		var gfxVendorData map[string]interface{}
		if err := json.Unmarshal([]byte(graphicalCap.VendorData), &gfxVendorData); err != nil {
			t.Errorf("Failed to parse vendor data: %v", err)
		}
		if html5Support, ok := gfxVendorData["supports_html5_console"].(bool); !ok || !html5Support {
			t.Error("Expected supports_html5_console to be true")
		}
	})

	t.Run("SyncConsoleCapabilities_Update", func(t *testing.T) {
		// Update the mock to return different values
		server2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/redfish/v1/Managers/BMC" {
				manager := map[string]interface{}{
					"@odata.id":       "/redfish/v1/Managers/BMC",
					"Id":              "BMC",
					"Manufacturer":    "Dell Inc.",
					"Model":           "iDRAC9",
					"FirmwareVersion": "4.50.00.00", // Updated firmware
					"SerialConsole": map[string]interface{}{
						"ServiceEnabled":        false, // Changed
						"MaxConcurrentSessions": 2,     // Changed
						"ConnectTypesSupported": []string{"Oem", "SSH"},
					},
					"GraphicalConsole": map[string]interface{}{
						"ServiceEnabled":        true,
						"MaxConcurrentSessions": 8, // Changed
						"ConnectTypesSupported": []string{"KVMIP"},
					},
					"Oem": map[string]interface{}{
						"Dell": map[string]interface{}{},
					},
				}
				json.NewEncoder(w).Encode(manager)
			}
		}))
		defer server2.Close()

		// Update connection method address
		cm.Address = server2.URL
		if err := db.DeleteConnectionMethod(ctx, "test-cm"); err != nil {
			t.Fatalf("Failed to delete old connection method: %v", err)
		}
		if err := db.CreateConnectionMethod(ctx, cm); err != nil {
			t.Fatalf("Failed to create updated connection method: %v", err)
		}

		// Sync again
		err := service.SyncConsoleCapabilities(ctx, "test-cm")
		if err != nil {
			t.Errorf("SyncConsoleCapabilities failed: %v", err)
		}

		// Verify serial console was updated
		serialCap, err := db.GetConsoleCapability(ctx, "test-cm", "BMC", models.ConsoleTypeSerial)
		if err != nil {
			t.Errorf("Failed to get serial capability: %v", err)
		}
		if serialCap.ServiceEnabled != false {
			t.Error("Expected serial console to be disabled after update")
		}
		if serialCap.MaxConcurrentSession != 2 {
			t.Errorf("Expected MaxConcurrentSession=2 after update, got %d", serialCap.MaxConcurrentSession)
		}

		// Verify firmware version was updated in vendor data
		var vendorData map[string]interface{}
		if err := json.Unmarshal([]byte(serialCap.VendorData), &vendorData); err != nil {
			t.Errorf("Failed to parse vendor data: %v", err)
		}
		if fwVersion, ok := vendorData["firmware_version"].(string); !ok || fwVersion != "4.50.00.00" {
			t.Errorf("Expected firmware_version 4.50.00.00, got %v", vendorData["firmware_version"])
		}

		// Verify graphical console was updated
		graphicalCap, err := db.GetConsoleCapability(ctx, "test-cm", "BMC", models.ConsoleTypeGraphical)
		if err != nil {
			t.Errorf("Failed to get graphical capability: %v", err)
		}
		if graphicalCap.MaxConcurrentSession != 8 {
			t.Errorf("Expected MaxConcurrentSession=8 after update, got %d", graphicalCap.MaxConcurrentSession)
		}
	})
}

func TestSyncConsoleCapabilities_NoConsole(t *testing.T) {
	// Test BMC that doesn't support console
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create mock BMC without console properties
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Managers/BMC" {
			manager := map[string]interface{}{
				"@odata.id": "/redfish/v1/Managers/BMC",
				"Id":        "BMC",
				"Name":      "Manager",
				// No console properties
			}
			json.NewEncoder(w).Encode(manager)
		}
	}))
	defer server.Close()

	managers := []map[string]interface{}{{"Id": "BMC"}}
	managersJSON, _ := json.Marshal(managers)

	cm := &models.ConnectionMethod{
		ID:                   "test-cm-noconsole",
		Name:                 "Test BMC No Console",
		ConnectionMethodType: "Redfish",
		Address:              server.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   string(managersJSON),
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)

	// Should not fail, just not store any capabilities
	err = service.SyncConsoleCapabilities(ctx, "test-cm-noconsole")
	if err != nil {
		t.Errorf("SyncConsoleCapabilities should not fail for BMC without console: %v", err)
	}

	// Verify no capabilities were stored
	caps, err := db.GetConsoleCapabilities(ctx, "test-cm-noconsole", "BMC")
	if err != nil {
		t.Errorf("Failed to get capabilities: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("Expected 0 capabilities for BMC without console, got %d", len(caps))
	}
}

func TestSyncConsoleCapabilities_MultipleManagers(t *testing.T) {
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create mock BMC with multiple managers
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Managers/BMC1":
			manager := map[string]interface{}{
				"@odata.id": "/redfish/v1/Managers/BMC1",
				"Id":        "BMC1",
				"SerialConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 1,
					"ConnectTypesSupported": []string{"Oem"},
				},
			}
			json.NewEncoder(w).Encode(manager)
		case "/redfish/v1/Managers/BMC2":
			manager := map[string]interface{}{
				"@odata.id": "/redfish/v1/Managers/BMC2",
				"Id":        "BMC2",
				"GraphicalConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 2,
					"ConnectTypesSupported": []string{"KVMIP"},
				},
			}
			json.NewEncoder(w).Encode(manager)
		}
	}))
	defer server.Close()

	managers := []map[string]interface{}{
		{"Id": "BMC1"},
		{"Id": "BMC2"},
	}
	managersJSON, _ := json.Marshal(managers)

	cm := &models.ConnectionMethod{
		ID:                   "test-cm-multi",
		Name:                 "Test Multi Manager",
		ConnectionMethodType: "Redfish",
		Address:              server.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   string(managersJSON),
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)

	err = service.SyncConsoleCapabilities(ctx, "test-cm-multi")
	if err != nil {
		t.Errorf("SyncConsoleCapabilities failed: %v", err)
	}

	// Verify BMC1 has serial console
	cap1, err := db.GetConsoleCapability(ctx, "test-cm-multi", "BMC1", models.ConsoleTypeSerial)
	if err != nil {
		t.Errorf("Failed to get BMC1 capability: %v", err)
	}
	if cap1 == nil {
		t.Error("Expected serial console capability for BMC1")
	}

	// Verify BMC2 has graphical console
	cap2, err := db.GetConsoleCapability(ctx, "test-cm-multi", "BMC2", models.ConsoleTypeGraphical)
	if err != nil {
		t.Errorf("Failed to get BMC2 capability: %v", err)
	}
	if cap2 == nil {
		t.Error("Expected graphical console capability for BMC2")
	}
}

func TestProcessConsoleCapability_ConnectTypes(t *testing.T) {
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-types",
		Name:                 "Test Connect Types",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)

	// Test various ConnectTypesSupported values
	testCases := []struct {
		name         string
		connectTypes []interface{}
		expected     string
	}{
		{
			name:         "Single type",
			connectTypes: []interface{}{"Oem"},
			expected:     `["Oem"]`,
		},
		{
			name:         "Multiple types",
			connectTypes: []interface{}{"KVMIP", "Oem"},
			expected:     `["KVMIP","Oem"]`,
		},
		{
			name:         "Empty array",
			connectTypes: []interface{}{},
			expected:     `[]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			consoleData := map[string]interface{}{
				"ServiceEnabled":        true,
				"MaxConcurrentSessions": 1,
				"ConnectTypesSupported": tc.connectTypes,
			}

			// Create a mock vendor capability
			vendorCap := &VendorCapability{
				Vendor:               VendorUnknown,
				SupportsWebSocket:    false,
				SupportsHTML5Console: false,
			}

			err := service.processConsoleCapability(ctx, "test-cm-types", fmt.Sprintf("MGR-%s", tc.name), models.ConsoleTypeSerial, consoleData, vendorCap)
			if err != nil {
				t.Errorf("processConsoleCapability failed: %v", err)
			}

			cap, err := db.GetConsoleCapability(ctx, "test-cm-types", fmt.Sprintf("MGR-%s", tc.name), models.ConsoleTypeSerial)
			if err != nil {
				t.Errorf("Failed to get capability: %v", err)
			}
			if cap.ConnectTypes != tc.expected {
				t.Errorf("Expected ConnectTypes=%s, got %s", tc.expected, cap.ConnectTypes)
			}
		})
	}
}

func TestSyncConsoleCapabilities_Supermicro(t *testing.T) {
	// Test Supermicro vendor detection and capability extraction
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create mock Supermicro BMC server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Managers/BMC" {
			manager := map[string]interface{}{
				"@odata.id":       "/redfish/v1/Managers/BMC",
				"Id":              "BMC",
				"Manufacturer":    "Supermicro",
				"Model":           "X11DPH-T",
				"FirmwareVersion": "1.73.14",
				"GraphicalConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 2,
					"ConnectTypesSupported": []string{"KVMIP"},
				},
				"Oem": map[string]interface{}{
					"Supermicro": map[string]interface{}{
						"iKVMEndpoint":     "/redfish/v1/Oem/Supermicro/iKVM",
						"WebSocketSupport": true,
					},
				},
			}
			json.NewEncoder(w).Encode(manager)
		}
	}))
	defer server.Close()

	managers := []map[string]interface{}{{"Id": "BMC"}}
	managersJSON, _ := json.Marshal(managers)

	cm := &models.ConnectionMethod{
		ID:                   "test-smc",
		Name:                 "Test Supermicro",
		ConnectionMethodType: "Redfish",
		Address:              server.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   string(managersJSON),
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)
	err = service.SyncConsoleCapabilities(ctx, "test-smc")
	if err != nil {
		t.Errorf("SyncConsoleCapabilities failed: %v", err)
	}

	// Verify graphical console capability with Supermicro vendor data
	cap, err := db.GetConsoleCapability(ctx, "test-smc", "BMC", models.ConsoleTypeGraphical)
	if err != nil {
		t.Fatalf("Failed to get capability: %v", err)
	}
	if cap == nil {
		t.Fatal("Expected capability to be stored")
	}

	var vendorData map[string]interface{}
	if err := json.Unmarshal([]byte(cap.VendorData), &vendorData); err != nil {
		t.Fatalf("Failed to parse vendor data: %v", err)
	}

	if vendor, ok := vendorData["vendor"].(string); !ok || vendor != "Supermicro" {
		t.Errorf("Expected vendor Supermicro, got %v", vendorData["vendor"])
	}
	if model, ok := vendorData["model"].(string); !ok || model != "X11DPH-T" {
		t.Errorf("Expected model X11DPH-T, got %v", vendorData["model"])
	}
	if wsSupport, ok := vendorData["supports_websocket"].(bool); !ok || !wsSupport {
		t.Error("Expected supports_websocket to be true for Supermicro")
	}
}

func TestSyncConsoleCapabilities_HPE(t *testing.T) {
	// Test HPE vendor detection and capability extraction
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create mock HPE iLO server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Managers/1" {
			manager := map[string]interface{}{
				"@odata.id":       "/redfish/v1/Managers/1",
				"Id":              "1",
				"Manufacturer":    "HPE",
				"Model":           "iLO 5",
				"FirmwareVersion": "2.44",
				"SerialConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 1,
					"ConnectTypesSupported": []string{"Oem"},
				},
				"GraphicalConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 6,
					"ConnectTypesSupported": []string{"KVMIP", "Oem"},
				},
				"Oem": map[string]interface{}{
					"Hpe": map[string]interface{}{
						"IRCEndpoint":            "/redfish/v1/Managers/1/RemoteConsole",
						"SerialConsoleWebSocket": "/redfish/v1/Managers/1/SerialConsole/WebSocket",
					},
				},
			}
			json.NewEncoder(w).Encode(manager)
		}
	}))
	defer server.Close()

	managers := []map[string]interface{}{{"Id": "1"}}
	managersJSON, _ := json.Marshal(managers)

	cm := &models.ConnectionMethod{
		ID:                   "test-hpe",
		Name:                 "Test HPE",
		ConnectionMethodType: "Redfish",
		Address:              server.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   string(managersJSON),
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)
	err = service.SyncConsoleCapabilities(ctx, "test-hpe")
	if err != nil {
		t.Errorf("SyncConsoleCapabilities failed: %v", err)
	}

	// Verify serial console capability with HPE vendor data
	serialCap, err := db.GetConsoleCapability(ctx, "test-hpe", "1", models.ConsoleTypeSerial)
	if err != nil {
		t.Fatalf("Failed to get serial capability: %v", err)
	}
	if serialCap == nil {
		t.Fatal("Expected serial capability to be stored")
	}

	var serialVendorData map[string]interface{}
	if err := json.Unmarshal([]byte(serialCap.VendorData), &serialVendorData); err != nil {
		t.Fatalf("Failed to parse vendor data: %v", err)
	}

	if vendor, ok := serialVendorData["vendor"].(string); !ok || vendor != "HPE" {
		t.Errorf("Expected vendor HPE, got %v", serialVendorData["vendor"])
	}
	if model, ok := serialVendorData["model"].(string); !ok || model != "iLO 5" {
		t.Errorf("Expected model iLO 5, got %v", serialVendorData["model"])
	}
	if wsSupport, ok := serialVendorData["supports_websocket"].(bool); !ok || !wsSupport {
		t.Error("Expected supports_websocket to be true for HPE")
	}

	// Verify graphical console capability
	graphicalCap, err := db.GetConsoleCapability(ctx, "test-hpe", "1", models.ConsoleTypeGraphical)
	if err != nil {
		t.Fatalf("Failed to get graphical capability: %v", err)
	}
	if graphicalCap == nil {
		t.Fatal("Expected graphical capability to be stored")
	}

	var gfxVendorData map[string]interface{}
	if err := json.Unmarshal([]byte(graphicalCap.VendorData), &gfxVendorData); err != nil {
		t.Fatalf("Failed to parse graphical vendor data: %v", err)
	}

	if html5Support, ok := gfxVendorData["supports_html5_console"].(bool); !ok || !html5Support {
		t.Error("Expected supports_html5_console to be true for HPE")
	}
}
