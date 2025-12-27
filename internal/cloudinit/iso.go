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

package cloudinit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Generator handles cloud-init ISO generation
type Generator struct {
	storageDir string
	isos       map[string]*ISOInfo
	mu         sync.RWMutex
}

// ISOInfo tracks information about a generated ISO
type ISOInfo struct {
	ID         string
	Path       string
	Token      string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Downloaded bool
}

// NewGenerator creates a new cloud-init ISO generator
func NewGenerator(storageDir string) (*Generator, error) {
	// Create storage directory if it doesn't exist
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &Generator{
		storageDir: storageDir,
		isos:       make(map[string]*ISOInfo),
	}, nil
}

// GenerateISO creates a cloud-init ISO from user-data and meta-data
// Returns the ISO ID and token for secure download
func (g *Generator) GenerateISO(userData, metaData string) (isoID, token string, err error) {
	// Generate unique ID for this ISO
	isoID, err = generateID()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate ISO ID: %w", err)
	}

	// Generate secure token for download
	token, err = generateToken()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate token: %w", err)
	}

	// Create temporary directory for ISO contents
	tempDir := filepath.Join(g.storageDir, fmt.Sprintf("tmp-%s", isoID))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Write user-data file
	userDataPath := filepath.Join(tempDir, "user-data")
	if err := os.WriteFile(userDataPath, []byte(userData), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write user-data: %w", err)
	}

	// Write meta-data file (use default if not provided)
	if metaData == "" {
		metaData = "instance-id: iid-local01\n"
	}
	metaDataPath := filepath.Join(tempDir, "meta-data")
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write meta-data: %w", err)
	}

	// Generate ISO file
	isoPath := filepath.Join(g.storageDir, fmt.Sprintf("%s.iso", isoID))
	if err := g.createISO(tempDir, isoPath); err != nil {
		return "", "", fmt.Errorf("failed to create ISO: %w", err)
	}

	// Store ISO info with expiration (default: 1 hour)
	info := &ISOInfo{
		ID:         isoID,
		Path:       isoPath,
		Token:      token,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(1 * time.Hour),
		Downloaded: false,
	}

	g.mu.Lock()
	g.isos[isoID] = info
	g.mu.Unlock()

	slog.Info("Generated cloud-init ISO", "id", isoID, "path", isoPath)
	return isoID, token, nil
}

// GetISO retrieves ISO information by ID and validates the token
func (g *Generator) GetISO(isoID, token string) (*ISOInfo, error) {
	g.mu.RLock()
	info, exists := g.isos[isoID]
	g.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("ISO not found")
	}

	// Validate token
	if info.Token != token {
		return nil, fmt.Errorf("invalid token")
	}

	// Check expiration
	if time.Now().After(info.ExpiresAt) {
		return nil, fmt.Errorf("ISO has expired")
	}

	return info, nil
}

// MarkDownloaded marks an ISO as downloaded for one-time use enforcement
func (g *Generator) MarkDownloaded(isoID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if info, exists := g.isos[isoID]; exists {
		info.Downloaded = true
	}
}

// CleanupExpired removes expired ISOs and their files
func (g *Generator) CleanupExpired() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	for id, info := range g.isos {
		if now.After(info.ExpiresAt) {
			// Remove ISO file
			if err := os.Remove(info.Path); err != nil && !os.IsNotExist(err) {
				slog.Warn("Failed to remove expired ISO file", "path", info.Path, "error", err)
			}
			// Remove from map
			delete(g.isos, id)
			slog.Info("Cleaned up expired ISO", "id", id)
		}
	}

	return nil
}

// createISO generates an ISO file from the directory contents using genisoimage
func (g *Generator) createISO(sourceDir, isoPath string) error {
	// Use genisoimage to create a NoCloud-compatible ISO
	// -volid cidata is required for cloud-init NoCloud datasource
	// -joliet and -rock for better compatibility
	cmd := exec.Command("genisoimage",
		"-output", isoPath,
		"-volid", "cidata",
		"-joliet",
		"-rock",
		sourceDir,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("genisoimage failed: %w, output: %s", err, string(output))
	}

	return nil
}

// generateID generates a random ID for an ISO
func generateID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// generateToken generates a secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
