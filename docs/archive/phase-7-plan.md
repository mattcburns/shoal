# Phase 7 — Full OS install (BMC + SOL)

Executable checklist for design **v2.0.9** § Phase 7. Design SoT:
[`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](../../SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md).

## Goal

Install a **real OS** onto local disk over the **BMC-only** path (Redfish Virtual
Media + SOL markers), reboot into the installed system, and keep cleanup +
lifecycle rules intact.

**Not this phase:** Phase **6a** bounded `/payload` write (still supported).
**Not required first:** Phase **6e+** polish (OEM screenshots, registry, tracing).

## Principles

- No PXE / provisioning VLAN for the install loop
- SOL primary progress (`DISK_PREP` → `IMAGE_WRITE` → `POSTINSTALL` → `VERIFY` → `DONE` / `ERROR`)
- Orchestrator sole lifecycle writer; JobStore pure persistence
- Secrets never in published ISO, SOL logs, slog, or LLM payloads
- Mandatory Virtual Media eject + boot override clear on all terminals
  (sushy Continuous/Hdd after clear is OK)

## Sub-phases

### 7a — Ubuntu nested-lab E2E — **COMPLETE**

| # | Task | AC | Status |
|---|------|-----|--------|
| A1 | Media pipeline for full Ubuntu on disk | Marker ISO + cloud payload **or** live-server remaster | **Done** (preferred: cloud image-write) |
| A2 | SOL producer markers + heartbeats | Marker `/init` write path markers | **Done** |
| A3 | Deploy job: attach, boot, watch, cleanup | Orchestrator + ForceRestart + post-check | **Done** |
| A4 | Lab: nested libvirt guest with real disk | Documented E2E; bootable Ubuntu + login | **Done** |
| A5 | Regression: `simulate` + 6a `write` | Unit tests / modes unchanged | **Done** |

**Preferred nested-lab path (shipped):**

1. `prepare-ubuntu-cloud-payload.sh` — customize Ubuntu cloud image → `.raw.gz`
2. `build-marker-iso.sh` with `SHOAL_INSTALL_MODE=autoinstall` + large payload on ISO root
3. Publish to `/srv/iso`, attach via sushy Virtual Media (`http://192.168.124.1:8080/…`)
4. Guest: mount CD → `gunzip|dd` → `/dev/vda` → SOL `DONE` → reboot
5. Job state **`provisioned`**

**Alternate / stretch:** `build-ubuntu-autoinstall-iso.sh` live-server remaster (autoinstall
user-data). Unreliable under nested sushy; keep for future hardware / better media fidelity.

**Operator docs:** [`lab-runbook.md`](../runbooks/lab-runbook.md) § Phase 7a.

### 7b — Profile + artifact model — **DEFERRED**

Original B1–B4 (install profiles, resolve ISO without hand flags) deferred pending the
**multi-stage provisioning + OS matrix** design document. Do not implement under this
checklist without that design.

### 7c — Second family + identity polish — **DEFERRED**

Original C1–C3 deferred for the same reason. Image-write for Ubuntu is already the 7a
path; further families (kickstart, Ignition, Windows) belong in the new design.

## Non-goals (7.0 / remaining)

- Multi-stage prep (wipe/RAID/firmware) then OS installer — **new design**
- Windows, ESXi, Flatcar as Phase 7 deliverables
- PXE-required topology
- OCR as install progress loop

## PR history

1. **PR18:** design v2.0.8 + this plan (initial Phase 7)
2. **PR19:** 7a implementation + v2.0.9 closeout (image-write preferred)

## Verify

```bash
gofmt -l .
go vet ./...
staticcheck ./...
go test ./...

# Lab E2E shape (see lab-runbook Phase 7a):
# prepare cloud payload → build marker ISO → deploy run -iso-url … -wait
```

## Done when

- **7a:** ACs above met (this closeout).  
- **7b/7c:** Not required to call Phase 7 “fully product-complete”; tracked via deferred
  rows + future multi-stage design.  
- Golden Rules §1 intact; 6a / Phase 2 paths remain green.
