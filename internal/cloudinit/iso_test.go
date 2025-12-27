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

package cloudinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateISO(t *testing.T) {
	// Create temporary storage directory
	tmpDir := t.TempDir()

	// Create generator
	gen, err := NewGenerator(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	// Test cloud-init data
	userData := `#cloud-config
users:
  - name: admin
    ssh_authorized_keys:
      - ssh-rsa AAAAB3...
`
	metaData := `instance-id: test-instance-01
local-hostname: test-host
`

	// Generate ISO
	isoID, token, err := gen.GenerateISO(userData, metaData)
	if err != nil {
		t.Fatalf("Failed to generate ISO: %v", err)
	}

	// Verify ID and token are not empty
	if isoID == "" {
		t.Error("ISO ID should not be empty")
	}
	if token == "" {
		t.Error("Token should not be empty")
	}

	// Verify ISO file exists
	isoPath := filepath.Join(tmpDir, isoID+".iso")
	if _, err := os.Stat(isoPath); os.IsNotExist(err) {
		t.Errorf("ISO file should exist at %s", isoPath)
	}

	// Retrieve ISO info with correct token
	info, err := gen.GetISO(isoID, token)
	if err != nil {
		t.Errorf("Failed to get ISO with valid token: %v", err)
	}
	if info == nil {
		t.Error("ISO info should not be nil")
	}
	if info.ID != isoID {
		t.Errorf("Expected ISO ID %s, got %s", isoID, info.ID)
	}

	// Try to retrieve with invalid token
	_, err = gen.GetISO(isoID, "invalid-token")
	if err == nil {
		t.Error("Should fail to get ISO with invalid token")
	}
}

func TestGenerateISOWithDefaultMetaData(t *testing.T) {
	tmpDir := t.TempDir()
	gen, err := NewGenerator(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	userData := `#cloud-config
hostname: test-server
`

	// Generate ISO without meta-data (should use default)
	isoID, token, err := gen.GenerateISO(userData, "")
	if err != nil {
		t.Fatalf("Failed to generate ISO: %v", err)
	}

	// Verify ISO was created
	if isoID == "" || token == "" {
		t.Error("ISO ID and token should not be empty")
	}

	isoPath := filepath.Join(tmpDir, isoID+".iso")
	if _, err := os.Stat(isoPath); os.IsNotExist(err) {
		t.Error("ISO file should exist")
	}
}

func TestGetISOWithExpiredToken(t *testing.T) {
	tmpDir := t.TempDir()
	gen, err := NewGenerator(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	userData := `#cloud-config
hostname: test
`

	isoID, token, err := gen.GenerateISO(userData, "")
	if err != nil {
		t.Fatalf("Failed to generate ISO: %v", err)
	}

	// Manually expire the ISO
	gen.mu.Lock()
	if info, exists := gen.isos[isoID]; exists {
		info.ExpiresAt = time.Now().Add(-1 * time.Hour)
	}
	gen.mu.Unlock()

	// Try to retrieve expired ISO
	_, err = gen.GetISO(isoID, token)
	if err == nil {
		t.Error("Should fail to get expired ISO")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("Error should mention expiration, got: %v", err)
	}
}

func TestMarkDownloaded(t *testing.T) {
	tmpDir := t.TempDir()
	gen, err := NewGenerator(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	userData := `#cloud-config
hostname: test
`

	isoID, _, err := gen.GenerateISO(userData, "")
	if err != nil {
		t.Fatalf("Failed to generate ISO: %v", err)
	}

	// Mark as downloaded
	gen.MarkDownloaded(isoID)

	// Verify downloaded flag is set
	gen.mu.RLock()
	info := gen.isos[isoID]
	gen.mu.RUnlock()

	if !info.Downloaded {
		t.Error("ISO should be marked as downloaded")
	}
}

func TestCleanupExpired(t *testing.T) {
	tmpDir := t.TempDir()
	gen, err := NewGenerator(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	userData := `#cloud-config
hostname: test
`

	// Generate multiple ISOs
	isoID1, _, err := gen.GenerateISO(userData, "")
	if err != nil {
		t.Fatalf("Failed to generate ISO 1: %v", err)
	}

	isoID2, _, err := gen.GenerateISO(userData, "")
	if err != nil {
		t.Fatalf("Failed to generate ISO 2: %v", err)
	}

	// Expire first ISO
	gen.mu.Lock()
	gen.isos[isoID1].ExpiresAt = time.Now().Add(-1 * time.Hour)
	gen.mu.Unlock()

	// Run cleanup
	if err := gen.CleanupExpired(); err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}

	// Verify first ISO is removed
	gen.mu.RLock()
	_, exists1 := gen.isos[isoID1]
	_, exists2 := gen.isos[isoID2]
	gen.mu.RUnlock()

	if exists1 {
		t.Error("Expired ISO should be removed")
	}
	if !exists2 {
		t.Error("Non-expired ISO should still exist")
	}

	// Verify ISO file is deleted
	isoPath1 := filepath.Join(tmpDir, isoID1+".iso")
	if _, err := os.Stat(isoPath1); !os.IsNotExist(err) {
		t.Error("Expired ISO file should be deleted")
	}

	// Verify second ISO file still exists
	isoPath2 := filepath.Join(tmpDir, isoID2+".iso")
	if _, err := os.Stat(isoPath2); os.IsNotExist(err) {
		t.Error("Non-expired ISO file should still exist")
	}
}

func TestGetISONotFound(t *testing.T) {
	tmpDir := t.TempDir()
	gen, err := NewGenerator(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	_, err = gen.GetISO("nonexistent-id", "some-token")
	if err == nil {
		t.Error("Should fail to get non-existent ISO")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Error should mention 'not found', got: %v", err)
	}
}
