# Cloud-Init ISO Generation

Shoal supports on-demand generation of cloud-init ISOs through a Redfish OEM extension. This feature enables automated server provisioning by generating ephemeral NoCloud-compatible cloud-init ISOs that are served securely to BMCs.

## Overview

When a user wants to provision a system with cloud-init, instead of pre-creating and hosting ISO files, they can provide the cloud-init `user-data` and optional `meta-data` directly through the Redfish InsertMedia action. Shoal will:

1. Generate a NoCloud-compatible cloud-init ISO
2. Create a secure, time-limited download URL
3. Serve the ISO to the BMC via the image proxy
4. Clean up expired ISOs automatically

## Requirements

- Shoal must be run with the `--enable-image-proxy` flag
- The `genisoimage` tool must be installed on the system running Shoal
- A storage directory for generated ISOs (default: `/var/lib/shoal/cloud-init`)

## Configuration

Enable the image proxy and configure cloud-init storage when starting Shoal:

```bash
./shoal \
  --enable-image-proxy \
  --image-proxy-port 8082 \
  --cloud-init-storage-dir /var/lib/shoal/cloud-init
```

### Configuration Options

| Flag | Default | Description |
|------|---------|-------------|
| `--enable-image-proxy` | `false` | Enable the image proxy server |
| `--image-proxy-port` | `8082` | Port for the image proxy server |
| `--cloud-init-storage-dir` | `/var/lib/shoal/cloud-init` | Directory for storing generated ISOs |
| `--image-proxy-allowed-domains` | `*` | Allowed domains for proxied images |
| `--image-proxy-allowed-subnets` | `` | IP subnets allowed to access the proxy |
| `--image-proxy-rate-limit` | `10` | Max concurrent downloads per IP |

## API Usage

### InsertMedia with Cloud-Init OEM Extension

To generate and attach a cloud-init ISO, send a POST request to the InsertMedia action with the `Oem.Shoal.GenerateCloudInit` extension:

```bash
POST /redfish/v1/Managers/{ManagerId}/VirtualMedia/{MediaId}/Actions/VirtualMedia.InsertMedia
Content-Type: application/json
X-Auth-Token: {your-session-token}

{
  "Image": "placeholder-will-be-replaced",
  "Inserted": true,
  "WriteProtected": true,
  "Oem": {
    "Shoal": {
      "GenerateCloudInit": true,
      "UserData": "#cloud-config\nhostname: webserver01\nusers:\n  - name: admin\n    ssh_authorized_keys:\n      - ssh-rsa AAAAB3...\n    sudo: ALL=(ALL) NOPASSWD:ALL\n",
      "MetaData": "instance-id: server-webserver01\nlocal-hostname: webserver01\n"
    }
  }
}
```

### OEM Extension Fields

| Field | Required | Description |
|-------|----------|-------------|
| `Oem.Shoal.GenerateCloudInit` | Yes | Set to `true` to enable cloud-init ISO generation |
| `Oem.Shoal.UserData` | Yes | Cloud-init user-data in YAML format |
| `Oem.Shoal.MetaData` | No | Cloud-init meta-data (defaults to basic instance-id if not provided) |

### Complete Example with curl

```bash
# Create a session
SESSION=$(curl -s -X POST http://localhost:8080/redfish/v1/SessionService/Sessions \
  -H "Content-Type: application/json" \
  -d '{"UserName":"admin","Password":"admin"}' \
  | jq -r '.Id')

# Prepare cloud-init user-data
USER_DATA="#cloud-config
hostname: webserver01
users:
  - name: admin
    ssh_authorized_keys:
      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
packages:
  - nginx
  - git
runcmd:
  - systemctl enable nginx
  - systemctl start nginx
"

# Generate and attach cloud-init ISO
curl -X POST http://localhost:8080/redfish/v1/Managers/MyBMC/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia \
  -H "Content-Type: application/json" \
  -H "X-Auth-Token: $SESSION" \
  -d @- <<EOF
{
  "Image": "http://localhost:8082/cloudinit-iso",
  "Inserted": true,
  "WriteProtected": true,
  "Oem": {
    "Shoal": {
      "GenerateCloudInit": true,
      "UserData": $(echo "$USER_DATA" | jq -Rs .),
      "MetaData": "instance-id: webserver01\nlocal-hostname: webserver01\n"
    }
  }
}
EOF
```

