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

package database

import (
	"context"
	"os"
	"testing"
	"time"

	"shoal/pkg/models"
)

func TestConsoleCapabilityOperations(t *testing.T) {
	// Create a temporary database for testing
	tempFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tempFile.Name()) }()
	_ = tempFile.Close()

	db, err := New(tempFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// Run migrations
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a connection method first
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-1",
		Name:                 "Test Connection",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	t.Run("UpsertConsoleCapability", func(t *testing.T) {
		cap := &models.ConsoleCapability{
			ConnectionMethodID:   "test-conn-1",
			ManagerID:            "BMC",
			ConsoleType:          models.ConsoleTypeSerial,
			ServiceEnabled:       true,
			MaxConcurrentSession: 1,
			ConnectTypes:         `["Oem"]`,
			VendorData:           `{"vendor":"Dell","endpoint":"/redfish/v1/Dell/Managers/BMC/SerialInterfaces"}`,
		}

		err := db.UpsertConsoleCapability(ctx, cap)
		if err != nil {
			t.Errorf("UpsertConsoleCapability failed: %v", err)
		}

		// Verify it was stored
		retrieved, err := db.GetConsoleCapability(ctx, "test-conn-1", "BMC", models.ConsoleTypeSerial)
		if err != nil {
			t.Errorf("GetConsoleCapability failed: %v", err)
		}
		if retrieved == nil {
			t.Fatal("Expected console capability, got nil")
		}
		if retrieved.ServiceEnabled != true {
			t.Errorf("Expected ServiceEnabled=true, got %v", retrieved.ServiceEnabled)
		}
		if retrieved.MaxConcurrentSession != 1 {
			t.Errorf("Expected MaxConcurrentSession=1, got %d", retrieved.MaxConcurrentSession)
		}
	})

	t.Run("UpsertConsoleCapability_Update", func(t *testing.T) {
		// Update existing capability
		cap := &models.ConsoleCapability{
			ConnectionMethodID:   "test-conn-1",
			ManagerID:            "BMC",
			ConsoleType:          models.ConsoleTypeSerial,
			ServiceEnabled:       false, // Changed
			MaxConcurrentSession: 2,     // Changed
			ConnectTypes:         `["Oem","SSH"]`,
			VendorData:           `{"vendor":"Dell"}`,
		}

		err := db.UpsertConsoleCapability(ctx, cap)
		if err != nil {
			t.Errorf("UpsertConsoleCapability update failed: %v", err)
		}

		// Verify it was updated
		retrieved, err := db.GetConsoleCapability(ctx, "test-conn-1", "BMC", models.ConsoleTypeSerial)
		if err != nil {
			t.Errorf("GetConsoleCapability failed: %v", err)
		}
		if retrieved.ServiceEnabled != false {
			t.Errorf("Expected ServiceEnabled=false after update, got %v", retrieved.ServiceEnabled)
		}
		if retrieved.MaxConcurrentSession != 2 {
			t.Errorf("Expected MaxConcurrentSession=2 after update, got %d", retrieved.MaxConcurrentSession)
		}
	})

	t.Run("GetConsoleCapabilities", func(t *testing.T) {
		// Add a graphical console capability
		graphicalCap := &models.ConsoleCapability{
			ConnectionMethodID:   "test-conn-1",
			ManagerID:            "BMC",
			ConsoleType:          models.ConsoleTypeGraphical,
			ServiceEnabled:       true,
			MaxConcurrentSession: 4,
			ConnectTypes:         `["KVMIP","Oem"]`,
			VendorData:           `{"vendor":"Dell","endpoint":"/redfish/v1/Dell/Managers/BMC/DellvKVM"}`,
		}

		if err := db.UpsertConsoleCapability(ctx, graphicalCap); err != nil {
			t.Fatalf("Failed to insert graphical capability: %v", err)
		}

		// Retrieve all capabilities for this manager
		caps, err := db.GetConsoleCapabilities(ctx, "test-conn-1", "BMC")
		if err != nil {
			t.Errorf("GetConsoleCapabilities failed: %v", err)
		}
		if len(caps) != 2 {
			t.Errorf("Expected 2 console capabilities, got %d", len(caps))
		}

		// Verify both types are present
		foundSerial := false
		foundGraphical := false
		for _, cap := range caps {
			if cap.ConsoleType == models.ConsoleTypeSerial {
				foundSerial = true
			}
			if cap.ConsoleType == models.ConsoleTypeGraphical {
				foundGraphical = true
			}
		}
		if !foundSerial || !foundGraphical {
			t.Error("Expected to find both serial and graphical console capabilities")
		}
	})

	t.Run("GetConsoleCapability_NotFound", func(t *testing.T) {
		cap, err := db.GetConsoleCapability(ctx, "non-existent", "BMC", models.ConsoleTypeSerial)
		if err != nil {
			t.Errorf("GetConsoleCapability should not error for non-existent: %v", err)
		}
		if cap != nil {
			t.Error("Expected nil for non-existent console capability")
		}
	})

	t.Run("CascadeDelete_ConnectionMethod", func(t *testing.T) {
		// Delete connection method should cascade delete capabilities
		if err := db.DeleteConnectionMethod(ctx, "test-conn-1"); err != nil {
			t.Fatalf("Failed to delete connection method: %v", err)
		}

		// Verify capabilities are deleted
		caps, err := db.GetConsoleCapabilities(ctx, "test-conn-1", "BMC")
		if err != nil {
			t.Errorf("GetConsoleCapabilities failed: %v", err)
		}
		if len(caps) != 0 {
			t.Errorf("Expected 0 capabilities after cascade delete, got %d", len(caps))
		}
	})
}

