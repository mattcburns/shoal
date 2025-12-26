# 020: Virtual Media Pass-Through

**Author:** GitHub Copilot  
**Date:** 2025-12-26  
**Status:** Proposed

## Abstract

This document outlines the design for implementing Virtual Media Pass-Through in Shoal. As a Redfish aggregator acting as a bastion service, Shoal needs the ability to host and serve ISO images and other virtual media to managed BMCs for operations like OS installation, firmware updates, and system recovery. This design addresses the current limitation where external systems cannot directly reach BMCs, requiring Shoal to act as both an aggregator and a media hosting service.

## Background

### Current State

Shoal currently provides excellent aggregation and pass-through capabilities for HTTP-based Redfish API calls. It discovers and manages BMCs through the `AggregationService`, providing unified access to system information, settings, and power management.

However, virtual media attachment presents a unique challenge:
- BMCs typically attach virtual media (ISOs, disk images) from HTTP/HTTPS URLs
- In Shoal's deployment model, BMCs are isolated and cannot reach external networks
- External systems cannot directly reach BMCs due to network isolation
- Shoal acts as a bastion, providing the only connectivity path to/from BMCs

### Problem Statement

When a user wants to boot a system from an ISO (e.g., for OS installation), they need:
1. A way to upload or reference an ISO image
2. Shoal to host/serve that image over HTTP(S)
3. Shoal to instruct the BMC to attach the virtual media from Shoal's URL
4. Tracking of which media is attached to which systems
5. The ability to detach media when operations are complete

### Use Cases

1. **OS Installation**: Boot multiple systems from an installation ISO
2. **System Recovery**: Attach recovery/diagnostic ISOs to troubleshoot systems
3. **Firmware Updates**: Serve bootable firmware update images
4. **Automated Provisioning**: Integrate with cloud-init or kickstart for automated deployments
5. **Multi-tenancy**: Different users/teams managing different sets of systems with different images

## Redfish Background

Virtual media in Redfish is accessed through the `VirtualMedia` collection under `Manager` resources. Key endpoints and operations:

### Resource Structure

```
/redfish/v1/Managers/{ManagerId}/VirtualMedia
└── /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}
```

### Key Properties

- **`Image`**: The URI of the image to mount (HTTP/HTTPS URL)
- **`ImageName`**: Human-readable name of the mounted image
- **`Inserted`**: Boolean indicating if media is currently inserted
- **`WriteProtected`**: Boolean for write protection status
- **`ConnectedVia`**: How the media is connected (e.g., `URI`, `Applet`)
- **`MediaTypes`**: Supported media types (e.g., `CD`, `DVD`, `USBStick`)

### Actions

- **`#VirtualMedia.InsertMedia`**: Action to attach virtual media
  ```json
  {
    "Image": "http://shoal.example.com/api/media/images/ubuntu-22.04.iso",
    "Inserted": true,
    "WriteProtected": true
  }
  ```
- **`#VirtualMedia.EjectMedia`**: Action to detach virtual media

## Architecture Overview

The solution will be implemented in multiple phases, starting with basic functionality and expanding to advanced features.

### High-Level Components

1. **Media Storage Layer**: Stores and manages ISO/image files
2. **Media HTTP Server**: Serves images to BMCs over HTTP/HTTPS
3. **Media Management API**: RESTful API for upload, listing, deletion
4. **Virtual Media Proxy**: Translates attach/detach operations to downstream BMCs
5. **Attachment Tracking**: Database records of what's attached where
6. **UI Integration**: Web interface for media management

### Data Flow

```
User → Upload ISO → Media Storage → Track in DB
                                   ↓
User → Request Attachment → Media Management API
                                   ↓
                          Call BMC VirtualMedia.InsertMedia
                          with Shoal-hosted image URL
                                   ↓
                          BMC downloads from Shoal → Media HTTP Server
                                   ↓
                          Update attachment status in DB
```

## Phase 1: Basic Virtual Media Hosting and Attachment

### 1.1 Media Storage

**Storage Location**: `/var/lib/shoal/media` (configurable via CLI flag)

