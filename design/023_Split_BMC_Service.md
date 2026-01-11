# 023: Split BMC Service into Focused Modules

- **Status**: Proposed
- **Author**: Matthew Burns
- **Date**: 2026-01-10

## Summary

This design splits `internal/bmc/service.go` (1,892 lines) into focused modules organized by domain. The primary goal is to reduce the cyclomatic complexity of `DiscoverSettings` (currently 77) and make the codebase easier for AI agents and humans to navigate.

## Motivation

`internal/bmc/service.go` contains 35+ functions covering:
- Settings discovery and enrichment
- ID discovery (manager/system IDs)
- Power control
- Connection testing
- HTTP request helpers
- Caching logic
- Storage device enumeration
- Network interface enumeration
- SEL (System Event Log) retrieval
- Connection method management

**Problems:**
1. **Extreme complexity**: `DiscoverSettings` has cyclomatic complexity of 77
2. **Large context requirement**: Any BMC-related change requires loading 1,900 lines
3. **Mixed concerns**: Storage, network, settings, and power control in one file
4. **Difficult testing**: Hard to test individual discovery paths in isolation

## Goals

- Split `service.go` into domain-focused modules
- Reduce `DiscoverSettings` complexity to <15 per function
- Keep the `Service` struct as the public API
- Maintain identical external behavior
- Improve testability of individual components

## Non-Goals

- No changes to public API or behavior
- No new features or capabilities
- No changes to caching strategy or TTLs
- No database schema changes

## Implementation Plan

### Milestone 1: Extract HTTP Helpers

Create `internal/bmc/http.go` with request construction helpers:

```go
// http.go - HTTP request helpers for BMC communication

// newAuthedGET creates an authenticated GET request to a BMC path
func (s *Service) newAuthedGET(ctx context.Context, bmc *models.BMC, path string) (*http.Request, error)

// fetchRedfishResource fetches and decodes a Redfish resource as JSON
func (s *Service) fetchRedfishResource(ctx context.Context, bmc *models.BMC, path string) (map[string]interface{}, error)

// buildBMCURL constructs a full URL for a BMC path
func (s *Service) buildBMCURL(bmc *models.BMC, path string) (string, error)

// createProxyRequest creates a proxy request to forward to a BMC
func (s *Service) createProxyRequest(r *http.Request, targetURL string, bmc *models.BMC) (*http.Request, error)
```

**Move from service.go:**
- `buildBMCURL` (lines 461-499)
- `createProxyRequest` (lines 501-535)
- `newAuthedGET` (lines 537-550)
- `fetchRedfishResource` (lines 1301-1324)

### Milestone 2: Extract ID Discovery

Create `internal/bmc/discovery.go` with ID discovery functions:

```go
// discovery.go - BMC manager and system ID discovery with caching

// GetFirstManagerID discovers and caches the first manager ID for a BMC
func (s *Service) GetFirstManagerID(ctx context.Context, bmcName string) (string, error)

// GetFirstSystemID discovers and caches the first system ID for a BMC
func (s *Service) GetFirstSystemID(ctx context.Context, bmcName string) (string, error)

// bmcIDCache stores discovered manager and system IDs for a BMC
type bmcIDCache struct {
    managerID string
    systemID  string
    cachedAt  time.Time
}
```

**Move from service.go:**
- `bmcIDCache` struct (lines 50-54)
- `GetFirstManagerID` (lines 134-198)
- `GetFirstSystemID` (lines 200-264)

### Milestone 3: Extract Settings Discovery

Create `internal/bmc/settings.go` - this is the largest refactor:

```go
// settings.go - Redfish settings discovery and attribute enrichment

// DiscoverSettings enumerates configurable settings for a BMC
func (s *Service) DiscoverSettings(ctx context.Context, bmcName string, resourceFilter string) ([]models.SettingDescriptor, error)

// discoverBIOSSettings probes Systems/{id}/Bios for BIOS attributes
func (s *Service) discoverBIOSSettings(ctx context.Context, bmc *models.BMC, systemID, resourceFilter string) []models.SettingDescriptor

// discoverBootOrderSettings probes ComputerSystem for Boot.BootOrder
func (s *Service) discoverBootOrderSettings(ctx context.Context, bmc *models.BMC, systemID, resourceFilter string) []models.SettingDescriptor

// discoverNetworkProtocolSettings probes Managers/{id}/NetworkProtocol
func (s *Service) discoverNetworkProtocolSettings(ctx context.Context, bmc *models.BMC, managerID, resourceFilter string) []models.SettingDescriptor

// discoverEthernetSettings probes Systems/{id}/EthernetInterfaces collection
func (s *Service) discoverEthernetSettings(ctx context.Context, bmc *models.BMC, systemID, resourceFilter string) []models.SettingDescriptor

// discoverStorageSettings probes Systems/{id}/Storage collection
func (s *Service) discoverStorageSettings(ctx context.Context, bmc *models.BMC, systemID, resourceFilter string) []models.SettingDescriptor
```

