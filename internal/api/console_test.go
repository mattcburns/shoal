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
	"strings"
	"testing"
	"time"

	"shoal/internal/database"
	"shoal/pkg/models"
)

func TestConsoleSessionCollection_Unauthenticated(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	// Create request without authentication
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/test-mgr/Oem/Shoal/ConsoleSessions", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}
}

func TestConsoleSessionCollection_ManagerNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	// Create admin user and session
	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "admin")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	// Create request with authentication but non-existent manager
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/nonexistent/Oem/Shoal/ConsoleSessions", nil)
	req.Header.Set("X-Auth-Token", session.Token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", rr.Code)
	}
}

func TestConnectSerialConsole_RequiresOperatorRole(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	// Create viewer user (insufficient privileges)
	ctx := context.Background()
	passwordHash, _ := hashPassword("password")
	user := &models.User{
		ID:           "viewer-user",
		Username:     "viewer",
		PasswordHash: passwordHash,
		Role:         "viewer",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	defer db.DeleteUser(ctx, user.ID)

	// Create session for viewer
	session := &models.Session{
		ID:        "viewer-session",
		UserID:    user.ID,
		Token:     "viewer-token",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateSession(ctx, session); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer db.DeleteSession(ctx, session.ID)

	// Try to connect console with viewer role
	reqBody := map[string]string{"ConnectType": "Oem"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-mgr/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", rr.Code)
	}
}

func TestConnectSerialConsole_InvalidConnectType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	// Create operator user
	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm",
		Name:                 "Test CM",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer db.DeleteConnectionMethod(ctx, cm.ID)

	// Try to connect with invalid ConnectType
	reqBody := map[string]string{"ConnectType": "InvalidType"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&response)
	errorObj := response["error"].(map[string]interface{})
	message := errorObj["message"].(string)
	if message != "only ConnectType 'Oem' is supported" {
		t.Errorf("Unexpected error message: %s", message)
	}
}

func TestConsoleEndpoints_MethodNotAllowed(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "admin")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	testCases := []struct {
		name   string
		method string
		path   string
	}{
		{"Connect with GET", http.MethodGet, "/redfish/v1/Managers/test/Actions/Oem/Shoal.ConnectSerialConsole"},
		{"Collection with POST", http.MethodPost, "/redfish/v1/Managers/test/Oem/Shoal/ConsoleSessions"},
		{"Session with POST", http.MethodPost, "/redfish/v1/Managers/test/Oem/Shoal/ConsoleSessions/123"},
		{"Disconnect with GET", http.MethodGet, "/redfish/v1/Managers/test/Oem/Shoal/ConsoleSessions/123/Actions/Disconnect"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("X-Auth-Token", session.Token)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status 405, got %d", rr.Code)
			}
		})
	}
}

// Helper functions

func setupTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	db, err := database.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	return db, func() { db.Close() }
}

func createTestUserAndSession(t *testing.T, db *database.DB, role string) (*models.User, *models.Session) {
	t.Helper()
	ctx := context.Background()

	passwordHash, _ := hashPassword("password")
	user := &models.User{
		ID:           role + "-user",
		Username:     role,
		PasswordHash: passwordHash,
		Role:         role,
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	session := &models.Session{
		ID:        role + "-session",
		UserID:    user.ID,
		Token:     role + "-token",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateSession(ctx, session); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	return user, session
}

func createTestUserWithRole(t *testing.T, db *database.DB, username, role string) (*models.User, *models.Session) {
	t.Helper()
	ctx := context.Background()

	passwordHash, _ := hashPassword("password")
	user := &models.User{
		ID:           username + "-id",
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	session := &models.Session{
		ID:        username + "-session",
		UserID:    user.ID,
		Token:     username + "-token",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := db.CreateSession(ctx, session); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	return user, session
}

func hashPassword(password string) (string, error) {
	// Use a simple hash for testing
	return "hashed:" + password, nil
}

func TestConnectSerialConsole_MaxSessionsExceeded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm",
		Name:                 "Test CM",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer db.DeleteConnectionMethod(ctx, cm.ID)

	// Create console capability with max 1 session
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       true,
		MaxConcurrentSession: 1,
		ConnectTypes:         `["Oem"]`,
	}
	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Create an active console session to reach the limit
	activeSession := &models.ConsoleSession{
		SessionID:          "existing-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          user.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/existing-session",
	}
	if err := db.CreateConsoleSession(ctx, activeSession); err != nil {
		t.Fatalf("Failed to create active session: %v", err)
	}

	// Try to create another session (should fail)
	reqBody := map[string]string{"ConnectType": "Oem"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&response)
	errorObj := response["error"].(map[string]interface{})
	message := errorObj["message"].(string)
	if !contains(message, "maximum concurrent sessions") {
		t.Errorf("Expected max sessions error, got: %s", message)
	}
}

func TestConnectSerialConsole_ConsoleDisabled(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm",
		Name:                 "Test CM",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer db.DeleteConnectionMethod(ctx, cm.ID)

	// Create console capability with service disabled
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       false,
		MaxConcurrentSession: 1,
		ConnectTypes:         `["Oem"]`,
	}
	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Try to create a session (should fail)
	reqBody := map[string]string{"ConnectType": "Oem"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&response)
	errorObj := response["error"].(map[string]interface{})
	message := errorObj["message"].(string)
	if !contains(message, "not enabled") {
		t.Errorf("Expected 'not enabled' error, got: %s", message)
	}
}

func TestDisconnectConsole_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm",
		Name:                 "Test CM",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer db.DeleteConnectionMethod(ctx, cm.ID)

	// Create an active console session
	consoleSession := &models.ConsoleSession{
		SessionID:          "test-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          user.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/test-session",
	}
	if err := db.CreateConsoleSession(ctx, consoleSession); err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}

	// Disconnect the session
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Oem/Shoal/ConsoleSessions/test-session/Actions/Disconnect", nil)
	req.Header.Set("X-Auth-Token", session.Token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", rr.Code)
	}

	// Verify session is marked as disconnected
	updatedSession, err := db.GetConsoleSession(ctx, "test-session")
	if err != nil {
		t.Fatalf("Failed to get updated session: %v", err)
	}
	if updatedSession.State != models.ConsoleSessionStateDisconnected {
		t.Errorf("Expected session state to be disconnected, got %s", updatedSession.State)
	}
}

func TestConsoleSessionResource_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()
	user, session := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, session.ID)
	defer db.DeleteUser(ctx, user.ID)

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm",
		Name:                 "Test CM",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}
	defer db.DeleteConnectionMethod(ctx, cm.ID)

	// Create a console session
	consoleSession := &models.ConsoleSession{
		SessionID:          "test-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          user.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/test-session",
	}
	if err := db.CreateConsoleSession(ctx, consoleSession); err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}

	// Get the session resource
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/test-cm/Oem/Shoal/ConsoleSessions/test-session", nil)
	req.Header.Set("X-Auth-Token", session.Token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&response)

	if response["Id"] != "test-session" {
		t.Errorf("Expected session ID 'test-session', got %v", response["Id"])
	}
	if response["ConsoleType"] != "SerialConsole" {
		t.Errorf("Expected ConsoleType 'SerialConsole', got %v", response["ConsoleType"])
	}
	if response["State"] != "active" {
		t.Errorf("Expected State 'active', got %v", response["State"])
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
