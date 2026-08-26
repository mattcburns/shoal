# Deprovision — Return a Node from `provisioned` to `ready`

Status: **draft, for review** (revised after code verification — see
"Verified against code" callouts throughout). Not implemented. No code
changes accompany this document.

## Overview

Shoal has no concept today of retiring a node it previously provisioned.
Once a device reaches `lifecycle_state=provisioned`, the only way back to a
reusable state is manual: an operator has to know to run a prep/wipe job by
hand, and nothing tells NetBox the device is available again. This document
proposes **deprovision**: an explicit action that wipes a provisioned node's
disk and returns it to `lifecycle_state=ready`, so it re-enters the normal
discover → ready → provisioning → provisioned pool instead of sitting in
`provisioned` forever or being tracked by hand outside Shoal.

The core design bet: **deprovision is not a new concept, it's the existing
`prep=wipe_only` mechanism run standalone, with the lifecycle write-back
changed from `provisioned` to `ready`.** `SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`
already names `PROVISIONED → READY` as the intended re-provision transition
and explicitly says not to invent a separate long-lived `CLEANUP` lifecycle
enum — this document follows that instruction rather than reopening it.

**"Standalone" is not free, though** — confirmed by reading the actual
stage-builder (`internal/deploy/job/stages.go`): `prep=wipe_only` today
*always* produces a two-stage job (prep, then os_install) and *always*
requires `iso_url`. Running prep alone is a real, scoped code change, not
just a documented convention. See Proposed Design.

### Is `/v1/jobs` provisioning-only?

Worth answering directly, since this document leans on reusing it. As of
today: not deliberately, but not really general either — a
provisioning-specific type wearing a generic name. The Go type is
`models.ProvisioningJob` (no bare `Job` type exists anywhere in the repo),
and every field beyond basic bookkeeping (`ID`/`State`/`Attempt`/timestamps)
is install-shaped: `ISOURL`, `InstallStrategy`, `SOLSessionID`. The CLI is
nested entirely under `deploy` (`deploy run`/`deploy cancel`). No design
doc discusses whether jobs were meant to support other kinds of work —
the question has simply never come up before now.

Two things already point toward generality, though: the wire routes are
`POST /v1/jobs` (not `/v1/deploy-jobs`), and `JobStageKind` already has
**three** values, not the two this document assumed at first —
`prep`, `os_install`, and `verify` — with `prep` already a first-class
stage kind carrying its own `ProgressPolicy` handling, not a special case
bolted onto install. So the *stage* layer already has real precedent for
non-install work; the top-level job type just hasn't caught up.

Rather than lean on an implicit "prep with no install fields means
deprovision" inference (which Key Decision 1 originally proposed and the
Risks section flagged as fragile), **this document adds an explicit `Kind`
field** — see Key Decision 5. And since adding `Kind` already changes the
request/response shape, this document also bundles in the rename that
shape change was going to make necessary sooner or later anyway:
`models.ProvisioningJob` → `models.Job` — see Key Decision 7. Better to do
it now, while the type's contract is already being touched, than let the
name drift further from what it actually holds every time a new `Kind` is
added later.

## Background & Motivation

- Every real-hardware bug fixed in `PROVISIONING_PROGRESS.md` this session
  assumed a forward path: discover, provision, done. Nothing exercises the
  reverse path, and there's no lifecycle state that means "wiped and free
  again."
- `prep`/`wipe_only` already exists (`internal/deploy/job/orchestrator.go`,
  `models.StartJobRequest.Prep`) and already does the destructive part
  correctly: `blkdiscard` or `dd`-zero the target disk, gated behind
  `approve_destruct`, running *inside* a booted marker ISO exactly like an
  install stage (`JobStageKindPrep`, `PREP_BOOT` → `PREP_WIPE` → `PREP_SEED`
  → `PREP_DONE` markers). It has never been run as the *only* stage of a
  job — only as stage 1 of a prep-then-install pipeline.
- Terminal-state BMC cleanup (eject virtual media, clear boot override) is
  already unconditional on every job ending, success or failure
  (`Orchestrator.cleanupBMC`, called from `handleTerminalOnce` for any
  `bmcURL != ""`). Deprovision gets this for free — no new BMC-cleanup code
  needed.
