# WebSocket Gateway for Serial Console

This document describes the WebSocket gateway implementation for serial console pass-through in Shoal.

## Overview

The WebSocket gateway enables browser-based serial console access to BMCs through Shoal. It provides a bidirectional WebSocket tunnel between the user's browser and the BMC's serial console WebSocket endpoint.

## Architecture

```
User Browser ←→ Shoal WebSocket Gateway ←→ BMC Serial Console WebSocket
  (xterm.js)         (/ws/console/{id})           (Redfish OEM endpoint)
```

### Components

1. **API Handler** (`internal/api/console.go`):
   - Creates console sessions
   - Upgrades HTTP connections to WebSocket
   - Manages session lifecycle

2. **BMC Session Handler** (`internal/bmc/console_session.go`):
   - Connects to BMC's WebSocket endpoint
   - Proxies data bidirectionally
   - Handles disconnections and errors

3. **Database Layer** (`internal/database`):
   - Stores console capabilities
   - Tracks active sessions
   - Manages session state

## API Endpoints

### Create Console Session

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

### WebSocket Connection

```
WebSocket: wss://shoal.example.com/ws/console/{SessionId}
```

**Headers:**
- `X-Auth-Token`: Valid Shoal session token

**Protocol:**
- Text or binary messages
- Bidirectional data flow
- Echo of all data from BMC serial console

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
  "ConsoleType": "SerialConsole",
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

### List Console Sessions

```http
GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions
```

### Disconnect Console Session

```http
POST /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}/Actions/Disconnect
```

## Session Lifecycle

1. **Creating**: User POSTs to `ConnectSerialConsole` action
2. **Connecting**: Shoal queries BMC for WebSocket URL and establishes connection
3. **Active**: User connects to Shoal's WebSocket endpoint, data flows bidirectionally
4. **Disconnected**: User closes WebSocket or calls Disconnect action
5. **Error**: Connection to BMC fails or encounters error

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
- `internal/api/console_test.go`: API handler tests
- `internal/bmc/console_session_test.go`: BMC session handler tests
- `internal/database/console_test.go`: Database operations tests

### Integration Tests
- `test/integration/console_websocket_test.go`:
  - End-to-end WebSocket gateway test
  - Authentication verification
  - Concurrent session limits
  - Special characters and data handling

## Usage Example

### Using cURL and wscat

```bash
# 1. Login to get session token
curl -X POST http://localhost:8080/redfish/v1/SessionService/Sessions \
  -H "Content-Type: application/json" \
  -d '{"UserName":"admin","Password":"admin"}' \
  -i

# Extract X-Auth-Token from response headers
TOKEN="<your-token>"

# 2. Create console session
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

### Using Browser (JavaScript)

```javascript
// Create console session
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
  // Set authentication header (not standard, may need alternative approach)
  console.log('WebSocket connected');
});

ws.addEventListener('message', (event) => {
  console.log('Received:', event.data);
  // Display in terminal emulator (e.g., xterm.js)
});

// Send data
ws.send('ls -la\n');
```

## BMC Compatibility

### Dell iDRAC
- WebSocket endpoint: `/redfish/v1/Dell/Managers/{id}/SerialConsole`
- Authentication: Basic Auth via WebSocket upgrade
- Tested with: iDRAC 9

### Supermicro
- WebSocket endpoint: Vendor-specific OEM path
- Authentication: Basic Auth
- Status: Partial support (depends on firmware version)

### HPE iLO
- WebSocket endpoint: `/redfish/v1/Managers/{id}/SerialConsole`
- Authentication: Basic Auth
- Status: Planned support

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
- Copy/paste support
- Binary data handling improvements
- Session reconnection with state preservation
- Multi-user collaborative console viewing
- Mobile-optimized console UI
