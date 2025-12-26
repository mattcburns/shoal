# 020: Virtual Media Pass-Through

**Author:** GitHub Copilot  
**Date:** 2025-12-26  
**Status:** Proposed

## Abstract

This document outlines the design for implementing DMTF Redfish-compliant Virtual Media Pass-Through in Shoal. As a Redfish aggregator acting as a bastion service, Shoal provides unified access to virtual media resources across managed BMCs. Images are stored externally (HTTP servers, OCI registries, NFS, etc.), and Shoal proxies virtual media attachment/detachment operations to downstream BMCs while tracking attachment state. This design enables operations like OS installation, firmware updates, and system recovery on isolated BMCs without requiring Shoal to host image files locally.

## Background

### Current State

Shoal currently provides excellent aggregation and pass-through capabilities for HTTP-based Redfish API calls. It discovers and manages BMCs through the `AggregationService`, providing unified access to system information, settings, and power management.

However, virtual media resources are not yet exposed through Shoal's aggregation layer:
- BMCs typically provide virtual media capabilities through `/redfish/v1/Managers/{id}/VirtualMedia`
- Users need unified access to attach ISOs or disk images from external sources
- In Shoal's deployment model, BMCs are isolated and cannot reach external networks
- Shoal acts as a bastion, providing the only connectivity path to/from BMCs

### Problem Statement

When a user wants to boot a system from an ISO (e.g., for OS installation), they need:
1. Access to virtual media resources through Shoal's unified Redfish API
2. Ability to specify external image URLs (HTTP, HTTPS, NFS, CIFS)
3. Shoal to proxy virtual media attach/detach operations to the correct downstream BMC
4. Tracking of which media is attached to which systems
5. Ability to detach media when operations are complete

**Key Constraint**: Images are stored externally. Shoal does not host or store image files locally. Shoal's role is to:
- Aggregate virtual media resources from managed BMCs
- Proxy attach/detach actions to downstream BMCs
- Track attachment state across the aggregated environment
- Optionally rewrite external URLs to be BMC-accessible (if Shoal can proxy image downloads)

### Use Cases

1. **OS Installation**: Boot systems from installation ISOs hosted on external HTTP servers
2. **System Recovery**: Attach recovery/diagnostic ISOs to troubleshoot systems
3. **Firmware Updates**: Reference bootable firmware update images from vendor URLs
4. **Automated Provisioning**: Attach cloud-init ISOs or kickstart media from provisioning systems
5. **Multi-source Media**: Support images from various external sources (HTTP, NFS, OCI registries)

## Redfish Background

Virtual media in Redfish is accessed through the `VirtualMedia` collection under `Manager` resources, as defined by the DMTF Redfish specification.

### Resource Structure (DMTF Standard)

```
/redfish/v1/Managers/{ManagerId}/VirtualMedia
└── /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}
```

### Key Properties

Per the DMTF `VirtualMedia` v1.6+ schema:

- **`Image`**: The URI of the image to mount (HTTP/HTTPS/NFS/CIFS URL)
- **`ImageName`**: Human-readable name of the mounted image  
- **`Inserted`**: Boolean indicating if media is currently inserted
- **`WriteProtected`**: Boolean for write protection status
- **`ConnectedVia`**: How the media is connected (e.g., `URI`, `Applet`, `NotConnected`)
- **`MediaTypes`**: Supported media types (e.g., `CD`, `DVD`, `USBStick`, `Floppy`)
- **`UserName`**: Username for accessing the image (if authentication required)
- **`Password`**: Password for accessing the image (write-only)
- **`TransferProtocolType`**: Protocol for image transfer (e.g., `HTTP`, `HTTPS`, `NFS`, `CIFS`, `OEM`)

### Actions (DMTF Standard)

- **`#VirtualMedia.InsertMedia`**: Action to attach virtual media
  ```json
  POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.InsertMedia
  {
    "Image": "http://fileserver.example.com/images/ubuntu-22.04.iso",
    "Inserted": true,
    "WriteProtected": true,
    "TransferProtocolType": "HTTP"
  }
  ```
  
- **`#VirtualMedia.EjectMedia`**: Action to detach virtual media
  ```json
  POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.EjectMedia
  {}
  ```

### Shoal's Aggregation Model