**Directory Structure**:
```
/var/lib/shoal/media/
├── images/           # Stored ISO/image files
│   ├── ubuntu-22.04.iso
│   ├── debian-12.iso
│   └── diagnostics.iso
└── metadata/         # Optional metadata files (future)
    └── ubuntu-22.04.json
```

**File Management**:
- Files stored with sanitized names (alphanumeric, hyphens, underscores, dots only)
- MD5/SHA256 checksums computed on upload
- Size limits enforced (configurable, default 10GB per file)
- Total storage quota (configurable, default 100GB)

### 1.2 Database Schema

New table: `virtual_media_images`
```sql
CREATE TABLE virtual_media_images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,           -- User-friendly name
    filename TEXT NOT NULL,               -- Sanitized filename on disk
    size_bytes INTEGER NOT NULL,
    checksum_sha256 TEXT NOT NULL,
    media_type TEXT DEFAULT 'CD',        -- CD, DVD, USBStick, Floppy
    uploaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    uploaded_by TEXT,                     -- Username who uploaded
    description TEXT
);

CREATE INDEX idx_vmi_name ON virtual_media_images(name);
```

New table: `virtual_media_attachments`
```sql
CREATE TABLE virtual_media_attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    image_id INTEGER NOT NULL,
    connection_method_id INTEGER NOT NULL,
    system_id TEXT NOT NULL,              -- e.g., "System.Embedded.1"
    virtual_media_id TEXT NOT NULL,       -- e.g., "CD1", "RemovableDisk"
    attached_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    attached_by TEXT,
    write_protected BOOLEAN DEFAULT 1,
    status TEXT DEFAULT 'attached',       -- attached, detaching, error
    FOREIGN KEY(image_id) REFERENCES virtual_media_images(id) ON DELETE CASCADE,
    FOREIGN KEY(connection_method_id) REFERENCES connection_methods(id) ON DELETE CASCADE,
    UNIQUE(connection_method_id, virtual_media_id)
);

CREATE INDEX idx_vma_image ON virtual_media_attachments(image_id);
CREATE INDEX idx_vma_connection ON virtual_media_attachments(connection_method_id);
```

### 1.3 Media Management API

**Base Path**: `/api/media`

#### Endpoints

**1. List Images**
```
GET /api/media/images
Response: 200 OK
[
  {
    "id": 1,
    "name": "Ubuntu 22.04 Server",
    "filename": "ubuntu-22.04.iso",
    "size_bytes": 1450000000,
    "checksum_sha256": "abc123...",
    "media_type": "CD",
    "uploaded_at": "2025-12-26T10:00:00Z",
    "uploaded_by": "admin",
    "url": "http://shoal.example.com/api/media/images/ubuntu-22.04.iso"
  }
]
```

**2. Upload Image**
```
POST /api/media/images
Content-Type: multipart/form-data

Fields:
  - file: (binary data)
  - name: "Ubuntu 22.04 Server"
  - media_type: "CD" (optional, default CD)
  - description: "Ubuntu installation media" (optional)

Response: 201 Created
{
  "id": 1,
  "name": "Ubuntu 22.04 Server",
  "url": "http://shoal.example.com/api/media/images/ubuntu-22.04.iso"
}
```

**3. Get Image Details**
```
GET /api/media/images/{id}
Response: 200 OK
{
  "id": 1,
  "name": "Ubuntu 22.04 Server",
  "filename": "ubuntu-22.04.iso",
  "size_bytes": 1450000000,
  "checksum_sha256": "abc123...",
  "media_type": "CD",
  "uploaded_at": "2025-12-26T10:00:00Z",
  "attachments": [
    {
      "connection_method_id": 5,
      "bmc_name": "server-01",
      "system_id": "System.Embedded.1",
      "attached_at": "2025-12-26T11:00:00Z"
    }
  ]
}
```

**4. Delete Image**
```
DELETE /api/media/images/{id}
Response: 204 No Content
Error: 409 Conflict (if currently attached)
{
  "error": "Cannot delete image that is currently attached to 2 systems"
}
```

