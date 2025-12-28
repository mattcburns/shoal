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

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"shoal/internal/ctxkeys"
	"shoal/internal/database"
	"shoal/pkg/auth"
	"shoal/pkg/models"
)

// Authenticator handles Redfish-compliant authentication
type Authenticator struct {
	db *database.DB
}

// New creates a new authenticator
func New(db *database.DB) *Authenticator {
	return &Authenticator{db: db}
}

// AuthenticateRequest handles both basic and session-based authentication
func (a *Authenticator) AuthenticateRequest(r *http.Request) (*models.User, error) {
	// Check for session-based authentication first (X-Auth-Token header)
	if token := r.Header.Get("X-Auth-Token"); token != "" {
		return a.AuthenticateToken(r.Context(), token)
	}

	// Check for basic authentication
	if username, password, ok := r.BasicAuth(); ok {
		return a.authenticateBasic(r.Context(), username, password)
	}

	return nil, fmt.Errorf("no authentication provided")
}

// AuthenticateToken validates a session token
func (a *Authenticator) AuthenticateToken(ctx context.Context, token string) (*models.User, error) {
	session, err := a.db.GetSessionByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	if session == nil {
		return nil, fmt.Errorf("invalid session token")
	}

	// Check session expiration
	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("session has expired")
	}

	// Get the user associated with this session
	user, err := a.db.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	if !user.Enabled {
		return nil, fmt.Errorf("user is disabled")
	}

	return user, nil
}

// authenticateBasic validates basic authentication credentials
func (a *Authenticator) authenticateBasic(ctx context.Context, username, password string) (*models.User, error) {
	// Get user from database
	user, err := a.db.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	// Check if user is enabled
	if !user.Enabled {
		return nil, fmt.Errorf("user is disabled")
	}

	// Verify password
	if err := auth.VerifyPassword(password, user.PasswordHash); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	return user, nil
}

// CreateSession creates a new authentication session
func (a *Authenticator) CreateSession(ctx context.Context, userID string) (*models.Session, error) {
	sessionID, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	session := &models.Session{
		ID:        sessionID,
		UserID:    userID,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour), // 24 hour session
		CreatedAt: time.Now(),
	}

	if err := a.db.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}

// DeleteSession removes a session (logout)
func (a *Authenticator) DeleteSession(ctx context.Context, token string) error {
	return a.db.DeleteSession(ctx, token)
}

// RequireAuth middleware that enforces authentication
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := a.AuthenticateRequest(r)
		if err != nil {
			// Return Redfish-compliant error response aligned with centralized helper
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("OData-Version", "4.0")
			w.Header().Set("WWW-Authenticate", "Basic realm=\"Redfish\"")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"Base.1.0.Unauthorized","message":"Authentication required","@Message.ExtendedInfo":[{"@odata.type":"#Message.v1_1_0.Message","MessageId":"Base.1.0.Unauthorized","Message":"Authentication required","Severity":"Critical","Resolution":"Provide valid credentials and resubmit the request."}]}}`))
			return
		}

		// Add user to request context (typed key)
		ctx := context.WithValue(r.Context(), ctxkeys.User, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUserFromContext extracts the authenticated user from request context

// generateID generates a random ID for sessions
func generateID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// generateToken generates a random session token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// AuthenticateBasic is a public wrapper for authenticateBasic
func (a *Authenticator) AuthenticateBasic(ctx context.Context, username, password string) (*models.User, error) {
	return a.authenticateBasic(ctx, username, password)
}
