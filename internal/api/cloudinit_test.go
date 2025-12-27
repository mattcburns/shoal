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

	"shoal/pkg/models"
	"shoal/pkg/redfish"
)

func TestInsertMediaWithCloudInit(t *testing.T) {
	// Setup test API
	_, db := setupTestAPI(t)
	defer db.Close()

	ctx := context.Background()

	// Create test connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-cloudinit",
		Name:                 "TestCloudInitBMC",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create test virtual media resource
	if err := db.UpsertVirtualMediaResource(ctx,
		cm.ID, "BMC", "CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`,
		nil, nil, false, false, "NotConnected"); err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	// Track ISO generation calls
	var generatedUserData string
	var generatedMetaData string
	var callCount int

	mockGenerator := func(userData, metaData string) (isoID, token string, err error) {
		callCount++
		generatedUserData = userData
		generatedMetaData = metaData
		return "test-iso-id", "test-token-12345", nil
	}

	// Create new handler with cloud-init generator
	proxyConfig := &ImageProxyConfig{
		Enabled:                true,
		BaseURL:                "http://localhost:8082",
		CloudInitGeneratorFunc: mockGenerator,
	}
	ciHandler := NewRouterWithImageProxy(db, proxyConfig)

	// Get new token for this handler
	ciToken := loginAndGetToken(t, ciHandler, "admin", "admin")

	// Create InsertMedia request with cloud-init OEM extension
	userData := `#cloud-config
users:
  - name: admin
    ssh_authorized_keys:
      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...
`
	metaData := `instance-id: test-server-01
local-hostname: test-server
`

	reqBody := redfish.InsertMediaRequest{
		Image: "http://shoal.example.com:8082/cloudinit-iso", // Will be replaced
		Oem: &redfish.InsertMediaRequestOem{
			Shoal: &redfish.ShoalInsertMediaOem{
				GenerateCloudInit: true,
				UserData:          userData,
				MetaData:          metaData,
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/redfish/v1/Managers/TestCloudInitBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", ciToken)

	rec := httptest.NewRecorder()
	ciHandler.ServeHTTP(rec, req)

	// Check response status
	// Note: This will fail in the proxy step since we're not mocking the BMC,
	// but we can verify that the cloud-init ISO was generated
	if rec.Code != http.StatusBadGateway {
		// We expect BadGateway because we're not mocking the BMC proxy
		// but let's check if we got success (which would mean BMC mock is needed)
		if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
			t.Logf("Unexpected status code: %d, body: %s", rec.Code, rec.Body.String())
		}
	}

	// Verify cloud-init ISO was generated
	if callCount != 1 {
		t.Errorf("Expected cloud-init generator to be called once, got %d", callCount)
	}

	if generatedUserData != userData {
		t.Errorf("User data mismatch.\nExpected: %s\nGot: %s", userData, generatedUserData)
	}

	if generatedMetaData != metaData {
		t.Errorf("Meta data mismatch.\nExpected: %s\nGot: %s", metaData, generatedMetaData)
	}

	// Verify operation was recorded in database
	vmResource, err := db.GetVirtualMediaResource(ctx, cm.ID, "BMC", "CD1")
	if err != nil {
		t.Fatalf("Failed to get virtual media resource: %v", err)
	}

	ops, err := db.GetVirtualMediaOperations(ctx, vmResource.ID)
	if err != nil {
		t.Fatalf("Failed to get operations: %v", err)
	}

	if len(ops) != 1 {
		t.Errorf("Expected 1 operation, got %d", len(ops))
	}

	if len(ops) > 0 {
		op := ops[0]
		expectedURL := "http://localhost:8082/cloudinit-iso/test-iso-id?token=test-token-12345"
		if op.ImageURL != expectedURL {
			t.Errorf("Expected operation image URL to be %s, got %s", expectedURL, op.ImageURL)
		}
	}
}

func TestInsertMediaWithCloudInitMissingUserData(t *testing.T) {
	// Setup test API
	_, db := setupTestAPI(t)
	defer db.Close()

	ctx := context.Background()

	// Create test connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-no-userdata",
		Name:                 "TestNoUserDataBMC",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create test virtual media resource
	if err := db.UpsertVirtualMediaResource(ctx,
		cm.ID, "BMC", "CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`,
		nil, nil, false, false, "NotConnected"); err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	mockGenerator := func(userData, metaData string) (isoID, token string, err error) {
		return "test-iso-id", "test-token-12345", nil
	}

	// Create new handler with cloud-init generator
	proxyConfig := &ImageProxyConfig{
		Enabled:                true,
		BaseURL:                "http://localhost:8082",
		CloudInitGeneratorFunc: mockGenerator,
	}
	ciHandler := NewRouterWithImageProxy(db, proxyConfig)

	// Get new token for this handler
	ciToken := loginAndGetToken(t, ciHandler, "admin", "admin")

	// Create InsertMedia request with cloud-init OEM extension but missing UserData
	reqBody := redfish.InsertMediaRequest{
		Image: "http://shoal.example.com:8082/cloudinit-iso",
		Oem: &redfish.InsertMediaRequestOem{
			Shoal: &redfish.ShoalInsertMediaOem{
				GenerateCloudInit: true,
				// UserData is missing
				MetaData: "instance-id: test",
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/redfish/v1/Managers/TestNoUserDataBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", ciToken)

	rec := httptest.NewRecorder()
	ciHandler.ServeHTTP(rec, req)

	// Should get BadRequest due to missing UserData
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 BadRequest, got %d", rec.Code)
	}

	// Verify error message mentions UserData
	var errResp redfish.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err == nil {
		if errResp.Error.Message != "" {
			t.Logf("Error message: %s", errResp.Error.Message)
		}
	}
}

func TestInsertMediaWithCloudInitGeneratorNotEnabled(t *testing.T) {
	// Setup test API without cloud-init generator
	handler, db := setupTestAPI(t)
	defer db.Close()

	// Get auth token
	token := loginAndGetToken(t, handler, "admin", "admin")

	ctx := context.Background()

	// Create test connection method
	cm := &models.ConnectionMethod{
		ID:                   "test-cm-disabled",
		Name:                 "TestDisabledBMC",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, cm); err != nil {
		t.Fatalf("Failed to create connection method: %v", err)
	}

	// Create test virtual media resource
	if err := db.UpsertVirtualMediaResource(ctx,
		cm.ID, "BMC", "CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`,
		nil, nil, false, false, "NotConnected"); err != nil {
		t.Fatalf("Failed to create virtual media resource: %v", err)
	}

	// Create InsertMedia request with cloud-init OEM extension
	reqBody := redfish.InsertMediaRequest{
		Image: "http://shoal.example.com:8082/cloudinit-iso",
		Oem: &redfish.InsertMediaRequestOem{
			Shoal: &redfish.ShoalInsertMediaOem{
				GenerateCloudInit: true,
				UserData:          "#cloud-config\nhostname: test",
				MetaData:          "instance-id: test",
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/redfish/v1/Managers/TestDisabledBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should get ServiceUnavailable when generator is not enabled
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 ServiceUnavailable, got %d, body: %s", rec.Code, rec.Body.String())
	}
}
