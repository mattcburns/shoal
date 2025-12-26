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

package bmc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"shoal/internal/database"
	"shoal/pkg/models"
)

// VirtualMediaSyncer manages periodic synchronization of virtual media state
type VirtualMediaSyncer struct {
	db       *database.DB
	service  *Service
	interval time.Duration
	enabled  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewVirtualMediaSyncer creates a new syncer
func NewVirtualMediaSyncer(db *database.DB, service *Service, interval time.Duration, enabled bool) *VirtualMediaSyncer {
	return &VirtualMediaSyncer{
		db:       db,
		service:  service,
		interval: interval,
		enabled:  enabled,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start begins the periodic sync loop
func (s *VirtualMediaSyncer) Start(ctx context.Context) {
	if !s.enabled {
		slog.Info("Virtual media state sync disabled")
		close(s.doneCh)
		return
	}

	slog.Info("Starting virtual media state sync", "interval", s.interval)

	go func() {
		defer close(s.doneCh)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		// Run initial sync
		if err := s.SyncAll(ctx); err != nil {
			slog.Warn("Initial virtual media sync failed", "error", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := s.SyncAll(ctx); err != nil {
					slog.Warn("Virtual media sync failed", "error", err)
				}
			case <-s.stopCh:
				slog.Info("Virtual media state sync stopped")
				return
			case <-ctx.Done():
				slog.Info("Virtual media state sync context cancelled")
				return
			}
		}
	}()
}

// Stop gracefully stops the sync loop
func (s *VirtualMediaSyncer) Stop() {
	if s.enabled {
		close(s.stopCh)
		<-s.doneCh // Wait for goroutine to finish
	}
}

// SyncAll syncs all enabled connection methods
func (s *VirtualMediaSyncer) SyncAll(ctx context.Context) error {
	slog.Debug("Starting virtual media sync cycle")

	connMethods, err := s.db.GetConnectionMethods(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection methods: %w", err)
	}

	var syncErrors int
	for _, cm := range connMethods {
		if !cm.Enabled {
			continue
		}

		if err := s.SyncConnectionMethod(ctx, cm.ID); err != nil {
			slog.Warn("Failed to sync connection method", "id", cm.ID, "name", cm.Name, "error", err)
			syncErrors++
		}
	}

	if syncErrors > 0 {
		slog.Debug("Virtual media sync completed with errors", "failed", syncErrors, "total", len(connMethods))
	} else {
		slog.Debug("Virtual media sync completed successfully", "synced", len(connMethods))
	}

	return nil
}

// SyncConnectionMethod syncs a single connection method
func (s *VirtualMediaSyncer) SyncConnectionMethod(ctx context.Context, connMethodID string) error {
	// Get connection method details
	cm, err := s.db.GetConnectionMethod(ctx, connMethodID)
	if err != nil {
		return fmt.Errorf("failed to get connection method: %w", err)
	}
	if cm == nil {
		return fmt.Errorf("connection method not found: %s", connMethodID)
	}

	// Parse aggregated managers
	var managers []map[string]interface{}
	if cm.AggregatedManagers != "" {
		if err := json.Unmarshal([]byte(cm.AggregatedManagers), &managers); err != nil {
			return fmt.Errorf("failed to parse aggregated managers: %w", err)
		}
	}

	// Sync virtual media for each manager
	for _, mgr := range managers {
		managerID, ok := mgr["Id"].(string)
		if !ok {
			continue
		}

		if err := s.syncManagerVirtualMedia(ctx, cm, managerID); err != nil {
			slog.Debug("Failed to sync manager virtual media", "connection_method", cm.Name, "manager", managerID, "error", err)
			// Continue with other managers
		}
	}

	return nil
}

// syncManagerVirtualMedia syncs virtual media for a specific manager
func (s *VirtualMediaSyncer) syncManagerVirtualMedia(ctx context.Context, cm *models.ConnectionMethod, managerID string) error {
	// Query VirtualMedia collection
	vmCollectionPath := fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia", managerID)

	req, err := http.NewRequestWithContext(ctx, "GET", vmCollectionPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.service.ProxyRequestToConnectionMethod(ctx, cm.ID, vmCollectionPath, req)
	if err != nil {
		return fmt.Errorf("failed to query virtual media collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var collection struct {
		Members []struct {
			ODataID string `json:"@odata.id"`
		} `json:"Members"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		return fmt.Errorf("failed to decode collection: %w", err)
	}

	// Sync each virtual media resource
	for _, member := range collection.Members {
		if err := s.syncVirtualMediaResource(ctx, cm, managerID, member.ODataID); err != nil {
			slog.Debug("Failed to sync virtual media resource", "odata_id", member.ODataID, "error", err)
			// Continue with other resources
		}
	}

	return nil
}

// syncVirtualMediaResource syncs a single virtual media resource
func (s *VirtualMediaSyncer) syncVirtualMediaResource(ctx context.Context, cm *models.ConnectionMethod, managerID, odataID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", odataID, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.service.ProxyRequestToConnectionMethod(ctx, cm.ID, odataID, req)
	if err != nil {
		return fmt.Errorf("failed to query virtual media resource: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var vmResource struct {
		ID               string   `json:"Id"`
		Image            string   `json:"Image"`
		ImageName        string   `json:"ImageName"`
		Inserted         bool     `json:"Inserted"`
		WriteProtected   bool     `json:"WriteProtected"`
		ConnectedVia     string   `json:"ConnectedVia"`
		MediaTypes       []string `json:"MediaTypes"`
		TransferProtocol string   `json:"TransferProtocolType"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&vmResource); err != nil {
		return fmt.Errorf("failed to decode resource: %w", err)
	}

	// Convert media types to JSON
	mediaTypesJSON := "[]"
	if len(vmResource.MediaTypes) > 0 {
		if b, err := json.Marshal(vmResource.MediaTypes); err == nil {
			mediaTypesJSON = string(b)
		}
	}

	// Build supported protocols JSON
	supportedProtocols := "[]"
	if vmResource.TransferProtocol != "" {
		supportedProtocols = fmt.Sprintf(`["%s"]`, vmResource.TransferProtocol)
	}

	// Prepare image URL and name pointers
	var imageURL, imageName *string
	if vmResource.Image != "" {
		imageURL = &vmResource.Image
	}
	if vmResource.ImageName != "" {
		imageName = &vmResource.ImageName
	}

	// Get existing resource to check for state changes
	existing, err := s.db.GetVirtualMediaResource(ctx, cm.ID, managerID, vmResource.ID)
	if err != nil {
		return fmt.Errorf("failed to get existing resource: %w", err)
	}

	// Log state changes
	if existing != nil {
		if existing.IsInserted != vmResource.Inserted {
			if vmResource.Inserted {
				slog.Info("Virtual media inserted", "connection_method", cm.Name, "manager", managerID, "resource", vmResource.ID, "image", vmResource.Image)
			} else {
				slog.Info("Virtual media ejected", "connection_method", cm.Name, "manager", managerID, "resource", vmResource.ID)
			}
		}
	}

	// Upsert the resource
	if err := s.db.UpsertVirtualMediaResource(ctx, cm.ID, managerID, vmResource.ID, odataID,
		mediaTypesJSON, supportedProtocols, imageURL, imageName,
		vmResource.Inserted, vmResource.WriteProtected, vmResource.ConnectedVia); err != nil {
		return fmt.Errorf("failed to upsert resource: %w", err)
	}

	return nil
}

// ProxyRequestToConnectionMethod is a wrapper around the service's proxy method
// This allows the syncer to make requests to connection methods
func (s *Service) ProxyRequestToConnectionMethod(ctx context.Context, connMethodID, path string, r *http.Request) (*http.Response, error) {
	cm, err := s.db.GetConnectionMethod(ctx, connMethodID)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection method: %w", err)
	}
	if cm == nil {
		return nil, fmt.Errorf("connection method not found: %s", connMethodID)
	}

	// Build the full URL
	fullURL := cm.Address + path

	// Create new request with proper method, URL, and body
	req, err := http.NewRequestWithContext(ctx, r.Method, fullURL, r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Copy headers
	req.Header = r.Header.Clone()

	// Set basic auth
	req.SetBasicAuth(cm.Username, cm.Password)

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}