- What deprovision actually needs that doesn't exist yet: (1) a way for a
  request to say what kind of job it is at all (no `Kind`/discriminator
  field exists today — see "Is `/v1/jobs` provisioning-only?" below), (2) a
  job that ends after `prep` with no install stage queued — **today
  `expandStages` never produces this shape**, (3) that job's success
  writing `lifecycle_state=ready` instead of `provisioned`, (4) the node
  powering itself off cleanly afterward instead of sitting in the existing
  wait-for-next-stage heartbeat loop. Device-credential handling turns out
  to need *no* new work (Key Decision 4) — though cleaning up unrelated
  ephemeral job credentials turned out to be worth doing while in this
  area (Key Decision 6), even though it isn't deprovision-specific.

## Goals

- Wipe a provisioned node's boot disk and return it to `ready` through the
  same job/marker/SOL-watch machinery every other stage already uses — no
  new transport, no new marker protocol.
- Make deprovision reachable from all three surfaces this session proved
  out for provisioning: CLI, `POST /v1/jobs`, NetBox button.
- Require explicit operator consent (`approve_destruct`) exactly like
  `wipe_only` already does — deprovision must never be a side effect of
  something less deliberate.
- Leave the device's BMC access (its NetBox-stored `credential_ref`)
  untouched, so re-provisioning the same device afterward doesn't require
  an operator to re-enter BMC credentials by hand. (This was originally
  framed as a goal to *delete* the credential — see Key Decision 4,
  revised, for why that was wrong.)

## Non-Goals

- **Firmware/BIOS reset, RAID teardown, or hardware re-inventory.**
  `validate.go` already reserves (and rejects) `prep=full` as "not
  implemented" — that's the eventual home for anything beyond a disk wipe,
  not this document.
- **A new `retired`/`decommissioned` lifecycle state.** Per the
  comprehensive design doc's own instruction, deprovision targets the
  existing `ready` state. A device that should never be provisioned again
  (hardware pulled, RMA'd) is a NetBox-side concern (change its role/status
  there) — Shoal's lifecycle field only tracks "is this usable by Shoal
  right now."
- **Rotating or deleting the device's BMC credential.** See Key Decision 4,
  revised — that credential is Shoal's own hardware access, not tenant
  data, and doesn't need to change just because the disk was wiped. If
  BMC-credential rotation is ever wanted, it's a separate feature (likely
  built on Redfish `AccountService`), not part of deprovision.
- **Secure erase / cryptographic wipe / multi-pass overwrite.** `wipe_level`
  already offers `discard` and `zero`; deprovision reuses whichever the
  caller picks. A stronger guarantee is a future `wipe_level`, not new
  deprovision-specific machinery.
- **Automatic/scheduled deprovisioning.** This is an explicit, operator- or
  automation-triggered action, not a TTL or idle-timeout.

## Key Decisions

These are the points most worth pushback before implementation starts.

1. **Reuse `POST /v1/jobs` (extended with `Kind`, see Key Decision 5)
   rather than adding a new `/v1/devices/{id}/deprovision` endpoint —
   requires a real orchestrator change, confirmed by reading the code.**
   `expandStages` (`internal/deploy/job/stages.go:15-98`) unconditionally
   builds the `os_install` stage first and, for `prep=wipe_only`, explicitly
   requires `installMedia != ""` (`"job: prep wipe_only requires iso_url
   for os_install stage"`) — every code path returns *both* stages, never
   prep alone. `Start` separately hard-requires `req.ISOURL != ""`
   regardless of `prep` (`orchestrator.go:321-323`). So reusing `/v1/jobs`
   isn't just a documentation convention — it needs `expandStages` taught a
   new branch, keyed on `Kind == "deprovision"` (not on which fields
   happen to be empty — that was the original framing here, superseded by
   Key Decision 5), that returns a single-stage
   `[]models.JobStage{prepStage}` and skips the `ISOURL` requirement.
   Recommended over a dedicated endpoint because it avoids duplicating
   BMC-connect/SOL-watch/stage-sequencing/terminal-cleanup logic `Start`
   already owns — "reuse" here means "extend `Start`," not "reuse
   as-is."

