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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"shoal/pkg/models"
)

// GraphicalConsoleSession represents an active graphical console session with WebSocket connection to BMC
type GraphicalConsoleSession struct {
	SessionID          string
	ConnectionMethodID string
	ManagerID          string
	BMCAddress         string
	BMCUsername        string
	BMCPassword        string
	BMCWebSocketURL    string
	State              models.ConsoleSessionState
	CreatedBy          string
	ErrorMessage       string

	bmcConn  *websocket.Conn
	userConn *websocket.Conn
	mutex    sync.RWMutex
	cancel   context.CancelFunc
	done     chan struct{}
}

// Connect establishes a WebSocket connection to the BMC's graphical console endpoint
func (s *GraphicalConsoleSession) Connect(ctx context.Context) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.State != models.ConsoleSessionStateConnecting {
		return fmt.Errorf("session is not in connecting state")
	}

	// Query BMC for graphical console WebSocket URL if not provided
	if s.BMCWebSocketURL == "" {
		wsURL, err := s.queryGraphicalConsoleWebSocketURL(ctx)
		if err != nil {
			s.State = models.ConsoleSessionStateError
			s.ErrorMessage = fmt.Sprintf("failed to query WebSocket URL: %v", err)
			return err
		}
		s.BMCWebSocketURL = wsURL
	}

	// Establish WebSocket connection to BMC
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // BMCs often use self-signed certificates
		},
		HandshakeTimeout: 30 * time.Second,
	}

	// Add basic auth to WebSocket request headers
	header := http.Header{}
	header.Set("Authorization", basicAuth(s.BMCUsername, s.BMCPassword))

	conn, resp, err := dialer.DialContext(ctx, s.BMCWebSocketURL, header)
	if err != nil {
		s.State = models.ConsoleSessionStateError
		s.ErrorMessage = fmt.Sprintf("failed to connect to BMC WebSocket: %v", err)
		if resp != nil {
			_ = resp.Body.Close()
		}
		return err
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	s.bmcConn = conn
	s.State = models.ConsoleSessionStateActive

	slog.Debug("Established WebSocket connection to BMC graphical console",
		"session_id", s.SessionID,
		"bmc_url", s.BMCWebSocketURL)

	return nil
}

// AttachUserWebSocket attaches the user's WebSocket connection and starts proxying
func (s *GraphicalConsoleSession) AttachUserWebSocket(userConn *websocket.Conn) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.State != models.ConsoleSessionStateActive {
		return fmt.Errorf("session is not in active state")
	}

	if s.bmcConn == nil {
		return fmt.Errorf("BMC connection not established")
	}

	s.userConn = userConn

	// Create cancellable context for proxying
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	// Start bidirectional data proxying
	go s.proxyUserToBMC(ctx)
	go s.proxyBMCToUser(ctx)

	slog.Debug("Attached user WebSocket to graphical console session",
		"session_id", s.SessionID)

	return nil
}

// proxyUserToBMC proxies data from user WebSocket to BMC WebSocket
func (s *GraphicalConsoleSession) proxyUserToBMC(ctx context.Context) {
	defer func() {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.mutex.RLock()
		userConn := s.userConn
		bmcConn := s.bmcConn
		s.mutex.RUnlock()

		if userConn == nil || bmcConn == nil {
			return
		}

		// Read from user WebSocket (can be text or binary for graphical console)
		messageType, data, err := userConn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("User WebSocket closed unexpectedly",
					"session_id", s.SessionID,
					"error", err)
			}
			s.Disconnect()
			return
		}

		// Forward to BMC WebSocket
		if err := bmcConn.WriteMessage(messageType, data); err != nil {
			slog.Error("Failed to write to BMC WebSocket",
				"session_id", s.SessionID,
				"error", err)
			s.Disconnect()
			return
		}
	}
}

