// Package directory defines the device-identity directory contract shared by
// the built-in web UI (internal/ui) and CLI wiring: Store persists
// models.DeviceIdentity records (name/serial/vendor/model/lifecycle/bmc_ip/
// credential_ref) independent of NetBox, so an operator can run Shoal without
// a NetBox instance.
//
// A file-backed Store is selected at runtime via config (never a build tag);
// a NetBox-backed Store may be added alongside it later, gated the same way.
// Both backends compile into the binary unconditionally.
//
// Provisional note: this package was written by the internal/ui unit as a
// self-contained stand-in because the sibling "CLI directory wiring" unit
// that owns internal/common/directory had not landed a fuller implementation
// yet when this PR was authored. It implements exactly the Store shape that
// unit was briefed against. If that unit's own internal/common/directory
// lands separately, resolve the merge by keeping its implementation as long
// as the Store interface below (method set + ErrNotFound) is preserved --
// internal/ui only depends on the interface, not on FileStore.
package directory
