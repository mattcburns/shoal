# Real-BMC provisioning progress

**Date:** 2026-08-24  
**Target:** Dell PowerEdge R750, iDRAC 7.30.10.50, BMC `172.16.21.202`, service tag `C784MH3` (NetBox device id `6`)  
**Operator path:** L0 `wg0` `172.16.20.138` ↔ BMC `172.16.21.202/28`

This is a field log, not a design rewrite. Design remains
`docs/sol-transports-design.md` and
`docs/real-hardware-sol-runbook.md`.

## UPDATE 2026-08-26: real root cause of the boot failures found — poller was authenticating with the wrong credentials

Two more real deprovision attempts against the R750 (`b74b50a0…`, `93fa9ab1…`)
reproduced the *same* symptom twice in a row: virtual CD inserted correctly,
boot override set correctly, firmware attempted the CD, and — new, not seen
before this session — `Boot Failed: Virtual Optical Drive`, falling through
to the already-installed disk OS. Confirmed via `BootProgress.LastState=OSRunning`:
the disk was never wiped either time.

Root cause, found in the iDRAC's own Lifecycle Controller log, not from any
Shoal log: continuous `Unable to log in for admin from 172.16.20.138 using
REDFISH` / `Login attempt alert ... IP will be blocked for 60 seconds`,
every 2–4 minutes, spanning both failure windows. `seedPollTargets`
(`internal/cli/cli.go`) seeded the background SEL/sensor/firmware/power
poller with the global `SHOAL_BMC_USERNAME`/`PASSWORD` (the lab's
`admin`/`password`) for **every** device, never resolving a job's own
`CredentialRef` — so every poll cycle against this real iDRAC (`root` +
real password via `bmc-C784MH3`) authenticated wrong, and the iDRAC's own
rate-limiter periodically blocked the source IP (`172.16.20.138`, this
workstation via wg0) — the exact IP every live job's SOL/media traffic also
comes from. This is the most likely real explanation for the TLS handshake
timeouts, total ping unreachability, and 401s chased across this entire
session as unexplained "iDRAC flakiness" or "session/rate hiccup" — it was
Shoal itself, self-inflicting a periodic denial of service against the BMC
it was trying to provision.

Fixed: `seedPollTargets` now resolves `CredentialRef` via the secrets
backend per job, falling back to the global default only when a job has
none (lab sushy nodes). New test `TestSeedPollTargetsUsesJobCredentialRef`
(confirmed it fails under the old behavior by mutation-testing back to it).
Deployed and verified live: first poll after redeploy succeeded cleanly
(`sel_new:5 sensors:38 firmware:21 failures:0`), and the failed-`admin`-login
spam — continuous every 2–4 min before the fix — stopped entirely after.

**Full round trip confirmed clean with the fix live:** deprovision
(`c7cd569aa6b781bddcd5804eef9bea32`, 04:06–04:12, `done_ok`) then re-provision
(`e807f37d3f7623a2508b6f4d1b37deb2`, 04:14–04:21, `done_ok`) — both via the
NetBox buttons, no auth errors, no boot failures, no false stalls. One benign
warning on the re-provision (`virtual media still inserted` at the DONE
post-check) did not block the `provisioned` transition — pre-existing, not
investigated further this session, worth a look if it recurs.

## UPDATE 2026-08-25 (later still): `POST /v1/jobs` no longer blocks on BMC bring-up

Removed the design wart behind the false-failure report below: the HTTP API
now returns as soon as the job row is durable, and callers poll
`GET /v1/jobs/{id}` for what happens next. Raising the client timeout only
made the symptom rarer; a request that blocks ~40s on hardware is the actual
problem.

`Orchestrator.Start` split into two halves:

- `prepareStart` — resolve, validate, store credentials, probe CD count,
  expand stages, insert the job row. Fast; on error no job row is left.
- `runStart` — the slow bring-up (SOL attach, media insert, boot override,
  power cycle) plus the existing failure bookkeeping.

`Start` = both, synchronous, **unchanged** — the CLI still needs it, because
its in-process orchestrator dies when the command returns (`deploy run`
without `-wait` would otherwise exit before the BMC was ever touched).
New `StartAsync` = `prepareStart` + `go runStart`, used by the API only.
Bring-up failures are not lost: `runStart` records them via `HandleTerminal`,
so they surface as a terminal state on the next poll.

Cancellation stays detached (`context.WithoutCancel`) independently of the
async split — that is what stops a client that gives up from aborting a start
mid-flight, and it is a *separate* hazard from blocking. Keeping both is
deliberate.

Verified live against the deployed binary using lab node 5 with an
instantly-refusing BMC endpoint (real R750 untouched): `HTTP 201` in **4.4s**
(server-side work 0.7s; the rest cold-start NetBox latency on the first
request after the container restart), and the background bring-up failure
landed correctly as `state=failed`. The NetBox Status tab needed no change —
it already auto-refreshes every 5s while a job is `provisioning`, which is now
the state the moment the POST returns.

Three new tests (`internal/deploy/job/start_async_test.go`) pin the contract:
returns before bring-up completes, bring-up failure becomes terminal state,
and caller cancellation does not abort bring-up. Confirmed they have teeth by
mutating `StartAsync` back to synchronous — the suite hangs rather than
passing. Full `go test ./...`, `go vet`, `gofmt`, and 39 plugin tests clean.

Note for whoever runs `-race`: `TestOrchestratorHappyPathDone` reports a data
race on the `netbox.Memory` fake's map (test reads `nb.BySerial[...]` while
the terminal handler writes it). **Pre-existing** — verified identical race
count on pristine HEAD with this work stashed. Not introduced here, and worth
fixing separately.

## UPDATE 2026-08-25 (later): NetBox Deprovision button — false failure; one real API bug + three lost `-e` settings

Operator reported "Deprovision failed" from the NetBox button four times.
**Three of the four were not what the UI said they were.** Final run
(`bc4ba2b3e69c8338f2da9f0a7c8ebdd0`) reported
`HTTPConnectionPool(host='host.docker.internal', port=8088): Read timed out
(read timeout=30)` and **still completed successfully**: `state=ready`,
8/8 markers, `reason=done_ok`, 6m49s; host `PowerState=Off`, boot override
cleared, both media slots ejected, NetBox `lifecycle_state=ready`.

Timeline that explains it: job created 22:07:30 → Django gives up 22:07:56
(30s) → SOL attached + media inserted **22:08:10 (39.4s)**. `POST /v1/jobs`
blocks until the first stage is actually running (NetBox resolve →
credentials → CD probe → SOL attach → media insert → boot override → power
cycle); that is ~40s on this R750, over the plugin's 30s default. The UI
even contradicted itself — the error banner sat directly above
`Active job bc4ba2b3e69c833…, Phase STARTING`.

**Real bug found (`internal/api/jobs.go`):** start ran as
`s.start.Start(r.Context(), req)`. `Orchestrator.Start` detaches its
*post-insert* work onto `context.Background()`, but everything before that
(`resolveDeviceID`, `secrets.Put`/`Get`, `probeCDCount` — a live Redfish
call — `store.Insert`, `syncNetBoxLifecycle`) ran on the HTTP request
context. A client timeout therefore **aborted an in-flight start at
whatever point it had reached**, which is exactly the 21:50 failure
(`netbox device resolve failed` → `jobstore: insert: context canceled`,
job never created). This is why the failures looked nondeterministic: the
outcome depended on which side of the 30s boundary the work was on. Fixed
with `context.WithoutCancel` — a disconnecting client can no longer abort
a start it already committed to. Note this hazard is *independent* of the
timeout value; raising the timeout alone would only have made it rarer.

