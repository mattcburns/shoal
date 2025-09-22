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

package models

import (
	"time"
)

// BMC represents a managed Baseboard Management Controller
type BMC struct {
	ID          int64      `json:"id" db:"id"`
	Name        string     `json:"name" db:"name"`
	Address     string     `json:"address" db:"address"`
	Username    string     `json:"username" db:"username"`
	Password    string     `json:"-" db:"password"` // Never expose password in JSON
	Description string     `json:"description" db:"description"`
	Enabled     bool       `json:"enabled" db:"enabled"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	LastSeen    *time.Time `json:"last_seen,omitempty" db:"last_seen"`
}

// Session represents an authentication session for the aggregator API
type Session struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	Token     string    `json:"token" db:"token"`
	ExpiresAt time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// User represents a user of the aggregator service
type User struct {
	ID           string    `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	PasswordHash string    `json:"-" db:"password_hash"` // Never expose password hash
	Role         string    `json:"role" db:"role"`
	Enabled      bool      `json:"enabled" db:"enabled"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// User roles
const (
	RoleAdmin    = "admin"    // Full access to everything
	RoleOperator = "operator" // Can manage BMCs but not users
	RoleViewer   = "viewer"   // Read-only access
)

// PowerAction represents a power control action
type PowerAction string

const (
	PowerActionOn               PowerAction = "On"
	PowerActionForceOff         PowerAction = "ForceOff"
	PowerActionGracefulShutdown PowerAction = "GracefulShutdown"
	PowerActionForceRestart     PowerAction = "ForceRestart"
	PowerActionGracefulRestart  PowerAction = "GracefulRestart"
	PowerActionNmi              PowerAction = "Nmi"
)

// PowerRequest represents a power control request
type PowerRequest struct {
	ResetType PowerAction `json:"ResetType"`
}

// ConnectionMethod represents an aggregation connection to an external BMC
type ConnectionMethod struct {
	ID                   string     `json:"id" db:"id"`
	Name                 string     `json:"Name" db:"name"`
	ConnectionMethodType string     `json:"ConnectionMethodType" db:"connection_type"`
	Address              string     `json:"ConnectionMethodVariant.Address" db:"address"`
	Username             string     `json:"-" db:"username"`
	Password             string     `json:"-" db:"password"`
	Enabled              bool       `json:"Enabled" db:"enabled"`
	CreatedAt            time.Time  `json:"CreatedAt" db:"created_at"`
	UpdatedAt            time.Time  `json:"UpdatedAt" db:"updated_at"`
	LastSeen             *time.Time `json:"LastSeen,omitempty" db:"last_seen"`
	// Cached aggregated data (stored as JSON in database)
	AggregatedManagers string `json:"-" db:"aggregated_managers"`
	AggregatedSystems  string `json:"-" db:"aggregated_systems"`
}

// DetailedBMCStatus represents comprehensive information about a BMC
type DetailedBMCStatus struct {
	BMC               BMC                `json:"bmc"`
	SystemInfo        *SystemInfo        `json:"system_info,omitempty"`
	NetworkInterfaces []NetworkInterface `json:"network_interfaces,omitempty"`
	StorageDevices    []StorageDevice    `json:"storage_devices,omitempty"`
	SELEntries        []SELEntry         `json:"sel_entries,omitempty"`
}

// SystemInfo represents basic system information
type SystemInfo struct {
	SerialNumber string `json:"serial_number,omitempty"`
	SKU          string `json:"sku,omitempty"`
	PowerState   string `json:"power_state,omitempty"`
	Model        string `json:"model,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
}

// NetworkInterface represents a network interface
type NetworkInterface struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	MACAddress  string   `json:"mac_address,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
}

// StorageDevice represents a storage device
type StorageDevice struct {
	Name          string `json:"name,omitempty"`
	Model         string `json:"model,omitempty"`
	SerialNumber  string `json:"serial_number,omitempty"`
	CapacityBytes int64  `json:"capacity_bytes,omitempty"`
	Status        string `json:"status,omitempty"`
	MediaType     string `json:"media_type,omitempty"`
}

// SELEntry represents a System Event Log entry
type SELEntry struct {
	ID        string `json:"id,omitempty"`
	Message   string `json:"message,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Created   string `json:"created,omitempty"`
	EntryType string `json:"entry_type,omitempty"`
}

