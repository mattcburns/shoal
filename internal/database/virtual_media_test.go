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
	"path/filepath"
	"testing"

	"shoal/pkg/models"
)

func TestVirtualMediaResourceOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create a connection method first
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-1",
		Name:                 "test-connection",
		ConnectionMethodType: "Redfish",
		Address:              "https://192.168.1.100",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Test UpsertVirtualMediaResource (insert)
	imageURL := "http://example.com/test.iso"
	imageName := "test.iso"
	err = db.UpsertVirtualMediaResource(ctx,
		"test-conn-1",
		"BMC",
		"CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD", "DVD"]`,
		`["HTTP", "HTTPS"]`,
		&imageURL,
		&imageName,
		true,
		true,
		"URI")
	if err != nil {
		t.Fatalf("Failed to upsert virtual media resource: %v", err)
	}

	// Test GetVirtualMediaResource
	resource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}
	if resource == nil {
		t.Fatal("Retrieved resource is nil")
	}
	if resource.ResourceID != "CD1" {
		t.Errorf("Expected ResourceID 'CD1', got %s", resource.ResourceID)
	}
	if resource.CurrentImageURL != imageURL {
		t.Errorf("Expected CurrentImageURL %s, got %s", imageURL, resource.CurrentImageURL)
	}
	if !resource.IsInserted {
		t.Error("Expected IsInserted to be true")
	}

	// Test GetVirtualMediaResources
	resources, err := db.GetVirtualMediaResources(ctx, "test-conn-1", "BMC")
	if err != nil {
		t.Fatalf("Failed to get virtual media resources: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("Expected 1 resource, got %d", len(resources))
	}

	// Test UpsertVirtualMediaResource (update)
	newImageURL := "http://example.com/new.iso"
	newImageName := "new.iso"
	err = db.UpsertVirtualMediaResource(ctx,
		"test-conn-1",
		"BMC",
		"CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD", "DVD"]`,
		`["HTTP", "HTTPS", "NFS"]`,
		&newImageURL,
		&newImageName,
		false,
		false,
		"NotConnected")
	if err != nil {
		t.Fatalf("Failed to update virtual media resource: %v", err)
	}

	// Verify update
	updatedResource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get updated virtual media resource: %v", err)
	}
	if updatedResource.CurrentImageURL != newImageURL {
		t.Errorf("Expected updated CurrentImageURL %s, got %s", newImageURL, updatedResource.CurrentImageURL)
	}
	if updatedResource.IsInserted {
		t.Error("Expected IsInserted to be false after update")
	}
	if updatedResource.SupportedProtocols != `["HTTP", "HTTPS", "NFS"]` {
		t.Errorf("Expected updated SupportedProtocols, got %s", updatedResource.SupportedProtocols)
	}
}

func TestVirtualMediaOperationCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create a connection method and virtual media resource first
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-1",
		Name:                 "test-connection",
		ConnectionMethodType: "Redfish",
		Address:              "https://192.168.1.100",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	imageURL := "http://example.com/test.iso"
	imageName := "test.iso"
	err = db.UpsertVirtualMediaResource(ctx,
		"test-conn-1",
		"BMC",
		"CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD"]`,
		`["HTTP"]`,
		&imageURL,
		&imageName,
		false,
		true,
		"NotConnected")
	if err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	// Get the resource to get its ID
	resource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}

	// Test CreateVirtualMediaOperation
	op := &VirtualMediaOperation{
		VirtualMediaResourceID: resource.ID,
		Operation:              "insert",
		ImageURL:               "http://example.com/insert.iso",
		RequestedBy:            "admin",
		Status:                 "pending",
	}
	err = db.CreateVirtualMediaOperation(ctx, op)
	if err != nil {
		t.Fatalf("Failed to create virtual media operation: %v", err)
	}

	if op.ID == 0 {
		t.Fatal("Operation ID was not set after creation")
	}

	// Test GetVirtualMediaOperation
	retrievedOp, err := db.GetVirtualMediaOperation(ctx, op.ID)
	if err != nil {
		t.Fatalf("Failed to get virtual media operation: %v", err)
	}
	if retrievedOp == nil {
		t.Fatal("Retrieved operation is nil")
	}
	if retrievedOp.Operation != "insert" {
		t.Errorf("Expected operation 'insert', got %s", retrievedOp.Operation)
	}
	if retrievedOp.ImageURL != "http://example.com/insert.iso" {
		t.Errorf("Expected ImageURL 'http://example.com/insert.iso', got %s", retrievedOp.ImageURL)
	}
	if retrievedOp.Status != "pending" {
		t.Errorf("Expected status 'pending', got %s", retrievedOp.Status)
	}

	// Test UpdateVirtualMediaOperationStatus
	err = db.UpdateVirtualMediaOperationStatus(ctx, op.ID, "success", "")
	if err != nil {
		t.Fatalf("Failed to update operation status: %v", err)
	}

	// Verify update
	updatedOp, err := db.GetVirtualMediaOperation(ctx, op.ID)
	if err != nil {
		t.Fatalf("Failed to get updated operation: %v", err)
	}
	if updatedOp.Status != "success" {
		t.Errorf("Expected status 'success', got %s", updatedOp.Status)
	}
	if updatedOp.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set after status update")
	}

	// Test GetVirtualMediaOperations
	operations, err := db.GetVirtualMediaOperations(ctx, resource.ID)
	if err != nil {
		t.Fatalf("Failed to get virtual media operations: %v", err)
	}
	if len(operations) != 1 {
		t.Errorf("Expected 1 operation, got %d", len(operations))
	}
	if operations[0].ID != op.ID {
		t.Errorf("Expected operation ID %d, got %d", op.ID, operations[0].ID)
	}
}

func TestVirtualMediaOperationWithError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create dependencies
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-1",
		Name:                 "test-connection",
		ConnectionMethodType: "Redfish",
		Address:              "https://192.168.1.100",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	err = db.UpsertVirtualMediaResource(ctx,
		"test-conn-1",
		"BMC",
		"CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD"]`,
		`["HTTP"]`,
		nil,
		nil,
		false,
		true,
		"NotConnected")
	if err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	resource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}

	// Create an operation with an error
	op := &VirtualMediaOperation{
		VirtualMediaResourceID: resource.ID,
		Operation:              "insert",
		ImageURL:               "http://example.com/bad.iso",
		RequestedBy:            "admin",
		Status:                 "failed",
		ErrorMessage:           "Connection timeout",
	}
	err = db.CreateVirtualMediaOperation(ctx, op)
	if err != nil {
		t.Fatalf("Failed to create failed operation: %v", err)
	}

	// Verify error message was stored
	retrievedOp, err := db.GetVirtualMediaOperation(ctx, op.ID)
	if err != nil {
		t.Fatalf("Failed to get operation: %v", err)
	}
	if retrievedOp.ErrorMessage != "Connection timeout" {
		t.Errorf("Expected error message 'Connection timeout', got %s", retrievedOp.ErrorMessage)
	}
}

func TestMultipleVirtualMediaOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create dependencies
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-1",
		Name:                 "test-connection",
		ConnectionMethodType: "Redfish",
		Address:              "https://192.168.1.100",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	err = db.UpsertVirtualMediaResource(ctx,
		"test-conn-1",
		"BMC",
		"CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD"]`,
		`["HTTP"]`,
		nil,
		nil,
		false,
		true,
		"NotConnected")
	if err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	resource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}

	// Create multiple operations
	operations := []struct {
		operation string
		imageURL  string
		status    string
	}{
		{"insert", "http://example.com/iso1.iso", "success"},
		{"eject", "", "success"},
		{"insert", "http://example.com/iso2.iso", "pending"},
	}

	for _, opData := range operations {
		op := &VirtualMediaOperation{
			VirtualMediaResourceID: resource.ID,
			Operation:              opData.operation,
			ImageURL:               opData.imageURL,
			RequestedBy:            "admin",
			Status:                 opData.status,
		}
		if err := db.CreateVirtualMediaOperation(ctx, op); err != nil {
			t.Fatalf("Failed to create operation: %v", err)
		}
	}

	// Verify we can retrieve all operations
	ops, err := db.GetVirtualMediaOperations(ctx, resource.ID)
	if err != nil {
		t.Fatalf("Failed to get operations: %v", err)
	}
	if len(ops) != 3 {
		t.Errorf("Expected 3 operations, got %d", len(ops))
	}

	// Verify all operations are present
	foundInsert := 0
	foundEject := 0
	for _, op := range ops {
		if op.Operation == "insert" {
			foundInsert++
		}
		if op.Operation == "eject" {
			foundEject++
		}
	}
	if foundInsert != 2 {
		t.Errorf("Expected 2 insert operations, got %d", foundInsert)
	}
	if foundEject != 1 {
		t.Errorf("Expected 1 eject operation, got %d", foundEject)
	}
}

func TestVirtualMediaResourceDeletion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Create connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-conn-1",
		Name:                 "test-connection",
		ConnectionMethodType: "Redfish",
		Address:              "https://192.168.1.100",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create virtual media resource
	err = db.UpsertVirtualMediaResource(ctx,
		"test-conn-1",
		"BMC",
		"CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD"]`,
		`["HTTP"]`,
		nil,
		nil,
		false,
		true,
		"NotConnected")
	if err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	resource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}

	// Create an operation
	op := &VirtualMediaOperation{
		VirtualMediaResourceID: resource.ID,
		Operation:              "insert",
		ImageURL:               "http://example.com/test.iso",
		RequestedBy:            "admin",
		Status:                 "pending",
	}
	if err := db.CreateVirtualMediaOperation(ctx, op); err != nil {
		t.Fatalf("Failed to create operation: %v", err)
	}

	// Delete the connection method (should cascade delete resources and operations)
	if err := db.DeleteConnectionMethod(ctx, "test-conn-1"); err != nil {
		t.Fatalf("Failed to delete connection method: %v", err)
	}

	// Verify resource is deleted
	deletedResource, err := db.GetVirtualMediaResource(ctx, "test-conn-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("Error checking for deleted resource: %v", err)
	}
	if deletedResource != nil {
		t.Error("Expected resource to be deleted, but it still exists")
	}

	// Verify operation is deleted (cascade from resource deletion)
	deletedOp, err := db.GetVirtualMediaOperation(ctx, op.ID)
	if err != nil {
		t.Fatalf("Error checking for deleted operation: %v", err)
	}
	if deletedOp != nil {
		t.Error("Expected operation to be deleted, but it still exists")
	}
}
