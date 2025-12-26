# 021: Console Pass-Through

**Author:** GitHub Copilot  
**Date:** 2025-12-26  
**Status:** Proposed

## Abstract

This document outlines the design for implementing DMTF Redfish-compliant console pass-through functionality in Shoal. As a Redfish aggregator acting as a bastion service, Shoal will provide unified, secure access to serial console and remote graphical console capabilities across managed BMCs through their Redfish APIs. This design enables direct console access to isolated systems for troubleshooting, installation, and management tasks without requiring direct network access to BMCs. The implementation leverages vendor Redfish OEM extensions and standard Redfish console properties, with initial focus on Dell iDRAC and Supermicro BMC Redfish implementations.

## Background

### Current State

Shoal currently provides excellent aggregation and pass-through capabilities for HTTP-based Redfish API calls. It discovers and manages BMCs through the `AggregationService`, providing unified access to system information, settings, power management, and virtual media.

However, console access capabilities are not yet exposed through Shoal's aggregation layer:
- BMCs provide console access through Serial Console and Graphical Console interfaces via Redfish
- Modern BMCs expose these capabilities through Redfish OEM extensions (WebSocket URLs, connection endpoints)
- In Shoal's deployment model, BMCs are isolated and users cannot reach them directly
- Shoal acts as a bastion, providing the only connectivity path to/from BMCs

### Problem Statement

When a user needs console access to a system (e.g., for troubleshooting, BIOS configuration, OS installation), they need:
1. Access to console resources through Shoal's unified Redfish API
2. Ability to establish persistent console sessions (serial console, graphical console)
3. Shoal to proxy console connections to the correct downstream BMC's Redfish endpoints
4. Support for both text-based (serial) and graphical (KVM) console sessions
5. Proper session management, authentication, and security

**Key Constraint**: Console sessions are interactive and persistent. Unlike typical HTTP requests, console connections require:
- Long-lived bidirectional communication channels
- WebSocket or HTTP streaming support from BMC Redfish APIs
- Session state management
- Proper cleanup on disconnection

### Use Cases

1. **Emergency Troubleshooting**: Access server console when network/OS is down
2. **BIOS Configuration**: Access BIOS/UEFI settings during POST
3. **OS Installation**: Monitor and interact with OS installer
4. **Firmware Updates**: Monitor firmware update progress and handle prompts
5. **Boot Debugging**: View boot messages and kernel panics
6. **Out-of-Band Management**: Access system console when network/OS is unavailable
7. **Remote KVM**: Graphical console access for GUI-based installations and troubleshooting

### Vendor Console Capabilities via Redfish

Different BMC vendors provide varying console capabilities through their Redfish APIs:

**Dell iDRAC**:
- Virtual Console (HTML5-based KVM) via Redfish OEM extensions
- Serial console access through Redfish OEM WebSocket endpoints
- Redfish `SerialConsole` and `GraphicalConsole` properties with OEM action endpoints
- iDRAC provides `/redfish/v1/Dell/Managers/{id}/DellvKVM` and serial console WebSocket URLs

**Supermicro BMC**:
- HTML5 iKVM console via Redfish OEM extensions
- Serial console through Redfish web interface
- Redfish `SerialConsole` and `GraphicalConsole` properties (firmware version dependent)
- OEM endpoints for console session establishment

**HPE iLO**:
- Integrated Remote Console (IRC) via Redfish OEM extensions
- Serial console through Redfish WebSocket endpoints
- Full Redfish console support with OEM actions

## Redfish Background

Console access in Redfish is defined through properties on the `Manager` resource, as specified by the DMTF Redfish specification.

### Manager Console Properties (DMTF Standard)

Per the DMTF `Manager` schema v1.10+:

**SerialConsole** (Text-based serial console):
```json
{
  "SerialConsole": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 1,
    "ConnectTypesSupported": ["Oem"]
  }
}
```

**GraphicalConsole** (KVM/Virtual Console):
```json
{
  "GraphicalConsole": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 4,
    "ConnectTypesSupported": ["KVMIP", "Oem"]
  }
}
```

### Console Connection Process

The Redfish specification does not define a standard API for establishing console connections. Instead, it provides:
1. **Discovery**: Properties on Manager resource indicating available console types
2. **OEM Extensions**: Vendor-specific Redfish endpoints and actions for establishing connections
3. **WebSocket/Streaming**: Many modern BMCs expose WebSocket URLs or streaming endpoints through OEM extensions

### Shoal's Approach

