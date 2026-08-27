# Shoal HTTP API

Shoal exposes a small `net/http` API (default `:8088`, `SHOAL_HTTP_ADDR`) for
Discover (asset ingest), Observe (device status/events/sensors/firmware/job
log), Deploy (provisioning jobs, on-demand power, on-demand poll), a device
directory (`GET`/`POST /v1/devices`, backed by `internal/common/directory.Store`
-- currently a local JSON file store or an in-memory fallback; a NetBox-backed
adapter is planned but not yet wired into `shoal serve`), stored device
credentials, and saved provisioning profiles. Handlers live in
`internal/api/*.go` and are registered in `internal/api/server.go`.

The full route-by-route reference — methods, path/query parameters, request
and response bodies, and status codes — is in [`openapi.yaml`](./openapi.yaml)
(OpenAPI 3.0, hand-written; no code generator or build-time dependency on
it). This file states the conventions that apply across all routes.

## Authentication

Every `/v1/*` route requires:

```
Authorization: Bearer <token>
```

The token is whatever `SHOAL_API_TOKEN` was set to when the server started.
Auth is blanket middleware (`internal/api/auth.go`) applied ahead of the
route mux — there is no per-route opt-out. A missing or mismatched token
returns `401` with `{"error": "unauthorized"}`.

**If `SHOAL_API_TOKEN` is empty, auth is disabled entirely** — every `/v1/*`
route is open. This is the lab default; set a token before exposing the API
beyond a trusted network.

`/healthz`, `/readyz`, and `/metrics` never require auth, so probes and
scrapers keep working regardless of token configuration.

## Error envelope

Every non-2xx JSON response is:

```json
{ "error": "<message>" }
```

Some 409 responses (partial job start) add a `job` field alongside `error`
— see `POST /v1/jobs` in the spec. A few `503` responses mean "this server
instance wasn't configured with the component this route needs" (e.g. no
`SHOAL_PROFILE_DIR`, no NetBox, no telemetry database) rather than a request
error — that distinction is called out per-route in `openapi.yaml`.

Failures from a configured backend dependency (the telemetry store, the job
store, or a BMC Redfish call) are reported as **502 Bad Gateway** with a
generic `error` message — the underlying error is logged server-side but
never echoed back into the response body, so a client never sees raw
internal error text (DSNs, stack fragments, etc.) in an API response.

## Pagination

List endpoints (`GET /v1/jobs/{id}/log`, `GET /v1/devices`,
`GET /v1/devices/{id}/events`, `GET /v1/devices/{id}/jobs`,
`GET /v1/devices/{id}/sensors`, `GET /v1/devices/{id}/firmware`) accept
`?limit=`. Values above the server-side maximum of **200** are clamped to
200 — a caller cannot force an unbounded scan or response. Defaults vary by
endpoint (see `openapi.yaml` for the exact default per route); most default
to 50, while `GET /v1/devices` and the sensors/firmware endpoints default to
the 200 maximum (there's no `since` cursor to page against, so a full list
is preferred up to that cap).

Time-bounded list endpoints also accept `?since=` (RFC3339). An unparseable
`since` is treated as "no filter" rather than a 400, mirroring how an
unrecognized `?state=` on `GET /v1/devices/{id}/jobs` matches no rows
instead of erroring.

## JSON naming

All request and response bodies use **snake_case** field names throughout
(`device_id`, `bmc_endpoint`, `lifecycle_state`, `needs_review`, ...) —
verified consistent across every model in `internal/common/models` and
every handler-local request/response struct in `internal/api`.

## See also

- [`openapi.yaml`](./openapi.yaml) — full route reference.
- [`internal/api/`](../../internal/api/) — handler implementations.
- [`internal/common/models/models.go`](../../internal/common/models/models.go) — shared request/response structs.
