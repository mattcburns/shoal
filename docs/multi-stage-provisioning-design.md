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
   where operator owns the ISO (ESXi/Windows).
8. **Operator API** (§6): extend `POST/GET /v1/jobs` and `shoal deploy *` — profile-first,
   explicit media overrides, staged job status; no separate control plane.

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
| **`operator_iso`** | Attach operator-supplied bootable ISO as-is | **ESXi + Windows v1**; any pre-baked media |
| **`scripted_iso`** | Attach installer ISO **plus** Shoal-delivered offline seed (§4.3.0 order) | **Ubuntu / Flatcar** when not using image-write |
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

type SeedDelivery string // auto | none | second_media | config_drive | single_iso

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
`scripted_iso` as selected). Operators do **not** hand-build a stage array in the happy
path; the Orchestrator expands profile + request into stages (§6).

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

## 6. Operator API (CLI + HTTP)

This section is **normative for the product surface**. Implementation extends existing
`POST /v1/jobs` / `shoal deploy run` rather than inventing a second control plane.

### 6.1 Surfaces

| Surface | Endpoints / commands | Notes |
|---------|----------------------|--------|
| **HTTP** | `POST /v1/jobs`, `GET /v1/jobs/{id}`, `POST /v1/jobs/{id}/cancel` | Same routes as today; request/response fields grow |
| **CLI** | `shoal deploy run \| status \| cancel` | Flags mirror JSON fields |
| **Profiles** | `shoal profile …` + `SHOAL_PROFILE_DIR` | Happy path: install intent lives here |
| **Auth** | `Authorization: Bearer $SHOAL_API_TOKEN` when token set | Unchanged (Phase 6d) |

**Not user-facing:** Redfish attach/eject, stage scheduling, SOL session ownership
(Orchestrator + Observe internals).

### 6.2 Mental model

```text
1. Publish or host install media on the management segment (BMC-reachable URL)
2. Save profile (and approve if wipe/destruct)
3. POST /v1/jobs  { device_id, profile_ref, bmc_*, credentials, … }
4. Poll GET /v1/jobs/{id} until state provisioned | failed
5. On failure: media already cleaned; fix inputs; retry
```

| Intent | What the operator passes |
|--------|---------------------------|
| Pre-built ESXi / Windows ISO | `install_strategy=operator_iso` + `iso_url` (or profile) |
| Ubuntu cloud image-write (7a) | `image_write` media / profile |
| Ubuntu / Flatcar installer + seed | `scripted_iso` + `seed_delivery=auto` (or explicit) |
| Wipe then install | `prep=wipe_only` (or `full`) + `approve_destruct` |

Stages are **derived** from profile + request. Operators do not submit a raw stage DAG
in the happy path.

### 6.3 Profile-driven happy path

**Example profile** (`scripted_iso` + prep):

```json
{
  "ref": "lab-ubuntu-wipe-install",
  "os_family": "ubuntu",
  "os_version": "22.04",
  "hostname": "node-a",
  "install_strategy": "scripted_iso",
  "seed_delivery": "auto",
  "prep": "wipe_only",
  "wipe_level": "discard",
  "media_ref": "ubuntu-22.04-live-server",
  "answer_template_ref": "ubuntu-autoinstall-default",
  "install_disk": "/dev/sda",
  "needs_approval": true,
  "destruct_steps": ["secure_wipe"]
}
```

**Example profile** (`operator_iso` — ESXi / Windows):

```json
{
  "ref": "esxi-fleet-ready",
  "os_family": "esxi",
  "install_strategy": "operator_iso",
  "seed_delivery": "none",
  "prep": "skip",
  "media_url": "http://192.168.124.1:8080/esxi-8-with-ks.iso"
}
```

**HTTP — start job from profile:**

```http
POST /v1/jobs
Content-Type: application/json

{
  "device_id": "rack1-u12",
  "profile_ref": "lab-ubuntu-wipe-install",
  "bmc_endpoint": "https://bmc.example/redfish/v1",
  "credential_ref": "secret/bmc/rack1-u12",
  "serial_target": "redfish_sol",
  "approve_destruct": true,
  "stall_timeout": "15m"
}
```

**CLI:**

```bash
shoal deploy run \
  -device-id rack1-u12 \
  -profile-ref lab-ubuntu-wipe-install \
  -bmc-url https://bmc.example/redfish/v1 \
  -credential-ref secret/bmc/rack1-u12 \
  -approve-destruct \
  -wait
```

Response (accepted job; same shape as today, plus stage fields when implemented):

