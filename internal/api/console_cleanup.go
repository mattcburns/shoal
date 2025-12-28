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
	"log/slog"
	"time"

	"shoal/pkg/models"
)

const (
	// ConsoleSessionIdleTimeout is the default idle timeout for console sessions
	ConsoleSessionIdleTimeout = 30 * time.Minute
	// ConsoleSessionMaxDuration is the maximum duration for a console session
	ConsoleSessionMaxDuration = 12 * time.Hour
	// ConsoleSessionCleanupInterval is how often to check for idle/expired sessions
	ConsoleSessionCleanupInterval = 5 * time.Minute
)

// StartConsoleSessionCleanup starts a background goroutine to clean up idle and expired console sessions
func (h *Handler) StartConsoleSessionCleanup(ctx context.Context) {
	go h.consoleSessionCleanupLoop(ctx)
}

// consoleSessionCleanupLoop runs periodically to clean up idle and expired console sessions
func (h *Handler) consoleSessionCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(ConsoleSessionCleanupInterval)
	defer ticker.Stop()

	slog.Info("Console session cleanup task started",
		"idle_timeout", ConsoleSessionIdleTimeout,
		"max_duration", ConsoleSessionMaxDuration,
		"check_interval", ConsoleSessionCleanupInterval)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Console session cleanup task stopped")
			return
		case <-ticker.C:
			h.cleanupIdleAndExpiredSessions(ctx)
		}
	}
}

// cleanupIdleAndExpiredSessions disconnects and cleans up idle or expired console sessions
func (h *Handler) cleanupIdleAndExpiredSessions(ctx context.Context) {
	if h == nil || h.db == nil {
		slog.Error("Handler or database is nil in cleanupIdleAndExpiredSessions")
		return
	}

	// Get all active sessions across all connection methods
	// We need to query all connection methods first
	connectionMethods, err := h.db.GetConnectionMethods(ctx)
	if err != nil {
		slog.Error("Failed to get connection methods for session cleanup", "error", err)
		return
	}

	now := time.Now()
	cleanedCount := 0

	// Check sessions for each connection method
	for _, cm := range connectionMethods {
		sessions, err := h.db.GetConsoleSessions(ctx, cm.ID, models.ConsoleSessionStateActive)
		if err != nil {
			slog.Error("Failed to get active console sessions for cleanup",
				"connection_method", cm.ID,
				"error", err)
			continue
		}

		for _, session := range sessions {
			shouldCleanup := false
			reason := ""

			idleDuration := now.Sub(session.LastActivity)
			totalDuration := now.Sub(session.CreatedAt)

			// Check idle timeout
			if idleDuration > ConsoleSessionIdleTimeout {
				shouldCleanup = true
				reason = "idle_timeout"
			}

			// Check max duration
			if totalDuration > ConsoleSessionMaxDuration {
				shouldCleanup = true
				reason = "max_duration_exceeded"
			}

			if shouldCleanup {
				// Disconnect BMC session if exists
				bmcSession := h.getBMCSession(session.SessionID)
				if bmcSession != nil {
					bmcSession.Disconnect()
					h.removeBMCSession(session.SessionID)
				}

				// Update database
				updateErr := h.db.UpdateConsoleSessionState(ctx, session.SessionID, models.ConsoleSessionStateDisconnected, "Automatically disconnected: "+reason)
				if updateErr != nil {
					slog.Error("Failed to update console session state during cleanup",
						"session_id", session.SessionID,
						"error", updateErr)
					continue
				}

				slog.Info("Console session automatically disconnected",
					"session_id", session.SessionID,
					"manager", session.ManagerID,
					"user", session.CreatedBy,
					"reason", reason,
					"idle_duration", idleDuration,
					"total_duration", totalDuration)

				cleanedCount++
			}
		}
	}

	if cleanedCount > 0 {
		slog.Info("Console session cleanup completed", "cleaned_count", cleanedCount)
	}
}
