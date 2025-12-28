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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shoal/internal/auth"
	"shoal/internal/bmc"
	"shoal/internal/database"
	"shoal/pkg/models"
	"shoal/pkg/redfish"
)

// validMessageIDs contains the set of valid Base message registry IDs
var validMessageIDs = map[string]struct{}{
	"Base.1.0.GeneralError":            {},
	"Base.1.0.ResourceNotFound":        {},
	"Base.1.0.MethodNotAllowed":        {},
	"Base.1.0.Unauthorized":            {},
	"Base.1.0.InternalError":           {},
	"Base.1.0.InsufficientPrivilege":   {},
	"Base.1.0.MalformedJSON":           {},
	"Base.1.0.PropertyMissing":         {},
	"Base.1.0.PropertyValueNotInList":  {},
	"Base.1.0.ResourceCannotBeCreated": {},
	"Base.1.0.NotImplemented":          {},
}

// Common error messages
const (
	errorUsernameAlreadyExists = "Username already exists"
)

// Handler implements the Redfish API endpoints
type Handler struct {
	db                     *database.DB
	auth                   *auth.Authenticator
	bmcSvc                 *bmc.Service
	imageProxyURL          string // Base URL for image proxy (empty if disabled)
	cloudInitGeneratorFunc func(userData, metaData string) (isoID, token string, err error)
	ociConverterFunc       func(ctx context.Context, imageRef string) (imageID, token string, err error)
}

// ImageProxyConfig holds configuration for URL rewriting
type ImageProxyConfig struct {
	Enabled                bool
	BaseURL                string // e.g., "http://localhost:8082"
	CloudInitGeneratorFunc func(userData, metaData string) (isoID, token string, err error)
	OCIConverterFunc       func(ctx context.Context, imageRef string) (imageID, token string, err error)
}

// New creates a new API handler
func New(db *database.DB) http.Handler {
	return NewRouter(db)
}

// NewWithImageProxy creates a new API handler with image proxy configuration
func NewWithImageProxy(db *database.DB, proxyConfig *ImageProxyConfig) http.Handler {
	return NewRouterWithImageProxy(db, proxyConfig)
}

// handleRedfish routes Redfish API requests
func (h *Handler) handleRedfish(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/redfish")

	slog.Debug("Handling Redfish request", "method", r.Method, "path", path)

	// Handle service root
	if path == "/" || path == "" {
		h.handleServiceRoot(w, r)
		return
	}

	// Handle version endpoint
	if path == "/v1/" || path == "/v1" {
		h.handleServiceRoot(w, r)
		return
	}

	// SessionService endpoints (handle auth within the handler to allow unauthenticated POST)
	if strings.HasPrefix(path, "/v1/SessionService") {
		h.handleSessionService(w, r, path)
		return
	}

	// OEM ProvisioningConfiguration endpoints (no auth required for installer access during boot)
	// Path pattern: /v1/Systems/{system-id}/Oem/Shoal/ProvisioningConfiguration/{Kickstart|Preseed}
	if strings.Contains(path, "/Oem/Shoal/ProvisioningConfiguration/") {
		// Let the proxy handler deal with it (it will intercept before proxying)
		h.handleBMCProxy(w, r, path)
		return
	}

	// All other endpoints require authentication
	user, err := h.auth.AuthenticateRequest(r)
	if err != nil {
		h.writeErrorResponse(w, http.StatusUnauthorized, "Base.1.0.Unauthorized", "Authentication required")
		return
	}

	// Handle console endpoints (OEM extensions)
	if h.isConsoleRequest(path) {
		h.handleConsoleRequest(w, r, path, user)
		return
	}

	// Check if this is a BMC proxy request
	if h.isBMCProxyRequest(path) {
		h.handleBMCProxy(w, r, path)
		return
	}

	// Handle AggregationService endpoints
	if strings.HasPrefix(path, "/v1/AggregationService") {
		h.handleAggregationService(w, r, path, user)
		return
	}

	// Handle AccountService endpoints
	if strings.HasPrefix(path, "/v1/AccountService") {
		h.handleAccountService(w, r, path, user)
		return
	}

	// Handle EventService endpoints (stub)
	if strings.HasPrefix(path, "/v1/EventService") {
		h.handleEventService(w, r, path, user)
		return
	}

	// Handle TaskService endpoints (stub)
	if strings.HasPrefix(path, "/v1/TaskService") {
		h.handleTaskService(w, r, path, user)
		return
	}

	// Handle aggregator-specific endpoints
	h.handleAggregatorEndpoints(w, r, path, user)
}

// handleMetadata serves the OData $metadata CSDL. For Phase 1, return a minimal static shell.

// handleRegistriesCollection lists available message registries (minimal: Base)

// handleRegistryFile serves individual registry; for now, return a small Base stub.

// handleSchemaStoreRoot returns a placeholder SchemaStore collection

// handleSchemaFile placeholder for individual schema files

// computeETag returns a strong ETag value for the provided bytes (quoted per RFC 7232)
func computeETag(b []byte) string {
	return rfComputeETag(b)
}