```json
{
  "id": "abc…",
  "device_id": "rack1-u12",
  "profile_ref": "lab-ubuntu-wipe-install",
  "state": "provisioning",
  "phase": "WAITING_SOL",
  "current_stage": "prep",
  "stages": [ … ],
  "started_at": "…",
  "updated_at": "…"
}
```

### 6.4 Explicit / one-off start (compat + power users)

Extends today’s spike flags: pass media URLs without a full profile. Used for lab 7a,
operator ISOs, and debugging.

**CLI examples:**

```bash
# 7a-style image-write
shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -serial-target shoal-node-1 \
  -iso-url http://192.168.124.1:8080/shoal-ubuntu-cloud-v5.iso \
  -wait

# Operator ISO (ESXi / Windows)
shoal deploy run \
  -device-id host-7 \
  -bmc-url https://bmc/redfish/v1 \
  -install-strategy operator_iso \
  -iso-url http://mgmt:8080/esxi-custom.iso \
  -wait

# Scripted Ubuntu with explicit second-media seed
shoal deploy run \
  -device-id host-7 \
  -install-strategy scripted_iso \
  -os-family ubuntu \
  -seed-delivery second_media \
  -iso-url http://mgmt:8080/ubuntu-live.iso \
  -seed-url http://mgmt:8080/cidata-host7.iso \
  -wait
```

**HTTP — extended `StartJobRequest` (illustrative; field names finalised in impl PR):**

```json
{
  "device_id": "host-7",
  "bmc_endpoint": "https://bmc/redfish/v1",
  "credential_ref": "secret/bmc/host-7",
  "serial_target": "redfish_sol",
  "install_strategy": "operator_iso",
  "iso_url": "http://mgmt:8080/esxi-custom.iso",
  "seed_delivery": "none",
  "stall_timeout": "45m"
}
```

| Field | Meaning |
|-------|---------|
| `device_id` | Target identity (required) |
| `profile_ref` | Load install/prep/seed defaults from profile store |
| `bmc_endpoint` | Redfish base URL |
| `bmc_username` / `bmc_password` | Lab/spike only; prefer `credential_ref` |
| `credential_ref` | Opaque BMC (and seed-render) secret handle |
| `serial_target` | Libvirt domain or SOL target for Observe |
| `iso_url` / `media_url` | Installer or marker ISO (**BMC-reachable**; mgmt HTTP OK) |
| `seed_url` | Second Virtual Media seed ISO URL (when `seed_delivery=second_media`) |
| `install_strategy` | `operator_iso` \| `scripted_iso` \| `image_write` \| `simulate` |
| `seed_delivery` | `auto` \| `second_media` \| `config_drive` \| `single_iso` \| `none` |
| `os_family` | `ubuntu` \| `flatcar` \| `esxi` \| `windows` |
| `prep` | `skip` \| `wipe_only` \| `full` (overrides profile) |
| `approve_destruct` | Required when prep wipe / profile `needs_approval` |
| `stall_timeout` | SOL silence window (family defaults may raise) |
| `build_iso` | Existing 6a/7a dynamic build (image-write / marker) |

**Forbidden in request fields:** guest-facing HTTP URLs for kickstart, user-data,
Ignition, or unattend (`ks=http://…`, nocloud-net, etc.). Reject at validate time.

**Optional advanced escape hatch** (not required for M1; product docs lead with profile):

```json
{
  "device_id": "…",
  "bmc_endpoint": "…",
  "credential_ref": "…",
  "serial_target": "…",
  "stages": [
    { "kind": "prep", "wipe_level": "discard" },
    {
      "kind": "os_install",
      "strategy": "scripted_iso",
      "family": "ubuntu",
      "media_url": "http://mgmt:8080/ubuntu.iso",
      "seed_delivery": "config_drive"
    }
  ]
}
```

If both `profile_ref` and `stages` are set, implementation must define precedence
(recommend: explicit `stages` wins, else expand profile).

### 6.5 Profile → stage expansion (orchestrator, not user)

| Profile / request | Stages executed |
|-------------------|-----------------|
| `prep: skip`, `image_write` | `[os_install]` (7a shape) |
| `prep: wipe_only`, `scripted_iso`, `seed_delivery: auto` | `[prep, os_install]` (+ optional verify) |
| `operator_iso` (ESXi/Windows) | `[os_install]` attach given ISO |
| `prep: skip`, `scripted_iso`, seed `second_media` | `[os_install]` with two Virtual Media attaches |