Since DMTF Redfish does not standardize console connection establishment, Shoal will:
1. **Aggregate console capabilities** from Manager resources (standard Redfish)
2. **Provide OEM extension endpoints** for establishing console sessions
3. **Proxy Redfish console endpoints** from downstream BMCs (WebSocket URLs, OEM actions)
4. **Expose unified WebSocket interface** for browser-based console access

## Architecture Overview

Shoal will expose console resources through DMTF Redfish-compliant endpoints and proxy Redfish OEM console endpoints from downstream BMCs for establishing and maintaining console sessions.

### High-Level Components

1. **Console Capability Discovery**: Aggregate SerialConsole and GraphicalConsole properties from managed BMCs
2. **Session Management**: Track active console sessions and their state
3. **Redfish Proxy**: Proxy Redfish OEM console endpoints (WebSocket URLs, actions) from downstream BMCs
4. **WebSocket Gateway**: Browser-accessible console interface that proxies to BMC WebSocket endpoints
5. **Authentication & Authorization**: Secure console access with proper credential management

### DMTF-Compliant API Endpoints

All endpoints follow DMTF Redfish specification where applicable:

**1. Manager Resource with Console Properties**
```
GET /redfish/v1/Managers/{ManagerId}
```
Returns Manager resource including console properties (standard Redfish):
```json
{
  "@odata.type": "#Manager.v1_10_0.Manager",
  "@odata.id": "/redfish/v1/Managers/BMC-server01",
  "Id": "BMC-server01",
  "Name": "Manager for Server 01",
  "SerialConsole": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 1,
    "ConnectTypesSupported": ["Oem"]
  },
  "GraphicalConsole": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 4,
    "ConnectTypesSupported": ["KVMIP", "Oem"]
  },
  "Oem": {
    "Shoal": {
      "@odata.type": "#ShoalManager.v1_0_0.ShoalManager",
      "ConsoleActions": {
        "#Manager.ConnectSerialConsole": {
          "target": "/redfish/v1/Managers/BMC-server01/Actions/Oem/Shoal.ConnectSerialConsole"
        },
        "#Manager.ConnectGraphicalConsole": {
          "target": "/redfish/v1/Managers/BMC-server01/Actions/Oem/Shoal.ConnectGraphicalConsole"
        }
      }
    }
  }
}
```

**2. Console Session Collection (OEM Extension)**
```
GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions
```
Returns collection of active console sessions for this Manager.

**3. Console Session Resource (OEM Extension)**
```
GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}
```
Returns details of a specific console session:
```json
{
  "@odata.type": "#ShoalConsoleSession.v1_0_0.ConsoleSession",
  "@odata.id": "/redfish/v1/Managers/BMC-server01/Oem/Shoal/ConsoleSessions/1",
  "Id": "1",
  "Name": "Serial Console Session",
  "ConsoleType": "SerialConsole",
  "ConnectType": "Oem",
  "State": "Active",
  "CreatedBy": "admin",
  "CreatedTime": "2025-12-26T17:00:00Z",
  "LastActivityTime": "2025-12-26T17:55:00Z",
  "WebSocketURI": "wss://shoal.example.com/ws/console/1",
  "Actions": {
    "#ConsoleSession.Disconnect": {
      "target": "/redfish/v1/Managers/BMC-server01/Oem/Shoal/ConsoleSessions/1/Actions/Disconnect"
    }
  }
}
```

**4. Connect Console Actions (OEM Extension)**
```
POST /redfish/v1/Managers/{ManagerId}/Actions/Oem/Shoal.ConnectSerialConsole
POST /redfish/v1/Managers/{ManagerId}/Actions/Oem/Shoal.ConnectGraphicalConsole
```

Request body:
```json
{
  "ConnectType": "Oem"
}
```

Response (201 Created):
```json
{
  "@odata.type": "#ShoalConsoleSession.v1_0_0.ConsoleSession",
  "@odata.id": "/redfish/v1/Managers/BMC-server01/Oem/Shoal/ConsoleSessions/1",
  "Id": "1",
  "ConsoleType": "SerialConsole",
  "ConnectType": "Oem",
  "State": "Connecting",
  "WebSocketURI": "wss://shoal.example.com/ws/console/1"
}
```

**5. WebSocket Console Endpoint**
```
WebSocket: wss://shoal.example.com/ws/console/{SessionId}
```
Bidirectional WebSocket for console I/O. Authentication via token in initial HTTP upgrade request.

