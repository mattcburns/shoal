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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shoal/pkg/models"
)

func TestConnectGraphicalConsole_Success(t *testing.T) {
	// Create test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create test user
	ctx := context.Background()
	_, session := createTestUserAndSession(t, db, "admin")

	// Create test connection method
	cm := &models.ConnectionMethod{
		ID:       "test-cm",
		Name:     "test-bmc",
		Address:  "https://10.0.0.1",
		Username: "root",
		Password: "calvin",
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create graphical console capability
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "test-cm",
		ConsoleType:          models.ConsoleTypeGraphical,
		ServiceEnabled:       true,
		MaxConcurrentSession: 4,
		ConnectTypes:         `["KVMIP", "Oem"]`,
		VendorData:           `{"vendor":"Dell","graphical_console_oem":{"html5_endpoint":"/redfish/v1/Dell/Managers/iDRAC.Embedded.1/DellvKVM"}}`,
		LastUpdated:          time.Now(),
	}
	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Create test handler
	handler := NewRouterWithImageProxy(db, nil)
	mux := newMux(handler)

	// Create request
	reqBody := map[string]string{
		"ConnectType": "Oem",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectGraphicalConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Perform request
	mux.ServeHTTP(rec, req)

	// Check response
	if rec.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Parse response
	var response map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify response
	if consoleType, ok := response["ConsoleType"].(string); !ok || consoleType != "GraphicalConsole" {
		t.Errorf("Expected ConsoleType 'GraphicalConsole', got %v", response["ConsoleType"])
	}

	if state, ok := response["State"].(string); !ok || state != "connecting" {
		t.Errorf("Expected State 'connecting', got %v", response["State"])
	}

	if wsURI, ok := response["WebSocketURI"].(string); !ok || wsURI == "" {
		t.Errorf("Expected non-empty WebSocketURI, got %v", response["WebSocketURI"])
	}
}

func TestConnectGraphicalConsole_InvalidConnectType(t *testing.T) {
	// Create test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create test user
	_, session := createTestUserAndSession(t, db, "admin")

	// Create test handler
	handler := NewRouterWithImageProxy(db, nil)
	mux := newMux(handler)

	// Create request with invalid ConnectType
	reqBody := map[string]string{
		"ConnectType": "SSH",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectGraphicalConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Perform request
	mux.ServeHTTP(rec, req)

	// Check response
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConnectGraphicalConsole_UnauthorizedUser(t *testing.T) {
	// Create test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create test user with ReadOnly role
	_, session := createTestUserAndSession(t, db, "viewer")

	// Create test handler
	handler := NewRouterWithImageProxy(db, nil)
	mux := newMux(handler)

	// Create request
	reqBody := map[string]string{
		"ConnectType": "Oem",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectGraphicalConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Perform request
	mux.ServeHTTP(rec, req)

	// Check response - should be forbidden
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConnectGraphicalConsole_MaxSessionsExceeded(t *testing.T) {
	// Create test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create test user
	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "admin")

	// Create test connection method
	cm := &models.ConnectionMethod{
		ID:       "test-cm",
		Name:     "test-bmc",
		Address:  "https://10.0.0.1",
		Username: "root",
		Password: "calvin",
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create graphical console capability with max 1 session
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "test-cm",
		ConsoleType:          models.ConsoleTypeGraphical,
		ServiceEnabled:       true,
		MaxConcurrentSession: 1,
		ConnectTypes:         `["KVMIP", "Oem"]`,
		VendorData:           `{"vendor":"Dell"}`,
		LastUpdated:          time.Now(),
	}
	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Create an active session
	consoleSession := &models.ConsoleSession{
		SessionID:          "existing-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          "test-cm",
		ConsoleType:        models.ConsoleTypeGraphical,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          user.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/existing-session",
	}
	if err := db.CreateConsoleSession(ctx, consoleSession); err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}

	// Create test handler
	handler := NewRouterWithImageProxy(db, nil)
	mux := newMux(handler)

	// Create request for second session
	reqBody := map[string]string{
		"ConnectType": "Oem",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectGraphicalConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Perform request
	mux.ServeHTTP(rec, req)

	// Check response - should be service unavailable
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
