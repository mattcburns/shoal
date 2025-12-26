# 021: Console Pass-Through

**Author:** GitHub Copilot  
**Date:** 2025-12-26  
**Status:** Proposed

## Abstract

This document outlines the design for implementing DMTF Redfish-compliant console pass-through functionality in Shoal. As a Redfish aggregator acting as a bastion service, Shoal will provide unified, secure access to Serial-over-LAN (SOL) and remote console capabilities across managed BMCs. This design enables direct console access to isolated systems for troubleshooting, installation, and management tasks without requiring direct network access to BMCs. The implementation focuses on vendor-agnostic Serial-over-LAN support initially, with extensibility for vendor-specific virtual/graphical console capabilities from Dell and Supermicro BMCs.

## Background

### Current State

Shoal currently provides excellent aggregation and pass-through capabilities for HTTP-based Redfish API calls. It discovers and manages BMCs through the `AggregationService`, providing unified access to system information, settings, power management, and virtual media.

However, console access capabilities are not yet exposed through Shoal's aggregation layer:
- BMCs provide console access through Serial Console, Graphical Console, and Command Shell interfaces
- These are typically accessed via Serial-over-LAN (SOL), SSH, or vendor-specific protocols
- In Shoal's deployment model, BMCs are isolated and users cannot reach them directly
- Shoal acts as a bastion, providing the only connectivity path to/from BMCs

### Problem Statement

When a user needs console access to a system (e.g., for troubleshooting, BIOS configuration, OS installation), they need:
1. Access to console resources through Shoal's unified Redfish API
2. Ability to establish persistent console sessions (Serial-over-LAN, remote console)
3. Shoal to proxy console connections to the correct downstream BMC
4. Support for both text-based (SOL) and graphical (virtual console) sessions
5. Proper session management, authentication, and security

**Key Constraint**: Console sessions are interactive and persistent. Unlike typical HTTP requests, console connections require:
- Long-lived bidirectional communication channels
- Protocol-specific handling (IPMI SOL, SSH, vendor proprietary)
- Session state management
- Proper cleanup on disconnection

### Use Cases

1. **Emergency Troubleshooting**: Access server console when network/OS is down
2. **BIOS Configuration**: Access BIOS/UEFI settings during POST
3. **OS Installation**: Monitor and interact with OS installer
4. **Firmware Updates**: Monitor firmware update progress and handle prompts
5. **Boot Debugging**: View boot messages and kernel panics
6. **Out-of-Band Management**: Execute commands when SSH/network is unavailable
7. **Remote KVM**: Graphical console access for GUI-based installations and troubleshooting

### Vendor Console Capabilities

Different BMC vendors provide varying console capabilities:

**Dell iDRAC**:
- Serial-over-LAN via IPMI
- Virtual Console (HTML5-based KVM)
- SSH access to BMC command shell
- Redfish `SerialConsole`, `GraphicalConsole`, and `CommandShell` properties

**Supermicro IPMI**:
- Serial-over-LAN via IPMI
- iKVM (Java-based or HTML5 virtual console)
- SSH access to BMC
- Redfish `SerialConsole` and `GraphicalConsole` support varies by model

**HPE iLO**:
- Serial-over-LAN via IPMI
- Integrated Remote Console (IRC)
- SSH access
- Full Redfish console support

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
    "ConnectTypesSupported": ["IPMI", "SSH", "Telnet", "Oem"]
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

**CommandShell** (BMC Command Line Interface):
```json
{
  "CommandShell": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 4,
    "ConnectTypesSupported": ["SSH", "Telnet", "IPMI", "Oem"]
  }
}
```

### Console Connection Process

The Redfish specification does not define a standard API for establishing console connections. Instead, it provides:
1. **Discovery**: Properties on Manager resource indicating available console types
2. **OEM Extensions**: Vendor-specific endpoints for establishing connections
3. **Out-of-Band Protocols**: IPMI SOL, SSH, or proprietary protocols outside Redfish

### Shoal's Approach

