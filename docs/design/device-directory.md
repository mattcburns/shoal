# Device directory abstraction

**Status:** design — describes the intended shape of `internal/common/directory`
and its two backends. Some pieces referenced here may still be landing in
sibling PRs; if the code and this doc disagree, the code wins and this doc
should be corrected in the same change (see `AGENTS.md` header).

**Related:** [`docs/design/netbox-integration.md`](./netbox-integration.md) —
NetBox specifics as one of the two backends described here.
[`AGENTS.md`](../../AGENTS.md) Golden Rules — the "one active backend,
config-gated" invariant this doc explains is enforced there.

## Why this exists

Before this change, "where does device identity + lifecycle state live" meant
"NetBox, if configured" — a single hardcoded backend, wired ad hoc at each
call site in `internal/cli`. That's fine for a lab that already runs NetBox,
but it means a single-operator deployment with no NetBox instance has no
device directory at all: no way to list known devices, no `device_id` ->
BMC/credential mapping, nothing for the new built-in web UI to render.

`internal/common/directory` generalizes "device identity + lifecycle store"
into an interface with two implementations: the existing NetBox client, and a
new local JSON-file-backed store that needs no extra infrastructure. Exactly
one is active per running Shoal process, chosen by configuration at startup.
This keeps Golden Rule 4 intact (identity + current `lifecycle_state` only —
no time-series, no events, no job logs) — the rule now describes what any
`directory.Store` implementation is scoped to, not just NetBox.

## The `Store` interface

```go
package directory

type Store interface {
    ListDevices(ctx context.Context) ([]models.DeviceIdentity, error)
    GetDevice(ctx context.Context, key string) (models.DeviceIdentity, error)
    UpsertDevice(ctx context.Context, id models.DeviceIdentity) (string, error)
    SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error
    ResolveDeviceID(ctx context.Context, key string) (string, error)
    DeleteDevice(ctx context.Context, key string) error
}
```

This is a superset of the three narrow interfaces `internal/common/netbox`
already exposes (`API`, `LifecycleWriter`, `DeviceResolver` — see
`netbox-integration.md`). `UpsertDevice`, `SetLifecycle`, `ResolveDeviceID`,
and `GetDevice` keep their existing signatures unchanged (all four already
exist on `*netbox.Client` today); `ListDevices` and `DeleteDevice` are the
two genuinely new methods that make a full device directory (not just
write-shaped operations plus a single-device lookup) possible.

Consumers that only need a subset (e.g. `internal/deploy/job`'s orchestrator,
which only calls `SetLifecycle`) can continue to depend on the narrower
`netbox.LifecycleWriter`-shaped interface rather than the full `Store` — Go's
structural typing means a `directory.Store` value satisfies any interface
that is a subset of its method set, so nothing downstream of construction
needs to change to accommodate the wider interface. See "Why existing
consumers don't change" below.

## The two backends

### NetBox adapter

`*netbox.Client` (`internal/common/netbox/client.go`) already implements
`UpsertDevice`, `SetLifecycle`, `ResolveDeviceID`, and `GetDevice`. A sibling
change adds `ListDevices` and `DeleteDevice` methods to the same type so that
`*netbox.Client` satisfies `directory.Store` directly — no separate adapter
struct, no wrapping. NetBox remains the system of record for identity and
`lifecycle_state` exactly as described in `netbox-integration.md`; this
backend is a thin passthrough onto NetBox's REST API (`/api/dcim/devices/`).

### Local `FileStore`

`directory.FileStore` (`internal/common/directory`) is a new backend for
deployments that don't run NetBox: it persists one JSON document per device
(or a single JSON file, implementation detail) under a directory named by
`SHOAL_DEVICE_STORE_DIR`. It requires no database, no external service, and
no network call — just a writable directory on the host running Shoal. It
exists specifically so the new built-in web UI (see `docs/README.md`) has
something to manage devices against in a deployment with no NetBox instance.

`FileStore` implements the identical `Store` interface with the identical
semantics (same lookup-by-serial/name/id resolution rules `ResolveDeviceID`
documents for NetBox, same "device not found" error shape, same lifecycle
enum from `internal/common/models`) — see "Conformance testing" below for how
that's verified rather than just asserted.

## Config-gated selection: exactly one active backend