**Meta-lesson — three settings had been applied via `-e` and evaporated.**
Each was "fixed" in an earlier session on the ansible command line and
never written to group_vars, so every later redeploy silently reverted it:

| Setting | Was | Effect of the loss |
|---|---|---|
| `shoal_netbox_plugin_request_timeout` | 30 (template default) | The false-failure regression above — *already fixed once*, per the 2026-08-24 update |
| `shoal_prep_iso_url` | unset | `validate: prep wipe_only requires prep_iso_url` — the first reported failure |
| `shoal_sol_debug_dir` | unset | **Raw SOL capture silently off** — the diagnostic that root-caused the boot-override and cold-start bugs was unavailable for every failure this session |

All three now persisted (`defaults.yml`; the two BMC-reachable URLs in
gitignored `vault.yml`, with a commented template in `vault.yml.example`),
plus a `compose_stack` task to create the SOL capture dir. **If a fix is
worth an `-e`, it is worth a group_vars entry** — otherwise it is a
regression waiting for the next redeploy.

Also fixed this session: NetBox deprovision never sent `stall_timeout`
(unlike the `start` action next to it), so it used the orchestrator's
generic 3m default instead of the 30m physical-role budget — job
`24c438d4…` failed at exactly `3m0s`. And `shoal_poll_watch_interval`
30s → 2m: the background SEL/sensor/firmware/power poll (4 Redfish calls)
runs at the *elevated* rate while a job holds a SOL watch, i.e. it hits
the same BMC hardest exactly when the job is using it.

Still unexplained: job `a9d21291…` died `reason=transport` ~11 min in, and
its cleanup then failed too, leaving the CD inserted (manually ejected).
Sustained `dial tcp 172.16.21.202:443: i/o timeout` around that window
suggests a genuine BMC/network stretch rather than the context bug, but
with SOL capture now enabled the next occurrence should leave evidence.

## UPDATE 2026-08-25: deprovision round trip PASSED (PR7, docs/deprovision-design.md)

First real-hardware run of `Kind=deprovision` (`internal/deploy/job` PRs 1-6,
`feature/real-bmc-provision`). Validated the prep-mode marker script locally
in QEMU first (scratch AHCI disk as `/dev/sda`, direct `-kernel`/`-initrd`
boot with `shoal.mode=prep shoal.target=/dev/sda`) — caught that the
existing `/tmp/shoal-iso/shoal-marker.iso` predated the `pack_modules()`
PATH/`modprobe` fixes (0 kernel modules packed, so `/dev/sda` never
appeared); rebuilt fresh via the current script and the local run then
passed clean (`blkdiscard` → `PREP_DONE` → heartbeat loop) before touching
the BMC.

**Deprovision** (job `caa016a63db7b378340ee3c61cf35761`): `SHOAL_INSTALL_MODE=prep
SHOAL_INSTALL_TARGET=/dev/sda SHOAL_PREP_WIPE_LEVEL=discard` ISO served at
`http://172.16.20.138:8080/shoal-prep.iso`; `shoal deploy deprovision
-device-id C784MH3 -wipe-level discard -approve-destruct -prep-iso-url
... -stall-timeout 15m`. `state=ready` in ~6 min (warm boot), 8/8 markers,
`reason=done_ok`. Verified independently via Redfish/NetBox: `blkdiscard`
ran against `/dev/sda`, `lifecycle_state=ready`, `credential_ref=bmc-C784MH3`
**unchanged**, host `PowerState=Off` (confirms orchestrator-issued `ForceOff`
on `PREP_DONE` — Key Decision 2 — not the guest's own heartbeat loop), boot
override cleared, both VirtualMedia slots ejected.

**Re-provision** (job `afbd958a1a8c8fd1843b86bb517b7969`): plain `shoal
deploy run` against the same device with the already-proven
`shoal-ubuntu-autoinstall.iso`, `-stall-timeout 30m` (cold boot this time,
host was off after deprovision — cold POST budget, not the 15m warm figure
above). `state=provisioned` in ~11m41s, 9/9 markers — matches the original
Phase 7a run almost exactly. One benign repeat of the known first-post-wipe-boot
hiccup (stale cached boot option after a fresh disk write —
`BootProgress` briefly went `None`/`PowerState=Off` mid-POST); self-corrected
on its own within seconds, same as the original Phase 7a note. Confirmed
`BootProgress.LastState=OSRunning` afterward. NetBox `lifecycle_state=provisioned`,
`credential_ref=bmc-C784MH3` still unchanged.

**Full round trip closed**: `provisioned` → (wipe) → `ready` → (reinstall) →
`provisioned`, OS confirmed running, BMC credential untouched throughout.
Small fix found along the way: `cmdDeployDeprovision` wasn't wiring
`Telemetry` into the orchestrator like `cmdDeployRun` does, so job_log lines
weren't persisted for a CLI-run deprovision job (job state/stages were fine
regardless — jobstore, not telemetry). Fixed.

## UPDATE 2026-08-24 (later same day): first clean end-to-end spike PASSED

Job `aed5014210f0c9313bc89edad9d3e20b`: `state=provisioned`, `reason=done_ok`,
`last_marker_seq=7`, NetBox synced to `lifecycle_state=provisioned`. SOL
attached ~25s after start; markers ran `BOOT → IMAGE_WRITE → VERIFY → DONE`
over ~6 minutes, inside the 12m stall window.

Root causes of all four prior stalls (`last_marker_seq=0` every time),
**both fixed in `infra/scripts/build-marker-iso.sh`** (uncommitted; synced to
`infra/ansible/roles/marker_iso/files/build-marker-iso.sh`):

1. **No UEFI boot catalog on the ISO.** It was isolinux/BIOS-only via a
   single `-b/-c` El Torito entry. This iDRAC's Redfish `Boot` object reports
   `BootSourceOverrideMode=UEFI`; a UEFI-mode CD boot with no UEFI boot image
   fails silently and falls through to the next boot device — matches the
   earlier-observed `Boot Failed: Ubuntu` → PXE. Fix: build a
   `grub-mkstandalone -O x86_64-efi` stub embedding a grub.cfg that
   `search`es for `/boot/vmlinuz` on the ISO9660 root and chainloads the same
   kernel/initrd/cmdline as isolinux, package it as a FAT image via `mtools`
   (no root/loop-mount needed), and add it as a second El Torito entry
   (`-eltorito-alt-boot -e boot/efiboot.img -no-emul-boot`). Requires
   `grub-efi-amd64-bin` + `mtools`; degrades to BIOS-only with a build
   warning if either is missing.
2. **Dynamically-linked busybox.** The initramfs packs only the busybox
   binary, no shared libs. A dynamic busybox's `/init` (`#!/bin/busybox sh`)
   can never exec → kernel panics `Failed to execute /init (error -2)`
   ~3s after boot, before the marker script runs at all — **on every boot
   path**, confirmed identically in local QEMU tests under both BIOS and
   UEFI before this fix. This alone explained the zero-marker pattern
   independent of the UEFI bug above; both had to be fixed together. Fix:
   require/prefer a statically-linked busybox (`busybox-static` package),
   hard error at build time if only a dynamic one is found (previously
   silent — no build error, just a dead ISO).

