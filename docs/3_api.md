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
