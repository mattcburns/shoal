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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewConverter(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oci-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	conv, err := NewConverter(tempDir)
	if err != nil {
		t.Fatalf("NewConverter() failed: %v", err)
	}
	defer conv.Stop()

	if conv.storageDir != tempDir {
		t.Errorf("expected storageDir %q, got %q", tempDir, conv.storageDir)
	}

	if conv.images == nil {
		t.Error("images map should be initialized")
	}

	if conv.refreshTicker == nil {
		t.Error("refreshTicker should be initialized")
	}
}

func TestGenerateImageID(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
	}{
		{
			name:     "simple image",
			imageRef: "oci://ghcr.io/fedora/coreos:stable",
		},
		{
			name:     "image with latest tag",
			imageRef: "oci://docker.io/nginx:latest",
		},
		{
			name:     "image with sha",
			imageRef: "oci://registry.example.com/app@sha256:abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id1 := generateImageID(tt.imageRef)
			id2 := generateImageID(tt.imageRef)

			if id1 != id2 {
				t.Errorf("generateImageID() should be deterministic, got %q and %q", id1, id2)
			}

			if len(id1) != 16 {
				t.Errorf("expected ID length 16, got %d", len(id1))
			}

			// Different images should produce different IDs
			differentRef := tt.imageRef + "-modified"
			id3 := generateImageID(differentRef)
			if id1 == id3 {
				t.Error("different image refs should produce different IDs")
			}
		})
	}
}

func TestIsMutableTag(t *testing.T) {
	tests := []struct {
		name        string
		ociImage    string
		wantMutable bool
	}{
		{
			name:        "latest tag",
			ociImage:    "docker.io/nginx:latest",
			wantMutable: true,
		},
		{
			name:        "stable tag",
			ociImage:    "ghcr.io/fedora/coreos:stable",
			wantMutable: true,
		},
		{
			name:        "main tag",
			ociImage:    "registry.example.com/app:main",
			wantMutable: true,
		},
		{
			name:        "dev tag",
			ociImage:    "registry.example.com/app:dev",
			wantMutable: true,
		},
		{
			name:        "nightly tag",
			ociImage:    "registry.example.com/app:nightly",
			wantMutable: true,
		},
		{
			name:        "no tag specified",
			ociImage:    "docker.io/nginx",
			wantMutable: true,
		},
		{
			name:        "specific version tag",
			ociImage:    "docker.io/nginx:1.21.0",
			wantMutable: false,
		},
		{
			name:        "sha256 reference",
			ociImage:    "registry.example.com/app@sha256:abc123",
			wantMutable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMutableTag(tt.ociImage)
			if got != tt.wantMutable {
				t.Errorf("isMutableTag(%q) = %v, want %v", tt.ociImage, got, tt.wantMutable)
			}
		})
	}
}

func TestGenerateToken(t *testing.T) {
	token1, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken() failed: %v", err)
	}

	if len(token1) != 64 {
		t.Errorf("expected token length 64 (32 bytes hex-encoded), got %d", len(token1))
	}

	token2, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken() failed: %v", err)
	}

	if token1 == token2 {
		t.Error("generateToken() should generate different tokens each time")
	}
}

func TestGenerateShortID(t *testing.T) {
	id1 := generateShortID()
	id2 := generateShortID()

	if len(id1) != 8 {
		t.Errorf("expected short ID length 8 (4 bytes hex-encoded), got %d", len(id1))
	}

	if id1 == id2 {
		t.Error("generateShortID() should generate different IDs each time")
	}
}

func TestGetImageNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oci-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	conv, err := NewConverter(tempDir)
	if err != nil {
		t.Fatalf("NewConverter() failed: %v", err)
	}
	defer conv.Stop()

	_, err = conv.GetImage("nonexistent", "token")
	if err == nil {
		t.Error("expected error for nonexistent image, got nil")
	}
}

