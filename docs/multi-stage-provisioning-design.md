# Multi-stage provisioning orchestrator & OS support matrix

**Status:** Design draft (implementation not started)  
**Date:** July 2026  
**Audience:** Human architect + coding agents  
**Related:** Design SoT `SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md` (v2.0.9+);
Phase **7a complete** (Ubuntu nested-lab cloud image-write). This document **supersedes**
deferred Phase **7b/7c** checklists for product direction.

**Constraint (hard):** Provisioning assumes **no guest transit / data-plane network** during
install. Answer files and installers must be available via **BMC Virtual Media** and/or
**local disk** prepared by Shoal. **No HTTP(S) URLs for kickstart, user-data, Ignition, or
unattend** fetched by the guest. (Serving install *ISOs* to the BMC over the management
segment for Virtual Media insert remains in scope — that traffic is BMC↔Shoal, not the
guest OS pulling config.)

---

## 1. Purpose

Specify how Shoal should provision **real operating systems** on bare metal (and nested
lab guests) beyond the single-media Phase 7a image-write path:

1. **Optional prep stage** — live maintenance image (wipe, RAID, firmware-related host
   prep) with SOL progress.
2. **OS install stage** — attach an installer (or image-write) medium via Virtual Media;
   deliver per-family config **offline** (see §4.3).
3. **Optional verify stage** — boot installed OS; confirm health / identity.
4. **OS support matrix** — Ubuntu, Flatcar, VMware ESXi, later Windows.

This is a **Deploy Orchestrator** evolution, not a new microservice. Golden Rules still
apply (BMC-only control path, SOL primary progress, Orchestrator sole lifecycle writer,
secrets never to logs/LLM, mandatory media cleanup).

---

## 2. Problem statement

### 2.1 What 7a proved

- Nested lab: Virtual Media + SOL + marker producer → **bootable Ubuntu** → job
  **`provisioned`**.
- Preferred mechanism: **cloud image-write** (`gunzip|dd`), not live-server autoinstall
  remaster (unreliable under nested sushy).

### 2.2 What 7a does not solve

| Need | Gap |
|------|-----|
| ESXi / Flatcar / Windows | Often need an **installer + offline answer file**, not only dd |
| Wipe / RAID / pre-config | Must run **before** OS install, often on a different live environment |
| One job spanning prep + install | Today: single media URL, single boot cycle |
| Product primary path | Image-write is great for golden/cloud images, **not** universal |

### 2.3 Product intent (agreed direction)

| Intent | Detail |
|--------|--------|
| Offline only | No guest HTTP seed URLs (no transit assumption) |
| Prep | First-class optional stage before OS install |
| Image-write | Keep (7a); secondary strategy for golden/cloud disks |
| Ubuntu / Flatcar | Installer + offline seed (preference order §4.3) |
| VMware ESXi / Windows (v1) | **Same boat:** operator supplies a **ready-to-boot install ISO** (ks / Autounattend already on media); Shoal attaches and provisions — **no ISO remaster/compose in this design** |
| Later designs | Optional separate docs for **ISO composition** (embed ks into ESXi, remaster Windows, etc.) |

---

## 3. Goals and non-goals

### 3.1 Goals

1. **Multi-stage job model** in Orchestrator (prep → os_install → verify).
2. **Media swap** between stages (eject stage N media, attach stage N+1, reboot).
3. **Install strategies:** `scripted_iso` / `operator_iso`, `image_write`, `simulate`.
4. **Offline config delivery** for Ubuntu/Flatcar (§4.3) without guest network.
5. **Operator-provided ISO** path for ESXi (and likely Windows v1).
6. Preserve SOL marker protocol; extend phases for prep/install namespaces.
7. Profile-driven happy path where Shoal owns config (Ubuntu/Flatcar); simple URL attach
   where operator owns the ISO (ESXi).

### 3.2 Non-goals (this design)

- **HTTP/HTTPS seed URLs** for answer files (`ks=http://…`, nocloud-net, `ignition.config.url=http://…`, etc.)
- **ESXi ISO composition / kickstart injection** (deferred to a later design if wanted)
- Implementing Windows as a first implementation slice (design options only)
- PXE / DHCP as a required provisioning network
- Multi-tenant imaging SaaS
- OCR as install progress authority
- Replacing Phase 7a image-write
- Full vendor RAID matrix on day one

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
         │ prep.iso                │ installer / image-write   │
         │ SOL: PREP_*             │ + offline seed (§4.3)     │ checks
         │ PREP_DONE ≠ job done    │ SOL: INSTALL_* / DONE     │
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
- Virtual Media: prefer **one** device when that is all the BMC offers; **second device**
  is used for seed when available (§4.3.1).