### 6.6 Job status API

```http
GET /v1/jobs/{id}
```

```json
{
  "id": "abc…",
  "device_id": "rack1-u12",
  "profile_ref": "lab-ubuntu-wipe-install",
  "state": "provisioning",
  "phase": "PREP_WIPE",
  "percent": 40,
  "current_stage": "prep",
  "stages": [
    {
      "id": "prep",
      "kind": "prep",
      "state": "running",
      "phase": "PREP_WIPE",
      "percent": 40
    },
    {
      "id": "os_install",
      "kind": "os_install",
      "strategy": "scripted_iso",
      "family": "ubuntu",
      "seed_delivery": "second_media",
      "state": "pending"
    },
    {
      "id": "verify",
      "kind": "verify",
      "state": "pending"
    }
  ],
  "last_marker_seq": 12,
  "iso_url": "http://…/ubuntu-live.iso",
  "error": null,
  "updated_at": "…"
}
```

**Terminal success:**

```json
{
  "state": "provisioned",
  "phase": "DONE",
  "percent": 100,
  "current_stage": "verify"
}
```

**Terminal failure:**

```json
{
  "state": "failed",
  "phase": "ERROR",
  "error": "prep wipe failed: …",
  "current_stage": "prep"
}
```

**CLI:**

```bash
shoal deploy status -job-id abc…
shoal deploy run … -wait    # block until provisioned|failed
```

Coarse lifecycle states remain **`provisioning` | `provisioned` | `failed`** (and
existing cancel path). `phase` / `current_stage` / `stages[]` carry multi-stage detail.

### 6.7 Cancel

```http
POST /v1/jobs/{id}/cancel
```

```bash
shoal deploy cancel -job-id abc…
```

At **any** stage: unregister SOL, eject **all** Virtual Media (installer + seed), clear
boot override, transition job to failed/canceled. Same mandatory cleanup as today.

### 6.8 What is not on the user API

- Guest HTTP answer-file URLs  
- Direct Redfish Virtual Media / boot override calls (Deploy owns them)  
- Per-marker lifecycle commits (Observe proposes progress via `jobport`; Orchestrator
  alone writes terminal state)  
- Requiring operators to list SOL marker phases

### 6.9 API acceptance criteria (implementation)

1. Existing single-ISO 7a / Phase 2 jobs remain expressible with `iso_url` only.  
2. Profile-only start works for at least one Ubuntu path without per-request stage lists.  
3. `operator_iso` start accepts `iso_url` + family and does not require seed fields.  
4. `GET /v1/jobs/{id}` exposes `current_stage` and per-stage state once multi-stage lands.  
5. Validation rejects guest HTTP seed patterns in request/profile fields.  
6. Cancel always triggers full media cleanup.

---

## 7. OS support matrix

### 7.1 Summary table

| Family | v1 strategy | Config delivery | Who builds the install ISO? |
|--------|-------------|-----------------|------------------------------|
| **Ubuntu** | `image_write` (done) and/or `scripted_iso` | §4.3.0: second_media → config_drive → single_iso; or baked in image (7a) | Shoal seed; stock/light installer |
| **Flatcar** | `scripted_iso` (or image if applicable) | §4.3.0 same order (Ignition offline) | Stock media + Shoal seed |
| **VMware ESXi** | **`operator_iso` (v1)** | Inside operator ISO | **Operator** |
| **Windows** | **`operator_iso` (v1)** — same boat as ESXi | Inside operator ISO | **Operator** |

### 7.2 Ubuntu

| Path | Notes |
|------|--------|
| **image_write (7a)** | Prepare cloud image offline (hostname, user, NoCloud seed on image) → marker ISO → dd. **No guest network.** |
| **scripted_iso** | Boot installer ISO; seed via §4.3.0 order (second_media → config_drive → single_iso). |

### 7.3 Flatcar

- Offline Ignition only; same §4.3.0 preference order as Ubuntu.
- No `ignition.config.url=http://…`.
- Progress may be coarse; verify stage important.

### 7.4 VMware ESXi and Windows (same v1 boat)

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

## 8. Orchestrator stage runner (behavior)

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

## 9. Artifact pipeline

### 9.1 What Shoal builds in *this* design

| Artifact | Builder |
|----------|---------|
| Marker image-write ISO | Existing `build-marker-iso.sh` + prepare scripts (7a) |
| Small **seed** ISO/FAT (CIDATA / Ignition) | New small builder — **not** full OS remaster |
| Config-drive filesystem image | New helper used by prep or Deploy |
| Prep live ISO | Evolve marker ISO |

