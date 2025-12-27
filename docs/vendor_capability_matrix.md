# Vendor Capability Matrix - Console Pass-Through

This document describes the BMC vendor detection and console capability mapping implementation for Shoal's console pass-through feature.

## Overview

Shoal automatically detects BMC vendors and their console capabilities by analyzing the Redfish Manager resource. This information is used to provide optimal console access through vendor-specific OEM endpoints.

## Supported Vendors

### Dell iDRAC

**Detection Methods:**
- Manufacturer field: "Dell Inc.", "Dell"
- Model field: Contains "iDRAC"
- OEM namespace: `"Dell"` present in Manager OEM
- @odata.type: Contains "Dell"

**Console Capabilities:**
- **Serial Console**: Supported via Redfish OEM WebSocket endpoints
  - WebSocket endpoint: `/redfish/v1/Dell/Managers/{id}/SerialInterfaces/{n}/WebSocket`
  - Connection type: `Oem`
  
- **Graphical Console**: HTML5 Virtual Console (vKVM)
  - HTML5 endpoint: `/redfish/v1/Dell/Managers/{id}/DellvKVM`
  - Connection types: `KVMIP`, `Oem`
  - Methods: HTML5, WebSocket

**Feature Support:**
- WebSocket: ✓ Yes
- HTML5 Console: ✓ Yes
- Concurrent Sessions: 1-4 (model dependent)

### Supermicro BMC

**Detection Methods:**
- Manufacturer field: "Supermicro", "Super Micro Computer Inc."
- OEM namespace: `"Supermicro"` present in Manager OEM
- @odata.type: Contains "Supermicro"

**Console Capabilities:**
- **Serial Console**: Limited support (firmware version dependent)
  
- **Graphical Console**: HTML5 iKVM (newer firmware versions)
  - HTML5 endpoint: `/redfish/v1/Oem/Supermicro/iKVM`
  - Connection types: `KVMIP`
  - Methods: HTML5

**Feature Support:**
- WebSocket: Firmware version dependent
- HTML5 Console: ✓ Yes (firmware >= 1.70)
- Concurrent Sessions: 1-2 (model/firmware dependent)

**Notes:**
- WebSocket support varies by firmware version
- Older firmware may not support console pass-through via Redfish
- Check `WebSocketSupport` flag in OEM data

### HPE iLO

**Detection Methods:**
- Manufacturer field: "HPE", "Hewlett Packard Enterprise", "HP Enterprise"
- Model field: Contains "iLO", "Integrated Lights-Out"
- OEM namespace: `"Hpe"` or `"Hp"` present in Manager OEM
- @odata.type: Contains "HPE", "HP."

**Console Capabilities:**
- **Serial Console**: Supported via Redfish OEM WebSocket endpoints
  - WebSocket endpoint: `/redfish/v1/Managers/{id}/SerialConsole/WebSocket`
  - Connection type: `Oem`
  
- **Graphical Console**: Integrated Remote Console (IRC)
  - HTML5 endpoint: `/redfish/v1/Managers/{id}/RemoteConsole`
  - Connection types: `KVMIP`, `Oem`
  - Methods: HTML5, WebSocket

**Feature Support:**
- WebSocket: ✓ Yes
- HTML5 Console: ✓ Yes
- Concurrent Sessions: 1-6 (iLO version dependent)

**Notes:**
- iLO 5 and later have full Redfish console support
- iLO 4 may have limited OEM endpoint support

## Vendor Detection Logic

The vendor detection process follows this priority:

1. **OEM Namespace Check** (Most reliable)
   - Looks for vendor-specific OEM namespaces in Manager resource
   - Dell: `Oem.Dell`
   - Supermicro: `Oem.Supermicro`
   - HPE: `Oem.Hpe` or `Oem.Hp`

2. **Manufacturer Field**
   - Case-insensitive string matching on common vendor names

3. **Model Field**
   - Checks for vendor-specific model identifiers (iDRAC, iLO)

4. **@odata.type**
   - Vendor-specific Redfish schema types

## Vendor Data Structure

Shoal stores vendor capability information in the `console_capabilities` table:

```json
{
  "vendor": "Dell",
  "model": "iDRAC9",
  "firmware_version": "4.40.00.00",
  "supports_websocket": true,
  "supports_html5_console": true,
  "serial_console_oem": {
    "websocket_endpoint": "/redfish/v1/Dell/Managers/BMC/SerialInterfaces/1/WebSocket"
  },
  "graphical_console_oem": {
    "html5_endpoint": "/redfish/v1/Dell/Managers/BMC/DellvKVM",
    "supported_methods": ["HTML5", "WebSocket"]
  },
  "additional_capabilities": {
    "dell_oem": { ... }
  }
}
```

## Console Type Support Matrix

| Vendor      | Serial Console | Graphical Console | WebSocket | HTML5 | Notes |
|-------------|----------------|-------------------|-----------|-------|-------|
| Dell iDRAC  | ✓ Yes          | ✓ Yes             | ✓ Yes     | ✓ Yes | Full Redfish OEM support |
| Supermicro  | Limited        | ✓ Yes             | FW Dep.   | ✓ Yes | WebSocket support firmware version dependent |
| HPE iLO     | ✓ Yes          | ✓ Yes             | ✓ Yes     | ✓ Yes | iLO 5+ recommended |
| Unknown     | Standard Only  | Standard Only     | No        | No    | DMTF standard properties only |

## Implementation Details

### Vendor Detection

Located in `internal/bmc/vendor.go`:

- `DetectVendor(managerData)`: Main detection function
- `ExtractVendorCapability(vendor, managerData)`: Extract vendor-specific OEM data

### Console Sync Integration

Located in `internal/bmc/console_sync.go`:

- `syncManagerConsoleCapabilities()`: Enhanced to detect vendor and extract capabilities
- `processConsoleCapability()`: Stores vendor data alongside console properties

### Database Schema

The `vendor_data` field in `console_capabilities` table stores JSON with:
- Vendor identification
- Model and firmware version
- Console-specific OEM endpoints
- Feature flags (WebSocket, HTML5 support)
- Additional vendor-specific capabilities

## Testing

Comprehensive unit tests cover:
- Vendor detection for all supported vendors
- Capability extraction with vendor-specific OEM data
- Integration with console sync workflow
- Data persistence and retrieval

Test files:
- `internal/bmc/vendor_test.go`: Vendor detection and capability extraction
- `internal/bmc/console_sync_test.go`: Integration tests

## Future Enhancements

Planned vendor support:
- Lenovo XClarity Controller
- Huawei iBMC
- AMI MegaRAC

Additional capabilities to detect:
- Command Shell support
- Virtual media capabilities via console
- Multi-user console session support
- Console recording capabilities

## References

- Design Document: [021_Console_Pass_Through.md](../design/021_Console_Pass_Through.md)
- Dell iDRAC Redfish API Guide
- Supermicro Redfish User Guide
- HPE iLO 5 Redfish API Reference
- DMTF Redfish Manager Schema v1.10+
