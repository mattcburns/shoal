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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProvisioningKickstart_Success(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert a kickstart template
	kickstartContent := `#kickstart
install
text
url --url="http://mirror.example.com/centos/7/os/x86_64"
lang en_US.UTF-8
keyboard us
network --bootproto=dhcp --device=eth0 --hostname={{system_id}}
rootpw --plaintext changeme
firewall --enabled --service=ssh
authconfig --enableshadow --passalgo=sha512
selinux --enforcing
timezone --utc America/New_York
bootloader --location=mbr
zerombr
clearpart --all --initlabel
autopart
reboot

%packages
@core
@base
%end
`
	err := db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", kickstartContent)
	if err != nil {
		t.Fatalf("Failed to insert kickstart template: %v", err)
	}

	// Request the kickstart file via OEM endpoint
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/system-001/Oem/Shoal/ProvisioningConfiguration/Kickstart", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify content type
	contentType := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/plain") {
		t.Errorf("Expected Content-Type text/plain, got %s", contentType)
	}

	// Verify content includes system ID substitution
	body := rec.Body.String()
	if !strings.Contains(body, "network --bootproto=dhcp --device=eth0 --hostname=system-001") {
		t.Errorf("Template variable {{system_id}} was not substituted correctly")
	}

	// Verify it contains kickstart content
	if !strings.Contains(body, "#kickstart") {
		t.Errorf("Response does not contain kickstart content")
	}
}

func TestProvisioningPreseed_Success(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert a preseed template
	preseedContent := `#### Contents of the preconfiguration file
### Localization
d-i debian-installer/locale string en_US
d-i keyboard-configuration/xkb-keymap select us

### Network configuration
d-i netcfg/choose_interface select auto
d-i netcfg/get_hostname string {{system_id}}
d-i netcfg/get_domain string example.com

### Mirror settings
d-i mirror/country string manual
d-i mirror/http/hostname string archive.ubuntu.com
d-i mirror/http/directory string /ubuntu
d-i mirror/http/proxy string

### Account setup
d-i passwd/root-login boolean false
d-i passwd/user-fullname string Ubuntu User
d-i passwd/username string ubuntu
d-i passwd/user-password password changeme
d-i passwd/user-password-again password changeme

### Partitioning
d-i partman-auto/method string regular
d-i partman-auto/choose_recipe select atomic
d-i partman/confirm_write_new_label boolean true
d-i partman/choose_partition select finish
d-i partman/confirm boolean true
d-i partman/confirm_nooverwrite boolean true

### Package selection
tasksel tasksel/first multiselect standard
d-i pkgsel/include string openssh-server

### Boot loader installation
d-i grub-installer/only_debian boolean true

### Finishing up the installation
d-i finish-install/reboot_in_progress note
`
	err := db.UpsertProvisioningTemplate(ctx, "ubuntu-001", "preseed", preseedContent)
	if err != nil {
		t.Fatalf("Failed to insert preseed template: %v", err)
	}

	// Request the preseed file via OEM endpoint
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/ubuntu-001/Oem/Shoal/ProvisioningConfiguration/Preseed", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify content type
	contentType := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/plain") {
		t.Errorf("Expected Content-Type text/plain, got %s", contentType)
	}

	// Verify content includes system ID substitution
	body := rec.Body.String()
	if !strings.Contains(body, "d-i netcfg/get_hostname string ubuntu-001") {
		t.Errorf("Template variable {{system_id}} was not substituted correctly")
	}

	// Verify it contains preseed content
	if !strings.Contains(body, "d-i debian-installer/locale string en_US") {
		t.Errorf("Response does not contain preseed content")
	}
}

func TestProvisioningKickstart_NotFound(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Request a kickstart file that doesn't exist
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/nonexistent-system/Oem/Shoal/ProvisioningConfiguration/Kickstart", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisioningPreseed_NotFound(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Request a preseed file that doesn't exist
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/nonexistent-system/Oem/Shoal/ProvisioningConfiguration/Preseed", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisioningKickstart_MissingSystemID(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Request without system ID - this will result in a malformed path with double slash
	// The http.ServeMux will redirect this with a 301
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems//Oem/Shoal/ProvisioningConfiguration/Kickstart", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// With the new OEM structure and http.ServeMux behavior, we expect a 301 redirect for malformed paths
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("Expected 301, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisioningPreseed_MissingSystemID(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Request without system ID - this will result in a malformed path with double slash
	// The http.ServeMux will redirect this with a 301
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems//Oem/Shoal/ProvisioningConfiguration/Preseed", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// With the new OEM structure and http.ServeMux behavior, we expect a 301 redirect for malformed paths
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("Expected 301, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisioningKickstart_MethodNotAllowed(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert a template
	err := db.UpsertProvisioningTemplate(ctx, "system-001", "kickstart", "test content")
	if err != nil {
		t.Fatalf("Failed to insert template: %v", err)
	}

	// Try POST instead of GET
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Systems/system-001/Oem/Shoal/ProvisioningConfiguration/Kickstart", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisioningPreseed_MethodNotAllowed(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert a template
	err := db.UpsertProvisioningTemplate(ctx, "system-001", "preseed", "test content")
	if err != nil {
		t.Fatalf("Failed to insert template: %v", err)
	}

	// Try POST instead of GET
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Systems/system-001/Oem/Shoal/ProvisioningConfiguration/Preseed", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvisioningTemplateRendering(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert a template with multiple variable references
	templateContent := `hostname={{system_id}}
system_id={{system_id}}
test={{system_id}}.example.com`

	err := db.UpsertProvisioningTemplate(ctx, "test-123", "kickstart", templateContent)
	if err != nil {
		t.Fatalf("Failed to insert template: %v", err)
	}

	// Request the file
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/test-123/Oem/Shoal/ProvisioningConfiguration/Kickstart", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	// All instances should be replaced
	expectedCount := strings.Count(body, "test-123")
	if expectedCount != 3 {
		t.Errorf("Expected 3 instances of 'test-123', found %d. Body: %s", expectedCount, body)
	}

	// No unreplaced variables should remain
	if strings.Contains(body, "{{system_id}}") {
		t.Errorf("Found unreplaced template variable in output: %s", body)
	}
}
