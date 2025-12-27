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
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shoal/internal/assets"
	internalAuth "shoal/internal/auth"
	"shoal/internal/bmc"
	"shoal/internal/ctxkeys"
	"shoal/internal/database"
	"shoal/pkg/auth"
	"shoal/pkg/models"
)

// Handler handles the web interface
type Handler struct {
	db        *database.DB
	bmcSvc    *bmc.Service
	auth      *internalAuth.Authenticator
	templates *template.Template
}

const bootOrderAttr = "Boot.BootOrder"

// New creates a new web handler
func New(db *database.DB) http.Handler {
	h := &Handler{
		db:     db,
		bmcSvc: bmc.New(db),
		auth:   internalAuth.New(db),
	}

	// Load templates
	h.loadTemplates()

	mux := http.NewServeMux()

	// Static files - serve embedded files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(assets.GetStaticFS()))))

	// Public routes (no auth required)
	mux.HandleFunc("/login", h.handleLogin)
	mux.HandleFunc("/logout", h.handleLogout)

	// Protected routes - wrap with auth middleware
	mux.Handle("/", h.requireAuth(http.HandlerFunc(h.handleHome)))
	mux.Handle("/bmcs", h.requireAuth(http.HandlerFunc(h.handleBMCs)))
	mux.Handle("/bmcs/add", h.requireAuth(http.HandlerFunc(h.handleAddBMC)))
	mux.Handle("/bmcs/edit", h.requireAuth(http.HandlerFunc(h.handleEditBMC)))
	mux.Handle("/bmcs/delete", h.requireAuth(http.HandlerFunc(h.handleDeleteBMC)))
	mux.Handle("/bmcs/power", h.requireAuth(http.HandlerFunc(h.handlePowerControl)))
	mux.Handle("/bmcs/details", h.requireAuth(http.HandlerFunc(h.handleBMCDetails)))
	mux.Handle("/api/bmcs/test-connection", h.requireAuth(http.HandlerFunc(h.handleTestConnection)))
	mux.Handle("/api/bmcs/details", h.requireAuth(http.HandlerFunc(h.handleBMCDetailsAPI)))
	// Settings discovery: support both query form and RESTful path form
	mux.Handle("/api/bmcs/settings", h.requireAuth(http.HandlerFunc(h.handleBMCSettingsAPI)))
	mux.Handle("/api/bmcs/", h.requireAuth(http.HandlerFunc(h.handleBMCSettingsAPIRestful)))

	// User management routes (admin only)
	mux.Handle("/users", h.requireAdmin(http.HandlerFunc(h.handleUsers)))
	mux.Handle("/users/add", h.requireAdmin(http.HandlerFunc(h.handleAddUser)))
	mux.Handle("/users/edit", h.requireAdmin(http.HandlerFunc(h.handleEditUser)))
	mux.Handle("/users/delete", h.requireAdmin(http.HandlerFunc(h.handleDeleteUser)))
	mux.Handle("/profile", h.requireAuth(http.HandlerFunc(h.handleProfile)))
	mux.Handle("/profile/password", h.requireAuth(http.HandlerFunc(h.handleChangePassword)))

	// Profiles feature removed in Design 014

	return mux
}

// loadTemplates loads HTML templates
func (h *Handler) loadTemplates() {
	// Define base template
	baseTemplate := `<!DOCTYPE html>
<html>
<head>
    <title>{{.Title}} - Shoal Redfish Aggregator</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background-color: #f5f5f5; }
        .container { max-width: 1200px; margin: 0 auto; background-color: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .header { border-bottom: 2px solid #007acc; padding-bottom: 10px; margin-bottom: 20px; }
        .header h1 { color: #007acc; margin: 0; }
        .nav { margin: 20px 0; }
        .nav a { margin-right: 20px; color: #007acc; text-decoration: none; }
        .nav a:hover { text-decoration: underline; }
        .btn { padding: 8px 16px; margin: 4px; border: none; border-radius: 4px; cursor: pointer; text-decoration: none; display: inline-block; }
        .btn-primary { background-color: #007acc; color: white; }
        .btn-danger { background-color: #dc3545; color: white; }
        .btn-success { background-color: #28a745; color: white; }
        .btn-warning { background-color: #ffc107; color: black; }
        .table { width: 100%; border-collapse: collapse; margin: 20px 0; }
        .table th, .table td { border: 1px solid #ddd; padding: 12px; text-align: left; }
        .table th { background-color: #f8f9fa; }
        .form-group { margin-bottom: 15px; }
        .form-group label { display: block; margin-bottom: 5px; font-weight: bold; }
        .form-group input, .form-group textarea, .form-group select { width: 100%; padding: 8px; border: 1px solid #ddd; border-radius: 4px; }
        .alert { padding: 15px; margin-bottom: 20px; border: 1px solid transparent; border-radius: 4px; }
        .alert-success { color: #155724; background-color: #d4edda; border-color: #c3e6cb; }
        .alert-danger { color: #721c24; background-color: #f8d7da; border-color: #f5c6cb; }
        .status { padding: 4px 8px; border-radius: 4px; font-size: 12px; }
        .status-enabled { background-color: #d4edda; color: #155724; }
        .status-disabled { background-color: #f8d7da; color: #721c24; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Shoal Redfish Aggregator</h1>
        </div>
        <div class="nav">
            <a href="/">Dashboard</a>
			<a href="/bmcs">Manage BMCs</a>
			{{if .User}}
                {{if eq .User.Role "admin"}}
                <a href="/users">Manage Users</a>
                {{end}}
                <a href="/profile">Profile</a>
                <span style="float: right;">
                    Logged in as <strong>{{.User.Username}}</strong> ({{.UserRole}})
                    | <a href="/logout">Logout</a>
                </span>
            {{else}}
                <span style="float: right;"><a href="/login">Login</a></span>
            {{end}}
        </div>
        {{template "content" .}}
    </div>
</body>
</html>`

	h.templates = template.Must(template.New("base").Parse(baseTemplate))
}

// PageData represents data passed to templates
type PageData struct {
	Title    string
	Message  string
	Error    string
	User     *models.User
	UserRole string
	BMCs     []models.BMC
	BMC      *models.BMC
	Users    []models.User
	EditUser *models.User
}

// handleHome displays the dashboard
func (h *Handler) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	bmcs, err := h.db.GetBMCs(r.Context())
	if err != nil {
		slog.Error("Failed to get BMCs", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := PageData{
		Title: "Dashboard",
		BMCs:  bmcs,
	}
	user := getUserFromContext(r.Context())
	if user != nil {
		data.User = user
		data.UserRole = auth.GetRoleDisplayName(user.Role)
	}

	homeTemplate := `{{define "content"}}
<h2>Dashboard</h2>
<div class="alert alert-success">
    <strong>Welcome!</strong> This is the Shoal Redfish Aggregator web interface.
</div>

<h3>BMC Status Overview</h3>
<table class="table">
    <thead>
        <tr>
            <th>Name</th>
            <th>Address</th>
            <th>Status</th>
            <th>Last Seen</th>
            <th>Actions</th>
        </tr>
    </thead>
    <tbody>
        {{range .BMCs}}
        <tr>
            <td>{{.Name}}</td>
            <td>{{.Address}}</td>
            <td>
                {{if .Enabled}}
                <span class="status status-enabled">Enabled</span>
                {{else}}
                <span class="status status-disabled">Disabled</span>
                {{end}}
            </td>
            <td>
                {{if .LastSeen}}{{.LastSeen.Format "2006-01-02 15:04:05"}}{{else}}Never{{end}}
            </td>
            <td>
                <a href="/bmcs/details?name={{.Name}}" class="btn btn-primary" style="font-size: 12px;">Details</a>
            </td>
        </tr>
        {{else}}
        <tr>
            <td colspan="5">No BMCs configured. <a href="/bmcs/add">Add your first BMC</a></td>
        </tr>
        {{end}}
    </tbody>
</table>

<div>
	<a href="/bmcs" class="btn btn-primary">Manage BMCs</a>
</div>
{{end}}`

	tmpl := template.Must(h.templates.Clone())
	template.Must(tmpl.Parse(homeTemplate))

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		slog.Error("Failed to execute template", "error", err)
		http.Error(w, "Template Error", http.StatusInternalServerError)
	}
}

