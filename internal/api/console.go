/*
Shoal is a Redfish aggregator service.
Copyright (C) 2025  Matthew Burns

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"shoal/internal/bmc"
	"shoal/internal/ctxkeys"
	"shoal/pkg/auth"
	"shoal/pkg/models"
)

// WebSocket upgrader with secure origin checking
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// TODO: Security - Implement proper origin validation before production deployment
		// Currently allowing all origins for development/testing.
		// In production, should validate origin against configured allowed origins
		// to prevent CSRF attacks.
		origin := r.Header.Get("Origin")
		if origin == "" {
			// No origin header - allow for non-browser clients
			return true
		}
		// TODO: Validate origin against allowed list
		return true
	},
	// Enable compression for better performance
	EnableCompression: true,
}

// handleConnectSerialConsole handles POST requests to create a serial console session
func (h *Handler) handleConnectSerialConsole(w http.ResponseWriter, r *http.Request, managerID string) {
	// Check permissions - require operator or admin role
	user := getUserFromContext(r.Context())
	if !auth.IsOperator(user) {
		h.writeErrorResponse(w, http.StatusForbidden, "Base.1.0.GeneralError", "operator privileges required for console access")
		return
	}

	// Parse request body
	var req struct {
		ConnectType string `json:"ConnectType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.GeneralError", "invalid request body")
		return
	}

	// Validate ConnectType
	if req.ConnectType != "Oem" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.GeneralError", "only ConnectType 'Oem' is supported")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Get connection method for this manager ID
	// managerID could be a BMC name or connection method ID
	cm, err := h.db.GetConnectionMethod(ctx, managerID)
	if err != nil || cm == nil {
		// Try to find by name in the BMC table (fallback for backward compatibility)
		bmcs, err := h.db.GetBMCs(ctx)
		if err != nil {
			h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "manager not found")
			return
		}
		for _, bmc := range bmcs {
			if bmc.Name == managerID {
				// For now, we don't support BMCs directly - need connection methods
				// This will be enhanced in future versions
				h.writeErrorResponse(w, http.StatusNotImplemented, "Base.1.0.GeneralError", "console access requires connection method")
				return
			}
		}
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "manager not found")
		return
	}

	// Check console capability and max sessions
	capability, err := h.db.GetConsoleCapability(ctx, cm.ID, "", models.ConsoleTypeSerial)
	if err != nil || capability == nil {
		// Console capability not found - may not be synced yet
		slog.Warn("Console capability not found", "connection_method", cm.ID, "manager", managerID)
		// Allow connection attempt anyway - capability may be synced later
	} else {
		// Check if console is enabled
		if !capability.ServiceEnabled {
			h.writeErrorResponse(w, http.StatusServiceUnavailable, "Base.1.0.GeneralError", "serial console is not enabled on this manager")
			return
		}

		// Check max concurrent sessions
		activeSessions, err := h.db.GetConsoleSessions(ctx, cm.ID, models.ConsoleSessionStateActive)
		if err != nil {
			slog.Error("Failed to get active console sessions", "error", err)
		} else {
			// Count active serial console sessions
			activeSerialSessions := 0
			for _, session := range activeSessions {
				if session.ConsoleType == models.ConsoleTypeSerial {
					activeSerialSessions++
				}
			}

			if capability.MaxConcurrentSession > 0 && activeSerialSessions >= capability.MaxConcurrentSession {
				h.writeErrorResponse(w, http.StatusServiceUnavailable, "Base.1.0.GeneralError",
					fmt.Sprintf("maximum concurrent sessions (%d) exceeded", capability.MaxConcurrentSession))
				return
			}
		}
	}

	// Create session ID
	sessionID := uuid.New().String()

	// Create console session record in database
	consoleSession := &models.ConsoleSession{
		SessionID:          sessionID,
		ConnectionMethodID: cm.ID,
		ManagerID:          managerID,
		ConsoleType:        models.ConsoleTypeSerial,
		ConnectType:        req.ConnectType,
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          user.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       fmt.Sprintf("/ws/console/%s", sessionID),
	}

	if err := h.db.CreateConsoleSession(ctx, consoleSession); err != nil {
		slog.Error("Failed to create console session",
			"error", err,
			"user", user.Username,
			"manager", managerID,
			"console_type", models.ConsoleTypeSerial)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.GeneralError", "failed to create console session")
		return
	}

	// Audit log: Console session created
	slog.Info("Console session created",
		"session_id", sessionID,
		"manager", managerID,
		"user", user.Username,
		"console_type", models.ConsoleTypeSerial,
		"connection_method", cm.ID)

	// Start console session handler in background
	go h.startConsoleSession(consoleSession, cm)

	// Return session resource
	sessionResource := map[string]interface{}{
		"@odata.type":  "#ShoalConsoleSession.v1_0_0.ConsoleSession",
		"@odata.id":    fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions/%s", managerID, sessionID),
		"Id":           sessionID,
		"ConsoleType":  string(consoleSession.ConsoleType),
		"ConnectType":  consoleSession.ConnectType,
		"State":        string(consoleSession.State),
		"WebSocketURI": consoleSession.WebSocketURI,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(sessionResource)
}

// handleConnectGraphicalConsole handles POST requests to create a graphical console session
func (h *Handler) handleConnectGraphicalConsole(w http.ResponseWriter, r *http.Request, managerID string) {
	// Check permissions - require operator or admin role
	user := getUserFromContext(r.Context())
	if !auth.IsOperator(user) {
		h.writeErrorResponse(w, http.StatusForbidden, "Base.1.0.GeneralError", "operator privileges required for console access")
		return
	}

	// Parse request body
	var req struct {
		ConnectType string `json:"ConnectType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.GeneralError", "invalid request body")
		return
	}

	// Validate ConnectType
	if req.ConnectType != "Oem" && req.ConnectType != "KVMIP" {
		h.writeErrorResponse(w, http.StatusBadRequest, "Base.1.0.GeneralError", "only ConnectType 'Oem' or 'KVMIP' is supported")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Get connection method for this manager ID
	cm, err := h.db.GetConnectionMethod(ctx, managerID)
	if err != nil || cm == nil {
		// Try to find by name in the BMC table (fallback for backward compatibility)
		bmcs, err := h.db.GetBMCs(ctx)
		if err != nil {
			h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "manager not found")
			return
		}
		for _, bmc := range bmcs {
			if bmc.Name == managerID {
				h.writeErrorResponse(w, http.StatusNotImplemented, "Base.1.0.GeneralError", "console access requires connection method")
				return
			}
		}
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "manager not found")
		return
	}

	// Check console capability and max sessions
	capability, err := h.db.GetConsoleCapability(ctx, cm.ID, "", models.ConsoleTypeGraphical)
	if err != nil || capability == nil {
		// Console capability not found - may not be synced yet
		slog.Warn("Console capability not found", "connection_method", cm.ID, "manager", managerID)
		// Allow connection attempt anyway - capability may be synced later
	} else {
		// Check if console is enabled
		if !capability.ServiceEnabled {
			h.writeErrorResponse(w, http.StatusServiceUnavailable, "Base.1.0.GeneralError", "graphical console is not enabled on this manager")
			return
		}

		// Check max concurrent sessions
		activeSessions, err := h.db.GetConsoleSessions(ctx, cm.ID, models.ConsoleSessionStateActive)
		if err != nil {
			slog.Error("Failed to get active console sessions", "error", err)
		} else {
			// Count active graphical console sessions
			activeGraphicalSessions := 0
			for _, session := range activeSessions {
				if session.ConsoleType == models.ConsoleTypeGraphical {
					activeGraphicalSessions++
				}
			}

			if capability.MaxConcurrentSession > 0 && activeGraphicalSessions >= capability.MaxConcurrentSession {
				h.writeErrorResponse(w, http.StatusServiceUnavailable, "Base.1.0.GeneralError",
					fmt.Sprintf("maximum concurrent sessions (%d) exceeded", capability.MaxConcurrentSession))
				return
			}
		}
	}

	// Create session ID
	sessionID := uuid.New().String()

	// Create console session record in database
	consoleSession := &models.ConsoleSession{
		SessionID:          sessionID,
		ConnectionMethodID: cm.ID,
		ManagerID:          managerID,
		ConsoleType:        models.ConsoleTypeGraphical,
		ConnectType:        req.ConnectType,
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          user.Username,
		CreatedAt:          time.Now(),
		LastActivity:       time.Now(),
		WebSocketURI:       fmt.Sprintf("/ws/console/%s", sessionID),
	}

	if err := h.db.CreateConsoleSession(ctx, consoleSession); err != nil {
		slog.Error("Failed to create console session",
			"error", err,
			"user", user.Username,
			"manager", managerID,
			"console_type", models.ConsoleTypeGraphical)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.GeneralError", "failed to create console session")
		return
	}

	// Audit log: Console session created
	slog.Info("Console session created",
		"session_id", sessionID,
		"manager", managerID,
		"user", user.Username,
		"console_type", models.ConsoleTypeGraphical,
		"connection_method", cm.ID)

	// Start console session handler in background
	go h.startGraphicalConsoleSession(consoleSession, cm)

	// Return session resource
	sessionResource := map[string]interface{}{
		"@odata.type":  "#ShoalConsoleSession.v1_0_0.ConsoleSession",
		"@odata.id":    fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions/%s", managerID, sessionID),
		"Id":           sessionID,
		"ConsoleType":  string(consoleSession.ConsoleType),
		"ConnectType":  consoleSession.ConnectType,
		"State":        string(consoleSession.State),
		"WebSocketURI": consoleSession.WebSocketURI,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(sessionResource)
}

// startConsoleSession initiates the BMC console connection in background
func (h *Handler) startConsoleSession(session *models.ConsoleSession, cm *models.ConnectionMethod) {
	ctx := context.Background()

	// Password should already be decrypted when retrieved from DB
	password := cm.Password

	// Create serial console session handler
	bmcSession := &bmc.SerialConsoleSession{
		SessionID:          session.SessionID,
		ConnectionMethodID: cm.ID,
		ManagerID:          session.ManagerID,
		BMCAddress:         cm.Address,
		BMCUsername:        cm.Username,
		BMCPassword:        password,
		BMCWebSocketURL:    "", // Will be queried by Connect()
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          session.CreatedBy,
	}

	// Attempt to connect to BMC
	connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := bmcSession.Connect(connectCtx); err != nil {
		slog.Error("Failed to connect to BMC console",
			"session_id", session.SessionID,
			"manager", session.ManagerID,
			"user", session.CreatedBy,
			"error", err)
		h.db.UpdateConsoleSessionState(ctx, session.SessionID, models.ConsoleSessionStateError, err.Error())
		return
	}

	// Update session state to active
	h.db.UpdateConsoleSessionState(ctx, session.SessionID, models.ConsoleSessionStateActive, "")

	// Store session in memory for WebSocket attachment
	h.storeBMCSession(session.SessionID, bmcSession)

	slog.Info("Console session connected to BMC",
		"session_id", session.SessionID,
		"manager", session.ManagerID,
		"user", session.CreatedBy,
		"console_type", models.ConsoleTypeSerial)
}

// startGraphicalConsoleSession initiates the BMC graphical console connection in background
func (h *Handler) startGraphicalConsoleSession(session *models.ConsoleSession, cm *models.ConnectionMethod) {
	ctx := context.Background()

	// Password should already be decrypted when retrieved from DB
	password := cm.Password

	// Create graphical console session handler
	bmcSession := &bmc.GraphicalConsoleSession{
		SessionID:          session.SessionID,
		ConnectionMethodID: cm.ID,
		ManagerID:          session.ManagerID,
		BMCAddress:         cm.Address,
		BMCUsername:        cm.Username,
		BMCPassword:        password,
		BMCWebSocketURL:    "", // Will be queried by Connect()
		State:              models.ConsoleSessionStateConnecting,
		CreatedBy:          session.CreatedBy,
	}

	// Attempt to connect to BMC
	connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := bmcSession.Connect(connectCtx); err != nil {
		slog.Error("Failed to connect to BMC graphical console",
			"session_id", session.SessionID,
			"manager", session.ManagerID,
			"user", session.CreatedBy,
			"error", err)
		h.db.UpdateConsoleSessionState(ctx, session.SessionID, models.ConsoleSessionStateError, err.Error())
		return
	}

	// Update session state to active
	h.db.UpdateConsoleSessionState(ctx, session.SessionID, models.ConsoleSessionStateActive, "")

	// Store session in memory for WebSocket attachment
	h.storeGraphicalBMCSession(session.SessionID, bmcSession)

	slog.Info("Console session connected to BMC",
		"session_id", session.SessionID,
		"manager", session.ManagerID,
		"user", session.CreatedBy,
		"console_type", models.ConsoleTypeGraphical)
}

// handleConsoleSessionCollection returns the collection of console sessions
func (h *Handler) handleConsoleSessionCollection(w http.ResponseWriter, r *http.Request, managerID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get connection method for this manager to find sessions
	cm, err := h.db.GetConnectionMethod(ctx, managerID)
	if err != nil || cm == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "manager not found")
		return
	}

	// Get all console sessions for this connection method (empty state returns all)
	sessions, err := h.db.GetConsoleSessions(ctx, cm.ID, "")
	if err != nil {
		slog.Error("Failed to get console sessions", "error", err)
		h.writeErrorResponse(w, http.StatusInternalServerError, "Base.1.0.GeneralError", "failed to retrieve console sessions")
		return
	}

	members := make([]map[string]string, 0, len(sessions))
	for _, session := range sessions {
		members = append(members, map[string]string{
			"@odata.id": fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions/%s", managerID, session.SessionID),
		})
	}

	collection := map[string]interface{}{
		"@odata.type":         "#ShoalConsoleSessionCollection.v1_0_0.ConsoleSessionCollection",
		"@odata.id":           fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions", managerID),
		"Name":                "Console Session Collection",
		"Members":             members,
		"Members@odata.count": len(members),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(collection)
}

// handleConsoleSession returns a specific console session resource
func (h *Handler) handleConsoleSession(w http.ResponseWriter, r *http.Request, managerID, sessionID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	session, err := h.db.GetConsoleSession(ctx, sessionID)
	if err != nil || session == nil {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "console session not found")
		return
	}

	if session.ManagerID != managerID {
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "console session not found")
		return
	}

	sessionResource := map[string]interface{}{
		"@odata.type":      "#ShoalConsoleSession.v1_0_0.ConsoleSession",
		"@odata.id":        fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions/%s", managerID, sessionID),
		"Id":               session.SessionID,
		"Name":             "Serial Console Session",
		"ConsoleType":      string(session.ConsoleType),
		"ConnectType":      session.ConnectType,
		"State":            string(session.State),
		"CreatedBy":        session.CreatedBy,
		"CreatedTime":      session.CreatedAt.Format(time.RFC3339),
		"LastActivityTime": session.LastActivity.Format(time.RFC3339),
		"WebSocketURI":     session.WebSocketURI,
		"Actions": map[string]interface{}{
			"#ConsoleSession.Disconnect": map[string]string{
				"target": fmt.Sprintf("/redfish/v1/Managers/%s/Oem/Shoal/ConsoleSessions/%s/Actions/Disconnect", managerID, sessionID),
			},
		},
	}

	if session.ErrorMessage != "" {
		sessionResource["ErrorMessage"] = session.ErrorMessage
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessionResource)
}

// handleDisconnectConsole handles POST requests to disconnect a console session
func (h *Handler) handleDisconnectConsole(w http.ResponseWriter, r *http.Request, managerID, sessionID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get authenticated user for audit logging
	user := getUserFromContext(r.Context())
	if user == nil {
		h.writeErrorResponse(w, http.StatusUnauthorized, "Base.1.0.Unauthorized", "authentication required")
		return
	}

	// Get session
	session, err := h.db.GetConsoleSession(ctx, sessionID)
	if err != nil || session == nil {
		slog.Warn("Console session not found for disconnect",
			"session_id", sessionID,
			"user", user.Username)
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.GeneralError", "console session not found")
		return
	}

	// Check ownership (users can only disconnect their own sessions, admins can disconnect any)
	if !auth.IsAdmin(user) && session.CreatedBy != user.Username {
		slog.Warn("Unauthorized console session disconnect attempt",
			"session_id", sessionID,
			"user", user.Username,
			"session_owner", session.CreatedBy)
		h.writeErrorResponse(w, http.StatusForbidden, "Base.1.0.GeneralError", "forbidden")
		return
	}

	// Calculate session duration for audit log
	duration := time.Since(session.CreatedAt)

	// Disconnect BMC session if exists
	bmcSession := h.getBMCSession(sessionID)
	if bmcSession != nil {
		bmcSession.Disconnect()
		h.removeBMCSession(sessionID)
	} else {
		// Try graphical console session
		graphicalSession := h.getGraphicalBMCSession(sessionID)
		if graphicalSession != nil {
			graphicalSession.Disconnect()
			h.removeGraphicalBMCSession(sessionID)
		}
	}

	// Update database
	h.db.UpdateConsoleSessionState(ctx, sessionID, models.ConsoleSessionStateDisconnected, "")

	w.WriteHeader(http.StatusNoContent)

	// Audit log: Console session disconnected
	slog.Info("Console session disconnected",
		"session_id", sessionID,
		"manager", managerID,
		"user", user.Username,
		"session_owner", session.CreatedBy,
		"duration_seconds", int(duration.Seconds()),
		"console_type", session.ConsoleType)
}

// handleConsoleWebSocket handles WebSocket connections for console sessions
func (h *Handler) handleConsoleWebSocket(w http.ResponseWriter, r *http.Request, sessionID string) {
	// Authenticate user
	user := getUserFromContext(r.Context())
	if user == nil {
		slog.Warn("Unauthenticated WebSocket console connection attempt",
			"session_id", sessionID,
			"remote_addr", r.RemoteAddr)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get console session
	session, err := h.db.GetConsoleSession(ctx, sessionID)
	if err != nil || session == nil {
		slog.Warn("Console session not found for WebSocket connection",
			"session_id", sessionID,
			"user", user.Username)
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Check ownership (users can only connect to their own sessions, admins can connect to any)
	if !auth.IsAdmin(user) && session.CreatedBy != user.Username {
		slog.Warn("Unauthorized WebSocket console connection attempt",
			"session_id", sessionID,
			"user", user.Username,
			"session_owner", session.CreatedBy)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Get BMC session (try serial first, then graphical)
	bmcSession := h.getBMCSession(sessionID)
	graphicalSession := h.getGraphicalBMCSession(sessionID)
	
	if bmcSession == nil && graphicalSession == nil {
		slog.Warn("Console session not ready for WebSocket connection",
			"session_id", sessionID,
			"user", user.Username,
			"state", session.State)
		http.Error(w, "Console session not ready", http.StatusServiceUnavailable)
		return
	}

	// Upgrade HTTP connection to WebSocket
	userConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade WebSocket",
			"error", err,
			"session_id", sessionID,
			"user", user.Username)
		return
	}

	// Attach user WebSocket to BMC session
	if bmcSession != nil {
		if err := bmcSession.AttachUserWebSocket(userConn); err != nil {
			slog.Error("Failed to attach user WebSocket",
				"error", err,
				"session_id", sessionID,
				"user", user.Username)
			userConn.Close()
			return
		}
	} else if graphicalSession != nil {
		if err := graphicalSession.AttachUserWebSocket(userConn); err != nil {
			slog.Error("Failed to attach user WebSocket",
				"error", err,
				"session_id", sessionID,
				"user", user.Username)
			userConn.Close()
			return
		}
	}

	// Audit log: WebSocket connected
	slog.Info("User WebSocket connected to console session",
		"session_id", sessionID,
		"user", user.Username,
		"manager", session.ManagerID,
		"console_type", session.ConsoleType)
}

// getUserFromContext retrieves the user from request context
func getUserFromContext(ctx context.Context) *models.User {
	if user, ok := ctx.Value(ctxkeys.User).(*models.User); ok {
		return user
	}
	return nil
}

// isConsoleRequest checks if the request is for a console endpoint
func (h *Handler) isConsoleRequest(path string) bool {
	// Check for console-related paths:
	// /v1/Managers/{id}/Actions/Oem/Shoal.ConnectSerialConsole
	// /v1/Managers/{id}/Actions/Oem/Shoal.ConnectGraphicalConsole
	// /v1/Managers/{id}/Oem/Shoal/ConsoleSessions
	// /v1/Managers/{id}/Oem/Shoal/ConsoleSessions/{sessionId}
	// /v1/Managers/{id}/Oem/Shoal/ConsoleSessions/{sessionId}/Actions/Disconnect

	if !strings.HasPrefix(path, "/v1/Managers/") {
		return false
	}

	// Check for console action or OEM console paths
	return strings.Contains(path, "/Actions/Oem/Shoal.ConnectSerialConsole") ||
		strings.Contains(path, "/Actions/Oem/Shoal.ConnectGraphicalConsole") ||
		strings.Contains(path, "/Oem/Shoal/ConsoleSessions")
}

// handleConsoleRequest routes console-related requests
func (h *Handler) handleConsoleRequest(w http.ResponseWriter, r *http.Request, path string, user *models.User) {
	// Add user to context for handlers
	ctx := context.WithValue(r.Context(), ctxkeys.User, user)
	r = r.WithContext(ctx)

	managerID, sessionID, action := parseConsolePath("/redfish" + path)

	switch action {
	case "connect_serial":
		if r.Method != http.MethodPost {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "method not allowed")
			return
		}
		h.handleConnectSerialConsole(w, r, managerID)
	case "connect_graphical":
		if r.Method != http.MethodPost {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "method not allowed")
			return
		}
		h.handleConnectGraphicalConsole(w, r, managerID)
	case "collection":
		if r.Method != http.MethodGet {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "method not allowed")
			return
		}
		h.handleConsoleSessionCollection(w, r, managerID)
	case "session":
		if r.Method != http.MethodGet {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "method not allowed")
			return
		}
		h.handleConsoleSession(w, r, managerID, sessionID)
	case "disconnect":
		if r.Method != http.MethodPost {
			h.writeErrorResponse(w, http.StatusMethodNotAllowed, "Base.1.0.MethodNotAllowed", "method not allowed")
			return
		}
		h.handleDisconnectConsole(w, r, managerID, sessionID)
	default:
		h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "resource not found")
	}
}

// In-memory storage for active BMC sessions (temporary approach)
// In production, consider using a distributed cache like Redis
var (
	bmcSessions             = make(map[string]*bmc.SerialConsoleSession)
	graphicalBMCSessions    = make(map[string]*bmc.GraphicalConsoleSession)
	bmcSessionsMutex        sync.RWMutex
	graphicalSessionsMutex  sync.RWMutex
)

func (h *Handler) storeBMCSession(sessionID string, session *bmc.SerialConsoleSession) {
	bmcSessionsMutex.Lock()
	defer bmcSessionsMutex.Unlock()
	bmcSessions[sessionID] = session
}

func (h *Handler) getBMCSession(sessionID string) *bmc.SerialConsoleSession {
	bmcSessionsMutex.RLock()
	defer bmcSessionsMutex.RUnlock()
	return bmcSessions[sessionID]
}

func (h *Handler) removeBMCSession(sessionID string) {
	bmcSessionsMutex.Lock()
	defer bmcSessionsMutex.Unlock()
	delete(bmcSessions, sessionID)
}

func (h *Handler) storeGraphicalBMCSession(sessionID string, session *bmc.GraphicalConsoleSession) {
	graphicalSessionsMutex.Lock()
	defer graphicalSessionsMutex.Unlock()
	graphicalBMCSessions[sessionID] = session
}

func (h *Handler) getGraphicalBMCSession(sessionID string) *bmc.GraphicalConsoleSession {
	graphicalSessionsMutex.RLock()
	defer graphicalSessionsMutex.RUnlock()
	return graphicalBMCSessions[sessionID]
}

func (h *Handler) removeGraphicalBMCSession(sessionID string) {
	graphicalSessionsMutex.Lock()
	defer graphicalSessionsMutex.Unlock()
	delete(graphicalBMCSessions, sessionID)
}

// handleConsoleRoutes routes console-related requests

// handleWebSocketRoutes routes WebSocket console connections
func (h *Handler) handleWebSocketConsole(w http.ResponseWriter, r *http.Request) {
	// Parse session ID from path: /ws/console/{sessionID}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "ws" || parts[1] != "console" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	sessionID := parts[2]
	h.handleConsoleWebSocket(w, r, sessionID)
}

// parseConsolePath parses console-related URLs
// Returns managerID, sessionID, action
func parseConsolePath(path string) (string, string, string) {
	// Expected patterns:
	// /redfish/v1/Managers/{managerID}/Actions/Oem/Shoal.ConnectSerialConsole
	// /redfish/v1/Managers/{managerID}/Actions/Oem/Shoal.ConnectGraphicalConsole
	// /redfish/v1/Managers/{managerID}/Oem/Shoal/ConsoleSessions
	// /redfish/v1/Managers/{managerID}/Oem/Shoal/ConsoleSessions/{sessionID}
	// /redfish/v1/Managers/{managerID}/Oem/Shoal/ConsoleSessions/{sessionID}/Actions/Disconnect

	parts := strings.Split(strings.Trim(path, "/"), "/")

	if len(parts) < 4 || parts[0] != "redfish" || parts[1] != "v1" || parts[2] != "Managers" {
		return "", "", ""
	}

	managerID := parts[3]

	// Check for ConnectSerialConsole action
	if len(parts) >= 7 && parts[4] == "Actions" && parts[5] == "Oem" && parts[6] == "Shoal.ConnectSerialConsole" {
		return managerID, "", "connect_serial"
	}

	// Check for ConnectGraphicalConsole action
	if len(parts) >= 7 && parts[4] == "Actions" && parts[5] == "Oem" && parts[6] == "Shoal.ConnectGraphicalConsole" {
		return managerID, "", "connect_graphical"
	}

	// Check for ConsoleSessions
	if len(parts) >= 6 && parts[4] == "Oem" && parts[5] == "Shoal" {
		if len(parts) == 7 && parts[6] == "ConsoleSessions" {
			return managerID, "", "collection"
		}
		if len(parts) >= 8 && parts[6] == "ConsoleSessions" {
			sessionID := parts[7]
			if len(parts) == 8 {
				return managerID, sessionID, "session"
			}
			if len(parts) == 10 && parts[8] == "Actions" && parts[9] == "Disconnect" {
				return managerID, sessionID, "disconnect"
			}
		}
	}

	return "", "", ""
}
