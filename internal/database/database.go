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

package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"shoal/pkg/crypto"
	"shoal/pkg/models"

	_ "modernc.org/sqlite"
)

// DB wraps the database connection and provides methods for data access
type DB struct {
	conn      *sql.DB
	encryptor *crypto.Encryptor
}

// New creates a new database connection without encryption
func New(dbPath string) (*DB, error) {
	return NewWithEncryption(dbPath, "")
}

// NewWithEncryption creates a new database connection with optional encryption
func NewWithEncryption(dbPath string, encryptionKey string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Initialize encryptor if key is provided
	var encryptor *crypto.Encryptor
	if encryptionKey != "" {
		encryptor, err = crypto.NewEncryptor(encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize encryptor: %w", err)
		}
		slog.Info("Password encryption enabled")
	} else {
		slog.Warn("Password encryption disabled - passwords will be stored in plaintext")
	}

	return &DB{
		conn:      conn,
		encryptor: encryptor,
	}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// encryptIfConfigured encrypts plaintext when an encryptor is configured.
// If the value appears already encrypted or encryption is not configured,
// it returns the input unchanged.
func (db *DB) encryptIfConfigured(plaintext string) (string, error) {
	// No encryption configured or empty plaintext: return as-is
	if db.encryptor == nil || plaintext == "" {
		return plaintext, nil
	}
	// Avoid double-encryption
	if crypto.IsEncrypted(plaintext) {
		return plaintext, nil
	}
	encrypted, err := db.encryptor.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return encrypted, nil
}

// decryptIfNeeded attempts to decrypt an encrypted value when an encryptor
// is configured. On failure or when not encrypted, it returns the input.
func (db *DB) decryptIfNeeded(s string) string {
	if db.encryptor == nil || s == "" || !crypto.IsEncrypted(s) {
		return s
	}
	decrypted, err := db.encryptor.Decrypt(s)
	if err != nil {
		slog.Error("Failed to decrypt value", "error", err)
		return s
	}
	return decrypted
}

// Migrate runs database migrations
func (db *DB) Migrate(ctx context.Context) error {
	slog.Info("Running database migrations")

	migrations := []string{
		// Key/value settings store for service-level configuration (e.g., service UUID)
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS bmcs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			address TEXT NOT NULL,
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			description TEXT,
			enabled BOOLEAN DEFAULT true,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT DEFAULT 'user',
			enabled BOOLEAN DEFAULT true,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bmcs_enabled ON bmcs(enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS connection_methods (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			connection_type TEXT NOT NULL DEFAULT 'Redfish',
			address TEXT NOT NULL,
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			enabled BOOLEAN DEFAULT true,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen DATETIME,
			aggregated_managers TEXT,
			aggregated_systems TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_connection_methods_enabled ON connection_methods(enabled)`,
		// Settings persistence
		`CREATE TABLE IF NOT EXISTS settings_descriptors (
			id TEXT PRIMARY KEY,
			bmc_id INTEGER NOT NULL,
			resource_path TEXT NOT NULL,
			attribute TEXT NOT NULL,
			display_name TEXT,
			description TEXT,
			type TEXT NOT NULL,
			enum_values TEXT,
			min REAL,
			max REAL,
			pattern TEXT,
			units TEXT,
			read_only BOOLEAN DEFAULT false,
			oem BOOLEAN DEFAULT false,
			oem_vendor TEXT,
			apply_times TEXT,
			action_target TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (bmc_id) REFERENCES bmcs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS settings_values (
			descriptor_id TEXT PRIMARY KEY,
			current_value TEXT,
			source_timestamp TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (descriptor_id) REFERENCES settings_descriptors(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_settings_by_bmc_resource ON settings_descriptors(bmc_id, resource_path)`,
		`CREATE INDEX IF NOT EXISTS idx_settings_by_bmc ON settings_descriptors(bmc_id)`,
		// Virtual media resources
		`CREATE TABLE IF NOT EXISTS virtual_media_resources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			connection_method_id TEXT NOT NULL,
			manager_id TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			odata_id TEXT NOT NULL,
			media_types TEXT,
			supported_protocols TEXT,
			current_image_url TEXT,
			current_image_name TEXT,
			is_inserted BOOLEAN DEFAULT 0,
			is_write_protected BOOLEAN,
			connected_via TEXT,
			last_updated DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(connection_method_id) REFERENCES connection_methods(id) ON DELETE CASCADE,
			UNIQUE(connection_method_id, manager_id, resource_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vmr_connection ON virtual_media_resources(connection_method_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vmr_manager ON virtual_media_resources(manager_id)`,
		// Virtual media operations tracking
		`CREATE TABLE IF NOT EXISTS virtual_media_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			virtual_media_resource_id INTEGER NOT NULL,
			operation TEXT NOT NULL,
			image_url TEXT,
			requested_by TEXT,
			requested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT DEFAULT 'pending',
			error_message TEXT,
			completed_at DATETIME,
			FOREIGN KEY(virtual_media_resource_id) REFERENCES virtual_media_resources(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vmo_resource ON virtual_media_operations(virtual_media_resource_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vmo_status ON virtual_media_operations(status)`,
		// Provisioning templates for kickstart/preseed configurations
		`CREATE TABLE IF NOT EXISTS provisioning_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			system_id TEXT NOT NULL,
			template_type TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(system_id, template_type)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_provisioning_system ON provisioning_templates(system_id)`,
		`CREATE INDEX IF NOT EXISTS idx_provisioning_type ON provisioning_templates(template_type)`,
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, migration := range migrations {
		if _, err := tx.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("failed to execute migration: %w", err)
		}
	}

	return tx.Commit()
}

// Settings helpers

// GetSetting retrieves a value by key from the settings table.
func (db *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := db.conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get setting %s: %w", key, err)
	}
	return value, nil
}

// SetSetting upserts a key/value pair in the settings table.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.conn.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set setting %s: %w", key, err)
	}
	return nil
}

// EnsureServiceUUID returns a stable service UUID, creating and persisting one if missing.
func (db *DB) EnsureServiceUUID(ctx context.Context) (string, error) {
	const key = "service_uuid"
	val, err := db.GetSetting(ctx, key)
	if err != nil {
		return "", err
	}
	if val != "" {
		return val, nil
	}
	// Generate a new UUID (v4-like) using crypto/rand hex
	b := make([]byte, 16)
	if _, err := crypto_randRead(b); err != nil {
		return "", fmt.Errorf("failed to generate service uuid: %w", err)
	}
	// Format as 8-4-4-4-12 hex segments
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	if err := db.SetSetting(ctx, key, uuid); err != nil {
		return "", err
	}
	return uuid, nil
}

// crypto_randRead wraps crypto/rand.Read for testability.
func crypto_randRead(b []byte) (int, error) { return rand.Read(b) }

// Settings operations

// UpsertSettingDescriptors stores or updates descriptors and current values for a BMC
func (db *DB) UpsertSettingDescriptors(ctx context.Context, bmcName string, descs []models.SettingDescriptor) error {
	if len(descs) == 0 {
		return nil
	}

	bmc, err := db.GetBMCByName(ctx, bmcName)
	if err != nil {
		return fmt.Errorf("failed to get BMC by name: %w", err)
	}
	if bmc == nil {
		return fmt.Errorf("BMC not found: %s", bmcName)
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	descStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO settings_descriptors (
			id, bmc_id, resource_path, attribute, display_name, description, type,
			enum_values, min, max, pattern, units, read_only, oem, oem_vendor,
			apply_times, action_target, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP
		)
		ON CONFLICT(id) DO UPDATE SET
			bmc_id=excluded.bmc_id,
			resource_path=excluded.resource_path,
			attribute=excluded.attribute,
			display_name=excluded.display_name,
			description=excluded.description,
			type=excluded.type,
			enum_values=excluded.enum_values,
			min=excluded.min,
			max=excluded.max,
			pattern=excluded.pattern,
			units=excluded.units,
			read_only=excluded.read_only,
			oem=excluded.oem,
			oem_vendor=excluded.oem_vendor,
			apply_times=excluded.apply_times,
			action_target=excluded.action_target,
			updated_at=CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare descriptor stmt failed: %w", err)
	}
	defer func() { _ = descStmt.Close() }()
	valStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO settings_values (descriptor_id, current_value, source_timestamp, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(descriptor_id) DO UPDATE SET
			current_value=excluded.current_value,
			source_timestamp=excluded.source_timestamp,
			updated_at=CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare values stmt failed: %w", err)
	}
	defer func() { _ = valStmt.Close() }()

	for _, d := range descs {
		// Marshal arrays and current value to JSON strings for storage
		enumJSON := ""
		if len(d.EnumValues) > 0 {
			if b, err := json.Marshal(d.EnumValues); err == nil {
				enumJSON = string(b)
			}
		}
		applyJSON := ""
		if len(d.ApplyTimes) > 0 {
			if b, err := json.Marshal(d.ApplyTimes); err == nil {
				applyJSON = string(b)
			}
		}
		var minPtr, maxPtr interface{}
		if d.Min != nil {
			minPtr = *d.Min
		} else {
			minPtr = nil
		}
		if d.Max != nil {
			maxPtr = *d.Max
		} else {
			maxPtr = nil
		}

		if _, err := descStmt.ExecContext(ctx,
			d.ID, bmc.ID, d.ResourcePath, d.Attribute, d.DisplayName, d.Description, d.Type,
			enumJSON, minPtr, maxPtr, d.Pattern, d.Units, d.ReadOnly, d.OEM, d.OEMVendor,
			applyJSON, d.ActionTarget,
		); err != nil {
			return fmt.Errorf("upsert descriptor failed: %w", err)
		}

		// Value row
		var valueJSON string
		if d.CurrentValue != nil {
			if b, err := json.Marshal(d.CurrentValue); err == nil {
				valueJSON = string(b)
			}
		}
		if _, err := valStmt.ExecContext(ctx, d.ID, valueJSON, d.SourceTimeISO); err != nil {
			return fmt.Errorf("upsert value failed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	return nil
}

// GetSettingsDescriptors returns descriptors (with latest values) for a BMC, optionally filtered by resource path substring
func (db *DB) GetSettingsDescriptors(ctx context.Context, bmcName, resourceFilter string) ([]models.SettingDescriptor, error) {
	bmc, err := db.GetBMCByName(ctx, bmcName)
	if err != nil {
		return nil, fmt.Errorf("failed to get BMC by name: %w", err)
	}
	if bmc == nil {
		return nil, nil
	}

	query := `
		SELECT d.id, d.resource_path, d.attribute, d.display_name, d.description, d.type,
			   d.enum_values, d.min, d.max, d.pattern, d.units, d.read_only, d.oem, d.oem_vendor,
			   d.apply_times, d.action_target, v.current_value, v.source_timestamp
		FROM settings_descriptors d
		LEFT JOIN settings_values v ON v.descriptor_id = d.id
		WHERE d.bmc_id = ? AND (? = '' OR d.resource_path LIKE '%' || ? || '%')
		ORDER BY d.resource_path, d.attribute`

	rows, err := db.conn.QueryContext(ctx, query, bmc.ID, resourceFilter, resourceFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to query settings descriptors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []models.SettingDescriptor
	for rows.Next() {
		var (
			id, resourcePath, attribute, displayName, description, typ, enumJSON, pattern, units, oemVendor, applyJSON, actionTarget string
			readOnly, oem                                                                                                            bool
			minNull, maxNull                                                                                                         sql.NullFloat64
			curValJSON, sourceTS                                                                                                     sql.NullString
		)
		if err := rows.Scan(&id, &resourcePath, &attribute, &displayName, &description, &typ,
			&enumJSON, &minNull, &maxNull, &pattern, &units, &readOnly, &oem, &oemVendor,
			&applyJSON, &actionTarget, &curValJSON, &sourceTS); err != nil {
			return nil, fmt.Errorf("failed to scan settings descriptor: %w", err)
		}

		var enumVals []string
		if enumJSON != "" {
			_ = json.Unmarshal([]byte(enumJSON), &enumVals)
		}
		var applyVals []string
		if applyJSON != "" {
			_ = json.Unmarshal([]byte(applyJSON), &applyVals)
		}
		var minPtr, maxPtr *float64
		if minNull.Valid {
			v := minNull.Float64
			minPtr = &v
		}
		if maxNull.Valid {
			v := maxNull.Float64
			maxPtr = &v
		}

		var current interface{}
		if curValJSON.Valid && curValJSON.String != "" {
			_ = json.Unmarshal([]byte(curValJSON.String), &current)
		}
		source := ""
		if sourceTS.Valid {
			source = sourceTS.String
		}

		result = append(result, models.SettingDescriptor{
			ID:            id,
			BMCName:       bmcName,
			ResourcePath:  resourcePath,
			Attribute:     attribute,
			DisplayName:   displayName,
			Description:   description,
			Type:          typ,
			EnumValues:    enumVals,
			Min:           minPtr,
			Max:           maxPtr,
			Pattern:       pattern,
			Units:         units,
			ReadOnly:      readOnly,
			OEM:           oem,
			OEMVendor:     oemVendor,
			ApplyTimes:    applyVals,
			ActionTarget:  actionTarget,
			CurrentValue:  current,
			SourceTimeISO: source,
		})
	}
	return result, rows.Err()
}

// GetSettingDescriptor returns a single descriptor by id for a BMC
func (db *DB) GetSettingDescriptor(ctx context.Context, bmcName, descriptorID string) (*models.SettingDescriptor, error) {
	bmc, err := db.GetBMCByName(ctx, bmcName)
	if err != nil {
		return nil, fmt.Errorf("failed to get BMC by name: %w", err)
	}
	if bmc == nil {
		return nil, nil
	}

	query := `
		SELECT d.id, d.resource_path, d.attribute, d.display_name, d.description, d.type,
			   d.enum_values, d.min, d.max, d.pattern, d.units, d.read_only, d.oem, d.oem_vendor,
			   d.apply_times, d.action_target, v.current_value, v.source_timestamp
		FROM settings_descriptors d
		LEFT JOIN settings_values v ON v.descriptor_id = d.id
		WHERE d.bmc_id = ? AND d.id = ?`

	var (
		id, resourcePath, attribute, displayName, description, typ, enumJSON, pattern, units, oemVendor, applyJSON, actionTarget string
		readOnly, oem                                                                                                            bool
		minNull, maxNull                                                                                                         sql.NullFloat64
		curValJSON, sourceTS                                                                                                     sql.NullString
	)
	err = db.conn.QueryRowContext(ctx, query, bmc.ID, descriptorID).Scan(&id, &resourcePath, &attribute, &displayName, &description, &typ,
		&enumJSON, &minNull, &maxNull, &pattern, &units, &readOnly, &oem, &oemVendor,
		&applyJSON, &actionTarget, &curValJSON, &sourceTS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get settings descriptor: %w", err)
	}

	var enumVals []string
	if enumJSON != "" {
		_ = json.Unmarshal([]byte(enumJSON), &enumVals)
	}
	var applyVals []string
	if applyJSON != "" {
		_ = json.Unmarshal([]byte(applyJSON), &applyVals)
	}
	var minPtr, maxPtr *float64
	if minNull.Valid {
		v := minNull.Float64
		minPtr = &v
	}
	if maxNull.Valid {
		v := maxNull.Float64
		maxPtr = &v
	}
	var current interface{}
	if curValJSON.Valid && curValJSON.String != "" {
		_ = json.Unmarshal([]byte(curValJSON.String), &current)
	}
	source := ""
	if sourceTS.Valid {
		source = sourceTS.String
	}

	desc := &models.SettingDescriptor{
		ID:            id,
		BMCName:       bmcName,
		ResourcePath:  resourcePath,
		Attribute:     attribute,
		DisplayName:   displayName,
		Description:   description,
		Type:          typ,
		EnumValues:    enumVals,
		Min:           minPtr,
		Max:           maxPtr,
		Pattern:       pattern,
		Units:         units,
		ReadOnly:      readOnly,
		OEM:           oem,
		OEMVendor:     oemVendor,
		ApplyTimes:    applyVals,
		ActionTarget:  actionTarget,
		CurrentValue:  current,
		SourceTimeISO: source,
	}
	return desc, nil
}

// BMC operations

// GetBMCs returns all BMCs from the database
func (db *DB) GetBMCs(ctx context.Context) ([]models.BMC, error) {
	query := `SELECT id, name, address, username, password, description, enabled, created_at, updated_at, last_seen FROM bmcs ORDER BY name`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query BMCs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var bmcs []models.BMC
	for rows.Next() {
		var bmc models.BMC
		err := rows.Scan(&bmc.ID, &bmc.Name, &bmc.Address, &bmc.Username, &bmc.Password,
			&bmc.Description, &bmc.Enabled, &bmc.CreatedAt, &bmc.UpdatedAt, &bmc.LastSeen)
		if err != nil {
			return nil, fmt.Errorf("failed to scan BMC: %w", err)
		}

		// Decrypt password if needed
		bmc.Password = db.decryptIfNeeded(bmc.Password)

		bmcs = append(bmcs, bmc)
	}

	return bmcs, rows.Err()
}

// GetBMC returns a single BMC by ID
func (db *DB) GetBMC(ctx context.Context, id int64) (*models.BMC, error) {
	query := `SELECT id, name, address, username, password, description, enabled, created_at, updated_at, last_seen FROM bmcs WHERE id = ?`

	var bmc models.BMC
	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&bmc.ID, &bmc.Name, &bmc.Address, &bmc.Username, &bmc.Password,
		&bmc.Description, &bmc.Enabled, &bmc.CreatedAt, &bmc.UpdatedAt, &bmc.LastSeen)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get BMC: %w", err)
	}

	// Decrypt password if needed
	bmc.Password = db.decryptIfNeeded(bmc.Password)

	return &bmc, nil
}

// GetBMCByName returns a single BMC by name
func (db *DB) GetBMCByName(ctx context.Context, name string) (*models.BMC, error) {
	query := `SELECT id, name, address, username, password, description, enabled, created_at, updated_at, last_seen FROM bmcs WHERE name = ?`

	var bmc models.BMC
	err := db.conn.QueryRowContext(ctx, query, name).Scan(
		&bmc.ID, &bmc.Name, &bmc.Address, &bmc.Username, &bmc.Password,
		&bmc.Description, &bmc.Enabled, &bmc.CreatedAt, &bmc.UpdatedAt, &bmc.LastSeen)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get BMC by name: %w", err)
	}

	// Decrypt password if needed
	bmc.Password = db.decryptIfNeeded(bmc.Password)

	return &bmc, nil
}

// CreateBMC creates a new BMC
func (db *DB) CreateBMC(ctx context.Context, bmc *models.BMC) error {
	query := `INSERT INTO bmcs (name, address, username, password, description, enabled) VALUES (?, ?, ?, ?, ?, ?)`

	// Encrypt password if configured
	password, err := db.encryptIfConfigured(bmc.Password)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	result, err := db.conn.ExecContext(ctx, query, bmc.Name, bmc.Address, bmc.Username, password, bmc.Description, bmc.Enabled)
	if err != nil {
		return fmt.Errorf("failed to create BMC: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert ID: %w", err)
	}

	bmc.ID = id
	bmc.CreatedAt = time.Now()
	bmc.UpdatedAt = time.Now()

	return nil
}

// UpdateBMC updates an existing BMC
func (db *DB) UpdateBMC(ctx context.Context, bmc *models.BMC) error {
	query := `UPDATE bmcs SET name = ?, address = ?, username = ?, password = ?, description = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`

	// Encrypt password if configured
	password, err := db.encryptIfConfigured(bmc.Password)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	_, err = db.conn.ExecContext(ctx, query, bmc.Name, bmc.Address, bmc.Username, password, bmc.Description, bmc.Enabled, bmc.ID)
	if err != nil {
		return fmt.Errorf("failed to update BMC: %w", err)
	}

	return nil
}

// DeleteBMC deletes a BMC by ID
func (db *DB) DeleteBMC(ctx context.Context, id int64) error {
	query := `DELETE FROM bmcs WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete BMC: %w", err)
	}

	return nil
}

// UpdateBMCLastSeen updates the last_seen timestamp for a BMC
func (db *DB) UpdateBMCLastSeen(ctx context.Context, id int64) error {
	query := `UPDATE bmcs SET last_seen = CURRENT_TIMESTAMP WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to update BMC last seen: %w", err)
	}

	return nil
}

// Session operations

// CreateSession creates a new session
func (db *DB) CreateSession(ctx context.Context, session *models.Session) error {
	query := `INSERT INTO sessions (id, user_id, token, expires_at) VALUES (?, ?, ?, ?)`

	_, err := db.conn.ExecContext(ctx, query, session.ID, session.UserID, session.Token, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

// GetSessionByToken returns a session by token
func (db *DB) GetSessionByToken(ctx context.Context, token string) (*models.Session, error) {
	query := `SELECT id, user_id, token, expires_at, created_at FROM sessions WHERE token = ? AND expires_at > ?`

	var session models.Session
	err := db.conn.QueryRowContext(ctx, query, token, time.Now()).Scan(
		&session.ID, &session.UserID, &session.Token, &session.ExpiresAt, &session.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &session, nil
}

// DeleteSession deletes a session
func (db *DB) DeleteSession(ctx context.Context, token string) error {
	query := `DELETE FROM sessions WHERE token = ?`

	_, err := db.conn.ExecContext(ctx, query, token)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

// CleanupExpiredSessions removes expired sessions
// NOTE: Test-only utility currently used in tests; retained for potential future scheduled cleanup task.
func (db *DB) CleanupExpiredSessions(ctx context.Context) error {
	query := `DELETE FROM sessions WHERE expires_at <= ?`

	_, err := db.conn.ExecContext(ctx, query, time.Now())
	if err != nil {
		return fmt.Errorf("failed to cleanup expired sessions: %w", err)
	}

	return nil
}

// GetSession returns a session by ID (only active sessions)
func (db *DB) GetSession(ctx context.Context, id string) (*models.Session, error) {
	query := `SELECT id, user_id, token, expires_at, created_at FROM sessions WHERE id = ? AND expires_at > ?`

	var session models.Session
	err := db.conn.QueryRowContext(ctx, query, id, time.Now()).Scan(
		&session.ID, &session.UserID, &session.Token, &session.ExpiresAt, &session.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session by id: %w", err)
	}
	return &session, nil
}

// GetSessions returns all active sessions
func (db *DB) GetSessions(ctx context.Context) ([]models.Session, error) {
	query := `SELECT id, user_id, token, expires_at, created_at FROM sessions WHERE expires_at > ? ORDER BY created_at DESC`

	rows, err := db.conn.QueryContext(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []models.Session
	for rows.Next() {
		var s models.Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.Token, &s.ExpiresAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// DeleteSessionByID deletes a session by ID
func (db *DB) DeleteSessionByID(ctx context.Context, id string) error {
	query := `DELETE FROM sessions WHERE id = ?`
	_, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete session by id: %w", err)
	}
	return nil
}

// DisableForeignKeys disables foreign key constraints (for testing)
// NOTE: Test-only helper used by unit/integration tests to bypass FK ordering constraints; not used in production code.
func (db *DB) DisableForeignKeys() error {
	_, err := db.conn.Exec("PRAGMA foreign_keys=OFF")
	return err
}

// User operations

// GetUsers returns all users from the database
func (db *DB) GetUsers(ctx context.Context) ([]models.User, error) {
	query := `SELECT id, username, password_hash, role, enabled, created_at, updated_at FROM users ORDER BY username`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []models.User
	for rows.Next() {
		var user models.User
		err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role,
			&user.Enabled, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}

	return users, rows.Err()
}

// GetUser returns a single user by ID
func (db *DB) GetUser(ctx context.Context, id string) (*models.User, error) {
	query := `SELECT id, username, password_hash, role, enabled, created_at, updated_at FROM users WHERE id = ?`

	var user models.User
	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Role,
		&user.Enabled, &user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

// GetUserByUsername returns a single user by username
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	query := `SELECT id, username, password_hash, role, enabled, created_at, updated_at FROM users WHERE username = ?`

	var user models.User
	err := db.conn.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Role,
		&user.Enabled, &user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by username: %w", err)
	}

	return &user, nil
}

// CreateUser creates a new user
func (db *DB) CreateUser(ctx context.Context, user *models.User) error {
	query := `INSERT INTO users (id, username, password_hash, role, enabled) VALUES (?, ?, ?, ?, ?)`

	_, err := db.conn.ExecContext(ctx, query, user.ID, user.Username, user.PasswordHash, user.Role, user.Enabled)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()

	return nil
}

// UpdateUser updates an existing user
func (db *DB) UpdateUser(ctx context.Context, user *models.User) error {
	query := `UPDATE users SET username = ?, password_hash = ?, role = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, user.Username, user.PasswordHash, user.Role, user.Enabled, user.ID)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

// DeleteUser deletes a user by ID
func (db *DB) DeleteUser(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	return nil
}

// CountUsers returns the number of users in the database
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM users`

	var count int
	err := db.conn.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}

	return count, nil
}

// ConnectionMethod operations

// GetConnectionMethods returns all connection methods from the database
func (db *DB) GetConnectionMethods(ctx context.Context) ([]models.ConnectionMethod, error) {
	query := `SELECT id, name, connection_type, address, username, password, enabled, created_at, updated_at, last_seen, aggregated_managers, aggregated_systems FROM connection_methods ORDER BY name`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query connection methods: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var methods []models.ConnectionMethod
	for rows.Next() {
		var method models.ConnectionMethod
		err := rows.Scan(&method.ID, &method.Name, &method.ConnectionMethodType, &method.Address, &method.Username, &method.Password,
			&method.Enabled, &method.CreatedAt, &method.UpdatedAt, &method.LastSeen, &method.AggregatedManagers, &method.AggregatedSystems)
		if err != nil {
			return nil, fmt.Errorf("failed to scan connection method: %w", err)
		}

		// Decrypt password if needed
		method.Password = db.decryptIfNeeded(method.Password)

		methods = append(methods, method)
	}

	return methods, rows.Err()
}

// GetConnectionMethod returns a single connection method by ID
func (db *DB) GetConnectionMethod(ctx context.Context, id string) (*models.ConnectionMethod, error) {
	query := `SELECT id, name, connection_type, address, username, password, enabled, created_at, updated_at, last_seen, aggregated_managers, aggregated_systems FROM connection_methods WHERE id = ?`

	var method models.ConnectionMethod
	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&method.ID, &method.Name, &method.ConnectionMethodType, &method.Address, &method.Username, &method.Password,
		&method.Enabled, &method.CreatedAt, &method.UpdatedAt, &method.LastSeen, &method.AggregatedManagers, &method.AggregatedSystems)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get connection method: %w", err)
	}

	// Decrypt password if needed
	method.Password = db.decryptIfNeeded(method.Password)

	return &method, nil
}

// CreateConnectionMethod creates a new connection method
func (db *DB) CreateConnectionMethod(ctx context.Context, method *models.ConnectionMethod) error {
	// Encrypt password if configured
	password, err := db.encryptIfConfigured(method.Password)
	if err != nil {
		return fmt.Errorf("failed to encrypt connection method password: %w", err)
	}

	query := `INSERT INTO connection_methods (id, name, connection_type, address, username, password, enabled, aggregated_managers, aggregated_systems) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = db.conn.ExecContext(ctx, query, method.ID, method.Name, method.ConnectionMethodType, method.Address, method.Username, password, method.Enabled, method.AggregatedManagers, method.AggregatedSystems)
	if err != nil {
		return fmt.Errorf("failed to create connection method: %w", err)
	}

	method.CreatedAt = time.Now()
	method.UpdatedAt = time.Now()

	return nil
}

// UpdateConnectionMethodAggregatedData updates the cached aggregated data for a connection method
// NOTE: Currently only exercised in tests; retained to support future aggregation caching logic.
func (db *DB) UpdateConnectionMethodAggregatedData(ctx context.Context, id string, managers, systems string) error {
	query := `UPDATE connection_methods SET aggregated_managers = ?, aggregated_systems = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, managers, systems, id)
	if err != nil {
		return fmt.Errorf("failed to update connection method aggregated data: %w", err)
	}

	return nil
}

// DeleteConnectionMethod deletes a connection method by ID
func (db *DB) DeleteConnectionMethod(ctx context.Context, id string) error {
	query := `DELETE FROM connection_methods WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete connection method: %w", err)
	}

	return nil
}

// UpdateConnectionMethodLastSeen updates the last seen timestamp
// NOTE: Currently only exercised in tests; retained for prospective connection method monitoring.
func (db *DB) UpdateConnectionMethodLastSeen(ctx context.Context, id string) error {
	query := `UPDATE connection_methods SET last_seen = CURRENT_TIMESTAMP WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to update connection method last seen: %w", err)
	}

	return nil
}

// Profile operations removed in Design 014

// VirtualMedia operations

// VirtualMediaResource represents a cached virtual media resource
type VirtualMediaResource struct {
	ID                 int64
	ConnectionMethodID string
	ManagerID          string
	ResourceID         string
	ODataID            string
	MediaTypes         string // JSON array
	SupportedProtocols string // JSON array
	CurrentImageURL    string
	CurrentImageName   string
	IsInserted         bool
	IsWriteProtected   bool
	ConnectedVia       string
	LastUpdated        time.Time
}

// GetVirtualMediaResources returns all virtual media resources for a specific manager
func (db *DB) GetVirtualMediaResources(ctx context.Context, connectionMethodID, managerID string) ([]VirtualMediaResource, error) {
	query := `SELECT id, connection_method_id, manager_id, resource_id, odata_id, 
		media_types, supported_protocols, current_image_url, current_image_name, 
		is_inserted, is_write_protected, connected_via, last_updated 
		FROM virtual_media_resources 
		WHERE connection_method_id = ? AND manager_id = ?
		ORDER BY resource_id`

	rows, err := db.conn.QueryContext(ctx, query, connectionMethodID, managerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query virtual media resources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var resources []VirtualMediaResource
	for rows.Next() {
		var r VirtualMediaResource
		var mediaTypes, supportedProtocols sql.NullString
		var currentImageURL, currentImageName sql.NullString
		var isWriteProtected sql.NullBool
		var connectedVia sql.NullString

		err := rows.Scan(&r.ID, &r.ConnectionMethodID, &r.ManagerID, &r.ResourceID, &r.ODataID,
			&mediaTypes, &supportedProtocols, &currentImageURL, &currentImageName,
			&r.IsInserted, &isWriteProtected, &connectedVia, &r.LastUpdated)
		if err != nil {
			return nil, fmt.Errorf("failed to scan virtual media resource: %w", err)
		}

		if mediaTypes.Valid {
			r.MediaTypes = mediaTypes.String
		}
		if supportedProtocols.Valid {
			r.SupportedProtocols = supportedProtocols.String
		}
		if currentImageURL.Valid {
			r.CurrentImageURL = currentImageURL.String
		}
		if currentImageName.Valid {
			r.CurrentImageName = currentImageName.String
		}
		if isWriteProtected.Valid {
			r.IsWriteProtected = isWriteProtected.Bool
		}
		if connectedVia.Valid {
			r.ConnectedVia = connectedVia.String
		}

		resources = append(resources, r)
	}

	return resources, rows.Err()
}

// GetVirtualMediaResource returns a specific virtual media resource
func (db *DB) GetVirtualMediaResource(ctx context.Context, connectionMethodID, managerID, resourceID string) (*VirtualMediaResource, error) {
	query := `SELECT id, connection_method_id, manager_id, resource_id, odata_id, 
		media_types, supported_protocols, current_image_url, current_image_name, 
		is_inserted, is_write_protected, connected_via, last_updated 
		FROM virtual_media_resources 
		WHERE connection_method_id = ? AND manager_id = ? AND resource_id = ?`

	var r VirtualMediaResource
	var mediaTypes, supportedProtocols sql.NullString
	var currentImageURL, currentImageName sql.NullString
	var isWriteProtected sql.NullBool
	var connectedVia sql.NullString

	err := db.conn.QueryRowContext(ctx, query, connectionMethodID, managerID, resourceID).Scan(
		&r.ID, &r.ConnectionMethodID, &r.ManagerID, &r.ResourceID, &r.ODataID,
		&mediaTypes, &supportedProtocols, &currentImageURL, &currentImageName,
		&r.IsInserted, &isWriteProtected, &connectedVia, &r.LastUpdated)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual media resource: %w", err)
	}

	if mediaTypes.Valid {
		r.MediaTypes = mediaTypes.String
	}
	if supportedProtocols.Valid {
		r.SupportedProtocols = supportedProtocols.String
	}
	if currentImageURL.Valid {
		r.CurrentImageURL = currentImageURL.String
	}
	if currentImageName.Valid {
		r.CurrentImageName = currentImageName.String
	}
	if isWriteProtected.Valid {
		r.IsWriteProtected = isWriteProtected.Bool
	}
	if connectedVia.Valid {
		r.ConnectedVia = connectedVia.String
	}

	return &r, nil
}

// UpsertVirtualMediaResource inserts or updates a virtual media resource
func (db *DB) UpsertVirtualMediaResource(ctx context.Context, connectionMethodID, managerID, resourceID, odataID, mediaTypes, supportedProtocols string, currentImageURL, currentImageName *string, isInserted, isWriteProtected bool, connectedVia string) error {
	query := `INSERT INTO virtual_media_resources 
		(connection_method_id, manager_id, resource_id, odata_id, media_types, supported_protocols, 
		 current_image_url, current_image_name, is_inserted, is_write_protected, connected_via, last_updated) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(connection_method_id, manager_id, resource_id) DO UPDATE SET
			odata_id=excluded.odata_id,
			media_types=excluded.media_types,
			supported_protocols=excluded.supported_protocols,
			current_image_url=excluded.current_image_url,
			current_image_name=excluded.current_image_name,
			is_inserted=excluded.is_inserted,
			is_write_protected=excluded.is_write_protected,
			connected_via=excluded.connected_via,
			last_updated=CURRENT_TIMESTAMP`

	var imgURL, imgName interface{}
	if currentImageURL != nil {
		imgURL = *currentImageURL
	}
	if currentImageName != nil {
		imgName = *currentImageName
	}

	_, err := db.conn.ExecContext(ctx, query, connectionMethodID, managerID, resourceID, odataID,
		mediaTypes, supportedProtocols, imgURL, imgName, isInserted, isWriteProtected, connectedVia)
	if err != nil {
		return fmt.Errorf("failed to upsert virtual media resource: %w", err)
	}

	return nil
}

// VirtualMediaOperation represents an insert/eject operation on virtual media
type VirtualMediaOperation struct {
	ID                     int64
	VirtualMediaResourceID int64
	Operation              string // 'insert' or 'eject'
	ImageURL               string
	RequestedBy            string
	RequestedAt            time.Time
	Status                 string // 'pending', 'success', 'failed'
	ErrorMessage           string
	CompletedAt            *time.Time
}

// CreateVirtualMediaOperation records a new virtual media operation
func (db *DB) CreateVirtualMediaOperation(ctx context.Context, op *VirtualMediaOperation) error {
	query := `INSERT INTO virtual_media_operations 
		(virtual_media_resource_id, operation, image_url, requested_by, status, error_message) 
		VALUES (?, ?, ?, ?, ?, ?)`

	result, err := db.conn.ExecContext(ctx, query, op.VirtualMediaResourceID, op.Operation,
		op.ImageURL, op.RequestedBy, op.Status, op.ErrorMessage)
	if err != nil {
		return fmt.Errorf("failed to create virtual media operation: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert ID: %w", err)
	}

	op.ID = id
	op.RequestedAt = time.Now()

	return nil
}

// GetVirtualMediaOperation returns a specific operation by ID
func (db *DB) GetVirtualMediaOperation(ctx context.Context, id int64) (*VirtualMediaOperation, error) {
	query := `SELECT id, virtual_media_resource_id, operation, image_url, requested_by, 
		requested_at, status, error_message, completed_at 
		FROM virtual_media_operations WHERE id = ?`

	var op VirtualMediaOperation
	var imageURL, requestedBy, errorMessage sql.NullString
	var completedAt sql.NullTime

	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&op.ID, &op.VirtualMediaResourceID, &op.Operation, &imageURL, &requestedBy,
		&op.RequestedAt, &op.Status, &errorMessage, &completedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual media operation: %w", err)
	}

	if imageURL.Valid {
		op.ImageURL = imageURL.String
	}
	if requestedBy.Valid {
		op.RequestedBy = requestedBy.String
	}
	if errorMessage.Valid {
		op.ErrorMessage = errorMessage.String
	}
	if completedAt.Valid {
		op.CompletedAt = &completedAt.Time
	}

	return &op, nil
}

// GetVirtualMediaOperations returns all operations for a specific virtual media resource
func (db *DB) GetVirtualMediaOperations(ctx context.Context, resourceID int64) ([]VirtualMediaOperation, error) {
	query := `SELECT id, virtual_media_resource_id, operation, image_url, requested_by, 
		requested_at, status, error_message, completed_at 
		FROM virtual_media_operations 
		WHERE virtual_media_resource_id = ?
		ORDER BY requested_at DESC`

	rows, err := db.conn.QueryContext(ctx, query, resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query virtual media operations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var operations []VirtualMediaOperation
	for rows.Next() {
		var op VirtualMediaOperation
		var imageURL, requestedBy, errorMessage sql.NullString
		var completedAt sql.NullTime

		err := rows.Scan(&op.ID, &op.VirtualMediaResourceID, &op.Operation, &imageURL, &requestedBy,
			&op.RequestedAt, &op.Status, &errorMessage, &completedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan virtual media operation: %w", err)
		}

		if imageURL.Valid {
			op.ImageURL = imageURL.String
		}
		if requestedBy.Valid {
			op.RequestedBy = requestedBy.String
		}
		if errorMessage.Valid {
			op.ErrorMessage = errorMessage.String
		}
		if completedAt.Valid {
			op.CompletedAt = &completedAt.Time
		}

		operations = append(operations, op)
	}

	return operations, rows.Err()
}

// UpdateVirtualMediaOperationStatus updates the status of an operation
func (db *DB) UpdateVirtualMediaOperationStatus(ctx context.Context, id int64, status, errorMessage string) error {
	query := `UPDATE virtual_media_operations 
		SET status = ?, error_message = ?, completed_at = CURRENT_TIMESTAMP 
		WHERE id = ?`

	_, err := db.conn.ExecContext(ctx, query, status, errorMessage, id)
	if err != nil {
		return fmt.Errorf("failed to update virtual media operation status: %w", err)
	}

	return nil
}

// ProvisioningTemplate represents a stored provisioning configuration
type ProvisioningTemplate struct {
	ID           int64     `db:"id"`
	SystemID     string    `db:"system_id"`
	TemplateType string    `db:"template_type"` // "kickstart" or "preseed"
	Content      string    `db:"content"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// GetProvisioningTemplate retrieves a provisioning template by system ID and type
func (db *DB) GetProvisioningTemplate(ctx context.Context, systemID, templateType string) (*ProvisioningTemplate, error) {
	query := `SELECT id, system_id, template_type, content, created_at, updated_at 
		FROM provisioning_templates 
		WHERE system_id = ? AND template_type = ?`

	var template ProvisioningTemplate
	err := db.conn.QueryRowContext(ctx, query, systemID, templateType).Scan(
		&template.ID,
		&template.SystemID,
		&template.TemplateType,
		&template.Content,
		&template.CreatedAt,
		&template.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get provisioning template: %w", err)
	}

	return &template, nil
}

// UpsertProvisioningTemplate creates or updates a provisioning template
func (db *DB) UpsertProvisioningTemplate(ctx context.Context, systemID, templateType, content string) error {
	query := `INSERT INTO provisioning_templates (system_id, template_type, content, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(system_id, template_type) DO UPDATE SET 
			content = excluded.content,
			updated_at = CURRENT_TIMESTAMP`

	_, err := db.conn.ExecContext(ctx, query, systemID, templateType, content)
	if err != nil {
		return fmt.Errorf("failed to upsert provisioning template: %w", err)
	}

	return nil
}

// DeleteProvisioningTemplate deletes a provisioning template by system ID
func (db *DB) DeleteProvisioningTemplate(ctx context.Context, systemID string) error {
	query := `DELETE FROM provisioning_templates WHERE system_id = ?`
	_, err := db.conn.ExecContext(ctx, query, systemID)
	if err != nil {
		return fmt.Errorf("failed to delete provisioning template: %w", err)
	}
	return nil
}
