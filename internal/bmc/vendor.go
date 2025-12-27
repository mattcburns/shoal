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
	"encoding/json"
	"strings"
)

// VendorType represents a BMC vendor
type VendorType string

const (
	VendorDell       VendorType = "Dell"
	VendorSupermicro VendorType = "Supermicro"
	VendorHPE        VendorType = "HPE"
	VendorUnknown    VendorType = "Unknown"
)

// VendorCapability represents vendor-specific console capabilities
type VendorCapability struct {
	Vendor                   VendorType              `json:"vendor"`
	Model                    string                  `json:"model,omitempty"`
	FirmwareVersion          string                  `json:"firmware_version,omitempty"`
	SerialConsoleOEM         *SerialConsoleOEMInfo   `json:"serial_console_oem,omitempty"`
	GraphicalConsoleOEM      *GraphicalConsoleOEMInfo `json:"graphical_console_oem,omitempty"`
	SupportsWebSocket        bool                    `json:"supports_websocket"`
	SupportsHTML5Console     bool                    `json:"supports_html5_console"`
	AdditionalCapabilities   map[string]interface{}  `json:"additional_capabilities,omitempty"`
}

// SerialConsoleOEMInfo contains vendor-specific serial console endpoints
type SerialConsoleOEMInfo struct {
	WebSocketEndpoint string `json:"websocket_endpoint,omitempty"`
	SSHEndpoint       string `json:"ssh_endpoint,omitempty"`
	TelnetEndpoint    string `json:"telnet_endpoint,omitempty"`
}

// GraphicalConsoleOEMInfo contains vendor-specific graphical console endpoints
type GraphicalConsoleOEMInfo struct {
	HTML5Endpoint     string   `json:"html5_endpoint,omitempty"`
	WebSocketEndpoint string   `json:"websocket_endpoint,omitempty"`
	SupportedMethods  []string `json:"supported_methods,omitempty"`
}

// DetectVendor detects the BMC vendor from Manager resource data
func DetectVendor(managerData map[string]interface{}) VendorType {
	// Check Manufacturer field
	if manufacturer, ok := managerData["Manufacturer"].(string); ok {
		vendor := detectVendorFromManufacturer(manufacturer)
		if vendor != VendorUnknown {
			return vendor
		}
	}

	// Check Model field
	if model, ok := managerData["Model"].(string); ok {
		vendor := detectVendorFromModel(model)
		if vendor != VendorUnknown {
			return vendor
		}
	}

	// Check OEM properties
	if oem, ok := managerData["Oem"].(map[string]interface{}); ok {
		vendor := detectVendorFromOEM(oem)
		if vendor != VendorUnknown {
			return vendor
		}
	}

	// Check @odata.type for vendor-specific types
	if odataType, ok := managerData["@odata.type"].(string); ok {
		vendor := detectVendorFromODataType(odataType)
		if vendor != VendorUnknown {
			return vendor
		}
	}

	return VendorUnknown
}

// detectVendorFromManufacturer detects vendor from Manufacturer field
func detectVendorFromManufacturer(manufacturer string) VendorType {
	manufacturer = strings.ToLower(manufacturer)
	
	if strings.Contains(manufacturer, "dell") {
		return VendorDell
	}
	if strings.Contains(manufacturer, "supermicro") || strings.Contains(manufacturer, "super micro") {
		return VendorSupermicro
	}
	if strings.Contains(manufacturer, "hpe") || strings.Contains(manufacturer, "hewlett") || strings.Contains(manufacturer, "hp enterprise") {
		return VendorHPE
	}
	
	return VendorUnknown
}

// detectVendorFromModel detects vendor from Model field
func detectVendorFromModel(model string) VendorType {
	model = strings.ToLower(model)
	
	// Dell iDRAC models
	if strings.Contains(model, "idrac") {
		return VendorDell
	}
	
	// Supermicro models often start with X or H series
	if strings.HasPrefix(model, "x") || strings.HasPrefix(model, "h") {
		// This is a weak heuristic, but combined with other checks it helps
		// Only use this as a fallback
	}
	
	// HPE iLO models
	if strings.Contains(model, "ilo") || strings.Contains(model, "integrated lights-out") {
		return VendorHPE
	}
	
	return VendorUnknown
}

// detectVendorFromOEM detects vendor from OEM extensions
func detectVendorFromOEM(oem map[string]interface{}) VendorType {
	// Check for Dell OEM namespace
	if _, ok := oem["Dell"]; ok {
		return VendorDell
	}
	
	// Check for Supermicro OEM namespace
	if _, ok := oem["Supermicro"]; ok {
		return VendorSupermicro
	}
	
	// Check for HPE OEM namespace
	if _, ok := oem["Hpe"]; ok {
		return VendorHPE
	}
	if _, ok := oem["Hp"]; ok {
		return VendorHPE
	}
	
	return VendorUnknown
}

// detectVendorFromODataType detects vendor from @odata.type
func detectVendorFromODataType(odataType string) VendorType {
	odataType = strings.ToLower(odataType)
	
	if strings.Contains(odataType, "dell") {
		return VendorDell
	}
	if strings.Contains(odataType, "supermicro") {
		return VendorSupermicro
	}
	if strings.Contains(odataType, "hpe") || strings.Contains(odataType, "hp.") {
		return VendorHPE
	}
	
	return VendorUnknown
}

