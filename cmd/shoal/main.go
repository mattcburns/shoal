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

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shoal/internal/api"
	"shoal/internal/database"
	"shoal/internal/logging"
	"shoal/internal/web"
	"shoal/pkg/auth"
	"shoal/pkg/models"
)

func main() {
	var (
		port          = flag.String("port", "8080", "HTTP server port")
		dbPath        = flag.String("db", "shoal.db", "SQLite database path")
		logLevel      = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		encryptionKey = flag.String("encryption-key", "", "Encryption key for BMC passwords (uses SHOAL_ENCRYPTION_KEY env var if not set)")
	)
	flag.Parse()

	// Initialize logging
	logger := logging.New(*logLevel)
	slog.SetDefault(logger)

	// Get encryption key from environment if not provided via flag
	if *encryptionKey == "" {
		*encryptionKey = os.Getenv("SHOAL_ENCRYPTION_KEY")
	}

	// If still no encryption key, generate a warning (in production, you might want to generate one)
	if *encryptionKey == "" {
		slog.Warn("No encryption key provided. BMC passwords will be stored in plaintext. Use --encryption-key or SHOAL_ENCRYPTION_KEY environment variable.")
	}

	ctx := context.Background()

	// Initialize database with encryption key
	db, err := database.NewWithEncryption(*dbPath, *encryptionKey)
	if err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// Run database migrations
	if err := db.Migrate(ctx); err != nil {
		slog.Error("Failed to migrate database", "error", err)
		os.Exit(1)
	}

	// Create default admin user if no users exist
	if err := createDefaultAdminUser(ctx, db); err != nil {
		slog.Error("Failed to create default admin user", "error", err)
		os.Exit(1)
	}

	// Initialize HTTP server
	mux := http.NewServeMux()

	// Register API routes
	apiHandler := api.New(db)
	mux.Handle("/redfish/", apiHandler)

	// Register web interface routes
	webHandler := web.New(db)
	mux.Handle("/", webHandler)

	server := &http.Server{
		Addr:         ":" + *port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		slog.Info("Starting Redfish Aggregator server", "port", *port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	// Create context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server exited")
}

// createDefaultAdminUser creates a default admin user if no users exist
func createDefaultAdminUser(ctx context.Context, db *database.DB) error {
	// Check if any users exist
	count, err := db.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("failed to count users: %w", err)
	}

	// If users already exist, nothing to do
	if count > 0 {
		return nil
	}

	// Generate a random password for the default admin
	defaultPassword := "admin" // Default password

	// Check if a custom admin password is provided via environment
	if envPassword := os.Getenv("SHOAL_ADMIN_PASSWORD"); envPassword != "" {
		defaultPassword = envPassword
	}

	// Hash the password
	passwordHash, err := auth.HashPassword(defaultPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Generate user ID
	userIDBytes := make([]byte, 16)
	if _, err := rand.Read(userIDBytes); err != nil {
		return fmt.Errorf("failed to generate user ID: %w", err)
	}
	userID := hex.EncodeToString(userIDBytes)

	// Create the default admin user
	adminUser := &models.User{
		ID:           userID,
		Username:     "admin",
		PasswordHash: passwordHash,
		Role:         models.RoleAdmin,
		Enabled:      true,
	}

	if err := db.CreateUser(ctx, adminUser); err != nil {
		return fmt.Errorf("failed to create admin user: %w", err)
	}

	slog.Info("Created default admin user", "username", "admin")
	if defaultPassword == "admin" {
		slog.Warn("Using default admin password. Please change it immediately!")
	}

	return nil
}
