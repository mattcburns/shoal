# Shoal docs index

This directory collects design docs, operational runbooks, historical/completed
plans, and reference material for Shoal. See the root [`README.md`](../README.md)
for how to run the app and the lab day-to-day.

Related docs kept outside this directory:
- [`PROVISIONING_PROGRESS.md`](../PROVISIONING_PROGRESS.md) — an active field
  log of real-hardware provisioning attempts; kept at the repo root because it
  is updated frequently and isn't a static reference.

## Design

Architecture and feature designs — some implemented, some proposed.

- [`design/architecture.md`](design/architecture.md) — the architecture/data-model/AI-guideline source of truth (split out of the former root design monolith).
- [`design/deprovision-design.md`](design/deprovision-design.md) — draft design for returning a node from `provisioned` back to `ready`.
- [`design/multi-stage-provisioning-design.md`](design/multi-stage-provisioning-design.md) — the multi-stage provisioning orchestrator and OS support matrix (stage runner, wipe, offline seed, Ignition, ISO compose).
- [`design/netbox-telemetry-ui-design.md`](design/netbox-telemetry-ui-design.md) — NetBox plugin integration for visual telemetry, events, and job context (backend APIs + device page tabs).
- [`design/sol-transports-design.md`](design/sol-transports-design.md) — real-hardware Serial-over-LAN transports (Dell iDRAC SSH attach and stdlib IPMI SOL).
- [`design/netbox-integration.md`](design/netbox-integration.md) — NetBox as an optional integration: the consumer interfaces and how the app degrades when it's unconfigured.
- [`design/device-directory.md`](design/device-directory.md) — the `directory.Store` abstraction (NetBox adapter vs. local file-backed store), config-gated backend selection, and the conformance test that keeps both behaviorally verified.

## API

- [`api/README.md`](api/README.md) — HTTP API conventions (auth, error envelope, pagination).
- [`api/openapi.yaml`](api/openapi.yaml) — hand-written OpenAPI 3.0 spec for every `/v1/*` route.

## Built-in web UI

Shoal includes a server-rendered web UI (`internal/ui`, Go `html/template` +
`//go:embed` — no JS framework, no new dependency) served at `/ui/*`
alongside the existing `/v1/*` JSON API. Like the device-directory
abstraction above, this is delivered by a sibling unit landing in the same
batch as this doc — if `/ui/*` 404s on your build, that unit hasn't merged
yet; this section describes its intended shape. Once merged, it's always
compiled into the binary; nothing needs to be enabled to make `/ui/*`
respond.

Two things make it useful:

- **`SHOAL_DEVICE_STORE_DIR`** — points the UI's device list/add/edit/delete
  pages at the local file-backed `directory.Store` backend when no NetBox
  instance is configured (see
  [`design/device-directory.md`](design/device-directory.md)). Without
  either this or NetBox configured, there's no device directory backend for
  the UI to read or write.
- **`SHOAL_API_TOKEN`** — doubles as the UI's login password. The UI signs
  in with a signed-cookie session; there is no separate UI credential to
  configure or a new secret to manage. If `SHOAL_API_TOKEN` is empty, the
  same "auth disabled" behavior that applies to `/v1/*`
  (see [`api/README.md`](api/README.md#authentication)) applies to the UI
  login.

Per-device pages mirror the tabs the `extras/netbox-plugin-shoal` NetBox
plugin already shows on a NetBox device page — Status/Provisioning/Power/
Credentials, Events, Jobs, Sensors, Firmware — but rendered natively by
Shoal, calling the same in-process Go service objects `internal/api`'s
handlers call (not proxying to `/v1/*` over HTTP).

**Built-in UI vs. the NetBox plugin:** use the built-in UI when you want
zero extra infrastructure — a simple, single-operator lab that has no
NetBox instance (or doesn't want Shoal wired into one). Use the NetBox
plugin when you already run NetBox for DCIM/IPAM and want Shoal's
provisioning/telemetry surfaced inside the tool your fleet inventory
already lives in, alongside every other device record. Both read the same
underlying data through the same service layer; neither is more
"authoritative" than the other.

## Plans

- [`plans/roadmap.md`](plans/roadmap.md) — at-a-glance phase status, linking out to the archive/design docs below instead of restating them.

## Runbooks

Operational how-tos for setting up and running the lab.

- [`runbooks/lab-runbook.md`](runbooks/lab-runbook.md) — day-to-day quick ops for the running lab (bring-up, health checks, recovery).
- [`runbooks/lab-setup-checklist.md`](runbooks/lab-setup-checklist.md) — first-time lab setup checklist.
- [`runbooks/lab-setup-debian.md`](runbooks/lab-setup-debian.md) — step-by-step guide to standing up the VM-hosted lab on a Debian (or Debian-family) L0 host.
- [`runbooks/operator-macos.md`](runbooks/operator-macos.md) — running the Shoal binary from a macOS operator machine against a remote Linux-hosted lab.
- [`runbooks/real-hardware-sol-runbook.md`](runbooks/real-hardware-sol-runbook.md) — recorded live-attach procedure and results for Serial-over-LAN against real BMC hardware.
- [`runbooks/phase2-live-image.md`](runbooks/phase2-live-image.md) — building the minimal live image / marker producer that emits progress markers over serial.

## Archive

Completed or historical plans, kept for record-keeping — not current guidance.

- [`archive/phase-6c-plan.md`](archive/phase-6c-plan.md) — packaging and L0 host profile plan; superseded by shipped code.
- [`archive/phase-6d-plan.md`](archive/phase-6d-plan.md) — Compose app, auth, and metrics plan; fully checked off.
- [`archive/phase-7-plan.md`](archive/phase-7-plan.md) — full OS autoinstall plan; explicitly marked complete/deferred by sub-phase.

## Reference

Standing reference material.

- [`reference/third-party-licenses.md`](reference/third-party-licenses.md) — reproduced license texts for third-party Go modules linked into the Shoal binary.
