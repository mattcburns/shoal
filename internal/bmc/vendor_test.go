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
	"testing"
)

func TestDetectVendor_Dell(t *testing.T) {
	testCases := []struct {
		name         string
		managerData  map[string]interface{}
		expectedVendor VendorType
	}{
		{
			name: "Dell via Manufacturer",
			managerData: map[string]interface{}{
				"Manufacturer": "Dell Inc.",
				"Model":        "14G Monolithic",
			},
			expectedVendor: VendorDell,
		},
		{
			name: "Dell via Model (iDRAC)",
			managerData: map[string]interface{}{
				"Model": "iDRAC9",
			},
			expectedVendor: VendorDell,
		},
		{
			name: "Dell via OEM namespace",
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Dell": map[string]interface{}{
						"WebSocketEndpoint": "/redfish/v1/Dell/Managers/BMC/SerialInterfaces/1/WebSocket",
					},
				},
			},
			expectedVendor: VendorDell,
		},
		{
			name: "Dell via @odata.type",
			managerData: map[string]interface{}{
				"@odata.type": "#DellManager.v1_0_0.DellManager",
			},
			expectedVendor: VendorDell,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vendor := DetectVendor(tc.managerData)
			if vendor != tc.expectedVendor {
				t.Errorf("Expected vendor %s, got %s", tc.expectedVendor, vendor)
			}
		})
	}
}

func TestDetectVendor_Supermicro(t *testing.T) {
	testCases := []struct {
		name         string
		managerData  map[string]interface{}
		expectedVendor VendorType
	}{
		{
			name: "Supermicro via Manufacturer",
			managerData: map[string]interface{}{
				"Manufacturer": "Supermicro",
				"Model":        "X11",
			},
			expectedVendor: VendorSupermicro,
		},
		{
			name: "Supermicro via Manufacturer (alternate)",
			managerData: map[string]interface{}{
				"Manufacturer": "Super Micro Computer Inc.",
			},
			expectedVendor: VendorSupermicro,
		},
		{
			name: "Supermicro via OEM namespace",
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Supermicro": map[string]interface{}{
						"iKVMEndpoint": "/redfish/v1/Oem/Supermicro/iKVM",
					},
				},
			},
			expectedVendor: VendorSupermicro,
		},
		{
			name: "Supermicro via @odata.type",
			managerData: map[string]interface{}{
				"@odata.type": "#SupermicroManager.v1_0_0.Manager",
			},
			expectedVendor: VendorSupermicro,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vendor := DetectVendor(tc.managerData)
			if vendor != tc.expectedVendor {
				t.Errorf("Expected vendor %s, got %s", tc.expectedVendor, vendor)
			}
		})
	}
}

func TestDetectVendor_HPE(t *testing.T) {
	testCases := []struct {
		name         string
		managerData  map[string]interface{}
		expectedVendor VendorType
	}{
		{
			name: "HPE via Manufacturer",
			managerData: map[string]interface{}{
				"Manufacturer": "HPE",
				"Model":        "iLO 5",
			},
			expectedVendor: VendorHPE,
		},
		{
			name: "HPE via Manufacturer (Hewlett Packard)",
			managerData: map[string]interface{}{
				"Manufacturer": "Hewlett Packard Enterprise",
			},
			expectedVendor: VendorHPE,
		},
		{
			name: "HPE via Model (iLO)",
			managerData: map[string]interface{}{
				"Model": "iLO 5",
			},
			expectedVendor: VendorHPE,
		},
		{
			name: "HPE via OEM namespace (Hpe)",
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Hpe": map[string]interface{}{
						"IRCEndpoint": "/redfish/v1/Managers/1/RemoteConsole",
					},
				},
			},
			expectedVendor: VendorHPE,
		},
		{
			name: "HPE via OEM namespace (Hp)",
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Hp": map[string]interface{}{
						"IRCEndpoint": "/redfish/v1/Managers/1/RemoteConsole",
					},
				},
			},
			expectedVendor: VendorHPE,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vendor := DetectVendor(tc.managerData)
			if vendor != tc.expectedVendor {
				t.Errorf("Expected vendor %s, got %s", tc.expectedVendor, vendor)
			}
		})
	}
}