Shoal will aggregate `VirtualMedia` resources from all managed BMCs, similar to how it currently aggregates `Managers` and `ComputerSystems`. Each aggregated `VirtualMedia` resource will maintain a link back to its source `ConnectionMethod`.

## Architecture Overview

Shoal will expose virtual media resources through DMTF Redfish-compliant endpoints and proxy operations to downstream BMCs.

### High-Level Components

1. **VirtualMedia Aggregation**: Discover and cache VirtualMedia resources from managed BMCs
2. **VirtualMedia Proxy**: Proxy InsertMedia/EjectMedia actions to downstream BMCs
3. **State Tracking**: Database records of virtual media state and attachments
4. **URL Rewriting** (Optional): Rewrite external image URLs to be BMC-accessible
5. **Image Proxy** (Optional): HTTP proxy to stream external images to BMCs

### DMTF-Compliant API Endpoints

All endpoints follow DMTF Redfish specification:

**1. Manager VirtualMedia Collection**
```
GET /redfish/v1/Managers/{ManagerId}/VirtualMedia
```
Returns aggregated collection of VirtualMedia resources for a specific manager.

**2. VirtualMedia Resource**
```
GET /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}
```
Returns details of a specific virtual media resource (aggregated from downstream BMC).

**3. InsertMedia Action**
```
POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.InsertMedia
{
  "Image": "http://fileserver.example.com/isos/ubuntu-22.04.iso",
  "Inserted": true,
  "WriteProtected": true,
  "TransferProtocolType": "HTTP"
}
```
Proxies to downstream BMC. Shoal may optionally rewrite the `Image` URL if needed.

**4. EjectMedia Action**
```
POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.EjectMedia
{}
```
Proxies eject operation to downstream BMC.

### Data Flow

```
User → GET /redfish/v1/Managers/{id}/VirtualMedia
                    ↓
       Shoal returns aggregated VirtualMedia resources
       (cached from downstream BMCs)
                    
User → POST InsertMedia with external image URL
                    ↓
       Shoal validates request
                    ↓
       Shoal optionally rewrites URL (if URL proxying enabled)
                    ↓
       Shoal proxies action to downstream BMC
                    ↓
       BMC downloads image from external source
       (or from Shoal's image proxy if configured)
                    ↓
       Shoal updates cached state in database
                    ↓
       Return success/failure to user
```

## Phase 1: Core Virtual Media Aggregation and Proxying

### 1.1 VirtualMedia Resource Discovery

When a `ConnectionMethod` is created (BMC is added), Shoal will discover VirtualMedia resources:

**Discovery Process**:
1. Query `/redfish/v1/Managers` on the downstream BMC
2. For each Manager, query the `VirtualMedia` collection
3. Fetch each `VirtualMedia` resource and cache properties
4. Store VirtualMedia metadata in Shoal's database

**Cached Properties**:
- Resource `@odata.id` and `Id`
- `MediaTypes` (supported media types for this slot)
- `ConnectedVia` (supported connection methods)
- Current `Image`, `ImageName`, `Inserted` state
- Available actions (`InsertMedia`, `EjectMedia`)

### 1.2 Database Schema

New table: `virtual_media_resources`
```sql
CREATE TABLE virtual_media_resources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    connection_method_id INTEGER NOT NULL,
    manager_id TEXT NOT NULL,              -- e.g., "BMC" from downstream
    resource_id TEXT NOT NULL,             -- e.g., "CD1", "RemovableDisk"
    odata_id TEXT NOT NULL,                -- Full @odata.id from downstream BMC
    media_types TEXT,                      -- JSON array: ["CD", "DVD"]
    supported_protocols TEXT,              -- JSON array: ["HTTP", "HTTPS", "NFS"]
    current_image_url TEXT,                -- Currently attached image URL
    current_image_name TEXT,
    is_inserted BOOLEAN DEFAULT 0,
    is_write_protected BOOLEAN,
    connected_via TEXT,
    last_updated DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(connection_method_id) REFERENCES connection_methods(id) ON DELETE CASCADE,
    UNIQUE(connection_method_id, manager_id, resource_id)
);

CREATE INDEX idx_vmr_connection ON virtual_media_resources(connection_method_id);
CREATE INDEX idx_vmr_manager ON virtual_media_resources(manager_id);
```

