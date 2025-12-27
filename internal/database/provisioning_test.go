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
	"testing"
)

func TestProvisioningTemplate_CreateAndRetrieve(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create a kickstart template
	kickstartContent := "install\ntext\nurl --url=http://mirror.example.com/centos/7/os/x86_64"
	err = db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", kickstartContent)
	if err != nil {
		t.Fatalf("Failed to upsert kickstart template: %v", err)
	}

	// Retrieve the template
	template, err := db.GetProvisioningTemplate(ctx, "system-001", "kickstart")
	if err != nil {
		t.Fatalf("Failed to get template: %v", err)
	}

	if template == nil {
		t.Fatal("Expected template to be found")
	}

	if template.SystemID != "system-001" {
		t.Errorf("Expected system_id 'system-001', got '%s'", template.SystemID)
	}

	if template.TemplateType != "kickstart" {
		t.Errorf("Expected template_type 'kickstart', got '%s'", template.TemplateType)
	}

	if template.Content != kickstartContent {
		t.Errorf("Expected content to match, got '%s'", template.Content)
	}
}

func TestProvisioningTemplate_Update(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create initial template
	err = db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", "initial content")
	if err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}

	// Update the template
	updatedContent := "updated content with more details"
	err = db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", updatedContent)
	if err != nil {
		t.Fatalf("Failed to update template: %v", err)
	}

	// Retrieve and verify
	template, err := db.GetProvisioningTemplate(ctx, "system-001", "kickstart")
	if err != nil {
		t.Fatalf("Failed to get template: %v", err)
	}

	if template.Content != updatedContent {
		t.Errorf("Expected updated content, got '%s'", template.Content)
	}
}

func TestProvisioningTemplate_Delete(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create a template
	err = db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", "content")
	if err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}

	// Delete the template
	err = db.DeleteProvisioningTemplate(ctx, "system-001")
	if err != nil {
		t.Fatalf("Failed to delete template: %v", err)
	}

	// Verify it's gone
	template, err := db.GetProvisioningTemplate(ctx, "system-001", "kickstart")
	if err != nil {
		t.Fatalf("Failed to get template: %v", err)
	}

	if template != nil {
		t.Error("Expected template to be deleted")
	}
}

func TestProvisioningTemplate_NotFound(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Try to get a non-existent template
	template, err := db.GetProvisioningTemplate(ctx, "nonexistent", "kickstart")
	if err != nil {
		t.Fatalf("Expected nil error for not found, got: %v", err)
	}

	if template != nil {
		t.Error("Expected nil template for non-existent system")
	}
}

func TestProvisioningTemplate_MultipleTypes(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create kickstart template
	kickstartContent := "kickstart content"
	err = db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", kickstartContent)
	if err != nil {
		t.Fatalf("Failed to create kickstart template: %v", err)
	}

	// Create preseed template for the same system (should coexist with kickstart)
	preseedContent := "preseed content"
	err = db.UpsertProvisioningTemplate(ctx, "system-001", "preseed", preseedContent)
	if err != nil {
		t.Fatalf("Failed to create preseed template: %v", err)
	}

	// Both templates should exist
	preseedTemplate, err := db.GetProvisioningTemplate(ctx, "system-001", "preseed")
	if err != nil {
		t.Fatalf("Failed to get preseed template: %v", err)
	}

	if preseedTemplate == nil {
		t.Fatal("Expected preseed template to be found")
	}

	if preseedTemplate.Content != preseedContent {
		t.Errorf("Expected preseed content, got '%s'", preseedTemplate.Content)
	}

	// The kickstart template should still exist (not replaced by preseed)
	kickstartTemplate, err := db.GetProvisioningTemplate(ctx, "system-001", "kickstart")
	if err != nil {
		t.Fatalf("Error getting kickstart template: %v", err)
	}

	if kickstartTemplate == nil {
		t.Error("Expected kickstart template to still exist")
	}

	if kickstartTemplate.Content != kickstartContent {
		t.Errorf("Expected kickstart content, got '%s'", kickstartTemplate.Content)
	}
}
