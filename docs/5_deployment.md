# Deployment and Operations

This guide covers deployment, security, and troubleshooting.

## Deployment

Shoal is designed for simple deployment as a single, self-contained binary with no external dependencies. For detailed instructions on production builds, systemd services, and Docker, see `DEPLOYMENT.md`.

**Quick Deployment (from source):**
```bash
# Build for production
python3 build.py build

# Copy and run on target server
scp build/shoal user@server:/opt/shoal/
ssh user@server '/opt/shoal/shoal -port 8080 -db /var/lib/shoal/shoal.db'
```

## Releases

Download pre-built binaries from the project's [GitHub Releases](https://github.com/mattcburns/shoal/releases) page.

```bash
# Linux AMD64
curl -L -o shoal "https://github.com/mattcburns/shoal/releases/latest/download/shoal-linux-amd64"
chmod +x shoal && ./shoal
```

## Security

### Password Security

**User Passwords**:
- Hashed using bcrypt.
- Original passwords are never stored or logged.

**BMC Password Encryption**:
- Shoal supports AES-256-GCM encryption for BMC passwords stored in the database.
- To enable, provide an encryption key via the `SHOAL_ENCRYPTION_KEY` environment variable or the `--encryption-key` flag.
- If no key is provided, passwords are stored in plaintext (not recommended for production).
- **IMPORTANT**: The same key must be used consistently. Losing the key means losing access to all BMC passwords.

```bash
# Using environment variable (recommended)
export SHOAL_ENCRYPTION_KEY="your-secret-encryption-key"
./build/shoal
```

### BMC Requirements

- BMCs must support DMTF Redfish API (v1.6.0 or compatible).
- Network connectivity from the Shoal server to BMC management interfaces.
- Valid BMC credentials (username/password).
- Certificate validation is disabled by default to support self-signed certificates, which are common in BMC environments.

## Image Proxy Server

Shoal includes an optional image proxy server that enables BMCs to access external image URLs via Shoal when direct internet access is unavailable. This is particularly useful for Virtual Media operations where BMCs need to download ISO images or other boot media from external sources.

### Enabling the Image Proxy

```bash
# Enable image proxy with default settings
./build/shoal --enable-image-proxy

# Enable with custom configuration
./build/shoal \
  --enable-image-proxy \
  --image-proxy-port 8082 \
  --image-proxy-allowed-domains "*.example.com,files.mycdn.net" \
  --image-proxy-allowed-subnets "192.168.1.0/24,10.0.0.0/8" \
  --image-proxy-rate-limit 10
```

### Configuration Options

- **`--enable-image-proxy`**: Enable the image proxy server (default: false)
- **`--image-proxy-port`**: Port for the image proxy server (default: 8082)
- **`--image-proxy-allowed-domains`**: Comma-separated list of allowed domains
  - Use `*` to allow all domains (default)
  - Use specific domains: `example.com,files.example.org`
  - Use wildcards: `*.example.com` matches any subdomain
- **`--image-proxy-allowed-subnets`**: Comma-separated list of allowed client IP subnets in CIDR notation
  - Example: `192.168.1.0/24,10.0.0.0/8`
  - Leave empty to allow all IPs (default)
- **`--image-proxy-rate-limit`**: Maximum concurrent downloads per IP (default: 10)

### How It Works

1. When you use InsertMedia to attach an ISO to a BMC, Shoal automatically rewrites the image URL to point to its proxy server
2. The BMC downloads the image from Shoal's proxy instead of the original URL
3. Shoal's proxy streams the image from the external source to the BMC

**Example:**
```
Original URL:  http://fileserver.example.com/isos/ubuntu-22.04.iso
Rewritten URL: http://shoal:8082/proxy?url=http%3A%2F%2Ffileserver.example.com%2Fisos%2Fubuntu-22.04.iso
```

### Security Features

The image proxy includes several security protections:

- **SSRF Protection**: Blocks access to private IP ranges (localhost, 10.0.0.0/8, 192.168.0.0/16, 172.16.0.0/12) to prevent Server-Side Request Forgery attacks
- **Domain Whitelisting**: Restrict which external domains can be proxied
- **IP/Subnet Access Control**: Limit which clients can use the proxy (useful to restrict to BMC subnets only)
- **Rate Limiting**: Prevent DoS attacks by limiting concurrent downloads per IP

### Range Request Support

The image proxy fully supports HTTP Range requests, enabling:
- Resumable downloads if a BMC connection is interrupted
- Partial content delivery for BMCs that download in chunks
- Better compatibility with different BMC implementations

## Troubleshooting

### Common Issues

1.  **BMC Connection Failed**:
    - Verify the BMC IP address and network connectivity.
    - Check the BMC credentials.
    - Ensure the Redfish service is enabled on the BMC.

2.  **Database Errors**:
    - Check file permissions for the database file.
    - Verify disk space availability.

3.  **Authentication Issues**:
    - Verify admin credentials (`admin`/`admin` by default).
    - Check if a session token has expired.

### Debug Logging

Enable debug logging to get detailed information about requests and internal operations.

```bash
# Enable debug logging
./build/shoal -log-level debug
```