// SettingDescriptor describes a configurable setting discovered via Redfish
type SettingDescriptor struct {
	ID            string      `json:"id"`
	BMCName       string      `json:"bmc_name"`
	ResourcePath  string      `json:"resource_path"`
	Attribute     string      `json:"attribute"`
	DisplayName   string      `json:"display_name,omitempty"`
	Description   string      `json:"description,omitempty"`
	Type          string      `json:"type"`
	EnumValues    []string    `json:"enum_values,omitempty"`
	Min           *float64    `json:"min,omitempty"`
	Max           *float64    `json:"max,omitempty"`
	Pattern       string      `json:"pattern,omitempty"`
	Units         string      `json:"units,omitempty"`
	ReadOnly      bool        `json:"read_only"`
	OEM           bool        `json:"oem"`
	OEMVendor     string      `json:"oem_vendor,omitempty"`
	ApplyTimes    []string    `json:"apply_times,omitempty"`
	ActionTarget  string      `json:"action_target,omitempty"`
	CurrentValue  interface{} `json:"current_value,omitempty"`
	SourceTimeISO string      `json:"source_timestamp,omitempty"`
}

// SettingsResponse is returned by the settings API
type SettingsResponse struct {
	BMCName     string              `json:"bmc_name"`
	Resource    string              `json:"resource,omitempty"`
	Descriptors []SettingDescriptor `json:"descriptors"`
	// Pagination metadata (007)
	Page     int `json:"page,omitempty"`
	PageSize int `json:"page_size,omitempty"`
	Total    int `json:"total,omitempty"`
}

// SettingValue stores current value snapshot for a descriptor
type SettingValue struct {
	DescriptorID    string      `json:"descriptor_id" db:"descriptor_id"`
	CurrentValue    interface{} `json:"current_value" db:"current_value"`
	SourceTimestamp string      `json:"source_timestamp" db:"source_timestamp"`
}

// Profiles (005)

// Profile represents a reusable set of desired settings
type Profile struct {
	ID                 string    `json:"id" db:"id"`
	Name               string    `json:"name" db:"name"`
	Description        string    `json:"description,omitempty" db:"description"`
	CreatedBy          string    `json:"created_by,omitempty" db:"created_by"`
	HardwareSelector   string    `json:"hardware_selector,omitempty" db:"hardware_selector"`
	FirmwareRangesJSON string    `json:"firmware_ranges_json,omitempty" db:"firmware_ranges_json"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

// ProfileVersion is a versioned collection of entries
type ProfileVersion struct {
	ID        string         `json:"id" db:"id"`
	ProfileID string         `json:"profile_id" db:"profile_id"`
	Version   int            `json:"version" db:"version"`
	CreatedAt time.Time      `json:"created_at" db:"created_at"`
	Notes     string         `json:"notes,omitempty" db:"notes"`
	Entries   []ProfileEntry `json:"entries,omitempty"`
}

// ProfileEntry specifies a desired value for a setting
type ProfileEntry struct {
	ID                  string      `json:"id" db:"id"`
	ProfileVersionID    string      `json:"profile_version_id" db:"profile_version_id"`
	ResourcePath        string      `json:"resource_path" db:"resource_path"`
	Attribute           string      `json:"attribute" db:"attribute"`
	DesiredValue        interface{} `json:"desired_value,omitempty"`
	ApplyTimePreference string      `json:"apply_time_preference,omitempty" db:"apply_time_preference"`
	OEMVendor           string      `json:"oem_vendor,omitempty" db:"oem_vendor"`
}

// ProfileAssignment binds a profile/version to a target
type ProfileAssignment struct {
	ID          string    `json:"id" db:"id"`
	ProfileID   string    `json:"profile_id" db:"profile_id"`
	Version     int       `json:"version" db:"version"`
	TargetType  string    `json:"target_type" db:"target_type"`   // e.g., "bmc" or "group"
	TargetValue string    `json:"target_value" db:"target_value"` // e.g., BMC name or group name
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// AuditRecord captures an auditable operation performed by the aggregator
type AuditRecord struct {
	ID           string    `json:"id" db:"id"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UserID       string    `json:"user_id,omitempty" db:"user_id"`
	UserName     string    `json:"user_name,omitempty" db:"user_name"`
	BMCName      string    `json:"bmc_name,omitempty" db:"bmc_name"`
	Action       string    `json:"action" db:"action"`           // e.g., "proxy", "power", "apply_profile"
	Method       string    `json:"method,omitempty" db:"method"` // HTTP verb for proxy operations
	Path         string    `json:"path,omitempty" db:"path"`
	StatusCode   int       `json:"status_code" db:"status_code"`
	DurationMS   int64     `json:"duration_ms" db:"duration_ms"`
	RequestBody  string    `json:"request_body,omitempty" db:"request_body"`
	ResponseBody string    `json:"response_body,omitempty" db:"response_body"`
	Error        string    `json:"error,omitempty" db:"error"`
}
