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
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"shoal/pkg/models"
)

// Mock Redfish Manager endpoint with serial console OEM data
func mockRedfishManagerHandler(vendor VendorType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract manager ID from path
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}
		managerID := parts[4]

		// Build mock Manager response with console capabilities
		manager := map[string]interface{}{
			"@odata.type": "#Manager.v1_10_0.Manager",
			"@odata.id":   fmt.Sprintf("/redfish/v1/Managers/%s", managerID),
			"Id":          managerID,
			"Name":        "Manager for Test BMC",
			"SerialConsole": map[string]interface{}{
				"ServiceEnabled":        true,
				"MaxConcurrentSessions": 1,
				"ConnectTypesSupported": []string{"Oem"},
			},
		}

		// Add vendor-specific OEM data
		switch vendor {
		case VendorDell:
			manager["Manufacturer"] = "Dell Inc."
			manager["Model"] = "iDRAC9"
			manager["FirmwareVersion"] = "5.00.00.00"
			manager["Oem"] = map[string]interface{}{
				"Dell": map[string]interface{}{
					"WebSocketEndpoint": fmt.Sprintf("/redfish/v1/Dell/Managers/%s/SerialConsole", managerID),
				},
			}
		case VendorSupermicro:
			manager["Manufacturer"] = "Supermicro"
			manager["Model"] = "X11DPH-T"
			manager["FirmwareVersion"] = "3.5"
			manager["Oem"] = map[string]interface{}{
				"Supermicro": map[string]interface{}{
					"SerialConsoleWebSocket": fmt.Sprintf("/redfish/v1/Managers/%s/SerialConsole", managerID),
				},
			}
		case VendorHPE:
			manager["Manufacturer"] = "HPE"
			manager["Model"] = "iLO 5"
			manager["FirmwareVersion"] = "2.40"
			manager["Oem"] = map[string]interface{}{
				"Hpe": map[string]interface{}{
					"SerialConsoleWebSocket": fmt.Sprintf("/redfish/v1/Managers/%s/SerialConsole", managerID),
				},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manager)
	}
}

// Mock WebSocket server that echoes messages back
func mockWebSocketEchoServer(t *testing.T) *httptest.Server {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for testing
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		// Echo messages back
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					t.Logf("WebSocket closed unexpectedly: %v", err)
				}
				break
			}

			if err := conn.WriteMessage(messageType, data); err != nil {
				t.Logf("Failed to echo message: %v", err)
				break
			}
		}
	})

	return httptest.NewServer(handler)
}

func TestSerialConsoleSession_Connect(t *testing.T) {
	tests := []struct {
		name    string
		vendor  VendorType
		wantErr bool
	}{
		{
			name:    "Dell iDRAC connection",
			vendor:  VendorDell,
			wantErr: false,
		},
		{
			name:    "Supermicro connection",
			vendor:  VendorSupermicro,
			wantErr: false,
		},
		{
			name:    "HPE iLO connection",
			vendor:  VendorHPE,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Redfish server
			redfishServer := httptest.NewServer(mockRedfishManagerHandler(tt.vendor))
			defer redfishServer.Close()

			// Create mock WebSocket server
			wsServer := mockWebSocketEchoServer(t)
			defer wsServer.Close()

			// Convert HTTP URL to WebSocket URL
			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

			// Create session
			session := &SerialConsoleSession{
				SessionID:          "test-session-123",
				ConnectionMethodID: "test-conn-1",
				ManagerID:          "BMC",
				BMCAddress:         redfishServer.URL,
				BMCUsername:        "admin",
				BMCPassword:        "password",
				BMCWebSocketURL:    wsURL,
				State:              models.ConsoleSessionStateConnecting,
				CreatedBy:          "testuser",
			}

			// Connect to WebSocket
			ctx := context.Background()
			err := session.Connect(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("Connect() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err == nil {
				// Verify state transition
				if session.GetState() != models.ConsoleSessionStateActive {
					t.Errorf("Expected state Active, got %s", session.GetState())
				}

				// Cleanup
				_ = session.Disconnect()
			}
		})
	}
}