// proxyBMCToUser proxies data from BMC WebSocket to user WebSocket
func (s *GraphicalConsoleSession) proxyBMCToUser(ctx context.Context) {
	defer func() {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.mutex.RLock()
		userConn := s.userConn
		bmcConn := s.bmcConn
		s.mutex.RUnlock()

		if userConn == nil || bmcConn == nil {
			return
		}

		// Read from BMC WebSocket (can be text or binary for graphical console)
		messageType, data, err := bmcConn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("BMC WebSocket closed unexpectedly",
					"session_id", s.SessionID,
					"error", err)
			}
			s.Disconnect()
			return
		}

		// Forward to user WebSocket
		if err := userConn.WriteMessage(messageType, data); err != nil {
			slog.Error("Failed to write to user WebSocket",
				"session_id", s.SessionID,
				"error", err)
			s.Disconnect()
			return
		}
	}
}

// Disconnect terminates the console session and closes all connections
func (s *GraphicalConsoleSession) Disconnect() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.State == models.ConsoleSessionStateDisconnected {
		return nil // Already disconnected
	}

	// Cancel context to stop proxying goroutines
	if s.cancel != nil {
		s.cancel()
	}

	// Wait for proxying goroutines to finish (with timeout)
	if s.done != nil {
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			slog.Warn("Timeout waiting for proxying goroutines to finish",
				"session_id", s.SessionID)
		}
	}

	// Close WebSocket connections
	if s.bmcConn != nil {
		_ = s.bmcConn.Close()
		s.bmcConn = nil
	}
	if s.userConn != nil {
		_ = s.userConn.Close()
		s.userConn = nil
	}

	s.State = models.ConsoleSessionStateDisconnected

	slog.Debug("Disconnected graphical console session",
		"session_id", s.SessionID)

	return nil
}

// GetState returns the current state of the session (thread-safe)
func (s *GraphicalConsoleSession) GetState() models.ConsoleSessionState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.State
}

// queryGraphicalConsoleWebSocketURL queries the BMC's Redfish OEM endpoint for graphical console WebSocket URL
func (s *GraphicalConsoleSession) queryGraphicalConsoleWebSocketURL(ctx context.Context) (string, error) {
	// Build URL for Manager resource
	managerURL := fmt.Sprintf("%s/redfish/v1/Managers/%s", s.BMCAddress, s.ManagerID)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", managerURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add basic auth
	req.SetBasicAuth(s.BMCUsername, s.BMCPassword)

	// Make request
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // BMCs often use self-signed certificates
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query manager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Parse Manager response
	var managerData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&managerData); err != nil {
		return "", fmt.Errorf("failed to decode manager response: %w", err)
	}

	// Detect vendor and extract WebSocket URL
	vendor := DetectVendor(managerData)
	wsURL, err := extractGraphicalConsoleWebSocketURL(vendor, managerData, s.BMCAddress, s.ManagerID)
	if err != nil {
		return "", fmt.Errorf("failed to extract WebSocket URL: %w", err)
	}

	return wsURL, nil
}

// extractGraphicalConsoleWebSocketURL extracts the graphical console WebSocket URL from vendor-specific OEM data
func extractGraphicalConsoleWebSocketURL(vendor VendorType, managerData map[string]interface{}, bmcAddress, managerID string) (string, error) {
	// Try to get from OEM data first
	if oem, ok := managerData["Oem"].(map[string]interface{}); ok {
		switch vendor {
		case VendorDell:
			return extractDellGraphicalConsoleWebSocketURL(oem, bmcAddress, managerID)
		case VendorSupermicro:
			return extractSupermicroGraphicalConsoleWebSocketURL(oem, bmcAddress, managerID)
		case VendorHPE:
			return extractHPEGraphicalConsoleWebSocketURL(oem, bmcAddress, managerID)
		}
	}

	// Fallback: Try standard GraphicalConsole property with OEM endpoints
	if graphicalConsole, ok := managerData["GraphicalConsole"].(map[string]interface{}); ok {
		if wsURL := extractWebSocketURLFromGraphicalConsole(graphicalConsole, bmcAddress); wsURL != "" {
			return wsURL, nil
		}
	}

	return "", fmt.Errorf("no graphical console WebSocket URL found for vendor %s", vendor)
}

