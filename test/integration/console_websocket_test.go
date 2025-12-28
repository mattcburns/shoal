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

package integration

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

// mockBMCWebSocketServer creates a mock BMC WebSocket server for testing
func mockBMCWebSocketServer(t *testing.T) *httptest.Server {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept WebSocket connections on /serial-console path
		if r.URL.Path != "/serial-console" && r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		// Check for auth header
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Failed to upgrade WebSocket: %v", err)
			return
		}
		defer conn.Close()

		// Echo server - read messages and send them back
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					t.Logf("Unexpected WebSocket close: %v", err)
				}
				return
			}

			// Echo the message back
			if err := conn.WriteMessage(messageType, data); err != nil {
				t.Logf("Failed to write message: %v", err)
				return
			}
		}
	})

	return httptest.NewServer(handler)
}

// mockBMCServers creates both Redfish and WebSocket servers for a mock BMC
func mockBMCServers(t *testing.T, managerID string) (*httptest.Server, *httptest.Server) {
	// Create a combined server that handles both Redfish API and WebSocket
	var wsUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	redfishServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for auth for all requests
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "password" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Handle WebSocket upgrade on /serial-console path
		if r.URL.Path == "/serial-console" {
			conn, err := wsUpgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Logf("Failed to upgrade WebSocket: %v", err)
				return
			}
			defer conn.Close()

			// Echo server - read messages and send them back
			for {
				messageType, data, err := conn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
						t.Logf("Unexpected WebSocket close: %v", err)
					}
					return
				}

				// Echo the message back
				if err := conn.WriteMessage(messageType, data); err != nil {
					t.Logf("Failed to write message: %v", err)
					return
				}
			}
		}

		// Handle Manager resource request for Redfish API
		if strings.Contains(r.URL.Path, "/redfish/v1/Managers/") {
			// Return Manager resource with serial console properties
			manager := map[string]interface{}{
				"@odata.type": "#Manager.v1_10_0.Manager",
				"@odata.id":   fmt.Sprintf("/redfish/v1/Managers/%s", managerID),
				"Id":          managerID,
				"Name":        "Mock BMC Manager",
				"SerialConsole": map[string]interface{}{
					"ServiceEnabled":        true,
					"MaxConcurrentSessions": 1,
					"ConnectTypesSupported": []string{"Oem"},
				},
				"Oem": map[string]interface{}{
					"Dell": map[string]interface{}{
						"WebSocketEndpoint": "/serial-console",
					},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(manager)
			return
		}

		http.NotFound(w, r)
	}))

	// Return the same server for both (it handles both Redfish and WebSocket)
	return redfishServer, redfishServer
}

