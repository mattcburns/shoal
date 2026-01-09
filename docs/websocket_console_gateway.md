# WebSocket Gateway for Console Access

This document describes the WebSocket gateway implementation for console pass-through in Shoal, supporting both serial and graphical console access.

## Overview

The WebSocket gateway enables browser-based console access to BMCs through Shoal. It provides bidirectional WebSocket tunnels between the user's browser and the BMC's console WebSocket endpoints for both text-based serial consoles and graphical KVM consoles.

## Architecture

```
User Browser ←→ Shoal WebSocket Gateway ←→ BMC Console WebSocket
  (xterm.js           (/ws/console/{id})        (Redfish OEM endpoint)
   or KVM client)
```

### Components

1. **API Handler** (`internal/api/console.go`):
   - Creates console sessions (serial and graphical)
   - Upgrades HTTP connections to WebSocket
   - Manages session lifecycle

2. **BMC Session Handlers**:
   - `internal/bmc/console_session.go`: Serial console WebSocket proxy
   - `internal/bmc/graphical_console_session.go`: Graphical console WebSocket proxy
   - Connects to BMC's WebSocket endpoint
   - Proxies data bidirectionally (text and binary)
   - Handles disconnections and errors

3. **Database Layer** (`internal/database`):
   - Stores console capabilities (serial and graphical)
   - Tracks active sessions
   - Manages session state

## Console Types

### Serial Console

Text-based serial console access for command-line interaction, BIOS configuration, and boot debugging.

**Typical Use Cases:**
- Emergency troubleshooting when network/OS is down
- BIOS/UEFI configuration during POST
- Boot debugging and kernel panic analysis
- Firmware update monitoring

### Graphical Console

KVM/HTML5 console access for GUI-based interaction with video, keyboard, and mouse support.

**Typical Use Cases:**
- OS installation with graphical installer
- GUI-based BIOS/UEFI configuration
- Remote desktop management
- Visual troubleshooting

**Supported Vendors:**
- **Dell iDRAC**: Virtual KVM (vKVM)
- **Supermicro**: HTML5 iKVM
- **HPE iLO**: Integrated Remote Console (IRC)

## API Endpoints

### Create Serial Console Session

```http
POST /redfish/v1/Managers/{ManagerId}/Actions/Oem/Shoal.ConnectSerialConsole
```

**Request:**
```json
{
  "ConnectType": "Oem"
}
```

**Response (201 Created):**
```json
{
  "@odata.type": "#ShoalConsoleSession.v1_0_0.ConsoleSession",
  "@odata.id": "/redfish/v1/Managers/BMC1/Oem/Shoal/ConsoleSessions/{SessionId}",
  "Id": "{SessionId}",
  "ConsoleType": "SerialConsole",
  "ConnectType": "Oem",
  "State": "Connecting",
  "WebSocketURI": "/ws/console/{SessionId}"
}
```

### Create Graphical Console Session

```http
POST /redfish/v1/Managers/{ManagerId}/Actions/Oem/Shoal.ConnectGraphicalConsole
```

**Request:**
```json
{
  "ConnectType": "Oem"
}
```

**Response (201 Created):**
```json
{
  "@odata.type": "#ShoalConsoleSession.v1_0_0.ConsoleSession",
  "@odata.id": "/redfish/v1/Managers/BMC1/Oem/Shoal/ConsoleSessions/{SessionId}",
  "Id": "{SessionId}",
  "ConsoleType": "GraphicalConsole",
  "ConnectType": "Oem",
  "State": "Connecting",
  "WebSocketURI": "/ws/console/{SessionId}"
}
```

### WebSocket Connection

```
WebSocket: wss://shoal.example.com/ws/console/{SessionId}
```

**Headers:**
- `X-Auth-Token`: Valid Shoal session token

**Protocol:**
- Text or binary messages (supports both serial and graphical console data)
- Bidirectional data flow
- Serial console: Text-based terminal I/O
- Graphical console: Binary video frames, keyboard/mouse events

**Data Types:**
- **Serial Console**: Primarily text messages with terminal control sequences
- **Graphical Console**: Binary WebSocket messages containing:
  - Video frame data (compressed or raw)
  - Keyboard event payloads
  - Mouse movement and click events
  - Display control messages

### Get Console Session

```http
GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}
```