Both fixes were validated locally first: booted the rebuilt ISO in QEMU under
both SeaBIOS and OVMF (UEFI), full marker sequence to `DONE` in both, before
spending another live BMC cycle. That local repro loop (QEMU + OVMF, no real
hardware needed) is the fast path for any future marker-ISO boot regression —
don't burn a live iDRAC cycle to debug boot failures when QEMU reproduces the
same panic in seconds.

Remaining before this counts as "provisioning actually works": rerun a
second/third spike for repeatability, then move to Phase 7 (real OS image
write) per item 5 in "Still open" below — the simulate marker path is now
proven, the full write path is not.

## UPDATE 2026-08-24 (same day, later): tested via Shoal API and NetBox trigger

The successful run above used `deploy run` (CLI), which builds its own
in-process `job.Orchestrator` and never touches the HTTP API or NetBox. Used
the lab stack (already running on the L1 VM, `192.168.122.100`: NetBox
`:8000`, shoal-app `:8088`, sushy `:8001`) to test the other two trigger
paths against the *real* BMC (device already enrolled there as NetBox id 6).

**Shoal HTTP API (`POST /v1/jobs` on the lab's shoal-app) — works.**
Direct API call with explicit `system_id=System.Embedded.1`,
`serial_transport=redfish_sol`, and real BMC creds reached `WAITING_SOL`
(SOL attach + media insert + boot override + power cycle all succeeded) —
full parity with the CLI path through job start. One of two attempts then
failed shortly after `WAITING_SOL` with a generic `"bmc error"` and no log
lines; BMC state afterward was clean (powered on, no media inserted, boot
override cleared, 0 active Redfish sessions) — no stuck state. This came
after several rapid-fire real-BMC hits in a few minutes (two job attempts +
several read-only Redfish probes); plausible iDRAC session/rate hiccup
rather than a code bug, but **not confirmed** — worth an isolated single
retry (nothing else hitting the BMC concurrently) before ruling that out.

**NetBox trigger (Start button on the device's Shoal Status tab) — does
NOT currently work end-to-end**, for two stacked reasons:

1. The lab's deployed `netbox_shoal` plugin container is running the
   pre-session code — confirmed by inspecting the rendered form: no
   `serial_transport`/`credential_ref` fields exist, and `iso_url` still
   defaults to the nested-lab-only `http://192.168.124.1:8080/...` (not
   BMC-reachable). This session's plugin/backend fixes are local,
   uncommitted changes; nothing has been redeployed to the lab stack.
2. **New bug, independent of (1):** `_start_defaults()` in `views.py`
   defaults `system_id` to the NetBox device name (`instance.name`) —
   correct for the lab's sushy simulator (`System.Name` == the libvirt
   domain name there) but **wrong for a real iDRAC**, whose Redfish System
   resource is always `System.Embedded.1`, not the service tag. Submitting
   the Start form with `bmc_endpoint` correct (pre-filled fine) and
   `iso_url` manually corrected still failed fast with `"bmc error"`
   because of this — no real-hardware side effects (job failed before
   inserting media or setting boot override), but it means today, an
   operator clicking Start in NetBox against a real BMC device cannot
   succeed even with a manually-fixed ISO URL, until `system_id` is also
   fixed (either a plugin default for physical roles, or leaving it blank
   and letting the orchestrator's own default apply — need to check what
   the orchestrator does when `system_id` is empty, since the CLI/API tests
   above always passed it explicitly).

**Net: two more fixes needed before "click Start in NetBox" works on real
hardware:** deploy this session's plugin+backend changes to the lab stack,
and fix the `system_id` default in `netbox_shoal/views.py` for non-lab
(physical) device roles.

## UPDATE 2026-08-24 (same day, later still): system_id fixed + real root cause of remaining failures found

Fixed `system_id` in `defaults.py`/`views.py` (new `system_id(role_slug,
device_name)`: blank for physical roles so the orchestrator auto-resolves
the BMC's single System; device name only for lab sushy nodes, which need
an explicit match). 3 new unit tests, 11/11 passing.

Hot-patched the fix into the *running* lab NetBox container (`docker cp` the
two files into both `/opt/netbox-shoal/` and the installed venv
site-packages path, `docker restart shoal-netbox`) since the plugin is baked
into the `netbox-shoal:lab` image, not bind-mounted — a real rebuild would
need an ansible run. Also added `SHOAL_REAL_BMC_ISO_URL` to the live
`/opt/shoal/infra/netbox-config/plugins.py` on the lab VM (backed up as
`.bak`) so `iso_url` prefills correctly too. Verified live: device 6's
rendered Shoal Status form now shows `bmc_endpoint`, `iso_url`, and
`system_id` all correct with **zero manual overrides** — this is a real,
if host-local, deploy of the fix, not just a local diff.

Submitted that exact form (simulating a real "Start" click). Job
`6df036d1...` created correctly (`system_id` auto-resolved to
`System.Embedded.1`, `credential_ref` correctly pulled from NetBox's stored
`bmc-C784MH3`), reached `WAITING_SOL`, then failed with `"bmc error"` — same
shape as the earlier direct-API test (`d7c9e54...`) that I'd previously
chalked up to a possible rate-limit hiccup. **That theory was wrong.**

Checked `docker logs shoal-app` on the lab VM directly (SSH access:
`lab@192.168.122.100` via `~/.ssh/shoal_lab_vm`, ansible-provisioned key) and
found the real, deterministic cause:

```
register watch: sol: open transport: sol: redfish transport: open sol:
redfish: SOL unsupported for vendor "dell" (connect types: [])
  ...
  8. [probe] ok=false vendor=dell — no supported SOL path (native WS
     unsupported/failed, Redfish did not advertise SSH); observed connect
     types: []
```

**The lab's deployed `shoal-app` container is running a binary older than
PR #39** (`c6eba6d feat(sol): Dell SSH attach when SerialConsole is empty`,
already merged to `master`). It never attempts the SSH fallback that makes
SOL work on this iDRAC — the probe stops after both WebSocket candidates
fail and reports no path, exactly like Shoal's behavior *before* PR #39
landed. This explains every lab-routed failure today (both the direct
`POST /v1/jobs` test and the NetBox-triggered one): they all go through
this same stale binary. It is unrelated to the `system_id` bug or to any of
this session's uncommitted changes — the lab's `shoal-app` image is simply
several merged PRs behind current `master`.

**Only the CLI (`deploy run`, built fresh from the local working tree) has
ever actually succeeded end-to-end on this iDRAC.** The lab-hosted API and
NetBox paths are code-correct as of this session's fixes but are running
against stale compiled binaries — they need a real image rebuild + redeploy
(ansible) to prove out, not a hotfix. That's a bigger, more consequential
step than the Python hotfixes above and hasn't been done.

BMC state checked clean after both `WAITING_SOL`-then-`bmc error` failures:
powered on, boot override disabled, no media inserted, 0 active Redfish
sessions — no cleanup needed.

## UPDATE 2026-08-24 (same day, later still): full lab redeploy + two more real bugs fixed + one still-open failure

Redeployed the lab stack from current source via
`ansible-playbook -i infra/ansible/inventory/lab-vm.yml
infra/ansible/playbooks/lab_up.yml --tags config,shoal,start,wait,status,compose`
(the `compose_stack` role builds the Go binary from the controller's working
tree and re-stages the plugin source every run — confirmed this picks up
uncommitted changes with no need to commit first). This is the durable
deploy path; the earlier `docker cp` hotfix was a stopgap only.

**Two more real, confirmed bugs found and fixed:**

