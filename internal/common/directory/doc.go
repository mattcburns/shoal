// Package directory defines Shoal's device-directory abstraction and a
// local, file-backed implementation of it.
//
// Shoal has no device inventory of its own beyond this package: device
// identity and lifecycle previously lived only in an optional NetBox
// integration (internal/common/netbox). This package introduces Store, a
// single interface both NetBox (a sibling unit adapts *netbox.Client to it)
// and a new local FileStore satisfy, so the rest of Shoal (internal/cli,
// internal/api, a built-in web UI, internal/discover, internal/deploy/job)
// can depend on directory.Store and be configured at runtime to use either
// backend -- never a build tag. Both backends are always compiled into the
// binary; selecting one is a config-time decision made by a caller in
// another unit (e.g. internal/common/config), not by this package.
//
// # Relationship to internal/common/netbox
//
// Store's UpsertDevice, SetLifecycle, and ResolveDeviceID methods have
// signatures that exactly match netbox.API, netbox.LifecycleWriter, and
// netbox.DeviceResolver respectively. This is deliberate: Go interfaces are
// structural, so any concrete type implementing directory.Store already
// satisfies those three narrower, pre-existing interfaces with zero changes
// to internal/discover or internal/deploy/job. Store adds GetDevice and
// DeleteDevice, which those narrower interfaces don't need.
//
// # GetDevice/DeleteDevice vs. SetLifecycle/ResolveDeviceID key semantics
//
// GetDevice and DeleteDevice take a literal id -- the stable primary key
// returned by UpsertDevice / ResolveDeviceID. They do not perform serial or
// name lookups; callers holding an operator-facing key (hostname, serial,
// or id) should call ResolveDeviceID first to obtain the id, exactly as
// internal/deploy/job's orchestrator does today against NetBox.
//
// SetLifecycle and ResolveDeviceID take a deviceKey/key that may be a
// serial, a name, or an id, mirroring netbox.Client's existing fallback
// order: serial match, then name match, then (only if it already names a
// stored device) the key itself as an id. This lets operator-facing code
// pass whatever it has on hand.
//
// # Error handling
//
// GetDevice, DeleteDevice, ResolveDeviceID, and SetLifecycle return
// ErrNotFound (wrapped with fmt.Errorf's %w, so use errors.Is) when the
// requested id/key does not resolve to a stored device.
//
// # Conformance testing
//
// RunConformance (conformance.go) is an exported test helper -- not gated
// behind a build tag or a _test.go suffix -- so it can be imported and
// invoked from any package's tests, including internal/common/netbox's,
// to verify a Store implementation satisfies this package's documented
// contract.
package directory