**Response:**
```json
{
  "@odata.type": "#ShoalConsoleSession.v1_0_0.ConsoleSession",
  "Id": "{SessionId}",
  "Name": "Serial Console Session",
  "ConsoleType": "SerialConsole",  // or "GraphicalConsole"
  "State": "Active",
  "CreatedBy": "admin",
  "CreatedTime": "2025-12-28T05:00:00Z",
  "LastActivityTime": "2025-12-28T05:30:00Z",
  "WebSocketURI": "/ws/console/{SessionId}",
  "Actions": {
    "#ConsoleSession.Disconnect": {
      "target": "/redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}/Actions/Disconnect"
    }
  }
}
```

**Console Types:**
- `SerialConsole`: Text-based serial console session
- `GraphicalConsole`: KVM/HTML5 graphical console session

### List Console Sessions

```http
GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions
```

### Disconnect Console Session

```http
POST /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}/Actions/Disconnect
```

## Session Lifecycle

1. **Creating**: User POSTs to `ConnectSerialConsole` or `ConnectGraphicalConsole` action
2. **Connecting**: Shoal queries BMC for vendor-specific WebSocket URL and establishes connection
3. **Active**: User connects to Shoal's WebSocket endpoint, data flows bidirectionally
4. **Disconnected**: User closes WebSocket or calls Disconnect action
5. **Error**: Connection to BMC fails or encounters error

**State Transitions:**
```
Creating → Connecting → Active → Disconnected
                ↓
              Error
```

## Authentication & Authorization

- **Operator Role Required**: Only users with Operator or Administrator role can create console sessions
- **Session Ownership**: Users can only connect to their own console sessions (Administrators can access any session)
- **WebSocket Authentication**: WebSocket upgrade requires valid `X-Auth-Token` header

## Concurrency & Limits

- **Max Sessions**: Enforced per BMC based on `MaxConcurrentSessions` from console capability
- **Session Timeout**: Sessions are terminated after idle timeout (configurable)
- **Cleanup**: Disconnected sessions are cleaned up after retention period

## Error Handling

### Connection Errors
- BMC unreachable: Session state → Error
- WebSocket handshake failure: User receives 503 error
- Invalid session ID: User receives 404 error

### Runtime Errors
- BMC WebSocket closes: User WebSocket is closed gracefully
- User WebSocket closes: BMC WebSocket is closed gracefully
- Network interruption: Both connections are terminated, session state → Disconnected

## Testing

### Unit Tests
- `internal/api/console_test.go`: API handler tests for serial console
- `internal/api/graphical_console_test.go`: API handler tests for graphical console
- `internal/bmc/console_session_test.go`: BMC serial console session handler tests
- `internal/bmc/graphical_console_session_test.go`: BMC graphical console session handler tests
- `internal/database/console_test.go`: Database operations tests

### Integration Tests
- `test/integration/console_websocket_test.go`:
  - End-to-end WebSocket gateway test
  - Authentication verification
  - Concurrent session limits
  - Special characters and data handling

## Usage Example

### Serial Console with cURL and wscat

```bash
# 1. Login to get session token
curl -X POST http://localhost:8080/redfish/v1/SessionService/Sessions \
  -H "Content-Type: application/json" \
  -d '{"UserName":"admin","Password":"admin"}' \
  -i

# Extract X-Auth-Token from response headers
TOKEN="<your-token>"

# 2. Create serial console session
curl -X POST http://localhost:8080/redfish/v1/Managers/BMC1/Actions/Oem/Shoal.ConnectSerialConsole \
  -H "X-Auth-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ConnectType":"Oem"}'

# Extract SessionId from response
SESSION_ID="<session-id>"

# 3. Connect to WebSocket
wscat -c "ws://localhost:8080/ws/console/$SESSION_ID" \
  -H "X-Auth-Token: $TOKEN"

# 4. Type commands in the terminal and see BMC serial console output
```

### Graphical Console with cURL

```bash
# 1. Login to get session token (same as above)
TOKEN="<your-token>"

# 2. Create graphical console session
curl -X POST http://localhost:8080/redfish/v1/Managers/BMC1/Actions/Oem/Shoal.ConnectGraphicalConsole \
  -H "X-Auth-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ConnectType":"Oem"}'

# Extract SessionId from response
SESSION_ID="<session-id>"

# 3. Connect to WebSocket (browser-based KVM client required)
# WebSocket URL: ws://localhost:8080/ws/console/$SESSION_ID
# Include X-Auth-Token header in WebSocket upgrade request
```

### Using Browser (JavaScript) - Serial Console

```javascript
// Create serial console session
const response = await fetch('/redfish/v1/Managers/BMC1/Actions/Oem/Shoal.ConnectSerialConsole', {
  method: 'POST',
  headers: {
    'X-Auth-Token': sessionToken,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({ ConnectType: 'Oem' })
});

const session = await response.json();
const sessionId = session.Id;

// Connect to WebSocket
const ws = new WebSocket(`wss://${window.location.host}/ws/console/${sessionId}`);
ws.addEventListener('open', () => {
  console.log('Serial console WebSocket connected');
});

