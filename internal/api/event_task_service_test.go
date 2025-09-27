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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceRootHasEventAndTaskLinks(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Login to get token
	loginBody, _ := json.Marshal(map[string]string{"UserName": "admin", "Password": "admin"})
	req := httptest.NewRequest(http.MethodPost, "/redfish/v1/SessionService/Sessions", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on login, got %d", rec.Code)
	}
	token := rec.Header().Get("X-Auth-Token")

	// Fetch service root (no auth required, but ensure headers set uniformly)
	req = httptest.NewRequest(http.MethodGet, "/redfish/v1/", nil)
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for service root, got %d", rec.Code)
	}
	var root map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("failed to parse service root: %v", err)
	}
	if _, ok := root["EventService"].(map[string]any); !ok {
		t.Fatalf("expected EventService link in service root")
	}
	if _, ok := root["TaskService"].(map[string]any); !ok {
		t.Fatalf("expected TaskService link in service root")
	}
}

func TestEventServiceStub(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// EventService root requires auth via top-level gate
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/EventService", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated EventService root, got %d", rec.Code)
	}

	// Login and fetch
	loginBody, _ := json.Marshal(map[string]string{"UserName": "admin", "Password": "admin"})
	req = httptest.NewRequest(http.MethodPost, "/redfish/v1/SessionService/Sessions", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on login, got %d", rec.Code)
	}
	token := rec.Header().Get("X-Auth-Token")

	req = httptest.NewRequest(http.MethodGet, "/redfish/v1/EventService", nil)
	req.Header.Set("X-Auth-Token", token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for EventService root, got %d", rec.Code)
	}
	var svc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &svc); err != nil {
		t.Fatalf("failed to parse EventService: %v", err)
	}
	if svc["ServiceEnabled"] != false {
		t.Fatalf("expected ServiceEnabled=false in EventService stub")
	}
}

func TestTaskServiceStub(t *testing.T) {
	handler, db := setupTestAPI(t)
	defer func() { _ = db.Close() }()

	// Unauthenticated should be 401
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/TaskService", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated TaskService root, got %d", rec.Code)
	}

	// Login
	loginBody, _ := json.Marshal(map[string]string{"UserName": "admin", "Password": "admin"})
	req = httptest.NewRequest(http.MethodPost, "/redfish/v1/SessionService/Sessions", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on login, got %d", rec.Code)
	}
	token := rec.Header().Get("X-Auth-Token")

	// Fetch TaskService root
	req = httptest.NewRequest(http.MethodGet, "/redfish/v1/TaskService", nil)
	req.Header.Set("X-Auth-Token", token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for TaskService root, got %d", rec.Code)
	}

	// Fetch Tasks collection
	req = httptest.NewRequest(http.MethodGet, "/redfish/v1/TaskService/Tasks", nil)
	req.Header.Set("X-Auth-Token", token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for TaskService Tasks collection, got %d", rec.Code)
	}
	var coll map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &coll); err != nil {
		t.Fatalf("failed to parse Tasks collection: %v", err)
	}
	if count, ok := coll["Members@odata.count"].(float64); !ok || int(count) != 0 {
		t.Fatalf("expected empty Tasks collection, got %v", coll["Members@odata.count"])
	}
}
