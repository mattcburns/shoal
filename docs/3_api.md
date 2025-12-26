# API Guide

Shoal provides a Redfish-compliant API for programmatic management.

## Authentication

The API supports two authentication methods:

1.  **HTTP Basic Auth**: Include credentials in the `Authorization` header.
    ```bash
    curl -u admin:admin http://localhost:8080/redfish/v1/
    ```

2.  **Redfish Sessions**: Create a session to get a token, then use the `X-Auth-Token` header.
    ```bash
    # Create session
    curl -X POST http://localhost:8080/redfish/v1/SessionService/Sessions \
      -H "Content-Type: application/json" \
      -d '{"UserName": "admin", "Password": "admin"}'

    # Use session token
    curl -H "X-Auth-Token: <token>" http://localhost:8080/redfish/v1/
    ```

## Core Redfish Endpoints

- `GET /redfish/v1/`: Service root.
- `GET /redfish/v1/EventService`: Minimal EventService stub (ServiceEnabled=false).
- `GET /redfish/v1/TaskService`: Minimal TaskService stub.
- `GET /redfish/v1/TaskService/Tasks`: Empty Tasks collection.
- `GET /redfish/v1/Managers`: List of aggregated managers from all BMCs.
- `GET /redfish/v1/Systems`: List of aggregated systems from all BMCs.
- `GET /redfish/v1/Managers/{bmc-name}`: Proxy to a specific BMC manager.
- `GET /redfish/v1/Systems/{bmc-name}`: Proxy to a specific system.
- `GET /redfish/v1/SessionService`: Session service root.

### Protocol Compliance Endpoints (Phase 1)

- `GET /redfish/v1/$metadata` (no auth): OData CSDL describing the service. Returns `Content-Type: application/xml` and `OData-Version: 4.0` with strong `ETag` support.
- `GET /redfish/v1/Registries` (auth required): Message registries collection (includes Base).
  - `GET /redfish/v1/Registries/Base` (auth required): Base registry file (en locale).
  - `GET /redfish/v1/Registries/Base/Base.json` (auth required): Explicit locale path.
- `GET /redfish/v1/SchemaStore` (auth required): JSON Schema store root enumerating embedded schemas. Shoal embeds the minimal set required by the Redfish Service Validator, including `ServiceRoot`, `AccountService`, `ManagerAccount`, `Role`, `SessionService`, `Session`, `AggregationService`, `ConnectionMethod`, `EventService`, `TaskService`, and the `Message.v1_1_0` schema referenced by `@Message.ExtendedInfo`.
  - `GET /redfish/v1/SchemaStore/{SchemaName}.vX_Y_Z.json` (auth required): Individual schema file.

### Caching and ETags

Shoal includes HTTP ETag support for both static Redfish assets and mutable resources to improve client-side caching:

- `GET /redfish/v1/$metadata`
- `GET /redfish/v1/Registries/{name}[/{file}]` (e.g., Base)
- `GET /redfish/v1/SchemaStore/{path}.json`
- `GET /redfish/v1/AccountService/Accounts`
- `GET /redfish/v1/AccountService/Accounts/{id}`
- `GET /redfish/v1/AggregationService/ConnectionMethods`
- `GET /redfish/v1/AggregationService/ConnectionMethods/{id}`

Static documents use strong validators derived from the exact content hash, while Accounts and ConnectionMethods emit weak validators that change whenever the underlying record is updated. Clients may send `If-None-Match` with the previously received ETag to receive `304 Not Modified` when content has not changed.

Examples:

```bash
# $metadata (no auth)
curl -i http://localhost:8080/redfish/v1/$metadata

# Registries (requires session token)
curl -s -X POST http://localhost:8080/redfish/v1/SessionService/Sessions \
  -H 'Content-Type: application/json' \
  -d '{"UserName":"admin","Password":"admin"}' | jq -r '. | .@odata.id' >/dev/null
TOKEN=$(curl -s -X POST http://localhost:8080/redfish/v1/SessionService/Sessions \
  -H 'Content-Type: application/json' \
  -d '{"UserName":"admin","Password":"admin"}' -D - 2>/dev/null | awk '/X-Auth-Token:/ {print $2}' | tr -d '\r')
curl -i -H "X-Auth-Token: $TOKEN" http://localhost:8080/redfish/v1/Registries/Base

# Conditional GET using ETag
ETAG=$(curl -sI -H "X-Auth-Token: $TOKEN" http://localhost:8080/redfish/v1/Registries/Base | awk -F': ' '/^ETag:/ {print $2}' | tr -d '\r')
curl -i -H "X-Auth-Token: $TOKEN" -H "If-None-Match: $ETAG" http://localhost:8080/redfish/v1/Registries/Base

# Schema file
curl -i -H "X-Auth-Token: $TOKEN" http://localhost:8080/redfish/v1/SchemaStore/ServiceRoot.v1_5_0.json
```