The refactored `DiscoverSettings` becomes an orchestrator:

```go
func (s *Service) DiscoverSettings(ctx context.Context, bmcName string, resourceFilter string) ([]models.SettingDescriptor, error) {
    bmc, err := s.db.GetBMCByName(ctx, bmcName)
    if err != nil {
        return nil, fmt.Errorf("failed to get BMC: %w", err)
    }
    if bmc == nil {
        return nil, fmt.Errorf("BMC not found: %s", bmcName)
    }
    if !bmc.Enabled {
        return nil, fmt.Errorf("BMC is disabled: %s", bmcName)
    }

    var descriptors []models.SettingDescriptor

    systemID, _ := s.GetFirstSystemID(ctx, bmcName)
    managerID, _ := s.GetFirstManagerID(ctx, bmcName)

    // Probe each resource type
    descriptors = append(descriptors, s.discoverBIOSSettings(ctx, bmc, systemID, resourceFilter)...)
    descriptors = append(descriptors, s.discoverBootOrderSettings(ctx, bmc, systemID, resourceFilter)...)
    descriptors = append(descriptors, s.discoverNetworkProtocolSettings(ctx, bmc, managerID, resourceFilter)...)
    descriptors = append(descriptors, s.discoverEthernetSettings(ctx, bmc, systemID, resourceFilter)...)
    descriptors = append(descriptors, s.discoverStorageSettings(ctx, bmc, systemID, resourceFilter)...)

    // Cache results
    if err := s.db.UpsertSettingDescriptors(ctx, bmcName, descriptors); err != nil {
        slog.Warn("Failed to cache setting descriptors", "bmc", bmcName, "error", err)
    }

    return descriptors, nil
}
```

**Move from service.go:**
- `DiscoverSettings` (lines 552-780) - refactor into orchestrator + helpers
- `pickWritableLookingFields` (lines 781-794)
- `extractApplyTimesAndSettingsObject` (lines 796-813)
- `buildDescriptorsFromMap` (lines 815-837)
- `inferType` (lines 839-854)
- `hashID` (lines 856-870)

### Milestone 4: Extract Registry Enrichment

Create `internal/bmc/registry.go` with attribute registry resolution:

```go
// registry.go - Attribute registry resolution and descriptor enrichment

// enrichDescriptors augments descriptors using Attribute Registries and ActionInfo
func (s *Service) enrichDescriptors(ctx context.Context, bmc *models.BMC, resource map[string]interface{}, descs []models.SettingDescriptor) []models.SettingDescriptor

// resolveAttributeRegistry fetches and caches attribute registry for a resource
func (s *Service) resolveAttributeRegistry(ctx context.Context, bmc *models.BMC, resource map[string]interface{}) map[string]interface{}

// resolveActionInfo fetches ActionInfo for a resource
func (s *Service) resolveActionInfo(ctx context.Context, bmc *models.BMC, resource map[string]interface{}) map[string]interface{}

// parseRegistryAttributes parses registry payload into attribute index
func parseRegistryAttributes(reg map[string]interface{}) map[string]map[string]interface{}

// Cache types
type registryCacheEntry struct {
    payload  map[string]interface{}
    cachedAt time.Time
}

type actionInfoCacheEntry struct {
    payload  map[string]interface{}
    cachedAt time.Time
}
```

**Move from service.go:**
- `registryCacheTTL` constant (line 81)
- `registryCacheEntry` struct (lines 83-86)
- `actionInfoCacheEntry` struct (lines 88-91)
- `enrichDescriptors` (lines 922-1040)
- `resolveAttributeRegistry` (lines 1042-1126)
- `parseRegistryAttributes` (lines 1128-1158)
- `resolveActionInfo` (lines 1160-1200)
- `isRefresh` helper (lines 1202-1213)

### Milestone 5: Extract Status Retrieval

Create `internal/bmc/status.go` with detailed status functions:

```go
// status.go - BMC detailed status retrieval (system info, storage, network, SEL)

// GetDetailedBMCStatus retrieves comprehensive status information
func (s *Service) GetDetailedBMCStatus(ctx context.Context, bmcName string) (*models.DetailedBMCStatus, error)

// getSystemInfo retrieves system information from ComputerSystem resource
func (s *Service) getSystemInfo(ctx context.Context, bmc *models.BMC) (*models.SystemInfo, error)

// getNetworkInterfaces retrieves network interface information
func (s *Service) getNetworkInterfaces(ctx context.Context, bmc *models.BMC) ([]models.NetworkInterface, error)

// getStorageDevices retrieves storage device information
func (s *Service) getStorageDevices(ctx context.Context, bmc *models.BMC) ([]models.StorageDevice, error)

// getSELEntries retrieves System Event Log entries
func (s *Service) getSELEntries(ctx context.Context, bmc *models.BMC) ([]models.SELEntry, error)
```

