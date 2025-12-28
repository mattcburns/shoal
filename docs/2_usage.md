# Usage Guide

This guide covers how to use the Shoal web interface and configure BMCs and users.

## Web Interface

Access the web interface at `http://localhost:8080`.

- **Dashboard**: Overview of managed BMCs with status and last seen timestamps.
- **BMC Management**: Complete CRUD operations - add, edit, delete, and enable/disable BMCs.
- **Detailed BMC Status**: Click the "Details" button to view comprehensive information about any BMC, including:
  - **System Information**: Serial number, SKU, power state, model, and manufacturer.
  - **Network Interfaces**: NIC details with MAC addresses and IP addresses.
  - **Storage Devices**: Drive information with capacity, model, serial numbers, and health status.
  - **System Event Log (SEL)**: Recent log entries with severity levels and timestamps.
  - **Settings**: Browse and configure BIOS, network, and storage settings with pagination and filtering.
  - **Virtual Media**: Attach and eject ISO/disk images to virtual media slots.
  - **Console**: Access the BMC's serial console directly in the browser via WebSocket connection.
- **Connection Testing**: Quick connectivity tests for any BMC with a one-click "Test" button.
- **Power Control**: Execute power actions (On, ForceOff, ForceRestart) directly from the web UI.
- **Real-time Feedback**: Success/error messaging for all operations.

## BMC Configuration

When adding BMCs, you provide the base URL that represents the BMC's network address. Shoal automatically handles the Redfish API path construction.

### Address Format

The BMC address should be the base URL **without** the `/redfish/v1` suffix.

**Standard BMCs (Physical Hardware):**
- IP address: `192.168.1.100` → Shoal uses `https://192.168.1.100/redfish/v1/...`
- With protocol: `https://192.168.1.100` or `http://192.168.1.100`
- Hostname: `bmc.example.com` → Shoal uses `https://bmc.example.com/redfish/v1/...`
- With port: `192.168.1.100:8443` → Shoal uses `https://192.168.1.100:8443/redfish/v1/...`

**Mock/Testing BMCs:**
- With path prefix: `https://mock.shoal.cloud/public-rackmount1`
  - Shoal preserves the path and appends: `https://mock.shoal.cloud/public-rackmount1/redfish/v1/...`

### Important Notes

- If no protocol is specified, Shoal defaults to HTTPS.
- The `/redfish/v1` path is automatically appended—don't include it in the address.
- For mock servers or proxies with path prefixes, include the full base path.
- Trailing slashes are automatically handled.

## User Management

### Default Administrator

On first run, Shoal creates a default administrator account:
- **Username**: `admin`
- **Password**: `admin`

**IMPORTANT**: Change the default password immediately after first login.

### User Roles

Shoal implements role-based access control with three user roles:

- **Administrator**: Full system access. Can manage users, configure BMCs, and execute all actions.
- **Operator**: BMC management access. Can view and manage BMCs and execute power control actions, but cannot manage users.
- **Viewer**: Read-only access. Can view BMC status and configuration but cannot make any changes.

### User Operations

- **Web Interface** (administrators only): Navigate to "Manage Users" to add, edit, or delete users.
- **User Profile**: All users can access their profile from the menu to change their own password.

## Serial Console Access

The Console tab on the BMC Details page provides browser-based access to the BMC's serial console.

### Access Requirements

- **Operator** or **Administrator** role required
- Users can only access their own console sessions
- Administrators can view and terminate all active sessions

### Using the Console

1. Navigate to a BMC's Details page
2. Click the **Console** tab
3. Click **Open Console** to create a new console session
4. The terminal will connect via WebSocket and display the serial console output
5. Type directly in the terminal to send commands to the BMC
6. Click **Disconnect** to end the session

### Active Sessions

The Console tab shows all active console sessions:
- Session ID and connection status
- User who created the session
- Creation timestamp
- Administrators can terminate any session using the **Terminate** button

### Technical Details

- Uses xterm.js terminal emulator with responsive sizing
- WebSocket connection at `/ws/console/{sessionId}`
- Session state tracked: connecting → active → disconnected
- Automatic cleanup on disconnect