2. **Power off after the wipe: orchestrator-issued, not guest-issued.**
   Confirmed: `build-marker-iso.sh`'s `/init` for `prep` mode deliberately
   stays on and heartbeats after `PREP_DONE`, waiting for the orchestrator
   to swap virtual media and `ForceRestart` into the next stage — correct
   for prep-then-install, wrong when there's no next stage. Decision: the
   orchestrator, on observing `PREP_DONE` with no next stage in the job,
   issues an explicit `ForceOff`/`GracefulShutdown` via the BMC itself,
   rather than teaching the marker ISO a new "standalone" mode. Keeps the
   guest's behavior identical regardless of job shape and avoids a
   marker-ISO rebuild/republish dependency for this feature. **Confirmed
   direction — no change from the original draft.**

3. **Wipe level: required, no default.** `discard` (`blkdiscard`) is fast
   but may leave data recoverable until the SSD's own garbage collection
   reclaims blocks; `zero` is a synchronous 64MiB `dd` — destroys the
   partition table and filesystem superblocks but is not a full-disk
   overwrite (matches today's `prep` semantics unchanged). Deprovision
   requires the caller to pass `wipe_level` explicitly — no implicit
   default. **Confirmed direction — no change from the original draft.**

4. **Credential handling — revised, this was wrong in the original draft.**
   The original draft proposed deleting the device's `credential_ref`
   secret and clearing NetBox's `credential_ref` custom field on
   deprovision, reasoning it prevented "a previous tenant's BMC password"
   leaking forward. That reasoning doesn't hold up: there turn out to be
   **two unrelated `credential_ref` lifecycles**, confirmed by reading
   `orchestrator.go`:
   - **Ephemeral, job-scoped** (`orchestrator.go:326-335`): if
     `req.CredentialRef` is empty after binding resolution, `Start` mints
     `"job-" + jobID`, storing the raw username/password under it for that
     job only. Nothing else ever references it — safe to delete once any
     job ends, but that's general job hygiene applicable to every job kind,
     not something deprovision-specific.
   - **Persistent, device-scoped** (`orchestrator.go:1676-1691`, e.g.
     `bmc-C784MH3`, sourced from NetBox's device `credential_ref` custom
     field via `GetDevice` — confirmed in
     `extras/netbox-plugin-shoal/netbox_shoal/views.py`'s
     `_device_credential_ref`): this is **Shoal's own operational access to
     the physical BMC** — the iDRAC username/password. It is unrelated to
     whatever OS or workload is on the wiped disk; BMC credentials live in
     the hardware's out-of-band management, not on the disk being erased.
     Deleting it on deprovision would only break the *next* provision of
     that same device, forcing an operator to manually re-enter BMC
     credentials in NetBox for no security benefit — the "leak forward"
     framing was a misunderstanding of what this ref actually is.

   **Revised decision: deprovision deletes nothing.** `secrets.Backend.Delete`
   (interface + `Memory`/`File` implementations already exist, confirmed
   zero call sites anywhere in the repo today) is not called *by this
   feature specifically* — but see Key Decision 6, which does put the first
   call site somewhere, just not gated on "is this a deprovision job."
   The device's persistent `credential_ref` is left exactly as-is in
   NetBox, so a subsequent `Start` on the same device resolves BMC access
   the same way it already does today.

5. **Add an explicit `Kind` field instead of inferring deprovision from
   request shape.** `StartJobRequest`/`ProvisioningJob` gain
   `Kind string` (`"install"` default/omitted for full backward
   compatibility, `"deprovision"` for this feature). The `expandStages`
   branch in Key Decision 1 keys on `Kind == "deprovision"` directly
   instead of "prep=wipe_only and every install field happens to be
   empty" — same underlying mechanism, but self-describing on the wire
   instead of inferred, and it directly answers "shouldn't jobs be able to
   say what kind of work they are?" (see "Is `/v1/jobs` provisioning-only?"
   above) rather than dodging the question. This also resolves the
   "Implicit request shape" item that was flagged as a Risk in the
   original draft — a caller can no longer accidentally land in
   deprovision by omitting install fields for some other reason, since
   `Kind` has to be set explicitly.

6. **Delete ephemeral job-scoped credentials on every job's terminal
   state — not deprovision-specific, but resolved here rather than left
   dangling.** `Start` mints `"job-" + jobID` (`orchestrator.go:326-335`)
   whenever a caller passes raw `bmc_username`/`bmc_password` instead of a
   stored `credential_ref`; nothing ever cleans these up today; they
   accumulate in `secrets.Backend` forever. Fix: in `handleTerminalOnce`
   (the same place `cleanupBMC` already runs unconditionally on every job
   ending), also delete the credential ref *if and only if* it matches the
   exact `"job-" + job.ID` pattern this job itself would have minted —
   never delete a ref that doesn't match that precise convention, so a
   persistent device-scoped ref (`bmc-C784MH3`) can never be touched even
   by a future bug. This applies to every job kind, install and
   deprovision alike, and is why it's numbered as its own decision rather
   than folded into Key Decision 4.