func TestSerialConsoleSession_Connect_QueryWebSocketURL(t *testing.T) {
	tests := []struct {
		name    string
		vendor  VendorType
		wantErr bool
	}{
		{
			name:    "Dell iDRAC query WebSocket URL",
			vendor:  VendorDell,
			wantErr: false,
		},
		{
			name:    "Supermicro query WebSocket URL",
			vendor:  VendorSupermicro,
			wantErr: false,
		},
		{
			name:    "HPE iLO query WebSocket URL",
			vendor:  VendorHPE,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Redfish server
			redfishServer := httptest.NewServer(mockRedfishManagerHandler(tt.vendor))
			defer redfishServer.Close()

			// Create mock WebSocket server
			wsServer := mockWebSocketEchoServer(t)
			defer wsServer.Close()

			// Setup a custom handler that redirects console WebSocket requests to our mock WS server
			// This simulates the BMC's WebSocket endpoint
			mux := http.NewServeMux()
			mux.HandleFunc("/redfish/v1/Managers/", func(w http.ResponseWriter, r *http.Request) {
				// Check if this is a WebSocket upgrade request
				if r.Header.Get("Upgrade") == "websocket" {
					// Handle WebSocket connection
					wsUpgrader := websocket.Upgrader{
						CheckOrigin: func(r *http.Request) bool { return true },
					}
					conn, err := wsUpgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					// Simple echo
					for {
						mt, msg, err := conn.ReadMessage()
						if err != nil {
							break
						}
						_ = conn.WriteMessage(mt, msg)
					}
				} else {
					// Regular HTTP request - return Manager data
					mockRedfishManagerHandler(tt.vendor)(w, r)
				}
			})

			// WebSocket upgrade handler for Dell-specific paths
			wsUpgrader := websocket.Upgrader{
				CheckOrigin: func(r *http.Request) bool { return true },
			}
			mux.HandleFunc("/redfish/v1/Dell/Managers/", func(w http.ResponseWriter, r *http.Request) {
				conn, err := wsUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				// Simple echo
				for {
					mt, msg, err := conn.ReadMessage()
					if err != nil {
						break
					}
					_ = conn.WriteMessage(mt, msg)
				}
			})

			combinedServer := httptest.NewServer(mux)
			defer combinedServer.Close()

			// Create session without WebSocket URL (will query from Redfish)
			session := &SerialConsoleSession{
				SessionID:          "test-session-456",
				ConnectionMethodID: "test-conn-1",
				ManagerID:          "BMC",
				BMCAddress:         combinedServer.URL,
				BMCUsername:        "admin",
				BMCPassword:        "password",
				BMCWebSocketURL:    "", // Empty - will query
				State:              models.ConsoleSessionStateConnecting,
				CreatedBy:          "testuser",
			}

			// Connect (should query WebSocket URL first)
			ctx := context.Background()
			err := session.Connect(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("Connect() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err == nil {
				// Verify WebSocket URL was discovered
				if session.BMCWebSocketURL == "" {
					t.Error("Expected BMCWebSocketURL to be populated")
				}

				// Verify state
				if session.GetState() != models.ConsoleSessionStateActive {
					t.Errorf("Expected state Active, got %s", session.GetState())
				}

				// Cleanup
				_ = session.Disconnect()
			}
		})
	}
}

func TestSerialConsoleSession_Disconnect(t *testing.T) {
	// Create mock WebSocket server
	wsServer := mockWebSocketEchoServer(t)
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	session := &SerialConsoleSession{
		SessionID:          "test-session-789",
		ConnectionMethodID: "test-conn-1",
		ManagerID:          "BMC",
		BMCWebSocketURL:    wsURL,
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          "testuser",
	}

	// Connect first
	ctx := context.Background()
	if err := session.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Verify active state
	if session.GetState() != models.ConsoleSessionStateActive {
		t.Errorf("Expected state Active, got %s", session.GetState())
	}

	// Disconnect
	if err := session.Disconnect(); err != nil {
		t.Errorf("Disconnect() error = %v", err)
	}

	// Verify disconnected state
	if session.GetState() != models.ConsoleSessionStateDisconnected {
		t.Errorf("Expected state Disconnected, got %s", session.GetState())
	}

	// Second disconnect should be idempotent
	if err := session.Disconnect(); err != nil {
		t.Errorf("Second Disconnect() should not error, got %v", err)
	}
}

func TestSerialConsoleSession_AttachUserWebSocket(t *testing.T) {
	// Create mock BMC WebSocket server
	bmcWsServer := mockWebSocketEchoServer(t)
	defer bmcWsServer.Close()

	bmcWsURL := "ws" + strings.TrimPrefix(bmcWsServer.URL, "http")

	// Create session and connect to BMC
	session := &SerialConsoleSession{
		SessionID:          "test-session-attach",
		ConnectionMethodID: "test-conn-1",
		ManagerID:          "BMC",
		BMCWebSocketURL:    bmcWsURL,
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          "testuser",
	}

	ctx := context.Background()
	if err := session.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect to BMC: %v", err)
	}
	defer session.Disconnect()

	// Create mock user WebSocket client
	userConn, _, err := websocket.DefaultDialer.Dial(bmcWsURL, nil)
	if err != nil {
		t.Fatalf("Failed to create user WebSocket connection: %v", err)
	}
	defer userConn.Close()

	// Attach user WebSocket
	if err := session.AttachUserWebSocket(userConn); err != nil {
		t.Errorf("AttachUserWebSocket() error = %v", err)
	}

	// Give goroutines time to start
	time.Sleep(100 * time.Millisecond)

	// Test message proxying: user -> BMC -> user (echo)
	testMessage := "Hello, serial console!"
	if err := userConn.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
		t.Fatalf("Failed to write test message: %v", err)
	}

	// Read echoed message
	userConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, receivedData, err := userConn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read echoed message: %v", err)
	}

	if string(receivedData) != testMessage {
		t.Errorf("Expected echoed message '%s', got '%s'", testMessage, string(receivedData))
	}
}

