# NetBox integration: visual telemetry, events, and job context

**Status:** Read-only MVP complete — backend APIs **N1–N3** and plugin tabs
**N4–N6** (Status, Events, Jobs, Sensors) implemented and lab-verified; **N7+**
(Grafana link, last-job pointer, write actions) not started.  
**Date:** July 2026 (status updated August 2026)  
**Audience:** Human architect + coding agents  
**Related:** Design SoT `SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md` (v2.0.9+);
[`docs/multi-stage-provisioning-design.md`](./multi-stage-provisioning-design.md) §16.1 (NetBox remains device identity SoT).

---

## 1. Purpose

Operators already use **NetBox** as the map of the fleet. Shoal already gathers BMC-adjacent
data (SEL/events, sensors, job progress, job logs) in **telemetry Postgres** and exposes
some of it over HTTP (`GET /v1/devices/{id}/status`, `…/events`, jobs API).

This design specifies a **tighter NetBox integration** so that, from a **device page in
NetBox**, operators can **visually inspect** that telemetry and provisioning context —
without making NetBox a time-series database and without building a parallel device
inventory inside Shoal.

---

## 2. Problem statement

### 2.1 What we have

| Layer | Role today |
|-------|------------|
| **NetBox** | Device identity + current `lifecycle_state` (+ credential ref / BMC metadata as designed) |
| **Shoal telemetry DB** | `jobs`, `events`, `sensor_readings`, `job_log` |
| **Shoal API** | Device status/events; job start/status/cancel; metrics |
| **Operator UX** | API + CLI only (no first-party browser UI in MVP) |

### 2.2 What operators want

From **NetBox** (where they already look at devices):

- See recent **SEL / normalized events**
- See **sensor** context (latest and/or simple history)
- See **active / recent provisioning jobs** and phase/progress
- Optionally tail **job log** lines related to that device
- Navigate without juggling raw URLs and tokens

### 2.3 Hard constraint (non-negotiable)

**Golden Rule:** *NetBox stores identity + current `lifecycle_state` only. Time-series and
events (SEL, sensors, job logs, durable jobs) go to the telemetry store — never into NetBox
custom fields as history.*

Therefore:

| Do | Do not |
|----|--------|
| Use NetBox as **navigation + identity hub** | Dump SEL/sensor series into custom fields |
| **Render** Shoal data in NetBox UI (plugin) or linked dashboards | Duplicate event tables into NetBox |
| Store tiny **pointers** on the device if useful (e.g. Shoal base URL, last job id) | Make NetBox SoT for job state machines |

---

## 3. Goals and non-goals

### 3.1 Goals

1. **Device-centric view** of Shoal telemetry keyed by the same `device_id` / NetBox device
   identity Discover and Deploy already use.
2. **In-NetBox visualization** as the primary operator path (plugin tabs preferred over
   “API only”).
3. Preserve **single device inventory** in NetBox (no Shoal device registry).
4. Clear **auth** model for NetBox → Shoal reads (service token).
5. Identify **API gaps** on Shoal needed for a good UI (jobs-by-device, sensors, logs).
6. Optional **Grafana** path for rich sensor graphs without blocking the NetBox plugin MVP.
7. Lab install path via Ansible (plugin + config) when ready.

### 3.2 Non-goals

- Replacing NetBox with a Shoal SPA as CMDB  
- Storing full event/sensor history in NetBox  
- Real-time SOL streaming inside NetBox (v1; optional later via Shoal log API)  
- Rewriting Observe/Deploy core for the UI  
- Multi-tenant SaaS NetBox hosting  
- Shipping a general-purpose Shoal web console (may share APIs later)

---

## 4. Architecture

### 4.1 Principle: NetBox shell, Shoal data plane

```text
┌─────────────────────────────────────────────────────────────┐
│  NetBox (identity + lifecycle_state)                        │
│  Device detail page                                         │
│    ├─ Plugin tab: Status      ──HTTP──► Shoal API           │
│    ├─ Plugin tab: Events/SEL  ──HTTP──► Shoal API           │
│    ├─ Plugin tab: Jobs        ──HTTP──► Shoal API           │
│    ├─ Plugin tab: Sensors     ──HTTP──► Shoal API           │
│    └─ Optional link: Grafana  ──query──► telemetry DB/API   │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ Bearer service token
                              ▼
┌─────────────────────────────────────────────────────────────┐
│  Shoal (:8088)                                              │
│  GET /v1/devices/{id}/status|events|sensors|jobs…           │
│  GET /v1/jobs/{id}                                          │
│         │                                                   │
│         ▼                                                   │
│  telemetry Postgres (events, sensor_readings, jobs, job_log)│
└─────────────────────────────────────────────────────────────┘
```

