# Virtual Media State Synchronization Background Job

## Overview

The Virtual Media State Synchronization background job is responsible for periodically polling downstream BMCs to detect and track changes in virtual media attachment state. This is critical for maintaining accurate state in Shoal's database, especially when virtual media operations occur outside of Shoal (e.g., direct BMC access).

## Purpose

1. **State Consistency**: Ensure Shoal's cached virtual media state matches the actual state on downstream BMCs
2. **Out-of-Band Change Detection**: Detect manual media attachments/detachments performed directly on BMCs
3. **Operation Verification**: Verify that Insert/Eject operations proxied by Shoal completed successfully
4. **Resource Availability**: Track which virtual media slots are currently available for use

## Design

### Sync Frequency

- **Default Interval**: 60 seconds (configurable)
- **Configuration Flag**: `--vmedia-sync-interval <seconds>`
- **Enable/Disable**: `--vmedia-sync-enabled <bool>` (default: true)

### Sync Process

For each enabled `ConnectionMethod`:

1. **Query Virtual Media Collection**
   - GET `/redfish/v1/Managers/{ManagerId}/VirtualMedia` on downstream BMC
   - Parse collection members

2. **Query Each Virtual Media Resource**
   - GET `/redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}`
   - Extract current state:
     - `Image` (current image URL)
     - `ImageName` (current image name)
     - `Inserted` (boolean - is media currently inserted)
     - `WriteProtected` (boolean - write protection status)
     - `ConnectedVia` (connection type: URI, Applet, NotConnected)

3. **Update Database Cache**
   - Call `UpsertVirtualMediaResource()` to update cached state
   - Update `last_updated` timestamp

4. **Detect State Changes**
   - Compare cached state with newly fetched state
   - Log changes (INFO level):
     - "Virtual Media CD1 on BMC-server01: inserted (image: http://example.com/test.iso)"
     - "Virtual Media CD1 on BMC-server01: ejected"
   - Optional: Generate events for state changes (future enhancement)

### Error Handling

- **BMC Unreachable**: Log warning, skip this connection method, continue with others
- **Timeout**: Apply reasonable timeout (30 seconds) per BMC query
- **Partial Failures**: Continue sync for other resources even if one fails
- **Rate Limiting**: Respect BMC rate limits, back off if errors occur

### Implementation Location

**Implemented in:** `internal/bmc/vmedia_sync.go`

The VirtualMediaSyncer is fully implemented with the following features:
- Periodic sync loop with configurable interval
- Graceful start/stop with context cancellation support
- Per-connection-method syncing
- Per-manager virtual media collection querying
- State change detection and logging
- Error handling with partial failure tolerance

### Key Functions

```go
// VirtualMediaSyncer manages periodic synchronization of virtual media state
type VirtualMediaSyncer struct {
    db       *database.DB
    interval time.Duration
    enabled  bool
    stopCh   chan struct{}
}

// NewVirtualMediaSyncer creates a new syncer
func NewVirtualMediaSyncer(db *database.DB, interval time.Duration, enabled bool) *VirtualMediaSyncer

// Start begins the periodic sync loop
func (s *VirtualMediaSyncer) Start(ctx context.Context)

// Stop gracefully stops the sync loop
func (s *VirtualMediaSyncer) Stop()

// SyncAll syncs all enabled connection methods
func (s *VirtualMediaSyncer) SyncAll(ctx context.Context) error

// SyncConnectionMethod syncs a single connection method
func (s *VirtualMediaSyncer) SyncConnectionMethod(ctx context.Context, connMethodID string) error
```

### Integration with Main Application

In `cmd/shoal/main.go`:

```go
// Create and start virtual media syncer if enabled
if viper.GetBool("vmedia-sync-enabled") {
    interval := time.Duration(viper.GetInt("vmedia-sync-interval")) * time.Second
    syncer := bmc.NewVirtualMediaSyncer(db, interval, true)
    syncer.Start(ctx)
    defer syncer.Stop()
}
```

## Configuration Options

### CLI Flags

```bash
--vmedia-sync-enabled bool          Enable periodic state synchronization (default: true)
--vmedia-sync-interval int          Sync interval in seconds (default: 60)
```

### Environment Variables

```bash
SHOAL_VMEDIA_SYNC_ENABLED=true
SHOAL_VMEDIA_SYNC_INTERVAL=60
```

## Observability

### Logging

- **DEBUG**: Each sync cycle start/completion
- **INFO**: State changes detected
- **WARN**: BMC unreachable, timeouts, partial failures
- **ERROR**: Critical errors preventing sync

### Metrics (Future Enhancement)

- `vmedia_sync_duration_seconds` - Histogram of sync duration
- `vmedia_sync_errors_total` - Counter of sync errors
- `vmedia_resources_synced_total` - Counter of resources synced
- `vmedia_state_changes_detected_total` - Counter of detected state changes

## Testing Strategy

### Unit Tests

- `TestVirtualMediaSyncer_SyncConnectionMethod`: Test syncing a single connection method
- `TestVirtualMediaSyncer_StateChangeDetection`: Verify state changes are logged
- `TestVirtualMediaSyncer_ErrorHandling`: Test BMC unreachable scenarios
- `TestVirtualMediaSyncer_StartStop`: Test lifecycle management

### Integration Tests

- Mock downstream BMC with virtual media resources
- Verify state updates in database
- Test sync frequency and timing
- Verify graceful shutdown

## Future Enhancements

1. **Event Generation**: Generate Redfish events when state changes are detected
2. **Webhook Notifications**: Call external webhooks on state changes
3. **Adaptive Sync Interval**: Adjust frequency based on change rate
4. **Per-BMC Intervals**: Different sync intervals for different connection methods
5. **Health Checks**: Expose sync health status via API endpoint
6. **Manual Sync Trigger**: API endpoint to trigger immediate sync

## Security Considerations

- **Credential Storage**: Reuse existing encrypted credentials from `ConnectionMethod`
- **Rate Limiting**: Respect BMC rate limits to avoid overload
- **Timeout Enforcement**: Prevent sync from blocking indefinitely
- **Error Logging**: Sanitize URLs and credentials in logs

## Rollout Plan

### Phase 1: Basic Implementation (Current)
- Database schema and CRUD operations ✅
- Documentation of sync design ✅
- Background job implementation ✅

### Phase 2: API Handlers (Next)
- Implement VirtualMedia collection/resource endpoints
- Implement InsertMedia/EjectMedia action handlers
- Add API integration tests

### Phase 3: Enhanced Observability (Future)
- Add structured logging
- Implement metrics collection
- Health check endpoint

## References

- [Design 020: Virtual Media Pass-Through](../design/020_Virtual_Media_Pass_Through.md)
- [DMTF Redfish VirtualMedia Schema](https://redfish.dmtf.org/schemas/v1/VirtualMedia.v1_6_2.json)