### 9.2 What Shoal does **not** build in this design

| Artifact | Owner |
|----------|--------|
| ESXi ISO + embedded kickstart | **Operator** (later design optional) |
| Windows ISO + Autounattend + drivers | **Operator** (later design optional) |

### 9.3 Serving media to the BMC

Plain HTTP on the **management segment** for Virtual Media **file fetch by the BMC**
(`SHOAL_ISO_BASE_URL`) remains. That is **not** “guest HTTP seed.”

Version media URLs when content changes (sushy cache lesson from 7a).

---

## 10. Security

- Seed templates: no production passwords in git/slog; render via `credential_ref`
- Operator-supplied ISOs are trusted inputs — treat as high sensitivity
- Wipe/RAID: 5b approval
- Redact compose logs for seed render

---

## 11. Phased implementation plan

| Slice | Deliverable | AC |
|-------|-------------|-----|
| **M0** | This design merged | Docs only |
| **M1** | Stage runner skeleton; single-stage compat with 7a image-write | **Implemented** (single `os_install` stage; job fields + API) |
| **M2** | Prep v1: wipe + `PREP_*` + handoff to image-write Ubuntu | **Implemented** (event-driven `PREP_DONE` → os_install) |
| **M3** | Offline seed preference #1 then #2: **second_media**, else **config_drive**, for Ubuntu NoCloud | Lab or hardware AC; no HTTP seed |
| **M4** | Flatcar offline Ignition (same preference order) | Documented AC |
| **M5** | **`operator_iso`** path shared by ESXi + Windows shape (attach + boot + cleanup + coarse progress) | Hardware preferred |
| **M6** | Profiles + `seed_delivery: auto` + Operator API §6 fields on `POST/GET /v1/jobs` | Happy path without ad-hoc flags; §6.9 ACs |
| **Later** | Separate designs: ESXi/Windows ISO compose; optional Windows dual-media unattend; single_iso remaster polish | Out of this doc’s v1 slices |

---

## 12. Relationship to Phase 7

| Item | Disposition |
|------|-------------|
| 7a Ubuntu nested image-write | **Complete**; `image_write` strategy |
| 7b profiles | **Superseded** by §5.2 + M6 |
| 7c second family | **Superseded** by matrix + M4/M5 |
| HTTP autoinstall seed | **Rejected** under offline constraint |

---

## 13. Open questions

1. **Always run prep?** Default `prep: skip` for 7a-compat; `wipe_only` for reimage.  
2. **Verify stage:** serial scrape vs future guest agent vs Redfish-only power/boot?  
3. **Config-drive layout:** exact partition size/offset/label per family (pin in M3).  
4. **ESXi/Windows progress without SOL markers:** max timeout + power state only for M5?  
5. **When to invest in single_iso compose** for Ubuntu if #1 and #2 both fail in lab?

---

## 14. Success metric

An operator can run **one Deploy job** over BMC (no guest transit network) that:

1. Optionally preps (wipe/RAID),  
2. Installs **Ubuntu** (image-write and/or offline-seeded scripted path) and **Flatcar**
   (offline seed),  
3. Can **attach and run** an **operator-built ESXi ISO**,  
4. Has a clear **Windows** path (at least operator_iso) without HTTP seeds,  
5. Ends **`provisioned`** with media cleaned.

---

## 15. References