func weakETag(parts ...string) string {
	return rfWeakETag(parts...)
}

func formatTimeForETag(t time.Time) string {
	return rfFormatTimeForETag(t)
}

// weakMatch compares If-None-Match header to a generated ETag, handling weak validators
func ifNoneMatchMatches(ifNoneMatch, etag string) bool {
	return rfIfNoneMatchMatches(ifNoneMatch, etag)
}

// sha256Sum returns hex-encoded SHA-256 sum of the input
func sha256Sum(b []byte) string {
	h := sha256.New()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// handleLogin creates a new session
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
		return
	}

	// Parse credentials from request body
	var loginReq struct {
		Username string `json:"UserName"`
		Password string `json:"Password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&loginReq); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.MalformedJSON", "Invalid JSON in request body")
		return
	}

	// Validate credentials
	user, err := h.auth.AuthenticateBasic(r.Context(), loginReq.Username, loginReq.Password)
	if err != nil {
		h.writeErrorResponse(w, http.StatusUnauthorized, "Base.1.0.Unauthorized", "Invalid credentials")
		return
	}

	// Create session
	session, err := h.auth.CreateSession(r.Context(), user.ID)
	if err != nil {
		slog.Error("Failed to create session", "error", err)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.InternalError", "Failed to create session")
		return
	}

	// Return session info
	sessionResp := map[string]interface{}{
		"@odata.context": "/redfish/v1/$metadata#Session.Session",
		"@odata.id":      fmt.Sprintf("/redfish/v1/SessionService/Sessions/%s", session.ID),
		"@odata.type":    "#Session.v1_0_0.Session",
		"Id":             session.ID,
		"Name":           "User Session",
		"UserName":       user.Username,
	}

	// Set session token header
	w.Header().Set("X-Auth-Token", session.Token)
	w.Header().Set("Location", fmt.Sprintf("/redfish/v1/SessionService/Sessions/%s", session.ID))
	h.writeJSONResponse(w, http.StatusCreated, sessionResp)
}

// handleBMCProxy proxies requests to managed BMCs

// isBMCProxyRequest checks if the request should be proxied to a BMC

// handleAggregatorEndpoints handles aggregator-specific endpoints
func (h *Handler) handleAggregatorEndpoints(w http.ResponseWriter, r *http.Request, path string, user *models.User) {
	if path == "/v1/Managers" || path == "/v1/Managers/" {
		h.handleManagersCollection(w, r)
		return
	}

	if path == "/v1/Systems" || path == "/v1/Systems/" {
		h.handleSystemsCollection(w, r)
		return
	}

	h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Resource not found")
}

// handleAccountService routes and implements AccountService endpoints

// handleGetAccountsCollection returns the Accounts collection

// handleCreateAccount creates a new user account

// handleGetAccount returns an individual account

// handlePatchAccount updates fields on an account

// handleDeleteAccount deletes a user account

// handleGetRolesCollection returns the Roles collection

// handleGetRole returns a specific role

// handleEventService provides a minimal EventService stub
func (h *Handler) handleEventService(w http.ResponseWriter, r *http.Request, path string, user *models.User) {
	subPath := strings.TrimPrefix(path, "/v1/EventService")

	// Root resource
	if subPath == "" || subPath == "/" {
		if r.Method == http.MethodOptions {
			h.writeAllow(w, http.MethodGet)
			return
		}
		if r.Method != http.MethodGet {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
			return
		}
		svc := redfish.EventService{
			ODataContext:   "/redfish/v1/$metadata#EventService.EventService",
			ODataID:        "/redfish/v1/EventService",
			ODataType:      "#EventService.v1_0_0.EventService",
			ID:             "EventService",
			Name:           "Event Service",
			ServiceEnabled: false,
		}
		h.writeJSONResponse(w, http.StatusOK, svc)
		return
	}

	h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Resource not found")
}

// handleTaskService provides a minimal TaskService stub
func (h *Handler) handleTaskService(w http.ResponseWriter, r *http.Request, path string, user *models.User) {
	subPath := strings.TrimPrefix(path, "/v1/TaskService")

	// Root resource
	if subPath == "" || subPath == "/" {
		if r.Method == http.MethodOptions {
			h.writeAllow(w, http.MethodGet)
			return
		}
		if r.Method != http.MethodGet {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
			return
		}
		svc := redfish.TaskService{
			ODataContext: "/redfish/v1/$metadata#TaskService.TaskService",
			ODataID:      "/redfish/v1/TaskService",
			ODataType:    "#TaskService.v1_0_0.TaskService",
			ID:           "TaskService",
			Name:         "Task Service",
			Tasks:        redfish.ODataIDRef{ODataID: "/redfish/v1/TaskService/Tasks"},
		}
		h.writeJSONResponse(w, http.StatusOK, svc)
		return
	}

	// Tasks collection
	if subPath == "/Tasks" || subPath == "/Tasks/" {
		if r.Method == http.MethodOptions {
			h.writeAllow(w, http.MethodGet)
			return
		}
		if r.Method != http.MethodGet {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "Method not allowed")
			return
		}
		coll := redfish.Collection{
			ODataContext: "/redfish/v1/$metadata#TaskCollection.TaskCollection",
			ODataID:      "/redfish/v1/TaskService/Tasks",
			ODataType:    "#TaskCollection.TaskCollection",
			Name:         "Task Collection",
			Members:      []redfish.ODataIDRef{},
			MembersCount: 0,
		}
		h.writeJSONResponse(w, http.StatusOK, coll)
		return
	}

	h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Resource not found")
}

// toRedfishAccount converts models.User to Redfish ManagerAccount
func toRedfishAccount(u *models.User) redfish.ManagerAccount {
	return redfish.ManagerAccount{
		ODataContext: "/redfish/v1/$metadata#ManagerAccount.ManagerAccount",
		ODataID:      fmt.Sprintf("/redfish/v1/AccountService/Accounts/%s", u.ID),
		ODataType:    "#ManagerAccount.v1_0_0.ManagerAccount",
		ID:           u.ID,
		Name:         "User Account",
		UserName:     u.Username,
		RoleID:       redfishRoleFromModels(u.Role),
		Enabled:      u.Enabled,
	}
}

func redfishRoleFromModels(role string) string {
	switch role {
	case models.RoleAdmin:
		return "Administrator"
	case models.RoleOperator:
		return "Operator"
	case models.RoleViewer:
		return "ReadOnly"
	default:
		return "ReadOnly"
	}
}

func modelsRoleFromRedfish(roleID string) (string, bool) {
	switch strings.ToLower(roleID) {
	case "administrator":
		return models.RoleAdmin, true
	case "operator":
		return models.RoleOperator, true
	case "readonly", "read-only", "read_only", "viewer":
		return models.RoleViewer, true
	default:
		return "", false
	}
}

// writeJSONResponse writes a JSON response
func (h *Handler) writeJSONResponse(w http.ResponseWriter, status int, data interface{}) {
	rfWriteJSONResponse(w, status, data)
}

// writeJSONResponseWithETag writes JSON while emitting an optional ETag header
func (h *Handler) writeJSONResponseWithETag(w http.ResponseWriter, status int, data interface{}, etag string) {
	rfWriteJSONResponseWithETag(w, nil, status, data, etag)
}

// writeAllow responds to an HTTP OPTIONS request by advertising allowed methods
func (h *Handler) writeAllow(w http.ResponseWriter, methods ...string) {
	rfWriteAllow(w, methods...)
}

// (removed unused handleSessions)

// handleManagedNodes handles BMC management operations
func (h *Handler) handleManagedNodes(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement BMC management endpoints
	h.writeErrorResponse(w, http.StatusNotImplemented, "Base.1.0.NotImplemented", "Managed nodes endpoint not yet implemented")
}

// handleAggregationService handles aggregation service endpoints

// handleConnectionMethodsCollection handles the ConnectionMethods collection

// handleConnectionMethod handles a specific ConnectionMethod resource

func accountETag(u *models.User) string {
	stamp := u.UpdatedAt
	if stamp.IsZero() {
		stamp = u.CreatedAt
	}
	return weakETag(
		"account",
		u.ID,
		formatTimeForETag(stamp),
		u.Username,
		u.Role,
		strconv.FormatBool(u.Enabled),
	)
}

func accountsCollectionETag(users []models.User) string {
	parts := []string{"accounts"}
	for _, u := range users {
		stamp := u.UpdatedAt
		if stamp.IsZero() {
			stamp = u.CreatedAt
		}
		parts = append(parts,
			u.ID,
			formatTimeForETag(stamp),
			u.Username,
			u.Role,
			strconv.FormatBool(u.Enabled),
		)
	}
	return weakETag(parts...)
}

func connectionMethodETag(m *models.ConnectionMethod) string {
	stamp := m.UpdatedAt
	if stamp.IsZero() {
		stamp = m.CreatedAt
	}
	return weakETag("connection-method", m.ID, formatTimeForETag(stamp), strconv.FormatBool(m.Enabled), m.Name)
}

func connectionMethodsCollectionETag(methods []models.ConnectionMethod) string {
	parts := []string{"connection-methods"}
	for _, m := range methods {
		stamp := m.UpdatedAt
		if stamp.IsZero() {
			stamp = m.CreatedAt
		}
		parts = append(parts, m.ID, formatTimeForETag(stamp))
	}
	return weakETag(parts...)
}

// writeErrorResponse writes a Redfish-compliant error response
func (h *Handler) writeErrorResponse(w http.ResponseWriter, status int, code, message string) {
	rfWriteErrorResponse(w, status, code, message)
}

// severityForStatus maps HTTP status to a Redfish severity string
func severityForStatus(status int) string {
	return rfSeverityForStatus(status)
}

// resolutionForMessageID returns a generic resolution for known Base messages
func resolutionForMessageID(msgID string) string {
	return rfResolutionForMessageID(msgID)
}
