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

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shoal/internal/api"
	"shoal/internal/database"
	"shoal/internal/web"
	"shoal/pkg/auth"
	"shoal/pkg/models"
)

// TestServer provides an integration test server
type TestServer struct {
	DB         *database.DB
	APIHandler http.Handler
	WebHandler http.Handler
	Server     *httptest.Server
}

func setupTestServer(t *testing.T) *TestServer {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Disable foreign key constraints for testing
	if err := db.DisableForeignKeys(); err != nil {
		t.Fatalf("Failed to disable foreign keys: %v", err)
	}

	// Create test admin user
	if err := createTestAdminUser(ctx, db); err != nil {
		t.Fatalf("Failed to create test admin user: %v", err)
	}

	// Create handlers
	apiHandler := api.New(db)
	webHandler := web.New(db)

	// Create combined server
	mux := http.NewServeMux()
	mux.Handle("/redfish/", apiHandler)
	mux.Handle("/ws/", apiHandler) // WebSocket console endpoints
	mux.Handle("/", webHandler)

	server := httptest.NewServer(mux)

	return &TestServer{
		DB:         db,
		APIHandler: apiHandler,
		WebHandler: webHandler,
		Server:     server,
	}
}

func (ts *TestServer) Close() {
	if ts.Server != nil {
		ts.Server.Close()
	}
	if ts.DB != nil {
		_ = ts.DB.Close()
	}
}

// createTestAdminUser creates a test admin user for integration tests
func createTestAdminUser(ctx context.Context, db *database.DB) error {
	// Hash the test password
	passwordHash, err := auth.HashPassword("admin")
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Generate user ID
	userIDBytes := make([]byte, 16)
	if _, err := rand.Read(userIDBytes); err != nil {
		return fmt.Errorf("failed to generate user ID: %w", err)
	}
	userID := hex.EncodeToString(userIDBytes)

	// Create the test admin user
	adminUser := &models.User{
		ID:           userID,
		Username:     "admin",
		PasswordHash: passwordHash,
		Role:         models.RoleAdmin,
		Enabled:      true,
	}

	return db.CreateUser(ctx, adminUser)
}

func TestRedfishServiceRoot(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	// Test unauthenticated access to service root (should work)
	resp, err := http.Get(ts.Server.URL + "/redfish/v1/")
	if err != nil {
		t.Fatalf("Failed to get service root: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Error("Expected JSON content type")
	}

	// Parse response
	var serviceRoot map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&serviceRoot); err != nil {
		t.Fatalf("Failed to parse service root response: %v", err)
	}

	// Verify required fields
	if serviceRoot["Name"] != "Shoal Redfish Aggregator" {
		t.Errorf("Expected service name 'Shoal Redfish Aggregator', got %v", serviceRoot["Name"])
	}

	if serviceRoot["RedfishVersion"] != "1.6.0" {
		t.Errorf("Expected Redfish version '1.6.0', got %v", serviceRoot["RedfishVersion"])
	}
}

func TestRedfishSessionAuthentication(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	// Create session
	loginData := map[string]string{
		"UserName": "admin",
		"Password": "admin",
	}
	loginJSON, _ := json.Marshal(loginData)

	resp, err := http.Post(
		ts.Server.URL+"/redfish/v1/SessionService/Sessions",
		"application/json",
		bytes.NewBuffer(loginJSON),
	)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 201, got %d. Response: %s", resp.StatusCode, string(body))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}

	// Extract session token
	token := resp.Header.Get("X-Auth-Token")
	if token == "" {
		t.Fatal("Expected X-Auth-Token header")
	}

	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("Expected Location header")
	}

	// Parse session response
	var sessionResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		t.Fatalf("Failed to parse session response: %v", err)
	}

	if sessionResp["UserName"] != "admin" {
		t.Errorf("Expected username 'admin', got %v", sessionResp["UserName"])
	}

	// Test using session token
	req, _ := http.NewRequest("GET", ts.Server.URL+"/redfish/v1/Managers", nil)
	req.Header.Set("X-Auth-Token", token)

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to use session token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 with session token, got %d", resp.StatusCode)
	}
}

func TestRedfishBasicAuthentication(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	// Test basic authentication
	req, _ := http.NewRequest("GET", ts.Server.URL+"/redfish/v1/Managers", nil)
	req.SetBasicAuth("admin", "admin")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to use basic auth: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 with basic auth, got %d", resp.StatusCode)
	}

	// Parse managers collection
	var collection map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		t.Fatalf("Failed to parse managers collection: %v", err)
	}

	if collection["Name"] != "Manager Collection" {
		t.Errorf("Expected collection name 'Manager Collection', got %v", collection["Name"])
	}
}