**5. Download/Serve Image** (for BMC consumption)
```
GET /api/media/images/{filename}
Response: 200 OK
Content-Type: application/octet-stream
Content-Length: 1450000000
(Binary ISO data...)
```

**6. List Attachments**
```
GET /api/media/attachments
Response: 200 OK
[
  {
    "id": 1,
    "image_name": "Ubuntu 22.04 Server",
    "bmc_name": "server-01",
    "system_id": "System.Embedded.1",
    "virtual_media_id": "CD1",
    "attached_at": "2025-12-26T11:00:00Z",
    "status": "attached"
  }
]
```

**7. Attach Virtual Media**
```
POST /api/media/attach
{
  "image_id": 1,
  "connection_method_id": 5,
  "system_id": "System.Embedded.1",      // optional, use first system if omitted
  "virtual_media_id": "CD1",              // optional, auto-detect available slot
  "write_protected": true
}

Response: 201 Created
{
  "id": 1,
  "status": "attached",
  "image_url": "http://shoal.example.com/api/media/images/ubuntu-22.04.iso"
}
```

**8. Detach Virtual Media**
```
POST /api/media/detach/{attachment_id}
Response: 200 OK
{
  "status": "detached"
}
```

### 1.4 Media HTTP Server

**Implementation**:
- Use Go's `http.FileServer` with custom handlers
- Support HTTP Range requests for partial downloads
- Rate limiting per BMC to prevent DoS
- Access logging for compliance/debugging
- Optional HTTPS with self-signed or provided certificates

**Security**:
- Serve files only from designated media directory (prevent path traversal)
- Validate all filenames against whitelist pattern
- Optional authentication (BMC credentials or token-based)
- Monitor for unusual access patterns

### 1.5 Virtual Media Proxy Logic

**Discovery Enhancement**:
Extend the existing BMC discovery process to:
1. Query `/redfish/v1/Managers/{id}/VirtualMedia` for each manager
2. Store available virtual media slots and their capabilities
3. Cache supported `MediaTypes` for each slot

**Attachment Process**:
1. Validate image exists and is accessible
2. Determine available virtual media slot on target BMC
3. Construct Shoal-hosted image URL
4. Call `#VirtualMedia.InsertMedia` action on BMC with Shoal URL
5. Record attachment in database
6. Return success/failure to user

**Detachment Process**:
1. Validate attachment exists
2. Call `#VirtualMedia.EjectMedia` action on BMC
3. Update/remove attachment record in database
4. Return success/failure to user

**Error Handling**:
- BMC rejects attachment (URL unreachable, unsupported type)
- Network failure during BMC download
- Concurrent attachment attempts to same slot
- Image deleted while attached (prevent or warn)

### 1.6 UI Integration

**New UI Pages**:

1. **Media Library** (`/media`)
   - Table listing all uploaded images
   - Upload button (file picker with drag-and-drop)
   - Delete button (with confirmation and attachment check)
   - Download link (for verification)
   - Show size, upload date, current attachments

2. **Attach Media Dialog** (from BMC detail page)
   - Dropdown to select from available images
   - Dropdown to select target system (if multiple)
   - Dropdown to select virtual media slot (if multiple)
   - Checkbox for write protection
   - Attach button

3. **Active Attachments** (section on Media Library page)
   - Table showing currently attached media
   - BMC name, system, image name, attached time
   - Detach button for each

**BMC Detail Page Enhancement**:
- Add "Virtual Media" tab or section
- Show available virtual media slots
- Show currently attached media
- Quick attach/detach actions

## Phase 2: Advanced Features

### 2.1 OCI Image Support

**Objective**: Support pulling images from OCI-compliant registries (Docker Hub, GHCR, etc.)

**Design**:
- Add `source_type` field to `virtual_media_images` (local, http, oci)
- For OCI images:
  - Store registry URL, image name, tag
  - Pull image layers on-demand or cache locally
  - Convert OCI image to bootable ISO if needed (use existing tools)
  - Schedule periodic pulls for `latest` tags

