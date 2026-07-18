# Multi-stage provisioning orchestrator & OS support matrix

**Status:** Design draft (implementation not started)  
**Date:** July 2026  
**Audience:** Human architect + coding agents  
**Related:** Design SoT `SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md` (v2.0.9+);
Phase **7a complete** (Ubuntu nested-lab cloud image-write). This document **supersedes**
deferred Phase **7b/7c** checklists for product direction.

---

## 1. Purpose

Specify how Shoal should provision **real operating systems** on bare metal (and
nested lab guests) beyond the single-media Phase 7a image-write path:

1. **Optional prep stage** — live maintenance image (wipe, RAID, firmware-related
   host prep) with SOL progress.
2. **OS install stage** — family-specific **scripted installer ISO** (or golden
   **image-write**) attached via Virtual Media.
3. **Optional verify stage** — boot installed OS; confirm health / identity.
4. **OS support matrix** — Ubuntu, Flatcar, VMware ESXi, later Windows.

This is a **Deploy Orchestrator** evolution, not a new microservice. Golden Rules
still apply (BMC-only control path, SOL primary progress, Orchestrator sole
lifecycle writer, secrets never to logs/LLM, mandatory media cleanup).

---

## 2. Problem statement

### 2.1 What 7a proved

- Nested lab: Virtual Media + SOL + marker producer → **bootable Ubuntu** → job
  **`provisioned`**.
- Preferred mechanism: **cloud image-write** (`gunzip|dd`), not live-server
  autoinstall remaster (unreliable under nested sushy).

### 2.2 What 7a does not solve

| Need | Gap |
|------|-----|
| ESXi / Flatcar / Windows | Not disk-clone products; need **installer + answer file** |
| Wipe / RAID / pre-config | Must run **before** OS installer, often on a different live environment |
| One job spanning prep + install | Today: single media URL, single boot cycle |
| Product primary path | Image-write is great for golden/cloud images, **not** universal |

### 2.3 Product intent (agreed direction)

- **Primary:** customized / composed **installer ISOs** with family answer files:
  - Ubuntu: autoinstall / cloud-init (`user-data`)
  - VMware ESXi: kickstart
  - Flatcar: Ignition
  - Windows (later): unattend / Autounattend
- **Secondary:** **image-write** (Phase 7a style) when a golden raw/cloud image exists
- **Prep:** first-class **stage** before OS install when profile requires it
- Delivery: **Redfish Virtual Media** (+ one-time boot); no PXE required

---

## 3. Goals and non-goals

### 3.1 Goals

1. **Multi-stage job model** in Orchestrator (prep → os_install → verify).
2. **Media swap** between stages (eject stage N media, attach stage N+1, reboot).
3. **Install strategies:** `scripted_iso` (primary), `image_write` (secondary),
   `simulate` (regression).
4. **Family adapters** for answer-file composition and progress semantics.
5. **Profile-driven** inputs (extends Phase 5b) without hand-built flags for happy path.
6. Preserve SOL marker protocol; extend phases for prep/install namespaces.
7. Nested-lab demonstration path for at least one scripted family **or** document
   fidelity limits honestly (sushy single CD, remaster quirks).

### 3.2 Non-goals (this design)

- Implementing Windows in the first implementation slice
- PXE / DHCP as a required provisioning network
- Multi-tenant imaging SaaS
- OCR as install progress authority
- Replacing Phase 7a image-write (it remains a supported strategy)
- Full vendor RAID matrix on day one (plugin/stub architecture first)

---

## 4. Architecture

### 4.1 Stage pipeline