**6. Disconnect Console Action (OEM Extension)**
```
POST /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}/Actions/Disconnect
```
Terminates an active console session.

### Data Flow

#### Serial Console Flow (via Redfish OEM)
```
User → POST ConnectSerialConsole (ConnectType: "Oem")
                ↓
       Shoal creates console session record
                ↓
       Shoal queries BMC's Redfish OEM endpoint for serial console WebSocket URL
                ↓
       Returns session resource with Shoal's WebSocket URI
                ↓
User → WebSocket connect to /ws/console/{id}
                ↓
       Shoal proxies data between user WebSocket and BMC's console WebSocket
       (User keystrokes → Shoal → BMC WebSocket)
       (BMC serial output → BMC WebSocket → Shoal → User)
                ↓
User → POST Disconnect or WebSocket close
                ↓
       Shoal closes BMC WebSocket connection
                ↓
       Session marked as Disconnected
```

#### Graphical Console Flow (Vendor-Specific Redfish)
```
User → POST ConnectGraphicalConsole (ConnectType: "Oem")
                ↓
       Shoal creates console session record
                ↓
       Shoal queries vendor-specific Redfish endpoint for console access
       (e.g., Dell: GET /redfish/v1/Dell/Managers/{id}/DellvKVM)
                ↓
       Returns session with:
       - Redirect URL to BMC's HTML5 console (Option A)
       - WebSocket URI proxied through Shoal (Option B)
                ↓
User → Browser connects to console
       (Direct redirect to BMC web console with temp token)
       (Or via Shoal WebSocket proxy to BMC's graphical console)
                ↓
       User interacts with graphical console
                ↓
       Session expires or user disconnects
```

## Phase 1: Serial Console (via Redfish OEM)

Serial console access through Redfish OEM extensions is the most critical console capability. Modern BMCs expose serial console through WebSocket endpoints or streaming APIs in their Redfish OEM implementations.

### 1.1 Console Capability Discovery

When a `ConnectionMethod` is created (BMC is added), Shoal will discover console capabilities:

**Discovery Process**:
1. Query `/redfish/v1/Managers/{id}` on the downstream BMC
2. Extract `SerialConsole` and `GraphicalConsole` properties
3. Query vendor-specific OEM endpoints for console connection details
4. Store console capabilities in Shoal's database

**Cached Properties**:
- Console type (Serial, Graphical)
- Service enabled status
- Max concurrent sessions
- Supported connection types
- Vendor-specific OEM endpoints and capabilities

### 1.2 Database Schema

New table: `console_capabilities`
```sql
CREATE TABLE console_capabilities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    connection_method_id INTEGER NOT NULL,
    manager_id TEXT NOT NULL,
    console_type TEXT NOT NULL,              -- 'SerialConsole', 'GraphicalConsole'
    service_enabled BOOLEAN DEFAULT 0,
    max_concurrent_sessions INTEGER,
    connect_types TEXT,                      -- JSON array: ["Oem", "KVMIP"]
    vendor_data TEXT,                        -- JSON: vendor-specific OEM endpoints and capabilities
    last_updated DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(connection_method_id) REFERENCES connection_methods(id) ON DELETE CASCADE,
    UNIQUE(connection_method_id, manager_id, console_type)
);

CREATE INDEX idx_cc_connection ON console_capabilities(connection_method_id);
CREATE INDEX idx_cc_type ON console_capabilities(console_type);
```

New table: `console_sessions`
```sql
CREATE TABLE console_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT UNIQUE NOT NULL,         -- UUID for session identification
    connection_method_id INTEGER NOT NULL,
    manager_id TEXT NOT NULL,
    console_type TEXT NOT NULL,              -- 'SerialConsole', 'GraphicalConsole'
    connect_type TEXT NOT NULL,              -- 'Oem', 'KVMIP', etc.
    state TEXT DEFAULT 'connecting',         -- 'connecting', 'active', 'disconnected', 'error'
    created_by TEXT NOT NULL,                -- Username who created session
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    disconnected_at DATETIME,
    websocket_uri TEXT,
    bmc_websocket_uri TEXT,                  -- BMC's WebSocket endpoint (if applicable)
    error_message TEXT,
    metadata TEXT,                           -- JSON: session-specific data
    FOREIGN KEY(connection_method_id) REFERENCES connection_methods(id) ON DELETE CASCADE
);

CREATE INDEX idx_cs_session ON console_sessions(session_id);
CREATE INDEX idx_cs_state ON console_sessions(state);
CREATE INDEX idx_cs_connection ON console_sessions(connection_method_id);
```

