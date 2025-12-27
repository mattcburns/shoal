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

package oci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Converter handles OCI image to ISO conversion
type Converter struct {
	storageDir    string
	images        map[string]*ImageInfo
	mu            sync.RWMutex
	refreshTicker *time.Ticker
	stopChan      chan struct{}
}

// ImageInfo tracks information about a converted OCI image
type ImageInfo struct {
	ID           string
	ImageRef     string // Original OCI image reference (e.g., oci://ghcr.io/fedora/coreos:stable)
	ISOPath      string // Path to the converted ISO
	Token        string // Access token for secure download
	CreatedAt    time.Time
	LastUsed     time.Time
	Size         int64
	IsLatestTag  bool // True if this references a :latest or similar mutable tag
	LastRefresh  time.Time
	RefreshError string
}

// NewConverter creates a new OCI image converter
func NewConverter(storageDir string) (*Converter, error) {
	// Create storage directory if it doesn't exist
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	c := &Converter{
		storageDir: storageDir,
		images:     make(map[string]*ImageInfo),
		stopChan:   make(chan struct{}),
	}

	// Start periodic refresh for mutable tags (every 6 hours by default)
	c.refreshTicker = time.NewTicker(6 * time.Hour)
	go c.refreshLoop()

	return c, nil
}

// ConvertToISO converts an OCI image reference to a bootable ISO
// Returns the image ID and access token
func (c *Converter) ConvertToISO(ctx context.Context, imageRef string) (imageID, token string, err error) {
	// Parse OCI image reference
	if !strings.HasPrefix(imageRef, "oci://") {
		return "", "", fmt.Errorf("invalid OCI image reference: must start with oci://")
	}

	// Strip oci:// prefix
	ociImage := strings.TrimPrefix(imageRef, "oci://")

	// Generate unique ID based on image reference hash
	imageID = generateImageID(imageRef)

	// Check if we already have this image cached
	c.mu.RLock()
	existing, exists := c.images[imageID]
	c.mu.RUnlock()

	if exists {
		// Check if image needs refresh (for latest tags)
		if existing.IsLatestTag && time.Since(existing.LastRefresh) > 1*time.Hour {
			slog.Info("OCI image cache hit but needs refresh", "image_ref", imageRef, "age", time.Since(existing.LastRefresh))
			// Refresh in background, return existing for now
			go func() {
				if err := c.refreshImage(context.Background(), imageID); err != nil {
					slog.Error("Failed to refresh OCI image", "image_ref", imageRef, "error", err)
				}
			}()
		}

		// Update last used timestamp
		c.mu.Lock()
		existing.LastUsed = time.Now()
		c.mu.Unlock()

		return imageID, existing.Token, nil
	}

	// Generate secure token for download
	token, err = generateToken()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate token: %w", err)
	}

	// Create ISO from OCI image
	isoPath := filepath.Join(c.storageDir, fmt.Sprintf("%s.iso", imageID))

	slog.Info("Converting OCI image to ISO", "image_ref", imageRef, "iso_path", isoPath)

	// Perform the conversion
	if err := c.convertImageToISO(ctx, ociImage, isoPath); err != nil {
		return "", "", fmt.Errorf("failed to convert OCI image to ISO: %w", err)
	}

	// Get file size
	fileInfo, err := os.Stat(isoPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to stat converted ISO: %w", err)
	}

	// Determine if this is a mutable tag (latest, stable, etc.)
	isLatestTag := isMutableTag(ociImage)

	// Store image info
	info := &ImageInfo{
		ID:          imageID,
		ImageRef:    imageRef,
		ISOPath:     isoPath,
		Token:       token,
		CreatedAt:   time.Now(),
		LastUsed:    time.Now(),
		Size:        fileInfo.Size(),
		IsLatestTag: isLatestTag,
		LastRefresh: time.Now(),
	}

	c.mu.Lock()
	c.images[imageID] = info
	c.mu.Unlock()

	slog.Info("OCI image converted successfully", "image_ref", imageRef, "size_mb", info.Size/1024/1024)
	return imageID, token, nil
}