```text
                    ┌──────────────────────────────────────┐
  Start job ───────►│ Orchestrator (stage runner)          │
                    │  current_stage, stage_results[]      │
                    └───────────────┬──────────────────────┘
                                    │
         ┌──────────────────────────┼──────────────────────────┐
         ▼                          ▼                          ▼
   ┌───────────┐            ┌──────────────┐            ┌────────────┐
   │ Stage PREP│            │ Stage OS     │            │ Stage VERIFY│
   │ (optional)│──next─────►│ INSTALL      │──next─────►│ (optional)  │
   └─────┬─────┘            └──────┬───────┘            └──────┬─────┘
         │                         │                           │
         │ Virtual Media           │ Virtual Media             │ boot HDD
         │ prep.iso                │ family installer ISO      │ (no media
         │ SOL: PREP_*             │ or image-write marker ISO │  or empty)
         │ PREP_DONE ≠ job done    │ SOL: INSTALL_* / DONE     │ checks
         └─────────────────────────┴───────────────────────────┘
                                    │
                                    ▼
                         terminal: provisioned | failed
                         always: eject media, clear override
```

**Rules:**

- One **ProvisioningJob** owns the whole pipeline (not chained independent jobs).
- Job state stays **`PROVISIONING`** until final stage succeeds or any stage fails.
- **`PREP_DONE` advances stage**; only final OS (or verify) **`DONE`** → `provisioned`.
- Failure / cancel at any stage → **HandleTerminal** cleanup (all media, override).
- At most **one** Virtual Media image attached at a time (lab/sushy constraint);
  dual-media is optional when BMC reports ≥2 devices.

### 4.2 Install strategies

| Strategy | Media content | Disk effect | Use when |
|----------|---------------|-------------|----------|
| **`scripted_iso`** | Vendor/custom installer + embedded (or HTTP) answer file | Installer partitions/installs | **Primary** product path |
| **`image_write`** | Marker live + `payload.gz` on ISO | `gunzip\|dd` full disk image | Golden/cloud images; nested lab (7a) |
| **`simulate`** | Marker demo | None | Phase 2 regression |

### 4.3 Seed / answer delivery modes (scripted_iso)

Prefer offline BMC-only:

| Mode | Description | Default |
|------|-------------|---------|
| **`embed`** | Compose single ISO: installer + answer file + boot args | **Yes** |
| **`http`** | Boot stock/light ISO; `ks=` / nocloud-net / ignition URL on mgmt HTTP | Lab/fast optional |
| **`second_media`** | Installer CD + seed CD/USB | Only if BMC supports ≥2 VM devices |

Secrets in answer files: render at compose time from **secret backend** /
`credential_ref`; never log full rendered user-data/ks/ignition with passwords;
published lab ISOs may use documented lab-only passwords (same as 7a).

### 4.4 Prep stage capabilities

Prep runs a **Shoal maintenance live image** (evolution of marker ISO):

| Capability | v1 | Later |
|------------|----|--------|
| Secure wipe / blkdiscard / NVMe sanitize | Required stub + one real method | Vendor crypto erase |
| Emit `PREP_*` SOL markers | Required | — |
| RAID / HBA configure | Stub interface | storcli/MegaCLI/… plugins |
| Host-visible firmware tools | Optional | Vendor packs |
| Inventory / “ready to install” gate | Optional | Required for strict profiles |

**BMC firmware** that Redfish can do without booting the host should stay on
**Deploy → Redfish**, not forced into the live image. Prep live is for
**host-disk / adapter / in-band** work.

Profile flags (sketch):

```text
prep: skip | wipe_only | full
wipe_level: none | discard | pass | crypto
raid_profile_ref: optional
```

Destruct/wipe still requires Phase **5b** approval.

### 4.5 Component boundaries (unchanged)

```text
cmd/shoal
  → deploy.Orchestrator   # stage runner + lifecycle + cleanup
  → deploy/iso            # compose scripted ISO / marker image-write ISO
  → observe (SOL)         # via watchport + jobport only
  → common/redfish        # Virtual Media, boot, power
```

- Observe still **never** imports Deploy; progress via `jobport`.
- Core AI **not** on the install hot path.
- New code stays Go stdlib-first; no new deps without §7.1 + NOTICE.

---

## 5. Data model (sketch)

### 5.1 Job stages

Illustrative (implementation PR defines exact structs):

