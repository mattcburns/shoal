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

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBMCDetailsPageHasConsoleTab(t *testing.T) {
	ts := createTestSetup(t)
	defer ts.DB.Close()

	// Create test request for BMC details page
	req := httptest.NewRequest(http.MethodGet, "/bmcs/details?name=test-bmc", nil)
	ts.addAuth(req)

	rr := httptest.NewRecorder()
	ts.Handler.ServeHTTP(rr, req)

	// Check that the response contains the Console tab
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	// Check for Console tab button
	if !strings.Contains(body, `id="tab-console"`) {
		t.Error("Expected Console tab button to be present")
	}

	// Check for Console tab content
	if !strings.Contains(body, `id="tab-console-content"`) {
		t.Error("Expected Console tab content to be present")
	}

	// Check for xterm.js script inclusion
	if !strings.Contains(body, "xterm.js") {
		t.Error("Expected xterm.js to be loaded")
	}

	// Check for terminal container
	if !strings.Contains(body, `id="terminal-container"`) {
		t.Error("Expected terminal container to be present")
	}

	// Check for connect/disconnect buttons
	if !strings.Contains(body, `id="btn-console-connect"`) {
		t.Error("Expected console connect button to be present")
	}
	if !strings.Contains(body, `id="btn-console-disconnect"`) {
		t.Error("Expected console disconnect button to be present")
	}

	// Check for active sessions list
	if !strings.Contains(body, `id="console-sessions-list"`) {
		t.Error("Expected console sessions list to be present")
	}

	// Check for initConsoleTab function
	if !strings.Contains(body, "initConsoleTab") {
		t.Error("Expected initConsoleTab function to be present")
	}
}

func TestBMCDetailsPageConsoleTabInitialization(t *testing.T) {
	ts := createTestSetup(t)
	defer ts.DB.Close()

	req := httptest.NewRequest(http.MethodGet, "/bmcs/details?name=test-bmc", nil)
	ts.addAuth(req)

	rr := httptest.NewRecorder()
	ts.Handler.ServeHTTP(rr, req)

	body := rr.Body.String()

	// Check that initConsoleTab is called with the BMC name
	if !strings.Contains(body, "initConsoleTab(bmcName)") {
		t.Error("Expected initConsoleTab to be called during page load")
	}

	// Check for tab switching handlers
	if !strings.Contains(body, "showConsole") {
		t.Error("Expected showConsole function to be present")
	}
}