// GetImage retrieves image information by ID and validates the token
func (c *Converter) GetImage(imageID, token string) (*ImageInfo, error) {
	c.mu.RLock()
	info, exists := c.images[imageID]
	c.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("image not found")
	}

	// Validate token
	if info.Token != token {
		return nil, fmt.Errorf("invalid token")
	}

	// Update last used timestamp
	c.mu.Lock()
	info.LastUsed = time.Now()
	c.mu.Unlock()

	return info, nil
}

// convertImageToISO performs the actual OCI image to ISO conversion using buildah/podman
func (c *Converter) convertImageToISO(ctx context.Context, ociImage, isoPath string) error {
	// Check if podman or buildah is available
	var tool string
	for _, candidate := range []string{"podman", "buildah"} {
		if _, err := exec.LookPath(candidate); err == nil {
			tool = candidate
			break
		}
	}

	if tool == "" {
		return fmt.Errorf("neither podman nor buildah found in PATH; OCI conversion requires one of these tools")
	}

	// Create a temporary directory for the conversion
	tempDir, err := os.MkdirTemp(c.storageDir, "oci-convert-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			slog.Warn("Failed to cleanup temp directory", "path", tempDir, "error", err)
		}
	}()

	// Pull the OCI image
	slog.Debug("Pulling OCI image", "tool", tool, "image", ociImage)
	pullCmd := exec.CommandContext(ctx, tool, "pull", ociImage)
	pullCmd.Stdout = os.Stdout
	pullCmd.Stderr = os.Stderr
	if err := pullCmd.Run(); err != nil {
		return fmt.Errorf("failed to pull OCI image: %w", err)
	}

	// Create a container from the image
	containerName := fmt.Sprintf("shoal-oci-convert-%s", generateShortID())
	createCmd := exec.CommandContext(ctx, tool, "create", "--name", containerName, ociImage)
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Export the container filesystem
	exportPath := filepath.Join(tempDir, "rootfs.tar")
	exportCmd := exec.CommandContext(ctx, tool, "export", "-o", exportPath, containerName)
	if err := exportCmd.Run(); err != nil {
		// Clean up container
		_ = exec.CommandContext(ctx, tool, "rm", containerName).Run()
		return fmt.Errorf("failed to export container: %w", err)
	}

	// Remove the container
	_ = exec.CommandContext(ctx, tool, "rm", containerName).Run()

	// Extract the filesystem
	rootfsDir := filepath.Join(tempDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		return fmt.Errorf("failed to create rootfs directory: %w", err)
	}

	tarCmd := exec.CommandContext(ctx, "tar", "-xf", exportPath, "-C", rootfsDir)
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("failed to extract rootfs: %w", err)
	}

	// Create bootable ISO using genisoimage or mkisofs
	var isoTool string
	for _, candidate := range []string{"genisoimage", "mkisofs", "xorriso"} {
		if _, err := exec.LookPath(candidate); err == nil {
			isoTool = candidate
			break
		}
	}

	if isoTool == "" {
		return fmt.Errorf("no ISO creation tool found (genisoimage, mkisofs, or xorriso required)")
	}

	slog.Debug("Creating bootable ISO", "tool", isoTool, "output", isoPath)

	var isoCmd *exec.Cmd
	if isoTool == "xorriso" {
		// xorriso has different syntax
		isoCmd = exec.CommandContext(ctx, "xorriso",
			"-as", "mkisofs",
			"-o", isoPath,
			"-R", "-J",
			"-V", "OCI_BOOT",
			"-b", "isolinux/isolinux.bin",
			"-c", "isolinux/boot.cat",
			"-no-emul-boot",
			"-boot-load-size", "4",
			"-boot-info-table",
			rootfsDir)
	} else {
		// genisoimage or mkisofs
		isoCmd = exec.CommandContext(ctx, isoTool,
			"-o", isoPath,
			"-R", "-J",
			"-V", "OCI_BOOT",
			"-b", "isolinux/isolinux.bin",
			"-c", "isolinux/boot.cat",
			"-no-emul-boot",
			"-boot-load-size", "4",
			"-boot-info-table",
			rootfsDir)
	}

	isoCmd.Stdout = os.Stdout
	isoCmd.Stderr = os.Stderr
	if err := isoCmd.Run(); err != nil {
		return fmt.Errorf("failed to create ISO: %w (note: OCI image must contain bootloader in isolinux/ directory)", err)
	}

	return nil
}

