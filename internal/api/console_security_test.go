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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shoal/pkg/models"
)

// TestConsoleSessionOwnershipEnforcement tests that users can only disconnect their own sessions
func TestConsoleSessionOwnershipEnforcement(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()

	// Create two users with unique usernames
	user1, session1 := createTestUserWithRole(t, db, "operator1", "operator")
	defer db.DeleteSession(ctx, session1.ID)
	defer db.DeleteUser(ctx, user1.ID)

	user2, session2 := createTestUserWithRole(t, db, "operator2", "operator")
	defer db.DeleteSession(ctx, session2.ID)
	defer db.DeleteUser(ctx, user2.ID)

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

	// Create a console session owned by user1
	consoleSession := &models.ConsoleSession{
		SessionID:          "user1-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          user1.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/user1-session",
	}
	if err := db.CreateConsoleSession(ctx, consoleSession); err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}

	// Try to disconnect user1's session as user2 (should fail)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Oem/Shoal/ConsoleSessions/user1-session/Actions/Disconnect", nil)
	req.Header.Set("X-Auth-Token", session2.Token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected status 403 for unauthorized disconnect, got %d", rr.Code)
	}

	// Verify session is still active
	updatedSession, err := db.GetConsoleSession(ctx, "user1-session")
	if err != nil {
		t.Fatalf("Failed to get updated session: %v", err)
	}
	if updatedSession.State != models.ConsoleSessionStateActive {
		t.Errorf("Session should still be active, got %s", updatedSession.State)
	}

	// Now try to disconnect as user1 (should succeed)
	req = httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Oem/Shoal/ConsoleSessions/user1-session/Actions/Disconnect", nil)
	req.Header.Set("X-Auth-Token", session1.Token)
	rr = httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("Expected status 204 for authorized disconnect, got %d", rr.Code)
	}
}

// TestConsoleSessionAdminCanDisconnectAny tests that admins can disconnect any session
func TestConsoleSessionAdminCanDisconnectAny(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()

	// Create operator and admin users
	operatorUser, operatorSession := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, operatorSession.ID)
	defer db.DeleteUser(ctx, operatorUser.ID)

	adminUser, adminSession := createTestUserAndSession(t, db, "admin")
	defer db.DeleteSession(ctx, adminSession.ID)
	defer db.DeleteUser(ctx, adminUser.ID)

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

	// Create a console session owned by operator
	consoleSession := &models.ConsoleSession{
		SessionID:          "operator-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          operatorUser.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       "/ws/console/operator-session",
	}
	if err := db.CreateConsoleSession(ctx, consoleSession); err != nil {
		t.Fatalf("Failed to create console session: %v", err)
	}

	// Admin should be able to disconnect operator's session
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Oem/Shoal/ConsoleSessions/operator-session/Actions/Disconnect", nil)
	req.Header.Set("X-Auth-Token", adminSession.Token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("Expected status 204 for admin disconnect, got %d", rr.Code)
	}
}

// TestConsoleSessionExpiredTokenRejection tests that expired session tokens are rejected
func TestConsoleSessionExpiredTokenRejection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()

	// Create user with expired session
	passwordHash, _ := hashPassword("password")
	user := &models.User{
		ID:           "expired-user",
		Username:     "expired",
		PasswordHash: passwordHash,
		Role:         "operator",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	defer db.DeleteUser(ctx, user.ID)

	// Create expired session
	expiredSession := &models.Session{
		ID:        "expired-session",
		UserID:    user.ID,
		Token:     "expired-token",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // Expired 1 hour ago
	}
	if err := db.CreateSession(ctx, expiredSession); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer db.DeleteSession(ctx, expiredSession.ID)

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

	// Try to connect console with expired token
	reqBody := map[string]string{"ConnectType": "Oem"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", expiredSession.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for expired token, got %d", rr.Code)
	}
}

// TestConsoleSessionRoleEnforcement tests that viewer role cannot create console sessions
func TestConsoleSessionRoleEnforcement(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewRouter(db)

	ctx := context.Background()

	// Test viewer role (should be rejected)
	viewerUser, viewerSession := createTestUserAndSession(t, db, "viewer")
	defer db.DeleteSession(ctx, viewerSession.ID)
	defer db.DeleteUser(ctx, viewerUser.ID)

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

	// Try to connect console with viewer role
	reqBody := map[string]string{"ConnectType": "Oem"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", viewerSession.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected status 403 for viewer role, got %d", rr.Code)
	}

	// Test operator role (should succeed)
	operatorUser, operatorSession := createTestUserAndSession(t, db, "operator")
	defer db.DeleteSession(ctx, operatorSession.ID)
	defer db.DeleteUser(ctx, operatorUser.ID)

	// Create console capability
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       true,
		MaxConcurrentSession: 10,
		ConnectTypes:         `["Oem"]`,
	}
	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	body, _ = json.Marshal(reqBody)
	req = httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", operatorSession.Token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("Expected status 201 for operator role, got %d", rr.Code)
	}
}