```go
type InstallStrategy string // simulate | image_write | scripted_iso

type JobStageKind string // prep | os_install | verify

type JobStageSpec struct {
    ID        string
    Kind      JobStageKind
    Strategy  InstallStrategy // os_install only
    Family    string          // ubuntu | flatcar | esxi | windows | ""
    MediaURL  string          // resolved before stage start; may be built
    AnswerRef string          // profile/template ref (non-secret)
    Timeout   time.Duration
    // Prep-only:
    WipeLevel string
}

// On ProvisioningJob (or side table):
// Stages []JobStageSpec
// CurrentStage int
// StageResults []StageResult
```

API/CLI may keep a **compat path**: single `-iso-url` ⇒ one-stage `os_install`
(image_write or scripted) for 7a-style runs.

### 5.2 Profile extensions (Phase 5b+)

```text
os_family: ubuntu | flatcar | esxi | windows
os_version: string
install_strategy: scripted_iso | image_write
answer_template_ref: path or id
base_iso_ref / base_image_ref: artifact refs
seed_mode: embed | http | second_media
prep: skip | wipe_only | full
hostname, install_disk, …
credential_ref: for passwords/keys used at compose time
```

### 5.3 SOL marker phases

Extend existing protocol (same `SHOAL|1|seq|ts|phase|percent|state|detail`):

**Prep:** `PREP_BOOT`, `PREP_WIPE`, `PREP_RAID`, `PREP_FIRMWARE`, `PREP_DONE`, `ERROR`  
**Install:** keep `DISK_PREP`, `IMAGE_WRITE`, `POSTINSTALL`, `VERIFY`, `DONE`  
  and/or family aliases `INSTALL_BOOT`, `INSTALL_COPY` mapped by parser to the same progress fields  
**Heartbeats:** existing `HEARTBEAT` / percent `-`

Orchestrator:

| Marker | Effect |
|--------|--------|
| Progress-only | Update phase/percent/seq |
| `PREP_DONE` | Complete prep stage; start next stage (media swap) |
| `DONE` (os stage) | If more stages → verify; else terminal success |
| `ERROR` | Terminal failure |

---

## 6. OS support matrix

| Family | Strategy | Answer file | Compose notes | Progress notes | Lab fidelity |
|--------|----------|-------------|-----------------|----------------|--------------|
| **Ubuntu** | `scripted_iso` primary; `image_write` supported (7a) | autoinstall `user-data` / NoCloud | Embed seed; preserve hybrid/UEFI boot (`xorriso` replay) | late-commands / marker inject preferred | Nested: image-write proven; autoinstall remaster stretch |
| **Flatcar** | `scripted_iso` | Ignition | Embed or `ignition.config.url` (http mode) | Limited serial; may need coarse progress + verify | TBD |
| **VMware ESXi** | `scripted_iso` | kickstart `ks.cfg` + boot `ks=cdrom:…` | Inject ks; do not assume dd image | %post markers if possible; else timeouts + verify | Needs real-ish media; sushy may be insufficient |
| **Windows** | `scripted_iso` (later) | `unattend.xml` | Driver injection hard; licensing out of band | SOL often poor → post-boot agent / Redfish checks | Later phase |

**Image-write column:** any family that publishes a **supported raw/cloud disk image** may use 7a mechanics; that is **not** a substitute for ESXi/Windows product install.

---

## 7. Orchestrator stage runner (behavior)

Pseudo-algorithm:

```text
func RunJob(job):
  for i, stage in job.Stages:
    job.CurrentStage = i
    resolveOrBuildMedia(stage)
    attachVirtualMedia(stage.MediaURL)   // skip if verify boots disk only
    setBootOverride(stage)               // Once CD for prep/os; disk for verify
    power ForceRestart or On
    register SOL watch
    wait until stage terminal marker or timeout/stall/cancel
    unregister SOL
    eject media; clear override (best-effort each stage end)
    if failed: HandleTerminal(failed); return
  HandleTerminal(provisioned)
```