func TestConsoleSessionOperations(t *testing.T) {
	// Create a temporary database for testing
	tempFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tempFile.Name()) }()
	_ = tempFile.Close()

	db, err := New(tempFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// Run migrations
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a connection method first
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-2",
		Name:                 "Test Connection 2",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc2.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	t.Run("CreateConsoleSession", func(t *testing.T) {
		session := &models.ConsoleSession{
			SessionID:          "session-123",
			ConnectionMethodID: "test-conn-2",
			ManagerID:          "BMC",
			ConsoleType:        models.ConsoleTypeSerial,
			ConnectType:        "Oem",
			State:              models.ConsoleSessionStateConnecting,
			CreatedBy:          "testuser",
			WebSocketURI:       "wss://shoal.example.com/ws/console/session-123",
			BMCWebSocketURI:    "wss://bmc2.example.com/serial",
			Metadata:           `{"test":"data"}`,
		}

		err := db.CreateConsoleSession(ctx, session)
		if err != nil {
			t.Errorf("CreateConsoleSession failed: %v", err)
		}
		if session.ID == 0 {
			t.Error("Expected session ID to be set after creation")
		}
	})

	t.Run("GetConsoleSession", func(t *testing.T) {
		retrieved, err := db.GetConsoleSession(ctx, "session-123")
		if err != nil {
			t.Errorf("GetConsoleSession failed: %v", err)
		}
		if retrieved == nil {
			t.Fatal("Expected console session, got nil")
		}
		if retrieved.SessionID != "session-123" {
			t.Errorf("Expected SessionID=session-123, got %s", retrieved.SessionID)
		}
		if retrieved.State != models.ConsoleSessionStateConnecting {
			t.Errorf("Expected State=connecting, got %s", retrieved.State)
		}
		if retrieved.CreatedBy != "testuser" {
			t.Errorf("Expected CreatedBy=testuser, got %s", retrieved.CreatedBy)
		}
	})

	t.Run("UpdateConsoleSessionState_Active", func(t *testing.T) {
		err := db.UpdateConsoleSessionState(ctx, "session-123", models.ConsoleSessionStateActive, "")
		if err != nil {
			t.Errorf("UpdateConsoleSessionState failed: %v", err)
		}

		// Verify state was updated
		retrieved, err := db.GetConsoleSession(ctx, "session-123")
		if err != nil {
			t.Errorf("GetConsoleSession failed: %v", err)
		}
		if retrieved.State != models.ConsoleSessionStateActive {
			t.Errorf("Expected State=active after update, got %s", retrieved.State)
		}
	})

	t.Run("UpdateConsoleSessionState_Error", func(t *testing.T) {
		err := db.UpdateConsoleSessionState(ctx, "session-123", models.ConsoleSessionStateError, "Connection failed")
		if err != nil {
			t.Errorf("UpdateConsoleSessionState failed: %v", err)
		}

		// Verify state and error message
		retrieved, err := db.GetConsoleSession(ctx, "session-123")
		if err != nil {
			t.Errorf("GetConsoleSession failed: %v", err)
		}
		if retrieved.State != models.ConsoleSessionStateError {
			t.Errorf("Expected State=error, got %s", retrieved.State)
		}
		if retrieved.ErrorMessage != "Connection failed" {
			t.Errorf("Expected ErrorMessage='Connection failed', got %s", retrieved.ErrorMessage)
		}
	})

	t.Run("UpdateConsoleSessionState_Disconnected", func(t *testing.T) {
		err := db.UpdateConsoleSessionState(ctx, "session-123", models.ConsoleSessionStateDisconnected, "")
		if err != nil {
			t.Errorf("UpdateConsoleSessionState failed: %v", err)
		}

		// Verify disconnected_at timestamp is set
		retrieved, err := db.GetConsoleSession(ctx, "session-123")
		if err != nil {
			t.Errorf("GetConsoleSession failed: %v", err)
		}
		if retrieved.State != models.ConsoleSessionStateDisconnected {
			t.Errorf("Expected State=disconnected, got %s", retrieved.State)
		}
		if retrieved.DisconnectedAt == nil {
			t.Error("Expected DisconnectedAt to be set")
		}
	})

	t.Run("GetConsoleSessions_All", func(t *testing.T) {
		// Create another session
		session2 := &models.ConsoleSession{
			SessionID:          "session-456",
			ConnectionMethodID: "test-conn-2",
			ManagerID:          "BMC",
			ConsoleType:        models.ConsoleTypeGraphical,
			ConnectType:        "KVMIP",
			State:              models.ConsoleSessionStateActive,
			CreatedBy:          "testuser2",
		}
		if err := db.CreateConsoleSession(ctx, session2); err != nil {
			t.Fatalf("Failed to create second session: %v", err)
		}

		// Get all sessions for connection method
		sessions, err := db.GetConsoleSessions(ctx, "test-conn-2", "")
		if err != nil {
			t.Errorf("GetConsoleSessions failed: %v", err)
		}
		if len(sessions) != 2 {
			t.Errorf("Expected 2 console sessions, got %d", len(sessions))
		}
	})

	t.Run("GetConsoleSessions_FilterByState", func(t *testing.T) {
		// Get only active sessions
		sessions, err := db.GetConsoleSessions(ctx, "test-conn-2", models.ConsoleSessionStateActive)
		if err != nil {
			t.Errorf("GetConsoleSessions failed: %v", err)
		}
		if len(sessions) != 1 {
			t.Errorf("Expected 1 active session, got %d", len(sessions))
		}
		if sessions[0].State != models.ConsoleSessionStateActive {
			t.Errorf("Expected active state, got %s", sessions[0].State)
		}
	})

	t.Run("DeleteConsoleSession", func(t *testing.T) {
		err := db.DeleteConsoleSession(ctx, "session-123")
		if err != nil {
			t.Errorf("DeleteConsoleSession failed: %v", err)
		}

		// Verify it was deleted
		retrieved, err := db.GetConsoleSession(ctx, "session-123")
		if err != nil {
			t.Errorf("GetConsoleSession should not error: %v", err)
		}
		if retrieved != nil {
			t.Error("Session should have been deleted")
		}
	})

	t.Run("CascadeDelete_ConnectionMethod", func(t *testing.T) {
		// Delete connection method should cascade delete sessions
		if err := db.DeleteConnectionMethod(ctx, "test-conn-2"); err != nil {
			t.Fatalf("Failed to delete connection method: %v", err)
		}

		// Verify sessions are deleted
		sessions, err := db.GetConsoleSessions(ctx, "test-conn-2", "")
		if err != nil {
			t.Errorf("GetConsoleSessions failed: %v", err)
		}
		if len(sessions) != 0 {
			t.Errorf("Expected 0 sessions after cascade delete, got %d", len(sessions))
		}
	})
}

func TestConsoleLastActivityUpdate(t *testing.T) {
	// Create a temporary database for testing
	tempFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tempFile.Name()) }()
	_ = tempFile.Close()

	db, err := New(tempFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// Run migrations
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-3",
		Name:                 "Test Connection 3",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc3.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create session
	session := &models.ConsoleSession{
		SessionID:          "session-789",
		ConnectionMethodID: "test-conn-3",
		ManagerID:          "BMC",
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        "Oem",
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          "testuser",
	}
	if err := db.CreateConsoleSession(ctx, session); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Get initial last_activity
	initial, err := db.GetConsoleSession(ctx, "session-789")
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	// Wait long enough for timestamp to change (SQLite CURRENT_TIMESTAMP has second precision)
	time.Sleep(1100 * time.Millisecond)

	// Update state (should update last_activity)
	if err := db.UpdateConsoleSessionState(ctx, "session-789", models.ConsoleSessionStateActive, ""); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	// Verify last_activity was updated
	updated, err := db.GetConsoleSession(ctx, "session-789")
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if !updated.LastActivity.After(initial.LastActivity) {
		t.Errorf("Expected LastActivity to be updated: initial=%v, updated=%v", initial.LastActivity, updated.LastActivity)
	}
}