1. **`SHOAL_REQUEST_TIMEOUT` (plugin config, default 30s) was shorter than
   real SOL-attach + NetBox-resolve latency**, causing the Django view's
   `requests.post()` to abort client-side before shoal-app finished (or in
   one case, before it even inserted a job row —
   `"start job","err":"jobstore: insert: context canceled"`, i.e. the
   client's abort tore down the request context the Go handler was using).
   Bumped to 120s via `-e shoal_netbox_plugin_request_timeout=120`.
2. **`orchestrator`'s default `StallTimeout` (3 minutes,
   `DefaultSOLStall` in `orchestrator.go`) is too short for real BMC virtual
   media boot latency** and nothing in the plugin/API path was setting it
   (only the CLI's `-stall-timeout` flag did). Added
   `stall_timeout_ns(role_slug)` to `defaults.py` (blank/0 for lab nodes —
   let the fast orchestrator default apply; 15 minutes in nanoseconds for
   physical roles, matching the CLI's own bump for `image_write` jobs) and
   wired it into `_start_defaults`/`_handle_control_post` in `views.py`.

**Also fixed, independent of the above:** `InsertVirtualMedia` in
`internal/common/redfish/client.go` had a fast-path
(`if vm.Inserted && vm.Image == imageURL { return nil }`) that skipped
eject+reinsert whenever the BMC already reported the *same* URL inserted —
true for every repeat job against this device, since they all reuse
`shoal-marker.iso`. Theory: a stale BMC-side redirection session can persist
under `Inserted=true` with a matching URL after a completed/failed job;
skipping the refresh means the next job's boot override gets consumed (BIOS
attempts the CD) against a dead mount, producing zero markers with no
error anywhere. Removed the fast path — always eject-if-inserted, then
insert. **This did not fully explain the failures seen after fixing it**
(see below) but is still a correct, real fix on its own logic — kept.

**With all of the above deployed and verified individually correct** (11→14
plugin unit tests passing; `go build`/`go test` clean; ansible dry-run then
real run both clean), submitted the actual NetBox Start form (not a direct
API call — the real button, real CSRF/session, zero manual field overrides)
three more times against the real R750:

- Job `6df036d1...`: failed fast — this was *before* the lab redeploy,
  caught the pre-PR#39 stale binary (see previous update).
- Job `dce52843...`: reached `WAITING_SOL`, ran the full **15-minute**
  stall window, zero markers. This was *before* the `InsertVirtualMedia`
  fix — consistent with the stale-media-session theory.
- Job `d5f88ade...`: reached `WAITING_SOL`, ran the full 15-minute window
  again, zero markers — **this was *after* the `InsertVirtualMedia` fix**.
  Checked `ss -tn` on the workstation serving the ISO during the entire
  window: **two (later one) TCP connections from `172.16.21.202` to the
  ISO server stayed ESTABLISHED throughout** — the BMC's virtual-media
  redirection genuinely was pulling the ISO live, not a dead/stale
  connection. So the eject fix did what it was supposed to (a fresh,
  live redirection existed) and the job *still* produced zero markers.

**Net: the media-redirection-refresh theory is disproven as the (sole)
explanation.** Something else is preventing markers from appearing on
repeat attempts against this specific R750, despite: a proven-good ISO
(succeeded once via CLI + twice in local QEMU under both BIOS and UEFI), a
confirmed-live BMC-side media fetch, and a generous 15-minute budget. The
one and only success today (`aed50142...`, via CLI) was the *first* real
job of the session in a genuinely cold state. Every subsequent attempt —
regardless of which bugs were fixed in between — has failed with zero
markers, which points at something session/state-dependent on the iDRAC
itself (rapid repeated `ForceRestart` + boot-override cycles; today was
roughly a dozen real power cycles) rather than remaining Shoal-side bugs.

**Not yet done, and the clear next diagnostic step:** capture *raw* SOL
output during one of these failing boots (attach to SOL directly, not
through the marker-parsing watch layer) to see what the console actually
shows — stuck at BIOS POST, looping, booting disk/PXE instead of the CD, or
genuinely silent. This is original item 1 from "Still open" above and was
never actually completed; every fix since has been inferred from job
state/logs, not a direct look at the boot screen. Given ~12 real power
cycles on this box today, also worth considering a cooldown before the next
attempt in case this is iDRAC-side session/thermal fatigue rather than a
deterministic bug.

BMC left in a clean-ish state: powered on, boot override cleared, one
lingering Redfish session (likely expires on its own).

## UPDATE 2026-08-24 (same day, final): raw SOL capture — root cause found, mystery solved

Added `TestLiveMarkerBootCapture` to `internal/common/redfish/sol_live_test.go`
(`live_sol`-gated, kept as a reusable diagnostic): inserts the marker ISO
into every `SupportsCD` virtual media slot, sets the one-time CD override,
opens SOL *before* `ForceRestart`, then dumps raw (unfiltered by the
`SHOAL|` marker parser) console output for up to 9 minutes.

```bash
set -a && . ./.env && set +a
SHOAL_BMC_URL=https://172.16.21.202 \
SHOAL_ISO_URL=http://172.16.20.138:8080/shoal-marker.iso \
  go test ./internal/common/redfish -tags=live_sol -run TestLiveMarkerBootCapture -v -count=1 -timeout 12m
```

**Result: full marker sequence came through clean, `BOOT` → `DONE`.** But
the raw capture also showed *why* prior attempts stalled. Between BIOS POST
and the marker ISO ever getting a chance to boot, the console printed:

```
Lifecycle Controller: Applying Updates or Setting System Configuration.
Unified Server Configurator does not support console redirection.
```

...followed by a live-updating BIOS Configuration progress screen
(`BIOS Configuration (JID_876055629082)`). Checked the iDRAC's job queue
(`GET /redfish/v1/Managers/iDRAC.Embedded.1/Jobs`) for jobs started today:

```
JID_875532384062 | Configure: BIOS.Setup.1-1 | Completed | 2026-08-24T01:33:58
JID_875538676672 | Configure: BIOS.Setup.1-1 | Completed | 2026-08-24T01:44:27
... (11 total today, one per SetBootOverrideOnceCD call, all Completed)
JID_876055629082 | Configure: BIOS.Setup.1-1 | Completed | 2026-08-24T16:06:02
```

**Root cause, confirmed mechanistically, not a Shoal bug:** on this
R750/iDRAC9, writing the one-time boot override isn't an instant BIOS
variable set — it's staged as a Lifecycle Controller "Configure:
BIOS.Setup.1-1" job that gets *applied during the next POST* via the Unified
Server Configurator, and **SOL/console redirection is explicitly
unavailable while that job runs.** Every job start queues a fresh one of
these (one per `SetBootOverrideOnceCD` call — unavoidable, each job
legitimately needs its own one-time override). Duration is variable and
sometimes large enough to eat most or all of even a 15-minute stall budget
before the marker ISO ever prints a byte, independent of every other fix
made today (all of which were real and are still correct). This is the
actual explanation for the intermittent zero-marker stalls, not the
media-redirection theory (already shown live to be wrong) or anything
UEFI/busybox/system_id/timeout-related (all separately confirmed fixed).

**Recommendation, not yet implemented:** the SOL stall watcher currently
resets its timer only on parsed `SHOAL|` lines, by deliberate design (noted
elsewhere in this doc/runbook as a considered choice). Given this finding,
that choice is worth revisiting — resetting the stall timer on *any*
incoming SOL byte (not just markers) would let the watcher correctly treat
"the LC job is actively repainting a progress screen" as "still alive"
instead of silent, and would only genuinely stall when the console goes
truly quiet. That's a more targeted fix than further-inflating the stall
timeout, which just gambles on the LC job finishing in time rather than
reacting to what the console is actually doing. Not implemented this
session — flagging for the next pass.

## UPDATE 2026-08-24 (final): stall watcher now resets on any SOL activity, not just markers

Implemented the recommendation above. `internal/observe/sol/watch.go`'s
stall timer previously reset only when a line parsed as a `SHOAL|` marker —
by design, per the original tradeoff note in this doc/runbook. The raw SOL
capture showed why that's no longer the right tradeoff: Dell's Lifecycle
Controller / Unified Server Configurator BIOS-config screen repaints via
cursor-positioning escapes (`\x1b[row;colH...`) with few or no newline
characters for minutes at a time. A naive "reset on any *line*" fix isn't
enough either — `bufio.Scanner` (used by every transport) only yields a line
on `\n`, so a screen that never prints one would still starve a line-based
reset.

Fix, in two parts:

1. **`ActivityReporter` interface** (`transport.go`): an optional capability
   a `Transport` can implement — `Activity() <-chan struct{}`, fed by a new
   `activityReader` that wraps the underlying `io.Reader` and best-effort
   pings (non-blocking, may drop under backpressure) on *every raw byte
   read*, independent of whether `bufio.Scanner` has found a line yet. Kept
   deliberately separate from the `lines` channel — mixing pings into
   `lines` was the first approach tried and it broke
   `TestRedfishTransportOpenReadsLines`, which reasonably assumes every
   value from `lines` is real content (the channel's documented contract).
   Wired into all four transports: `ReaderTransport`, `LibvirtTransport`
   (`transport.go`), `RedfishTransport` (`redfish_transport.go` — the one
   that matters for real hardware), and `SSHLibvirtTransport`
   (`ssh_libvirt.go`).
2. **`watch.go`'s `run()`**: type-asserts `aw.trans` (already available, no
   signature change needed) for `ActivityReporter` and adds a
   `case <-activityC: resetStall()` arm to the select loop — nil-channel-safe
   (a transport that doesn't implement it just makes that case permanently
   non-ready, a correct no-op). Also moved the existing line-received
   `resetStall()` earlier so it fires on *any* received line, not only ones
   that parse as markers (belt-and-suspenders alongside `activityC`).
   Stall message text updated from `"no SOL marker for %s"` to
   `"no SOL activity for %s"` to match the new, more accurate semantics.

New regression test,
`TestWatchServiceActivityWithoutMarkersPreventsStall` in
`internal/observe/sol/watch_test.go`: feeds continuous no-newline,
non-marker bytes faster than the stall timeout for ~5 timeout-multiples,
asserts zero stalls, then goes silent and asserts a stall *does* still fire
— proving both halves (activity prevents a false stall; true silence still
stalls correctly, this isn't "never stall").

Verified: `gofmt`, `go vet ./...`, `go vet -tags=live_sol ./...`, and the
full `go test ./...` all clean. Redeployed to the lab stack via the same
`ansible-playbook ... lab_up.yml --tags config,shoal,start,wait,status,compose`
path (no real-BMC action — container rebuild only). Not yet re-verified
against the real R750 with a live job in this session; the fix is proven at
the unit-test level (exact reproduction of the failure condition found via
the raw SOL capture) and the underlying mechanism (SOL attach → media →
boot → markers) was already independently proven working multiple times
today. Next real-hardware run against this device is a good opportunity to
confirm end-to-end, but isn't required to trust this change — it's a
narrowly-scoped, well-tested fix for a concretely diagnosed problem, not a
speculative one.

## UPDATE 2026-08-24 (truly final): live-confirmed the stall fix works — and found a separate, still-open problem

Ran one more real NetBox Start against the R750 (job `566a2e90...`) to
confirm the activity-based stall fix live, not just at the unit-test level.

**The fix itself is proven correct, live:**

- Old behavior: would have failed at 15 minutes with `"no SOL marker for
  15m0s"`, full stop, regardless of what the console was doing.
- New behavior, observed: the job ran `WAITING_SOL` for **~24 minutes**
  past start with zero markers — well past the old 15-minute boundary —
  without falsely stalling, because *something* on the console kept
  producing activity. This job's own `Configure: BIOS.Setup.1-1` LC job
  (`JID_876087610700`) actually completed in under a minute this time (not
  the multi-minute delay from the earlier raw capture), so whatever kept it
  alive for 24 minutes was not that same LC-job mechanism — some other
  activity. **Then it went genuinely silent, and correctly stalled**:
  `"no SOL activity for 15m0s"`, 15 minutes after the last real byte, not
  15 minutes after job start. Exactly the intended behavior — no false
  stall during real activity, a true stall once things actually went quiet.
- BMC checked clean afterward: powered on, boot override consumed/cleared,
  0 active Redfish sessions. No cleanup needed.

**But the underlying job still failed to reach `DONE`.** This is a
*different*, not-yet-diagnosed problem from anything fixed today: ~24
minutes of unexplained console activity (not the LC job this time) that
never resulted in a `SHOAL|` marker, on a device/ISO/BMC combination that
has independently succeeded multiple times today in well under 10 minutes.
Boot override showed `Disabled/None` afterward (consumed, meaning BIOS did
cycle through the one-time CD boot at some point) and virtual media stayed
`Inserted=true` with the correct URL, so it's not an obviously failed
insert/override — but there's no way to know *what* those 24 minutes of
activity actually were without a raw SOL capture running concurrently,
which wasn't done for this run (starting one would have contended with the
job's own active SOL session).

**Net for today:** the specific ask ("make the stall-detection change
solid") is done and live-verified — it behaves correctly in both
directions.

## UPDATE 2026-08-24 (evening): failure mechanism found — the boot override never wins; boot order fall-through was carrying every success

Added `SHOAL_SOL_DEBUG_DIR` (raw per-session SOL byte capture inside the
transports themselves — `activityReader` tees every byte to a file; wired
into all real transports, compose template exposes it via
`shoal_sol_debug_dir`, mounted at `/var/lib/shoal/sol-debug`). This lets a
*real job* record its own console, which an operator cannot otherwise do —
the job holds the SOL session.

**First-ever full NetBox-button end-to-end success on real hardware:** job
`bf119676` → `provisioned`, 7 markers, ~6.9 min. And its capture exposed
the true mechanics: the console shows `Booting from Ubuntu` → `Boot
Failed: Ubuntu` → `PXE Device 1` → `PXE-E18: Server response timeout` →
`Boot Failed: PXE` → `Booting from Virtual Network File 2` → markers.
**The one-time CD override has never once won on this iDRAC** (`UsbCd`
absent from AllowableValues; `Cd` PATCH reads back `Disabled/None`; the
LC BIOS.Setup job it queues applies nothing effective for that boot).
Every "success" was the host walking its normal boot order and reaching
the virtual optical *third*, gated on PXE failing fast. Every mysterious
"activity but no markers" stall was PXE retrying slowly. Mystery solved.

**Fix implemented:** `dellOneTimeVirtualCD()` in
`internal/common/redfish/client.go` — Dell OEM one-time boot via manager
attributes `ServerBoot.1.FirstBootDevice=VCD-DVD` + `BootOnce=Enabled`
(the racadm mechanism; iDRAC-native, no LC BIOS job, targets the virtual
CD explicitly). Verified accepted live: PATCH → HTTP 200, instant
readback. `SetBootOverrideOnceCD` uses it first for Dell, falling back to
the standard PATCH. Bonus: no LC config job means no
console-redirection-disabled USC screen during POST.

**Next live run (job `e0e4ec24`) failed differently and revealed a second
independent variable: cold vs warm start.** Tabulating all runs: every
success began with `ForceRestart` from PowerState=On; both hard failures
began from Off via the orchestrator's `Power On` fallback. This run:
console silent right after `Loading Lifecycle Controller Drivers...Done`
(395 bytes, ~2 min), then nothing for 13+ min → stall. Afterward the host
was **powered Off with `BootOnce` consumed** — i.e. the VCD-DVD boot very
likely *did* happen and the marker init *did* run and power off, later
than the stall window, or its output never reached the SOL bridge. Cold
POST on an R750 does memory training / LC init with long console-silent
stretches; the leading hypothesis is cold boot simply takes longer than
the stall budget (with a possible secondary question of whether SOL
output survives the cold path at all).

**In flight:** `TestLiveMarkerBootCapture` extended (`ForceRestart`→`On`
fallback mirroring the orchestrator; `SHOAL_CAPTURE_MINUTES` env) and
running a 25-minute cold-start capture with VCD-DVD staged to settle it.

## UPDATE 2026-08-24 (night): SOLVED — cold-start proof passed, all failure modes explained

The 25-minute cold capture settled everything:

- **VCD-DVD boot override works**: console showed `Booting...` →
  `Booting from Virtual Optical Drive` → full marker sequence. No
  `Boot Failed: Ubuntu`, no PXE — first time the override ever won on
  this box.
- **Cold POST took ~25 minutes** in that capture — but that was the LC
  *backlog draining*: the old Boot-PATCH path had queued one
  `BIOS.Setup.1-1` LC job per attempt (11 that day), and cold POST
  cycled through "Applying Updates or Setting System Configuration"
  repeatedly to apply them. The VCD-DVD path queues none.

Raised the plugin's physical-role stall budget 15m → 30m (safe: the
activity-based watch only counts true console silence), redeployed, and
ran the **final proof: NetBox Start button, cold start (host Off), VCD-DVD
path** — the exact combination that had failed every prior time. Job
`30d8c14e...`: **`provisioned` in 7.3 minutes**, 7/7 markers, capture
confirms direct `Booting from Virtual Optical Drive`. With no LC backlog,
cold boot is no slower than warm.

**Failure-mode ledger, all closed:**
| Symptom | Cause | Fix |
|---|---|---|
| Zero markers ever (early runs) | ISO BIOS-only on UEFI host; dynamic busybox panicking /init | UEFI El Torito + static busybox |
| Boot override "worked" but boot went disk→PXE→media | Standard Boot PATCH never effective on iDRAC9 (UsbCd unsupported, Cd reads back Disabled); successes were boot-order fall-through gated on fast PXE failure | Dell OEM `ServerBoot.1.FirstBootDevice=VCD-DVD` + `BootOnce` (`dellOneTimeVirtualCD`) |
| Intermittent "activity but no markers" stalls | Slow PXE retry cycles during fall-through | Same VCD-DVD fix (no fall-through anymore) |
| Stall during LC config screen | Watch only reset on markers; LC repaints with no newlines | Activity-based stall reset (`ActivityReporter`) |
| Cold-start jobs dying at 15m | LC job backlog made cold POST take ~25m | VCD-DVD queues no LC jobs + 30m physical stall budget |

Remaining known-good state: warm NetBox run 6.9 min, cold NetBox run
7.3 min, CLI run 6.5 min — three trigger paths, both power states.
Phase 7 (real OS image write) is the next frontier; the transport and
boot plumbing under it is now solid.

## UPDATE 2026-08-24 (night, Phase 7): real Ubuntu install — three more bugs found and fixed, all via local QEMU repro first

Went after Phase 7a (real OS write, not just `simulate` markers) against the
real R750: downloaded Ubuntu 24.04 cloud image, built the customized
payload (`prepare-ubuntu-cloud-payload.sh`: hostname `shoal-r750`, user
`ubuntu`, dual-UART GRUB console), built the autoinstall marker ISO
(`SHOAL_INSTALL_MODE=autoinstall`, target `/dev/sda` -- the R750's BOSS-S2
SATA boot module, confirmed via Redfish `Storage` before touching anything:
`CPU.1` is 2x1.92TB NVMe data drives, `AHCI.SL.6-1`/BOSS-S2 is 2x480GB SATA,
non-RAID pass-through -- BOSS is Dell's dedicated OS-boot device, matching
the disk with the existing `Boot Failed: Ubuntu` entry). User confirmed OK
to wipe that disk before any of this started.

**Every simulate-mode run all day never actually exercised the
mount-the-CD-from-inside-Linux path** -- markers print without ever
touching `/payload`. First real write-mode attempt exposed that the whole
module-loading path was silently broken, in three layers, none surfaced
by four weeks of simulate-only testing:

1. **`modinfo`/`modprobe` (kmod) not on `PATH`.** They live in `/usr/sbin`,
   which this environment's shell doesn't have. `pack_modules()` calls them
   with `2>/dev/null || true`, tolerating a builtin-only kernel -- so it
   silently packed **0 modules**, every single build, all day. Harmless for
   simulate (never mounts the CD from Linux); would have silently no-op'd a
   real disk write. Fixed: `build-marker-iso.sh` now prepends
   `/usr/sbin:/sbin:/usr/local/sbin` to its own `PATH`, and hard-fails the
   build for any non-simulate mode if 0 modules got packed (previously
   silent).
2. **Modules are `.ko.xz`, but `/init`'s hardcoded `insmod` paths were bare
   `.ko`.** Once (1) was fixed, `pack_modules()` correctly found and copied
   the `.ko.xz` files -- but busybox's `insmod` applet can't decompress
   `.xz`, and the copies kept their compressed name, so `/init` failed with
   "No such file". Fixed: decompress at pack time (`xz -dc`, `zstd -dc`, or
   `gzip -dc` as needed) so the initramfs carries plain `.ko` files matching
   what `/init` expects.
3. **The real one: `ahci`/`libata` need a much deeper dependency chain than
   the hand-written `insmod` list assumed, and two of the modules needed
   aren't dependencies of `ahci` at all.** Confirmed via a from-scratch
   local repro (extracted the built kernel+initrd, booted directly under
   QEMU with a custom diagnostic `/init` -- no real hardware touched):
   `insmod libata.ko` alone failed "unknown symbol" because it needs
   `scsi_mod`+`scsi_common` first, and even with the *entire* transport
   chain loaded correctly (confirmed via `lsmod`) and the kernel logging
   `scsi 0:0:0:0: CD-ROM ...` / `scsi 1:0:0:0: Direct-Access ...`, **no
   block device ever appeared** -- `sr_mod` and `sd_mod`, the SCSI class
   drivers that actually create `/dev/sr*`/`/dev/sd*`, were never requested
   anywhere; they aren't dependencies of `ahci`, they bind to what `ahci`+
   `libata` expose. Fixed properly rather than patched around: replaced the
   hand-ordered `insmod` lists in `load_mods()`/`mount_cd()` with
   `modprobe` (busybox's applet, which correctly walks the `modules.dep`
   `depmod` already generates) for `ahci sr_mod sd_mod isofs virtio_blk`,
   and added `sr_mod`/`sd_mod` to `pack_modules()`'s requested-module list
   so their dependency chains get packed too (10 modules packed now, up
   from 0).

**Validated the actual write mechanism end-to-end locally before any of
this reached the BMC**: QEMU with an AHCI-attached scratch disk as
`/dev/sda` (matching the real R750's controller type), first with an 80MB
synthetic payload (`md5sum` of what landed on the emulated disk == `md5sum`
of the source raw image, byte-for-byte), then with the **actual 4.5GB
Ubuntu payload** -- `IMAGE_WRITE` progressed via heartbeats to 100%,
`VERIFY`/`POSTINSTALL`/`DONE`, ~4 minutes total. Mounted the resulting disk
image afterward and confirmed: valid ext4 rootfs, correct hostname, `ubuntu`
user present.

**Fourth bug, found by that same disk inspection:** `/boot/grub/grub.cfg`
was completely unmodified -- our `console=ttyS1` addition never took
effect, because Ubuntu 24.04 cloud images ship `/boot` as its own partition
(`LABEL=BOOT`, separate from rootfs and the ESP), and the script's
`chroot ... update-grub` call had nothing bind-mounted at `/boot` inside
that chroot (nor `/proc`/`/sys`/`/dev`, which `grub-probe` needs) -- it
silently no-op'd. The ESP's `grub.cfg` (`LABEL=UEFI`) is confirmed to be
just a two-line `configfile` chainload to the real one; no separate edit
needed there. Fixed: `prepare-ubuntu-cloud-payload.sh` now mounts the
`BOOT`-labeled partition directly and `sed`-edits its `grub.cfg` in place
(both the normal and recovery menu entries) instead of trying to regenerate
it via a broken chroot. Rebuilt the payload and reran the full QEMU
validation: `console=ttyS1,115200n8` confirmed present on both `linux`
lines in the written disk's actual `grub.cfg`.

All four fixes went through the same discipline as the earlier SOL work:
reproduce locally (QEMU, in this case a from-scratch kernel+initrd boot
with a custom diagnostic init, since the failure was silent with no error
anywhere) before spending a live BMC cycle. `build-marker-iso.sh` synced to
`infra/ansible/roles/marker_iso/files/`; `prepare-ubuntu-cloud-payload.sh`
has no ansible-role copy to sync.

**First live run failed -- a fifth bug, this one only visible on real
hardware:** `cd mount fail devs=sda sdb sda sdb merr= modprobe=`. The two
BOSS-S2 SATA disks were found correctly (`sda sdb`) but **no CD device at
all**. Root cause: Dell iDRAC (like most real BMCs) presents Virtual Media
as a **USB mass-storage device**, not SATA/AHCI -- QEMU's `-cdrom` (used
for every local validation so far) attaches via AHCI/IDE by default, which
is why local QEMU testing never caught this even though it exercised the
same module-loading code path successfully. BMC checked clean after the
failure: powered on, no boot override, media ejected -- safe, no cleanup
needed.

Fixed: added `xhci_pci`/`ehci_pci` (USB host controllers) and
`usb_storage`/`uas` (USB mass-storage class drivers, bulk-only and USB
Attached SCSI respectively -- covers either mode a BMC might use) to
`pack_modules()`'s requested list and to the `modprobe` calls in
`load_mods()`/`mount_cd()` (18 modules packed now, up from 10). This time
validated locally with a QEMU rig that actually matches the real
topology -- CD attached via `usb-storage` on a `qemu-xhci` controller,
scratch disk via AHCI, exactly like the real R750 -- using `-kernel`/
`-initrd` to boot the built image's own kernel+initrd directly (bypassing
SeaBIOS's USB-boot-order limitations, which are a QEMU firmware quirk
unrelated to the actual bug: on real hardware the firmware already
proved capable of booting the virtual USB CD, since markers reached
`BOOT` in every failed run today -- the failure was always in the
in-Linux mount step, after boot, which is what this rig actually tests).
Both the small synthetic payload (byte-exact checksum) and the full
4.5GB Ubuntu payload passed clean through this corrected rig before
retrying real hardware.

**Second live run: SUCCESS.** `state=provisioned`, `last_marker_seq=9`,
full `IMAGE_WRITE → VERIFY → POSTINSTALL → DONE → reboot`, ~11.5 minutes
total. Real Ubuntu 24.04, customized (hostname `shoal-r750`, user `ubuntu`),
written to the R750's BOSS-S2 boot disk (`/dev/sda`) and rebooted.

**Post-install boot took one extra wrinkle to confirm, but resolved
cleanly.** The very first post-install boot hit `Booting from AHCI
Controller in SL 6: EFI Fixed Disk Boot Device 1` -> `Reset System` --
the *generic* UEFI boot entry (no specific file path) rather than the
disk's own named `Boot0002 "Unavailable: Ubuntu"` entry, which Dell's
firmware had cached as unavailable from *before* today's wipe (stale GPT
partition UUID). Confirmed the ESP itself was fine the whole time --
`\EFI\BOOT\BOOTX64.EFI` (the standard fallback), `\EFI\ubuntu\shimx64.efi`,
and `grubx64.efi` all present and correct on the written disk, checked by
mounting the payload image directly. Issued one more clean `ForceRestart`
via Redfish and captured the full cycle from a known start: firmware
re-enumerated boot options (`Enumerating Boot options... Done`), this time
correctly resolved `Boot0002` and booted `AHCI Controller in SL 6: Ubuntu`
(the *named* entry) straight through -- GRUB, then the kernel's own EFI
stub (`Measured initrd data into PCR 9`, confirming TPM measured boot is
active on this box). One quiet SOL attach later showed nothing new (a
live SOL feed shows no scrollback, so an idle login prompt looks silent to
a fresh attach) -- confirmed definitively instead via Redfish's own
`BootProgress.LastState`, which the iDRAC only ever reports once the OS
itself has signaled it's up:

```
"BootProgress": { "LastState": "OSRunning", "LastStateTime": "2026-08-25T00:36:49-05:00" }
```

**Real Ubuntu 24.04, installed by Shoal's own write pipeline against a
real Dell PowerEdge R750, is running.** The one-time stale-boot-option
hiccup self-corrected on the very next POST cycle with no code change
needed -- a plain Redfish `ForceRestart`, nothing special -- and is most
likely specific to a *first* install onto a disk that previously held a
different OS (stale NVRAM cache of that old install's boot health). Worth
knowing about for the next fresh-wipe install on real hardware, not
necessarily worth engineering around given it corrected itself immediately.

Added `TestLiveConsoleTail` (`live_sol`-gated,
`internal/common/redfish/sol_live_test.go`) alongside
`TestLiveMarkerBootCapture` during this diagnosis: a passive, read-only,
continuously-accumulating SOL watch (no Reset, no virtual-media change) for
checking on an already-running host without disturbing it -- useful beyond
today, e.g. confirming an install actually came up before handing a box
back to an operator.

## Phase 7 summary

Five real bugs found and fixed, all reproduced locally before spending a
live BMC cycle where that was possible (four of five; the fifth --
USB vs. AHCI virtual-media attachment -- only diverges from local QEMU
testing on real hardware, discovered live and then re-validated locally
with a corrected QEMU rig before the successful retry): PATH-starved
module packing, compressed vs. bare `.ko` module names, an incomplete
hand-ordered `insmod` dependency chain (missing `libata`/`sr_mod`/`sd_mod`),
a `chroot update-grub` that silently no-op'd against a separate `/boot`
partition, and USB (not AHCI) virtual-media attachment on real Dell BMCs.
Phase 7a (real OS install to real hardware, not just the nested lab) is
now proven end-to-end: written, booted, and confirmed running via the
BMC's own authoritative status. Overall end-to-end reliability on this specific R750 is still
not 100%: multiple clean successes today, multiple failures with
increasingly well-understood (and now mostly fixed) causes, and this one
remaining failure mode that's real but not yet diagnosed. The tool for
diagnosing it already exists (`TestLiveMarkerBootCapture`, added earlier
today) — the next step is running it back-to-back with an API/NetBox job
(or just relying on the CLI, which threads its own SOL watch through the
same code and could be extended to dump raw bytes) to actually see what a
~20+-minute "alive but no markers" stretch looks like on the console.

## Short answer (original, before the fix above)

**Plumbing works. End-to-end provision does not.**

Shoal can attach SOL, the iDRAC can fetch and insert a marker ISO over HTTP,
and a Deploy job starts (watch registered, media attached, NetBox
`lifecycle_state=provisioning`). Three spike jobs then **stalled** with
`last_marker_seq=0` — no `SHOAL|` markers, so the job never reached
`provisioned`. (Superseded — see UPDATE above.)

## What landed in git (merged)

| PR | What |
|----|------|
| [#37](https://github.com/mattcburns/shoal/pull/37) | NetBox demo polish: poll, power, credentials, firmware; physical vs lab BMC |
| [#38](https://github.com/mattcburns/shoal/pull/38) | SOL transports design (`docs/sol-transports-design.md`) |
| [#39](https://github.com/mattcburns/shoal/pull/39) | Golden Rule 6 split; Dell SSH attach when `SerialConsole` is empty; WS sniff |
| [#40](https://github.com/mattcburns/shoal/pull/40) | Stdlib IPMI 2.0 SOL last-resort (`internal/common/redfish/internal/ipmi`) |

The **console transport design is implemented** (WS sniff → SSH → IPMI 3 then 17).
Telnet, HPE `vsp`/`textcons`, ATEN SMASH-CLP, and SSH host-key pinning remain
non-goals.

Uncommitted on `feature/real-bmc-provision` (when this note was written):
Start uses stored `credential_ref` when user/pass are empty; HTTPS BMC infers
`redfish_sol`; NetBox Start sends `redfish_sol` + `credential_ref` for physical
servers; Dell one-time boot prefers `UsbCd`; both virtual-CD slots get the
install ISO; marker ISO fans stdout to `ttyS0` and `ttyS1`.

## Proven live on this iDRAC

| Probe | Result |
|-------|--------|
| Redfish `SerialConsole` | Empty (`ConnectTypesSupported=[]`) |
| SSH eligibility | `NetworkProtocol.SSH` port 22 + OEM `SerialRedirection.1.Enable=Enabled` |
| SSH auth | `keyboard-interactive` (not `password`) |
| Attach | `session.Start("console com2")` → `Kind=ssh` `vendor=dell` |
| WebSocket `/console` | HTTP 200, not a 101 upgrade — correctly falls through |
| WebSocket `/wsman/virtualconsole` | HTTP 404 |
| BIOS POST over SOL | After `ForceRestart`: BIOS 1.15.2, F2/F10/F11/F12, then `Boot Failed: Ubuntu` → PXE |
| Virtual Media insert | **Succeeded** for `http://172.16.20.138:8080/shoal-marker.iso` (Range-capable Go `FileServer` on `wg0:8080`) |
| Job start | SOL watch `sol_kind=ssh`, media attached, NetBox `provisioning` |

ISO HTTP from the nested lab (`http://192.168.124.1:8080/…`) is **not**
BMC-reachable. Serve on the management path the BMC shares with this
workstation.

IPMI UDP/623 is still **filtered** from this workstation. IPMI SOL is in
tree for SuperMicro-class BMCs; it cannot attach here.

## Failed live: spike provision

Command shape (credentials from gitignored `.env`, never logged):

```bash
go run ./cmd/shoal deploy run \
  -device-id C784MH3 \
  -bmc-url https://172.16.21.202 \
  -serial-transport redfish_sol \
  -iso-url http://172.16.20.138:8080/shoal-marker.iso \
  -stall-timeout 12m
```

| Job id (prefix) | Outcome |
|-----------------|---------|
| `5dbbe5f2…` | `failed` / `sol stall` / `last_marker_seq=0` (10m) |
| `0de8627d…` | same (12m, ISO rebuilt with `console=ttyS1`) |
| `fc771697…` | same (12m, `UsbCd` boot + ISO on both CD slots + tee to ttyS0/ttyS1) |

Observe resets the stall timer **only** on parsed `SHOAL|` lines, not BIOS
text. BIOS POST on SOL therefore does not keep the job alive. No markers
means either:

1. The host did not boot the virtual CD (disk/PXE instead — last watched POST
   ended `Boot Failed: Ubuntu` then PXE), or
2. The live image still is not printing on the UART iDRAC SOL is bridging.

The 13 MiB lab `shoal-marker.iso` is the **simulate** marker image, not a
full OS write. A stall does not imply a disk wipe.

## What “wired” means now

| Path | Behavior |
|------|----------|
| Empty Start user/pass | NetBox `credential_ref` if that secret exists in *this* process; else `SHOAL_BMC_*` |
| HTTPS BMC URL | Infers `serial_transport=redfish_sol` (lab sushy HTTP stays `libvirt`) |
| NetBox Start on a physical server | POSTs `redfish_sol` + `credential_ref`; ISO prefill `SHOAL_REAL_BMC_ISO_URL` when set |
| CLI on a machine without the stored secret | Must pass BMC user/pass (env flags). Do not force a NetBox ref whose secret is missing |

Lab sushy + libvirt SOL is unchanged.

## Still open (to actually provision)

1. **Close the marker loop** — confirm one-time `UsbCd`/`Cd` actually booted the
   virtual CD (Redfish boot override + next POST), and/or log raw SOL during
   boot to see PXE vs CD vs kernel console.
2. **Publish the dual-UART marker ISO** into the serve dir the BMC uses
   (`http://172.16.20.138:8080/shoal-marker.iso` after rebuild).
3. **NetBox plugin config** — set `SHOAL_REAL_BMC_ISO_URL` (Ansible
   `shoal_netbox_plugin_real_bmc_iso_url`) so Status → Start prefills a
   BMC-reachable URL. Needs a plugin/config rollout, not just the Go binary.
4. **ISO server** — keep a Range-capable HTTP listener on `wg0:8080` for the
   duration of the job (plain HTTP, Golden Rule 7).
5. **Full OS install** (Phase 7) is separate: cloud-image write ISO, much
   larger media, longer stall. Do not attempt until the simulate marker job
   hits `DONE`.

## Useful commands

```bash
# SOL attach only (no power)
set -a && . ./.env && set +a
SHOAL_BMC_URL=https://172.16.21.202 \
  go test ./internal/common/redfish -tags=live_sol -run 'TestLiveOpenSOL$' -v -count=1 -timeout 60s

# Virtual Media insert + eject (needs ISO server up)
SHOAL_BMC_URL=https://172.16.21.202 \
SHOAL_ISO_URL=http://172.16.20.138:8080/shoal-marker.iso \
  go test ./internal/common/redfish -tags=live_sol -run TestLiveVirtualMediaISO -v -count=1 -timeout 3m

# Serve ISO (Range support)
# go run /tmp/shoal-iso/serve.go   # binds 172.16.20.138:8080, dir /tmp/shoal-iso
```

CI must not use `-tags=live_sol`.