### 4.2 Install strategies

| Strategy | Media / disk effect | Use when |
|----------|---------------------|----------|
| **`operator_iso`** | Attach operator-supplied bootable ISO as-is | **ESXi v1**; likely **Windows v1**; any pre-baked media |
| **`scripted_iso`** | Attach installer ISO **plus** Shoal-delivered offline seed (second media or config-drive) | **Ubuntu / Flatcar** when not using image-write |
| **`image_write`** | Marker live + `payload.gz` on ISO → `gunzip\|dd` | Golden/cloud images (7a Ubuntu) |
| **`simulate`** | Marker demo | Phase 2 regression |

Naming note: `operator_iso` makes “we don’t compose this ISO” explicit. Implementation may
fold it under a broader `scripted_iso` with `compose: false` — pick one in the impl PR.

### 4.3 Offline config delivery (no guest HTTP)

**Forbidden:** any install-time fetch of answer files over the guest network.

#### 4.3.0 Preference order (when Shoal owns the seed)

For families where Shoal renders and delivers config (Ubuntu, Flatcar; not operator-built
ESXi/Windows ISOs), prefer the **first mode the BMC/hardware allows**:

| Priority | Mode | When to use |
|----------|------|-------------|
| **1 (preferred)** | **Second Virtual Media + OS installer** | BMC has ≥2 virtual media (or CD+USB): boot installer on device A, seed ISO/FAT on device B |
| **2** | **Config-drive partition** on install disk | Only one Virtual Media device, or second_media unavailable; Ironic-style local partition/label |
| **3** | **Customized single deploy ISO** | Remaster/compose installer + answer into one ISO (highest complexity; last resort for Shoal-owned seeds) |

**Separate track (not ranked in 1–3):**

| Mode | Role |
|------|------|
| **`operator_iso`** | Operator already built a single ready ISO (ESXi + ks, Windows + Autounattend). Shoal does not compose. |
| **`image_write`** | Full disk image already contains first-boot config (7a cloud prepare). No separate seed device. |

Implementation picks seed mode from profile + BMC capability probe (list Virtual Media
count); never fall back to guest HTTP.

#### 4.3.1 Second Virtual Media (userdata volume) — preference #1

When the BMC exposes **≥2** virtual media devices (or CD + USB):

```text
Device A: installer ISO (Ubuntu live/server, Flatcar, …)  — boot target
Device B: small seed ISO/FAT image
          Ubuntu: NoCloud layout (user-data, meta-data), often label CIDATA
          Flatcar: Ignition config per family conventions
```

Orchestrator attaches both, sets one-time boot to the installer device, ejects both on
cleanup.

**Lab note:** sushy-tools often models **one** CD. Dual-media may require real BMC or
lab domain XML extensions — document fidelity gap; do not pretend nested sushy covers it
until proven.

#### 4.3.2 Config-drive partition (Ironic-style) — preference #2

OpenStack Ironic (and Nova) can present instance metadata as a **configuration drive**:

- Small image/partition presented to the instance (in Ironic bare metal, commonly as a
  **disk partition**, not only as a second CD).
- Filesystem is discovered by **label** (classically **`config-2`** for OpenStack
  config-drive).
- **cloud-init** (and similar) mount by label and apply `user_data` / network / meta
  **without any network** to a metadata service.