// handleBMCs displays and manages BMCs
func (h *Handler) handleBMCs(w http.ResponseWriter, r *http.Request) {
	bmcs, err := h.db.GetBMCs(r.Context())
	if err != nil {
		slog.Error("Failed to get BMCs", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := PageData{
		Title: "Manage BMCs",
		BMCs:  bmcs,
	}
	user := getUserFromContext(r.Context())
	if user != nil {
		data.User = user
		data.UserRole = auth.GetRoleDisplayName(user.Role)
	}

	// Check for messages in URL parameters
	if msg := r.URL.Query().Get("message"); msg != "" {
		data.Message = msg
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		data.Error = errMsg
	}

	bmcsTemplate := `{{define "content"}}
<h2>Manage BMCs</h2>

{{if .Message}}
<div class="alert alert-success">{{.Message}}</div>
{{end}}

{{if .Error}}
<div class="alert alert-danger">{{.Error}}</div>
{{end}}

<div style="margin-bottom: 20px;">
    <a href="/bmcs/add" class="btn btn-primary">Add New BMC</a>
</div>

<!-- Test result display area -->
<div id="test-result-global" style="margin-bottom: 10px;"></div>

<table class="table">
    <thead>
        <tr>
            <th>Name</th>
            <th>Address</th>
            <th>Username</th>
            <th>Description</th>
            <th>Status</th>
            <th>Last Seen</th>
            <th>Actions</th>
        </tr>
    </thead>
    <tbody>
        {{range .BMCs}}
        <tr id="bmc-row-{{.ID}}">
            <td>{{.Name}}</td>
            <td>{{.Address}}</td>
            <td>{{.Username}}</td>
            <td>{{.Description}}</td>
            <td>
                {{if .Enabled}}
                <span class="status status-enabled">Enabled</span>
                {{else}}
                <span class="status status-disabled">Disabled</span>
                {{end}}
                <span id="test-status-{{.ID}}" style="margin-left: 10px;"></span>
            </td>
            <td>
                {{if .LastSeen}}{{.LastSeen.Format "2006-01-02 15:04:05"}}{{else}}Never{{end}}
            </td>
            <td>
                <a href="/bmcs/details?name={{.Name}}" class="btn btn-primary" style="margin: 2px;">Details</a>
                <button onclick="testBMCConnection('{{.ID}}', '{{.Address}}', '{{.Name}}')" class="btn btn-success" style="margin: 2px;">Test</button>
                <a href="/bmcs/edit?id={{.ID}}" class="btn btn-primary" style="margin: 2px;">Edit</a>
                <a href="/bmcs/power?id={{.ID}}&action=On" class="btn btn-success" style="margin: 2px;">Power On</a>
                <a href="/bmcs/power?id={{.ID}}&action=ForceOff" class="btn btn-warning" style="margin: 2px;">Power Off</a>
                <a href="/bmcs/delete?id={{.ID}}" class="btn btn-danger" style="margin: 2px;" onclick="return confirm('Are you sure?')">Delete</a>
            </td>
        </tr>
        {{else}}
        <tr>
            <td colspan="7">No BMCs configured.</td>
        </tr>
        {{end}}
    </tbody>
</table>

<script>
function testBMCConnection(bmcId, address, name) {
    const testStatusSpan = document.getElementById('test-status-' + bmcId);
    const globalResultDiv = document.getElementById('test-result-global');
    const testButton = event.target;

    // Show loading state
    testButton.disabled = true;
    testButton.textContent = 'Testing...';
    testStatusSpan.innerHTML = '<span style="color: #666;">⏳ Testing...</span>';
    globalResultDiv.innerHTML = '';

    // Make AJAX request
    fetch('/api/bmcs/test-connection', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({ address: address })
    })
    .then(response => response.json())
    .then(data => {
        if (data.success) {
            testStatusSpan.innerHTML = '<span style="color: #28a745;">✓ OK</span>';
            globalResultDiv.innerHTML = '<div class="alert alert-success">Connection test successful for BMC "' + name + '": ' + data.message + '</div>';
        } else {
            testStatusSpan.innerHTML = '<span style="color: #dc3545;">✗ Failed</span>';
            globalResultDiv.innerHTML = '<div class="alert alert-danger">Connection test failed for BMC "' + name + '": ' + data.message + '</div>';
        }

        // Clear the inline status after 5 seconds
        setTimeout(() => {
            testStatusSpan.innerHTML = '';
        }, 5000);
    })
    .catch(error => {
        testStatusSpan.innerHTML = '<span style="color: #dc3545;">✗ Error</span>';
        globalResultDiv.innerHTML = '<div class="alert alert-danger">Test failed for BMC "' + name + '": ' + error.message + '</div>';
    })
    .finally(() => {
        testButton.disabled = false;
        testButton.textContent = 'Test';
    });
}
</script>
{{end}}`

	tmpl := template.Must(h.templates.Clone())
	template.Must(tmpl.Parse(bmcsTemplate))

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		slog.Error("Failed to execute template", "error", err)
		http.Error(w, "Template Error", http.StatusInternalServerError)
	}
}

// handleAddBMC handles adding a new BMC
func (h *Handler) handleAddBMC(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/bmcs?error=Invalid+form+data", http.StatusSeeOther)
			return
		}

		bmc := &models.BMC{
			Name:        strings.TrimSpace(r.FormValue("name")),
			Address:     strings.TrimSpace(r.FormValue("address")),
			Username:    strings.TrimSpace(r.FormValue("username")),
			Password:    r.FormValue("password"),
			Description: strings.TrimSpace(r.FormValue("description")),
			Enabled:     r.FormValue("enabled") == "on",
		}

		// Validate required fields
		if bmc.Name == "" || bmc.Address == "" || bmc.Username == "" || bmc.Password == "" {
			http.Redirect(w, r, "/bmcs/add?error=All+fields+are+required", http.StatusSeeOther)
			return
		}

		// Create BMC
		if err := h.db.CreateBMC(r.Context(), bmc); err != nil {
			slog.Error("Failed to create BMC", "error", err)
			http.Redirect(w, r, "/bmcs/add?error=Failed+to+create+BMC", http.StatusSeeOther)
			return
		}

		// Test connection if enabled
		if bmc.Enabled {
			if err := h.bmcSvc.TestConnection(r.Context(), bmc); err != nil {
				slog.Warn("BMC connection test failed", "bmc", bmc.Name, "error", err)
				http.Redirect(w, r, fmt.Sprintf("/bmcs?message=BMC+added+but+connection+test+failed:+%v", err), http.StatusSeeOther)
				return
			}
		}

		http.Redirect(w, r, "/bmcs?message=BMC+added+successfully", http.StatusSeeOther)
		return
	}

	// GET request - show form
	data := PageData{
		Title: "Add BMC",
	}
	user := getUserFromContext(r.Context())
	if user != nil {
		data.User = user
		data.UserRole = auth.GetRoleDisplayName(user.Role)
	}

	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		data.Error = errMsg
	}

	addTemplate := `{{define "content"}}
<h2>Add New BMC</h2>

{{if .Error}}
<div class="alert alert-danger">{{.Error}}</div>
{{end}}

<form method="POST">
    <div class="form-group">
        <label for="name">Name:</label>
        <input type="text" id="name" name="name" required>
    </div>

    <div class="form-group">
        <label for="address">Address:</label>
        <input type="text" id="address" name="address" placeholder="192.168.1.100" required>
        <button type="button" id="test-connection" class="btn btn-success" style="margin-top: 5px;" onclick="testConnection()">Test Connection</button>
        <div id="test-result" style="margin-top: 10px;"></div>
    </div>

    <div class="form-group">
        <label for="username">Username:</label>
        <input type="text" id="username" name="username" required>
    </div>

    <div class="form-group">
        <label for="password">Password:</label>
        <input type="password" id="password" name="password" required>
    </div>

    <div class="form-group">
        <label for="description">Description:</label>
        <textarea id="description" name="description" rows="3"></textarea>
    </div>

    <div class="form-group">
        <label>
            <input type="checkbox" name="enabled" checked> Enabled
        </label>
    </div>

    <div>
        <button type="submit" class="btn btn-primary">Add BMC</button>
        <a href="/bmcs" class="btn btn-danger">Cancel</a>
    </div>
</form>

<script>
function testConnection() {
    const addressInput = document.getElementById('address');
    const testButton = document.getElementById('test-connection');
    const resultDiv = document.getElementById('test-result');

    const address = addressInput.value.trim();
    if (!address) {
        resultDiv.innerHTML = '<div class="alert alert-danger">Please enter a BMC address first</div>';
        return;
    }

    // Show loading state
    testButton.disabled = true;
    testButton.textContent = 'Testing...';
    resultDiv.innerHTML = '<div style="color: #666;">Testing connection to ' + address + '...</div>';

    // Make AJAX request
    fetch('/api/bmcs/test-connection', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({ address: address })
    })
    .then(response => response.json())
    .then(data => {
        if (data.success) {
            resultDiv.innerHTML = '<div class="alert alert-success">' + data.message + '</div>';
        } else {
            resultDiv.innerHTML = '<div class="alert alert-danger">' + data.message + '</div>';
        }
    })
    .catch(error => {
        resultDiv.innerHTML = '<div class="alert alert-danger">Test failed: ' + error.message + '</div>';
    })
    .finally(() => {
        testButton.disabled = false;
        testButton.textContent = 'Test Connection';
    });
}
</script>
{{end}}`

	tmpl := template.Must(h.templates.Clone())
	template.Must(tmpl.Parse(addTemplate))

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		slog.Error("Failed to execute template", "error", err)
		http.Error(w, "Template Error", http.StatusInternalServerError)
	}
}