### 1.3 Redfish Serial Console Implementation

**Redfish Serial Console via OEM**:
- Modern BMCs expose serial console through Redfish OEM WebSocket endpoints
- Dell iDRAC provides WebSocket URLs through OEM extensions
- Supermicro and HPE provide similar capabilities through their OEM implementations
- Bidirectional WebSocket-based communication

**Vendor-Specific Endpoints**:
- **Dell iDRAC**: Query `/redfish/v1/Dell/Managers/{id}/SerialInterfaces` or similar OEM endpoint for WebSocket URL
- **Supermicro**: Check OEM extensions for console WebSocket endpoints
- **HPE iLO**: Query OEM console endpoints for WebSocket URLs

**Serial Console Session Handler**:
```go
type SerialConsoleSession struct {
    ID                string
    ConnectionMethodID int
    BMCAddress        string
    BMCCredentials    Credentials
    BMCWebSocketURL   string
    BMCWebSocketConn  *websocket.Conn
    UserWebSocketConn *websocket.Conn
    State             string
    CreatedBy         string
    mutex             sync.Mutex
}

func (s *SerialConsoleSession) Connect() error {
    // 1. Query BMC Redfish OEM endpoint for serial console WebSocket URL
    // 2. Establish WebSocket connection to BMC
    // 3. Start goroutines for bidirectional data flow
    // 4. Update session state to 'active'
}

func (s *SerialConsoleSession) handleUserToBMC() {
    // Read from user WebSocket, send to BMC WebSocket
}

func (s *SerialConsoleSession) handleBMCToUser() {
    // Read from BMC WebSocket, send to user WebSocket
}

func (s *SerialConsoleSession) Disconnect() error {
    // 1. Close BMC WebSocket connection
    // 2. Close user WebSocket
    // 3. Update session state to 'disconnected'
}
```

### 1.4 Redfish API Implementation

**Handler Structure** (in `internal/api`):

**1. Manager Resource Handler Enhancement**
```go
// GET /redfish/v1/Managers/{ManagerId}
func (h *Handler) handleManager(w http.ResponseWriter, r *http.Request)
// Enhancement: Include SerialConsole and GraphicalConsole properties
// Include Oem.Shoal.ConsoleActions links
```

**2. Console Session Collection Handler**
```go
// GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions
func (h *Handler) handleConsoleSessionCollection(w http.ResponseWriter, r *http.Request)
```

**3. Console Session Resource Handler**
```go
// GET /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}
func (h *Handler) handleConsoleSession(w http.ResponseWriter, r *http.Request)
```

**4. Connect Serial Console Action Handler**
```go
// POST /redfish/v1/Managers/{ManagerId}/Actions/Oem/Shoal.ConnectSerialConsole
func (h *Handler) handleConnectSerialConsole(w http.ResponseWriter, r *http.Request)
```

**Implementation Steps**:
1. Validate request body (ConnectType must be in supported types)
2. Check console capability (SerialConsole enabled, max sessions not exceeded)
3. Retrieve connection method credentials
4. Query BMC's Redfish OEM endpoint for serial console WebSocket URL
5. Generate unique session ID
6. Create console session record in database (state: 'connecting')
7. Spawn serial console session handler goroutine
8. Return session resource with Shoal's WebSocket URI
9. Handler continues asynchronously to establish BMC WebSocket connection

**5. WebSocket Console Handler**
```go
// WebSocket: /ws/console/{SessionId}
func (h *Handler) handleConsoleWebSocket(w http.ResponseWriter, r *http.Request)
```

**Implementation Steps**:
1. Validate session ID exists and belongs to authenticated user
2. Upgrade HTTP connection to WebSocket
3. Retrieve active console session
4. Attach user WebSocket connection to console session
5. Console session handles bidirectional data flow between user and BMC WebSockets
6. On WebSocket close, disconnect console session

**6. Disconnect Console Action Handler**
```go
// POST /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}/Actions/Disconnect
func (h *Handler) handleDisconnectConsole(w http.ResponseWriter, r *http.Request)
```

Terminates console session and cleans up resources.

### 1.5 Session Management

**Lifecycle Management**:
- **Creation**: User POSTs to ConnectSerialConsole
- **Active**: BMC WebSocket connected, user WebSocket attached
- **Idle Timeout**: Close sessions after N minutes of inactivity (configurable)
- **Max Sessions**: Enforce max concurrent sessions per BMC
- **Cleanup**: Remove session records after retention period

