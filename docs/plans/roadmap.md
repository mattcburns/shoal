# Roadmap

At-a-glance phase status for Shoal. This page is deliberately short — it
points at the executable per-phase plan docs for detail rather than
restating their task lists. For architecture see
[`docs/design/architecture.md`](../design/architecture.md); for conventions
see [`AGENTS.md`](../../AGENTS.md).

## Status at a glance

| Phase | What | Status |
|-------|------|--------|
| 0 | Lab environment (Ansible, sushy-tools, NetBox, Ollama) | Done |
| 1 | Go scaffolding, config, CLI/API skeleton | Done |
| 2 | Thesis spike — BMC-only provisioning + SOL feedback, no AI/Discover | Done |
| 3 | Discover + Core hybrid normalization (deterministic + AI reconciler) | Done |
| 4 | Observe broaden — SEL/sensor poll, watch mode, status API | Done |
| 5 | Deploy harden — full ISO pipeline, profiles + approval, NetBox lifecycle sync | Done |
| 6a | Bounded payload-write install mode (`SHOAL_INSTALL_MODE=write`) | Done |
| 6b | Graphics failure-screen OCR via `Core.CompleteVision` | Done |
| 6c | Multi-platform packaging + L0 host profiles (secureblue, macOS-as-operator) | Done — [`docs/phase-6c-plan.md`](../phase-6c-plan.md) |
| 6d | Compose `shoal` service, optional API bearer auth, `/metrics`, replay CI | Done — [`docs/phase-6d-plan.md`](../phase-6d-plan.md) |
| 6e+ | Optional polish (more OEM screenshot vendors, registry publish, tracing) | Not started; not blocking anything |
| 7a | Full Ubuntu OS install over BMC-only path (nested-lab cloud image-write) | Done — [`docs/phase-7-plan.md`](../phase-7-plan.md) |
| 7b/7c | Profile/artifact model; second OS family + NetBox identity polish | Superseded — see below |
| Deprovision | `prep=wipe_only` run standalone, `provisioned → ready` | Done — [`docs/deprovision-design.md`](../deprovision-design.md) |
| Multi-stage / OS matrix | Stage runner, prep wipe, offline seed, Flatcar Ignition, operator ISO, profile defaults (M1–M6) | Active — [`docs/multi-stage-provisioning-design.md`](../multi-stage-provisioning-design.md) (verify stage, ESXi/Windows compose remain open) |
| NetBox UI | Provision/deprovision plugin pages inside NetBox | Active — [`docs/netbox-telemetry-ui-design.md`](../netbox-telemetry-ui-design.md) |
| Real-hardware SOL | SSH attach + stdlib IPMI 2.0 SOL as fallback transports | Done — [`docs/sol-transports-design.md`](../sol-transports-design.md), [`docs/real-hardware-sol-runbook.md`](../real-hardware-sol-runbook.md) |

**Original Phase 7b/7c scope (a profile/artifact model, a second OS family,
and NetBox identity polish) is superseded by**
[`docs/multi-stage-provisioning-design.md`](../multi-stage-provisioning-design.md),
which is the current product direction for multi-stage prep (wipe/RAID/
firmware) plus a scripted-ISO OS matrix (autoinstall / kickstart / Ignition /
eventually Windows). Don't implement against the old 7b/7c checklist — follow
the multi-stage design instead.

## Where to look for detail

- Packaging and L0 host profiles: [`docs/phase-6c-plan.md`](../phase-6c-plan.md)
- Compose/auth/metrics/replay: [`docs/phase-6d-plan.md`](../phase-6d-plan.md)
- Full OS install (Phase 7): [`docs/phase-7-plan.md`](../phase-7-plan.md)
- Multi-stage prep + OS matrix (current direction beyond 7a): [`docs/multi-stage-provisioning-design.md`](../multi-stage-provisioning-design.md)
- Deprovision: [`docs/deprovision-design.md`](../deprovision-design.md)
- Real-hardware SOL transports: [`docs/sol-transports-design.md`](../sol-transports-design.md)
- Active field log (real hardware bring-up, not a design doc): [`PROVISIONING_PROGRESS.md`](../../PROVISIONING_PROGRESS.md)

## Not planned

- PXE / provisioning VLANs as a required path (BMC-only stays the thesis)
- Multi-tenancy, RBAC, high-scale (thousands of nodes) orchestration
- Browser UI (API + CLI only, revisit if product prioritizes it)