// ExtractVendorCapability extracts vendor-specific console capabilities
func ExtractVendorCapability(vendor VendorType, managerData map[string]interface{}) *VendorCapability {
	capability := &VendorCapability{
		Vendor:                 vendor,
		AdditionalCapabilities: make(map[string]interface{}),
	}

	// Extract Model and FirmwareVersion
	if model, ok := managerData["Model"].(string); ok {
		capability.Model = model
	}
	if fwVersion, ok := managerData["FirmwareVersion"].(string); ok {
		capability.FirmwareVersion = fwVersion
	}

	// Extract vendor-specific OEM data
	if oem, ok := managerData["Oem"].(map[string]interface{}); ok {
		switch vendor {
		case VendorDell:
			capability.extractDellOEM(oem)
		case VendorSupermicro:
			capability.extractSupermicroOEM(oem)
		case VendorHPE:
			capability.extractHPEOEM(oem)
		}
	}

	return capability
}

// extractDellOEM extracts Dell-specific OEM console information
func (vc *VendorCapability) extractDellOEM(oem map[string]interface{}) {
	dellOEM, ok := oem["Dell"].(map[string]interface{})
	if !ok {
		return
	}

	vc.SupportsWebSocket = true
	vc.SupportsHTML5Console = true

	// Extract serial console WebSocket endpoint
	if wsEndpoint, ok := dellOEM["WebSocketEndpoint"].(string); ok {
		if vc.SerialConsoleOEM == nil {
			vc.SerialConsoleOEM = &SerialConsoleOEMInfo{}
		}
		vc.SerialConsoleOEM.WebSocketEndpoint = wsEndpoint
	}

	// Extract graphical console endpoint (vKVM)
	if kvmEndpoint, ok := dellOEM["vKVMEndpoint"].(string); ok {
		if vc.GraphicalConsoleOEM == nil {
			vc.GraphicalConsoleOEM = &GraphicalConsoleOEMInfo{}
		}
		vc.GraphicalConsoleOEM.HTML5Endpoint = kvmEndpoint
		vc.GraphicalConsoleOEM.SupportedMethods = []string{"HTML5", "WebSocket"}
	}

	// Store additional Dell-specific capabilities
	vc.AdditionalCapabilities["dell_oem"] = dellOEM
}

// extractSupermicroOEM extracts Supermicro-specific OEM console information
func (vc *VendorCapability) extractSupermicroOEM(oem map[string]interface{}) {
	smcOEM, ok := oem["Supermicro"].(map[string]interface{})
	if !ok {
		return
	}

	// Supermicro supports HTML5 iKVM in newer firmware
	vc.SupportsHTML5Console = true

	// Extract graphical console endpoint
	if ikvmEndpoint, ok := smcOEM["iKVMEndpoint"].(string); ok {
		if vc.GraphicalConsoleOEM == nil {
			vc.GraphicalConsoleOEM = &GraphicalConsoleOEMInfo{}
		}
		vc.GraphicalConsoleOEM.HTML5Endpoint = ikvmEndpoint
		vc.GraphicalConsoleOEM.SupportedMethods = []string{"HTML5"}
	}

	// Check for WebSocket support (firmware version dependent)
	if wsSupport, ok := smcOEM["WebSocketSupport"].(bool); ok {
		vc.SupportsWebSocket = wsSupport
	}

	// Store additional Supermicro-specific capabilities
	vc.AdditionalCapabilities["supermicro_oem"] = smcOEM
}

// extractHPEOEM extracts HPE-specific OEM console information
func (vc *VendorCapability) extractHPEOEM(oem map[string]interface{}) {
	// HPE can use either "Hpe" or "Hp" namespace
	var hpeOEM map[string]interface{}
	var ok bool
	
	if hpeOEM, ok = oem["Hpe"].(map[string]interface{}); !ok {
		hpeOEM, ok = oem["Hp"].(map[string]interface{})
		if !ok {
			return
		}
	}

	vc.SupportsWebSocket = true
	vc.SupportsHTML5Console = true

	// Extract Integrated Remote Console (IRC) endpoint
	if ircEndpoint, ok := hpeOEM["IRCEndpoint"].(string); ok {
		if vc.GraphicalConsoleOEM == nil {
			vc.GraphicalConsoleOEM = &GraphicalConsoleOEMInfo{}
		}
		vc.GraphicalConsoleOEM.HTML5Endpoint = ircEndpoint
		vc.GraphicalConsoleOEM.SupportedMethods = []string{"HTML5", "WebSocket"}
	}

	// Extract serial console WebSocket endpoint
	if wsEndpoint, ok := hpeOEM["SerialConsoleWebSocket"].(string); ok {
		if vc.SerialConsoleOEM == nil {
			vc.SerialConsoleOEM = &SerialConsoleOEMInfo{}
		}
		vc.SerialConsoleOEM.WebSocketEndpoint = wsEndpoint
	}

	// Store additional HPE-specific capabilities
	vc.AdditionalCapabilities["hpe_oem"] = hpeOEM
}

// ToJSON converts VendorCapability to JSON string for storage
func (vc *VendorCapability) ToJSON() (string, error) {
	data, err := json.Marshal(vc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// VendorCapabilityFromJSON parses VendorCapability from JSON string
func VendorCapabilityFromJSON(jsonStr string) (*VendorCapability, error) {
	var vc VendorCapability
	if err := json.Unmarshal([]byte(jsonStr), &vc); err != nil {
		return nil, err
	}
	return &vc, nil
}