**Concurrency Control**:
- Mutex per session for thread-safe state updates
- Database transactions for session state changes
- Graceful handling of duplicate connection attempts

**Session State Machine**:
```
Connecting → Active → Disconnected
     ↓           ↓
   Error ←——————┘
```

### 1.6 Security Considerations

**Authentication**:
- All console endpoints require authenticated Redfish session
- WebSocket upgrade includes authentication token validation
- Session ownership tracked (users can only access their own sessions, admins can access all)

**Authorization**:
- Operator role or higher required for console access
- ReadOnly role cannot create console sessions

**Network Security**:
- WebSocket over TLS (WSS) required in production
- Redfish API calls to BMCs over isolated management network
- No direct BMC port exposure to end users

**Session Isolation**:
- Each user gets isolated console session
- Input/output streams not shared between users
- Proper cleanup prevents session hijacking

**Credential Protection**:
- BMC credentials never exposed to end users
- Shoal uses stored credentials to establish Redfish sessions and WebSocket connections
- User WebSocket does not carry BMC credentials

**Audit Logging**:
- Log console session creation, connection, and disconnection
- Track user identity, timestamp, duration
- Optional: Log console I/O for compliance (with privacy considerations)

### 1.7 UI Integration

**Manager Detail Page** (`/managers/{id}`):
- Add "Console" tab
- Show available console types (Serial, Graphical)
- Display active console sessions
- Button: "Open Serial Console" (launches console in new window/panel)

**Console Interface**:
- Web-based terminal emulator (xterm.js or similar)
- Full-screen console window
- WebSocket connection to Shoal
- Send Ctrl+Alt+Del, Break, and other special keys
- Session status indicator
- "Disconnect" button

**Active Sessions Table**:
- List all active console sessions across all BMCs
- Columns: BMC name, console type, user, started, last activity
- Quick disconnect button
- Admin view: see all users' sessions

**Example Console UI Flow**:
1. User navigates to Manager detail page
2. Clicks "Open Serial Console"
3. Shoal creates console session via POST to ConnectSerialConsole
4. Shoal queries BMC's Redfish OEM endpoint for WebSocket URL
5. New window opens with xterm.js terminal
6. JavaScript establishes WebSocket connection to /ws/console/{id}
7. Shoal proxies between user WebSocket and BMC WebSocket
8. User interacts with serial console
9. Closing window or clicking "Disconnect" terminates session

## Phase 2: Graphical Console (Virtual KVM via Redfish)

Graphical console support is vendor-specific and provided through Redfish OEM extensions.

### 2.1 Vendor-Specific Redfish Implementations

**Dell iDRAC Virtual Console**:
- HTML5 Virtual Console
- Access via Redfish OEM: `/redfish/v1/Dell/Managers/{id}/DellvKVM` or similar
- OEM actions provide console session URLs or redirect endpoints
- Protocol: WebSocket-based or HTML5 redirect to iDRAC web interface

**Supermicro HTML5 iKVM**:
- HTML5 iKVM (newer firmware)
- Access via Redfish OEM extensions
- OEM endpoints provide console access URLs
- Protocol: HTML5 WebSocket or redirect to BMC web console

**HPE iLO Integrated Remote Console**:
- HTML5 IRC
- Access via Redfish OEM endpoints
- OEM actions provide console URLs
- Protocol: WebSocket-based

### 2.2 Implementation Approaches

**Option A: Direct Redirect (Simplest)**:
- Shoal queries BMC's Redfish OEM endpoint for console URL
- Returns temporary access URL/token to user's browser
- Browser redirects directly to BMC's console URL
- **Pros**: Simple, no complex proxying needed
- **Cons**: User's browser must reach BMC network, less secure

**Option B: WebSocket Proxying**:
- Shoal queries BMC's Redfish OEM endpoint for console WebSocket URL
- Shoal establishes WebSocket connection to BMC's console endpoint
- User connects to Shoal's WebSocket, data proxied to/from BMC
- **Pros**: User never directly accesses BMC, more secure
- **Cons**: More complex, bandwidth intensive

**Recommended**: Hybrid approach
- Phase 2.1: Implement Option A (direct redirect) for quick wins
- Phase 2.2: Implement Option B (WebSocket proxying) for secure environments
- Configuration flag to choose approach per deployment

### 2.3 GraphicalConsole Session Flow

