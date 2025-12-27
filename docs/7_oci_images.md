# OCI Image Support for Virtual Media

## Overview

Shoal supports attaching OCI (Open Container Initiative) images as bootable virtual media to managed BMCs. This feature allows you to use container images from registries as boot media, converting them on-the-fly to bootable ISOs.

## Prerequisites

To use OCI image conversion, your Shoal server must have one of the following tools installed:
- `podman` (recommended)
- `buildah`

Additionally, one of these ISO creation tools must be available:
- `genisoimage` (recommended)
- `mkisofs`
- `xorriso`

## Configuration

Enable OCI image conversion by starting Shoal with the image proxy enabled and specifying an OCI storage directory:

```bash
shoal \
  --enable-image-proxy \
  --image-proxy-port 8082 \
  --oci-storage-dir /var/lib/shoal/oci
```

### Configuration Options

- `--enable-image-proxy`: Enable the HTTP image proxy server (required for OCI conversion)
- `--image-proxy-port`: Port for the image proxy server (default: 8082)
- `--oci-storage-dir`: Directory for storing converted OCI images (default: /var/lib/shoal/oci)

## Usage

### Attaching an OCI Image

To attach an OCI image as virtual media, send a Redfish `InsertMedia` request with:
1. The `Image` field set to an OCI image reference with the `oci://` prefix
2. The `Oem.Shoal.OCIConversion` flag set to `true`

**Example Request:**

```bash
curl -X POST \
  -H "X-Auth-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "Image": "oci://ghcr.io/fedora/coreos:stable",
    "Inserted": true,
    "WriteProtected": true,
    "Oem": {
      "Shoal": {
        "OCIConversion": true
      }
    }
  }' \
  https://shoal.example.com/redfish/v1/Managers/BMC-server01/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia
```

### Supported Image References

Shoal supports standard OCI image reference formats:

- **With explicit tag:** `oci://registry.example.com/namespace/image:tag`
- **With digest:** `oci://registry.example.com/namespace/image@sha256:abc123...`
- **Default registry:** `oci://docker.io/nginx:latest`
- **GitHub Container Registry:** `oci://ghcr.io/owner/repo:tag`

## How It Works

1. **Image Conversion**: When you request to attach an OCI image, Shoal:
   - Pulls the OCI image using podman/buildah
   - Exports the container filesystem
   - Creates a bootable ISO from the filesystem
   - Stores the ISO in the OCI storage directory

2. **Caching**: Converted ISOs are cached to avoid redundant conversions. The cache is keyed by the image reference.

3. **Image Serving**: Shoal serves the converted ISO to the BMC via the image proxy with a secure token.

4. **Automatic Refresh**: For mutable tags (`:latest`, `:stable`, `:main`, `:dev`, `:nightly`), Shoal periodically refreshes the cache (default: every 6 hours).

## Image Requirements

For an OCI image to be bootable as virtual media, it must contain:

- A bootloader in the `isolinux/` directory
- `isolinux/isolinux.bin` - The bootloader binary
- `isolinux/boot.cat` - Boot catalog file

Most bootable container images (like Fedora CoreOS, Ubuntu cloud images, etc.) include these components.

## Caching and Performance

### Cache Behavior

- **Immutable tags**: Images referenced by specific version tags or digest hashes are cached indefinitely.
- **Mutable tags**: Images referenced by mutable tags (`:latest`, `:stable`, etc.) are refreshed periodically.

### Cache Management

The OCI converter includes automatic cache management:

- **Periodic refresh**: Mutable tags are refreshed every 6 hours by default
- **LRU cleanup**: Old images can be cleaned up based on last-used time
- **Manual cleanup**: Administrators can remove unused cached images from the OCI storage directory

## Security Considerations

### Access Control

- All OCI image conversions require authentication
- Converted ISOs are served with secure tokens
- Tokens are unique per conversion and cannot be reused

### Registry Authentication

If pulling from private OCI registries, ensure the Shoal server has appropriate credentials configured for podman/buildah:

```bash
# Configure registry credentials
podman login registry.example.com
```

Credentials are stored in `~/.config/containers/auth.json` (or `/run/containers/0/auth.json` for root).

## Troubleshooting

### "Neither podman nor buildah found in PATH"

Ensure podman or buildah is installed:

```bash
# Fedora/RHEL
sudo dnf install podman

# Ubuntu/Debian
sudo apt-get install podman

# Or install buildah
sudo dnf install buildah
```

### "No ISO creation tool found"

Install genisoimage or xorriso:

```bash
# Fedora/RHEL
sudo dnf install genisoimage

# Ubuntu/Debian
sudo apt-get install genisoimage

# Or install xorriso
sudo dnf install xorriso
```

### "Failed to pull OCI image"

Check:
- Network connectivity to the registry
- Registry authentication (for private registries)
- Image reference syntax

### "OCI image must contain bootloader in isolinux/ directory"

The container image is not bootable. Ensure the image contains:
- `isolinux/isolinux.bin`
- `isolinux/boot.cat`

Not all container images are designed to be bootable. Use images specifically designed for bare-metal booting.

## Example Use Cases

### 1. Fedora CoreOS Installation

```bash
curl -X POST \
  -H "X-Auth-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "Image": "oci://quay.io/fedora/fedora-coreos:stable",
    "Inserted": true,
    "WriteProtected": true,
    "Oem": {
      "Shoal": {
        "OCIConversion": true
      }
    }
  }' \
  https://shoal.example.com/redfish/v1/Managers/server01/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia
```

### 2. Custom Bootable Image

```bash
curl -X POST \
  -H "X-Auth-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "Image": "oci://ghcr.io/myorg/custom-boot-image:v1.2.3",
    "Inserted": true,
    "WriteProtected": true,
    "Oem": {
      "Shoal": {
        "OCIConversion": true
      }
    }
  }' \
  https://shoal.example.com/redfish/v1/Managers/server02/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia
```

### 3. Using Digest for Immutable Deployment

```bash
curl -X POST \
  -H "X-Auth-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "Image": "oci://ghcr.io/myorg/boot-image@sha256:abc123def456...",
    "Inserted": true,
    "WriteProtected": true,
    "Oem": {
      "Shoal": {
        "OCIConversion": true
      }
    }
  }' \
  https://shoal.example.com/redfish/v1/Managers/server03/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia
```

## Limitations

1. **Conversion Time**: Converting large OCI images can take several minutes on first use. Subsequent uses benefit from caching.

2. **Disk Space**: Cached ISO images consume disk space. Monitor the OCI storage directory size.

3. **Bootable Images Only**: Only container images designed to be bootable (with isolinux bootloader) can be used.

4. **Network Requirement**: The Shoal server must have network access to pull images from registries.

## See Also

- [Virtual Media Pass-Through Design](../design/020_Virtual_Media_Pass_Through.md)
- [Cloud-Init ISO Generation](6_cloud_init_isos.md)
- [Deployment Guide](5_deployment.md)
