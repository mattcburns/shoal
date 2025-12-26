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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shoal/internal/database"
	"shoal/pkg/models"
	"shoal/pkg/redfish"
)

func setupVirtualMediaTest(t *testing.T) (http.Handler, *database.DB, string) {
	t.Helper()

	handler, db := setupTestAPI(t)

	// Get token for the admin user that setupTestAPI creates
	token := loginAndGetToken(t, handler, "admin", "admin")

	// Create a test connection method
	ctx := context.Background()
	method := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "TestBMC",
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

	// Insert test virtual media resources
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "CD1", "/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`, "", "", false, false, "NotConnected")
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "Floppy1", "/redfish/v1/Managers/BMC/VirtualMedia/Floppy1",
		`["Floppy"]`, `["HTTP","HTTPS"]`, "", "", false, false, "NotConnected")
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "USBStick1", "/redfish/v1/Managers/BMC/VirtualMedia/USBStick1",
		`["USBStick"]`, `["HTTP","HTTPS","NFS"]`, "http://fileserver.example.com/isos/ubuntu-22.04.iso", "ubuntu-22.04.iso", true, true, "URI")

	return handler, db, token
}

func insertVirtualMediaResource(t *testing.T, db *database.DB, connMethodID, managerID, resourceID, odataID, mediaTypes, protocols, imageURL, imageName string, inserted, writeProtected bool, connectedVia string) {
	t.Helper()

	ctx := context.Background()

	var imgURL, imgName *string
	if imageURL != "" {
		imgURL = &imageURL
		imgName = &imageName
	}

	err := db.UpsertVirtualMediaResource(ctx, connMethodID, managerID, resourceID, odataID,
		mediaTypes, protocols, imgURL, imgName, inserted, writeProtected, connectedVia)
	if err != nil {
		t.Fatalf("failed to insert virtual media resource: %v", err)
	}
}

func TestVirtualMediaCollection_HappyPath(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var collection redfish.Collection
	if err := json.Unmarshal(rec.Body.Bytes(), &collection); err != nil {
		t.Fatalf("failed to parse collection response: %v", err)
	}

	// Verify collection structure
	if collection.ODataType != "#VirtualMediaCollection.VirtualMediaCollection" {
		t.Errorf("expected ODataType VirtualMediaCollection, got %s", collection.ODataType)
	}

	if collection.Name != "Virtual Media Services" {
		t.Errorf("expected Name 'Virtual Media Services', got %s", collection.Name)
	}

	// Verify we have 3 members
	if collection.MembersCount != 3 {
		t.Errorf("expected 3 members, got %d", collection.MembersCount)
	}

	if len(collection.Members) != 3 {
		t.Fatalf("expected 3 member entries, got %d", len(collection.Members))
	}

	// Verify member OData IDs
	expectedMembers := map[string]bool{
		"/redfish/v1/Managers/TestBMC/VirtualMedia/CD1":       false,
		"/redfish/v1/Managers/TestBMC/VirtualMedia/Floppy1":   false,
		"/redfish/v1/Managers/TestBMC/VirtualMedia/USBStick1": false,
	}

	for _, member := range collection.Members {
		if _, exists := expectedMembers[member.ODataID]; !exists {
			t.Errorf("unexpected member: %s", member.ODataID)
		}
		expectedMembers[member.ODataID] = true
	}

	for id, found := range expectedMembers {
		if !found {
			t.Errorf("missing expected member: %s", id)
		}
	}
}