7. **Rename `models.ProvisioningJob` → `models.Job`, bundled into this
   work rather than deferred.** The original draft raised this as an
   explicitly out-of-scope future cleanup (Open Question). Reversed: since
   `Kind` (Key Decision 5) is already changing the type's contract, and
   the wire routes (`/v1/jobs`) and `jobstore` package name already assume
   this generality, doing the rename now avoids the type's name drifting
   even further from what it actually holds. Scope, precisely:
   - **In scope**: rename the Go type `models.ProvisioningJob` →
     `models.Job` and update every call site (confirmed 57 across
     non-test `.go` files) — purely mechanical, one type identifier.
     Optionally clean up obviously provisioning-specific *parameter*
     names on `jobstore.Store` methods for readability (e.g.
     `UpdateStages`'s `installStrategy string` parameter) — cosmetic,
     no signature/behavior change, do only if it doesn't add risk.
   - **Explicitly out of scope, not decided here**: the `deploy` CLI verb
     namespace (`deploy run`/`deploy cancel`/now `deploy deprovision`) —
     that's user-facing surface area operators already have muscle memory
     and scripts around; renaming it is a real, separate, breaking
     decision this document doesn't make. The `jobstore` package name —
     already generic, nothing to change. Any JSON field name — **zero
     wire-format impact either way**: Go's `encoding/json` keys off struct
     tags (`json:"id"`, `json:"device_id"`, etc.), not the Go type name,
     so this rename is invisible to every existing HTTP client, the CLI's
     own JSON handling, and the NetBox plugin's `client.py`. That's what
     makes it safe to bundle in now rather than a reason to avoid it.

### State transition

```
provisioned --[deprovision job: prep=wipe_only, no install stage]--> ready
    (also reachable from `failed`, for a device that never finished
     provisioning but still needs its disk wiped before reuse)
```

No new `LifecycleState` value. `internal/common/models/models.go`'s existing
`discovered | ready | provisioning | provisioned | failed` is unchanged.
While a deprovision job is running, lifecycle is synced to `provisioning`
exactly like a normal install (existing `syncNetBoxLifecycle` call at job
start already does this unconditionally) — an operator watching NetBox sees
the same "provisioning" spinner state either direction, which is honest:
the device genuinely isn't usable mid-wipe either.

### Orchestrator changes

- **`models.StartJobRequest`/`ProvisioningJob`: new `Kind` field**
  (Key Decision 5) — `json:"kind,omitempty"`, empty/`"install"` preserves
  every existing caller's behavior unchanged, `"deprovision"` is new.
- **`expandStages` (`internal/deploy/job/stages.go`): new branch.** When
  `req.Kind == "deprovision"`, return a single-stage
  `[]models.JobStage{prepStage}` instead of always appending `os_install`.
  The existing `iso_url`-required-for-`os_install` check must not fire for
  this shape. This is the one confirmed, concrete code change everything
  else here depends on (see Key Decision 1).
- **`Start` (`orchestrator.go:321-323`): relax the unconditional
  `req.ISOURL != ""` requirement** for `Kind == "deprovision"`.
- **Terminal write-back.** On a `Kind == "deprovision"` job reaching
  `PREP_DONE`: mark the job `done`, issue the BMC power-off (Key Decision
  2), delete the job's own ephemeral credential if it minted one (Key
  Decision 6 — applies here as it does to every job, nothing
  deprovision-specific about that step), then call
  `syncNetBoxLifecycle(ctx, req.DeviceID, models.StateReady)` instead of
  `models.StateProvisioned`. This is the one behavioral fork needed in
  whatever currently hardcodes `provisioned` on a successful terminal job —
  now a clean `switch req.Kind` instead of inferring intent from stage
  shape.
- **No device-credential handling** (Key Decision 4, revised) — nothing to
  add beyond what `applyStartBindings` already does to resolve the
  device's existing persistent credential for the wipe job itself, and
  the ephemeral-ref cleanup from Key Decision 6 (which every job kind
  gets, not something deprovision adds).