```
User → POST ConnectGraphicalConsole (ConnectType: "KVMIP")
                ↓
       Shoal queries BMC for console access URL/token
       (Vendor-specific API call)
                ↓
       Creates console session record
                ↓
       Returns session with:
       - Redirect URL (Option A)
       - WebSocket URI (Option C)
                ↓
User → Browser navigates to console
       (Direct to BMC or via Shoal proxy)
                ↓
       Session remains active
                ↓
       Session expires or user disconnects
```

### 2.4 Vendor Detection and Capability Mapping

**Detection**:
- Check Manager `Model`, `FirmwareVersion`, and OEM properties
- Query vendor-specific OEM endpoints for console capabilities
- Maintain vendor capability matrix

**Capability Matrix**:
```json
{
  "Dell": {
    "iDRAC9": {
      "SerialConsole": {
        "OEMEndpoint": "/redfish/v1/Dell/Managers/{id}/SerialInterfaces",
        "WebSocketSupport": true
      },
      "GraphicalConsole": {
        "OEMEndpoint": "/redfish/v1/Dell/Managers/{id}/DellvKVM",
        "Methods": ["HTML5Redirect", "WebSocket"]
      }
    }
  },
  "Supermicro": {
    "X11": {
      "GraphicalConsole": {
        "OEMEndpoint": "/redfish/v1/Oem/Supermicro/iKVM",
        "Methods": ["HTML5Redirect"]
      }
    }
  }
}
```

## Implementation Milestones

### Milestone 1: Core Console Discovery and Data Model
- [ ] Enhance BMC discovery to query console capabilities (SerialConsole, GraphicalConsole, CommandShell)
- [ ] Create database schema for `console_capabilities` and `console_sessions`
- [ ] Implement database migrations
- [ ] Add caching logic for console capabilities
- [ ] Write unit tests for discovery and caching

### Milestone 2: Serial Console - Redfish Backend
- [ ] Query BMC Redfish OEM endpoints for serial console WebSocket URLs
- [ ] Implement serial console session handler (connect to BMC WebSocket, bidirectional data flow, disconnect)
- [ ] Implement session lifecycle management (create, active, timeout, cleanup)
- [ ] Add concurrency control and state management
- [ ] Write unit tests with mock Redfish WebSocket endpoints

### Milestone 3: Serial Console - Redfish API
- [ ] Enhance Manager resource handler to include console properties
- [ ] Implement ConnectSerialConsole action handler
- [ ] Implement console session collection and resource handlers
- [ ] Implement disconnect action handler
- [ ] Add request validation and error handling
- [ ] Write unit tests for API handlers

### Milestone 4: Serial Console - WebSocket Gateway
- [ ] Implement WebSocket upgrade and connection handler
- [ ] Implement bidirectional WebSocket-to-WebSocket proxying (user to BMC)
- [ ] Add WebSocket authentication and authorization
- [ ] Implement graceful WebSocket closure handling
- [ ] Write integration tests with real WebSocket clients

### Milestone 5: Serial Console - UI Integration
- [ ] Add "Console" tab to Manager detail page
- [ ] Integrate xterm.js for browser terminal
- [ ] Implement console connection UI workflow
- [ ] Add session management UI (list, disconnect)
- [ ] Test end-to-end serial console functionality via Redfish

### Milestone 6: Graphical Console - Vendor Detection
- [ ] Implement vendor detection logic (Dell, Supermicro, HPE)
- [ ] Build vendor capability matrix with Redfish OEM endpoints
- [ ] Query GraphicalConsole properties and OEM endpoints from BMCs
- [ ] Store vendor-specific Redfish capabilities in database
- [ ] Write unit tests for vendor detection

### Milestone 7: Graphical Console - Direct Redirect (Option A)
- [ ] Implement vendor-specific Redfish OEM console URL retrieval (Dell iDRAC)
- [ ] Query temporary session/token from BMC via Redfish
- [ ] Implement ConnectGraphicalConsole action handler (redirect mode)
- [ ] Create UI for graphical console launch (new window)
- [ ] Test with Dell and Supermicro BMCs

### Milestone 8: Graphical Console - WebSocket Proxying (Option B)
- [ ] Query BMC Redfish OEM endpoints for graphical console WebSocket URLs
- [ ] Implement graphical console session handler
- [ ] Implement WebSocket-to-WebSocket bridge (user to BMC console)
- [ ] Add session state tracking for graphical consoles
- [ ] Write integration tests with real BMC console WebSockets

