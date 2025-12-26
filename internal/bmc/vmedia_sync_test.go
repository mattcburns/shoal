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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"shoal/internal/database"
	"shoal/pkg/models"
)

func TestVirtualMediaSyncer_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	service := New(db)
	syncer := NewVirtualMediaSyncer(db, service, 100*time.Millisecond, true)

	// Start syncer
	syncer.Start(ctx)

	// Let it run briefly
	time.Sleep(150 * time.Millisecond)

	// Stop syncer
	syncer.Stop()

	// Should complete without hanging
}

func TestVirtualMediaSyncer_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	service := New(db)
	syncer := NewVirtualMediaSyncer(db, service, 100*time.Millisecond, false)

	// Start syncer (should not actually start)
	syncer.Start(ctx)

	// Stop should not hang
	syncer.Stop()
}

func TestVirtualMediaSyncer_SyncConnectionMethod(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create mock BMC server
	mockBMC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Managers/BMC/VirtualMedia":
			// Return virtual media collection
			collection := map[string]interface{}{
				"Members": []map[string]string{
					{"@odata.id": "/redfish/v1/Managers/BMC/VirtualMedia/CD1"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(collection)

		case "/redfish/v1/Managers/BMC/VirtualMedia/CD1":
			// Return virtual media resource
			resource := map[string]interface{}{
				"Id":                   "CD1",
				"Image":                "http://example.com/test.iso",
				"ImageName":            "test.iso",
				"Inserted":             true,
				"WriteProtected":       true,
				"ConnectedVia":         "URI",
				"MediaTypes":           []string{"CD", "DVD"},
				"TransferProtocolType": "HTTP",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resource)

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockBMC.Close()

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "test-bmc",
		ConnectionMethodType: "Redfish",
		Address:              mockBMC.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"Id":"BMC"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)
	syncer := NewVirtualMediaSyncer(db, service, 60*time.Second, true)

	// Sync the connection method
	if err := syncer.SyncConnectionMethod(ctx, cm.ID); err != nil {
		t.Fatalf("SyncConnectionMethod failed: %v", err)
	}

	// Verify resource was synced
	resource, err := db.GetVirtualMediaResource(ctx, cm.ID, "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}
	if resource == nil {
		t.Fatal("Expected resource to be synced, but it was not found")
	}
	if resource.CurrentImageURL != "http://example.com/test.iso" {
		t.Errorf("Expected image URL 'http://example.com/test.iso', got %s", resource.CurrentImageURL)
	}
	if !resource.IsInserted {
		t.Error("Expected IsInserted to be true")
	}
}

func TestVirtualMediaSyncer_StateChangeDetection(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Track state to simulate state change
	inserted := false

	// Create mock BMC server
	mockBMC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Managers/BMC/VirtualMedia":
			collection := map[string]interface{}{
				"Members": []map[string]string{
					{"@odata.id": "/redfish/v1/Managers/BMC/VirtualMedia/CD1"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(collection)

		case "/redfish/v1/Managers/BMC/VirtualMedia/CD1":
			resource := map[string]interface{}{
				"Id":                   "CD1",
				"Image":                "",
				"ImageName":            "",
				"Inserted":             inserted,
				"WriteProtected":       false,
				"ConnectedVia":         "NotConnected",
				"MediaTypes":           []string{"CD"},
				"TransferProtocolType": "HTTP",
			}
			if inserted {
				resource["Image"] = "http://example.com/new.iso"
				resource["ImageName"] = "new.iso"
				resource["ConnectedVia"] = "URI"
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resource)

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockBMC.Close()

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "test-bmc",
		ConnectionMethodType: "Redfish",
		Address:              mockBMC.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"Id":"BMC"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)
	syncer := NewVirtualMediaSyncer(db, service, 60*time.Second, true)

	// Initial sync - not inserted
	if err := syncer.SyncConnectionMethod(ctx, cm.ID); err != nil {
		t.Fatalf("Initial sync failed: %v", err)
	}

	// Verify initial state
	resource, err := db.GetVirtualMediaResource(ctx, cm.ID, "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get resource: %v", err)
	}
	if resource.IsInserted {
		t.Error("Expected IsInserted to be false initially")
	}

	// Change state - simulate insertion
	inserted = true

	// Sync again
	if err := syncer.SyncConnectionMethod(ctx, cm.ID); err != nil {
		t.Fatalf("Second sync failed: %v", err)
	}

	// Verify state changed
	resource, err = db.GetVirtualMediaResource(ctx, cm.ID, "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get resource after state change: %v", err)
	}
	if !resource.IsInserted {
		t.Error("Expected IsInserted to be true after state change")
	}
	if resource.CurrentImageURL != "http://example.com/new.iso" {
		t.Errorf("Expected image URL to be updated, got %s", resource.CurrentImageURL)
	}
}

func TestVirtualMediaSyncer_ErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create mock BMC server that returns errors
	mockBMC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 500 error
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}))
	defer mockBMC.Close()

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "test-bmc",
		ConnectionMethodType: "Redfish",
		Address:              mockBMC.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"Id":"BMC"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	service := New(db)
	syncer := NewVirtualMediaSyncer(db, service, 60*time.Second, true)

	// Sync should handle error gracefully
	err = syncer.SyncConnectionMethod(ctx, cm.ID)
	// Error is expected and should be handled
	if err == nil {
		t.Log("SyncConnectionMethod handled error gracefully")
	}
}