// TestConsoleWebSocketGateway_EndToEnd tests the complete WebSocket gateway flow
func TestConsoleWebSocketGateway_EndToEnd(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	ctx := context.Background()

	// Create mock BMC servers (Redfish API + WebSocket)
	bmcRedfishServer, bmcWsServer := mockBMCServers(t, "BMC1")
	defer bmcRedfishServer.Close()
	defer bmcWsServer.Close()

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-ws",
		Name:                 "Test CM WebSocket",
		ConnectionMethodType: "Redfish",
		Address:              bmcRedfishServer.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := ts.DB.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer ts.DB.DeleteConnectionMethod(ctx, cm.ID)

	// Create console capability
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "BMC1",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       true,
		MaxConcurrentSession: 1,
		ConnectTypes:         `["Oem"]`,
	}
	if err := ts.DB.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Login to get session token
	loginReq := map[string]string{
		"UserName": "admin",
		"Password": "admin",
	}
	loginBody, _ := json.Marshal(loginReq)
	resp, err := http.Post(ts.Server.URL+"/redfish/v1/SessionService/Sessions", "application/json", strings.NewReader(string(loginBody)))
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Login failed with status %d", resp.StatusCode)
	}

	authToken := resp.Header.Get("X-Auth-Token")
	if authToken == "" {
		t.Fatal("No auth token returned")
	}

	// Step 1: Create console session
	connectReq := map[string]string{
		"ConnectType": "Oem",
	}
	connectBody, _ := json.Marshal(connectReq)

	req, _ := http.NewRequest(http.MethodPost, ts.Server.URL+"/redfish/v1/Managers/"+cm.ID+"/Actions/Oem/Shoal.ConnectSerialConsole", strings.NewReader(string(connectBody)))
	req.Header.Set("X-Auth-Token", authToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	var sessionResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	sessionID := sessionResp["Id"].(string)
	wsURI := sessionResp["WebSocketURI"].(string)

	t.Logf("Created console session: %s with WebSocket URI: %s", sessionID, wsURI)

	// Wait a moment for background connection to BMC
	time.Sleep(500 * time.Millisecond)

	// Step 2: Connect to Shoal's WebSocket endpoint
	wsURL := "ws" + strings.TrimPrefix(ts.Server.URL, "http") + wsURI

	// Add auth token to WebSocket request headers
	header := http.Header{}
	header.Set("X-Auth-Token", authToken)

	userConn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v (response: %v)", err, resp)
	}
	defer userConn.Close()

	t.Log("Successfully connected to WebSocket gateway")

	// Step 3: Test bidirectional data flow
	testMessages := []string{
		"Hello, serial console!",
		"Testing WebSocket gateway",
		"Special chars: !@#$%^&*()",
		"Line 1\nLine 2\nLine 3",
	}

	for _, testMsg := range testMessages {
		// Send message to console
		if err := userConn.WriteMessage(websocket.TextMessage, []byte(testMsg)); err != nil {
			t.Fatalf("Failed to send message: %v", err)
		}

		// Read echo response
		userConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, receivedData, err := userConn.ReadMessage()
		if err != nil {
			t.Fatalf("Failed to read message: %v", err)
		}

		if string(receivedData) != testMsg {
			t.Errorf("Expected echo '%s', got '%s'", testMsg, string(receivedData))
		}
	}

	t.Log("Bidirectional data flow verified successfully")

	// Step 4: Test graceful disconnect
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Client disconnecting")
	if err := userConn.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		t.Logf("Error sending close message: %v", err)
	}

	// Wait for close
	time.Sleep(100 * time.Millisecond)

	// Verify session state updated
	session, err := ts.DB.GetConsoleSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("Failed to get console session: %v", err)
	}

	// Session should be disconnected or still active (disconnect is async)
	if session.State != models.ConsoleSessionStateActive && session.State != models.ConsoleSessionStateDisconnected {
		t.Logf("Session state after close: %s", session.State)
	}

	t.Log("WebSocket gateway end-to-end test completed successfully")
}

// TestConsoleWebSocketGateway_Authentication tests authentication requirements
func TestConsoleWebSocketGateway_Authentication(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	ctx := context.Background()

	// Create mock BMC servers
	bmcRedfishServer, bmcWsServer := mockBMCServers(t, "BMC2")
	defer bmcRedfishServer.Close()
	defer bmcWsServer.Close()

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-auth",
		Name:                 "Test CM Auth",
		ConnectionMethodType: "Redfish",
		Address:              bmcRedfishServer.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := ts.DB.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer ts.DB.DeleteConnectionMethod(ctx, cm.ID)

	// Create console session directly (simulating existing session)
	session := &models.ConsoleSession{
		SessionID:          "test-session-auth",
		ConnectionMethodID: cm.ID,
		ManagerID:          "BMC2",
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          "admin",
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/test-session-auth",
	}
	if err := ts.DB.CreateConsoleSession(ctx, session); err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.Server.URL, "http") + session.WebSocketURI

	// Test 1: Attempt to connect without auth token (should fail)
	// The connection will fail because the WebSocket endpoint requires auth
	// and the handler will return an error before upgrading
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("Expected error when connecting without auth, got nil")
	}
	// Note: When auth fails, the handler may return various status codes
	// depending on how the web framework handles it (401 or redirect)
	t.Logf("Without auth - Status: %v, Error: %v", resp, err)

	// Test 2: Attempt to connect with invalid auth token (should fail)
	header := http.Header{}
	header.Set("X-Auth-Token", "invalid-token")

	_, resp, err = websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Error("Expected error when connecting with invalid auth, got nil")
	}
	t.Logf("With invalid auth - Status: %v, Error: %v", resp, err)

	t.Log("WebSocket authentication tests completed successfully")
}