### Milestone 9: Security and Production Hardening
- [ ] Implement idle session timeouts
- [ ] Add rate limiting for console connection requests
- [ ] Implement audit logging for all console operations
- [ ] Add session ownership and access control
- [ ] Security review and penetration testing
- [ ] Performance testing (concurrent sessions, data throughput)

### Milestone 10: Advanced Features
- [ ] Session recording/playback (compliance)
- [ ] Multi-user collaborative console (screen sharing)
- [ ] Console history buffer (reconnect without data loss)
- [ ] Clipboard integration for graphical console
- [ ] Mobile-friendly console UI

## Configuration

New CLI flags and environment variables:

```bash
# Console General
--console-enabled bool                  Enable console pass-through (default: true)
--console-idle-timeout int              Idle timeout for console sessions in minutes (default: 30)
--console-max-sessions-per-bmc int      Max concurrent console sessions per BMC (default: 4)

# Serial Console
--serial-console-enabled bool           Enable serial console via Redfish (default: true)

# Graphical Console
--graphical-console-enabled bool        Enable graphical console (default: true)
--graphical-console-mode string         Mode: "redirect", "proxy", "auto" (default: "auto")

# WebSocket
--console-websocket-path string         WebSocket path prefix (default: "/ws/console")
--console-websocket-ping-interval int   WebSocket ping interval in seconds (default: 30)

# Security
--console-require-operator bool         Require Operator role for console access (default: true)
--console-audit-logging bool            Enable console I/O audit logging (default: false)
--console-audit-log-dir string          Directory for console audit logs (default: /var/log/shoal/console)

# Vendor-Specific
--dell-idrac-html5-console bool         Use Dell iDRAC HTML5 console (default: true)
--supermicro-ikvm-html5 bool            Use Supermicro HTML5 iKVM (default: true)
```

## Testing Strategy

### Unit Tests
- Console capability discovery and caching
- Database operations (CRUD for console_capabilities and console_sessions)
- Session state machine transitions
- Vendor detection logic
- Redfish OEM endpoint URL generation

### Integration Tests
- Full serial console session workflow (connect, data flow, disconnect) via Redfish WebSocket
- Concurrent console sessions to same BMC
- Session timeout and cleanup
- WebSocket connection and authentication
- Redfish WebSocket proxying with mock BMC endpoints

### Manual Testing
- Connect to real BMC serial console via Shoal using Redfish
- Verify bidirectional data flow (keyboard input, serial output)
- Test special keys (Ctrl+C, Ctrl+D, Break)
- Verify graphical console redirect to Dell iDRAC via Redfish OEM
- Verify WebSocket proxying for graphical console (if implemented)
- Verify session cleanup on disconnect
- Test concurrent sessions from multiple users
- Verify audit logging captures events

### Performance Testing
- Max concurrent sessions per Shoal instance
- Data throughput (bytes/sec for serial and graphical consoles)
- Latency measurements (user input to BMC)
- Resource usage (memory, CPU, goroutines)
- WebSocket connection handling under load

## Security Considerations

### 1. Authentication and Authorization
- **Session-Based Auth**: All console endpoints require valid Redfish session
- **WebSocket Auth**: Token validation on WebSocket upgrade
- **Role-Based Access**: Operator or Administrator role required for console access
- **Session Ownership**: Users can only access their own console sessions (admins exempt)

### 2. Credential Management
- **BMC Credentials**: Stored encrypted in database, never exposed to end users
- **Redfish Session Management**: Shoal manages Redfish sessions to BMCs for console access
- **WebSocket Authentication**: Secure token-based authentication for console WebSocket connections

### 3. Network Security
- **WSS (WebSocket Secure)**: Mandatory TLS for WebSocket connections in production
- **Management Network Isolation**: Redfish API and WebSocket traffic to BMCs over isolated network
- **No Port Forwarding**: End users never directly access BMC endpoints
- **Firewall Rules**: Shoal server is only host with BMC network access

### 4. Session Security
- **Session Isolation**: Each user gets independent console session
- **No Shared Sessions**: Input/output streams not shared between users
- **Secure Cleanup**: Proper resource cleanup prevents session hijacking
- **Idle Timeout**: Automatic disconnection after inactivity

### 5. Data Protection
- **TLS Encryption**: All data in transit encrypted (user to Shoal, Shoal to BMC)
- **Audit Logging**: Console session events logged (who, when, which BMC)
- **Optional I/O Logging**: Console input/output can be logged for compliance
- **Log Sanitization**: Redact sensitive data (passwords) from logs