**API Extension**:
```
POST /api/media/images/oci
{
  "name": "Fedora CoreOS",
  "oci_url": "ghcr.io/fedora/coreos:stable",
  "auto_update": true
}
```

### 2.2 HTTP Image Proxying

**Objective**: Reference external HTTP(S) images without full download

**Design**:
- Add option to proxy external URLs through Shoal
- Shoal acts as HTTP proxy, streaming image to BMC
- Useful for large images or when storage is limited
- Cache control headers to prevent redundant downloads

**API Extension**:
```
POST /api/media/images/proxy
{
  "name": "Vendor Diagnostics",
  "external_url": "https://vendor.example.com/tools/diag-v2.iso",
  "cache_locally": false
}
```

### 2.3 Cloud-Init / Metadata Injection

**Objective**: Generate and serve cloud-init ISOs for automated provisioning

**Design**:
- User provides cloud-init user-data and meta-data
- Shoal generates ISO with proper structure (`user-data`, `meta-data` files)
- ISO attached as virtual media during boot
- Cloud-init in OS reads configuration from ISO

**Use Case**:
- Automated OS installation with pre-configured network, users, SSH keys
- Integration with provisioning workflows

**API Extension**:
```
POST /api/media/images/cloudinit
{
  "name": "CloudInit for server-01",
  "user_data": "#cloud-config\n...",
  "meta_data": "instance-id: server-01\n..."
}
```

### 2.4 Kickstart / Preseed File Hosting

**Objective**: Serve kickstart (Red Hat/Fedora) or preseed (Debian/Ubuntu) files

**Design**:
- Dedicated endpoint for serving text-based config files
- Template system for generating configs with variables
- Boot parameters on systems reference Shoal URLs

**Endpoints**:
```
GET /api/media/kickstart/{name}
GET /api/media/preseed/{name}
```

### 2.5 Image Templates and Versioning

**Objective**: Manage multiple versions of same image, track lineage

**Design**:
- Tag images with versions (v1.0, v2.1, latest)
- Allow rollback to previous versions
- Track which version was used for which deployment

### 2.6 Multi-tenancy and Access Control

**Objective**: Restrict which users/groups can access which images

**Design**:
- Extend RBAC to include media permissions
- Images can be owned by users or shared with groups
- Attachment permissions enforced (can only attach to BMCs you manage)

## Implementation Milestones

### Milestone 1: Storage and API Foundation
- [ ] Create database schema and migrations
- [ ] Implement media storage directory management
- [ ] Build media upload API endpoint
- [ ] Build media listing and deletion endpoints
- [ ] Add checksum validation
- [ ] Write unit tests for storage layer

### Milestone 2: HTTP Serving
- [ ] Implement media HTTP server
- [ ] Support Range requests
- [ ] Add access logging
- [ ] Implement rate limiting
- [ ] Write integration tests

### Milestone 3: Virtual Media Proxy
- [ ] Enhance BMC discovery to detect virtual media capabilities
- [ ] Implement attach API endpoint and BMC proxy logic
- [ ] Implement detach API endpoint and BMC proxy logic
- [ ] Add attachment tracking to database
- [ ] Handle error cases (BMC unreachable, slot in use, etc.)
- [ ] Write unit tests with mock BMC responses

### Milestone 4: UI Integration
- [ ] Create Media Library page (list, upload, delete)
- [ ] Add attach/detach controls to BMC detail page
- [ ] Display active attachments
- [ ] Add frontend validation (file size, type)
- [ ] Update navigation menu

### Milestone 5: Advanced Features (Phase 2)
- [ ] OCI image support
- [ ] HTTP image proxying
- [ ] Cloud-init ISO generation
- [ ] Kickstart/preseed hosting
- [ ] Image versioning
- [ ] Multi-tenancy RBAC

## Security Considerations

### 1. Input Validation
- **Filename Sanitization**: Prevent path traversal attacks (../../../etc/passwd)
- **File Type Validation**: Verify uploaded files are actually ISO/image formats
- **Size Limits**: Prevent DoS via massive uploads
- **Rate Limiting**: Limit upload frequency per user