Since DMTF Redfish does not standardize console connection establishment, Shoal will:
1. **Aggregate console capabilities** from Manager resources (standard Redfish)
2. **Provide OEM extension endpoints** for establishing console sessions
3. **Implement protocol proxies** for SOL, SSH, and vendor-specific consoles
4. **Expose WebSocket/Server-Sent Events** for browser-based console access

## Architecture Overview

Shoal will expose console resources through DMTF Redfish-compliant endpoints and implement protocol-specific proxies for establishing and maintaining console sessions.

### High-Level Components

1. **Console Capability Discovery**: Aggregate SerialConsole, GraphicalConsole, and CommandShell properties from managed BMCs
2. **Session Management**: Track active console sessions and their state
3. **Protocol Proxies**: Protocol-specific handlers for SOL, SSH, and vendor consoles
4. **WebSocket Gateway**: Browser-accessible console interface
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
    "ConnectTypesSupported": ["IPMI", "SSH"]
  },
  "GraphicalConsole": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 4,
    "ConnectTypesSupported": ["KVMIP"]
  },
  "CommandShell": {
    "ServiceEnabled": true,
    "MaxConcurrentSessions": 4,
    "ConnectTypesSupported": ["SSH"]
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
        },
        "#Manager.ConnectCommandShell": {
          "target": "/redfish/v1/Managers/BMC-server01/Actions/Oem/Shoal.ConnectCommandShell"
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
  "ConnectType": "IPMI",
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
POST /redfish/v1/Managers/{ManagerId}/Actions/Oem/Shoal.ConnectCommandShell
```

Request body:
```json
{
  "ConnectType": "IPMI",
  "SessionType": "WebSocket"
}
```

Response (201 Created):
```json
{
  "@odata.type": "#ShoalConsoleSession.v1_0_0.ConsoleSession",
  "@odata.id": "/redfish/v1/Managers/BMC-server01/Oem/Shoal/ConsoleSessions/1",
  "Id": "1",
  "ConsoleType": "SerialConsole",
  "ConnectType": "IPMI",
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

#### Serial-over-LAN (IPMI) Flow
```
User → POST ConnectSerialConsole (ConnectType: "IPMI")
                ↓
       Shoal creates console session record
                ↓
       Shoal initiates IPMI SOL connection to downstream BMC
                ↓
       Returns session resource with WebSocket URI
                ↓
User → WebSocket connect to /ws/console/{id}
                ↓
       Shoal proxies data between WebSocket and IPMI SOL
       (User keystrokes → IPMI → BMC serial port)
       (BMC serial output → IPMI → WebSocket → User)
                ↓
User → POST Disconnect or WebSocket close
                ↓
       Shoal closes IPMI SOL session
                ↓
       Session marked as Disconnected
```

#### Graphical Console Flow (Vendor-Specific)
```
User → POST ConnectGraphicalConsole (ConnectType: "KVMIP")
                ↓
       Shoal creates console session record
                ↓
       Shoal queries vendor-specific console URL from BMC
       (e.g., Dell: GET iDRAC console redirect URL)
                ↓
       Returns session with redirect URL or WebSocket proxy
                ↓
User → Browser connects to console
       (Option A: Direct redirect to BMC web console with temp token)
       (Option B: Shoal proxies VNC/RFB protocol via WebSocket)
                ↓
       User interacts with graphical console
                ↓
       Session cleanup on disconnect
```

## Phase 1: Serial-over-LAN (IPMI SOL)

Serial-over-LAN is the most standardized and critical console capability. It provides text-based serial console access via IPMI.

### 1.1 Console Capability Discovery

When a `ConnectionMethod` is created (BMC is added), Shoal will discover console capabilities:

**Discovery Process**:
1. Query `/redfish/v1/Managers/{id}` on the downstream BMC
2. Extract `SerialConsole`, `GraphicalConsole`, and `CommandShell` properties
3. Store console capabilities in Shoal's database
4. Validate BMC supports IPMI SOL (check `ConnectTypesSupported` includes "IPMI")

**Cached Properties**:
- Console type (Serial, Graphical, CommandShell)
- Service enabled status
- Max concurrent sessions
- Supported connection types
- Vendor-specific capabilities (from OEM extensions)

### 1.2 Database Schema

New table: `console_capabilities`
```sql
CREATE TABLE console_capabilities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    connection_method_id INTEGER NOT NULL,
    manager_id TEXT NOT NULL,
    console_type TEXT NOT NULL,              -- 'SerialConsole', 'GraphicalConsole', 'CommandShell'
    service_enabled BOOLEAN DEFAULT 0,
    max_concurrent_sessions INTEGER,
    connect_types TEXT,                      -- JSON array: ["IPMI", "SSH"]
    vendor_data TEXT,                        -- JSON: vendor-specific capabilities
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
    console_type TEXT NOT NULL,              -- 'SerialConsole', 'GraphicalConsole', 'CommandShell'
    connect_type TEXT NOT NULL,              -- 'IPMI', 'SSH', 'KVMIP', etc.
    state TEXT DEFAULT 'connecting',         -- 'connecting', 'active', 'disconnected', 'error'
    created_by TEXT NOT NULL,                -- Username who created session
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    disconnected_at DATETIME,
    websocket_uri TEXT,
    error_message TEXT,
    metadata TEXT,                           -- JSON: session-specific data
    FOREIGN KEY(connection_method_id) REFERENCES connection_methods(id) ON DELETE CASCADE
);

CREATE INDEX idx_cs_session ON console_sessions(session_id);
CREATE INDEX idx_cs_state ON console_sessions(state);
CREATE INDEX idx_cs_connection ON console_sessions(connection_method_id);
```

### 1.3 IPMI SOL Implementation

**IPMI SOL Protocol**:
- Uses IPMI over LAN (UDP port 623)
- Requires RMCP+ session establishment
- SOL payload type for serial data
- Bidirectional packet-based communication

**Go Library**: Use `github.com/gebn/bmc` or similar IPMI library for SOL support.

**SOL Session Handler**:
```go
type SOLSession struct {
    ID              string
    ConnectionMethodID int
    BMCAddress      string
    BMCCredentials  Credentials
    IPMISession     *bmc.Session
    WebSocketConn   *websocket.Conn
    State           string
    CreatedBy       string
    mutex           sync.Mutex
}

func (s *SOLSession) Connect() error {
    // 1. Establish IPMI session with BMC
    // 2. Activate SOL
    // 3. Start goroutines for bidirectional data flow
    // 4. Update session state to 'active'
}

func (s *SOLSession) handleWebSocketToIPMI() {
    // Read from WebSocket, send to IPMI SOL
}

func (s *SOLSession) handleIPMIToWebSocket() {
    // Read from IPMI SOL, send to WebSocket
}

func (s *SOLSession) Disconnect() error {
    // 1. Deactivate SOL
    // 2. Close IPMI session
    // 3. Close WebSocket
    // 4. Update session state to 'disconnected'
}
```

### 1.4 Redfish API Implementation

**Handler Structure** (in `internal/api`):

**1. Manager Resource Handler Enhancement**
```go
// GET /redfish/v1/Managers/{ManagerId}
func (h *Handler) handleManager(w http.ResponseWriter, r *http.Request)
// Enhancement: Include SerialConsole, GraphicalConsole, CommandShell properties
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
4. Generate unique session ID
5. Create console session record in database (state: 'connecting')
6. Spawn SOL session handler goroutine
7. Return session resource with WebSocket URI
8. Handler continues asynchronously to establish IPMI SOL connection

**5. WebSocket Console Handler**
```go
// WebSocket: /ws/console/{SessionId}
func (h *Handler) handleConsoleWebSocket(w http.ResponseWriter, r *http.Request)
```

**Implementation Steps**:
1. Validate session ID exists and belongs to authenticated user
2. Upgrade HTTP connection to WebSocket
3. Retrieve active SOL session
4. Attach WebSocket connection to SOL session
5. SOL session handles bidirectional data flow
6. On WebSocket close, disconnect SOL session

**6. Disconnect Console Action Handler**
```go
// POST /redfish/v1/Managers/{ManagerId}/Oem/Shoal/ConsoleSessions/{SessionId}/Actions/Disconnect
func (h *Handler) handleDisconnectConsole(w http.ResponseWriter, r *http.Request)
```

Terminates SOL session and cleans up resources.

### 1.5 Session Management

**Lifecycle Management**:
- **Creation**: User POSTs to ConnectSerialConsole
- **Active**: IPMI SOL connected, WebSocket attached
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
- IPMI traffic to BMCs over isolated management network
- No direct IPMI port exposure to end users

**Session Isolation**:
- Each user gets isolated console session
- Input/output streams not shared between users
- Proper cleanup prevents session hijacking

**Credential Protection**:
- BMC credentials never exposed to end users
- Shoal uses stored credentials to establish IPMI sessions
- WebSocket does not carry BMC credentials

**Audit Logging**:
- Log console session creation, connection, and disconnection
- Track user identity, timestamp, duration
- Optional: Log console I/O for compliance (with privacy considerations)

### 1.7 UI Integration

**Manager Detail Page** (`/managers/{id}`):
- Add "Console" tab
- Show available console types (Serial, Graphical, Command)
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
4. New window opens with xterm.js terminal
5. JavaScript establishes WebSocket connection to /ws/console/{id}
6. User interacts with serial console
7. Closing window or clicking "Disconnect" terminates session

## Phase 2: Graphical Console (Virtual KVM)

Graphical console support is vendor-specific and more complex than SOL.

### 2.1 Vendor-Specific Implementations

**Dell iDRAC Virtual Console**:
- HTML5 Virtual Console (modern iDRAC versions)
- Legacy: Java-based KVM (deprecated)
- Access via: `/console` endpoint on iDRAC web interface
- Authentication: Single-use token or session cookie
- Protocol: WebSocket-based VNC/RFB or proprietary

**Supermicro iKVM**:
- HTML5 iKVM (newer firmware)
- Legacy: Java-based KVM
- Access via: `/cgi/url_redirect.cgi?url_name=ikvm`
- Protocol: HTML5 WebSocket or Java applet

**HPE iLO Integrated Remote Console**:
- HTML5 IRC
- Access via: `/html/console_page.html`
- Protocol: WebSocket-based

### 2.2 Implementation Approaches

**Option A: Direct Redirect (Simplest)**:
- Shoal generates temporary access token/URL from BMC
- Redirects user's browser directly to BMC's console URL
- **Pros**: Simple, no protocol proxying needed
- **Cons**: User's browser must reach BMC network, less secure

**Option B: HTML Proxying**:
- Shoal proxies BMC's HTML console interface
- Rewrites URLs and injects authentication
- **Pros**: User never directly accesses BMC
- **Cons**: Complex HTML/JS rewriting, vendor-specific

**Option C: Protocol Proxying (VNC/RFB)**:
- If BMC exposes VNC/RFB protocol, Shoal proxies it
- Use noVNC or similar WebSocket-to-VNC bridge
- **Pros**: Standardized protocol, works across vendors (if VNC available)
- **Cons**: Not all BMCs expose VNC, may require configuration

**Recommended**: Hybrid approach
- Phase 2.1: Implement Option A (direct redirect) for quick wins
- Phase 2.2: Implement Option C (VNC proxying) for secure environments
- Phase 2.3: Add vendor-specific HTML proxying as needed

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

### 2.4 VNC Proxying Implementation (Option C)

**Components**:
- VNC client library (e.g., `github.com/mitchellh/go-vnc`)
- noVNC (browser-based VNC client)
- WebSocket-to-VNC bridge in Shoal

**Architecture**:
```
Browser (noVNC) ←—WebSocket—→ Shoal ←—VNC/RFB—→ BMC VNC Server
```

**Implementation**:
```go
type VNCSession struct {
    ID              string
    VNCConnection   *vnc.Conn
    WebSocketConn   *websocket.Conn
    // Bidirectional proxying
}

func (s *VNCSession) Connect() error {
    // 1. Connect to BMC VNC port
    // 2. Authenticate with BMC
    // 3. Proxy VNC protocol to WebSocket in noVNC format
}
```

### 2.5 Vendor Detection and Capability Mapping

**Detection**:
- Check Manager `Model`, `FirmwareVersion`, and OEM properties
- Maintain vendor capability matrix

**Capability Matrix**:
```json
{
  "Dell": {
    "iDRAC9": {
      "GraphicalConsole": {
        "Methods": ["HTML5Redirect", "VNC"],
        "VNCPort": 5900,
        "RedirectEndpoint": "/restgui/start"
      }
    }
  },
  "Supermicro": {
    "X11": {
      "GraphicalConsole": {
        "Methods": ["HTML5Redirect"],
        "RedirectEndpoint": "/cgi/url_redirect.cgi?url_name=ikvm"
      }
    }
  }
}
```

## Phase 3: Command Shell (SSH)

Command shell access provides BMC CLI via SSH.

### 3.1 SSH Proxying

**Architecture**:
```
Browser (xterm.js) ←—WebSocket—→ Shoal ←—SSH—→ BMC SSH Server
```

**Implementation**:
- Use `golang.org/x/crypto/ssh` for SSH client
- Establish SSH connection to BMC
- Proxy SSH I/O to WebSocket (similar to SOL)

**Differences from SOL**:
- SSH provides interactive shell vs. raw serial
- Supports terminal resizing, colors, etc.
- Authentication via SSH keys or password

### 3.2 Terminal Handling

**Terminal Emulation**:
- Full ANSI/VT100 support
- Terminal resize events (propagate to SSH)
- Special key handling (Ctrl+C, Tab completion, etc.)

**Implementation**:
```go
type SSHSession struct {
    ID            string
    SSHClient     *ssh.Client
    SSHSession    *ssh.Session
    WebSocketConn *websocket.Conn
    PTY           *ssh.Pty
}

func (s *SSHSession) Connect() error {
    // 1. SSH connect to BMC
    // 2. Request PTY
    // 3. Start shell
    // 4. Proxy I/O bidirectionally
}

func (s *SSHSession) Resize(rows, cols int) {
    // Handle terminal resize from browser
    s.SSHSession.WindowChange(rows, cols)
}
```

## Implementation Milestones

### Milestone 1: Core Console Discovery and Data Model
- [ ] Enhance BMC discovery to query console capabilities (SerialConsole, GraphicalConsole, CommandShell)
- [ ] Create database schema for `console_capabilities` and `console_sessions`
- [ ] Implement database migrations
- [ ] Add caching logic for console capabilities
- [ ] Write unit tests for discovery and caching

### Milestone 2: Serial-over-LAN (IPMI SOL) - Backend
- [ ] Integrate IPMI SOL library (`github.com/gebn/bmc` or equivalent)
- [ ] Implement SOL session handler (connect, bidirectional data flow, disconnect)
- [ ] Implement session lifecycle management (create, active, timeout, cleanup)
- [ ] Add concurrency control and state management
- [ ] Write unit tests with mock IPMI sessions

### Milestone 3: Serial Console - Redfish API
- [ ] Enhance Manager resource handler to include console properties
- [ ] Implement ConnectSerialConsole action handler
- [ ] Implement console session collection and resource handlers
- [ ] Implement disconnect action handler
- [ ] Add request validation and error handling
- [ ] Write unit tests for API handlers

### Milestone 4: Serial Console - WebSocket Gateway
- [ ] Implement WebSocket upgrade and connection handler
- [ ] Implement bidirectional WebSocket-to-SOL proxying
- [ ] Add WebSocket authentication and authorization
- [ ] Implement graceful WebSocket closure handling
- [ ] Write integration tests with real WebSocket clients

### Milestone 5: Serial Console - UI Integration
- [ ] Add "Console" tab to Manager detail page
- [ ] Integrate xterm.js for browser terminal
- [ ] Implement console connection UI workflow
- [ ] Add session management UI (list, disconnect)
- [ ] Test end-to-end SOL console functionality

### Milestone 6: Graphical Console - Vendor Detection
- [ ] Implement vendor detection logic (Dell, Supermicro, HPE)
- [ ] Build vendor capability matrix
- [ ] Query GraphicalConsole properties from BMCs
- [ ] Store vendor-specific capabilities in database
- [ ] Write unit tests for vendor detection

### Milestone 7: Graphical Console - Direct Redirect (Option A)
- [ ] Implement vendor-specific console URL retrieval (Dell iDRAC)
- [ ] Implement temporary token/session creation on BMC
- [ ] Implement ConnectGraphicalConsole action handler (redirect mode)
- [ ] Create UI for graphical console launch (new window)
- [ ] Test with Dell and Supermicro BMCs

### Milestone 8: Graphical Console - VNC Proxying (Option C)
- [ ] Integrate VNC client library
- [ ] Implement VNC session handler
- [ ] Implement WebSocket-to-VNC bridge
- [ ] Integrate noVNC in UI
- [ ] Add VNC authentication handling
- [ ] Write integration tests with VNC servers

### Milestone 9: Command Shell - SSH Proxying
- [ ] Integrate SSH client library (`golang.org/x/crypto/ssh`)
- [ ] Implement SSH session handler with PTY
- [ ] Implement WebSocket-to-SSH proxying
- [ ] Add terminal resize handling
- [ ] Implement ConnectCommandShell action handler
- [ ] Write integration tests with SSH servers

### Milestone 10: Security and Production Hardening
- [ ] Implement idle session timeouts
- [ ] Add rate limiting for console connection requests
- [ ] Implement audit logging for all console operations
- [ ] Add session ownership and access control
- [ ] Security review and penetration testing
- [ ] Performance testing (concurrent sessions, data throughput)

### Milestone 11: Advanced Features
- [ ] Session recording/playback (compliance)
- [ ] Multi-user collaborative console (screen sharing)
- [ ] Console history buffer (reconnect without data loss)
- [ ] Clipboard integration for graphical console
- [ ] File transfer via console (ZMODEM, etc.)
- [ ] Mobile-friendly console UI

## Configuration

New CLI flags and environment variables:

```bash
# Console General
--console-enabled bool                  Enable console pass-through (default: true)
--console-idle-timeout int              Idle timeout for console sessions in minutes (default: 30)
--console-max-sessions-per-bmc int      Max concurrent console sessions per BMC (default: 4)

# Serial-over-LAN (IPMI SOL)
--sol-enabled bool                      Enable Serial-over-LAN (default: true)
--sol-ipmi-port int                     IPMI port for SOL (default: 623)

# Graphical Console
--graphical-console-enabled bool        Enable graphical console (default: true)
--graphical-console-mode string         Mode: "redirect", "proxy", "auto" (default: "auto")
--vnc-proxy-enabled bool                Enable VNC proxying (default: false)

# Command Shell
--command-shell-enabled bool            Enable command shell access (default: true)
--ssh-port int                          SSH port for BMC access (default: 22)

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
- URL generation for redirects

### Integration Tests
- Full SOL session workflow (connect, data flow, disconnect)
- Concurrent SOL sessions to same BMC
- Session timeout and cleanup
- WebSocket connection and authentication
- IPMI SOL protocol communication with mock BMC

### Manual Testing
- Connect to real BMC serial console via Shoal
- Verify bidirectional data flow (keyboard input, serial output)
- Test special keys (Ctrl+C, Ctrl+D, Break)
- Verify graphical console redirect to Dell iDRAC
- Verify VNC proxying (if implemented)
- Test SSH command shell access
- Verify session cleanup on disconnect
- Test concurrent sessions from multiple users
- Verify audit logging captures events

### Performance Testing
- Max concurrent sessions per Shoal instance
- Data throughput (bytes/sec for SOL, VNC)
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
- **IPMI Credentials**: Used by Shoal to establish SOL sessions on behalf of users
- **SSH Keys**: Optional SSH key storage for BMC authentication

### 3. Network Security
- **WSS (WebSocket Secure)**: Mandatory TLS for WebSocket connections in production
- **Management Network Isolation**: IPMI/SSH traffic to BMCs over isolated network
- **No Port Forwarding**: End users never directly access BMC ports (623, 22, 5900)
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
- **Dell iDRAC**: Validate single-use console tokens, enforce token expiration
- **VNC Authentication**: Support VNC password authentication
- **SSH Host Key Verification**: Validate BMC SSH host keys to prevent MITM

## Future Considerations

1. **Session Recording and Playback**: Record console sessions for compliance, audit, and training
2. **Multi-User Collaborative Console**: Allow multiple users to view same console (screen sharing)
3. **Console History Buffer**: Maintain scrollback buffer to survive reconnections
4. **File Transfer**: Support ZMODEM or similar protocols for file transfer via serial console
5. **Clipboard Integration**: Copy/paste support for graphical consoles
6. **Mobile Console**: Mobile-optimized UI for emergency console access
7. **Console Automation**: Scriptable console interactions (expect-like functionality)
8. **Advanced Terminal Features**: Sixel graphics, true color support
9. **Console Notifications**: Alert users when console output matches patterns (e.g., kernel panic)
10. **Cross-Site Consoles**: Aggregate consoles across multiple Shoal instances (multi-datacenter)

## Open Questions

1. **Session Persistence**: Should console sessions survive Shoal restarts? (Likely no for Phase 1)
2. **Shared vs. Exclusive**: Should serial console be exclusive (one user at a time) or shared?
3. **I/O Buffering**: How much console output to buffer for late-joining WebSocket clients?
4. **Vendor Priorities**: Which vendors to prioritize for graphical console beyond Dell and Supermicro?
5. **VNC Security**: How to handle VNC connections without password (unencrypted)?
6. **Audit Log Retention**: How long to keep console I/O audit logs? Privacy implications?
7. **Binary Data**: How to handle binary data in serial console (firmware uploads, etc.)?
8. **Terminal Size**: Default terminal size for SOL sessions (80x24, 80x43, or configurable)?

## References

- [DMTF Redfish Manager Schema v1.10](https://redfish.dmtf.org/schemas/v1/Manager.v1_10_0.json)
- [DMTF Redfish Specification](https://www.dmtf.org/standards/redfish)
- [IPMI v2.0 Specification](https://www.intel.com/content/www/us/en/products/docs/servers/ipmi/ipmi-second-gen-interface-spec-v2-rev1-1.html)
- [RFC 2217: Telnet Com Port Control Option](https://www.rfc-editor.org/rfc/rfc2217) (SOL reference)
- [RFB Protocol (VNC)](https://github.com/rfbproto/rfbproto/blob/master/rfbproto.rst)
- [Dell iDRAC9 Redfish API Guide](https://www.dell.com/support/manuals/en-us/idrac9-lifecycle-controller-v3.x-series/idrac_3.00.00.00_redfishapiguide/)
- [Supermicro IPMI User's Guide](https://www.supermicro.com/manuals/other/IPMI_Users_Guide.pdf)
- [xterm.js - Terminal for the web](https://xtermjs.org/)
- [noVNC - Browser-based VNC client](https://novnc.com/)

## Conclusion

This design provides a comprehensive roadmap for implementing DMTF Redfish-compliant console pass-through in Shoal. By aggregating console capabilities from downstream BMCs and providing protocol-specific proxies (IPMI SOL, VNC, SSH), Shoal enables unified, secure console access to isolated systems.

**Key Design Principles**:
- **DMTF Compliance**: Manager console properties follow Redfish specification
- **OEM Extensions**: Console connection establishment uses Shoal-specific OEM actions (no DMTF standard exists)
- **Protocol Agnostic**: Support for multiple console protocols (IPMI SOL, SSH, VNC)
- **Vendor Extensibility**: Framework for vendor-specific graphical console implementations
- **Security First**: Authentication, authorization, encryption, and audit logging
- **Phase-Based Delivery**: Start with SOL (most critical), expand to graphical and command shell

The architecture leverages Shoal's bastion role to provide secure, unified console access for isolated BMCs, enabling critical troubleshooting, installation, and management workflows without requiring direct network access to BMC management networks.

**Implementation Priority**:
1. **Phase 1**: Serial-over-LAN (IPMI SOL) - Most standardized and critical
2. **Phase 2**: Graphical Console - High user value, vendor-specific complexity
3. **Phase 3**: Command Shell (SSH) - Lower priority, similar to SOL implementation
