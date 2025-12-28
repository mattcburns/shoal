# Shoal Deployment Guide

## Single-File Deployment

Shoal is designed as a single, self-contained binary that includes all necessary assets (templates, CSS, etc.) embedded within the executable. This makes deployment extremely simple - you only need to copy one file to deploy the application.

## Building for Production

### Single Platform Build

To build for the current platform with all assets embedded:

```bash
python build.py clean
python build.py build
```

The resulting binary will be located at `build/shoal` (approximately 12MB).

### Cross-Platform Build

To build for all supported platforms (Linux, Windows, macOS on both x86_64 and ARM64):

```bash
python build.py clean
python build.py build-all
```

To build for a specific platform:

```bash
python build.py build --platform linux/amd64
python build.py build --platform windows/amd64
python build.py build --platform darwin/arm64
```

**Supported Platforms:**
- `linux/amd64` - Linux x86_64 → `build/shoal-linux-amd64`
- `linux/arm64` - Linux ARM64 → `build/shoal-linux-arm64`
- `windows/amd64` - Windows x86_64 → `build/shoal-windows-amd64.exe`
- `darwin/amd64` - macOS Intel → `build/shoal-darwin-amd64`
- `darwin/arm64` - macOS Apple Silicon → `build/shoal-darwin-arm64`

### Build Features

The production build includes:
- **Embedded Assets**: All static files (CSS, templates) are embedded in the binary
- **Static Linking**: Uses pure Go implementations for networking (`netgo`) and user lookups (`osusergo`)
- **Stripped Binary**: Debug symbols removed for smaller size (`-s -w` flags)
- **Optimized**: Built with production optimizations

## Deployment Steps

1. **Build the binary** (on your build machine):
   ```bash
   # Build for current platform
   python build.py build

   # OR build for specific target platform
   python build.py build --platform linux/amd64
   ```

2. **Copy the appropriate binary** to your target server:
   ```bash
   # For current platform build
   scp build/shoal user@server:/opt/shoal/shoal

   # OR for cross-compiled binary (example: Linux x86_64)
   scp build/shoal-linux-amd64 user@server:/opt/shoal/shoal

   chmod +x /opt/shoal/shoal
   ```

3. **Run the application**:
   ```bash
   /opt/shoal/shoal \
     -port 8080 \
     -db /var/lib/shoal/shoal.db \
     -encryption-key "your-secret-key" \
     -log-level info
   ```

## Configuration Options

The binary accepts the following command-line flags:

### Core Configuration
- `-port`: HTTP server port (default: "8080")
- `-db`: SQLite database path (default: "shoal.db")
- `-encryption-key`: Encryption key for BMC passwords (can also use SHOAL_ENCRYPTION_KEY env var)
- `-log-level`: Log level - debug, info, warn, error (default: "info")

### Console Pass-Through Configuration

The console pass-through feature provides secure, audited access to BMC serial consoles. This feature requires operator or admin privileges and includes automatic session management and cleanup.

**Security Features:**
- **Authentication**: All console endpoints require valid session authentication
- **Role-Based Access**: Operator or admin role required (viewers cannot access consoles)
- **Session Ownership**: Users can only access their own console sessions (admins can access any)
- **Automatic Timeouts**: 
  - **Idle Timeout**: Sessions automatically disconnect after 30 minutes of inactivity
  - **Max Duration**: Sessions automatically disconnect after 12 hours
  - **Cleanup Interval**: Background task runs every 5 minutes to clean up expired sessions

**Audit Logging:**
All console operations are logged with structured logging (JSON format). Logs include:
- Console session creation (user, manager, timestamp, console type)
- Session disconnections (user, duration, reason: manual/idle/timeout)
- Authentication failures (user, session ID, remote address)
- Error conditions (connection failures, timeouts, BMC errors)
- Automatic cleanup events (idle duration, total duration)

**Production Security Recommendations:**
1. **TLS/HTTPS Required**: Always deploy Shoal behind a reverse proxy (nginx, Apache) with TLS
2. **WebSocket Security**: Ensure WSS (WebSocket Secure) is used for console connections in production
3. **Network Isolation**: Deploy Shoal in a secure network segment with controlled access to BMC management network
4. **Session Monitoring**: Monitor audit logs for unusual console access patterns
5. **Rate Limiting**: Consider implementing rate limiting at reverse proxy level for `/ws/console/` endpoints
6. **Origin Validation**: WebSocket origin validation is currently permissive; plan to implement strict origin checking before production deployment

**Example Audit Log Entry:**
```json
{
  "time": "2025-12-28T16:30:00Z",
  "level": "INFO",
  "msg": "Console session created",
  "session_id": "abc123",
  "manager": "bmc-server01",
  "user": "admin",
  "console_type": "SerialConsole",
  "connection_method": "cm-001"
}
```

## Systemd Service Example

Create `/etc/systemd/system/shoal.service`:

```ini
[Unit]
Description=Shoal Redfish Aggregator
After=network.target

[Service]
Type=simple
User=shoal
Group=shoal
WorkingDirectory=/opt/shoal
ExecStart=/opt/shoal/shoal -port 8080 -db /var/lib/shoal/shoal.db -log-level info
Restart=on-failure
RestartSec=5
Environment="SHOAL_ENCRYPTION_KEY=your-secret-key-here"

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/shoal

[Install]
WantedBy=multi-user.target
```

Then enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable shoal
sudo systemctl start shoal
```

## Docker Deployment

Since Shoal is a single binary, Docker deployment is very simple:

```dockerfile
FROM scratch
COPY shoal /shoal
EXPOSE 8080
ENTRYPOINT ["/shoal"]
```

Build and run:

```bash
docker build -t shoal .
docker run -d \
  -p 8080:8080 \
  -v /data/shoal:/data \
  -e SHOAL_ENCRYPTION_KEY="your-secret-key" \
  shoal -db /data/shoal.db
```

## Verification

After deployment, verify the installation:

1. Check the service is running:
   ```bash
   curl http://localhost:8080/login
   ```

2. Check logs:
   ```bash
   journalctl -u shoal -f
   ```

## Advantages of Single-File Deployment

- **Simple Distribution**: Only one file to copy, no dependencies
- **Easy Updates**: Replace the binary and restart the service
- **Consistent Deployments**: No missing files or version mismatches
- **Container-Friendly**: Perfect for minimal container images
- **Air-Gap Friendly**: Easy to transfer to isolated networks

## Troubleshooting

If the application doesn't start:

1. Check file permissions: The binary must be executable
2. Check port availability: Ensure the specified port is not in use
3. Check database path: Ensure the database directory is writable
4. Check logs: Use `-log-level debug` for detailed logging

## Build Verification

To verify your build has embedded assets correctly:

```bash
# Check binary size (should be ~12MB)
ls -lh build/shoal

# Verify it's statically linked
file build/shoal
# Should show: "statically linked"

# Test the binary runs
./build/shoal -h
```