### 2. Access Control
- **Authentication**: All media management APIs require authenticated user
- **Authorization**: RBAC enforced (admin can manage all, users their own)
- **BMC Credentials**: Secure storage of credentials for BMC API calls
- **Image Access**: Only authenticated BMCs can download (optional, may complicate setup)

### 3. Storage Security
- **Disk Quotas**: Prevent filling disk with uploads
- **Encryption at Rest**: Optional encryption of stored images (future)
- **Secure Deletion**: Overwrite files on deletion (optional, for sensitive images)
- **Backup and Recovery**: Guidance for backing up media library

### 4. Network Security
- **HTTPS for API**: Encrypt upload/download traffic
- **HTTPS for BMC Serving**: Optional, may require cert trust on BMC side
- **Firewall Rules**: Ensure only BMCs can reach media server ports
- **Audit Logging**: Track all attachment/detachment operations

## Configuration

New CLI flags and environment variables:

```bash
--media-dir string           Directory for storing media files (default: /var/lib/shoal/media)
--media-quota-total int      Total storage quota in GB (default: 100)
--media-quota-per-file int   Per-file size limit in GB (default: 10)
--media-server-port int      Port for media HTTP server (default: 8081)
--media-server-tls bool      Enable HTTPS for media server (default: false)
--media-server-cert string   TLS certificate file
--media-server-key string    TLS key file
```

## Testing Strategy

### Unit Tests
- Storage layer (file operations, checksum, quota enforcement)
- API handlers (upload, list, delete, attach, detach)
- Database operations (CRUD for images and attachments)
- Filename sanitization and validation

### Integration Tests
- Full attach/detach workflow with mock BMC server
- Concurrent attachment attempts
- Image deletion while attached (should fail)
- Quota enforcement on upload

### Manual Testing
- Upload real ISO and attach to real BMC
- Verify BMC can download and boot from image
- Test detachment and re-attachment
- Verify audit logs are created

## Future Considerations

1. **Image Deduplication**: Hash-based storage to avoid storing identical images multiple times
2. **Distributed Storage**: Support for S3, NFS, or other backend storage
3. **Image Streaming**: Stream directly from source without full local copy
4. **Bandwidth Management**: QoS for image serving to prevent network saturation
5. **Image Signing**: Cryptographic verification of image integrity
6. **Pre-warming**: Pre-download images to BMC cache before attachment
7. **Scheduled Attachments**: Attach media at specific times (e.g., maintenance windows)
8. **Notification Webhooks**: Alert when attachment succeeds/fails
9. **Image Catalog**: Public repository of common OS images with auto-download

## Open Questions

1. **BMC Authentication for Image Download**: Should BMCs authenticate to download images, or rely on network isolation?
2. **Image Retention Policy**: How long to keep unused images? Auto-cleanup after N days?
3. **Concurrent Attachments**: Can same image be attached to multiple BMCs simultaneously? (Yes, likely)
4. **Attachment State Sync**: How to detect if BMC detached media outside of Shoal control?
5. **Large File Uploads**: Support resumable uploads for multi-GB ISOs?

## References

- [DMTF Redfish VirtualMedia Schema](https://redfish.dmtf.org/schemas/v1/VirtualMedia.json)
- [Redfish Specification](https://www.dmtf.org/standards/redfish)
- [Cloud-Init Documentation](https://cloudinit.readthedocs.io/)
- [OCI Image Specification](https://github.com/opencontainers/image-spec)

## Conclusion

This design provides a comprehensive roadmap for implementing virtual media pass-through in Shoal. Phase 1 delivers essential functionality for hosting and attaching ISO images, enabling critical use cases like OS installation and system recovery. Phase 2 expands capabilities with advanced features like OCI support and cloud-init integration, positioning Shoal as a complete provisioning solution.

The architecture is designed to be extensible, secure, and consistent with Shoal's existing patterns. By leveraging Shoal's bastion role, organizations can safely boot and provision isolated BMC-managed systems without exposing them to external networks.