- Design SoT § Phase 7 (v2.0.9 7a closeout)  
- [`docs/phase-7-plan.md`](./phase-7-plan.md)  
- [`docs/lab-runbook.md`](./lab-runbook.md) § Phase 7a  
- [Ironic configuration drive](https://docs.openstack.org/ironic/latest/install/configdrive.html)  
- [cloud-init ConfigDrive datasource](https://docs.cloud-init.io/en/latest/reference/datasources/configdrive.html)  
- Golden Rules in `AGENTS.md` §1  

---

## 16. Future plans (decision guide)

These items are **explicitly out of scope** for the first multi-stage implementation
slices (M1–M6). They are recorded so near-term choices stay **compatible** with them
and we do not paint ourselves into a corner.

### 16.1 Keep NetBox as device identity (do not build a parallel inventory)

**Today (design Golden Rule):** **NetBox stores identity + current `lifecycle_state`
only.** Shoal does **not** keep its own durable device inventory table. Durable jobs /
events live in telemetry Postgres; the device record (name, serial, BMC access metadata,
`shoal_lifecycle_state`, `shoal_credential_ref`, etc.) is NetBox’s job.

**Decision for this design and near-term implementation:** **Keep NetBox.** Multi-stage
provisioning should continue to:

- Key jobs by `device_id` that aligns with NetBox (or lab spike flags that later bind)
- Best-effort Orchestrator sync of `lifecycle_state` on terminal transitions (as today)
- Resolve BMC/credential context from NetBox / `credential_ref` where the product path
  uses Discover/identity — without inventing a second CMDB inside Shoal

**Lab / spike exception (unchanged):** Phase 2–style `deploy run` with explicit
`-bmc-url` / serial flags can still run **without** a NetBox round-trip for a single job.
That is a binding convenience, not a plan to replace NetBox with an internal device
store.

**What we are *not* planning:** a Shoal-owned device registry that duplicates NetBox
fields. If a future CMDB other than NetBox ever appears, that would be a new design —
not a reason to stop using NetBox now.

**Near-term guidance for M1–M6:** Stage runner owns **job + BMC + media** truth; NetBox
updates stay best-effort and non-blocking for media cleanup (same as existing Deploy
reliability rules). Do not put stage/seed config into NetBox custom fields (profiles +
job store remain the place for install intent).

### 16.2 Image builder for VMware ESXi and Windows

**Today (this design):** ESXi and Windows v1 are **`operator_iso`** — the operator
builds the ready install ISO (kickstart / Autounattend already present). Shoal attaches
and provisions only.

**Future option:** Shoal-side (or sidecar) **media composition**:

| Family | Possible builder responsibilities |
|--------|-----------------------------------|
| **ESXi** | Inject `ks.cfg`, patch `boot.cfg` / boot args, version-aware layouts |
| **Windows** | Inject `Autounattend.xml`, optional driver packs, ISO rebuild tooling |
| **Shared** | Versioned output names (VM cache), secret render at compose time, publish to `SHOAL_ISO_*` |

**Near-term guidance:** Keep a clean boundary — `MediaComposer` / `operator_iso` vs
“compose then attach.” Do not special-case ESXi/Windows seed paths that assume Shoal
remastered the media. When this lands, it should be a **separate design document**
(compose pipeline, tooling host OS, licensing for Windows, Secure Boot) and new
implementation slices after M5.

**Trigger:** Operators want Shoal to own kickstart/unattend injection instead of an
external media factory.

### 16.3 Customizable pre-install (prep) image: actions and tooling

**Today:** Prep is a **Shoal maintenance live** with a fixed capability set (wipe stub,
optional RAID stub, config-drive write, `PREP_*` markers).

**Future option:** Treat prep as a **pluggable action pipeline** on a customizable
live image:

```text
prep_profile:
  actions:
    - type: wipe
      level: discard
    - type: raid
      profile_ref: raid1-os
    - type: firmware_bundle   # or redfish_side_effect for BMC firmware
      ref: …
    - type: shell_plugin      # carefully sandboxed; lab/advanced only
      image_layer: vendor-storcli
    - type: write_config_drive
      seed_ref: …
```

| Capability | Near-term (M2) | Future |
|------------|----------------|--------|
| Action list | Hard-coded wipe (+ seed write) | Ordered, validated action graph |
| Tooling | Busybox + minimal tools in one ISO | Optional **layers** / packs (storcli, MegaCLI, vendor fw) selected by profile |
| Build | Single prep ISO artifact | Compose prep ISO from base + selected packs (still offline for the guest) |
| Safety | 5b approval for wipe | Per-action approval tags; no untrusted network pulls during prep |
| Redfish vs in-band | BMC firmware via Redfish when possible | Keep that split; plugins document in-band vs OOB |

**Near-term guidance:** Implement prep as an **interface** (`PrepAction` / runner) even
if only one action exists. Avoid baking MegaCLI paths into Orchestrator. Keep SOL phase
names stable (`PREP_*`) so custom actions map into the same protocol.

**Trigger:** Real hardware needs vendor RAID/firmware packs, or customers require
site-specific prep scripts without forking Shoal.

### 16.4 How to use this section

When reviewing an implementation PR:

1. Does it invent a **Shoal device inventory**? Prefer NetBox for identity/lifecycle (§16.1).  
2. Does it **assume** Shoal remasters ESXi/Windows? Keep `operator_iso` until §16.2.  
3. Does prep add a **one-off special case** instead of an action/plugin slot? Prefer
   §16.3 shape.

None of §16 is required to merge M0–M6 or to call the first multi-stage path successful.