ws.addEventListener('message', (event) => {
  console.log('Console output:', event.data);
  // Display in terminal emulator (e.g., xterm.js)
});

// Send data
ws.send('ls -la\n');
```

### Using Browser (JavaScript) - Graphical Console

```javascript
// Create graphical console session
const response = await fetch('/redfish/v1/Managers/BMC1/Actions/Oem/Shoal.ConnectGraphicalConsole', {
  method: 'POST',
  headers: {
    'X-Auth-Token': sessionToken,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({ ConnectType: 'Oem' })
});

const session = await response.json();
const sessionId = session.Id;

// Connect to WebSocket
const ws = new WebSocket(`wss://${window.location.host}/ws/console/${sessionId}`);
ws.binaryType = 'arraybuffer'; // Important for binary data

ws.addEventListener('open', () => {
  console.log('Graphical console WebSocket connected');
});

ws.addEventListener('message', (event) => {
  if (event.data instanceof ArrayBuffer) {
    // Binary data: video frames, etc.
    handleVideoFrame(event.data);
  } else {
    // Text data: control messages
    console.log('Control message:', event.data);
  }
});

// Send keyboard event
ws.send(JSON.stringify({
  type: 'keyboard',
  keyCode: 13, // Enter key
  pressed: true
}));

// Send mouse event
ws.send(JSON.stringify({
  type: 'mouse',
  x: 100,
  y: 200,
  buttons: 1 // Left click
}));
```

## BMC Compatibility

### Dell iDRAC
**Serial Console:**
- WebSocket endpoint: `/redfish/v1/Dell/Managers/{id}/SerialConsole`
- Authentication: Basic Auth via WebSocket upgrade
- Tested with: iDRAC 9

**Graphical Console (vKVM):**
- WebSocket endpoint: `/redfish/v1/Dell/Managers/{id}/DellvKVM`
- Authentication: Basic Auth via WebSocket upgrade
- Protocol: Binary WebSocket (video, keyboard, mouse)
- Tested with: iDRAC 9

### Supermicro
**Serial Console:**
- WebSocket endpoint: Vendor-specific OEM path
- Authentication: Basic Auth
- Status: Partial support (depends on firmware version)

**Graphical Console (iKVM):**
- WebSocket endpoint: `/redfish/v1/Oem/Supermicro/iKVM`
- Authentication: Basic Auth
- Protocol: HTML5-based KVM
- Status: Supported (firmware version dependent)

### HPE iLO
**Serial Console:**
- WebSocket endpoint: `/redfish/v1/Managers/{id}/SerialConsole`
- Authentication: Basic Auth
- Status: Planned support

**Graphical Console (IRC):**
- WebSocket endpoint: `/redfish/v1/Managers/{id}/RemoteConsole`
- Authentication: Basic Auth
- Protocol: HTML5 Integrated Remote Console
- Status: Supported

## Security Considerations

1. **TLS Required**: Use WSS (WebSocket Secure) in production
2. **BMC Credentials**: Never exposed to end users; Shoal manages BMC authentication
3. **Network Isolation**: BMCs should be on isolated management network
4. **Audit Logging**: All console session creation/termination events are logged
5. **Session Isolation**: Each user gets independent WebSocket connection

## Performance

- **Latency**: Typical user keystroke to BMC < 50ms
- **Throughput**: Supports full serial console bandwidth
- **Concurrent Sessions**: Limited by BMC capability (typically 1-4 per BMC)
- **Resource Usage**: ~1-2MB memory per active session

## Troubleshooting

### WebSocket Connection Fails
1. Check authentication token is valid
2. Verify session state is "Active" (GET session resource)
3. Check BMC connectivity from Shoal server
4. Review Shoal logs for connection errors

### No Data Received
1. Verify BMC serial console is enabled
2. Check BMC WebSocket URL is correct (vendor-specific)
3. Test BMC console directly if possible
4. Check firewall rules between Shoal and BMC

### Session Disconnects Unexpectedly
1. Check idle timeout settings
2. Verify network stability
3. Review BMC logs for issues
4. Check max concurrent sessions limit

## Future Enhancements

- Session recording and playback
- Copy/paste support for graphical consoles
- Binary data handling improvements
- Session reconnection with state preservation
- Multi-user collaborative console viewing
- Mobile-optimized console UI
- Clipboard integration for graphical KVM
- Resolution adjustment for graphical consoles