// refreshImage refreshes a cached OCI image (for mutable tags like :latest)
func (c *Converter) refreshImage(ctx context.Context, imageID string) error {
	c.mu.RLock()
	info, exists := c.images[imageID]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("image not found")
	}

	if !info.IsLatestTag {
		return nil // No need to refresh immutable tags
	}

	slog.Info("Refreshing OCI image", "image_ref", info.ImageRef)

	// Create new ISO path
	newISOPath := filepath.Join(c.storageDir, fmt.Sprintf("%s-new.iso", imageID))

	// Convert the image again
	ociImage := strings.TrimPrefix(info.ImageRef, "oci://")
	if err := c.convertImageToISO(ctx, ociImage, newISOPath); err != nil {
		c.mu.Lock()
		info.RefreshError = err.Error()
		c.mu.Unlock()
		return fmt.Errorf("failed to refresh OCI image: %w", err)
	}

	// Replace old ISO with new one
	oldISOPath := info.ISOPath
	if err := os.Rename(newISOPath, oldISOPath); err != nil {
		_ = os.Remove(newISOPath)
		return fmt.Errorf("failed to replace old ISO: %w", err)
	}

	// Update file size
	fileInfo, err := os.Stat(oldISOPath)
	if err != nil {
		return fmt.Errorf("failed to stat refreshed ISO: %w", err)
	}

	c.mu.Lock()
	info.LastRefresh = time.Now()
	info.RefreshError = ""
	info.Size = fileInfo.Size()
	c.mu.Unlock()

	slog.Info("OCI image refreshed successfully", "image_ref", info.ImageRef, "size_mb", info.Size/1024/1024)
	return nil
}

// refreshLoop periodically refreshes mutable OCI images
func (c *Converter) refreshLoop() {
	for {
		select {
		case <-c.refreshTicker.C:
			c.mu.RLock()
			var toRefresh []string
			for id, info := range c.images {
				if info.IsLatestTag && time.Since(info.LastRefresh) > 6*time.Hour {
					toRefresh = append(toRefresh, id)
				}
			}
			c.mu.RUnlock()

			for _, id := range toRefresh {
				if err := c.refreshImage(context.Background(), id); err != nil {
					slog.Error("Failed to refresh OCI image in background", "image_id", id, "error", err)
				}
			}
		case <-c.stopChan:
			return
		}
	}
}

// Stop stops the background refresh loop
func (c *Converter) Stop() {
	if c.refreshTicker != nil {
		c.refreshTicker.Stop()
	}
	close(c.stopChan)
}

// CleanupOldImages removes ISOs that haven't been used in the specified duration
func (c *Converter) CleanupOldImages(maxAge time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var removed []string
	for id, info := range c.images {
		if time.Since(info.LastUsed) > maxAge {
			// Remove ISO file
			if err := os.Remove(info.ISOPath); err != nil {
				if !os.IsNotExist(err) {
					slog.Warn("Failed to remove old OCI ISO", "path", info.ISOPath, "error", err)
				}
				// File doesn't exist or failed to remove, but continue cleanup
			}
			// Always remove from cache, even if file removal failed or file didn't exist
			removed = append(removed, id)
			delete(c.images, id)
		}
	}

	if len(removed) > 0 {
		slog.Info("Cleaned up old OCI images", "count", len(removed))
	}

	return nil
}

// generateImageID generates a unique ID for an OCI image reference
func generateImageID(imageRef string) string {
	hash := sha256.Sum256([]byte(imageRef))
	return hex.EncodeToString(hash[:])[:16] // Use first 16 chars of hash
}

// generateToken generates a secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// generateShortID generates a short random ID
func generateShortID() string {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().Unix())
	}
	return hex.EncodeToString(bytes)
}

// isMutableTag checks if an OCI image reference uses a mutable tag
func isMutableTag(ociImage string) bool {
	// Common mutable tags that may change over time
	// Note: :master is included for backward compatibility with older images
	mutableTags := []string{":latest", ":stable", ":main", ":master", ":dev", ":nightly"}

	for _, tag := range mutableTags {
		if strings.HasSuffix(ociImage, tag) {
			return true
		}
	}

	// If no tag specified, it defaults to :latest
	if !strings.Contains(ociImage, ":") && !strings.Contains(ociImage, "@sha256:") {
		return true
	}

	return false
}