func TestVirtualMediaCollection_EmptyCollection(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Get token for the admin user that setupTestAPI creates
	token := loginAndGetToken(t, handler, "admin", "admin")

	ctx := context.Background()
	method := &models.ConnectionMethod{
		ID:                   "test-cm-2",
		Name:                 "EmptyBMC",
		ConnectionMethodType: "Redfish",
		Address:              "https://bmc2.example.com",
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC2"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/EmptyBMC/VirtualMedia", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var collection redfish.Collection
	if err := json.Unmarshal(rec.Body.Bytes(), &collection); err != nil {
		t.Fatalf("failed to parse collection response: %v", err)
	}

	if collection.MembersCount != 0 {
		t.Errorf("expected 0 members, got %d", collection.MembersCount)
	}

	if len(collection.Members) != 0 {
		t.Errorf("expected empty members array, got %d entries", len(collection.Members))
	}
}

func TestVirtualMediaCollection_Unauthenticated(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestVirtualMediaCollection_MethodNotAllowed(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	// Test POST method
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestVirtualMediaCollection_OptionsMethod(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodOptions, "/redfish/v1/Managers/TestBMC/VirtualMedia", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	if allow != "GET" {
		t.Errorf("expected Allow: GET, got %s", allow)
	}
}

func TestVirtualMedia_HappyPath_NotInserted(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var vm redfish.VirtualMedia
	if err := json.Unmarshal(rec.Body.Bytes(), &vm); err != nil {
		t.Fatalf("failed to parse virtual media response: %v", err)
	}

	// Verify basic properties
	if vm.ODataType != "#VirtualMedia.v1_6_0.VirtualMedia" {
		t.Errorf("expected ODataType VirtualMedia.v1_6_0.VirtualMedia, got %s", vm.ODataType)
	}

	if vm.ID != "CD1" {
		t.Errorf("expected ID CD1, got %s", vm.ID)
	}

	if vm.Name != "Virtual Media CD1" {
		t.Errorf("expected Name 'Virtual Media CD1', got %s", vm.Name)
	}

	// Verify media types
	if len(vm.MediaTypes) != 2 {
		t.Fatalf("expected 2 media types, got %d", len(vm.MediaTypes))
	}

	expectedTypes := map[string]bool{"CD": false, "DVD": false}
	for _, mt := range vm.MediaTypes {
		if _, exists := expectedTypes[mt]; !exists {
			t.Errorf("unexpected media type: %s", mt)
		}
		expectedTypes[mt] = true
	}

	// Verify not inserted
	if vm.Inserted {
		t.Error("expected Inserted to be false")
	}

	if vm.Image != "" {
		t.Errorf("expected empty Image, got %s", vm.Image)
	}

	if vm.ConnectedVia != "NotConnected" {
		t.Errorf("expected ConnectedVia 'NotConnected', got %s", vm.ConnectedVia)
	}

	// Verify actions are present
	if vm.Actions == nil {
		t.Fatal("expected Actions to be present")
	}

	if vm.Actions.InsertMedia == nil {
		t.Error("expected InsertMedia action")
	} else if vm.Actions.InsertMedia.Target != "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia" {
		t.Errorf("unexpected InsertMedia target: %s", vm.Actions.InsertMedia.Target)
	}

	if vm.Actions.EjectMedia == nil {
		t.Error("expected EjectMedia action")
	}
}

func TestVirtualMedia_HappyPath_Inserted(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia/USBStick1", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var vm redfish.VirtualMedia
	if err := json.Unmarshal(rec.Body.Bytes(), &vm); err != nil {
		t.Fatalf("failed to parse virtual media response: %v", err)
	}

	// Verify inserted state
	if !vm.Inserted {
		t.Error("expected Inserted to be true")
	}

	if vm.Image != "http://fileserver.example.com/isos/ubuntu-22.04.iso" {
		t.Errorf("expected Image URL, got %s", vm.Image)
	}

	if vm.ImageName != "ubuntu-22.04.iso" {
		t.Errorf("expected ImageName 'ubuntu-22.04.iso', got %s", vm.ImageName)
	}

	if !vm.WriteProtected {
		t.Error("expected WriteProtected to be true")
	}

	if vm.ConnectedVia != "URI" {
		t.Errorf("expected ConnectedVia 'URI', got %s", vm.ConnectedVia)
	}

	if vm.TransferProtocolType != "HTTP" {
		t.Errorf("expected TransferProtocolType 'HTTP', got %s", vm.TransferProtocolType)
	}
}

func TestVirtualMedia_NotFound(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia/NonExistent", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestVirtualMedia_Unauthenticated(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestVirtualMedia_MethodNotAllowed(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodDelete, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestVirtualMedia_OptionsMethod(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodOptions, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	if allow != "GET" {
		t.Errorf("expected Allow: GET, got %s", allow)
	}
}

func TestVirtualMedia_HTTPSProtocol(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	// Insert a resource with HTTPS URL
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "CD2", "/redfish/v1/Managers/BMC/VirtualMedia/CD2",
		`["CD"]`, `["HTTPS"]`, "https://secure.example.com/images/test.iso", "test.iso", true, true, "URI")

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD2", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var vm redfish.VirtualMedia
	if err := json.Unmarshal(rec.Body.Bytes(), &vm); err != nil {
		t.Fatalf("failed to parse virtual media response: %v", err)
	}

	if vm.TransferProtocolType != "HTTPS" {
		t.Errorf("expected TransferProtocolType 'HTTPS', got %s", vm.TransferProtocolType)
	}
}

func TestVirtualMedia_NFSProtocol(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	// Insert a resource with NFS URL
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "CD3", "/redfish/v1/Managers/BMC/VirtualMedia/CD3",
		`["CD"]`, `["NFS"]`, "nfs://nfsserver.example.com/share/image.iso", "image.iso", true, false, "URI")

	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD3", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var vm redfish.VirtualMedia
	if err := json.Unmarshal(rec.Body.Bytes(), &vm); err != nil {
		t.Fatalf("failed to parse virtual media response: %v", err)
	}

	if vm.TransferProtocolType != "NFS" {
		t.Errorf("expected TransferProtocolType 'NFS', got %s", vm.TransferProtocolType)
	}
}