// extractDellGraphicalConsoleWebSocketURL extracts Dell iDRAC graphical console (vKVM) WebSocket URL
func extractDellGraphicalConsoleWebSocketURL(oem map[string]interface{}, bmcAddress, managerID string) (string, error) {
	dellOEM, ok := oem["Dell"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Dell OEM data not found")
	}

	// Check for direct vKVM WebSocket endpoint
	if wsEndpoint, ok := dellOEM["vKVMWebSocketEndpoint"].(string); ok {
		return buildWebSocketURL(bmcAddress, wsEndpoint), nil
	}

	// Check for vKVM endpoint that might need further querying
	if vkvmEndpoint, ok := dellOEM["vKVMEndpoint"].(string); ok {
		// The vKVMEndpoint may be a Redfish resource that contains the WebSocket URL
		// For now, try to construct a WebSocket URL from it
		// In a real implementation, we might need to query this endpoint
		return buildWebSocketURL(bmcAddress, vkvmEndpoint), nil
	}

	// Fallback: Construct standard Dell vKVM WebSocket path
	// Dell iDRAC typically uses: wss://<bmc>/redfish/v1/Dell/Managers/<id>/DellvKVM
	wsPath := fmt.Sprintf("/redfish/v1/Dell/Managers/%s/DellvKVM", managerID)
	return buildWebSocketURL(bmcAddress, wsPath), nil
}

// extractSupermicroGraphicalConsoleWebSocketURL extracts Supermicro graphical console (iKVM) WebSocket URL
func extractSupermicroGraphicalConsoleWebSocketURL(oem map[string]interface{}, bmcAddress, managerID string) (string, error) {
	smcOEM, ok := oem["Supermicro"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Supermicro OEM data not found")
	}

	// Check for iKVM WebSocket endpoint
	if wsEndpoint, ok := smcOEM["iKVMWebSocketEndpoint"].(string); ok {
		return buildWebSocketURL(bmcAddress, wsEndpoint), nil
	}

	// Check for iKVM endpoint that might need further querying
	if ikvmEndpoint, ok := smcOEM["iKVMEndpoint"].(string); ok {
		return buildWebSocketURL(bmcAddress, ikvmEndpoint), nil
	}

	// Fallback: Construct standard Supermicro iKVM WebSocket path
	// Supermicro typically uses: wss://<bmc>/redfish/v1/Oem/Supermicro/iKVM
	wsPath := "/redfish/v1/Oem/Supermicro/iKVM"
	return buildWebSocketURL(bmcAddress, wsPath), nil
}

// extractHPEGraphicalConsoleWebSocketURL extracts HPE iLO graphical console (IRC) WebSocket URL
func extractHPEGraphicalConsoleWebSocketURL(oem map[string]interface{}, bmcAddress, managerID string) (string, error) {
	// HPE can use either "Hpe" or "Hp" namespace
	var hpeOEM map[string]interface{}
	var ok bool

	if hpeOEM, ok = oem["Hpe"].(map[string]interface{}); !ok {
		hpeOEM, ok = oem["Hp"].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("HPE OEM data not found")
		}
	}

	// Check for IRC WebSocket endpoint
	if wsEndpoint, ok := hpeOEM["IRCWebSocketEndpoint"].(string); ok {
		return buildWebSocketURL(bmcAddress, wsEndpoint), nil
	}

	// Check for IRC endpoint
	if ircEndpoint, ok := hpeOEM["IRCEndpoint"].(string); ok {
		return buildWebSocketURL(bmcAddress, ircEndpoint), nil
	}

	// Fallback: Construct standard HPE IRC WebSocket path
	// HPE iLO typically uses: wss://<bmc>/redfish/v1/Managers/<id>/RemoteConsole
	wsPath := fmt.Sprintf("/redfish/v1/Managers/%s/RemoteConsole", managerID)
	return buildWebSocketURL(bmcAddress, wsPath), nil
}

// extractWebSocketURLFromGraphicalConsole extracts WebSocket URL from standard GraphicalConsole property
func extractWebSocketURLFromGraphicalConsole(graphicalConsole map[string]interface{}, bmcAddress string) string {
	// Check for OEM WebSocket endpoint in GraphicalConsole
	if oem, ok := graphicalConsole["Oem"].(map[string]interface{}); ok {
		for _, vendorOEM := range oem {
			if vendorMap, ok := vendorOEM.(map[string]interface{}); ok {
				if wsEndpoint, ok := vendorMap["WebSocketEndpoint"].(string); ok {
					return buildWebSocketURL(bmcAddress, wsEndpoint)
				}
			}
		}
	}

	return ""
}
