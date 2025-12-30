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

func TestExtractDellGraphicalConsoleWebSocketURL(t *testing.T) {
	testCases := []struct {
		name          string
		oem           map[string]interface{}
		bmcAddress    string
		managerID     string
		expectedURL   string
		expectError   bool
	}{
		{
			name: "Direct vKVM WebSocket endpoint",
			oem: map[string]interface{}{
				"Dell": map[string]interface{}{
					"vKVMWebSocketEndpoint": "/redfish/v1/Dell/Managers/iDRAC.Embedded.1/vKVM/WebSocket",
				},
			},
			bmcAddress:  "https://10.0.0.1",
			managerID:   "iDRAC.Embedded.1",
			expectedURL: "wss://10.0.0.1/redfish/v1/Dell/Managers/iDRAC.Embedded.1/vKVM/WebSocket",
			expectError: false,
		},
		{
			name: "vKVM endpoint without WebSocket",
			oem: map[string]interface{}{
				"Dell": map[string]interface{}{
					"vKVMEndpoint": "/redfish/v1/Dell/Managers/iDRAC.Embedded.1/DellvKVM",
				},
			},
			bmcAddress:  "https://10.0.0.1",
			managerID:   "iDRAC.Embedded.1",
			expectedURL: "wss://10.0.0.1/redfish/v1/Dell/Managers/iDRAC.Embedded.1/DellvKVM",
			expectError: false,
		},
		{
			name: "Fallback to default path",
			oem: map[string]interface{}{
				"Dell": map[string]interface{}{},
			},
			bmcAddress:  "https://10.0.0.1",
			managerID:   "iDRAC.Embedded.1",
			expectedURL: "wss://10.0.0.1/redfish/v1/Dell/Managers/iDRAC.Embedded.1/DellvKVM",
			expectError: false,
		},
		{
			name:        "No Dell OEM data",
			oem:         map[string]interface{}{},
			bmcAddress:  "https://10.0.0.1",
			managerID:   "iDRAC.Embedded.1",
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := extractDellGraphicalConsoleWebSocketURL(tc.oem, tc.bmcAddress, tc.managerID)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if url != tc.expectedURL {
					t.Errorf("Expected URL %q, got %q", tc.expectedURL, url)
				}
			}
		})
	}
}

func TestExtractSupermicroGraphicalConsoleWebSocketURL(t *testing.T) {
	testCases := []struct {
		name          string
		oem           map[string]interface{}
		bmcAddress    string
		managerID     string
		expectedURL   string
		expectError   bool
	}{
		{
			name: "Direct iKVM WebSocket endpoint",
			oem: map[string]interface{}{
				"Supermicro": map[string]interface{}{
					"iKVMWebSocketEndpoint": "/redfish/v1/Oem/Supermicro/iKVM/WebSocket",
				},
			},
			bmcAddress:  "https://10.0.0.2",
			managerID:   "1",
			expectedURL: "wss://10.0.0.2/redfish/v1/Oem/Supermicro/iKVM/WebSocket",
			expectError: false,
		},
		{
			name: "iKVM endpoint without WebSocket",
			oem: map[string]interface{}{
				"Supermicro": map[string]interface{}{
					"iKVMEndpoint": "/redfish/v1/Oem/Supermicro/iKVM",
				},
			},
			bmcAddress:  "https://10.0.0.2",
			managerID:   "1",
			expectedURL: "wss://10.0.0.2/redfish/v1/Oem/Supermicro/iKVM",
			expectError: false,
		},
		{
			name: "Fallback to default path",
			oem: map[string]interface{}{
				"Supermicro": map[string]interface{}{},
			},
			bmcAddress:  "https://10.0.0.2",
			managerID:   "1",
			expectedURL: "wss://10.0.0.2/redfish/v1/Oem/Supermicro/iKVM",
			expectError: false,
		},
		{
			name:        "No Supermicro OEM data",
			oem:         map[string]interface{}{},
			bmcAddress:  "https://10.0.0.2",
			managerID:   "1",
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := extractSupermicroGraphicalConsoleWebSocketURL(tc.oem, tc.bmcAddress, tc.managerID)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if url != tc.expectedURL {
					t.Errorf("Expected URL %q, got %q", tc.expectedURL, url)
				}
			}
		})
	}
}