- `cleanupBMC`/`postCheckClean` run unchanged — already unconditional on
  every terminal state, already do the right thing (eject media, clear
  boot override) for deprovision with no modification needed.
- Failure handling: if the wipe stage fails (stall, BMC error, marker
  `ERROR`), land in `failed` as today — no lifecycle side effects either
  way (device-credential untouched regardless; ephemeral-ref cleanup from
  Key Decision 6 still applies since it's unconditional on any terminal
  state, not gated on success) — so this requires no new failure-path code
  beyond what `os_install` failures already do.

### Validation

Reuse `validate.go`'s existing `prep=wipe_only` rule (`approve_destruct`
required, `prep_iso_url` required via request or `SHOAL_PREP_ISO_URL`) with
one addition: when `Kind == "deprovision"`, `wipe_level` becomes
**required** rather than optional (Key Decision 3) — today it's optional
with an implicit default inherited from the prep stage's own script
default.

## API / Interface Changes

**CLI** (`internal/cli/deploy.go`): new `deploy deprovision` verb, shaped
like `cmdDeployCancel` (build a full `Orchestrator`, poll the jobstore the
same way `cmdDeployCancel` already does — no long-running watch of its
own needed). Flags: `-device-id`, `-bmc-url`, `-bmc-user`/`-bmc-pass` (or
rely on the device's stored `credential_ref`, resolved automatically
exactly like `deploy run` already does via `applyStartBindings`),
`-wipe-level` (required, no default per Key Decision 3), `-approve-destruct`
(required — omitting it should be a validation error, not a silent no-op),
`-prep-iso-url` (or `SHOAL_PREP_ISO_URL`), `-wait`/`-wait-timeout`/
`-stall-timeout` reused as-is. Internally this is sugar over
`POST /v1/jobs` with `prep=wipe_only` and nothing else — `Kind=deprovision`
needed no new orchestrator method of its own, it flows through the same
`Start`/`StartAsync` (added 2026-08-25 so the HTTP path returns before BMC
bring-up finishes, not before-vs-after this feature) as every other job kind.

Example request body (compare to today's shape, confirmed from
`orchestrator_test.go`):

```json
// today, POST /v1/jobs (install):
{
  "device_id": "lab-node-1",
  "bmc_endpoint": "http://bmc.test",
  "serial_target": "lab-node-1",
  "iso_url": "http://iso/shoal-marker.iso"
}

// proposed, POST /v1/jobs (deprovision):
{
  "device_id": "lab-node-1",
  "bmc_endpoint": "http://bmc.test",
  "kind": "deprovision",
  "prep": "wipe_only",
  "wipe_level": "zero",
  "approve_destruct": true,
  "prep_iso_url": "http://iso/shoal-prep.iso"
}
```

`kind` is what the new `expandStages` branch keys on (Key Decision 5) —
self-describing, not inferred from the absence of `iso_url`/`install_strategy`.

**HTTP API**: no new route. `POST /v1/jobs` accepts the existing
`StartJobRequest` shape once extended with `Kind` per Proposed Design
above — `kind=deprovision` is explicit on the wire, not inferred.