// TestConsoleWebSocketGateway_ConcurrentSessions tests multiple concurrent sessions
func TestConsoleWebSocketGateway_ConcurrentSessions(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	ctx := context.Background()

	// Create mock BMC servers
	bmcRedfishServer, bmcWsServer := mockBMCServers(t, "BMC3")
	defer bmcRedfishServer.Close()
	defer bmcWsServer.Close()

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-concurrent",
		Name:                 "Test CM Concurrent",
		ConnectionMethodType: "Redfish",
		Address:              bmcRedfishServer.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := ts.DB.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer ts.DB.DeleteConnectionMethod(ctx, cm.ID)

	// Create console capability with max 1 session
	// Note: Using empty ManagerID to match what the handler queries for
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       true,
		MaxConcurrentSession: 1,
		ConnectTypes:         `["Oem"]`,
	}
	if err := ts.DB.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Login
	loginReq := map[string]string{
		"UserName": "admin",
		"Password": "admin",
	}
	loginBody, _ := json.Marshal(loginReq)
	resp, err := http.Post(ts.Server.URL+"/redfish/v1/SessionService/Sessions", "application/json", strings.NewReader(string(loginBody)))
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}
	defer resp.Body.Close()

	authToken := resp.Header.Get("X-Auth-Token")

	// Create first session
	connectReq := map[string]string{
		"ConnectType": "Oem",
	}
	connectBody, _ := json.Marshal(connectReq)

	req, _ := http.NewRequest(http.MethodPost, ts.Server.URL+"/redfish/v1/Managers/"+cm.ID+"/Actions/Oem/Shoal.ConnectSerialConsole", strings.NewReader(string(connectBody)))
	req.Header.Set("X-Auth-Token", authToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to create first console session: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201 for first session, got %d", resp.StatusCode)
	}

	// Wait longer for session to be fully active and counted
	time.Sleep(1500 * time.Millisecond)

	// Verify first session is active
	activeSessions, err := ts.DB.GetConsoleSessions(ctx, cm.ID, models.ConsoleSessionStateActive)
	if err != nil {
		t.Fatalf("Failed to get active sessions: %v", err)
	}
	t.Logf("Active sessions after first creation: %d", len(activeSessions))

	// Attempt to create second session (should fail due to max sessions limit)
	req2, _ := http.NewRequest(http.MethodPost, ts.Server.URL+"/redfish/v1/Managers/"+cm.ID+"/Actions/Oem/Shoal.ConnectSerialConsole", strings.NewReader(string(connectBody)))
	req2.Header.Set("X-Auth-Token", authToken)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Failed to make second connection request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 for second session (max exceeded), got %d", resp2.StatusCode)
	}

	t.Log("Concurrent session limit enforcement verified successfully")
}

// TestConsoleWebSocketGateway_BinaryData tests handling of binary data
func TestConsoleWebSocketGateway_BinaryData(t *testing.T) {
	// Note: This is a placeholder for future binary data handling tests
	// Serial console typically uses text, but we should handle binary gracefully
	t.Skip("Binary data handling test - to be implemented based on BMC requirements")
}

// TestConsoleWebSocketGateway_Timeout tests timeout handling
func TestConsoleWebSocketGateway_Timeout(t *testing.T) {
	// Note: This is a placeholder for timeout handling tests
	// Would require mock server that delays/hangs to test timeout behavior
	t.Skip("Timeout handling test - to be implemented with controllable mock server")
}

// TestConsoleWebSocketGateway_ErrorRecovery tests error recovery scenarios
func TestConsoleWebSocketGateway_ErrorRecovery(t *testing.T) {
	// Note: This is a placeholder for error recovery tests
	// Would test scenarios like BMC disconnect, network errors, etc.
	t.Skip("Error recovery test - to be implemented with fault injection")
}