// TestConsoleSessionConcurrencyLimit tests concurrent session limit enforcement
func TestConsoleSessionConcurrencyLimit(t *testing.T) {
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

	// Create console capability with max 2 sessions
	capability := &models.ConsoleCapability{
		ConnectionMethodID:   cm.ID,
		ManagerID:            "",
		ConsoleType:          models.ConsoleTypeSerial,
		ServiceEnabled:       true,
		MaxConcurrentSession: 2,
		ConnectTypes:         `["Oem"]`,
	}
	if err := db.UpsertConsoleCapability(ctx, capability); err != nil {
		t.Fatalf("Failed to create console capability: %v", err)
	}

	// Create 2 active sessions
	for i := 0; i < 2; i++ {
		consoleSession := &models.ConsoleSession{
			SessionID:          fmt.Sprintf("session-%d", i),
			ConnectionMethodID: cm.ID,
			ManagerID:          cm.ID,
			ConsoleType:        models.ConsoleTypeSerial,
			ConnectType:        "Oem",
			State:              models.ConsoleSessionStateActive,
			CreatedBy:          user.Username,
			CreatedAt:          time.Now(),
			LastActivity:       time.Now(),
			WebSocketURI:       fmt.Sprintf("/ws/console/session-%d", i),
		}
		if err := db.CreateConsoleSession(ctx, consoleSession); err != nil {
			t.Fatalf("Failed to create console session: %v", err)
		}
	}

	// Try to create a 3rd session (should fail)
	reqBody := map[string]string{"ConnectType": "Oem"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/test-cm/Actions/Oem/Shoal.ConnectSerialConsole", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", session.Token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 for exceeding concurrent session limit, got %d", rr.Code)
	}
}

// TestConsoleSessionIdleTimeout tests that idle sessions are automatically cleaned up
func TestConsoleSessionIdleTimeout(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	h := &Handler{db: db}
	ctx := context.Background()

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

	// Create an active console session with old LastActivity timestamp
	oldSession := &models.ConsoleSession{
		SessionID:          "old-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          "testuser",
		CreatedAt:          time.Now().Add(-2 * time.Hour),
		LastActivity:       time.Now().Add(-35 * time.Minute), // Idle for 35 minutes (exceeds 30 min timeout)
		WebSocketURI:       "/ws/console/old-session",
	}
	if err := db.CreateConsoleSession(ctx, oldSession); err != nil {
		t.Fatalf("Failed to create old console session: %v", err)
	}

	// Create a recent console session that should NOT be cleaned up
	recentSession := &models.ConsoleSession{
		SessionID:          "recent-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          "testuser",
		CreatedAt:          time.Now().Add(-10 * time.Minute),
		LastActivity:       time.Now().Add(-5 * time.Minute), // Active recently
		WebSocketURI:       "/ws/console/recent-session",
	}
	if err := db.CreateConsoleSession(ctx, recentSession); err != nil {
		t.Fatalf("Failed to create recent console session: %v", err)
	}

	// Run cleanup
	t.Logf("Running cleanup with idle timeout: %v, max duration: %v", ConsoleSessionIdleTimeout, ConsoleSessionMaxDuration)
	t.Logf("Handler: %v, DB: %v", h != nil, h.db != nil)
	
	// Get connection methods to verify they exist
	cms, err := db.GetConnectionMethods(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection methods: %v", err)
	}
	t.Logf("Connection methods: %d", len(cms))
	
	// Check if sessions exist before cleanup
	for _, cm := range cms {
		sessions, err := db.GetConsoleSessions(ctx, cm.ID, models.ConsoleSessionStateActive)
		if err != nil {
			t.Fatalf("Failed to get sessions: %v", err)
		}
		t.Logf("Active sessions for CM %s: %d", cm.ID, len(sessions))
		for _, s := range sessions {
			t.Logf("  Session %s: idle=%v, total=%v", s.SessionID, time.Now().Sub(s.LastActivity), time.Now().Sub(s.CreatedAt))
		}
	}
	
	h.cleanupIdleAndExpiredSessions(ctx)
	t.Logf("Cleanup complete")

	// Verify old session was disconnected
	updatedOld, err := db.GetConsoleSession(ctx, "old-session")
	if err != nil {
		t.Fatalf("Failed to get updated old session: %v", err)
	}
	if updatedOld.State != models.ConsoleSessionStateDisconnected {
		t.Errorf("Old session should be disconnected due to idle timeout, got %s (idle: %v)", updatedOld.State, time.Now().Sub(oldSession.LastActivity))
	}

	// Verify recent session is still active
	updatedRecent, err := db.GetConsoleSession(ctx, "recent-session")
	if err != nil {
		t.Fatalf("Failed to get updated recent session: %v", err)
	}
	if updatedRecent.State != models.ConsoleSessionStateActive {
		t.Errorf("Recent session should still be active, got %s", updatedRecent.State)
	}
}

// TestConsoleSessionMaxDuration tests that sessions are automatically disconnected after max duration
func TestConsoleSessionMaxDuration(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	h := &Handler{db: db}
	ctx := context.Background()

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

	// Create a console session that exceeds max duration
	longSession := &models.ConsoleSession{
		SessionID:          "long-session",
		ConnectionMethodID: cm.ID,
		ManagerID:          cm.ID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateActive,
		CreatedBy:          "testuser",
		CreatedAt:          time.Now().Add(-13 * time.Hour), // Exceeds 12 hour max
		LastActivity:       time.Now().Add(-1 * time.Minute), // Still active
		WebSocketURI:       "/ws/console/long-session",
	}
	if err := db.CreateConsoleSession(ctx, longSession); err != nil {
		t.Fatalf("Failed to create long console session: %v", err)
	}

	// Run cleanup
	h.cleanupIdleAndExpiredSessions(ctx)

	// Verify long session was disconnected
	updatedLong, err := db.GetConsoleSession(ctx, "long-session")
	if err != nil {
		t.Fatalf("Failed to get updated long session: %v", err)
	}
	if updatedLong.State != models.ConsoleSessionStateDisconnected {
		t.Errorf("Long session should be disconnected, got %s", updatedLong.State)
	}
}