`internal/cli` gains a `buildDirectory(cfg, log) directory.Store` helper that
replaces the several scattered ad hoc `netbox.New(...)` constructions
previously repeated at each call site (`cmdServe`, `deploy.go`'s
`cmdDeployRun` / `cmdDeployCancel` / `cmdDeployDeprovision`, `discover.go`'s
`openDiscoverService` — see `netbox-integration.md`'s "Config-gated
construction" section for the pattern this replaces). The selection rule:

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
```

- **`SHOAL_NETBOX_URL` and `SHOAL_NETBOX_TOKEN` both set** -> NetBox backend.
  Matches the existing NetBox config-gate exactly (`netbox-integration.md`),
  so nothing changes for a deployment that already runs the lab's NetBox
  container.
- **Neither set, but `SHOAL_DEVICE_STORE_DIR` set** -> local `FileStore`.
  This is the new path: a simple/single-operator lab with no NetBox instance
  still gets a working device directory, and therefore a working device list
  in the built-in UI.
- **Neither configured** -> `nil`, same as today's NetBox-unconfigured case.
  Every consumer already nil-checks before use (`netbox-integration.md`
  "Degradation when unconfigured"); that nil-check pattern is unchanged and
  now covers "no directory backend of any kind" rather than just "no
  NetBox".

**Both backends are always compiled into the one binary.** This is
deliberately a runtime config gate, not a Go build tag — there is no
`//go:build` split between "NetBox edition" and "local edition" of the Shoal
binary. A single `shoal` binary works either way depending on which
environment variables are set when it starts, matching how NetBox
configuration already works today.

Precedence is NetBox-first: if both `SHOAL_NETBOX_URL`/`TOKEN` and
`SHOAL_DEVICE_STORE_DIR` happen to be set, NetBox wins and the file store is
never opened. There is no dual-write and no migration path between the two —
picking a backend is a deployment-time decision, not something Shoal
reconciles across.

## Why existing consumers don't change

`internal/discover` and `internal/deploy/job` were written against the
narrower `netbox.API` / `netbox.LifecycleWriter` interfaces, not against a
concrete `*netbox.Client`. Because `directory.Store`'s method set is a
superset of those narrower interfaces, any value that satisfies `Store` also
satisfies them — Go doesn't require an explicit adapter or wrapper type for
this to work. Concretely:

- `internal/discover/service.go`'s `Ingest` still just needs something with
  `UpsertDevice`; whether that something is a `*netbox.Client` or a
  `*directory.FileStore` passed in as a `netbox.API`-shaped value is invisible
  to it.
- `internal/deploy/job`'s orchestrator still just needs `SetLifecycle`.

So this change is scoped to **construction only**: `internal/cli`'s
`buildDirectory` decides which concrete backend to build and hands it to
existing consumers through their existing narrow interface types, exactly as
`netbox.New(...)` was handed to them before. Neither package needed a single
line changed to gain "local file store" support. This is the payoff of the
existing narrow-interface design described in `netbox-integration.md` — it
was already set up to make the backend swappable, this change just adds a
second implementation and a place to choose between them.

`internal/api` continues to not import either `internal/common/netbox` or
`internal/common/directory`'s concrete backends directly wherever avoidable;
new routes that need directory access (`GET /v1/devices`, `POST /v1/devices`
— see `docs/api/openapi.yaml`) depend on `directory.Store` the same way
existing handlers depend on other narrow port interfaces.

## Conformance testing

`directory.RunConformance(t, store)` is a shared test helper (not a
`_test.go` file itself, so it can be imported from other packages' test
files) that exercises the full `Store` interface against a given instance:
create, list, get, resolve-by-key (serial/name/id), lifecycle transition,
delete, and the "not found" error shape for a missing key. Both
`internal/common/netbox`'s test suite and `internal/common/directory`'s
`FileStore` test suite call `RunConformance` against their respective
backend, so the two implementations are held to one behavioral contract
instead of two independently-asserted ones drifting apart over time. A
future third backend (if one is ever added) gets the same guarantee for
free by calling the same helper.

## What this doc doesn't cover

- NetBox-specific details (the three original narrow interfaces, REST
  endpoints used, classification/role/manufacturer bootstrapping, the
  Python NetBox plugin) stay in `netbox-integration.md`.
- The built-in web UI that consumes `directory.Store` (`internal/ui`) is
  covered in `docs/README.md`'s "Built-in web UI" section.
- The `GET /v1/devices` / `POST /v1/devices` HTTP routes are covered in
  `docs/api/openapi.yaml` and `docs/api/README.md`.
