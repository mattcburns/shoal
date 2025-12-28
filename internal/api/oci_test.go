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

// mockOCIConverter is a mock OCI converter for testing
func mockOCIConverter(ctx context.Context, imageRef string) (imageID, token string, err error) {
	return "test-oci-image-id", "test-oci-token-67890", nil
}

func TestInsertMediaWithOCIConversion(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Get token for the admin user
	token := loginAndGetToken(t, handler, "admin", "admin")

	// Create a test connection method
	ctx := context.Background()
	method := &models.ConnectionMethod{
		ID:                   "test-cm-oci",
		Name:                 "TestBMC-OCI",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	// Insert test virtual media resource
	err := db.UpsertVirtualMediaResource(ctx, "test-cm-oci", "BMC", "CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS","OEM"]`, nil, nil, false, false, "NotConnected")
	if err != nil {
		t.Fatalf("failed to insert virtual media resource: %v", err)
	}

	// Create handler with OCI converter
	proxyConfig := &ImageProxyConfig{
		Enabled:          true,
		BaseURL:          "http://localhost:8082",
		OCIConverterFunc: mockOCIConverter,
	}

	handlerWithOCI := newMux(NewRouterWithImageProxy(db, proxyConfig))

	// Create InsertMedia request with OCI image
	reqBody := redfish.InsertMediaRequest{
		Image: "oci://ghcr.io/fedora/coreos:stable",
		Oem: &redfish.InsertMediaRequestOem{
			Shoal: &redfish.ShoalInsertMediaOem{
				OCIConversion: true,
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost,
		"/redfish/v1/Managers/TestBMC-OCI/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		bytes.NewReader(bodyBytes))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlerWithOCI.ServeHTTP(rec, req)

	// We expect this to fail because we can't actually communicate with the BMC in tests
	// But it should get far enough to call the OCI converter
	// The converter should have been called, and we should see a log entry about OCI conversion

	if rec.Code == http.StatusNoContent {
		t.Error("expected failure due to BMC communication, but got success")
	}

	// The important thing is that the OCI converter was called and the URL was rewritten
	// We can verify this by checking that no "OCI image conversion not enabled" error was returned
	if rec.Code == http.StatusServiceUnavailable {
		t.Error("OCI conversion should have been enabled but got ServiceUnavailable")
	}
}

func TestInsertMediaWithOCIConversionNotEnabled(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Get token for the admin user
	token := loginAndGetToken(t, handler, "admin", "admin")

	// Create a test connection method
	ctx := context.Background()
	method := &models.ConnectionMethod{
		ID:                   "test-cm-no-oci",
		Name:                 "TestBMC-NoOCI",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	// Insert test virtual media resource
	err := db.UpsertVirtualMediaResource(ctx, "test-cm-no-oci", "BMC", "CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`, nil, nil, false, false, "NotConnected")
	if err != nil {
		t.Fatalf("failed to insert virtual media resource: %v", err)
	}

	// Create InsertMedia request with OCI image but OCI conversion not enabled
	reqBody := redfish.InsertMediaRequest{
		Image: "oci://ghcr.io/fedora/coreos:stable",
		Oem: &redfish.InsertMediaRequestOem{
			Shoal: &redfish.ShoalInsertMediaOem{
				OCIConversion: true,
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost,
		"/redfish/v1/Managers/TestBMC-NoOCI/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		bytes.NewReader(bodyBytes))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Use default handler (without OCI converter)
	handler.ServeHTTP(rec, req)

	// Should get ServiceUnavailable because OCI conversion is not enabled
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 (ServiceUnavailable), got %d: %s", rec.Code, rec.Body.String())
	}

	// Check error message
	var errResp redfish.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}

	expectedMsg := "OCI image conversion not enabled"
	if errResp.Error.Message != expectedMsg {
		t.Errorf("expected error message %q, got %q", expectedMsg, errResp.Error.Message)
	}
}

func TestInsertMediaWithOCIImageURLButNoOEMFlag(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Get token for the admin user
	token := loginAndGetToken(t, handler, "admin", "admin")

	// Create a test connection method
	ctx := context.Background()
	method := &models.ConnectionMethod{
		ID:                   "test-cm-oci-no-flag",
		Name:                 "TestBMC-OCINoFlag",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	// Insert test virtual media resource
	err := db.UpsertVirtualMediaResource(ctx, "test-cm-oci-no-flag", "BMC", "CD1",
		"/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`, nil, nil, false, false, "NotConnected")
	if err != nil {
		t.Fatalf("failed to insert virtual media resource: %v", err)
	}

	// Create InsertMedia request with OCI image URL but WITHOUT OCI conversion flag
	// This should be treated as a regular URL (and likely fail at BMC proxy stage)
	reqBody := redfish.InsertMediaRequest{
		Image: "oci://ghcr.io/fedora/coreos:stable",
		// No Oem.Shoal.OCIConversion flag
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost,
		"/redfish/v1/Managers/TestBMC-OCINoFlag/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		bytes.NewReader(bodyBytes))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should fail at BMC communication stage, not at OCI conversion stage
	// Important: should NOT get ServiceUnavailable (which would indicate OCI conversion was attempted)
	if rec.Code == http.StatusServiceUnavailable {
		// Check if it's the OCI error
		var errResp redfish.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err == nil {
			if errResp.Error.Message == "OCI image conversion not enabled" {
				t.Error("OCI conversion should not have been attempted without OCIConversion flag")
			}
		}
	}
}