**Idempotency:** re-entering a stage must re-read BMC media/boot state (existing Deploy rule).

**Timeouts:** per-stage; prep shorter than full OS install; ESXi/Windows longer than Ubuntu image-write.

---

## 8. ISO / artifact pipeline

### 8.1 Interfaces (sketch)

```text
type MediaComposer interface {
    Compose(ctx, ComposeInput) (Artifact, error)
}

// ComposeInput: Strategy, Family, BaseISO/BaseImage, Answer rendered bytes,
// Hostname, SeedMode, OutDir, …
```

Implementations:

- `MarkerImageWriteComposer` — existing `build-marker-iso.sh` + prepare scripts  
- `UbuntuAutoinstallComposer` — remaster / inject (evolve 7a remaster scripts)  
- `ESXiKickstartComposer` — later  
- `FlatcarIgnitionComposer` — later  

Publish remains **plain HTTP** on mgmt segment (`SHOAL_ISO_*`) for MVP.

### 8.2 Caching

Virtual Media clients (sushy) may **cache by URL**. Composed artifacts should use
**content-addressed or versioned basenames** when inputs change (lesson from 7a lab).

---

## 9. Security

- Answer files: no plaintext production passwords in git or slog  
- Lab defaults documented (`shoal-lab`) only  
- Wipe/RAID: 5b approval  
- Published ISOs treated as sensitive if they embed credentials  
- Redact compose logs  

---

## 10. Phased implementation plan

Suggested slices (separate PRs after this design is accepted):

| Slice | Deliverable | AC |
|-------|-------------|-----|
| **M0** | This design merged; design SoT pointer from main plan | Docs only |
| **M1** | Stage runner skeleton: multi-stage job with **one** stage (compat with 7a) | Existing 7a still green |
| **M2** | Prep stage v1: wipe + `PREP_*` markers + handoff to existing image-write Ubuntu | Nested lab E2E prep→install |
| **M3** | Ubuntu `scripted_iso` embed path (best-effort nested; hardware preferred) | Documented AC |
| **M4** | Profile fields + Start without hand ISO flags for one family | 5b-style |
| **M5** | Flatcar **or** ESXi first scripted family | Lab or hardware AC |
| **M6** | Windows spike design + optional prototype | Explicit fidelity note |

Do **not** block M1–M2 on full OS matrix.

---

## 11. Relationship to Phase 7

| Item | Disposition |
|------|-------------|
| 7a Ubuntu nested image-write | **Complete** (v2.0.9); remains `image_write` strategy |
| 7b profiles | **Superseded** by §5.2 + M4 |
| 7c second family | **Superseded** by §6 + M5 |
| Live autoinstall remaster | Alternate Ubuntu path under `scripted_iso` |

Update main design doc status line when M0 merges (pointer to this file).

---

## 12. Open questions

1. **Always run prep?** Default `prep: skip` for 7a-compat; `wipe_only` for reimage profiles.  
2. **Verify stage:** marker from guest agent vs SSH vs serial login scrape?  
3. **HTTP seed vs embed** for lab CI speed — allow both; production prefer embed.  
4. **Windows SOL** — accept non-marker progress for that family?  
5. **Single composer binary vs shell scripts** — keep scripts for ISO (5c/7a pattern) unless complexity forces Go.  

Resolve in implementation PRs; record decisions here.

---

## 13. Success metric

An operator can:

1. Select a profile (family + prep policy + strategy),  
2. Run one Deploy job over BMC,  
3. See SOL/stage progress,  
4. End in **`provisioned`** with media cleaned,  

for **Ubuntu** (image-write and/or scripted) and at least **one** of Flatcar/ESXi,
with Windows on a clear later slice — without PXE.

---

## 14. References

- Design SoT § Phase 7 (v2.0.9 7a closeout)  
- [`docs/phase-7-plan.md`](./phase-7-plan.md)  
- [`docs/lab-runbook.md`](./lab-runbook.md) § Phase 7a  
- Golden Rules in `AGENTS.md` §1  
