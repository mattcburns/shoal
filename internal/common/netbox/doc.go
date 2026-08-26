// Package netbox implements an optional NetBox REST integration.
//
// NetBox is fully optional at runtime. Every consumer (internal/cli,
// internal/discover, internal/deploy/job) gates construction of a real
// *Client on both SHOAL_NETBOX_URL and SHOAL_NETBOX_TOKEN being set, and
// passes a nil interface value otherwise. Every call site downstream
// nil-checks before use (see internal/discover/service.go's
// `s.NetBox != nil` and internal/deploy/job/orchestrator.go's equivalent
// guard), so Shoal's discover/ingest and deploy/provisioning flows run
// unchanged, with the NetBox step simply skipped, when NetBox is not
// configured. An unconfigured NetBox is not an error condition.
//
// This package stores and updates NetBox device identity and
// lifecycle_state only -- never time-series data or events. Time-series
// telemetry (SEL entries, sensor polls, job logs) lives in
// internal/common/telemetry, not here.
//
// Three narrow interfaces describe the whole surface any consumer needs,
// each satisfied by both *Client (the real HTTP-backed implementation)
// and *Memory (a non-persistent in-memory fake used only in tests):
//
//   - API: UpsertDevice -- find-or-create a device by serial and record
//     its identity fields. Used by internal/discover on ingest/confirm.
//   - LifecycleWriter: SetLifecycle -- update lifecycle_state for a
//     device. Used by internal/deploy/job's orchestrator as jobs
//     transition state; internal/core never calls this directly.
//   - DeviceResolver: ResolveDeviceID -- map an operator-facing device
//     key (hostname, serial, or NetBox numeric id) to the NetBox primary
//     key Shoal uses as device_id, so NetBox plugin tabs keyed by
//     device.pk see the right jobs.
//
// A consumer should depend on the narrowest of these interfaces it needs,
// not on *Client, so it can be constructed with nil (not netbox.NewMemory)
// when NetBox is unconfigured and swapped for *Memory in unit tests.
//
// See docs/design/netbox-integration.md for the full design writeup,
// including how this differs from extras/netbox-plugin-shoal (a separate
// Python NetBox plugin that calls back into Shoal's HTTP API -- not a Go
// dependency in the other direction).
package netbox
