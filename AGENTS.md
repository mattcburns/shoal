# AGENTS.md — Working Conventions for Shoal

This file is the canonical guide for how to work in this repository. It covers
project conventions, commands, and style. For **architecture, data models, and
the phased plan**, the source of truth is
[`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](./SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md)
(**v2.0.5**, Go stack). When this file and the design doc disagree, fix one of
them in the same change — they must stay consistent.

> **Read order for any task:** (1) this file, (2) the relevant Chapter 4 section
> of the design doc for the component you're touching, (3) the code.

**Stack:** the application is **Go only** (module
`github.com/mattcburns/shoal`). There is no dual-stack app. Lab automation
remains Ansible (and whatever Python the lab tools require). Do **not** add
application Python packages.

---

## 1. Golden Rules (non-negotiable invariants)

These encode the core design decisions. Violating one is a bug, even if tests pass.

1. **All LLM calls go through `internal/core/ai` over `net/http`.** No provider
   SDKs (`openai-go`, official Anthropic clients, …) outside that package. No
   LiteLLM. Discover/Observe/Deploy never call an LLM directly — they delegate
   to Core (`Reconciler` / profile helpers).
2. **Deterministic-first; hybrid ownership is fixed.** Discover owns
   deterministic adapters, the confidence gate, merge, and hybrid pipeline
   orchestration. Core owns **AI reconciliation only** (`internal/core/reconcile`).
   Core **never** imports Discover. Never route already-structured data straight
   to an LLM.
3. **Secrets never reach an LLM or a log.** BMC credentials live in the secret
   backend and are referenced by an opaque `credential_ref`. Strip password-like
   fields from any payload **before** it touches a model or `log/slog`.
   `NormalizedAsset` must never contain a password.
4. **NetBox stores identity + current `lifecycle_state` only.** Time-series and
   events (SEL, sensors, job logs, durable jobs) go to the telemetry/Postgres
   store — never into NetBox custom fields.
5. **SOL is the primary provisioning feedback channel.** Progress comes from the
   `SHOAL|...` serial marker protocol. OCR is only for graphics-only failure
   screens (Phase 6; approach not yet decided), never the progress loop.
6. **Redfish only via `internal/common/redfish` (gofish-backed).** gofish is the
   chosen Redfish stack from day one. **gofish types must not leak** outside
   `internal/common/redfish`. Reuse sessions (or basic auth in lab), cap
   per-BMC concurrency (~1–2), and back off on throttling (respect `Retry-After`).
7. **Artifact/ISO serving is plain HTTP on the management segment for MVP.** Do
   not add TLS to the ISO server (many BMCs reject self-signed certs); TLS is
   Phase 6. Phase 2 publishes ISOs to the lab ISO dir and serves them at
   `http://…:8080/<name>.iso`.
8. **Structs + decode/validate for AI and cross-component data.** No Pydantic.
   AI path: schema text → Complete → strip fences → `json.Unmarshal` into `T` →
   `internal/common/validate` → conflict policy. Validate all model output.
9. **Respect component boundaries and import rules** (design §3–4):
   - Core never calls Redfish or writes NetBox.
   - Discover/Observe/Deploy never call an LLM directly.
   - **`internal/observe` must not import `internal/deploy`** and vice versa.
   - Cross-sibling collaboration uses neutral ports:
     `internal/common/jobport` (`JobProgress`) and
     `internal/common/watchport` (`WatchRegistrar`).
   - Composition root `cmd/shoal` is the only place that constructs concrete
     types and injects interfaces.
10. **MVP runs as one process.** One binary, goroutines + `context.Context`.
    Don't introduce Celery/Redis/microservices/queues without a demonstrated
    bottleneck. Lab Redis is for NetBox, not the app.
11. **Orchestrator is the sole lifecycle writer.** `JobStore` is **pure
    persistence** (CRUD / progress / transition SQL). Observe proposes progress
    via `jobport` (`ApplyMarker`, `ReportStall`); terminal handling and BMC
    cleanup run in Deploy `Orchestrator.HandleTerminal` (async), never inline
    on the Observe path.
12. **Stdlib-first (documented exceptions only).** Prefer the Go standard
    library. External modules must be on the design doc §7.1 allow-list; adding
    one requires updating §7.1 in the same change. Current allow-list:
    `gofish`, `pgx` (default Postgres), optional `modernc.org/sqlite` (demo),
    `staticcheck` (toolchain). Rejected for MVP: Cobra, Gin/Echo/Chi, LiteLLM /
    provider SDKs, ORMs, greenfield thin Redfish client.

---

## 2. Repository Layout

```
shoal/                                    # module: github.com/mattcburns/shoal
  cmd/shoal/                              # composition root + main
  internal/
    api/                                  # net/http ServeMux handlers
    cli/                                  # flag-based subcommands
    core/
      ai/                                 # LLM HTTP + decode pipeline
      reconcile/                          # AI reconciliation only
      profile/                            # provisioning profile generation
    discover/
      adapters/                           # deterministic Redfish/CSV — NOT in core
    observe/
      sol/                                # parser + transports → models.SOLMarker
      poll/
    deploy/
      job/                                # Orchestrator + HandleTerminal + jobport adapter
      jobstore/                           # pure jobs table CRUD/progress/transition
      iso/
    common/
      models/                             # data structs only (incl. SOLMarker)
      jobport/                            # JobProgress — Observe consumes; Deploy implements
      watchport/                          # WatchRegistrar — Deploy consumes; Observe implements
      validate/
      redact/
      secrets/
      redfish/                            # gofish behind Shoal BMC interfaces
      netbox/
      telemetry/
      config/
  prompts/                                # versioned prompts + schemas/ + fewshot/
  testdata/
    golden/                               # prompt regression fixtures
    redfish/                              # record/replay corpus
  infra/ansible/                          # lab automation (language-agnostic)
  docs/
  go.mod
  AGENTS.md
  SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md
```

The lab is driven entirely by Ansible (there is **no Makefile**). Lab config
lives in `infra/ansible/inventory/group_vars/` as YAML vars, with secrets in
`group_vars/all/vault.yml` (git-ignored; see `vault.yml.example`). See §3.

**Dependency direction** (never reverse, never cycle):

```
cmd/shoal                              # composition root — wires all concrete types
  → internal/api, internal/cli
      → internal/discover | internal/observe | internal/deploy
          → internal/core              # AI only
          → internal/common/...        # models, ports, redfish, netbox, secrets, …
  internal/core → internal/common only
  internal/common → (nothing under internal/)
  internal/observe ↛ internal/deploy   # FORBIDDEN
  internal/deploy  ↛ internal/observe  # FORBIDDEN
```

---

## 3. Commands

### 3.1 Lab (Ansible)

The lab is driven through **Ansible playbooks** (see design doc §8). There is
no Makefile. Phase 0 wires the lab; Phase 1+ wires the app. Two Phase 0 lab
modes are supported: direct host mode and VM-hosted mode with nested
virtualization.

One-time setup:
```bash
ansible-galaxy collection install -r infra/ansible/requirements.yml
cp infra/ansible/inventory/group_vars/all/vault.yml.example \
   infra/ansible/inventory/group_vars/all/vault.yml   # then edit; optionally ansible-vault encrypt
```

Lab lifecycle (add `--ask-vault-pass` if your vault is encrypted). The inventory
you pass selects the mode:
```bash
# VM-hosted mode
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml

# Direct host mode
ansible-playbook -i infra/ansible/inventory/lab.yml     infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab.yml     infra/ansible/playbooks/smoke.yml
ansible-playbook -i infra/ansible/inventory/lab.yml     infra/ansible/playbooks/down.yml
```

`up.yml` and `down.yml` are the only entrypoints you normally run. They import
the phase playbooks (`vm_provision`, `preflight`, `lab_up`, `bootstrap_netbox`,
`lab_down`, `vm_destroy`), which can also be run individually; see
`docs/lab-runbook.md`.

Default VM-hosted endpoints: NetBox `:8000`, sushy `:8001`, ISO HTTP `:8080`,
Ollama `:11434`, telemetry Postgres `:5433`. Shoal app listens on **`:8088`**.

### 3.2 App (Go)

**Go 1.22+.** Module path: `github.com/mattcburns/shoal`.

Toolchain (one-time / CI):
```bash
go install honnef.co/go/tools/cmd/staticcheck@2025.1.1
```

Quality and tests (no make):
```bash
gofmt -w .
go vet ./...
staticcheck ./...
go test ./...
go test ./... -tags=integration   # needs lab (up.yml)
```

Run the app (Phase 1+):
```bash
go run ./cmd/shoal serve -addr "${SHOAL_HTTP_ADDR:-:8088}"
```

Phase 2 thesis spike shape (flag-bound; no Discover/NetBox required):
```bash
go run ./cmd/shoal deploy run \
  -device lab-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -bmc-user "$SHOAL_BMC_USERNAME" \
  -bmc-pass "$SHOAL_BMC_PASSWORD" \
  -serial-target <libvirt-or-sol-target> \
  -iso-url http://192.168.122.100:8080/shoal-marker.iso
```

- Always run `gofmt`, `go vet`, `staticcheck`, and `go test ./...` before
  declaring a task done.
- Lab config is YAML under `infra/ansible/inventory/group_vars/` (shared
  defaults in `all/defaults.yml`, mode overrides in `vm_mode.yml` /
  `direct_mode.yml`, secrets in `all/vault.yml`). Do **not** commit a
  repo-level `.env` for lab secrets. App config is **process environment**
  (Compose/`env.j2` may render it; a local untracked `.env` for `go run` is
  fine for developers).

### 3.3 App environment (contract)

| Env var | Notes |
|---------|--------|
| `SHOAL_HTTP_ADDR` | Default `:8088`; bind management interface in lab |
| `SHOAL_LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error` (default `info`) |
| `SHOAL_TELEMETRY_DATABASE_URL` | Lab Postgres `…:5433/shoal_telemetry` (jobs + events/sensors; Observe poll) |
| `SHOAL_NETBOX_URL` / `SHOAL_NETBOX_TOKEN` | Identity store |
| `SHOAL_AI_PROVIDER` | `ollama` \| `cloud` |
| `SHOAL_AI_MODEL` | Text / default model (lab: `llama3.2:3b`) |
| `SHOAL_AI_VISION_MODEL` | Vision/OCR model for `CompleteVision` (lab: `deepseek-ocr`; empty skips photo) |
| `SHOAL_OLLAMA_URL` | Local Ollama base URL |
| `SHOAL_CLOUD_AI_BASE_URL` / `SHOAL_CLOUD_AI_API_KEY` | Cloud only; key is vault secret — never log |
| `SHOAL_REDFISH_AUTH_MODE` | `basic` (lab default) \| `session` |
| `SHOAL_REDFISH_TLS_MODE` | `off` \| `insecure` \| `custom_ca` |
| `SHOAL_REDFISH_CA_FILE` | If `custom_ca` |
| `SHOAL_BMC_USERNAME` / `SHOAL_BMC_PASSWORD` | Lab defaults only; production uses secrets backend per device |
| `SHOAL_ISO_BASE_URL` | BMC-reachable ISO HTTP prefix (lab nested: `http://192.168.124.1:8080`). Used to resolve profile `iso_base` and `deploy iso publish` |
| `SHOAL_ISO_PUBLISH_DIR` | Filesystem dir served on `:8080` (lab: `/srv/iso`). Phase 5c publish target |
| `SHOAL_ISO_BUILD_SCRIPT` | Optional path to `build-marker-iso.sh` (auto-discovers from repo) |
| `SHOAL_ISO_DYNAMIC` | If `true`, Start may build+publish when `ISOURL` empty (needs publish dir + base URL; Phase 6a) |
| `SHOAL_RECONCILE_FAIL_ORPHANS` | Default `true` |
| `SHOAL_FEWSHOT_DIR` | Append-only learned few-shot JSONL (confirm learning). Lab Ansible default: `/var/lib/shoal/fewshot` via `shoal_fewshot_dir` + `env.j2`. Empty disables confirm |
| `SHOAL_PROFILE_DIR` | JSON provisioning profiles + approval records (Phase 5b). Lab Ansible default: `/var/lib/shoal/profiles` via `shoal_profile_dir` + `env.j2`. Empty disables non-spike profile load |

Full table and Ansible extension points: design doc §8.1.

**Phase 4 Observe:** `shoal observe status|poll`, `GET /v1/devices/{id}/status` and
`…/events`. Poll uses Redfish `ListSEL`/`ListSensors` → durable `telemetry.Store`
only (no silent memory fallback). Empty SEL/sensors with exit 0 is valid when the
BMC has no logs; write failures and Redfish errors fail the poll. Observe never
imports Deploy (job reads via `jobport.JobQuery`).

**Phase 5–6a Deploy:** Orchestrator best-effort syncs NetBox `lifecycle_state`.
Profiles under `SHOAL_PROFILE_DIR`; destruct needs approve or `-approve-destruct`.
ISO: publish + profile resolve (5c); **6a** `install-mode write` writes `/payload`
with real SOL progress; optional `-build-iso` / `SHOAL_ISO_DYNAMIC`. Marker
`simulate` mode remains the Phase 2 demo. Plain HTTP ISO on mgmt segment.
Sole lifecycle writer remains Orchestrator; JobStore stays pure persistence.

---

## 4. Coding Style

- **Formatting/linting:** `gofmt` (write before commit), `go vet`, `staticcheck`.
  Don't hand-format around `gofmt`. Pin staticcheck in CI as in §3.2.
- **Language:** Go 1.22+ idioms. Prefer stdlib (`net/http`, `flag`,
  `encoding/json`, `context`, `log/slog`, `database/sql`, `os/exec`, `testing`).
- **Typing:** exported APIs with clear types; use modern constructs
  (`any`, `~` constraints where useful). Interfaces at package boundaries;
  accept interfaces, return structs where practical.
- **Concurrency:** I/O work uses goroutines + `context.Context`. No shared
  maps without synchronization. Per-job child contexts; root cancel on
  shutdown runs cleanup. Per-BMC semaphore (~1–2). Prefer `sync` from stdlib;
  do not add `golang.org/x/sync` without updating the allow-list.
- **Naming:** `MixedCaps` for exported names, `mixedCaps` for unexported,
  `SCREAMING_SNAKE` only for true constants where idiomatic. Package names are
  short lowercase (`redfish`, `jobstore`, not `redfish_client`).
- **Errors:** return `error` with actionable context (`fmt.Errorf("…: %w", err)`).
  Never swallow errors silently; never put secrets in error strings or logs.
- **Docs:** godoc one-liners on exported symbols; document contract (inputs,
  outputs, side effects), not the obvious.
- **Logging:** `log/slog` only. Level from `SHOAL_LOG_LEVEL`. Redact secrets in
  attributes; add redaction tests when touching log or AI paths.
- **Config:** never hard-code endpoints, tokens, or model names. Lab secrets in
  vault; app reads env. Keep `vault.yml.example` current.
- **CGO:** prefer **CGO-free** main binary builds for simple cross-compile /
  static linking.

---

## 5. Data Models & State

- All shared data models live in `internal/common/models` (see design doc §5):
  `NormalizedAsset`, `FieldConfidence`, `NormalizationResult`, `NormalizedEvent`
  (includes `DeviceID`), `LifecycleState`, `ProvisioningJob`,
  `ProvisioningProfile`, `DeviceStatus`, `WatchSession`, `SOLMarker`, raw
  ingest DTOs, job start/cancel API types.
- Ports (interfaces) live next to their concern, **not** in `models`:
  `jobport`, `watchport`, `secrets`, `redfish`, `netbox`, `telemetry`.
- Changing a shared model is a cross-component change: update all consumers and
  the design doc §5 in the same change.
- Provisioning lifecycle follows the state machine in design §5.
  **Only Deploy Orchestrator commits lifecycle transitions.** Observe may
  propose progress fields via `jobport`. Transitions must be explicit and logged.
- Durable jobs live in Postgres (`jobs` table on lab `:5433` /
  `shoal_telemetry`) so orphan reconcile works after restart.

---

## 6. AI & Prompt Conventions

- Prompts are **versioned files** under `prompts/` (`.md`/`.txt`) with JSON
  schemas under `prompts/schemas/`. Treat a prompt change like an API change:
  bump it and update golden tests under `testdata/golden/`.
- Every prompt requests JSON matching the target schema, includes 2–4 few-shot
  examples, states the model's role, and asks for **per-field confidence + a
  raw excerpt as evidence**.
- **Decode pipeline** (no Pydantic): embed schema text → `Complete` /
  `CompleteVision` → strip markdown fences → `json.Unmarshal` into `T` →
  `validate.T` → conflict policy in Discover. Confidence ∈ [0,1]; AI-sourced
  fields should carry evidence.
- **Redact secrets** from every payload before the call
  (`internal/common/redact`).
- Log every AI call: prompt hash/version, **resolved model name**, token counts
  (if available), latency, and output summary. Logs must contain no secrets and
  no full raw photo bytes.
- **Model selection** (design §6 / v2.0.5):
  - `Complete` → `req.Model` or `SHOAL_AI_MODEL` (lab text: `llama3.2:3b`)
  - `CompleteVision` → `req.Model` or `SHOAL_AI_VISION_MODEL` (lab:
    `deepseek-ocr`); require a real vision/OCR model for photo — do not fall
    back to the text model for images
  - Discover **text** path uses `Complete` only; **photo** path uses
    `CompleteVision` with prompt `Free OCR.`, then parse SERIAL/VENDOR/MODEL.
    Never send image bytes through text-only `Complete`. If serial cannot be
    extracted from OCR, **fail** (no synthetic `photo-unknown` serials).
  - Do **not** use `deepseek-ocr` as the text hybrid model; do **not** treat
    `moondream` as photo AC.
- **Learning loop (Phase 3b):** operator `discover confirm` / `POST /v1/discover/confirm`
  appends redacted examples to `SHOAL_FEWSHOT_DIR` (Core `fewshot` store). Only
  confirmed examples are learned — never auto-learn every ingest. Secrets must
  not appear in few-shot input. Learned lines load into `ReconcileAsset` prompts
  (capped). Confirm does **not** re-write NetBox.
- Local vs cloud via env (`SHOAL_AI_PROVIDER`, `SHOAL_AI_MODEL`,
  `SHOAL_AI_VISION_MODEL`, `SHOAL_OLLAMA_URL`, `SHOAL_CLOUD_AI_*`,
  `SHOAL_FEWSHOT_DIR`). Cloud API key lives in vault — never in `defaults.yml`
  or a log. Nested lab Ollama is often CPU-bound; text stays small
  (`llama3.2:3b`), vision OCR uses `deepseek-ocr` (~6.7GB). Hybrid pipeline
  keeps most inputs off vision. **Graphics failure-screen OCR** (Phase 6b):
  Core `CompleteVision` + `observe ocr`; image from `-file` (lab) or Redfish OEM
  capture (Dell / Supermicro first, rich debug). Separate from Phase 3
  asset-label OCR. SOL remains primary progress.

---

## 7. Redfish / BMC Conventions

- All Redfish interaction goes through **`internal/common/redfish`**, which wraps
  **`github.com/stmcginnis/gofish`**. Call sites depend only on Shoal interfaces
  (`BMC`, config, domain types) — never import gofish outside that package.
- Isolate per-vendor quirks in the wrapper/adapters, not in Deploy/Observe call
  sites.
- **Auth:** lab default **basic** (`SHOAL_REDFISH_AUTH_MODE=basic`, matches
  sushy-tools smoke); production prefers **session** tokens. Reuse and close
  sessions; cap concurrency per BMC (~1–2); back off on 4xx/5xx (respect
  `Retry-After`).
- **TLS:** `SHOAL_REDFISH_TLS_MODE` = `off` \| `insecure` \| `custom_ca`. Never
  default to insecure except when explicitly configured for lab/real BMC
  self-signed certs.
- **Idempotency:** every Deploy action reads current BMC state (media inserted?
  override set? power?) and converges; re-runs must be safe.
- **Cleanup is mandatory:** always eject Virtual Media and clear the one-time
  boot override on success, failure, and cancel (Orchestrator finalizer).
  Leaving them set bricks the next boot.
- **SOL ownership:** Observe holds the single SOL session for a node during a
  job (register via `watchport`). Lab uses libvirt serial; real hardware uses
  Redfish/IPMI SOL transports behind `sol.Transport`.
- Capture real Redfish responses into the **record/replay corpus**
  (`testdata/redfish/`) when you hit a new vendor/firmware shape, and add a
  fixture-based test. Pin gofish version in `go.mod`.

---

## 8. Testing

- Framework: Go `testing` package; **table-driven** tests. Prefer stdlib
  `testing` over heavy assertion libraries.
- Mark lab-dependent tests with build tags, e.g. `//go:build integration` or
  `e2e`. Integration needs the lab (`up.yml` first).
- **Unit:** every public Core function (especially Reconciler and conflict
  policy), SOL parser, validate/redact, JobStore transitions with a test DB or
  fakes.
- **Integration:** against sushy-tools lab nodes (direct or VM-hosted inventory).
- **E2E:** full Discover → Observe → Deploy against the lab; Phase 2 spike is
  Deploy+Observe+SOL without AI/Discover.
- **Prompt regression:** golden inputs/outputs under `testdata/golden/`; update
  deliberately when a prompt or schema changes.
- **SOL protocol:** producer/consumer/marker parsing against libvirt serial in
  the lab.
- **Record/replay:** vendor Redfish fixtures for wrapper + reconciliation tests.
- Know the **lab fidelity gap** (design doc §10): sushy-tools cannot prove SOL
  transport on real BMCs, vendor Virtual Media quirks, graphics OCR, or realistic
  SEL/sensor variety. Don't claim those are validated from lab runs alone.

```bash
go test ./...
go test ./... -tags=integration
go test ./internal/core/... -run Reconcile
```

---

## 9. Security & Secrets

- Never commit secrets. Lab secrets live in
  `infra/ansible/inventory/group_vars/all/vault.yml` (git-ignored; see
  `vault.yml.example`). Optionally encrypt with `ansible-vault`.
- BMC credentials go to the secret backend keyed by device/`bmc_ip`; code holds
  a `credential_ref`, never the password in models.
- Don't log credentials, tokens, cloud API keys, or full raw payloads that may
  contain them. Phase 1+ includes slog redaction tests.
- **MVP HTTP API is unauthenticated** — bind to the management interface only
  (`SHOAL_HTTP_ADDR`). Anyone on that segment can trigger jobs (accepted lab/MVP
  risk). Phase 6+ may add auth.
- Prefer CGO-free builds; no secrets baked into the binary.
- The Shoal host holds fleet BMC credentials — treat it as high-value; keep
  credential access auditable; secret files mode `0600`.

---

## 10. Git & Pull Requests

- **Branches:** `feature/<phase>-<short-desc>`, `fix/<short-desc>`.
- **Commits:** Conventional Commits (`feat:`, `fix:`, `refactor:`, `docs:`,
  `test:`, `chore:`). Keep them scoped and reference the design doc section or
  phase when relevant. Include a `Co-Authored-By:` trailer identifying the agent.
- **Don't commit unless asked** to by the human driving the work.
- **PR description:** what changed, which phase/acceptance criteria it advances,
  how it was tested (note lab vs real-hardware), and any design-doc updates.
- **New dependencies:** only with design §7.1 allow-list update in the same PR.

---

## 11. Definition of Done

A change is done when:

- It satisfies the acceptance criteria of its phase in the design doc.
- `gofmt` is clean, `go vet ./...` and `staticcheck ./...` pass, and
  `go test ./...` passes (plus integration/e2e tags when relevant).
- New/changed behavior has tests (unit at minimum; integration/e2e where
  relevant).
- No secret can reach a log or an LLM (add a redaction test if you touched that
  path).
- The design doc is updated if you changed architecture, models, prompts, allow-
  listed deps, or scope.
- Component boundaries, import rules, and the Golden Rules (§1) are intact —
  especially: no observe↔deploy imports, no gofish leakage, Core AI-only,
  JobStore pure persistence.

---

## 12. Live image (marker producer)

Phase 2 needs a minimal live image that emits `SHOAL|…` markers over serial.

- **Primary:** Ansible role on the lab VM builds and publishes into the lab ISO
  directory (nginx `:8080`).
- **Alternate:** developer workstation build + copy/rsync/scp into the lab ISO
  dir.

Document both paths (design Appendix I / §8.2). Default serve URL for the spike:
`http://<lab-host>:8080/<name>.iso`. Do not rely on an in-process ISO server for
Phase 2 ACs.