- **Write path** for lifecycle remains Deploy Orchestrator → NetBox (best-effort), as today.  
- **Read path** for telemetry is always Shoal → Postgres (or Grafana → Postgres).  
- Plugin is **read-mostly** for v1 (no “start job from NetBox” required for MVP; can be a
  later phase).

### 4.2 Identity mapping

**Resolved (N4/N5, confirmed against `internal/common/netbox/client.go`):** Shoal's
`device_id` **is the NetBox device's numeric primary key** (string form).
`Client.UpsertDevice` looks up/creates devices by `serial`, but the NetBox device ID
returned from that create/lookup is what Shoal writes onto every job/event/telemetry
row from then on (`internal/discover/service.go`'s `finalize()`). **No dedicated
`shoal_device_id` custom field is needed or created** — the plugin's views read
`device.pk` directly off the `Device` object NetBox already resolved for the tab. This
replaces the speculative "name or `shoal_device_id` CF" MVP rule below, which is kept
struck through for history.

~~**MVP rule:** Plugin uses NetBox device name or a dedicated custom field
`shoal_device_id` if present; otherwise NetBox numeric id string only if that is
already how the lab keys devices.~~

**Custom fields actually bootstrapped** (`infra/ansible/roles/netbox_bootstrap`, all
`dcim.device`-scoped, plain names — **not** `shoal_`-prefixed as an earlier draft of
this table assumed):

| Field | Purpose |
|-------|---------|
| `lifecycle_state` | Current Shoal lifecycle state |
| `credential_ref` | Secrets-backend key for BMC credentials (never a password) |
| `bmc_ip` | Management BMC address |

No `shoal_last_job_id` or `shoal_ui_base` fields exist; both remain optional future
work (N8 and a per-device Shoal-URL override, respectively), not needed for N4/N5.

### 4.3 Components

| Component | Language / home | Responsibility |
|-----------|-----------------|----------------|
| **Shoal API** | Go `internal/api` | Device-scoped read APIs; auth |
| **Shoal telemetry** | Postgres | Durable events/sensors/jobs/logs |
| **NetBox plugin** | Python/Django (NetBox plugin SDK) | Device tabs; server-side HTTP to Shoal |
| **Ansible (lab)** | `infra/ansible` | Install plugin into lab NetBox, set config |
| **Grafana (optional)** | Lab compose | Sensor dashboards; linked from plugin |

The plugin is a **separate artifact** (not linked into the Go binary). License/docs: treat
like other lab stack pieces (NetBox itself); do not fold into `NOTICE` unless we ship it
as a redistributed bundle with explicit policy.

### 4.4 Auth and security

| Concern | Approach |
|---------|----------|
| Plugin → Shoal | `Authorization: Bearer <token>` using `SHOAL_API_TOKEN` (or dedicated read-only token later) |
| Token storage | NetBox plugin config / env (not in git); vault in lab Ansible |
| Browser → plugin | Normal NetBox session (operator already authenticated to NetBox) |
| Secrets in UI | Never show BMC passwords, API keys, full credential material |
| Log lines | Redact password-like patterns before display if not already redacted at write |
| SSRF | Plugin only calls configured Shoal base URL (allowlist), not operator-supplied arbitrary hosts per request |

**Read-only token (future):** optional `SHOAL_API_TOKEN_READ` that cannot start/cancel jobs;
MVP may use the existing single token with plugin only calling GET routes.

### 4.5 What NetBox must not become

- A warehouse for SEL history  
- The job state machine SoT (Shoal `jobs` table remains SoT for provisioning jobs)  
- A substitute for Observe poll

---

## 5. Shoal API surface (for UI)

### 5.1 Existing (keep / polish)

| Method | Path | Use in UI |
|--------|------|-----------|
| `GET` | `/v1/devices/{id}/status` | Status tab: lifecycle, power, active job, phase/percent |
| `GET` | `/v1/devices/{id}/events?since=&limit=` | Events/SEL tab |
| `GET` | `/v1/jobs/{id}` | Job detail panel |
| `GET` | `/metrics` | Ops only; not NetBox-facing |

### 5.2 Gaps to add (implementation slices)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | ✅ `/v1/devices/{id}/jobs?limit=&state=` | Recent jobs for device |
| `GET` | ✅ `/v1/devices/{id}/sensors?since=&limit=` | Sensors tab (flat since/limit list, newest-first; no "latest-per-sensor" query yet — dedupe client-side if needed) |
| `GET` | ✅ `/v1/jobs/{id}/log?since=&limit=` | Job log lines from `job_log`, oldest-first (empty until a writer exists — see N3) |
| `GET` | `/v1/devices/{id}/summary` (optional) | Not implemented. Single round-trip: status + last N events + active job |

**Query conventions:** RFC3339 `since`, bounded `limit` (cap 200, enforced on the three
new endpoints above — existing `status`/`events` endpoints are unchanged), stable JSON
shapes, empty lists not 404.

**Auth:** Same Bearer gate as other `/v1/*` when token configured.

### 5.3 Illustrative response shapes

**Jobs list:**

```json
{
  "device_id": "lab-node-1",
  "jobs": [
    {
      "id": "abc…",
      "state": "provisioning",
      "phase": "IMAGE_WRITE",
      "percent": 40,
      "profile_ref": "…",
      "updated_at": "…"
    }
  ]
}
```

**Sensors (latest snapshot):**

```json
{
  "device_id": "lab-node-1",
  "readings": [
    { "sensor": "Inlet Temp", "value": 24.5, "unit": "C", "ts": "…" }
  ]
}
```

**Job log:**

```json
{
  "job_id": "abc…",
  "lines": [
    { "ts": "…", "line": "SHOAL|1|3|…|IMAGE_WRITE|10|OK|…" }
  ]
}
```

(Exact marker formatting may already be in Observe/job progress; log API exposes durable
`job_log` rows when writers exist.)

### 5.4 Non-API data path (optional Grafana)

For long sensor history and multi-device graphs:

- Grafana → Postgres `sensor_readings` (read-only role) **or**  
- Grafana → Shoal API if we add range queries later  

Plugin shows a button: “Open sensor dashboard” with `device_id` variable.

---

## 6. NetBox plugin UX

### 6.1 Device tabs (MVP)

| Tab | Content | Data | Status |
|-----|---------|------|--------|
| **Shoal Status** | Lifecycle, power, active job, phase | `GET …/status` | ✅ N5 |
| **Shoal Events** | Table: ts, severity, type, component, message | `GET …/events` | ✅ N5 |
| **Jobs** | Table of recent jobs (id, state, phase, percent, profile, updated, error) | `GET …/jobs` | ✅ N6 |
| **Sensors** | Flat readings table (sensor, value, unit, ts); no sparkline yet | `GET …/sensors` | ✅ N6 |

Empty states: “No events yet — has Observe poll run?” with lab runbook link.

### 6.2 Global plugin configuration

Implemented in `extras/netbox-plugin-shoal` (`netbox_shoal.PluginConfig.default_settings`),
rendered into the lab's `plugins.py` by Ansible (`infra/ansible/roles/compose_stack`):

```python
PLUGINS_CONFIG = {
    "netbox_shoal": {
        "SHOAL_BASE_URL": "http://host.docker.internal:8088",  # empty = "not configured" in the UI
        "SHOAL_API_TOKEN": "",                                  # optional Bearer; empty = no header sent
        "SHOAL_REQUEST_TIMEOUT": 10,
    }
}
```

`SHOAL_DEVICE_ID_FIELD` from the earlier speculative version of this block is dropped —
see §4.2, no device-id field lookup is needed.

### 6.3 Optional v2 plugin features

- “Refresh” button with cache-bust  
- Embed job log viewer for selected job  
- Deep link to cancel job (POST) with NetBox permission gate — **after** read MVP  
- Start job wizard — **later** (needs profile picker + secrets discipline)

### 6.4 Deep links only (phase 0 deliverable)

If plugin packaging is slow, ship first:

1. NetBox custom link / button template → Shoal external minimal HTML page **or**  
2. Custom link to Grafana  

Prefer not to stop at deep links forever; plugin is the target UX.

---

## 7. Minimal external Shoal HTML (optional, not full product UI)

If useful for deep links without Grafana:

- `GET /ui/devices/{id}` static/template page served by Shoal (stdlib templates only)
- Same APIs as plugin; no NetBox dependency  

**Not** a commitment to a large SPA. Defer if plugin lands first.

---

## 8. Data flow (Observe remains source of SEL/sensors)

```text
BMC (Redfish) ──poll──► Observe ──write──► telemetry Postgres
                                              ▲
NetBox plugin ──GET /v1/devices/… ──► Shoal API ─┘
```

Provisioning:

```text
Deploy job ──progress──► jobs (+ job_log)
              └──lifecycle──► NetBox CF (current state only)
NetBox plugin ──GET jobs by device──► Shoal
```

---

## 9. Lab / ops packaging

| Slice | Work |
|-------|------|
| Ansible | Install NetBox plugin into lab NetBox container/image; set env; restart |
| Custom fields | Ensure `shoal_*` fields exist (bootstrap playbook already partially does lifecycle) |
| Network | NetBox → Shoal HTTP on mgmt network (lab: `192.168.122.100:8088` or compose DNS) |
| Docs | lab-runbook: “Open device in NetBox → Shoal tabs” |

---

## 10. Phased implementation plan

| Slice | Deliverable | AC |
|-------|-------------|-----|
| **N0** | This design merged | Docs only |
| **N1** | ✅ Shoal API: `GET /v1/devices/{id}/jobs` (+ tests) | Done — `jobstore.Store.ListByDevice`, unit + lab-integration tests |
| **N2** | ✅ Shoal API: sensors latest (and/or since) | Done — `GET /v1/devices/{id}/sensors`, unit + lab-integration tests |
| **N3** | ✅ Shoal API: `GET /v1/jobs/{id}/log` | Done — honest empty state confirmed: `job_log` has no production writer yet (`WriteJobLog` is only called from tests), so this endpoint correctly returns `{"lines":[]}` until a writer lands; that writer work is a separate, not-yet-scoped slice |
| **N4** | ✅ Config context for Shoal base URL (no new custom fields needed — see §4.2) | Done — `extras/netbox-plugin-shoal`, Ansible `plugins.py` wiring |
| **N5** | ✅ NetBox plugin MVP: Status + Events tabs | Done — `extras/netbox-plugin-shoal/netbox_shoal/views.py`; verified live in the lab (both tabs render real job/event data and the designed empty states) |
| **N6** | ✅ Plugin Jobs + Sensors tabs | Done — `ShoalJobsView`/`ShoalSensorsView`; verified live in the lab |
| **N7** | Optional Grafana dashboard + plugin link | Lab compose |
| **N8** | Optional Orchestrator write of `shoal_last_job_id` pointer | NetBox shows last job link |

Do **not** block N5 on Grafana or job start-from-NetBox.

---

## 11. Relationship to other designs

| Design | Relationship |
|--------|----------------|
| Main SoT Golden Rule 4 | Reinforced: no time-series in NetBox |
| Multi-stage provisioning §16.1 | NetBox remains identity; this design is the **visibility** half |
| Phase 4 Observe | Produces the events/sensors this UI displays |
| Phase 6d auth | Bearer token reused for plugin |
| Phase 6e+ | Not a substitute for this |

---

## 12. Open questions

1. ✅ **Canonical `device_id`:** resolved — NetBox numeric device ID (`device.pk`), matching
   `internal/common/netbox/client.go`'s existing `UpsertDevice` convention. No new custom
   field. See §4.2.
2. ✅ **Plugin repo location:** resolved — monorepo `extras/netbox-plugin-shoal/`.
3. **Read-only API token** vs single token for MVP: still open, but moot for N4/N5 — the
   plugin only calls `GET` routes (never starts/cancels jobs), so the existing single
   `SHOAL_API_TOKEN` is sufficient until a write-capable feature is built.
4. ✅ **job_log population:** resolved (see N3, PR #32) — no production writer exists yet;
   `GET /v1/jobs/{id}/log` honestly returns `{"lines":[]}` until one lands. Not relevant to
   N4/N5 (Status/Events tabs don't call this endpoint).
5. **Historical sensor retention** and Grafana retention policy: still open, relevant to N6/N7.

---

## 13. Success metric

An operator can:

1. Open a device in **NetBox**,  
2. See **Shoal status**, **recent events**, **recent jobs**, and **latest sensors** without
   using curl,  
3. Trust that history lives in **Shoal telemetry**, while NetBox still shows only identity +
   current lifecycle,  
4. In lab, complete this path after `up.yml` + plugin install docs.

---

## 14. Future plans (decision guide)

### 14.1 Start / cancel jobs from NetBox

NetBox as a control surface for Deploy. Requires careful secrets UX, profile picker, and
permission mapping. **After** read-only MVP (N5–N6).

### 14.2 Live SOL / log stream in the browser

SSE/WebSocket from Shoal to plugin or Shoal UI. Higher complexity; keep job_log polling
first.

### 14.3 Multi-CMDB

If another inventory system appears, extract a “device hub link” pattern; **do not** build
a Shoal inventory to replace NetBox without a new design.

### 14.4 Richer Shoal-native UI

Plugin and optional `/ui` pages share the same read APIs. A larger SPA is optional product
work, not required for NetBox integration success.

---

## 15. References

- Design SoT § NetBox integration, Golden Rules, Phase 4 Observe  
- [`docs/multi-stage-provisioning-design.md`](./multi-stage-provisioning-design.md) §16.1  
- `internal/api/devices.go` — status/events handlers  
- `internal/common/telemetry/schema.sql` — `events`, `sensor_readings`, `jobs`, `job_log`  
- `internal/common/netbox` — identity + lifecycle only  
- NetBox plugin development docs (upstream)  