func TestGetImageInvalidToken(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oci-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	conv, err := NewConverter(tempDir)
	if err != nil {
		t.Fatalf("NewConverter() failed: %v", err)
	}
	defer conv.Stop()

	// Manually add an image to the cache
	imageID := "test-image-id"
	info := &ImageInfo{
		ID:          imageID,
		ImageRef:    "oci://test:latest",
		ISOPath:     filepath.Join(tempDir, imageID+".iso"),
		Token:       "valid-token",
		CreatedAt:   time.Now(),
		LastUsed:    time.Now(),
		Size:        1024,
		IsLatestTag: true,
		LastRefresh: time.Now(),
	}

	conv.mu.Lock()
	conv.images[imageID] = info
	conv.mu.Unlock()

	// Try to get with wrong token
	_, err = conv.GetImage(imageID, "wrong-token")
	if err == nil {
		t.Error("expected error for invalid token, got nil")
	}

	// Try to get with correct token
	retrieved, err := conv.GetImage(imageID, "valid-token")
	if err != nil {
		t.Errorf("GetImage() with valid token failed: %v", err)
	}

	if retrieved.ID != imageID {
		t.Errorf("expected image ID %q, got %q", imageID, retrieved.ID)
	}
}

func TestCleanupOldImages(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oci-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	conv, err := NewConverter(tempDir)
	if err != nil {
		t.Fatalf("NewConverter() failed: %v", err)
	}
	defer conv.Stop()

	// Add old and new images
	oldImageID := "old-image"
	oldISOPath := filepath.Join(tempDir, oldImageID+".iso")
	if err := os.WriteFile(oldISOPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create old ISO file: %v", err)
	}

	oldInfo := &ImageInfo{
		ID:          oldImageID,
		ImageRef:    "oci://old:latest",
		ISOPath:     oldISOPath,
		Token:       "old-token",
		CreatedAt:   time.Now().Add(-48 * time.Hour),
		LastUsed:    time.Now().Add(-48 * time.Hour),
		Size:        1024,
		IsLatestTag: true,
		LastRefresh: time.Now().Add(-48 * time.Hour),
	}

	newImageID := "new-image"
	newISOPath := filepath.Join(tempDir, newImageID+".iso")
	if err := os.WriteFile(newISOPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create new ISO file: %v", err)
	}

	newInfo := &ImageInfo{
		ID:          newImageID,
		ImageRef:    "oci://new:latest",
		ISOPath:     newISOPath,
		Token:       "new-token",
		CreatedAt:   time.Now(),
		LastUsed:    time.Now(),
		Size:        1024,
		IsLatestTag: true,
		LastRefresh: time.Now(),
	}

	conv.mu.Lock()
	conv.images[oldImageID] = oldInfo
	conv.images[newImageID] = newInfo
	conv.mu.Unlock()

	// Cleanup images older than 24 hours
	if err := conv.CleanupOldImages(24 * time.Hour); err != nil {
		t.Errorf("CleanupOldImages() failed: %v", err)
	}

	// Check that old image was removed
	conv.mu.RLock()
	if _, exists := conv.images[oldImageID]; exists {
		t.Error("old image should have been removed")
	}

	// Check that new image still exists
	if _, exists := conv.images[newImageID]; !exists {
		t.Error("new image should still exist")
	}
	conv.mu.RUnlock()

	// Check that old ISO file was removed
	if _, err := os.Stat(oldISOPath); !os.IsNotExist(err) {
		t.Error("old ISO file should have been removed")
	}

	// Check that new ISO file still exists
	if _, err := os.Stat(newISOPath); os.IsNotExist(err) {
		t.Error("new ISO file should still exist")
	}
}

// TestConvertToISO_InvalidImageRef tests validation of OCI image references
func TestConvertToISO_InvalidImageRef(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "oci-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	conv, err := NewConverter(tempDir)
	if err != nil {
		t.Fatalf("NewConverter() failed: %v", err)
	}
	defer conv.Stop()

	ctx := context.Background()

	// Test without oci:// prefix
	_, _, err = conv.ConvertToISO(ctx, "docker.io/nginx:latest")
	if err == nil {
		t.Error("expected error for image ref without oci:// prefix, got nil")
	}

	// Test with http:// prefix (should fail)
	_, _, err = conv.ConvertToISO(ctx, "http://example.com/image.iso")
	if err == nil {
		t.Error("expected error for http:// image ref, got nil")
	}
}

// Note: Actual OCI image conversion tests would require podman/buildah to be installed
// and would be slow, so they are omitted from unit tests. Integration tests should
// cover the full conversion workflow.
