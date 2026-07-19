# Real-hardware Redfish SOL runbook

`internal/common/redfish/sol.go` (`BMC.OpenSOL`) and the `redfish_sol` serial
transport (`internal/observe/sol/redfish_transport.go`,
`internal/observe/sol/factory.go`) are **unit-tested only** — sushy-tools
(the lab BMC simulator) has no SOL support at all, so nothing in this repo's
automated test suite proves this works against a real BMC. This doc is the
checklist for the first time a human runs it against real hardware.

Read this before pointing `redfish_sol` at a real BMC, and update it (or the
candidate list in `sol.go`) with what you learn — the initial WebSocket
candidate URLs are **unverified guesses**, not documented vendor APIs.

## What's implemented, and how

- **Redfish only.** No IPMI (`ipmitool`) is ever used, regardless of what a
  BMC advertises. This is a hard project rule, not a v1 limitation.
- **Discovery order** (`OpenSOL`): classify the BMC vendor from
  `Manufacturer`/`Model`/manager hints (Dell, Supermicro recognized; same
  `detectVendor` used by OEM screenshot capture) → try native WebSocket SOL
  candidates for that vendor → if that's unsupported or fails, check whether
  Redfish's own capability metadata
  (`ComputerSystem.SerialConsole.SSH.ServiceEnabled` /
  `Manager.SerialConsole`) advertises SSH, and if so, open an SSH session
  (password auth, BMC credentials) using the `ConsoleEntryCommand` Redfish
  itself provides when present → otherwise, `*redfish.SOLUnsupportedError`
  with a full debug trail.
- **Telnet is never attempted** (deferred; a Telnet-only BMC is reported as
  unsupported, not implemented).
- **WebSocket candidates are placeholders.** No vendor publishes a documented
  client-pull plain-text SOL-over-WebSocket endpoint. Real iDRAC/Supermicro
  console redirection is typically an HTML5/binary KVM protocol, not
  line-oriented SOL — the candidates in `solWSCandidates` (`sol.go`) are
  best-effort guesses in the same spirit as the OEM screenshot-capture
  candidate list, and are very likely to fail until someone finds the real
  endpoint on real firmware (see "What to do when it fails" below).

## Prerequisites

- A real BMC reachable from the Shoal host, ideally Dell iDRAC or Supermicro
  first (matches the vendor detection already wired up).
- `SHOAL_REDFISH_TLS_MODE` set appropriately for that BMC's certificate
  (`off` | `insecure` | `custom_ca` — see AGENTS.md §3.3 / §7 for the full
  matrix). Most real BMCs use self-signed certs; `insecure` is the common
  starting point for a first test, not a production recommendation.
- `SHOAL_REDFISH_AUTH_MODE=basic` — the native WebSocket SOL path in this
  slice only supports basic auth (`session` mode dials are explicitly
  skipped with a debug-trail note, not silently attempted).
- BMC credentials via `SHOAL_BMC_USERNAME` / `SHOAL_BMC_PASSWORD`, or a
  per-job `bmc_username`/`bmc_password` on `StartJobRequest` (stored to the
  secrets backend as usual).

## How to opt in

Either set the orchestrator-wide default:

```bash
export SHOAL_SERIAL_TRANSPORT=redfish_sol
```

or override per job on `StartJobRequest`:

```json
{ "serial_transport": "redfish_sol", "bmc_endpoint": "https://<real-bmc>", ... }
```

Note: `validate.StartJobRequest` cannot see the config-wide default (it only
validates the request as submitted), so **`serial_target` is still required
on the request** even when relying purely on `SHOAL_SERIAL_TRANSPORT`. The
orchestrator ignores `serial_target` once `redfish_sol` is selected — the
watch's `Target` becomes `bmc_endpoint` instead. This is a deliberate v1
tradeoff (safe/explicit over clever); don't "fix" it by threading config into
`validate` without discussing the tradeoff first.

## Step-by-step per-vendor checklist

1. Start a job against the real BMC with `redfish_sol` selected (see above).
2. Check the Shoal logs for `sol watch registered` with
   `target=<bmc_endpoint>` — confirms `WatchSession.Transport == "redfish_sol"`
   reached the watch service.
3. If the job proceeds normally (SOL markers flow), it worked — note in this
   file which vendor/firmware/candidate URL it was (WebSocket or SSH) so the
   next person doesn't have to rediscover it.
4. If the job stalls or errors, look for `*redfish.SOLUnsupportedError` (or a
   wrapped WebSocket/SSH dial error) in the logs/job error field. The error
   text includes the full `[]CaptureDebugStep` trail: every candidate URL
   tried, HTTP status, and a redacted body preview (never a password/token —
   see `sanitizePreview`).
5. Dell iDRAC first, then Supermicro — matches the existing OEM
   screenshot-capture precedent (`internal/common/redfish/screenshot.go`),
   so both features can eventually share field notes about the same BMCs.

## What to do when it fails (expected on the first attempt)

The WebSocket candidates in `solWSCandidates` (`internal/common/redfish/sol.go`)
are unverified. When you find the real endpoint on real firmware:

1. Capture it from the BMC's own web console (browser devtools → Network →
   WS filter) while opening its virtual console/SOL viewer.
2. Add it to the candidate list in `solWSCandidates`, keeping the existing
   debug-trail-on-every-attempt discipline.
3. Add a fixture-style unit test following the pattern in
   `internal/common/redfish/sol_test.go` (`newFakeSOLServer` +
   `TestOpenSOL_WebSocketDell_Success`) so the working shape is regression-tested.
4. Update this doc's checklist with the vendor/firmware version that worked.

If the BMC's console turns out to be a binary/graphical KVM protocol rather
than line-oriented SOL text (the likely outcome for most vendors), `redfish_sol`
is not the right transport for that BMC — fall back to `SHOAL_SERIAL_TRANSPORT=libvirt`
(lab only) or leave it as a documented gap; do not force IPMI or a
graphics-OCR hack into the SOL progress loop (Golden Rule 5: OCR is for
graphics-only failure screens, never the primary progress channel).

## Known limitations (as of this PR)

- **No SSH host-key pinning.** The SSH fallback uses
  `ssh.InsecureIgnoreHostKey()` — BMC SSH host keys are not verified. This
  mirrors the reality that operators don't pin these today, but it is a real
  gap, not an oversight; revisit if this transport sees production use.
- **Telnet is not implemented** — a Telnet-only BMC is reported as
  unsupported, not a crash.
- **Raw IPMI is never attempted**, even if a BMC only advertises IPMI SOL.
  This is intentional and permanent (project rule: Redfish only).
- **WebSocket candidate URLs are unverified** against any real vendor
  firmware as of this PR — see "What to do when it fails" above.
- **BMC concurrent-session limits are not load-tested.** `OpenSOL` opens a
  short-lived discovery session (closed before returning) plus one
  long-lived WS/SSH connection per active watch — this is the minimum
  possible footprint, but real BMCs (e.g. iDRAC's default ~6 session cap)
  have not been tested under concurrent Shoal load.
- **`session` Redfish auth mode is not supported for the native WebSocket
  path** — it's explicitly skipped (see the debug trail) rather than
  guessed at; only `basic` auth has been wired for WS candidate dials.

## Rollback

`redfish_sol` is opt-in and additive. To revert to the existing (lab-proven)
behavior:

```bash
unset SHOAL_SERIAL_TRANSPORT   # or export SHOAL_SERIAL_TRANSPORT=libvirt
```

or omit `serial_transport` on the `StartJobRequest`. Nothing about the
default `libvirt` path changes as part of this feature.
