# Real-hardware Redfish SOL runbook

`internal/common/redfish/sol.go` (`BMC.OpenSOL`) and the `redfish_sol` serial
transport (`internal/observe/sol/redfish_transport.go`,
`internal/observe/sol/factory.go`) are proven by fake-SSH unit tests; sushy-tools
has no SOL, so lab CI cannot. A live attach against the lab iDRAC is recorded
below (`-tags=live_sol`). Re-run that gated test when firmware or the attach
path changes.

Read this before pointing `redfish_sol` at a real BMC, and update it (or the
candidate list in `sol.go`) with what you learn — the initial WebSocket
candidate URLs are **unverified guesses**, not documented vendor APIs.

## What's implemented, and how

- **BMC control is Redfish-only.** Power, Virtual Media, boot override, SEL,
  sensors, inventory, and screenshots never use IPMI (`ipmitool chassis` etc.).
- **SOL may leave HTTP** (`BMC.OpenSOL`, opt-in `redfish_sol`). Discovery
  order: classify vendor → try native WebSocket SOL candidates, but **only keep
  the socket if the first frame is line-oriented SOL text** (HTML/binary/silence
  fall through; do not treat idle KVM as SOL) → SSH attach when eligible →
  otherwise `*redfish.SOLUnsupportedError` with a debug trail.
- **SSH eligibility:** any vendor if `ComputerSystem.SerialConsole.SSH` is
  enabled, or manager `SerialConsole` lists `SSH`; **Dell only** if
  `NetworkProtocol.SSH` is enabled or OEM serial-redirection attributes are
  `Enabled` — even when standard SerialConsole is empty (live iDRAC 7.30 on a
  PowerEdge R750, 2026-08-23). Attach commands: Redfish `ConsoleEntryCommand` if
  set; else Dell `console com2`, then `connect`. Non-Dell without
  `ConsoleEntryCommand` does **not** guess `console com2`. SSH auth is
  password **and** keyboard-interactive (iDRAC advertises KI only, not
  `password`).
- **IPMI 2.0 SOL** is specified in [`docs/sol-transports-design.md`](./sol-transports-design.md)
  as last resort and **is not implemented yet**. IPMI-only BMCs still return
  `*SOLUnsupportedError`. `WatchSession.Transport=ipmi_sol` remains an error.
  On this workstation, UDP/TCP 623 to the lab iDRAC is filtered; a future IPMI
  client must timeout, not hang.
- **Host Off:** SSH attach still returns a stream; silence until power-on is
  Observe’s stall timer, not an OpenSOL error. This runbook does not send
  power commands; the live probe below is attach-only.
- **Telnet is never attempted** (deferred).
- **WebSocket candidates are placeholders.** No vendor publishes a documented
  client-pull plain-text SOL-over-WebSocket endpoint. Real iDRAC/Supermicro
  console redirection is typically an HTML5/binary KVM protocol, not
  line-oriented SOL — the candidates in `solWSCandidates` (`sol.go`) are
  best-effort guesses. Per-candidate dial ≤3s; 500ms sniff. Do not expand the
  URL list until a real SOL URL is proven.

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
- **IPMI 2.0 SOL is specified, not implemented.** IPMI-only BMCs still return
  `*SOLUnsupportedError`. `WatchSession.Transport=ipmi_sol` remains an error.
- **WebSocket candidate URLs are not SOL on the probed iDRAC** (`/console`
  HTTP 200, `/wsman/virtualconsole` 404). Do not expand the list until a real
  vendor SOL URL is proven.
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

## Field notes — Dell PowerEdge R750 / iDRAC 7.30.10.50 (2026-08-23)

Live `OpenSOL` against `https://172.16.21.202` (`System.Embedded.1`),
attach-only (no power/media/boot changes):

| Probe | Result |
|-------|--------|
| Redfish `SerialConsole` | empty (`ConnectTypesSupported=[]`) |
| `NetworkProtocol.SSH` | enabled, port 22 |
| OEM `SerialRedirection.1.Enable` | Enabled |
| `wss://…/console` | HTTP 200, not a WebSocket (101) — sniffed fail, fall through |
| `wss://…/wsman/virtualconsole` | HTTP 404 |
| SSH auth methods | `publickey,keyboard-interactive` (**not** `password`) |
| Attach | `session.Start("console com2")` after KI auth → `Kind=ssh` `vendor=dell` |
| First bytes (host already On) | ANSI reset/clear, then `Connected to Serial Device 2. To end type: ^\` |
| After `Reset ForceRestart` (operator-authorized) | BIOS 1.15.2 POST on SOL: PCIe/USB/Video init, `PowerEdge R750`, F2/F10/F11/F12, Lifecycle Controller inventory, `Booting from Ubuntu` → `Boot Failed: Ubuntu` → PXE IPv4 on NIC Slot 5 Port 1 |

`TestLiveOpenSOL_ResetAndRead` (`-tags=live_sol`) attaches first, then `On` or `ForceRestart`, and captures ~90s of console. It sends no Virtual Media / boot override. The host may be left in PXE if the disk boot option failed — that is BIOS policy, not Shoal media.

Re-run (credentials from gitignored `.env`; never log the password):

```bash
set -a && . ./.env && set +a
SHOAL_BMC_URL=https://172.16.21.202 \
  go test ./internal/common/redfish -tags=live_sol -run TestLiveOpenSOL -v -count=1 -timeout 60s
```

CI must not use `-tags=live_sol`. IPMI UDP/623 remains filtered from this
workstation; do not treat that as an attach failure.