func TestDetectVendor_Unknown(t *testing.T) {
	testCases := []struct {
		name         string
		managerData  map[string]interface{}
	}{
		{
			name: "Generic BMC",
			managerData: map[string]interface{}{
				"Manufacturer": "Generic Manufacturer",
				"Model":        "Generic BMC",
			},
		},
		{
			name: "Empty data",
			managerData: map[string]interface{}{},
		},
		{
			name: "Unrecognized vendor",
			managerData: map[string]interface{}{
				"Manufacturer": "Some Other Vendor",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vendor := DetectVendor(tc.managerData)
			if vendor != VendorUnknown {
				t.Errorf("Expected vendor Unknown, got %s", vendor)
			}
		})
	}
}

func TestExtractVendorCapability_Dell(t *testing.T) {
	managerData := map[string]interface{}{
		"Manufacturer":    "Dell Inc.",
		"Model":           "iDRAC9",
		"FirmwareVersion": "4.40.00.00",
		"Oem": map[string]interface{}{
			"Dell": map[string]interface{}{
				"WebSocketEndpoint": "/redfish/v1/Dell/Managers/BMC/SerialInterfaces/1/WebSocket",
				"vKVMEndpoint":      "/redfish/v1/Dell/Managers/BMC/DellvKVM",
			},
		},
	}

	capability := ExtractVendorCapability(VendorDell, managerData)

	if capability.Vendor != VendorDell {
		t.Errorf("Expected vendor Dell, got %s", capability.Vendor)
	}
	if capability.Model != "iDRAC9" {
		t.Errorf("Expected model iDRAC9, got %s", capability.Model)
	}
	if capability.FirmwareVersion != "4.40.00.00" {
		t.Errorf("Expected firmware version 4.40.00.00, got %s", capability.FirmwareVersion)
	}
	if !capability.SupportsWebSocket {
		t.Error("Expected Dell to support WebSocket")
	}
	if !capability.SupportsHTML5Console {
		t.Error("Expected Dell to support HTML5 console")
	}
	if capability.SerialConsoleOEM == nil {
		t.Fatal("Expected SerialConsoleOEM to be set")
	}
	if capability.SerialConsoleOEM.WebSocketEndpoint != "/redfish/v1/Dell/Managers/BMC/SerialInterfaces/1/WebSocket" {
		t.Errorf("Unexpected serial console WebSocket endpoint: %s", capability.SerialConsoleOEM.WebSocketEndpoint)
	}
	if capability.GraphicalConsoleOEM == nil {
		t.Fatal("Expected GraphicalConsoleOEM to be set")
	}
	if capability.GraphicalConsoleOEM.HTML5Endpoint != "/redfish/v1/Dell/Managers/BMC/DellvKVM" {
		t.Errorf("Unexpected graphical console endpoint: %s", capability.GraphicalConsoleOEM.HTML5Endpoint)
	}
}

func TestExtractVendorCapability_Supermicro(t *testing.T) {
	managerData := map[string]interface{}{
		"Manufacturer":    "Supermicro",
		"Model":           "X11DPH-T",
		"FirmwareVersion": "1.73.14",
		"Oem": map[string]interface{}{
			"Supermicro": map[string]interface{}{
				"iKVMEndpoint":     "/redfish/v1/Oem/Supermicro/iKVM",
				"WebSocketSupport": true,
			},
		},
	}

	capability := ExtractVendorCapability(VendorSupermicro, managerData)

	if capability.Vendor != VendorSupermicro {
		t.Errorf("Expected vendor Supermicro, got %s", capability.Vendor)
	}
	if capability.Model != "X11DPH-T" {
		t.Errorf("Expected model X11DPH-T, got %s", capability.Model)
	}
	if !capability.SupportsHTML5Console {
		t.Error("Expected Supermicro to support HTML5 console")
	}
	if !capability.SupportsWebSocket {
		t.Error("Expected WebSocket support to be true")
	}
	if capability.GraphicalConsoleOEM == nil {
		t.Fatal("Expected GraphicalConsoleOEM to be set")
	}
	if capability.GraphicalConsoleOEM.HTML5Endpoint != "/redfish/v1/Oem/Supermicro/iKVM" {
		t.Errorf("Unexpected graphical console endpoint: %s", capability.GraphicalConsoleOEM.HTML5Endpoint)
	}
}