**NetBox plugin** (`extras/netbox-plugin-shoal/netbox_shoal/`):
- `client.py`: new `deprovision_device(device_pk, body)` →
  `POST /v1/jobs` with the deprovision-shaped body (mirrors `start_job`,
  doesn't need a new endpoint).
- `views.py`: new `action == "deprovision"` branch in the same
  `_handle_control_post` dispatcher `start`/`cancel`/`power` already live
  in. Requires `dcim.change_device` (same as `start`/`cancel` today) *and*
  a checkbox/confirmation field in the form, mirroring how `start` already
  guards `approve_destruct` for `NeedsApproval`/`DestructSteps` profiles —
  same UI pattern, not a new one.
- `templates/netbox_shoal/status.html`: a "Deprovision" button, visibly
  separate from Start/Cancel/Power (different color, e.g. `btn-danger`),
  only enabled when `lifecycle_state` is `provisioned` or `failed` (mirrors
  the tab's existing state-gating for Start/Cancel visibility).

## Data Model Changes

None to `LifecycleState`. Two changes, both decided (Key Decisions 5 and
7, neither left open):

- `models.StartJobRequest` and `models.ProvisioningJob` both gain
  `Kind string` (`json:"kind,omitempty"`), values `"install"` (default,
  omitted preserves every existing caller) and `"deprovision"`. Beyond
  driving the `expandStages` branch, this also means
  `internal/observe`'s device-jobs views (`GET /v1/devices/{id}/jobs`) can
  show "Deprovisioned" vs "Provisioned" directly instead of
  reverse-engineering intent from `stages[].kind == "prep"` after the fact.
- `models.ProvisioningJob` renamed to `models.Job` (Key Decision 7) — Go
  identifier only, zero JSON/wire changes, ~57 call sites.

## NetBox Plugin UX

Sketch, not final copy: the Shoal Status tab's control form gains a second,
visually distinct section below Start/Cancel — "Deprovision this device"
with `wipe_level` (radio: discard/zero, no default selected), a required
confirmation checkbox ("This will permanently wipe the boot disk. The
device's stored BMC credential is not affected."), and a Deprovision
button disabled until the checkbox is checked. Success message on submit
mirrors Start's ("Deprovision job %s started (state=provisioning)...")
since the underlying mechanism is the same job/polling pattern operators
already know from watching a Start job run.

## Alternatives Considered

**A. Dedicated `POST /v1/devices/{id}/deprovision` endpoint.** More
self-describing on the wire, but duplicates job-orchestration logic
`Orchestrator.Start` already owns, or has to internally construct a
`StartJobRequest` anyway and call `Start` — at which point it's a thin
wrapper, not a different mechanism. Rejected in favor of the reuse in Key
Decision 1, now on firmer footing than the original draft since Key
Decision 5's explicit `Kind` field removes the "implicit shape" concern
that was the main argument in this alternative's favor.

**B. New `retired` lifecycle state instead of returning to `ready`.**
Rejected per Non-Goals — the design doc this project already follows
explicitly calls this out as unnecessary, and Shoal doesn't need to model
"permanently retired" since that's NetBox's job (role/status), not a
provisioning-tool concern.

**C. Have the guest (marker ISO) power itself off after `PREP_DONE` when
it detects no next stage, via a new kernel cmdline flag.** This is Key
Decision 2's rejected alternative. Rejected in favor of orchestrator-side
power-off to avoid a marker-ISO rebuild/republish dependency and keep the
guest's behavior identical regardless of what kind of job it's in.

**D. Delete the device's persistent `credential_ref` on deprovision.**
This was the *original* design (see Key Decision 4). Rejected on
verification: that ref is Shoal's own BMC access, not tenant data tied to
the wiped disk; deleting it breaks the device's next provision for no
actual security gain. Superseded by "deprovision deletes nothing."

## Security & Privacy Considerations

- `approve_destruct` is mandatory, no default-true path — matches
  `wipe_only`'s existing gate exactly, no new consent mechanism to design.
- **No credential deletion** (Key Decision 4, revised) — deprovision
  doesn't touch `secrets.Backend` at all. The device's BMC credential is
  infrastructure access that persists independent of whatever's installed
  on the disk, the same way it would if you reimaged a physical server by
  hand without ever touching its iDRAC password.
- No new secret ever transits a deprovision request — it resolves the
  device's *existing* `credential_ref` the same way `Start` already does
  via `applyStartBindings`, purely to authenticate the wipe job itself.
- Job logs / SOL markers for a wipe job are not more sensitive than an
  install job's — same Golden Rule 3 (secrets never in SOL/logs/LLM
  payloads) applies unchanged.

## Observability

- Existing `GET /v1/jobs/{id}`, `GET /v1/jobs/{id}/log`,
  `GET /v1/devices/{id}/jobs` all work unchanged for a deprovision job —
  it's a job like any other, just with a `prep`-only stage list.
- The new `Kind` field (Data Model Changes) means device-jobs list views
  show "Deprovisioned" vs "Provisioned" directly, no reverse-engineering
  from `stages[].kind == "prep"` needed.
- NetBox lifecycle history: `SetLifecycle`'s existing comments-field
  fallback is the only history mechanism today (the custom field only
  holds current state) — a deprovision-then-reprovision cycle is visible
  there the same way any lifecycle change already is, no new gap
  introduced.

## Rollout Plan

1. `ProvisioningJob` → `Job` rename (Key Decision 7) — mechanical, ships
   alone first, everything after builds on the renamed type.
2. Ephemeral job-credential cleanup (Key Decision 6) — touches every job
   kind, easiest to verify in isolation, independent of `Kind`.
3. Orchestrator: `Kind` field, `expandStages` single-stage branch keyed on
   it, relaxed `ISOURL` requirement for that shape, conditional
   `ready`-vs-`provisioned` terminal write-back, BMC power-off on
   no-next-stage `PREP_DONE`. Lab-verified against nested sushy first (fast
   iteration, matches this project's existing testing discipline).
4. CLI `deploy deprovision` verb.
5. NetBox plugin action + UI.
6. Real-hardware validation on the R750 this session already proved out —
   deprovision it, confirm `lifecycle_state=ready` in NetBox, confirm the
   disk is actually gone (mount check, same discipline as the Phase 7
   write-verification), confirm the device's stored (persistent)
   `credential_ref` is unchanged in NetBox, then re-provision it as a full
   round-trip test.

## Risks

- **`expandStages` change touches shared, load-bearing code.** Every
  existing `wipe_only` request today is guaranteed two stages
  (`stages_test.go` asserts `len(stages) == 2` throughout) — the new
  single-stage branch keys on `Kind == "deprovision"` (Key Decision 5), so
  it's strictly additive by construction (no existing caller has ever set
  `Kind`, since the field is new) rather than relying on inferring intent
  from which fields happen to be empty. Still needs explicit test coverage
  for "prep=wipe_only WITH `Kind` unset still produces 2 stages" alongside
  the new "`Kind=deprovision` produces 1 stage" case, to prove the branch
  really is additive and not just additive-in-theory.
- **Power-off-after-wipe race**: if the orchestrator issues a BMC
  power-off immediately on `PREP_DONE`, and the guest's own `/init` is
  still mid-heartbeat-loop (it doesn't know this job has no next stage —
  see Key Decision 2), need to confirm this doesn't race with
  `cleanupBMC`'s media-eject/boot-override-clear in a way that leaves the
  BMC session in a bad state (matches the kind of session-contention issue
  this project hit repeatedly with the real iDRAC this session — worth a
  live test pass specifically for this ordering, not just a lab pass).

## Open Questions

1. Should deprovision be reachable from `provisioning` (abort + wipe in
   one action), or only from terminal states (`provisioned`/`failed`) as
   scoped above? Scoped above assumes the latter — cancel first, then
   deprovision, as two explicit operator actions.
2. Any interest in a `wipe_level` stronger than `discard`/`zero` as part
   of this work, or is that explicitly out of scope (per Non-Goals) and a
   separate future document?
3. Now that `models.Job` (Key Decision 7) exists, is the `deploy` CLI verb
   namespace worth revisiting too (e.g. a generic `job` verb alongside or
   instead of `deploy`), or does `deploy deprovision` read fine enough
   that it's not worth the user-facing churn? Explicitly not decided by
   this document — flagging since Key Decision 7 makes the question more
   natural to ask than it was before.

## PR Plan

1. **PR1** — `models.ProvisioningJob` → `models.Job` rename (Key Decision
   7): pure mechanical rename across ~57 call sites, zero behavior change,
   `go build`/full existing test suite must pass byte-for-byte unchanged.
   Ships alone first, easiest possible PR to review (a diff that should
   contain nothing but the identifier change), and gives every subsequent
   PR the renamed type to build on from the start instead of renaming
   underneath work in progress.
2. **PR2** — ephemeral job-scoped credential cleanup (Key Decision 6):
   delete the `"job-"+ID` ref on any job's terminal state, gated on exact
   ref-name match. Independent of `Kind`/deprovision, de-risked in
   isolation before anything depends on it.
3. **PR3** — `models.StartJobRequest`/`Job.Kind` field (Key Decision 5) +
   `expandStages` single-stage branch keyed on `Kind == "deprovision"` +
   relaxed `ISOURL` check for that shape + regression coverage proving
   existing `wipe_only` callers (no `Kind` set) still produce 2 stages.
4. **PR4** — conditional `ready`/`provisioned` terminal write-back keyed
   on `Kind` + BMC power-off on terminal `PREP_DONE` with no next stage.
   Lab-verified.
5. **PR5** — CLI `deploy deprovision` verb.
6. **PR6** — NetBox plugin action, template, `client.py` call.
7. **PR7** — real-hardware round-trip verification (deprovision then
   re-provision the R750, confirm the persistent device `credential_ref`
   untouched throughout), doc closeout.