// TestInsertMedia_HappyPath tests successful InsertMedia operation
func TestInsertMedia_HappyPath(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	// Create mock BMC server
	mockBMC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Managers/BMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia" {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			// Verify request body
			var req redfish.InsertMediaRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("failed to decode request: %v", err)
			}
			if req.Image != "http://fileserver.example.com/test.iso" {
				t.Errorf("expected Image URL, got %s", req.Image)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected path: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockBMC.Close()

	// Update connection method to point to mock BMC
	ctx := context.Background()

	// Delete existing connection method
	if err := db.DeleteConnectionMethod(ctx, "test-cm-1"); err != nil {
		t.Fatalf("failed to delete connection method: %v", err)
	}

	// Create new connection method pointing to mock BMC
	method := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "TestBMC",
		ConnectionMethodType: "Redfish",
		Address:              mockBMC.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
		AggregatedSystems:    `[{"@odata.id":"/redfish/v1/Systems/System1"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	// Recreate virtual media resources
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "CD1", "/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`, "", "", false, false, "NotConnected")

	// Make InsertMedia request
	reqBody := `{"Image": "http://fileserver.example.com/test.iso", "Inserted": true, "WriteProtected": true}`
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia", strings.NewReader(reqBody))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify operation was recorded in database
	resource, err := db.GetVirtualMediaResource(ctx, "test-cm-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}

	ops, err := db.GetVirtualMediaOperations(ctx, resource.ID)
	if err != nil {
		t.Fatalf("failed to get operations: %v", err)
	}

	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(ops))
	}

	op := ops[0]
	if op.Operation != "insert" {
		t.Errorf("expected operation 'insert', got %s", op.Operation)
	}
	if op.Status != "success" {
		t.Errorf("expected status 'success', got %s", op.Status)
	}
	if op.ImageURL != "http://fileserver.example.com/test.iso" {
		t.Errorf("expected image URL, got %s", op.ImageURL)
	}

	// Verify resource state was updated
	resource, err = db.GetVirtualMediaResource(ctx, "test-cm-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}
	if resource.CurrentImageURL != "http://fileserver.example.com/test.iso" {
		t.Errorf("expected image URL, got %s", resource.CurrentImageURL)
	}
	if !resource.IsInserted {
		t.Error("expected resource to be inserted")
	}
}

// TestInsertMedia_MissingImage tests InsertMedia with missing Image field
func TestInsertMedia_MissingImage(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	reqBody := `{}`
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia", strings.NewReader(reqBody))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}

	if errResp["error"] == nil {
		t.Error("expected error object in response")
	}
}

// TestInsertMedia_BMCError tests InsertMedia when BMC returns an error
func TestInsertMedia_BMCError(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	// Create mock BMC server that returns an error
	mockBMC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Managers/BMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid image URL",
					"code":    "Base.1.0.GeneralError",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockBMC.Close()

	// Update connection method
	ctx := context.Background()

	// Delete existing connection method
	if err := db.DeleteConnectionMethod(ctx, "test-cm-1"); err != nil {
		t.Fatalf("failed to delete connection method: %v", err)
	}

	// Create new connection method pointing to mock BMC
	method := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "TestBMC",
		ConnectionMethodType: "Redfish",
		Address:              mockBMC.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	// Recreate virtual media resources
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "CD1", "/redfish/v1/Managers/BMC/VirtualMedia/CD1",
		`["CD","DVD"]`, `["HTTP","HTTPS"]`, "", "", false, false, "NotConnected")

	reqBody := `{"Image": "http://invalid-url"}`
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia", strings.NewReader(reqBody))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify operation was marked as failed
	resource, err := db.GetVirtualMediaResource(ctx, "test-cm-1", "BMC", "CD1")
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}

	ops, err := db.GetVirtualMediaOperations(ctx, resource.ID)
	if err != nil {
		t.Fatalf("failed to get operations: %v", err)
	}

	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(ops))
	}

	if ops[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %s", ops[0].Status)
	}
}