New table: `virtual_media_operations`
```sql
CREATE TABLE virtual_media_operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    virtual_media_resource_id INTEGER NOT NULL,
    operation TEXT NOT NULL,               -- 'insert', 'eject'
    image_url TEXT,                        -- For insert operations
    requested_by TEXT,
    requested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT DEFAULT 'pending',         -- pending, success, failed
    error_message TEXT,
    completed_at DATETIME,
    FOREIGN KEY(virtual_media_resource_id) REFERENCES virtual_media_resources(id) ON DELETE CASCADE
);

CREATE INDEX idx_vmo_resource ON virtual_media_operations(virtual_media_resource_id);
CREATE INDEX idx_vmo_status ON virtual_media_operations(status);
```

### 1.3 Redfish API Implementation

**Handler Structure** (in `internal/api`):

**1. VirtualMedia Collection Handler**
```go
// GET /redfish/v1/Managers/{ManagerId}/VirtualMedia
func (h *Handler) handleVirtualMediaCollection(w http.ResponseWriter, r *http.Request)
```

Returns DMTF-compliant collection:
```json
{
  "@odata.context": "/redfish/v1/$metadata#VirtualMediaCollection.VirtualMediaCollection",
  "@odata.id": "/redfish/v1/Managers/BMC-server01/VirtualMedia",
  "@odata.type": "#VirtualMediaCollection.VirtualMediaCollection",
  "Name": "Virtual Media Services",
  "Members@odata.count": 2,
  "Members": [
    {"@odata.id": "/redfish/v1/Managers/BMC-server01/VirtualMedia/CD1"},
    {"@odata.id": "/redfish/v1/Managers/BMC-server01/VirtualMedia/RemovableDisk"}
  ]
}
```

**2. VirtualMedia Resource Handler**
```go
// GET /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}
func (h *Handler) handleVirtualMedia(w http.ResponseWriter, r *http.Request)
```

Returns DMTF-compliant resource from cache:
```json
{
  "@odata.context": "/redfish/v1/$metadata#VirtualMedia.VirtualMedia",
  "@odata.id": "/redfish/v1/Managers/BMC-server01/VirtualMedia/CD1",
  "@odata.type": "#VirtualMedia.v1_6_0.VirtualMedia",
  "Id": "CD1",
  "Name": "Virtual CD",
  "MediaTypes": ["CD", "DVD"],
  "Image": "http://fileserver.example.com/isos/ubuntu-22.04.iso",
  "ImageName": "ubuntu-22.04.iso",
  "Inserted": true,
  "WriteProtected": true,
  "ConnectedVia": "URI",
  "TransferProtocolType": "HTTP",
  "Actions": {
    "#VirtualMedia.InsertMedia": {
      "target": "/redfish/v1/Managers/BMC-server01/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
      "@Redfish.ActionInfo": "/redfish/v1/Managers/BMC-server01/VirtualMedia/CD1/InsertMediaActionInfo"
    },
    "#VirtualMedia.EjectMedia": {
      "target": "/redfish/v1/Managers/BMC-server01/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia"
    }
  }
}
```

**3. InsertMedia Action Handler**
```go
// POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.InsertMedia
func (h *Handler) handleInsertMedia(w http.ResponseWriter, r *http.Request)
```

**Request Body** (DMTF standard):
```json
{
  "Image": "http://fileserver.example.com/isos/ubuntu-22.04.iso",
  "Inserted": true,
  "WriteProtected": true,
  "TransferProtocolType": "HTTP",
  "UserName": "optional-username",
  "Password": "optional-password"
}
```

**Implementation Steps**:
1. Validate request body against DMTF schema
2. Look up VirtualMedia resource in database (by Manager ID + Media ID)
3. Retrieve connection method credentials for downstream BMC
4. Optionally rewrite `Image` URL (see URL Rewriting section)
5. Forward action to downstream BMC's VirtualMedia endpoint
6. Record operation in `virtual_media_operations` table
7. Update cached state in `virtual_media_resources`
8. Return HTTP 204 No Content on success, or error message

**4. EjectMedia Action Handler**
```go
// POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.EjectMedia
func (h *Handler) handleEjectMedia(w http.ResponseWriter, r *http.Request)
```

Similar to InsertMedia, but proxies eject action to downstream BMC.

### 1.4 URL Rewriting (Optional Feature)

**Problem**: BMCs may not be able to reach external image URLs directly.

**Solution**: Shoal can optionally rewrite image URLs to point to Shoal's image proxy.