func TestExtractHPEGraphicalConsoleWebSocketURL(t *testing.T) {
	testCases := []struct {
		name          string
		oem           map[string]interface{}
		bmcAddress    string
		managerID     string
		expectedURL   string
		expectError   bool
	}{
		{
			name: "Direct IRC WebSocket endpoint (Hpe)",
			oem: map[string]interface{}{
				"Hpe": map[string]interface{}{
					"IRCWebSocketEndpoint": "/redfish/v1/Managers/1/RemoteConsole/WebSocket",
				},
			},
			bmcAddress:  "https://10.0.0.3",
			managerID:   "1",
			expectedURL: "wss://10.0.0.3/redfish/v1/Managers/1/RemoteConsole/WebSocket",
			expectError: false,
		},
		{
			name: "IRC endpoint without WebSocket (Hp)",
			oem: map[string]interface{}{
				"Hp": map[string]interface{}{
					"IRCEndpoint": "/redfish/v1/Managers/1/RemoteConsole",
				},
			},
			bmcAddress:  "https://10.0.0.3",
			managerID:   "1",
			expectedURL: "wss://10.0.0.3/redfish/v1/Managers/1/RemoteConsole",
			expectError: false,
		},
		{
			name: "Fallback to default path",
			oem: map[string]interface{}{
				"Hpe": map[string]interface{}{},
			},
			bmcAddress:  "https://10.0.0.3",
			managerID:   "1",
			expectedURL: "wss://10.0.0.3/redfish/v1/Managers/1/RemoteConsole",
			expectError: false,
		},
		{
			name:        "No HPE OEM data",
			oem:         map[string]interface{}{},
			bmcAddress:  "https://10.0.0.3",
			managerID:   "1",
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := extractHPEGraphicalConsoleWebSocketURL(tc.oem, tc.bmcAddress, tc.managerID)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if url != tc.expectedURL {
					t.Errorf("Expected URL %q, got %q", tc.expectedURL, url)
				}
			}
		})
	}
}

func TestExtractGraphicalConsoleWebSocketURL(t *testing.T) {
	testCases := []struct {
		name        string
		vendor      VendorType
		managerData map[string]interface{}
		bmcAddress  string
		managerID   string
		expectedURL string
		expectError bool
	}{
		{
			name:   "Dell vendor with vKVM endpoint",
			vendor: VendorDell,
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Dell": map[string]interface{}{
						"vKVMEndpoint": "/redfish/v1/Dell/Managers/iDRAC.Embedded.1/DellvKVM",
					},
				},
			},
			bmcAddress:  "https://10.0.0.1",
			managerID:   "iDRAC.Embedded.1",
			expectedURL: "wss://10.0.0.1/redfish/v1/Dell/Managers/iDRAC.Embedded.1/DellvKVM",
			expectError: false,
		},
		{
			name:   "Supermicro vendor with iKVM endpoint",
			vendor: VendorSupermicro,
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Supermicro": map[string]interface{}{
						"iKVMEndpoint": "/redfish/v1/Oem/Supermicro/iKVM",
					},
				},
			},
			bmcAddress:  "https://10.0.0.2",
			managerID:   "1",
			expectedURL: "wss://10.0.0.2/redfish/v1/Oem/Supermicro/iKVM",
			expectError: false,
		},
		{
			name:   "HPE vendor with IRC endpoint",
			vendor: VendorHPE,
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{
					"Hpe": map[string]interface{}{
						"IRCEndpoint": "/redfish/v1/Managers/1/RemoteConsole",
					},
				},
			},
			bmcAddress:  "https://10.0.0.3",
			managerID:   "1",
			expectedURL: "wss://10.0.0.3/redfish/v1/Managers/1/RemoteConsole",
			expectError: false,
		},
		{
			name:   "Unknown vendor",
			vendor: VendorUnknown,
			managerData: map[string]interface{}{
				"Oem": map[string]interface{}{},
			},
			bmcAddress:  "https://10.0.0.4",
			managerID:   "1",
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := extractGraphicalConsoleWebSocketURL(tc.vendor, tc.managerData, tc.bmcAddress, tc.managerID)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if url != tc.expectedURL {
					t.Errorf("Expected URL %q, got %q", tc.expectedURL, url)
				}
			}
		})
	}
}