## Security Features

### Time-Limited URLs

Generated cloud-init ISOs are served with time-limited tokens that expire after 1 hour by default. Each ISO download URL includes:

- A unique ISO ID
- A cryptographically secure random token
- Automatic expiration checking

Example generated URL:
```
http://localhost:8082/cloudinit-iso/a1b2c3d4e5f6g7h8?token=9i0j1k2l3m4n5o6p7q8r9s0t1u2v3w4x
```

### One-Time Download (Optional)

ISOs can be marked as downloaded to prevent reuse. The cleanup process removes expired ISOs and their files automatically.

### IP-Based Access Control

The image proxy can be configured to only accept requests from specific IP subnets:

```bash
./shoal \
  --enable-image-proxy \
  --image-proxy-allowed-subnets "10.0.0.0/8,192.168.1.0/24"
```

## NoCloud Data Source Format

Generated ISOs conform to the cloud-init NoCloud data source specification:

- Volume label: `cidata`
- Files:
  - `user-data`: Cloud-init configuration
  - `meta-data`: Instance metadata
- Filesystem: ISO 9660 with Joliet and Rock Ridge extensions

This format is compatible with most Linux distributions that support cloud-init, including:
- Ubuntu
- Debian
- RHEL/CentOS/Rocky Linux
- Fedora
- openSUSE

## Troubleshooting

### ISO Generation Fails

**Error**: `genisoimage failed: exec: "genisoimage": executable file not found in $PATH`

**Solution**: Install genisoimage:
```bash
# Ubuntu/Debian
sudo apt-get install genisoimage

# RHEL 8+/CentOS Stream/Rocky Linux
sudo dnf install genisoimage

# RHEL 7/CentOS 7 (older)
sudo yum install genisoimage

# Fedora
sudo dnf install genisoimage

# openSUSE/SLES
sudo zypper install mkisofs
```

### BMC Cannot Download ISO

**Symptoms**: BMC reports image download failure

**Possible causes**:
1. **Network connectivity**: Ensure the BMC can reach the Shoal image proxy server
2. **Firewall rules**: Check that the image proxy port (default 8082) is accessible
3. **IP restrictions**: Verify the BMC's IP is in the allowed subnets
4. **Token expiration**: The ISO URL token may have expired (1 hour default)

### Service Unavailable Error

**Error**: `Cloud-init ISO generation not enabled`

**Solution**: Start Shoal with `--enable-image-proxy` flag

## Cleanup and Maintenance

Expired ISOs are automatically cleaned up through periodic background tasks. You can also manually remove old ISOs:

```bash
# List generated ISOs
ls -lh /var/lib/shoal/cloud-init/

# Remove old ISOs (example)
find /var/lib/shoal/cloud-init/ -name "*.iso" -mtime +1 -delete
```

## Example User-Data Templates

### Basic Server Setup

```yaml
#cloud-config
hostname: myserver
manage_etc_hosts: true

users:
  - name: admin
    groups: sudo
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - ssh-rsa AAAAB3...

packages:
  - vim
  - htop
  - curl

runcmd:
  - echo "Server setup complete" > /etc/motd
```

### Docker Host

```yaml
#cloud-config
hostname: docker-host

users:
  - name: docker-admin
    groups: sudo,docker
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - ssh-rsa AAAAB3...

packages:
  - docker.io
  - docker-compose

runcmd:
  - systemctl enable docker
  - systemctl start docker
  - usermod -aG docker docker-admin
```

### Kubernetes Node

```yaml
#cloud-config
hostname: k8s-node-01

packages:
  - docker.io
  - kubelet
  - kubeadm
  - kubectl

runcmd:
  - systemctl enable docker
  - systemctl start docker
  - swapoff -a
  - sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab
```

## See Also

- [Virtual Media Pass-Through Design](../design/020_Virtual_Media_Pass_Through.md)
- [Cloud-Init Documentation](https://cloudinit.readthedocs.io/)
- [NoCloud Data Source](https://cloudinit.readthedocs.io/en/latest/reference/datasources/nocloud.html)