### OPTIONS and Allow

Shoal advertises supported HTTP methods via OPTIONS with the `Allow` header on key resources. Examples:

- `OPTIONS /redfish/v1/` → `Allow: GET`
- `OPTIONS /redfish/v1/AccountService/Accounts` → `Allow: GET, POST`
- `OPTIONS /redfish/v1/AccountService/Accounts/{id}` → `Allow: GET, PATCH, DELETE`
- `OPTIONS /redfish/v1/SessionService/Sessions` → `Allow: GET, POST` (accessible without auth)

All Redfish JSON responses include `OData-Version: 4.0`.

### Error Responses and Message Registries

Shoal returns Redfish-compliant error envelopes that include `@Message.ExtendedInfo`. The `MessageId` values map to entries in the Base Message Registry, allowing clients to correlate errors with standardized messages.

- Example `MessageId` values: `Base.1.0.Unauthorized`, `Base.1.0.MethodNotAllowed`, `Base.1.0.ResourceNotFound`, `Base.1.0.InsufficientPrivilege`, `Base.1.0.MalformedJSON`, `Base.1.0.PropertyMissing`, `Base.1.0.PropertyValueNotInList`, `Base.1.0.ResourceCannotBeCreated`, `Base.1.0.NotImplemented`, `Base.1.0.InternalError`, and `Base.1.0.GeneralError`.
- The Base registry is available at `/redfish/v1/Registries/Base` (and `/redfish/v1/Registries/Base/Base.json`).
- 401 responses also include `WWW-Authenticate: Basic realm="Redfish"`.

Sample error payload:

```json
{
  "error": {
    "code": "Base.1.0.Unauthorized",
    "message": "Authentication required",
    "@Message.ExtendedInfo": [
      {
        "@odata.type": "#Message.v1_1_0.Message",
        "MessageId": "Base.1.0.Unauthorized",
        "Message": "Authentication required",
        "Severity": "Critical",
        "Resolution": "Provide valid credentials and resubmit the request."
      }
    ]
  }
}
```

## Account Management (AccountService)

Shoal now implements the Redfish AccountService for managing local user accounts.

### Endpoints

- `GET /redfish/v1/AccountService` (auth required): AccountService root with links to Accounts and Roles collections.
- `GET /redfish/v1/AccountService/Accounts` (Admin only): List all local accounts.
- `POST /redfish/v1/AccountService/Accounts` (Admin only): Create a new account. Provide `UserName`, `Password`, optional `Enabled` (default `true`), and `RoleId` (`Administrator`, `Operator`, or `ReadOnly`).
- `GET /redfish/v1/AccountService/Accounts/{id}` (Admin only): Retrieve account details.
- `PATCH /redfish/v1/AccountService/Accounts/{id}` (Admin only): Update `Enabled`, `RoleId`, or `Password`. Password updates are immediately hashed and stored securely.
- `DELETE /redfish/v1/AccountService/Accounts/{id}` (Admin only): Remove a non-admin account.
- `GET /redfish/v1/AccountService/Roles` (auth required): List available Redfish roles.
- `GET /redfish/v1/AccountService/Roles/{roleId}` (auth required): Retrieve details for a specific role.

### Role-Based Access Control

- **Administrator**: Full control over all AccountService resources. Cannot be disabled or deleted via the API to prevent lockout.
- **Operator**: Operational capabilities for BMC resources but no user management privileges.
- **ReadOnly**: View-only access to Redfish resources; no mutation privileges.

Account collection and resource mutations are blocked for non-admin users and return `403 Forbidden` with `Base.1.0.InsufficientPrivilege`. All AccountService endpoints require a valid session or basic authentication.

### Validation and Error Messaging

- Missing required fields return `400 Bad Request` with `Base.1.0.PropertyMissing`.
- Invalid role selections return `400 Bad Request` with `Base.1.0.PropertyValueNotInList`.
- Malformed JSON payloads return `400 Bad Request` with `Base.1.0.MalformedJSON`.
- Attempting to create a duplicate username returns `409 Conflict` with `Base.1.0.ResourceCannotBeCreated`.
- Operations that are not yet implemented respond with `Base.1.0.NotImplemented`.

Refer to the Base message registry for full descriptions and recommended resolutions.