**Move from service.go:**
- `GetDetailedBMCStatus` (lines 1326-1381)
- `getSystemInfo` (lines 1383-1416)
- `getNetworkInterfaces` (lines 1418-1483)
- `getStorageDevices` (lines 1485-1502)
- `getStorageDevicesFromStorage` (lines 1504-1551)
- `getStorageDevicesFromSimpleStorage` (lines 1553-1598)
- `parseStorageDevice` (lines 1600-1641)
- `getSELEntries` (lines 1643-1721)

### Milestone 6: Extract Setting Updates

Create `internal/bmc/update.go` with setting modification functions:

```go
// update.go - BMC setting update operations

// UpdateSetting applies a new value to a BMC setting
func (s *Service) UpdateSetting(ctx context.Context, bmcName, descriptorID string, newValue interface{}) error

// validateSettingValue validates a value against descriptor constraints
func (s *Service) validateSettingValue(descriptor *models.SettingDescriptor, value interface{}) error
```

**Move from service.go:**
- `UpdateSetting` (lines 1738-1829)
- `validateSettingValue` (lines 1831-1893)

### Milestone 7: Slim Down service.go

After extraction, `service.go` will contain only:

```go
// service.go - BMC service initialization and core operations

// Service handles BMC communication and management
type Service struct {
    db         *database.DB
    client     *http.Client
    idCache    map[string]*bmcIDCache
    idCacheMux sync.RWMutex
    regCache    map[string]map[string]*registryCacheEntry
    regCacheMux sync.RWMutex
    aiCache     map[string]map[string]*actionInfoCacheEntry
    aiCacheMux  sync.RWMutex
}

// New creates a new BMC service
func New(db *database.DB) *Service

// ProxyRequest forwards a request to the appropriate BMC
func (s *Service) ProxyRequest(ctx context.Context, bmcName, path string, r *http.Request) (*http.Response, error)

// PowerControl sends a power action to a BMC
func (s *Service) PowerControl(ctx context.Context, bmcName string, action models.PowerAction) error

// TestConnection tests connectivity to a BMC
func (s *Service) TestConnection(ctx context.Context, bmc *models.BMC) error

// TestUnauthenticatedConnection tests unauthenticated connectivity
func (s *Service) TestUnauthenticatedConnection(ctx context.Context, address string) error

// Connection method management
func (s *Service) AddConnectionMethod(ctx context.Context, name, address, username, password string) (*models.ConnectionMethod, error)
func (s *Service) RemoveConnectionMethod(ctx context.Context, id string) error
func (s *Service) GetConnectionMethods(ctx context.Context) ([]models.ConnectionMethod, error)
func (s *Service) GetConnectionMethod(ctx context.Context, id string) (*models.ConnectionMethod, error)

// FetchAggregatedData retrieves manager and system data for a BMC
func (s *Service) FetchAggregatedData(ctx context.Context, bmc *models.BMC) ([]map[string]interface{}, []map[string]interface{}, error)
```

## File Changes Summary

### New Files
| File | Lines (est.) | Purpose |
|------|--------------|---------|
| `internal/bmc/http.go` | ~100 | HTTP request helpers |
| `internal/bmc/discovery.go` | ~150 | Manager/system ID discovery |
| `internal/bmc/settings.go` | ~350 | Settings discovery (refactored) |
| `internal/bmc/registry.go` | ~250 | Attribute registry enrichment |
| `internal/bmc/status.go` | ~400 | Detailed status retrieval |
| `internal/bmc/update.go` | ~150 | Setting update operations |

### Modified Files
| File | Before | After |
|------|--------|-------|
| `internal/bmc/service.go` | 1,892 lines | ~400 lines |

### Complexity Reduction

| Function | Before | After |
|----------|--------|-------|
| `DiscoverSettings` | 77 | ~10 (orchestrator) |
| `discoverBIOSSettings` | (part of above) | ~12 |
| `discoverStorageSettings` | (part of above) | ~15 |
| `enrichDescriptors` | 39 | ~15 (moved) |
| `getSELEntries` | 23 | ~15 (moved) |

## Testing

1. **All existing tests must pass**: `internal/bmc/*_test.go`
2. **No behavior changes**: Mock responses should produce identical results
3. **Verify imports**: Each new file must have correct imports

## Validation

```bash
go run build.go validate
```

## Implementation Notes for AI Agents

1. **License headers required**: All new `.go` files MUST include the AGPLv3 license header as specified in AGENTS.md section 1.4. Use the Go format for all files.
2. **Start with Milestone 1** (HTTP helpers) as it has no dependencies
3. **Extract in order** - later milestones depend on earlier ones
4. **Keep function signatures identical** - only file location changes
5. **Update imports** in service.go to reference new files within same package (no import needed)
6. **Run tests after each milestone** to catch issues early

## Rollback Plan

Revert the commits for this design. All code will be restored to service.go.