// TestEjectMedia_HappyPath tests successful EjectMedia operation
func TestEjectMedia_HappyPath(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	// Create mock BMC server
	mockBMC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Managers/BMC/VirtualMedia/USBStick1/Actions/VirtualMedia.EjectMedia" {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected path: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockBMC.Close()

	// Update connection method
	ctx := context.Background()

	// Delete existing connection method
	if err := db.DeleteConnectionMethod(ctx, "test-cm-1"); err != nil {
		t.Fatalf("failed to delete connection method: %v", err)
	}

	// Create new connection method pointing to mock BMC
	method := &models.ConnectionMethod{
		ID:                   "test-cm-1",
		Name:                 "TestBMC",
		ConnectionMethodType: "Redfish",
		Address:              mockBMC.URL,
		Username:             "admin",
		Password:             "password",
		Enabled:              true,
		AggregatedManagers:   `[{"@odata.id":"/redfish/v1/Managers/BMC"}]`,
	}
	if err := db.CreateConnectionMethod(ctx, method); err != nil {
		t.Fatalf("failed to create connection method: %v", err)
	}

	// Recreate virtual media resources
	insertVirtualMediaResource(t, db, "test-cm-1", "BMC", "USBStick1", "/redfish/v1/Managers/BMC/VirtualMedia/USBStick1",
		`["USBStick"]`, `["HTTP","HTTPS","NFS"]`, "http://fileserver.example.com/isos/ubuntu-22.04.iso", "ubuntu-22.04.iso", true, true, "URI")

	// Make EjectMedia request
	reqBody := `{}`
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia/USBStick1/Actions/VirtualMedia.EjectMedia", strings.NewReader(reqBody))
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify operation was recorded in database
	resource, err := db.GetVirtualMediaResource(ctx, "test-cm-1", "BMC", "USBStick1")
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}

	ops, err := db.GetVirtualMediaOperations(ctx, resource.ID)
	if err != nil {
		t.Fatalf("failed to get operations: %v", err)
	}

	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(ops))
	}

	op := ops[0]
	if op.Operation != "eject" {
		t.Errorf("expected operation 'eject', got %s", op.Operation)
	}
	if op.Status != "success" {
		t.Errorf("expected status 'success', got %s", op.Status)
	}

	// Verify resource state was updated
	resource, err = db.GetVirtualMediaResource(ctx, "test-cm-1", "BMC", "USBStick1")
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}
	if resource.CurrentImageURL != "" {
		t.Errorf("expected empty image URL, got %s", resource.CurrentImageURL)
	}
	if resource.IsInserted {
		t.Error("expected resource to not be inserted")
	}
	if resource.ConnectedVia != "NotConnected" {
		t.Errorf("expected ConnectedVia 'NotConnected', got %s", resource.ConnectedVia)
	}
}

// TestInsertMedia_Unauthenticated tests InsertMedia without authentication
func TestInsertMedia_Unauthenticated(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	reqBody := `{"Image": "http://example.com/test.iso"}`
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestEjectMedia_Unauthenticated tests EjectMedia without authentication
func TestEjectMedia_Unauthenticated(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestInsertMedia_OptionsMethod tests OPTIONS method for InsertMedia
func TestInsertMedia_OptionsMethod(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodOptions, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	if allow != "POST" {
		t.Errorf("expected Allow: POST, got %s", allow)
	}
}

// TestEjectMedia_OptionsMethod tests OPTIONS method for EjectMedia
func TestEjectMedia_OptionsMethod(t *testing.T) {
	handler, db, token := setupVirtualMediaTest(t)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodOptions, "/redfish/v1/Managers/TestBMC/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia", nil)
	req.Header.Set("X-Auth-Token", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	if allow != "POST" {
		t.Errorf("expected Allow: POST, got %s", allow)
	}
}