**Configuration**:
```bash
--enable-image-proxy bool           # Enable image proxying (default: false)
--image-proxy-port int              # Port for image proxy (default: 8082)
--external-image-base-url string    # Base URL pattern to rewrite
```

**URL Rewriting Logic**:
```
Original:  http://fileserver.example.com/isos/ubuntu-22.04.iso
Rewritten: http://shoal.example.com:8082/proxy?url=http://fileserver.example.com/isos/ubuntu-22.04.iso
```

The BMC then downloads from Shoal's proxy, which streams the image from the original source.

### 1.5 Image Proxy Server (Optional Feature)

**Purpose**: Stream external images to BMCs when direct access is unavailable.

**Implementation**:
```go
// GET /proxy?url=<encoded-url>
func (h *Handler) handleImageProxy(w http.ResponseWriter, r *http.Request)
```

**Features**:
- Validates `url` parameter (whitelist domains or authentication)
- Streams image from external source to BMC
- Supports HTTP Range requests (for resumable downloads)
- Rate limiting per BMC
- Access logging

**Security**:
- URL whitelist to prevent SSRF attacks
- Authentication token in query string (optional)
- IP-based access control (only BMC subnets)

### 1.6 State Synchronization

**Periodic Refresh**:
- Background job polls downstream BMCs for VirtualMedia state changes
- Updates `virtual_media_resources` table with current `Image`, `Inserted` status
- Detects external changes (manual BMC operations outside Shoal)

**Refresh Interval**: Configurable (default: 60 seconds)

### 1.7 UI Integration

**Manager Detail Page** (`/managers/{id}`):
- Add "Virtual Media" tab
- Display available virtual media slots
- Show currently attached images
- Buttons: "Attach Media", "Eject Media"

**Attach Media Dialog**:
- Input field for image URL
- Dropdown for media type (if multiple slots available)
- Checkbox for write protection
- Optional: username/password for authenticated image servers

**Active Media Table**:
- List of all attached virtual media across all BMCs
- Columns: BMC name, slot, image URL, attached time
- Quick eject button

## Phase 2: Advanced Features

### 2.1 Enhanced Image Proxy Capabilities

**Objective**: Advanced image proxy features for complex scenarios

**Features**:
- **Caching**: Optionally cache frequently-used images for faster subsequent attachments
- **Compression**: On-the-fly compression/decompression
- **Format Conversion**: Convert between image formats (e.g., QCOW2 to ISO)
- **Bandwidth Throttling**: QoS to prevent network saturation

### 2.2 Cloud-Init ISO Generation

**Objective**: Generate cloud-init ISOs on-demand for automated provisioning

**Design**:
- Shoal exposes endpoint to generate cloud-init ISOs
- User provides user-data and meta-data via Redfish OEM extension
- Shoal generates temporary ISO and serves via image proxy
- ISO attached as virtual media during boot

**Redfish OEM Extension Example**:
```json
POST /redfish/v1/Managers/BMC-server01/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia
{
  "Image": "http://shoal.example.com:8082/cloudinit-iso",
  "Oem": {
    "Shoal": {
      "GenerateCloudInit": true,
      "UserData": "#cloud-config\nusers:\n  - name: admin\n...",
      "MetaData": "instance-id: server-01\n..."
    }
  }
}
```

### 2.3 OCI Image Support

**Objective**: Support attaching OCI images (container images) as bootable media

**Design**:
- OCI images converted to bootable ISOs using tools like `buildah` or `mkosi`
- Shoal caches converted ISOs
- Users reference OCI images by registry URL
- Periodic refresh for `latest` tags

**Example**:
```json
POST /redfish/v1/Managers/BMC-server01/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia
{
  "Image": "oci://ghcr.io/fedora/coreos:stable",
  "TransferProtocolType": "OEM",
  "Oem": {
    "Shoal": {
      "OCIConversion": true
    }
  }
}
```

### 2.4 Kickstart / Preseed Integration

**Objective**: Serve kickstart or preseed files referenced during installation

**Design**:
- Shoal serves text-based provisioning configs via HTTP
- Configs generated from templates with per-system variables
- Boot parameters reference Shoal URLs

**Non-Redfish Endpoint** (provisioning-specific):
```
GET /provision/kickstart/{system-id}
GET /provision/preseed/{system-id}
```

### 2.5 Event Notifications

