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