// handleEditBMC handles editing a BMC
func (h *Handler) handleEditBMC(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Redirect(w, r, "/bmcs?error=Missing+BMC+ID", http.StatusSeeOther)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/bmcs?error=Invalid+BMC+ID", http.StatusSeeOther)
		return
	}

	if r.Method == "POST" {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, fmt.Sprintf("/bmcs/edit?id=%d&error=Invalid+form+data", id), http.StatusSeeOther)
			return
		}

		bmc := &models.BMC{
			ID:          id,
			Name:        strings.TrimSpace(r.FormValue("name")),
			Address:     strings.TrimSpace(r.FormValue("address")),
			Username:    strings.TrimSpace(r.FormValue("username")),
			Password:    r.FormValue("password"),
			Description: strings.TrimSpace(r.FormValue("description")),
			Enabled:     r.FormValue("enabled") == "on",
		}

		// Validate required fields
		if bmc.Name == "" || bmc.Address == "" || bmc.Username == "" || bmc.Password == "" {
			http.Redirect(w, r, fmt.Sprintf("/bmcs/edit?id=%d&error=All+fields+are+required", id), http.StatusSeeOther)
			return
		}

		// Update BMC
		if err := h.db.UpdateBMC(r.Context(), bmc); err != nil {
			slog.Error("Failed to update BMC", "id", id, "error", err)
			http.Redirect(w, r, fmt.Sprintf("/bmcs/edit?id=%d&error=Failed+to+update+BMC", id), http.StatusSeeOther)
			return
		}

		// Test connection if enabled
		if bmc.Enabled {
			if err := h.bmcSvc.TestConnection(r.Context(), bmc); err != nil {
				slog.Warn("BMC connection test failed", "bmc", bmc.Name, "error", err)
				http.Redirect(w, r, fmt.Sprintf("/bmcs?message=BMC+updated+but+connection+test+failed:+%v", err), http.StatusSeeOther)
				return
			}
		}

		http.Redirect(w, r, "/bmcs?message=BMC+updated+successfully", http.StatusSeeOther)
		return
	}

	// GET request - get existing BMC and show form
	bmc, err := h.db.GetBMC(r.Context(), id)
	if err != nil {
		slog.Error("Failed to get BMC", "id", id, "error", err)
		http.Redirect(w, r, "/bmcs?error=Failed+to+get+BMC", http.StatusSeeOther)
		return
	}
	if bmc == nil {
		http.Redirect(w, r, "/bmcs?error=BMC+not+found", http.StatusSeeOther)
		return
	}

	data := PageData{
		Title: "Edit BMC",
		BMC:   bmc,
	}
	user := getUserFromContext(r.Context())
	if user != nil {
		data.User = user
		data.UserRole = auth.GetRoleDisplayName(user.Role)
	}

	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		data.Error = errMsg
	}

	editTemplate := `{{define "content"}}
<h2>Edit BMC</h2>

{{if .Error}}
<div class="alert alert-danger">{{.Error}}</div>
{{end}}

<form method="POST">
    <div class="form-group">
        <label for="name">Name:</label>
        <input type="text" id="name" name="name" value="{{.BMC.Name}}" required>
    </div>

    <div class="form-group">
        <label for="address">Address:</label>
        <input type="text" id="address" name="address" value="{{.BMC.Address}}" placeholder="192.168.1.100" required>
        <button type="button" id="test-connection" class="btn btn-success" style="margin-top: 5px;" onclick="testConnection()">Test Connection</button>
        <div id="test-result" style="margin-top: 10px;"></div>
    </div>

    <div class="form-group">
        <label for="username">Username:</label>
        <input type="text" id="username" name="username" value="{{.BMC.Username}}" required>
    </div>

    <div class="form-group">
        <label for="password">Password:</label>
        <input type="password" id="password" name="password" value="{{.BMC.Password}}" required>
    </div>

    <div class="form-group">
        <label for="description">Description:</label>
        <textarea id="description" name="description" rows="3">{{.BMC.Description}}</textarea>
    </div>

    <div class="form-group">
        <label>
            <input type="checkbox" name="enabled" {{if .BMC.Enabled}}checked{{end}}> Enabled
        </label>
    </div>

    <div>
        <button type="submit" class="btn btn-primary">Update BMC</button>
        <a href="/bmcs" class="btn btn-danger">Cancel</a>
    </div>
</form>

<script>
function testConnection() {
    const addressInput = document.getElementById('address');
    const testButton = document.getElementById('test-connection');
    const resultDiv = document.getElementById('test-result');

    const address = addressInput.value.trim();
    if (!address) {
        resultDiv.innerHTML = '<div class="alert alert-danger">Please enter a BMC address first</div>';
        return;
    }

    // Show loading state
    testButton.disabled = true;
    testButton.textContent = 'Testing...';
    resultDiv.innerHTML = '<div style="color: #666;">Testing connection to ' + address + '...</div>';

    // Make AJAX request
    fetch('/api/bmcs/test-connection', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({ address: address })
    })
    .then(response => response.json())
    .then(data => {
        if (data.success) {
            resultDiv.innerHTML = '<div class="alert alert-success">' + data.message + '</div>';
        } else {
            resultDiv.innerHTML = '<div class="alert alert-danger">' + data.message + '</div>';
        }
    })
    .catch(error => {
        resultDiv.innerHTML = '<div class="alert alert-danger">Test failed: ' + error.message + '</div>';
    })
    .finally(() => {
        testButton.disabled = false;
        testButton.textContent = 'Test Connection';
    });
}
</script>
{{end}}`

	tmpl := template.Must(h.templates.Clone())
	template.Must(tmpl.Parse(editTemplate))

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		slog.Error("Failed to execute template", "error", err)
		http.Error(w, "Template Error", http.StatusInternalServerError)
	}
}