See: [Ironic configdrive](https://docs.openstack.org/ironic/latest/install/configdrive.html),
[cloud-init ConfigDrive datasource](https://docs.cloud-init.io/en/latest/reference/datasources/configdrive.html).

**Shoal adaptation (proposed):**

| Step | Actor | Action |
|------|--------|--------|
| 1 | Prep stage and/or Deploy host-side tooling | Build a small FAT/ISO9660 image (NoCloud or OpenStack layout) with rendered user-data / Ignition |
| 2 | Prep live **or** specialized write | Create a small partition at the **start or end of the install disk** (or write config-drive into a reserved area), label appropriately, copy seed files |
| 3 | OS install / first boot | Installer or first-boot agent reads local config-drive / NoCloud |

**Ubuntu:** cloud-init NoCloud + optional OpenStack datasource; autoinstall can be fed via
NoCloud seed (exact layout fixed in impl PR). Prefer labels cloud-init already searches
(`cidata` / `CIDATA`, `config-2`).

**Flatcar:** Ignition supports offline sources (e.g. from disk/OEM); exact path chosen in
impl (config-drive-like partition vs second media). Prefer **no network**.

**Ordering with wipe:** If prep **wipes** the disk, config-drive must be written
**after** wipe and **before** or as part of the OS stage that expects it. Image-write of a
full cloud image that already includes seed (7a prepare) is an alternative that avoids a
separate config partition.

**Risks:** Partition layout must not fight the installer (leave free space / use end of
disk / use a second disk if present). Impl PR must pin a tested layout per family.

#### 4.3.3 Customized single deploy ISO — preference #3

Remaster or inject answer files into one bootable installer ISO (Ubuntu autoinstall
remaster, future Flatcar embeds, etc.). **Valid but last among Shoal-owned seed modes**
because of hybrid/UEFI boot fragility, large rebuilds, and version skew. Prefer #1 or #2
when the BMC and family support them.

**Not** the v1 path for ESXi/Windows (those stay `operator_iso` until a separate compose
design exists).

#### 4.3.4 Explicitly rejected for this design

| Mode | Why rejected |
|------|----------------|
| `ks=http://…` / `ignition.config.url=http://…` / nocloud-net | Requires guest network (transit) |
| Metadata service (169.254.169.254) | Same; also not BMC-only story |
| “Download answer file during install” | Violates offline constraint |

### 4.4 Prep stage capabilities

Prep runs a **Shoal maintenance live image** (evolution of marker ISO):

| Capability | v1 | Later |
|------------|----|--------|
| Secure wipe / blkdiscard / NVMe sanitize | Required stub + one real method | Vendor crypto erase |
| Emit `PREP_*` SOL markers | Required | — |
| Write config-drive partition (§4.3.2) | Optional v1 if second media unavailable | Preferred offline seed path |
| RAID / HBA configure | Stub interface | storcli/MegaCLI/… plugins |
| Host-visible firmware tools | Optional | Vendor packs |

**BMC firmware** that Redfish can perform without booting the host stays on
**Deploy → Redfish**. Prep live is for **host-disk / adapter / in-band** work.

Profile flags (sketch):

```text
prep: skip | wipe_only | full
wipe_level: none | discard | pass | crypto
raid_profile_ref: optional
seed_delivery: auto | second_media | config_drive | single_iso | none
# auto = prefer second_media, else config_drive, else single_iso (see §4.3.0)
# none = operator_iso or image_write (config already on media/image)
```

Destruct/wipe still requires Phase **5b** approval.

### 4.5 Component boundaries (unchanged)

```text
cmd/shoal
  → deploy.Orchestrator   # stage runner + lifecycle + cleanup
  → deploy/iso            # marker image-write; small seed ISO build; NOT ESXi compose in v1
  → observe (SOL)         # via watchport + jobport only
  → common/redfish        # Virtual Media (1–2 devices), boot, power
```

- Observe still **never** imports Deploy; progress via `jobport`.
- Core AI **not** on the install hot path.
- New code stays Go stdlib-first; no new deps without §7.1 + NOTICE.

---

## 5. Data model (sketch)

### 5.1 Job stages

```go
type InstallStrategy string // simulate | image_write | scripted_iso | operator_iso

type SeedDelivery string // none | second_media | config_drive

type JobStageKind string // prep | os_install | verify

type JobStageSpec struct {
    ID           string
    Kind         JobStageKind
    Strategy     InstallStrategy
    Family       string // ubuntu | flatcar | esxi | windows | ""
    MediaURL     string // installer or marker ISO (BMC-reachable)
    SeedMediaURL string // optional second VM URL
    AnswerRef    string // template for seed render (ubuntu/flatcar); unused for operator_iso
    SeedDelivery SeedDelivery
    Timeout      time.Duration
    WipeLevel    string // prep
}
```

**Compat path:** single `-iso-url` ⇒ one-stage job (`image_write` or `operator_iso` /
`scripted_iso` as selected).

### 5.2 Profile extensions (Phase 5b+)

```text
os_family: ubuntu | flatcar | esxi | windows
os_version: string
install_strategy: operator_iso | scripted_iso | image_write
media_url / media_ref: operator or published installer / marker ISO
seed_delivery: auto | second_media | config_drive | single_iso | none
# auto implements §4.3.0 preference order when strategy is scripted_iso
answer_template_ref: ubuntu/flatcar only
prep: skip | wipe_only | full
hostname, install_disk, …
credential_ref: secrets for seed render (not for operator_iso contents Shoal did not build)
```

### 5.3 SOL marker phases

**Prep:** `PREP_BOOT`, `PREP_WIPE`, `PREP_RAID`, `PREP_FIRMWARE`, `PREP_SEED` (config-drive
write), `PREP_DONE`, `ERROR`  

**Install:** `DISK_PREP`, `IMAGE_WRITE` / `INSTALL_*`, `POSTINSTALL`, `VERIFY`, `DONE`  

**Heartbeats:** existing rules.

| Marker | Effect |
|--------|--------|
| Progress-only | Update phase/percent/seq |
| `PREP_DONE` | Complete prep; start next stage |
| `DONE` (os stage) | Next stage or terminal success |
| `ERROR` | Terminal failure |

---

## 6. OS support matrix

### 6.1 Summary table

| Family | v1 strategy | Config delivery | Who builds the install ISO? |
|--------|-------------|-----------------|------------------------------|
| **Ubuntu** | `image_write` (done) and/or `scripted_iso` | §4.3.0: second_media → config_drive → single_iso; or baked in image (7a) | Shoal seed; stock/light installer |
| **Flatcar** | `scripted_iso` (or image if applicable) | §4.3.0 same order (Ignition offline) | Stock media + Shoal seed |
| **VMware ESXi** | **`operator_iso` (v1)** | Inside operator ISO | **Operator** |
| **Windows** | **`operator_iso` (v1)** — same boat as ESXi | Inside operator ISO | **Operator** |

### 6.2 Ubuntu

| Path | Notes |
|------|--------|
| **image_write (7a)** | Prepare cloud image offline (hostname, user, NoCloud seed on image) → marker ISO → dd. **No guest network.** |
| **scripted_iso** | Boot installer ISO; seed via §4.3.0 order (second_media → config_drive → single_iso). |

### 6.3 Flatcar

- Offline Ignition only; same §4.3.0 preference order as Ubuntu.
- No `ignition.config.url=http://…`.
- Progress may be coarse; verify stage important.

### 6.4 VMware ESXi and Windows (same v1 boat)

Both are **`operator_iso`** for the first product slice: operator builds the ready
install ISO; Shoal attaches, boots, waits, cleans up.

| | ESXi | Windows |
|--|------|---------|
| **Who builds media** | Operator (ks already on ISO if unattended) | Operator (`Autounattend.xml` already on ISO if unattended) |
| **Shoal does** | Attach → boot CD → wait → cleanup | Same |
| **Shoal does not (v1)** | Remaster / inject kickstart | Remaster / inject unattend + drivers |
| **Progress** | Coarse (timeouts / power; markers if lucky) | Coarse (SOL usually poor) |
| **Later** | Optional compose design | Optional compose or dual-media unattend |

**Why same boat:** neither treats Ironic/cloud-init config-drive as the natural *setup*
path the way Ubuntu/Flatcar do. Offline unattended config is expected **on the media the
operator already built** until a dedicated compose design exists.

**Windows-only nuances (not ESXi):**

- Setup looks for `Autounattend.xml` on **install/removable media**, not `config-2`
  mid-setup — so §4.3 preference **#2 (config_drive)** is a poor fit for Windows *setup*.
- Dual Virtual Media (installer + small unattend ISO) is closer to preference **#1** and
  may be a **later** Windows improvement when the BMC has two devices — not required for
  v1 if unattend is baked into the ISO.
- Golden **image_write** remains valid if the org already ships sysprep’d images.
- Licensing / OEM drivers stay out of band.

**In scope v1:** `media_url` → attach → boot → cleanup → optional verify.  
**Out of scope v1:** becoming an ESXi or Windows media factory.

---

## 7. Orchestrator stage runner (behavior)

```text
func RunJob(job):
  for i, stage in job.Stages:
    job.CurrentStage = i
    if stage needs seed and seed_delivery == config_drive:
      ensureConfigDriveOnDisk(stage)   // via prep live or host-side when safe
    attachVirtualMedia(stage.MediaURL)
    if stage.SeedMediaURL != "" && seed_delivery == second_media:
      attachSecondVirtualMedia(stage.SeedMediaURL)
    setBootOverride(stage)
    power ForceRestart or On
    register SOL watch (family policy: markers required vs best-effort)
    wait until stage terminal or timeout/stall/cancel
    unregister SOL
    eject all media; clear override
    if failed: HandleTerminal(failed); return
  HandleTerminal(provisioned)
```

**Idempotency:** re-read BMC media/boot state each stage (existing Deploy rule).

**Timeouts:** per-stage; OS install longer than prep; Windows/ESXi longer than Ubuntu
image-write.

---

## 8. Artifact pipeline

### 8.1 What Shoal builds in *this* design

| Artifact | Builder |
|----------|---------|
| Marker image-write ISO | Existing `build-marker-iso.sh` + prepare scripts (7a) |
| Small **seed** ISO/FAT (CIDATA / Ignition) | New small builder — **not** full OS remaster |
| Config-drive filesystem image | New helper used by prep or Deploy |
| Prep live ISO | Evolve marker ISO |

### 8.2 What Shoal does **not** build in this design

| Artifact | Owner |
|----------|--------|
| ESXi ISO + embedded kickstart | **Operator** (later design optional) |
| Windows ISO + Autounattend + drivers | **Operator** (later design optional) |

### 8.3 Serving media to the BMC

Plain HTTP on the **management segment** for Virtual Media **file fetch by the BMC**
(`SHOAL_ISO_BASE_URL`) remains. That is **not** “guest HTTP seed.”

Version media URLs when content changes (sushy cache lesson from 7a).

---

## 9. Security

- Seed templates: no production passwords in git/slog; render via `credential_ref`
- Operator-supplied ISOs are trusted inputs — treat as high sensitivity
- Wipe/RAID: 5b approval
- Redact compose logs for seed render

---

## 10. Phased implementation plan

| Slice | Deliverable | AC |
|-------|-------------|-----|
| **M0** | This design merged | Docs only |
| **M1** | Stage runner skeleton; single-stage compat with 7a image-write | 7a still green |
| **M2** | Prep v1: wipe + `PREP_*` + handoff to image-write Ubuntu | Nested E2E |
| **M3** | Offline seed preference #1 then #2: **second_media**, else **config_drive**, for Ubuntu NoCloud | Lab or hardware AC; no HTTP seed |
| **M4** | Flatcar offline Ignition (same preference order) | Documented AC |
| **M5** | **`operator_iso`** path shared by ESXi + Windows shape (attach + boot + cleanup + coarse progress) | Hardware preferred |
| **M6** | Profiles + `seed_delivery: auto` implementing §4.3.0 | Happy path without ad-hoc flags |
| **Later** | Separate designs: ESXi/Windows ISO compose; optional Windows dual-media unattend; single_iso remaster polish | Out of this doc’s v1 slices |

---

## 11. Relationship to Phase 7

| Item | Disposition |
|------|-------------|
| 7a Ubuntu nested image-write | **Complete**; `image_write` strategy |
| 7b profiles | **Superseded** by §5.2 + M6 |
| 7c second family | **Superseded** by matrix + M4/M5 |
| HTTP autoinstall seed | **Rejected** under offline constraint |

---

## 12. Open questions

1. **Always run prep?** Default `prep: skip` for 7a-compat; `wipe_only` for reimage.  
2. **Verify stage:** serial scrape vs future guest agent vs Redfish-only power/boot?  
3. **Config-drive layout:** exact partition size/offset/label per family (pin in M3).  
4. **ESXi/Windows progress without SOL markers:** max timeout + power state only for M5?  
5. **When to invest in single_iso compose** for Ubuntu if #1 and #2 both fail in lab?

---

## 13. Success metric

An operator can run **one Deploy job** over BMC (no guest transit network) that:

1. Optionally preps (wipe/RAID),  
2. Installs **Ubuntu** (image-write and/or offline-seeded scripted path) and **Flatcar**
   (offline seed),  
3. Can **attach and run** an **operator-built ESXi ISO**,  
4. Has a clear **Windows** path (at least operator_iso) without HTTP seeds,  
5. Ends **`provisioned`** with media cleaned.

---

## 14. References

- Design SoT § Phase 7 (v2.0.9 7a closeout)  
- [`docs/phase-7-plan.md`](./phase-7-plan.md)  
- [`docs/lab-runbook.md`](./lab-runbook.md) § Phase 7a  
- [Ironic configuration drive](https://docs.openstack.org/ironic/latest/install/configdrive.html)  
- [cloud-init ConfigDrive datasource](https://docs.cloud-init.io/en/latest/reference/datasources/configdrive.html)  
- Golden Rules in `AGENTS.md` §1  
