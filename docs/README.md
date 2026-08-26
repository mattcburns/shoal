# Shoal docs index

This directory collects design docs, operational runbooks, historical/completed
plans, and reference material for Shoal. See the root [`README.md`](../README.md)
for how to run the app and the lab day-to-day.

Related docs kept outside this directory:
- [`PROVISIONING_PROGRESS.md`](../PROVISIONING_PROGRESS.md) — an active field
  log of real-hardware provisioning attempts; kept at the repo root because it
  is updated frequently and isn't a static reference.
- [`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](../SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md) —
  the design/implementation source of truth. It's being split into smaller
  documents by a separate, parallel cleanup effort; linked here as-is until
  that lands.

## Design

Architecture and feature designs — some implemented, some proposed.

- [`design/deprovision-design.md`](design/deprovision-design.md) — draft design for returning a node from `provisioned` back to `ready`.
- [`design/multi-stage-provisioning-design.md`](design/multi-stage-provisioning-design.md) — the multi-stage provisioning orchestrator and OS support matrix (stage runner, wipe, offline seed, Ignition, ISO compose).
- [`design/netbox-telemetry-ui-design.md`](design/netbox-telemetry-ui-design.md) — NetBox plugin integration for visual telemetry, events, and job context (backend APIs + device page tabs).
- [`design/sol-transports-design.md`](design/sol-transports-design.md) — real-hardware Serial-over-LAN transports (Dell iDRAC SSH attach and stdlib IPMI SOL).

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