func TestSerialConsoleSession_StateTransitions(t *testing.T) {
	wsServer := mockWebSocketEchoServer(t)
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	session := &SerialConsoleSession{
		SessionID:          "test-session-states",
		ConnectionMethodID: "test-conn-1",
		ManagerID:          "BMC",
		BMCWebSocketURL:    wsURL,
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          "testuser",
	}

	// Initial state should be Connecting
	if session.GetState() != models.ConsoleSessionStateConnecting {
		t.Errorf("Initial state should be Connecting, got %s", session.GetState())
	}

	// Connect should transition to Active
	ctx := context.Background()
	if err := session.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	if session.GetState() != models.ConsoleSessionStateActive {
		t.Errorf("After Connect, state should be Active, got %s", session.GetState())
	}

	// Disconnect should transition to Disconnected
	if err := session.Disconnect(); err != nil {
		t.Errorf("Failed to disconnect: %v", err)
	}

	if session.GetState() != models.ConsoleSessionStateDisconnected {
		t.Errorf("After Disconnect, state should be Disconnected, got %s", session.GetState())
	}
}

func TestSerialConsoleSession_Connect_InvalidURL(t *testing.T) {
	session := &SerialConsoleSession{
		SessionID:          "test-session-invalid",
		ConnectionMethodID: "test-conn-1",
		ManagerID:          "BMC",
		BMCWebSocketURL:    "ws://invalid-host-that-does-not-exist:9999",
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          "testuser",
	}

	ctx := context.Background()
	err := session.Connect(ctx)

	if err == nil {
		t.Error("Expected error when connecting to invalid URL, got nil")
	}

	if session.GetState() != models.ConsoleSessionStateError {
		t.Errorf("Expected state Error after failed connection, got %s", session.GetState())
	}

	if session.ErrorMessage == "" {
		t.Error("Expected error message to be set")
	}
}

func TestExtractVendorWebSocketURLs(t *testing.T) {
	tests := []struct {
		name      string
		vendor    VendorType
		managerID string
		wantURL   string
	}{
		{
			name:      "Dell iDRAC",
			vendor:    VendorDell,
			managerID: "BMC",
			wantURL:   "/redfish/v1/Dell/Managers/BMC/SerialConsole",
		},
		{
			name:      "Supermicro",
			vendor:    VendorSupermicro,
			managerID: "BMC",
			wantURL:   "/redfish/v1/Managers/BMC/SerialConsole",
		},
		{
			name:      "HPE iLO",
			vendor:    VendorHPE,
			managerID: "BMC",
			wantURL:   "/redfish/v1/Managers/BMC/SerialConsole",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Redfish server
			server := httptest.NewServer(mockRedfishManagerHandler(tt.vendor))
			defer server.Close()

			// Query manager data
			resp, err := http.Get(server.URL + "/redfish/v1/Managers/" + tt.managerID)
			if err != nil {
				t.Fatalf("Failed to query manager: %v", err)
			}
			defer resp.Body.Close()

			var managerData map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&managerData); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			// Extract WebSocket URL
			wsURL, err := extractSerialConsoleWebSocketURL(tt.vendor, managerData, server.URL, tt.managerID)
			if err != nil {
				t.Fatalf("extractSerialConsoleWebSocketURL() error = %v", err)
			}

			// Verify URL contains expected path
			if !strings.Contains(wsURL, tt.wantURL) {
				t.Errorf("Expected WebSocket URL to contain '%s', got '%s'", tt.wantURL, wsURL)
			}

			// Verify WebSocket scheme
			if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
				t.Errorf("Expected WebSocket URL to have ws:// or wss:// scheme, got '%s'", wsURL)
			}
		})
	}
}

func TestBuildWebSocketURL(t *testing.T) {
	tests := []struct {
		name       string
		bmcAddress string
		path       string
		wantScheme string
		wantHost   string
		wantPath   string
	}{
		{
			name:       "HTTPS BMC address",
			bmcAddress: "https://bmc.example.com",
			path:       "/redfish/v1/serial",
			wantScheme: "wss",
			wantHost:   "bmc.example.com",
			wantPath:   "/redfish/v1/serial",
		},
		{
			name:       "HTTP BMC address",
			bmcAddress: "http://192.168.1.100",
			path:       "/console",
			wantScheme: "ws",
			wantHost:   "192.168.1.100",
			wantPath:   "/console",
		},
		{
			name:       "HTTPS with port",
			bmcAddress: "https://bmc.example.com:8443",
			path:       "/serial",
			wantScheme: "wss",
			wantHost:   "bmc.example.com:8443",
			wantPath:   "/serial",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsURL := buildWebSocketURL(tt.bmcAddress, tt.path)

			if !strings.HasPrefix(wsURL, tt.wantScheme+"://") {
				t.Errorf("Expected scheme %s, got %s", tt.wantScheme, wsURL)
			}

			if !strings.Contains(wsURL, tt.wantHost) {
				t.Errorf("Expected host %s in URL %s", tt.wantHost, wsURL)
			}

			if !strings.HasSuffix(wsURL, tt.wantPath) {
				t.Errorf("Expected path %s in URL %s", tt.wantPath, wsURL)
			}
		})
	}
}