### 6. Denial of Service Protection
- **Rate Limiting**: Limit console connection requests per user
- **Max Sessions**: Enforce max concurrent sessions per BMC and per user
- **Resource Limits**: CPU/memory limits for console session handlers
- **Connection Timeout**: Fail fast if BMC connection cannot be established

### 7. Vendor-Specific Security
- **Dell iDRAC**: Validate Redfish OEM console tokens, enforce token expiration
- **Redfish Authentication**: Use BMC Redfish sessions for all console operations
- **TLS Verification**: Validate BMC TLS certificates for secure Redfish connections

## Future Considerations

1. **Session Recording and Playback**: Record console sessions for compliance, audit, and training
2. **Multi-User Collaborative Console**: Allow multiple users to view same console (screen sharing)
3. **Console History Buffer**: Maintain scrollback buffer to survive reconnections
4. **Clipboard Integration**: Copy/paste support for graphical consoles
5. **Mobile Console**: Mobile-optimized UI for emergency console access
6. **Console Automation**: Scriptable console interactions via Redfish (expect-like functionality)
7. **Advanced Terminal Features**: Sixel graphics, true color support
8. **Console Notifications**: Alert users when console output matches patterns (e.g., kernel panic)
9. **Cross-Site Consoles**: Aggregate consoles across multiple Shoal instances (multi-datacenter)
10. **Enhanced OEM Discovery**: Automatic discovery of vendor-specific Redfish console endpoints

## Open Questions

1. **Session Persistence**: Should console sessions survive Shoal restarts? (Likely no for Phase 1)
2. **Shared vs. Exclusive**: Should serial console be exclusive (one user at a time) or shared?
3. **I/O Buffering**: How much console output to buffer for late-joining WebSocket clients?
4. **Vendor Priorities**: Which vendors to prioritize for graphical console beyond Dell and Supermicro?
5. **Redfish OEM Standardization**: How to handle different OEM endpoint structures across vendors?
6. **Audit Log Retention**: How long to keep console I/O audit logs? Privacy implications?
7. **Binary Data**: How to handle binary data in serial console (firmware uploads, etc.)?
8. **Terminal Size**: Default terminal size for serial console sessions (80x24, 80x43, or configurable)?

## References

- [DMTF Redfish Manager Schema v1.10](https://redfish.dmtf.org/schemas/v1/Manager.v1_10_0.json)
- [DMTF Redfish Specification](https://www.dmtf.org/standards/redfish)
- [Dell iDRAC9 Redfish API Guide](https://www.dell.com/support/manuals/en-us/idrac9-lifecycle-controller-v3.x-series/idrac_3.00.00.00_redfishapiguide/)
- [Supermicro Redfish User Guide](https://www.supermicro.com/manuals/other/Redfish_Users_Guide.pdf)
- [HPE iLO 5 Redfish API Reference](https://hewlettpackard.github.io/ilo-rest-api-docs/ilo5/)
- [xterm.js - Terminal for the web](https://xtermjs.org/)
- [WebSocket Protocol (RFC 6455)](https://www.rfc-editor.org/rfc/rfc6455)

## Conclusion

This design provides a comprehensive roadmap for implementing DMTF Redfish-compliant console pass-through in Shoal. By aggregating console capabilities from downstream BMCs and proxying Redfish OEM console endpoints (WebSocket URLs, session tokens), Shoal enables unified, secure console access to isolated systems.

**Key Design Principles**:
- **DMTF Compliance**: Manager console properties follow Redfish specification
- **OEM Extensions**: Console connection establishment uses vendor Redfish OEM endpoints
- **Redfish-Native**: All console access through Redfish API and OEM WebSocket endpoints
- **Vendor Extensibility**: Framework for Dell and Supermicro specific Redfish implementations
- **Security First**: Authentication, authorization, TLS encryption, and audit logging
- **Phase-Based Delivery**: Start with serial console (most critical), expand to graphical console

The architecture leverages Shoal's bastion role to provide secure, unified console access for isolated BMCs, enabling critical troubleshooting, installation, and management workflows without requiring direct network access to BMC management networks. All console functionality is implemented through Redfish API methods, ensuring vendor interoperability and standards compliance.

**Implementation Priority**:
1. **Phase 1**: Serial Console via Redfish OEM - Most critical for troubleshooting
2. **Phase 2**: Graphical Console via Redfish OEM - High user value for installations and GUI access