func TestExtractVendorCapability_HPE(t *testing.T) {
	managerData := map[string]interface{}{
		"Manufacturer":    "HPE",
		"Model":           "iLO 5",
		"FirmwareVersion": "2.44",
		"Oem": map[string]interface{}{
			"Hpe": map[string]interface{}{
				"IRCEndpoint":            "/redfish/v1/Managers/1/RemoteConsole",
				"SerialConsoleWebSocket": "/redfish/v1/Managers/1/SerialConsole/WebSocket",
			},
		},
	}

	capability := ExtractVendorCapability(VendorHPE, managerData)

	if capability.Vendor != VendorHPE {
		t.Errorf("Expected vendor HPE, got %s", capability.Vendor)
	}
	if capability.Model != "iLO 5" {
		t.Errorf("Expected model iLO 5, got %s", capability.Model)
	}
	if !capability.SupportsWebSocket {
		t.Error("Expected HPE to support WebSocket")
	}
	if !capability.SupportsHTML5Console {
		t.Error("Expected HPE to support HTML5 console")
	}
	if capability.SerialConsoleOEM == nil {
		t.Fatal("Expected SerialConsoleOEM to be set")
	}
	if capability.SerialConsoleOEM.WebSocketEndpoint != "/redfish/v1/Managers/1/SerialConsole/WebSocket" {
		t.Errorf("Unexpected serial console WebSocket endpoint: %s", capability.SerialConsoleOEM.WebSocketEndpoint)
	}
	if capability.GraphicalConsoleOEM == nil {
		t.Fatal("Expected GraphicalConsoleOEM to be set")
	}
	if capability.GraphicalConsoleOEM.HTML5Endpoint != "/redfish/v1/Managers/1/RemoteConsole" {
		t.Errorf("Unexpected graphical console endpoint: %s", capability.GraphicalConsoleOEM.HTML5Endpoint)
	}
}

func TestExtractVendorCapability_Unknown(t *testing.T) {
	managerData := map[string]interface{}{
		"Manufacturer":    "Generic Vendor",
		"Model":           "Generic BMC",
		"FirmwareVersion": "1.0",
	}

	capability := ExtractVendorCapability(VendorUnknown, managerData)

	if capability.Vendor != VendorUnknown {
		t.Errorf("Expected vendor Unknown, got %s", capability.Vendor)
	}
	if capability.Model != "Generic BMC" {
		t.Errorf("Expected model Generic BMC, got %s", capability.Model)
	}
	if capability.SupportsWebSocket {
		t.Error("Unknown vendor should not have WebSocket support set")
	}
	if capability.SupportsHTML5Console {
		t.Error("Unknown vendor should not have HTML5 console support set")
	}
}

func TestVendorCapability_JSON(t *testing.T) {
	original := &VendorCapability{
		Vendor:              VendorDell,
		Model:               "iDRAC9",
		FirmwareVersion:     "4.40.00.00",
		SupportsWebSocket:   true,
		SupportsHTML5Console: true,
		SerialConsoleOEM: &SerialConsoleOEMInfo{
			WebSocketEndpoint: "/ws/serial",
		},
		GraphicalConsoleOEM: &GraphicalConsoleOEMInfo{
			HTML5Endpoint:    "/console",
			SupportedMethods: []string{"HTML5", "WebSocket"},
		},
		AdditionalCapabilities: map[string]interface{}{
			"test_key": "test_value",
		},
	}

	// Convert to JSON
	jsonStr, err := original.ToJSON()
	if err != nil {
		t.Fatalf("Failed to convert to JSON: %v", err)
	}

	// Parse back from JSON
	parsed, err := VendorCapabilityFromJSON(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse from JSON: %v", err)
	}

	// Verify fields match
	if parsed.Vendor != original.Vendor {
		t.Errorf("Vendor mismatch: expected %s, got %s", original.Vendor, parsed.Vendor)
	}
	if parsed.Model != original.Model {
		t.Errorf("Model mismatch: expected %s, got %s", original.Model, parsed.Model)
	}
	if parsed.FirmwareVersion != original.FirmwareVersion {
		t.Errorf("FirmwareVersion mismatch: expected %s, got %s", original.FirmwareVersion, parsed.FirmwareVersion)
	}
	if parsed.SupportsWebSocket != original.SupportsWebSocket {
		t.Error("SupportsWebSocket mismatch")
	}
	if parsed.SupportsHTML5Console != original.SupportsHTML5Console {
		t.Error("SupportsHTML5Console mismatch")
	}
	if parsed.SerialConsoleOEM.WebSocketEndpoint != original.SerialConsoleOEM.WebSocketEndpoint {
		t.Error("SerialConsoleOEM.WebSocketEndpoint mismatch")
	}
	if parsed.GraphicalConsoleOEM.HTML5Endpoint != original.GraphicalConsoleOEM.HTML5Endpoint {
		t.Error("GraphicalConsoleOEM.HTML5Endpoint mismatch")
	}
}