## Settings Discovery

- `GET /api/bmcs/{bmc-name}/settings`: Returns discovered configurable settings for a BMC.
  - **Query Parameters:**
    - `resource`: Filter to a specific Redfish resource path (e.g., `EthernetInterfaces`, `/Storage`).
    - `search`: Free-text filter across attribute, display name, description, etc.
    - `oem`: Filter by OEM vs. non-OEM (`true` or `false`).
    - `page` / `page_size`: For pagination.
    - `refresh`: `true` to bypass caches and force re-discovery (requires Operator or Admin).
  - **Scope**: Includes settings from `Bios`, `ManagerNetworkProtocol`, `EthernetInterfaces`, and `Storage` resources.
  - **Enrichment**: Descriptors are enriched with metadata from Redfish Attribute Registries and `ActionInfo`.

**Example:**
```bash
curl -s -u admin:admin \
  "http://localhost:8080/api/bmcs/bmc1/settings?resource=EthernetInterfaces" | jq .
```


## DMTF Standard AggregationService

Shoal implements the DMTF Redfish AggregationService standard for programmatic BMC management.

- `GET /redfish/v1/AggregationService/ConnectionMethods`: List connection methods (BMCs).
- `POST /redfish/v1/AggregationService/ConnectionMethods`: Add a new BMC connection.
- `DELETE /redfish/v1/AggregationService/ConnectionMethods/{id}`: Remove a BMC connection.

**Example: Add a BMC**
```bash
curl -X POST http://localhost:8080/redfish/v1/AggregationService/ConnectionMethods \
  -H "Content-Type: application/json" \
  -H "X-Auth-Token: <token>" \
  -d '{
    "Name": "Production Server BMC",
    "ConnectionMethodType": "Redfish",
    "ConnectionMethodVariant.Address": "192.168.1.100",
    "ConnectionMethodVariant.Authentication": {
      "Username": "admin",
      "Password": "password"
    }
  }'
```

## Virtual Media Management

Shoal provides DMTF Redfish-compliant VirtualMedia endpoints for attaching and ejecting virtual media (ISO images, disk images) to/from managed BMCs.

### Endpoints

- `GET /redfish/v1/Managers/{bmc-name}/VirtualMedia`: List virtual media resources for a specific manager.
- `GET /redfish/v1/Managers/{bmc-name}/VirtualMedia/{mediaId}`: Get details of a specific virtual media resource.
- `POST /redfish/v1/Managers/{bmc-name}/VirtualMedia/{mediaId}/Actions/VirtualMedia.InsertMedia`: Attach virtual media from an external image URL.
- `POST /redfish/v1/Managers/{bmc-name}/VirtualMedia/{mediaId}/Actions/VirtualMedia.EjectMedia`: Detach currently attached virtual media.

### Insert Media

Attach an ISO or disk image to a virtual media slot. The BMC will download the image from the provided URL.

**Request:**
```bash
curl -X POST \
  http://localhost:8080/redfish/v1/Managers/bmc1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia \
  -H "X-Auth-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "Image": "http://fileserver.example.com/ubuntu-22.04.iso",
    "Inserted": true,
    "WriteProtected": true
  }'
```

**Parameters:**
- `Image` (required): URL of the image file (HTTP, HTTPS, NFS, or CIFS).
- `Inserted` (optional): Whether to insert the media (default: true).
- `WriteProtected` (optional): Whether the media is write-protected (default: false).
- `TransferProtocolType` (optional): Protocol type (e.g., "HTTP", "HTTPS", "NFS").
- `UserName` (optional): Username for authenticated image servers.
- `Password` (optional): Password for authenticated image servers.

**Response:** `204 No Content` on success.

**Operation Tracking:** Each InsertMedia operation is recorded in Shoal's database with status tracking (pending → success/failed), username, timestamp, and any error messages.

### Eject Media

Detach currently attached virtual media from a slot.

**Request:**
```bash
curl -X POST \
  http://localhost:8080/redfish/v1/Managers/bmc1/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia \
  -H "X-Auth-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{}'
```

**Response:** `204 No Content` on success.

**State Updates:** After successful eject, the resource state is updated to clear the image URL and set `Inserted` to false.

### Error Handling

- `400 Bad Request`: Missing required fields or invalid request format.
- `404 Not Found`: Manager or VirtualMedia resource not found.
- `502 Bad Gateway`: BMC communication failure.
- Errors from the downstream BMC are forwarded with original status codes and response bodies.

All VirtualMedia operations require authentication (session token or basic auth).