// handleDeleteBMC handles deleting a BMC
func (h *Handler) handleDeleteBMC(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Redirect(w, r, "/bmcs?error=Missing+BMC+ID", http.StatusSeeOther)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/bmcs?error=Invalid+BMC+ID", http.StatusSeeOther)
		return
	}

	if err := h.db.DeleteBMC(r.Context(), id); err != nil {
		slog.Error("Failed to delete BMC", "id", id, "error", err)
		http.Redirect(w, r, "/bmcs?error=Failed+to+delete+BMC", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/bmcs?message=BMC+deleted+successfully", http.StatusSeeOther)
}

// handlePowerControl handles power control actions
func (h *Handler) handlePowerControl(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	action := r.URL.Query().Get("action")

	if idStr == "" || action == "" {
		http.Redirect(w, r, "/bmcs?error=Missing+parameters", http.StatusSeeOther)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/bmcs?error=Invalid+BMC+ID", http.StatusSeeOther)
		return
	}

	// Get BMC info
	bmc, err := h.db.GetBMC(r.Context(), id)
	if err != nil || bmc == nil {
		http.Redirect(w, r, "/bmcs?error=BMC+not+found", http.StatusSeeOther)
		return
	}

	// Execute power control
	powerAction := models.PowerAction(action)
	if err := h.bmcSvc.PowerControl(r.Context(), bmc.Name, powerAction); err != nil {
		slog.Error("Power control failed", "bmc", bmc.Name, "action", action, "error", err)
		http.Redirect(w, r, fmt.Sprintf("/bmcs?error=Power+control+failed:+%v", err), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/bmcs?message=Power+action+%s+executed+successfully+on+%s", action, bmc.Name), http.StatusSeeOther)
}

// TestConnectionRequest represents a connection test request
type TestConnectionRequest struct {
	Address string `json:"address"`
}

// TestConnectionResponse represents a connection test response
type TestConnectionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// handleTestConnection handles AJAX requests to test BMC connectivity
func (h *Handler) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	// Only allow POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set response headers for JSON
	w.Header().Set("Content-Type", "application/json")

	// Parse JSON request
	var req TestConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response := TestConnectionResponse{
			Success: false,
			Message: "Invalid request format",
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Validate address
	address := strings.TrimSpace(req.Address)
	if address == "" {
		response := TestConnectionResponse{
			Success: false,
			Message: "BMC address is required",
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Test the connection
	err := h.bmcSvc.TestUnauthenticatedConnection(ctx, address)
	if err != nil {
		response := TestConnectionResponse{
			Success: false,
			Message: fmt.Sprintf("Connection failed: %v", err),
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Success response
	response := TestConnectionResponse{
		Success: true,
		Message: "Connection successful! BMC is reachable and responding with Redfish API",
	}
	_ = json.NewEncoder(w).Encode(response)
}

// handleBMCDetails displays detailed information about a specific BMC
func (h *Handler) handleBMCDetails(w http.ResponseWriter, r *http.Request) {
	bmcName := r.URL.Query().Get("name")
	if bmcName == "" {
		http.Redirect(w, r, "/bmcs?error=Missing+BMC+name", http.StatusSeeOther)
		return
	}

	data := PageData{
		// Title should be just the BMC name; the template adds the 'BMC Details - ' prefix
		Title: bmcName,
	}
	user := getUserFromContext(r.Context())
	if user != nil {
		data.User = user
		data.UserRole = auth.GetRoleDisplayName(user.Role)
	}

	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		data.Error = errMsg
	}

	detailsTemplate := `{{define "content"}}
<h2>BMC Details - {{.Title}}</h2>

<div style="margin-bottom: 20px;">
    <a href="/bmcs" class="btn btn-primary">← Back to BMC List</a>
</div>

{{if .Error}}
<div class="alert alert-danger">{{.Error}}</div>
{{end}}

<div id="loading-indicator" style="text-align: center; padding: 20px;">
    <p>Loading BMC details...</p>
</div>

<div id="bmc-details" style="display: none;">
	<!-- Tabs -->
	<div style="margin-bottom: 20px;">
		<button id="tab-overview" class="btn btn-primary">Overview</button>
		<button id="tab-settings" class="btn">Settings</button>
		<button id="tab-virtualmedia" class="btn">Virtual Media</button>
	</div>

	<div id="tab-overview-content">
		<!-- System Information -->
		<div class="details-section">
			<h3>System Information</h3>
			<div id="system-info" class="info-grid"></div>
		</div>

		<!-- Network Interfaces -->
		<div class="details-section">
			<h3>Network Interfaces</h3>
			<div id="network-interfaces"></div>
		</div>

		<!-- Storage Devices -->
		<div class="details-section">
			<h3>Storage Devices</h3>
			<div id="storage-devices"></div>
		</div>

		<!-- System Event Log -->
		<div class="details-section">
			<h3>System Event Log</h3>
			<div id="sel-entries"></div>
		</div>
	</div>

	<div id="tab-settings-content" style="display:none;">
		<div class="details-section">
			<h3>Boot Order</h3>
			<div id="boot-order-section">
				<div id="boot-order-status" style="margin-bottom:8px; color:#333;"></div>
				<div id="boot-order-controls" style="display:none; gap:12px; align-items:flex-start; flex-wrap:wrap;">
					<div style="flex:1 1 280px;">
						<label>Current Order</label>
						<ul id="boot-order-list" aria-label="Boot order list" role="listbox" style="list-style:none; padding:0; margin:0; max-width:420px;"></ul>
					</div>
					<div style="flex:1 1 280px;">
						<label>Allowable Values</label>
						<div id="boot-order-allow" style="font-size:12px; color:#555; max-width:420px;"></div>
					</div>
				</div>
				<div style="margin-top:10px; display:flex; gap:8px;">
					<button id="boot-order-reset" class="btn">Reset to Allowable Order</button>
				</div>
			</div>
		</div>

		<div class="details-section">
			<h3>Settings</h3>
			<form id="settings-filters" style="display:flex; gap:8px; align-items:flex-end; flex-wrap:wrap;">
				<div>
					<label>Resource</label>
					<input type="text" id="set-resource" placeholder="Bios|NetworkProtocol" />
				</div>
				<div>
					<label>Search</label>
					<input type="text" id="set-search" placeholder="attribute/name/path" />
				</div>
				<div>
					<label>OEM</label>
					<select id="set-oem">
						<option value="">Any</option>
						<option value="true">OEM only</option>
						<option value="false">Non-OEM</option>
					</select>
				</div>
				<div>
					<label>Page size</label>
					<input type="number" id="set-page-size" value="25" min="1" max="200" style="width:100px;" />
				</div>
				<button type="submit" class="btn btn-primary">Apply</button>
			</form>

			<div style="margin-top:10px;">
				<div id="set-summary" style="margin-bottom:8px;"></div>
				<table class="table" id="settings-table">
					<thead>
						<tr><th>Resource</th><th>Attribute</th><th>Value</th></tr>
					</thead>
					<tbody></tbody>
				</table>
				<div style="display:flex; gap:8px; align-items:center;">
					<button id="set-prev" class="btn">Prev</button>
					<span id="set-page-info"></span>
					<button id="set-next" class="btn">Next</button>
				</div>
			</div>
		</div>

		</div>
	</div>

	<div id="tab-virtualmedia-content" style="display:none;">
		<div class="details-section">
			<h3>Virtual Media Slots</h3>
			<div id="vmedia-status" style="margin-bottom:8px; color:#333;">Loading virtual media...</div>
			<div id="vmedia-slots"></div>
		</div>

		<div class="details-section">
			<h3>Attached Media</h3>
			<div id="vmedia-attached-list"></div>
		</div>
	</div>

</div>

<!-- Virtual Media Attach Dialog -->
<div id="vmedia-attach-dialog" style="display:none; position:fixed; top:0; left:0; right:0; bottom:0; background:rgba(0,0,0,0.5); z-index:1000;">
	<div style="position:absolute; top:50%; left:50%; transform:translate(-50%,-50%); background:white; padding:30px; border-radius:8px; max-width:600px; width:90%; box-shadow:0 4px 12px rgba(0,0,0,0.3);">
		<h3 style="margin-top:0;">Attach Virtual Media</h3>
		<div id="vmedia-attach-error" class="alert alert-danger" style="display:none; margin-bottom:15px;"></div>
		<form id="vmedia-attach-form">
			<input type="hidden" id="vmedia-attach-slot-id" />
			<div class="form-group">
				<label for="vmedia-image-url">Image URL *</label>
				<input type="text" id="vmedia-image-url" class="form-control" placeholder="http://server.example.com/images/ubuntu-22.04.iso" required />
				<small style="color:#666;">Supported protocols: HTTP, HTTPS, NFS, CIFS</small>
			</div>
			<div class="form-group">
				<label>
					<input type="checkbox" id="vmedia-write-protected" checked /> Write Protected
				</label>
			</div>
			<div class="form-group">
				<label>
					<input type="checkbox" id="vmedia-inserted" checked /> Inserted
				</label>
			</div>
			<div style="margin-top:20px; display:flex; gap:10px; justify-content:flex-end;">
				<button type="button" class="btn btn-danger" onclick="closeAttachDialog()">Cancel</button>
				<button type="submit" class="btn btn-primary">Attach Media</button>
			</div>
		</form>
	</div>
</div>

<div id="error-display" class="alert alert-danger" style="display: none;"></div>

<style>
.details-section {
    margin-bottom: 30px;
    border: 1px solid #ddd;
    border-radius: 5px;
    padding: 20px;
    background-color: #f9f9f9;
}

.details-section h3 {
    margin-top: 0;
    color: #007acc;
    border-bottom: 2px solid #007acc;
    padding-bottom: 10px;
}

.info-grid {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 10px;
    max-width: 600px;
}

.info-label {
    font-weight: bold;
    padding: 5px;
}

.info-value {
    padding: 5px;
}

.interface-card, .device-card, .log-entry {
    border: 1px solid #ccc;
    border-radius: 3px;
    padding: 15px;
    margin-bottom: 15px;
    background-color: white;
}

.interface-card h4, .device-card h4 {
    margin: 0 0 10px 0;
    color: #333;
}

.log-entry {
    border-left: 4px solid #007acc;
}

.log-entry.severity-critical {
    border-left-color: #dc3545;
}

.log-entry.severity-warning {
    border-left-color: #ffc107;
}

.log-entry.severity-ok {
    border-left-color: #28a745;
}

.log-meta {
    font-size: 12px;
    color: #666;
    margin-bottom: 5px;
}

.capacity {
    font-weight: bold;
}
</style>

<script>
// Helper function to escape HTML (used across multiple tabs)
function escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function displaySystemInfo(systemInfo) {
    const container = document.getElementById('system-info');
    if (!systemInfo) {
        container.innerHTML = '<p>System information not available</p>';
        return;
    }

    let html = '';
    if (systemInfo.manufacturer) {
        html += '<div class="info-label">Manufacturer:</div><div class="info-value">' + systemInfo.manufacturer + '</div>';
    }
    if (systemInfo.model) {
        html += '<div class="info-label">Model:</div><div class="info-value">' + systemInfo.model + '</div>';
    }
    if (systemInfo.serial_number) {
        html += '<div class="info-label">Serial Number:</div><div class="info-value">' + systemInfo.serial_number + '</div>';
    }
    if (systemInfo.sku) {
        html += '<div class="info-label">SKU:</div><div class="info-value">' + systemInfo.sku + '</div>';
    }
    if (systemInfo.power_state) {
        html += '<div class="info-label">Power State:</div><div class="info-value">' + systemInfo.power_state + '</div>';
    }

    container.innerHTML = html || '<p>No system information available</p>';
}

function displayNetworkInterfaces(nics) {
    const container = document.getElementById('network-interfaces');
    if (!nics || nics.length === 0) {
        container.innerHTML = '<p>No network interfaces found</p>';
        return;
    }

    let html = '';
    nics.forEach(function(nic) {
        html += '<div class="interface-card">';
        html += '<h4>' + (nic.name || 'Network Interface') + '</h4>';
        if (nic.description) {
            html += '<p><strong>Description:</strong> ' + nic.description + '</p>';
        }
        if (nic.mac_address) {
            html += '<p><strong>MAC Address:</strong> ' + nic.mac_address + '</p>';
        }
        if (nic.ip_addresses && nic.ip_addresses.length > 0) {
            html += '<p><strong>IP Addresses:</strong> ' + nic.ip_addresses.join(', ') + '</p>';
        }
        html += '</div>';
    });

    container.innerHTML = html;
}

function displayStorageDevices(devices) {
    const container = document.getElementById('storage-devices');
    if (!devices || devices.length === 0) {
        container.innerHTML = '<p>No storage devices found</p>';
        return;
    }

    let html = '';
    devices.forEach(function(device) {
        html += '<div class="device-card">';
        html += '<h4>' + (device.name || 'Storage Device') + '</h4>';
        if (device.model) {
            html += '<p><strong>Model:</strong> ' + device.model + '</p>';
        }
        if (device.serial_number) {
            html += '<p><strong>Serial Number:</strong> ' + device.serial_number + '</p>';
        }
        if (device.capacity_bytes) {
            html += '<p><strong>Capacity:</strong> <span class="capacity">' + formatBytes(device.capacity_bytes) + '</span></p>';
        }
        if (device.media_type) {
            html += '<p><strong>Media Type:</strong> ' + device.media_type + '</p>';
        }
        if (device.status) {
            html += '<p><strong>Status:</strong> ' + device.status + '</p>';
        }
        html += '</div>';
    });

    container.innerHTML = html;
}

function displaySELEntries(entries) {
    const container = document.getElementById('sel-entries');
    if (!entries || entries.length === 0) {
        container.innerHTML = '<p>No SEL entries found</p>';
        return;
    }

    let html = '';
    // Sort entries by created date (newest first)
    const sortedEntries = entries.sort(function(a, b) {
        return new Date(b.created || 0) - new Date(a.created || 0);
    });

    sortedEntries.forEach(function(entry) {
        const severityClass = entry.severity ? 'severity-' + entry.severity.toLowerCase() : '';
        html += '<div class="log-entry ' + severityClass + '">';

        html += '<div class="log-meta">';
        if (entry.created) {
            html += '<span><strong>Date:</strong> ' + entry.created + '</span> | ';
        }
        if (entry.severity) {
            html += '<span><strong>Severity:</strong> ' + entry.severity + '</span> | ';
        }
        if (entry.entry_type) {
            html += '<span><strong>Type:</strong> ' + entry.entry_type + '</span>';
        }
        html += '</div>';

        if (entry.message) {
            html += '<div><strong>Message:</strong> ' + entry.message + '</div>';
        }
        html += '</div>';
    });

    container.innerHTML = html;
}

function loadBMCDetails() {
    const bmcName = new URLSearchParams(window.location.search).get('name');
    if (!bmcName) {
        document.getElementById('error-display').textContent = 'BMC name is required';
        document.getElementById('error-display').style.display = 'block';
        document.getElementById('loading-indicator').style.display = 'none';
        return;
    }

    fetch('/api/bmcs/details?name=' + encodeURIComponent(bmcName))
        .then(function(response) {
            if (!response.ok) {
                throw new Error('Failed to fetch BMC details: ' + response.statusText);
            }
            return response.json();
        })
        .then(function(data) {
            document.getElementById('loading-indicator').style.display = 'none';
            document.getElementById('bmc-details').style.display = 'block';

            displaySystemInfo(data.system_info);
            displayNetworkInterfaces(data.network_interfaces);
            displayStorageDevices(data.storage_devices);
            displaySELEntries(data.sel_entries);
			initSettingsTab(bmcName);
			initVirtualMediaTab(bmcName);
        })
        .catch(function(error) {
            document.getElementById('loading-indicator').style.display = 'none';
            document.getElementById('error-display').textContent = error.message;
            document.getElementById('error-display').style.display = 'block';
        });
}

// Load details when page loads
loadBMCDetails();

function initSettingsTab(bmcName) {
	const tabOverviewBtn = document.getElementById('tab-overview');
	const tabSettingsBtn = document.getElementById('tab-settings');
	const tabVirtualMediaBtn = document.getElementById('tab-virtualmedia');
	const overview = document.getElementById('tab-overview-content');
	const settings = document.getElementById('tab-settings-content');
	const virtualmedia = document.getElementById('tab-virtualmedia-content');

	function showOverview() {
		tabOverviewBtn.classList.add('btn-primary');
		tabSettingsBtn.classList.remove('btn-primary');
		tabVirtualMediaBtn.classList.remove('btn-primary');
		overview.style.display = '';
		settings.style.display = 'none';
		virtualmedia.style.display = 'none';
	}
	function showSettings() {
		tabSettingsBtn.classList.add('btn-primary');
		tabOverviewBtn.classList.remove('btn-primary');
		tabVirtualMediaBtn.classList.remove('btn-primary');
		overview.style.display = 'none';
		settings.style.display = '';
		virtualmedia.style.display = 'none';
		fetchSettings();
		loadBootOrderWidget();
	}
	function showVirtualMedia() {
		tabVirtualMediaBtn.classList.add('btn-primary');
		tabOverviewBtn.classList.remove('btn-primary');
		tabSettingsBtn.classList.remove('btn-primary');
		overview.style.display = 'none';
		settings.style.display = 'none';
		virtualmedia.style.display = '';
	}
	tabOverviewBtn.addEventListener('click', showOverview);
	tabSettingsBtn.addEventListener('click', showSettings);
	tabVirtualMediaBtn.addEventListener('click', showVirtualMedia);

	let page = 1;
	const tbody = document.querySelector('#settings-table tbody');
	const summary = document.getElementById('set-summary');
	const pageInfo = document.getElementById('set-page-info');
	const filtersForm = document.getElementById('settings-filters');
	const btnPrev = document.getElementById('set-prev');
	const btnNext = document.getElementById('set-next');

	filtersForm.addEventListener('submit', function(e){ e.preventDefault(); page = 1; fetchSettings(); });
	btnPrev.addEventListener('click', function(){ if (page>1) { page--; fetchSettings(); }});
	btnNext.addEventListener('click', function(){ page++; fetchSettings(); });

	async function fetchSettings() {
		const resource = document.getElementById('set-resource').value.trim();
		const search = document.getElementById('set-search').value.trim();
		const oem = document.getElementById('set-oem').value;
		const pageSize = parseInt(document.getElementById('set-page-size').value || '25', 10);
		const params = new URLSearchParams();
		if (resource) params.set('resource', resource);
		if (search) params.set('search', search);
		if (oem) params.set('oem', oem);
		if (page) params.set('page', String(page));
		if (pageSize) params.set('page_size', String(pageSize));
		const url = '/api/bmcs/' + encodeURIComponent(bmcName) + '/settings?' + params.toString();
		tbody.innerHTML = '<tr><td colspan="3">Loading...</td></tr>';
		try {
			const res = await fetch(url);
				if (!res.ok) {
					tbody.innerHTML = '<tr><td colspan="3">Error ' + res.status + '</td></tr>';
				return;
			}
			const data = await res.json();
			const list = data.descriptors || [];
			const total = data.total || list.length;
			const pageSizeResp = data.page_size || pageSize;
			const pageResp = data.page || page;
			summary.textContent = 'Total ' + total + ' settings';
			pageInfo.textContent = 'Page ' + pageResp + (pageSizeResp ? (' of ~' + Math.ceil(total / pageSizeResp)) : '');
			btnPrev.disabled = (pageResp <= 1);
			btnNext.disabled = (pageSizeResp ? (pageResp * pageSizeResp >= total) : list.length === 0);
				if (!Array.isArray(list) || list.length === 0) {
					tbody.innerHTML = '<tr><td colspan="3">No settings found</td></tr>';
					return;
				}
					tbody.innerHTML = list.map(function(d){
						const cur = (d.current_value == null) ? '' : JSON.stringify(d.current_value);
						return '<tr>' +
							'<td>' + (d.resource_path || '') + '</td>' +
							'<td>' + (d.attribute || '') + '</td>' +
							'<td>' + cur + '</td>' +
							'</tr>';
					}).join('');

		} catch (e) {
			tbody.innerHTML = '<tr><td colspan="3">Error loading settings</td></tr>';
		}
	}

	// Helper function to create edit controls based on setting type
	function createEditControl(descriptor, editId, valueId) {
		const currentValue = descriptor.current_value;
		let input = '';

		if (descriptor.enum_values && descriptor.enum_values.length > 0) {
			// Dropdown for enum values
			input = '<select id="' + valueId + '" class="form-control">';
			descriptor.enum_values.forEach(function(value) {
				const selected = (value === currentValue) ? 'selected' : '';
				input += '<option value="' + escapeHtml(value) + '" ' + selected + '>' + escapeHtml(value) + '</option>';
			});
			input += '</select>';
		} else if (descriptor.type === 'boolean') {
			// Checkbox for boolean values
			const checked = currentValue === true ? 'checked' : '';
			input = '<input type="checkbox" id="' + valueId + '" ' + checked + ' />';
		} else if (descriptor.type === 'number' || descriptor.type === 'integer') {
			// Number input with min/max constraints
			let attrs = 'type="number" id="' + valueId + '" class="form-control" value="' + (currentValue || 0) + '"';
			if (descriptor.min !== null && descriptor.min !== undefined) {
				attrs += ' min="' + descriptor.min + '"';
			}
			if (descriptor.max !== null && descriptor.max !== undefined) {
				attrs += ' max="' + descriptor.max + '"';
			}
			input = '<input ' + attrs + ' />';
		} else {
			// Text input for strings and other types
			input = '<input type="text" id="' + valueId + '" class="form-control" value="' + escapeHtml(currentValue || '') + '" />';
		}

		return input +
			'<div style="margin-top:4px;">' +
				'<button class="btn btn-sm btn-primary setting-save-btn" data-id="' + descriptor.id + '" data-value-id="' + valueId + '">Save</button> ' +
				'<button class="btn btn-sm setting-cancel-btn" data-id="' + descriptor.id + '">Cancel</button>' +
			'</div>';
	}

	// Function to start editing a setting
	function startEditingSetting(id, rowId) {
		// Hide current value and show edit controls
		const currentSpan = document.getElementById('current-' + id);
		const editDiv = document.getElementById('edit-' + id);
		const editBtn = document.querySelector('[data-id="' + id + '"]');

		if (currentSpan) currentSpan.style.display = 'none';
		if (editDiv) editDiv.style.display = 'block';
		if (editBtn) editBtn.style.display = 'none';

		// Add event listeners for save/cancel buttons
		const saveBtn = document.querySelector('.setting-save-btn[data-id="' + id + '"]');
		const cancelBtn = document.querySelector('.setting-cancel-btn[data-id="' + id + '"]');

		if (saveBtn) {
			saveBtn.addEventListener('click', function() {
				saveSetting(id, this.dataset.valueId);
			});
		}

		if (cancelBtn) {
			cancelBtn.addEventListener('click', function() {
				cancelEditingSetting(id);
			});
		}
	}

	// Function to cancel editing
	function cancelEditingSetting(id) {
		const currentSpan = document.getElementById('current-' + id);
		const editDiv = document.getElementById('edit-' + id);
		const editBtn = document.querySelector('.setting-edit-btn[data-id="' + id + '"]');

		if (currentSpan) currentSpan.style.display = 'inline';
		if (editDiv) editDiv.style.display = 'none';
		if (editBtn) editBtn.style.display = 'inline';
	}

	// Function to save a setting
	async function saveSetting(id, valueId) {
		const input = document.getElementById(valueId);
		if (!input) return;

		let newValue;
		if (input.type === 'checkbox') {
			newValue = input.checked;
		} else if (input.type === 'number') {
			newValue = parseFloat(input.value);
		} else {
			newValue = input.value;
		}

		// Show loading state
		const saveBtn = document.querySelector('.setting-save-btn[data-id="' + id + '"]');
		const originalText = saveBtn.textContent;
		saveBtn.textContent = 'Saving...';
		saveBtn.disabled = true;

		try {
			const response = await fetch('/api/bmcs/' + encodeURIComponent(bmcName) + '/settings/' + encodeURIComponent(id), {
				method: 'PATCH',
				headers: {
					'Content-Type': 'application/json'
				},
				body: JSON.stringify({ value: newValue })
			});

			if (response.ok) {
				// Update the current value display
				const currentSpan = document.getElementById('current-' + id);
				if (currentSpan) {
					currentSpan.textContent = JSON.stringify(newValue);
				}

				// Cancel edit mode
				cancelEditingSetting(id);

				// Show success message
				showSettingMessage('Setting updated successfully', 'success');
			} else {
				const errorData = await response.json();
				showSettingMessage('Failed to update setting: ' + (errorData.error || 'Unknown error'), 'error');
			}
		} catch (error) {
			showSettingMessage('Network error: ' + error.message, 'error');
		} finally {
			// Restore button state
			saveBtn.textContent = originalText;
			saveBtn.disabled = false;
		}
	}

	// Function to show setting update messages
	function showSettingMessage(message, type) {
		const messageDiv = document.getElementById('setting-message') || createMessageDiv();
		messageDiv.textContent = message;
		messageDiv.className = 'alert alert-' + (type === 'success' ? 'success' : 'danger');
		messageDiv.style.display = 'block';

		// Auto-hide after 3 seconds
		setTimeout(() => {
			messageDiv.style.display = 'none';
		}, 3000);
	}

	// Function to create message div if it doesn't exist
	function createMessageDiv() {
		const messageDiv = document.createElement('div');
		messageDiv.id = 'setting-message';
		messageDiv.style.display = 'none';
		messageDiv.style.marginTop = '8px';

		// Insert after the summary div
		const summary = document.getElementById('set-summary');
		summary.parentNode.insertBefore(messageDiv, summary.nextSibling);

		return messageDiv;
	}

	// Boot Order widget
	function loadBootOrderWidget() {
		const status = document.getElementById('boot-order-status');
		const controls = document.getElementById('boot-order-controls');
		const list = document.getElementById('boot-order-list');
		const allowDiv = document.getElementById('boot-order-allow');
		const btnReset = document.getElementById('boot-order-reset');
		// Create Apply button
		let btnApply = document.getElementById('boot-order-apply');
		if (!btnApply) {
			btnApply = document.createElement('button');
			btnApply.id = 'boot-order-apply';
			btnApply.className = 'btn btn-primary';
			btnApply.textContent = 'Apply Boot Order';
			btnApply.style.marginLeft = '8px';
			btnReset && btnReset.parentElement && btnReset.parentElement.appendChild(btnApply);
		}
		// Add a subtle tip once
		if (status && !status.dataset.tip) {
			status.dataset.tip = '1';
			const hint = document.createElement('div');
			hint.textContent = 'Tip: drag items or use ↑/↓ to reorder';
			hint.style.fontSize = '12px';
			hint.style.color = '#666';
			status.parentElement && status.parentElement.insertBefore(hint, status.nextSibling);
		}

		if (!status || status.dataset.init === '1') return;
		status.dataset.init = '1';

		let descriptor = null;
		let allowable = [];

		function setStatus(msg){ status.textContent = msg || ''; }
		let dragFromIndex = null;
		function renderList(items){
			list.innerHTML = '';
			(items || []).forEach((val, idx) => {
				const li = document.createElement('li');
				li.style.display = 'flex';
				li.style.alignItems = 'center';
				li.style.gap = '8px';
				li.style.padding = '6px 4px';
				li.style.border = '1px solid #ddd';
				li.style.borderRadius = '4px';
				li.style.margin = '4px 0';
				li.draggable = true;
				li.setAttribute('role', 'option');
				li.setAttribute('aria-grabbed', 'false');
				const name = document.createElement('span');
				name.className = 'boot-option-name';
				name.textContent = String(val);
				name.style.flex = '1 1 auto';
				const handle = document.createElement('span');
				handle.textContent = '⋮⋮';
				handle.title = 'Drag to reorder';
				handle.style.cursor = 'move';
				handle.style.userSelect = 'none';
				const up = document.createElement('button'); up.type = 'button'; up.className='btn'; up.textContent='↑';
				const down = document.createElement('button'); down.type = 'button'; down.className='btn'; down.textContent='↓';
				up.addEventListener('click', () => moveItem(idx, -1));
				down.addEventListener('click', () => moveItem(idx, +1));
				li.addEventListener('dragstart', (e) => {
					li.classList.add('dragging');
					li.setAttribute('aria-grabbed', 'true');
					dragFromIndex = idx;
					if (e.dataTransfer) {
						e.dataTransfer.effectAllowed = 'move';
						e.dataTransfer.setData('text/plain', String(idx));
					}
				});
				li.addEventListener('dragend', () => {
					li.classList.remove('dragging');
					li.setAttribute('aria-grabbed', 'false');
					dragFromIndex = null;
				});
				li.addEventListener('dragover', (e) => {
					e.preventDefault();
					if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
					li.style.background = '#f5f5f5';
				});
				li.addEventListener('dragleave', () => { li.style.background = ''; });
				li.addEventListener('drop', (e) => {
					e.preventDefault();
					li.style.background = '';
					let from = dragFromIndex;
					try { const dt = e.dataTransfer && e.dataTransfer.getData('text/plain'); if (dt) from = parseInt(dt, 10); } catch(_){ /* ignore */ }
					const to = idx;
					if (from == null || from === to) return;
					const cur = currentValues();
					const moved = cur.splice(from, 1)[0];
					cur.splice(to, 0, moved);
					renderList(cur);
				});
				li.appendChild(handle); li.appendChild(up); li.appendChild(down); li.appendChild(name);
				list.appendChild(li);
			});
		}
		function currentValues(){
			return Array.from(list.querySelectorAll('li span.boot-option-name')).map(s => s.textContent);
		}
		function moveItem(index, delta){
			const items = currentValues();
			const ni = index + delta;
			if (ni < 0 || ni >= items.length) return;
			const tmp = items[index]; items[index] = items[ni]; items[ni] = tmp;
			renderList(items);
		}
		function renderAllow(){
			if (!allowable || !allowable.length) { allowDiv.textContent = 'No allowable values reported'; return; }
			allowDiv.textContent = allowable.join(', ');
		}

		btnReset && btnReset.addEventListener('click', () => {
			if (allowable && allowable.length) renderList(allowable);
		});

		// No profile integration; apply handled via settings table

		(async function fetchDescriptor(){
			setStatus('Loading Boot Order…');
			controls.style.display = 'none';
			try {
				const res = await fetch('/api/bmcs/' + encodeURIComponent(bmcName) + '/settings?search=' + encodeURIComponent('Boot.BootOrder'));
				if (!res.ok) { setStatus('Boot Order not available'); return; }
				const data = await res.json();
				let d = null;
				const list = data.descriptors || [];
				for (let i=0; i<list.length; i++) {
					const it = list[i];
					if ((it.attribute||'').toLowerCase() === 'boot.bootorder') { d = it; break; }
				}
				if (!d && list.length) d = list.find(x => (x.display_name||'').toLowerCase().includes('boot order')) || null;
				if (!d) { setStatus('Boot Order not found'); return; }
				descriptor = d;
				allowable = Array.isArray(d.enum_values) ? d.enum_values.slice() : [];
				let cur = [];
				if (Array.isArray(d.current_value)) {
					cur = d.current_value.map(String);
				} else if (allowable.length) {
					cur = allowable.slice();
				}
				renderAllow();
				renderList(cur);
				controls.style.display = 'flex';
				setStatus('');
			} catch(e) {
				setStatus('Failed to load Boot Order');
			}
		})();

		// Apply handler: PATCH Boot.BootOrder via settings endpoint
		btnApply && btnApply.addEventListener('click', async () => {
			if (!descriptor || !descriptor.id) { setStatus('Descriptor not loaded'); return; }
			const values = currentValues();
			btnApply.disabled = true; const prev = btnApply.textContent; btnApply.textContent = 'Applying…';
			try {
				const resp = await fetch('/api/bmcs/' + encodeURIComponent(bmcName) + '/settings/' + encodeURIComponent(descriptor.id), {
					method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ value: values })
				});
				if (resp.ok) {
					setStatus('Boot Order applied successfully');
				} else {
					const err = await resp.json().catch(() => ({}));
					setStatus('Failed to apply Boot Order: ' + (err.error || resp.statusText));
				}
			} catch(e) {
				setStatus('Network error applying Boot Order');
			} finally {
				btnApply.disabled = false; btnApply.textContent = prev;
			}
		});
	}
    // Profiles UI removed
}

// Virtual Media Tab Implementation
function initVirtualMediaTab(bmcName) {
	let virtualMediaSlots = [];
	let currentManagerId = null;

	// Load virtual media slots from API
	async function loadVirtualMediaSlots() {
		const statusDiv = document.getElementById('vmedia-status');
		const slotsDiv = document.getElementById('vmedia-slots');
		const attachedDiv = document.getElementById('vmedia-attached-list');

		try {
			statusDiv.textContent = 'Loading virtual media slots...';
			
			// First, we need to get the manager ID from the connection methods
			// For simplicity, we'll use the BMC name as the manager ID
			currentManagerId = bmcName;
			
			const response = await fetch('/redfish/v1/Managers/' + encodeURIComponent(bmcName) + '/VirtualMedia');
			if (!response.ok) {
				throw new Error('Failed to fetch virtual media: ' + response.statusText);
			}
			
			const collection = await response.json();
			
			if (!collection.Members || collection.Members.length === 0) {
				statusDiv.textContent = 'No virtual media slots available';
				slotsDiv.innerHTML = '<p>This BMC does not support virtual media.</p>';
				attachedDiv.innerHTML = '<p>No media attached.</p>';
				return;
			}
			
			// Fetch details for each slot
			virtualMediaSlots = [];
			const slotPromises = collection.Members.map(member => 
				fetch(member['@odata.id']).then(r => r.json())
			);
			
			virtualMediaSlots = await Promise.all(slotPromises);
			
			statusDiv.textContent = 'Found ' + virtualMediaSlots.length + ' virtual media slot(s)';
			renderVirtualMediaSlots();
			renderAttachedMedia();
			
		} catch (error) {
			statusDiv.textContent = 'Error loading virtual media';
			slotsDiv.innerHTML = '<div class="alert alert-danger">Error: ' + error.message + '</div>';
		}
	}

	// Render virtual media slots
	function renderVirtualMediaSlots() {
		const slotsDiv = document.getElementById('vmedia-slots');
		
		let html = '<div style="display:grid; gap:15px;">';
		
		virtualMediaSlots.forEach(slot => {
			const slotId = slot.Id;
			const isInserted = slot.Inserted || false;
			const imageName = slot.ImageName || 'None';
			const imageUrl = slot.Image || '';
			const mediaTypes = (slot.MediaTypes || []).join(', ') || 'Unknown';
			const writeProtected = slot.WriteProtected ? 'Yes' : 'No';
			
			html += '<div class="interface-card">';
			html += '<h4>' + escapeHtml(slotId);
			if (isInserted) {
				html += ' <span style="color:#28a745; font-size:14px;">● Inserted</span>';
			} else {
				html += ' <span style="color:#666; font-size:14px;">○ Empty</span>';
			}
			html += '</h4>';
			
			html += '<p><strong>Media Types:</strong> ' + escapeHtml(mediaTypes) + '</p>';
			
			if (isInserted) {
				html += '<p><strong>Image:</strong> ' + escapeHtml(imageName) + '</p>';
				if (imageUrl) {
					html += '<p><strong>URL:</strong> <span style="font-size:12px; word-break:break-all;">' + escapeHtml(imageUrl) + '</span></p>';
				}
				html += '<p><strong>Write Protected:</strong> ' + writeProtected + '</p>';
				html += '<button class="btn btn-warning" onclick="ejectMedia(\'' + escapeHtml(slotId) + '\')">Eject Media</button>';
			} else {
				html += '<button class="btn btn-primary" onclick="openAttachDialog(\'' + escapeHtml(slotId) + '\')">Attach Media</button>';
			}
			
			html += '</div>';
		});
		
		html += '</div>';
		slotsDiv.innerHTML = html;
	}

	// Render attached media list
	function renderAttachedMedia() {
		const attachedDiv = document.getElementById('vmedia-attached-list');
		
		const attachedSlots = virtualMediaSlots.filter(slot => slot.Inserted);
		
		if (attachedSlots.length === 0) {
			attachedDiv.innerHTML = '<p>No media currently attached.</p>';
			return;
		}
		
		let html = '<table class="table">';
		html += '<thead><tr><th>Slot</th><th>Image Name</th><th>URL</th><th>Write Protected</th><th>Actions</th></tr></thead>';
		html += '<tbody>';
		
		attachedSlots.forEach(slot => {
			html += '<tr>';
			html += '<td>' + escapeHtml(slot.Id) + '</td>';
			html += '<td>' + escapeHtml(slot.ImageName || '') + '</td>';
			html += '<td style="word-break:break-all; max-width:300px;">' + escapeHtml(slot.Image || '') + '</td>';
			html += '<td>' + (slot.WriteProtected ? 'Yes' : 'No') + '</td>';
			html += '<td><button class="btn btn-warning btn-sm" onclick="ejectMedia(\'' + escapeHtml(slot.Id) + '\')">Eject</button></td>';
			html += '</tr>';
		});
		
		html += '</tbody></table>';
		attachedDiv.innerHTML = html;
	}

	// Attach media dialog handlers
	window.openAttachDialog = function(slotId) {
		document.getElementById('vmedia-attach-slot-id').value = slotId;
		document.getElementById('vmedia-image-url').value = '';
		document.getElementById('vmedia-write-protected').checked = true;
		document.getElementById('vmedia-inserted').checked = true;
		document.getElementById('vmedia-attach-error').style.display = 'none';
		document.getElementById('vmedia-attach-dialog').style.display = 'block';
	};

	window.closeAttachDialog = function() {
		document.getElementById('vmedia-attach-dialog').style.display = 'none';
	};

	// Attach media form submission
	document.getElementById('vmedia-attach-form').addEventListener('submit', async function(e) {
		e.preventDefault();
		
		const slotId = document.getElementById('vmedia-attach-slot-id').value;
		const imageUrl = document.getElementById('vmedia-image-url').value.trim();
		const writeProtected = document.getElementById('vmedia-write-protected').checked;
		const inserted = document.getElementById('vmedia-inserted').checked;
		const errorDiv = document.getElementById('vmedia-attach-error');
		
		// Frontend validation
		if (!imageUrl) {
			errorDiv.textContent = 'Image URL is required';
			errorDiv.style.display = 'block';
			return;
		}
		
		// Validate URL format
		try {
			const url = new URL(imageUrl);
			const protocol = url.protocol.toLowerCase();
			if (!['http:', 'https:', 'nfs:', 'cifs:'].includes(protocol)) {
				errorDiv.textContent = 'Invalid protocol. Supported: HTTP, HTTPS, NFS, CIFS';
				errorDiv.style.display = 'block';
				return;
			}
		} catch (err) {
			errorDiv.textContent = 'Invalid URL format';
			errorDiv.style.display = 'block';
			return;
		}
		
		errorDiv.style.display = 'none';
		
		// Disable form
		const submitBtn = e.target.querySelector('button[type="submit"]');
		const originalText = submitBtn.textContent;
		submitBtn.disabled = true;
		submitBtn.textContent = 'Attaching...';
		
		try {
			const response = await fetch(
				'/redfish/v1/Managers/' + encodeURIComponent(currentManagerId) + 
				'/VirtualMedia/' + encodeURIComponent(slotId) + 
				'/Actions/VirtualMedia.InsertMedia',
				{
					method: 'POST',
					headers: {
						'Content-Type': 'application/json'
					},
					body: JSON.stringify({
						Image: imageUrl,
						Inserted: inserted,
						WriteProtected: writeProtected
					})
				}
			);
			
			if (!response.ok) {
				const errorData = await response.json().catch(() => ({ error: { message: 'Unknown error' } }));
				throw new Error(errorData.error?.message || 'Failed to attach media');
			}
			
			// Success - close dialog and reload
			closeAttachDialog();
			await loadVirtualMediaSlots();
			
		} catch (error) {
			errorDiv.textContent = 'Error: ' + error.message;
			errorDiv.style.display = 'block';
		} finally {
			submitBtn.disabled = false;
			submitBtn.textContent = originalText;
		}
	});

	// Eject media handler
	window.ejectMedia = async function(slotId) {
		if (!confirm('Are you sure you want to eject media from slot "' + slotId + '"?')) {
			return;
		}
		
		try {
			const response = await fetch(
				'/redfish/v1/Managers/' + encodeURIComponent(currentManagerId) + 
				'/VirtualMedia/' + encodeURIComponent(slotId) + 
				'/Actions/VirtualMedia.EjectMedia',
				{
					method: 'POST',
					headers: {
						'Content-Type': 'application/json'
					},
					body: JSON.stringify({})
				}
			);
			
			if (!response.ok) {
				const errorData = await response.json().catch(() => ({ error: { message: 'Unknown error' } }));
				throw new Error(errorData.error?.message || 'Failed to eject media');
			}
			
			// Success - reload
			await loadVirtualMediaSlots();
			
		} catch (error) {
			alert('Error ejecting media: ' + error.message);
		}
	};

	// Initial load when tab is first accessed
	const tabVirtualMediaBtn = document.getElementById('tab-virtualmedia');
	if (tabVirtualMediaBtn) {
		tabVirtualMediaBtn.addEventListener('click', function() {
			if (virtualMediaSlots.length === 0) {
				loadVirtualMediaSlots();
			}
		});
	}
}

</script>
{{end}}`

	tmpl := template.Must(h.templates.Clone())
	template.Must(tmpl.Parse(detailsTemplate))

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		slog.Error("Failed to execute template", "error", err)
		http.Error(w, "Template Error", http.StatusInternalServerError)
	}
}

// handleBMCDetailsAPI handles AJAX requests for BMC detailed information
func (h *Handler) handleBMCDetailsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set response headers for JSON
	w.Header().Set("Content-Type", "application/json")

	bmcName := r.URL.Query().Get("name")
	if bmcName == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "BMC name is required"})
		return
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Get detailed BMC status
	details, err := h.bmcSvc.GetDetailedBMCStatus(ctx, bmcName)
	if err != nil {
		slog.Error("Failed to get BMC details", "bmc", bmcName, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get BMC details: %v", err)})
		return
	}

	// Return detailed status
	if err := json.NewEncoder(w).Encode(details); err != nil {
		slog.Error("Failed to encode BMC details response", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// handleBMCSettingsAPI handles discovery of configurable settings for a BMC
func (h *Handler) handleBMCSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	bmcName := r.URL.Query().Get("name")
	if bmcName == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "BMC name is required"})
		return
	}

	q := r.URL.Query()
	resource := q.Get("resource")
	search := strings.TrimSpace(q.Get("search"))
	oemParam := strings.TrimSpace(q.Get("oem"))
	refresh := strings.EqualFold(strings.TrimSpace(q.Get("refresh")), "true")
	page, pageSize := 1, 0
	if p := strings.TrimSpace(q.Get("page")); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if ps := strings.TrimSpace(q.Get("page_size")); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 {
			pageSize = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if refresh {
		ctx = context.WithValue(ctx, ctxkeys.Refresh, true)
	}

	// If refresh=true, enforce operator/admin RBAC
	if refresh {
		if u := getUserFromContext(r.Context()); !auth.IsOperator(u) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "refresh requires operator privileges"})
			return
		}
	}

	descriptors, err := h.bmcSvc.DiscoverSettings(ctx, bmcName, resource)
	if err != nil {
		slog.Error("Settings discovery failed", "bmc", bmcName, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to discover settings: %v", err)})
		return
	}

	// Apply filters: OEM and free-text search over attribute/display/description/resource path
	filtered := make([]models.SettingDescriptor, 0, len(descriptors))
	wantOEM := -1 // -1 any, 0 false, 1 true
	if oemParam != "" {
		switch strings.ToLower(oemParam) {
		case "1", "true", "yes":
			wantOEM = 1
		case "0", "false", "no":
			wantOEM = 0
		}
	}
	s := strings.ToLower(search)
	for _, d := range descriptors {
		if wantOEM == 1 && !d.OEM {
			continue
		}
		if wantOEM == 0 && d.OEM {
			continue
		}
		if s != "" {
			hay := strings.ToLower(d.Attribute + " " + d.DisplayName + " " + d.Description + " " + d.ResourcePath + " " + d.OEMVendor)
			if !strings.Contains(hay, s) {
				continue
			}
		}
		filtered = append(filtered, d)
	}

	total := len(filtered)
	// Pagination
	if pageSize > 0 {
		start := (page - 1) * pageSize
		if start < 0 {
			start = 0
		}
		end := start + pageSize
		if start >= total {
			filtered = []models.SettingDescriptor{}
		} else {
			if end > total {
				end = total
			}
			filtered = filtered[start:end]
		}
	}

	resp := models.SettingsResponse{
		BMCName:     bmcName,
		Resource:    resource,
		Descriptors: filtered,
		Page:        page,
		PageSize:    pageSize,
		Total:       total,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("Failed to encode settings response", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// handleBMCSettingsAPIRestful handles /api/bmcs/{name}/settings style routes
func (h *Handler) handleBMCSettingsAPIRestful(w http.ResponseWriter, r *http.Request) {
	// Handle both list and detail: /api/bmcs/{name}/settings[/{id}]
	// Path format parts: ["", "api", "bmcs", "{name}", "settings", "{id}"?]
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	// Ensure prefix
	if !strings.HasPrefix(path, "/api/bmcs/") {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "bmcs" || parts[3] != "settings" {
		// Not our route; fall back to next handlers by returning 404
		http.NotFound(w, r)
		return
	}

	bmcName := parts[2]
	// Detail if id present
	if len(parts) == 5 {
		id := parts[4]
		w.Header().Set("Content-Type", "application/json")
		if bmcName == "" || id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "BMC name and id are required"})
			return
		}

		if r.Method == http.MethodPatch {
			// Handle setting update
			h.handleUpdateSetting(w, r, bmcName, id)
			return
		}

		// Handle GET request for setting detail
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		// Try DB first
		desc, err := h.db.GetSettingDescriptor(ctx, bmcName, id)
		if err != nil {
			slog.Error("Failed to fetch setting descriptor", "bmc", bmcName, "id", id, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to fetch descriptor: %v", err)})
			return
		}
		if desc == nil {
			// Trigger discovery to populate, then retry DB
			if _, err := h.bmcSvc.DiscoverSettings(ctx, bmcName, ""); err != nil {
				slog.Warn("Discovery failed while resolving descriptor", "bmc", bmcName, "error", err)
			}
			desc, _ = h.db.GetSettingDescriptor(ctx, bmcName, id)
		}

		if desc == nil {
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(desc); err != nil {
			slog.Error("Failed to encode descriptor response", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// List path
	// Optional resource filter via query
	q := r.URL.Query()
	resource := q.Get("resource")
	search := strings.TrimSpace(q.Get("search"))
	oemParam := strings.TrimSpace(q.Get("oem"))
	refresh := strings.EqualFold(strings.TrimSpace(q.Get("refresh")), "true")
	page, pageSize := 1, 0
	if p := strings.TrimSpace(q.Get("page")); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if ps := strings.TrimSpace(q.Get("page_size")); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 {
			pageSize = n
		}
	}

	w.Header().Set("Content-Type", "application/json")

	if bmcName == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "BMC name is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if refresh {
		if u := getUserFromContext(r.Context()); !auth.IsOperator(u) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "refresh requires operator privileges"})
			return
		}
	}

	descriptors, err := h.bmcSvc.DiscoverSettings(ctx, bmcName, resource)
	if err != nil {
		slog.Error("Settings discovery failed", "bmc", bmcName, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to discover settings: %v", err)})
		return
	}

	// Apply filters and pagination like query handler
	filtered := make([]models.SettingDescriptor, 0, len(descriptors))
	wantOEM := -1
	if oemParam != "" {
		switch strings.ToLower(oemParam) {
		case "1", "true", "yes":
			wantOEM = 1
		case "0", "false", "no":
			wantOEM = 0
		}
	}
	s := strings.ToLower(search)
	for _, d := range descriptors {
		if wantOEM == 1 && !d.OEM {
			continue
		}
		if wantOEM == 0 && d.OEM {
			continue
		}
		if s != "" {
			hay := strings.ToLower(d.Attribute + " " + d.DisplayName + " " + d.Description + " " + d.ResourcePath + " " + d.OEMVendor)
			if !strings.Contains(hay, s) {
				continue
			}
		}
		filtered = append(filtered, d)
	}
	total := len(filtered)
	if pageSize > 0 {
		start := (page - 1) * pageSize
		if start < 0 {
			start = 0
		}
		end := start + pageSize
		if start >= total {
			filtered = []models.SettingDescriptor{}
		} else {
			if end > total {
				end = total
			}
			filtered = filtered[start:end]
		}
	}

	resp := models.SettingsResponse{
		BMCName:     bmcName,
		Resource:    resource,
		Descriptors: filtered,
		Page:        page,
		PageSize:    pageSize,
		Total:       total,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("Failed to encode settings response", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// handleUpdateSetting handles PATCH requests to update individual BMC settings
func (h *Handler) handleUpdateSetting(w http.ResponseWriter, r *http.Request, bmcName, descriptorID string) {
	// Check permissions - require operator or admin role for BMC setting updates
	if u := getUserFromContext(r.Context()); !auth.IsOperator(u) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "operator privileges required to update BMC settings"})
		return
	}

	// Parse the request body to get the new value
	var updateReq struct {
		Value interface{} `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updateReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Design 015: Only allow Boot.BootOrder to be updated; other settings are read-only
	desc, err := h.db.GetSettingDescriptor(ctx, bmcName, descriptorID)
	if err != nil || desc == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "setting descriptor not found"})
		return
	}
	if !strings.EqualFold(desc.Attribute, bootOrderAttr) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Only Boot.BootOrder settings can be updated; all other settings are read-only"})
		return
	}

	// Update the setting via the BMC service
	if err := h.bmcSvc.UpdateSetting(ctx, bmcName, descriptorID, updateReq.Value); err != nil {
		slog.Error("Failed to update BMC setting", "bmc", bmcName, "descriptor", descriptorID, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to update setting: %v", err)})
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Setting updated successfully",
		"bmc":     bmcName,
		"setting": descriptorID,
	})

	slog.Info("BMC setting updated via UI", "bmc", bmcName, "setting", descriptorID, "user", getUserFromContext(r.Context()).Username)
}

// Profiles API removed in Design 014

// Authentication middleware

// requireAuth is a middleware that ensures the user is authenticated
func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var user *models.User
		// Try to get user from session cookie first, as this is the primary method for the web UI
		if cookie, err := r.Cookie("session_token"); err == nil {
			user, _ = h.auth.AuthenticateToken(r.Context(), cookie.Value)
		}

		// If no user from cookie, try other auth methods (like basic auth for API calls)
		if user == nil {
			var authErr error
			user, authErr = h.auth.AuthenticateRequest(r)
			if authErr != nil {
				// For web UI, redirect to login
				redirectURL := "/login"
				if r.Method == "GET" && r.URL.Path != "/" {
					redirectURL += "?redirect=" + url.QueryEscape(r.URL.String())
				}
				http.Redirect(w, r, redirectURL, http.StatusSeeOther)
				return
			}
		}

		// Store user in context for subsequent handlers
		ctx := context.WithValue(r.Context(), ctxkeys.User, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin is a middleware that ensures the user is an administrator
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return h.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := getUserFromContext(r.Context())
		if !auth.IsAdmin(user) {
			http.Error(w, "Access denied. Admin privileges required.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// requireOperator middleware ensures user has operator role or higher

// getUserFromContext retrieves the authenticated user from the request context
func getUserFromContext(ctx context.Context) *models.User {
	if user, ok := ctx.Value(ctxkeys.User).(*models.User); ok {
		return user
	}
	return nil
}