**Objective**: Notify when virtual media operations complete or fail

**Design**:
- Integrate with Shoal's existing EventService (if implemented)
- Generate Redfish events for InsertMedia/EjectMedia success/failure
- Clients can subscribe to virtual media events

**Event Types**:
- `VirtualMedia.InsertMedia.Success`
- `VirtualMedia.InsertMedia.Failed`
- `VirtualMedia.EjectMedia.Success`

### 2.6 Multi-Protocol Support

**Objective**: Support additional transfer protocols beyond HTTP/HTTPS

**Protocols**:
- **NFS**: Native NFS URL support (if BMC supports)
- **CIFS/SMB**: Windows file shares
- **TFTP**: Legacy network boot protocol
- **iSCSI**: Block-level virtual media

## Implementation Milestones

### Milestone 1: Core VirtualMedia Aggregation
- [ ] Enhance BMC discovery to query VirtualMedia resources
- [ ] Create database schema for `virtual_media_resources` and `virtual_media_operations`
- [ ] Implement database migrations
- [ ] Add caching logic for VirtualMedia resources
- [ ] Write unit tests for discovery and caching

### Milestone 2: Redfish API Handlers
- [ ] Implement VirtualMedia collection handler (`GET /redfish/v1/Managers/{id}/VirtualMedia`)
- [ ] Implement VirtualMedia resource handler (`GET /redfish/v1/Managers/{id}/VirtualMedia/{mediaId}`)
- [ ] Implement InsertMedia action handler
- [ ] Implement EjectMedia action handler
- [ ] Add Redfish schema validation
- [ ] Write unit tests with mock downstream BMCs

### Milestone 3: Proxy and State Management
- [ ] Implement proxy logic to forward actions to downstream BMCs
- [ ] Add error handling and retry logic
- [ ] Implement state synchronization (periodic polling)
- [ ] Track operations in database
- [ ] Write integration tests

### Milestone 4: URL Rewriting and Image Proxy (Optional)
- [ ] Implement URL rewriting logic
- [ ] Build image proxy HTTP server
- [ ] Add Range request support
- [ ] Implement rate limiting
- [ ] Add access control and security measures
- [ ] Write integration tests

### Milestone 5: UI Integration
- [ ] Add "Virtual Media" tab to Manager detail page
- [ ] Implement attach/detach dialogs
- [ ] Display active virtual media attachments
- [ ] Add frontend validation
- [ ] Update navigation

### Milestone 6: Advanced Features (Phase 2)
- [ ] Enhanced image proxy with caching
- [ ] Cloud-init ISO generation (OEM extension)
- [ ] OCI image support
- [ ] Kickstart/preseed hosting
- [ ] Event notifications
- [ ] Multi-protocol support

## Security Considerations

### 1. URL Validation
- **Whitelist Domains**: Optionally restrict which external domains can be used for image URLs
- **SSRF Prevention**: Prevent Server-Side Request Forgery attacks via image proxy
- **URL Sanitization**: Validate and sanitize all external URLs before proxying

### 2. Access Control
- **Authentication**: All VirtualMedia operations require authenticated user
- **Authorization**: RBAC enforced at Redfish API level (follows existing Shoal RBAC model)
- **BMC Credentials**: Secure storage and transmission of credentials to downstream BMCs
- **Proxy Access**: Image proxy restricted to BMC IP ranges (if enabled)

### 3. State Integrity
- **Concurrent Access**: Handle concurrent InsertMedia requests to same slot
- **State Synchronization**: Detect and handle out-of-band changes to VirtualMedia
- **Transaction Safety**: Database transactions for state updates

### 4. Network Security
- **HTTPS for API**: All Redfish API calls over HTTPS
- **Image Proxy TLS**: Optional HTTPS for image proxy (requires BMC cert trust)
- **Rate Limiting**: Prevent DoS via excessive InsertMedia requests
- **Audit Logging**: Track all VirtualMedia operations (who, what, when, BMC)

### 5. Data Protection
- **Credential Encryption**: Encrypt image server credentials (username/password) in database
- **Secrets in Transit**: Use HTTPS when proxying to external image servers
- **Log Sanitization**: Redact sensitive URLs and credentials from logs

## Configuration

New CLI flags and environment variables:

```bash
# Optional Image Proxy
--enable-image-proxy bool           Enable HTTP image proxy (default: false)
--image-proxy-port int              Port for image proxy server (default: 8082)
--image-proxy-cache-dir string      Cache directory for proxied images (default: /var/lib/shoal/image-cache)
--image-proxy-cache-size-gb int     Max cache size in GB (default: 50)
--image-proxy-allowed-domains string  Comma-separated list of allowed domains (default: *)

# State Synchronization
--vmedia-sync-interval int          VirtualMedia state sync interval in seconds (default: 60)
--vmedia-sync-enabled bool          Enable periodic state synchronization (default: true)

# Security
--vmedia-url-whitelist string       Comma-separated URL patterns allowed for InsertMedia
--media-server-tls bool      Enable HTTPS for media server (default: false)
--media-server-cert string   TLS certificate file
--vmedia-url-whitelist string       Comma-separated URL patterns allowed for InsertMedia
```

## Testing Strategy

### Unit Tests
- VirtualMedia resource discovery and caching
- Database operations (CRUD for virtual_media_resources and virtual_media_operations)
- Redfish API handlers (collection, resource, actions)
- URL rewriting logic
- Proxy request forwarding

### Integration Tests
- Full InsertMedia/EjectMedia workflow with mock downstream BMC
- Concurrent InsertMedia requests to same VirtualMedia slot
- State synchronization with downstream BMCs
- Image proxy streaming (if enabled)
- Error handling (BMC unreachable, invalid URLs, etc.)

### Manual Testing
- Attach external ISO URL to real BMC via Shoal
- Verify BMC can download and boot from external image
- Test image proxy with isolated BMC (cannot reach internet)
- Verify audit logs capture all operations
- Test UI controls for attach/detach

## Future Considerations

1. **Image Catalog**: Central catalog of common OS images with metadata
2. **Bandwidth Management**: QoS for image proxy to prevent network saturation
3. **Pre-warming**: Pre-download images to BMC cache before boot
4. **Scheduled Operations**: Schedule InsertMedia at specific times (maintenance windows)
5. **Notification Webhooks**: External webhooks when operations complete
6. **Multi-site Support**: Coordinate VirtualMedia across geographically distributed Shoal instances
7. **Image Versioning**: Track image versions and allow rollback
8. **Advanced Caching**: Intelligent caching based on usage patterns
9. **Image Signing**: Cryptographic verification of image integrity

## Open Questions

1. **State Synchronization Frequency**: How often to poll downstream BMCs? Balance freshness vs. API load.
2. **Image Proxy Performance**: Should proxy cache images locally, or always stream from source?
3. **Concurrent Attachments**: Can same external image URL be attached to multiple BMCs simultaneously? (Yes, likely)
4. **Out-of-Band Changes**: How to handle manual VirtualMedia operations on BMC (outside Shoal)?
5. **URL Authentication**: How to securely pass image server credentials to BMCs?
6. **Certificate Trust**: How to handle HTTPS image URLs with self-signed certs?

## References

- [DMTF Redfish VirtualMedia Schema v1.6](https://redfish.dmtf.org/schemas/v1/VirtualMedia.v1_6_2.json)
- [DMTF Redfish Specification](https://www.dmtf.org/standards/redfish)
- [Redfish White Paper: Virtual Media](https://www.dmtf.org/sites/default/files/standards/documents/DSP2046_2019.1.pdf)
- [Cloud-Init Documentation](https://cloudinit.readthedocs.io/)
- [OCI Image Specification](https://github.com/opencontainers/image-spec)

## Conclusion

This design provides a DMTF Redfish-compliant roadmap for implementing virtual media pass-through in Shoal. By aggregating VirtualMedia resources from downstream BMCs and proxying InsertMedia/EjectMedia actions, Shoal enables unified virtual media management across isolated BMC environments.

**Key Design Principles**:
- **DMTF Compliance**: All endpoints follow Redfish specification
- **External Storage**: Images stored on external servers (HTTP, NFS, OCI registries)
- **Proxy Model**: Shoal proxies actions and optionally proxies image downloads
- **State Tracking**: Database tracks VirtualMedia resources and operations
- **Extensible**: Phase 2 features (cloud-init, OCI, etc.) build on solid Phase 1 foundation

The architecture leverages Shoal's bastion role to provide secure access to virtual media capabilities for isolated BMCs, enabling critical use cases like OS installation, system recovery, and automated provisioning without requiring direct external network access from BMCs.
