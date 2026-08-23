// Package redfish wraps gofish behind Shoal BMC interfaces.
// gofish types must not leak outside this package.
package redfish

import "time"

// Config configures a BMC connection.
type Config struct {
	BaseURL        string
	Username       string
	Password       string // never logged
	AuthMode       string // "basic" | "session" — lab default basic
	TLSMode        string // "off" | "insecure" | "custom_ca"
	CAFile         string // when TLSMode=custom_ca
	MaxConcurrent  int    // default 1–2
	RequestTimeout time.Duration
}

// ServiceRoot is a Shoal-domain view of the Redfish service root.
type ServiceRoot struct {
	Name           string
	RedfishVersion string
	UUID           string
	SystemsURL     string
	ManagersURL    string
}

// SystemInfo is a ComputerSystem snapshot used by Deploy/Discover.
type SystemInfo struct {
	ID           string
	Name         string
	UUID         string
	Serial       string
	Model        string
	Manufacturer string
	PowerState   string
	// ODataID is the system resource URI (for subsequent operations).
	ODataID string
}

// BootInfo is the current boot override state.
type BootInfo struct {
	OverrideEnabled string // Disabled | Once | Continuous
	OverrideTarget  string // None | Cd | Pxe | ...
}

// VirtualMedia describes a virtual media slot.
type VirtualMedia struct {
	// URI is the @odata.id used for Insert/Eject.
	URI        string
	Name       string
	ID         string
	Image      string
	Inserted   bool
	MediaTypes []string
	SupportsCD bool
}

// SELOptions filters ListSEL results.
type SELOptions struct {
	// MaxEntries caps returned entries (0 = default 200).
	MaxEntries int
	// Since, when non-zero, keeps entries at or after this time (Created/EventTimestamp).
	Since time.Time
}

// SELEntry is a Shoal-domain log/SEL record (no gofish types).
type SELEntry struct {
	ID         string // Log entry ID
	Message    string
	Severity   string    // OK | Warning | Critical | …
	EntryType  string    // Event | SEL | Oem
	Created    time.Time // zero if unknown
	SensorType string
	SensorNum  int // 0 when unset / not SEL
	ODataID    string
	LogService string // parent log service name or URI fragment
}

// FirmwareComponent is one Redfish SoftwareInventory / FirmwareInventory item.
type FirmwareComponent struct {
	ID           string
	Name         string
	Version      string
	SoftwareID   string
	Manufacturer string
	ReleaseDate  string
	Health       string
	State        string
	Updateable   bool
}

// SensorSample is one thermal/power sensor reading from Redfish.
type SensorSample struct {
	Name            string
	Reading         float64
	HasReading      bool   // false when Redfish omitted Reading (JSON null)
	Note            string // why HasReading is false, for operator UI
	Units           string
	PhysicalContext string
	Status          string // Health / State summary when available
	Kind            string // "temperature" | "fan" | "voltage" | "power" | …
}
