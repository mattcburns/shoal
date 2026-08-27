# NetBox integration

NetBox is one of two interchangeable backends for Shoal's device directory
(device identity + lifecycle state) -- the other is a local JSON-file-backed
store for deployments that don't run NetBox. Exactly one backend is active
per process, selected at startup by configuration. The abstraction that
makes them interchangeable (`internal/common/directory`'s `Store` interface,
the selection rule, and the local backend) is described in
[`device-directory.md`](./device-directory.md); **this document covers only
NetBox-specific details** -- the three narrow interfaces `internal/common/netbox`
exposes, its REST-backed implementation, and its config gate. Shoal runs
fully without NetBox configured: every code path that touches it is gated on
configuration and nil-checked downstream, so an unconfigured NetBox is a
normal, expected state -- not an error, and (when `SHOAL_DEVICE_STORE_DIR` is
set) not even a loss of device-directory functionality.

This document describes the Go-side integration (`internal/common/netbox`)
and its consumers. It does not describe the separate Python NetBox plugin;
see the last section for how the two relate.

## The three interfaces

`internal/common/netbox` exposes three narrow interfaces instead of one
broad client type. Consumers depend on the interface they need, not on the
concrete `*Client`:

- **`API`** -- `UpsertDevice(ctx, models.DeviceIdentity) (string, error)`.
  Finds a device by serial or creates one, and records identity fields
  (vendor, model, serial, BMC IP, credential_ref). Used by
  `internal/discover` on ingest/confirm to publish newly discovered assets
  into NetBox.

- **`LifecycleWriter`** -- `SetLifecycle(ctx, deviceKey string, state models.LifecycleState) error`.
  Updates only the `lifecycle_state` custom field for a device. Used by
  `internal/deploy/job`'s orchestrator as a provisioning job transitions
  state (e.g. `provisioning` -> `provisioned` -> `ready`). `internal/core`
  never calls this directly -- lifecycle writes happen at the Deploy layer.

- **`DeviceResolver`** -- `ResolveDeviceID(ctx, key string) (string, error)`.
  Maps an operator-facing device key (hostname, serial, or NetBox numeric
  id) to the NetBox primary key Shoal uses as `device_id`. This lets
  hostname-style lab keys (e.g. `shoal-node-1`) resolve to the same
  `device.pk` the NetBox plugin's UI tabs are keyed by, so jobs started
  with a friendly name still show up in the right place in NetBox.

Two implementations satisfy all three interfaces:

- **`*Client`** (`client.go`) -- the real implementation, backed by NetBox's
  REST API (`/api/dcim/devices/`, `/api/dcim/device-types/`, etc.).
- **`*Memory`** (`client.go`) -- a non-persistent in-memory fake used only
  in unit tests (`internal/common/netbox/client_test.go`,
  `internal/discover/*_test.go`, `internal/cli/credentials_test.go`). It is
  never wired into production code paths.

`var _ API = (*Client)(nil)` style assertions in `client.go` keep both
implementations honest against all three interfaces.

`*Client` also gains `ListDevices` and `DeleteDevice` methods so that it
satisfies the wider `directory.Store` interface (`ListDevices`, `GetDevice`,
`UpsertDevice`, `SetLifecycle`, `ResolveDeviceID`, `DeleteDevice`) described
in `device-directory.md`, alongside the local `FileStore` backend. The three
narrow interfaces above are unchanged and still exist -- `Store` is a
superset, not a replacement, so existing consumers that only need
`netbox.API` or `netbox.LifecycleWriter` keep depending on those narrower
shapes.

## Config-gated construction

NetBox integration is entirely optional and is enabled by two environment
variables, read via `internal/common/config`:

- `SHOAL_NETBOX_URL` -- base URL of the NetBox instance (e.g.
  `http://192.168.122.100:8000`)
- `SHOAL_NETBOX_TOKEN` -- API token for that instance

`internal/cli` is the composition root for NetBox construction (per
AGENTS.md, `internal/cli` and `cmd/shoal` are the only places allowed to
construct concrete types and inject interfaces). This used to be a pattern
repeated ad hoc at every call site (`cli.go`'s `cmdServe`, `deploy.go`'s
`cmdDeployRun` / `cmdDeployCancel` / `cmdDeployDeprovision`, `discover.go`'s
`openDiscoverService`); it is now centralized in a single `buildDirectory`
helper (see `device-directory.md`) that every call site uses instead of
constructing `netbox.New(...)` directly:

```go
func buildDirectory(cfg *config.Config, log *slog.Logger) directory.Store {
    if cfg.NetBoxURL != "" && cfg.NetBoxToken != "" {
        return netbox.New(cfg.NetBoxURL, cfg.NetBoxToken)
    }
    if cfg.DeviceStoreDir != "" {
        return directory.NewFileStore(cfg.DeviceStoreDir)
    }
    return nil
}
// The nil case is passed straight through to the consumer's constructor,
// same as before centralization; never netbox.NewMemory() -- that fake
// exists for tests only.
```

The unconfigured branch (neither NetBox nor a local store directory set)
passes a `nil` interface value straight through to the consumer's
constructor. There is deliberately no in-memory fallback in any production
call site -- a silent, non-persistent fake would let writes appear to
succeed while being discarded on process exit, which is worse than clearly
skipping the step. NetBox configuration takes precedence when both NetBox
and `SHOAL_DEVICE_STORE_DIR` are set -- see `device-directory.md`'s
"Config-gated selection" section for the full precedence rule.

## Degradation when unconfigured

Every consumer nil-checks the interface value before using it, so the
absence of NetBox changes behavior, not correctness:

- `internal/discover/service.go`: `if s.NetBox != nil { ... UpsertDevice ... }`
  around the NetBox upsert step of `Ingest`. When nil, ingest/confirm
  proceed exactly as they do when NetBox succeeds, just without an
  `NetBoxID` on the result.
- `internal/deploy/job/orchestrator.go`: the equivalent guard around
  `SetLifecycle` calls during job state transitions. When nil, jobs run to
  completion; lifecycle_state simply isn't mirrored into NetBox.

In both cases, "NetBox not configured" is logged at most as an informational
or warning line at startup -- it is never surfaced as a request error to a
CLI caller or HTTP client.

`internal/api` does not import `internal/common/netbox` at all; the HTTP
API layer is unaware NetBox exists. `internal/api`'s device-directory routes
(`GET /v1/devices`, `POST /v1/devices` -- see `docs/api/openapi.yaml`) depend
only on `directory.Store`, never on `*netbox.Client` directly. NetBox wiring
proper is confined to the composition root (`internal/cli`) and the two
consumers above.

## Relationship to `extras/netbox-plugin-shoal`

`extras/netbox-plugin-shoal` is a separate Python NetBox plugin (installed
into a NetBox instance, not into Shoal) that calls back into Shoal's HTTP
API to show job status, trigger provisioning, and similar operator UI
actions from within NetBox's device pages. The dependency direction is the
plugin depending on Shoal's HTTP API -- it is *not* a dependency of the Go
binary in the other direction. The Go module never imports, builds, or
runs any Python code, and nothing in `internal/common/netbox` talks to the
plugin directly; both sides only ever talk to NetBox's own REST API and
Shoal's HTTP API, respectively.