func TestRedfishUnauthorizedAccess(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	// Test unauthorized access to protected endpoint
	resp, err := http.Get(ts.Server.URL + "/redfish/v1/Managers")
	if err != nil {
		t.Fatalf("Failed to test unauthorized access: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp.StatusCode)
	}

	// Verify WWW-Authenticate header
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("Expected WWW-Authenticate header")
	}

	// Parse error response
	var errorResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&errorResp); err != nil {
		t.Fatalf("Failed to parse error response: %v", err)
	}

	errorObj, ok := errorResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected error object in response")
	}

	if !strings.Contains(errorObj["code"].(string), "Unauthorized") {
		t.Errorf("Expected Unauthorized error code, got %v", errorObj["code"])
	}
}

func TestBMCManagementWorkflow(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	ctx := context.Background()

	// Add a test BMC through the database
	bmc := &models.BMC{
		Name:        "test-bmc-1",
		Address:     "192.168.1.100",
		Username:    "root",
		Password:    "calvin",
		Description: "Test BMC for integration tests",
		Enabled:     true,
	}

	if err := ts.DB.CreateBMC(ctx, bmc); err != nil {
		t.Fatalf("Failed to create test BMC: %v", err)
	}

	// Test managers collection includes our BMC
	req, _ := http.NewRequest("GET", ts.Server.URL+"/redfish/v1/Managers", nil)
	req.SetBasicAuth("admin", "admin")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to get managers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var collection map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		t.Fatalf("Failed to parse managers collection: %v", err)
	}

	members, ok := collection["Members"].([]interface{})
	if !ok {
		t.Fatal("Expected Members array in collection")
	}

	if len(members) != 1 {
		t.Errorf("Expected 1 manager, got %d", len(members))
	}

	// Verify member URL
	member := members[0].(map[string]interface{})
	expectedURL := "/redfish/v1/Managers/test-bmc-1"
	if member["@odata.id"] != expectedURL {
		t.Errorf("Expected member URL %s, got %v", expectedURL, member["@odata.id"])
	}

	// Test systems collection includes our BMC
	req, _ = http.NewRequest("GET", ts.Server.URL+"/redfish/v1/Systems", nil)
	req.SetBasicAuth("admin", "admin")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to get systems: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		t.Fatalf("Failed to parse systems collection: %v", err)
	}

	members, ok = collection["Members"].([]interface{})
	if !ok {
		t.Fatal("Expected Members array in systems collection")
	}

	if len(members) != 1 {
		t.Errorf("Expected 1 system, got %d", len(members))
	}

	// Verify system member URL
	member = members[0].(map[string]interface{})
	expectedURL = "/redfish/v1/Systems/test-bmc-1"
	if member["@odata.id"] != expectedURL {
		t.Errorf("Expected system URL %s, got %v", expectedURL, member["@odata.id"])
	}
}

func TestWebInterface(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	// Test home page
	resp, err := http.Get(ts.Server.URL + "/")
	if err != nil {
		t.Fatalf("Failed to get home page: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Error("Expected HTML content type")
	}

	// Test BMC management page
	resp, err = http.Get(ts.Server.URL + "/bmcs")
	if err != nil {
		t.Fatalf("Failed to get BMCs page: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Test add BMC page
	resp, err = http.Get(ts.Server.URL + "/bmcs/add")
	if err != nil {
		t.Fatalf("Failed to get add BMC page: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestConcurrentRequests(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.Close()

	// Test concurrent requests to service root
	const numRequests = 50
	results := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			resp, err := http.Get(ts.Server.URL + "/redfish/v1/")
			if err != nil {
				results <- err
				return
			}
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				results <- fmt.Errorf("expected status 200, got %d", resp.StatusCode)
				return
			}

			results <- nil
		}()
	}

	// Wait for all requests to complete
	for i := 0; i < numRequests; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("Concurrent request failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Concurrent request timed out")
		}
	}
}

func BenchmarkRedfishServiceRoot(b *testing.B) {
	ts := setupTestServer(&testing.T{})
	defer ts.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := http.Get(ts.Server.URL + "/redfish/v1/")
		if err != nil {
			b.Fatalf("Request failed: %v", err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}
	}
}

func BenchmarkRedfishAuthentication(b *testing.B) {
	ts := setupTestServer(&testing.T{})
	defer ts.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("GET", ts.Server.URL+"/redfish/v1/Managers", nil)
		req.SetBasicAuth("admin", "admin")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			b.Fatalf("Request failed: %v", err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}
	}
}
