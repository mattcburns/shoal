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
	"shoal/internal/imageproxy"
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

		// Image proxy configuration
		enableImageProxy         = flag.Bool("enable-image-proxy", false, "Enable HTTP image proxy for BMCs")
		imageProxyPort           = flag.String("image-proxy-port", "8082", "Port for image proxy server")
		imageProxyAllowedDomains = flag.String("image-proxy-allowed-domains", "*", "Comma-separated list of allowed domains (* for all)")
		imageProxyAllowedSubnets = flag.String("image-proxy-allowed-subnets", "", "Comma-separated list of allowed IP subnets (CIDR notation)")
		imageProxyRateLimit      = flag.Int("image-proxy-rate-limit", 10, "Max concurrent downloads per IP")
		cloudInitStorageDir      = flag.String("cloud-init-storage-dir", "/var/lib/shoal/cloud-init", "Directory for storing generated cloud-init ISOs")
		ociStorageDir            = flag.String("oci-storage-dir", "/var/lib/shoal/oci", "Directory for storing OCI-converted ISOs")
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

	// Start image proxy server if enabled (do this first to get the generator)
	var proxyServer *http.Server
	var apiProxyConfig *api.ImageProxyConfig
	if *enableImageProxy {
		proxyConfig, err := imageproxy.NewConfig(*imageProxyPort, *imageProxyAllowedDomains, *imageProxyAllowedSubnets, *imageProxyRateLimit)
		if err != nil {
			slog.Error("Failed to create image proxy config", "error", err)
			os.Exit(1)
		}

		// Set cloud-init storage directory
		proxyConfig.CloudInitStorageDir = *cloudInitStorageDir

		// Set OCI storage directory
		proxyConfig.OCIStorageDir = *ociStorageDir

		proxyHandler, err := imageproxy.NewServer(proxyConfig)
		if err != nil {
			slog.Error("Failed to create image proxy server", "error", err)
			os.Exit(1)
		}
		proxyMux := http.NewServeMux()
		proxyMux.Handle("/proxy", proxyHandler)
		// Add cloud-init ISO endpoint
		proxyMux.HandleFunc("/cloudinit-iso/", proxyHandler.ServeCloudInitISO)
		// Add OCI ISO endpoint
		proxyMux.HandleFunc("/oci-iso/", proxyHandler.ServeOCIISO)

		// Create API proxy config with cloud-init generator and OCI converter
		baseURL := fmt.Sprintf("http://localhost:%s", *imageProxyPort)
		apiProxyConfig = &api.ImageProxyConfig{
			Enabled: true,
			BaseURL: baseURL,
		}

		// Wire cloud-init generator to API if available
		gen := proxyHandler.GetCloudInitGenerator()
		if gen != nil {
			apiProxyConfig.CloudInitGeneratorFunc = gen.GenerateISO
		}

		// Wire OCI converter to API if available
		ociConv := proxyHandler.GetOCIConverter()
		if ociConv != nil {
			apiProxyConfig.OCIConverterFunc = ociConv.ConvertToISO
		}

		proxyServer = &http.Server{
			Addr:         ":" + *imageProxyPort,
			Handler:      proxyMux,
			ReadTimeout:  5 * time.Minute, // Longer timeout for large images
			WriteTimeout: 5 * time.Minute,
			IdleTimeout:  120 * time.Second,
		}

		go func() {
			cloudInitEnabled := gen != nil
			ociEnabled := ociConv != nil
			slog.Info("Starting image proxy server", "port", *imageProxyPort, "cloud_init_enabled", cloudInitEnabled, "oci_enabled", ociEnabled)
			if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("Image proxy server failed to start", "error", err)
				os.Exit(1)
			}
		}()
	}

	// Register API routes with image proxy configuration
	var apiHandler http.Handler
	if apiProxyConfig != nil {
		apiHandler = api.NewWithImageProxy(db, apiProxyConfig)
	} else {
		apiHandler = api.New(db)
	}
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown main server
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	// Shutdown proxy server if it was started
	if proxyServer != nil {
		if err := proxyServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("Image proxy server forced to shutdown", "error", err)
		}
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
