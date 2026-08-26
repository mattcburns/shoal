# Shoal: Architecture & Design

**Status:** Living design reference (split from the original
`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md` monolith, last
versioned v2.0.9).
**Intended Audience:** Human architect + team of AI coding agents.

**Purpose of this document**
This is the architecture, data-model, and AI-design reference for Shoal. For
day-to-day working conventions (commands, coding style, testing, security,
Git/PR process, and the non-negotiable Golden Rules), see
[`AGENTS.md`](../../AGENTS.md) — that file is the single source of truth for
conventions; this document does not restate them. For phase status and what's
next, see [`docs/plans/roadmap.md`](../plans/roadmap.md).

**Product continuity.** Shoal is a **language/stack revision** of an earlier
Python (v1.1) design — not a greenfield redesign. Vision, component
boundaries, hybrid normalization, the SOL marker protocol, the provisioning
state machine, the lab environment (Ansible + sushy-tools), security
principles, and the phased plan structure were preserved across the rewrite.
What changed is the application implementation language (Go), package layout,
interfaces, tooling, and agent conventions. A condensed history of the
version-by-version decisions that got here lives in
[Appendix: Document History](#appendix-document-history).

**UI note:** the MVP ships **API + CLI only**; any browser UI remains
deferred unless product prioritizes it. Operators use `shoal observe status`,
`shoal deploy run`, and HTTP JSON.

**Code samples in this document** are **illustrative sketches** (may omit
imports or use abbreviated names). Implementers must produce compiling code;
do not copy sketches as-is.

---

## 1. Executive Summary & Vision

**Shoal** is a modern, BMC-centric bare-metal lifecycle platform focused on
**Redfish/Swordfish** ecosystems.

**Core Differentiator**: The entire **provisioning path runs over the BMC
management network only** — no provisioning VLAN, no PXE, no extra transit
network. Status flows back over **Serial-over-LAN (SOL)**, and provisioning
uses **Redfish Virtual Media + one custom live image** whose payload is
embedded (written to local disk). Strong multimodal observability (SEL +
sensors + SOL, with OCR for graphics-only failure screens).

**Target Users**: Bare-metal operators, colo/AI cluster teams, and
organizations that value isolation and simplicity.

**Key Outcomes**:
- Ingest messy hardware data → clean NetBox records (deterministic-first, AI
  for the messy edges)
- Real-time provisioning visibility via **SOL status markers** (SEL +
  sensors for health)
- Provision bare metal using only Redfish Virtual Media + one custom live
  image
- Dead-simple switch between local Ollama and cloud AI providers

**Scope note on the thesis**: "BMC-only" refers to the **provisioning path**.
After install, the node reboots into its final OS and uses its normal
production network. The provisioning loop needs no host data network because
(a) the OS payload is embedded in the live image and written locally, and (b)
status returns over SOL.

**Why Go**: A single static binary, excellent concurrency for concurrent BMC
polling/SOL tails, strong typing without a heavy runtime, and a stdlib that
already covers HTTP, JSON, SQL interfaces, logging, testing, and subprocess
control.

---

## 2. Goals, Non-Goals, Constraints & Principles

### 2.1 Goals
- Fast iteration for a solo developer or small team
- Excellent observability during provisioning
- Minimal infrastructure footprint
- Easy binary / container distribution (single Go binary)
- Clean separation of concerns (so AI agents can work in parallel)
- Maximize Go standard library; minimize external dependencies

### 2.2 Non-Goals
- Multi-tenancy, RBAC, enterprise features
- Full replacement of Ironic / MAAS / Foreman
- Support for every obscure vendor quirk
- High-scale orchestration (thousands of nodes)
- TLS everywhere / full PKI (see §8)
- Authenticated multi-user HTTP API beyond the optional bearer token (§8)
- Browser UI (API + CLI only)
- Python application code (the lab remains Ansible/Python where tools
  require it; see the intro of `AGENTS.md` for the app-vs-lab boundary)
- Heavy frameworks (Cobra, Gin/Echo/Chi, ORM frameworks) unless a measured
  need appears

### 2.3 Constraints
- Must work well on modest hardware. **Reference GPU reality:** a Quadro P600
  has only **2 GB VRAM** — the recommended local text models barely fit and
  `*-vision:11b` does **not** fit. Treat local vision as a slow CPU fallback;
  prefer cloud for vision. The hybrid normalizer (§4.2) is designed so most
  inputs never need a heavy model.
- Must support both fully local (air-gapped) and cloud-assisted AI
- **Must be Go-based** for the entire application (CLI, API, Core, Discover,
  Observe, Deploy, shared models)
- Prefer stdlib; every external module must be on the allow-list (§7.1) with
  justification
- Must produce single-binary or small-container deliverables where practical
- Prefer **CGO-free** builds for the main binary so cross-compile and static
  linking stay simple

### 2.4 Guiding Principles (for all agents)
1. **Modularity first** — Every major component must be independently
   testable and replaceable.
2. **AI abstraction** — All LLM calls go through `internal/core/ai` over
   `net/http`. No provider SDKs outside that package.
3. **Deterministic-first** — Use AI where the world is messy, not where data
   is already structured.
4. **Observability by default** — Every important action produces structured
   events (`log/slog` + telemetry store).
5. **Human-in-the-loop friendly** — Prompts and outputs should be inspectable
   and debuggable.
6. **Secrets stay local** — Credentials are never placed in models that get
   logged or sent to an LLM.
7. **Simplicity over cleverness** — Prefer boring, readable code over complex
   abstractions. MVP runs as a single process.
8. **Document decisions** — Any significant choice must be recorded in this
   document or linked issues.
9. **Stdlib-first (with documented exceptions)** — Reach for the standard
   library before a dependency. **Exception:** Redfish uses **gofish** from
   day one (§4.5). Any other new module must update §7.1 in the same change.
10. **Interfaces at boundaries** — Data structs live in `models` (and small
    protocol packages under `common`). **Cross-component ports live in
    neutral `common` packages** so siblings never import each other.
    Composition root (`cmd/shoal`) is the only place that constructs concrete
    types and injects interfaces.

These principles are the *rationale* behind the non-negotiable Golden Rules
in `AGENTS.md` §1 — that file states the rules as enforceable conventions;
this section explains why they exist.

---

## 3. High-Level Architecture

### 3.1 Component Diagram (import-faithful)

Wiring flows **from the composition root outward**. Core is a **dependency**
of Discover/Observe/Deploy — not their orchestrator. **Observe and Deploy
never import each other.**

```mermaid
graph TD
    CMD[cmd/shoal<br/>composition root]
    CMD --> CLI[internal/cli]
    CMD --> API[internal/api<br/>net/http]

    CLI --> DIS[internal/discover]
    CLI --> OBS[internal/observe]
    CLI --> DEP[internal/deploy]
    API --> DIS
    API --> OBS
    API --> DEP

    DIS --> CORE[internal/core<br/>AI Reconciler only]
    OBS --> CORE
    DEP --> CORE

    CORE --> AI[core/ai<br/>net/http LLM client]
    AI --> OLL[Ollama]
    AI --> CLOUD[Cloud OpenAI-compatible]

    DIS --> NB[NetBox<br/>identity + lifecycle]
    DEP --> NB
    DEP --> JS[(JobStore pure persistence)]
    OBS --> JP[common/jobport<br/>JobProgress]
    JP -.->|implemented by<br/>Orchestrator adapter| DEP
    OBS --> TS[(Telemetry store<br/>events / sensors / job_log)]

    DIS --> SEC[secrets.Backend]
    DEP --> RF[common/redfish]
    OBS --> RF
    DEP --> WR[common/watchport<br/>WatchRegistrar]
    WR -.->|implemented by Observe| OBS
    RF --> BMC[BMC / sushy-tools]
    BMC -- Virtual Media --> LIVE[Live image]
    LIVE -- SOL markers --> OBS
```

**Topology**: packages under one Go module, compiled into **one binary**
(`cmd/shoal`). The process hosts:

- HTTP API (`net/http` ServeMux) for machine clients
- CLI subcommands for operator workflows
- Background workers as goroutines (polling, SOL tail, job state machine)
  cancelled via `context.Context`

**Component Responsibilities** (strict boundaries):

| Component | Owns | Does NOT own |
|-----------|------|--------------|
| **Shoal Core** (`internal/core`) | AI client, **AI reconciliation only**, prompts, profile generation, few-shot/RAG store for AI | Deterministic adapters, Redfish, NetBox, job transitions |
| **Shoal Discover** (`internal/discover`) | Ingestion workflows, **deterministic parsers/adapters**, confidence gate, hybrid pipeline **orchestration**, photo handling, NetBox writes | Direct LLM HTTP (delegates to Core `Reconciler`) |
| **Shoal Observe** (`internal/observe`) | SEL/sensor polling, **SOL session + marker parsing**, telemetry writes, status aggregation, OCR of failure screens | Direct LLM HTTP; **does not import Deploy**; does not commit lifecycle transitions |
| **Shoal Deploy** (`internal/deploy`) | ISO building, Redfish Virtual Media orchestration, **`JobStore` (persistence)**, **Orchestrator** (sole lifecycle + cleanup), watches via `WatchRegistrar` port | Direct LLM HTTP; **does not import Observe** |
| **Common** (`internal/common`) | Shared models, **jobport / watchport interfaces**, secret backend, config, Redfish client, NetBox client, telemetry SQL, validation, redaction | Business workflows |

**Hybrid normalization ownership (canonical — no package cycle):**

```
Discover.Ingest
  ├─ adapters.ParseDeterministic(raw)     // internal/discover/adapters only
  ├─ gate.Accept(partial)? ──yes──► validate → secrets → NetBox → return
  └─ no → core.Reconciler.Reconcile(redacted raw, partial, schema)
            └─ core/ai LLM only
       → merge + conflict policy in Discover
       → validate → secrets → NetBox → return
```

- **Adapters stay in Discover.** Core **never** imports Discover.
- Core exposes `Reconciler` (and `LLM` / `Profiler`), **not** a full-pipeline
  `Normalizer` that owns deterministic code.
- Layout: `internal/discover/adapters/`, `internal/core/reconcile/`,
  `internal/core/ai/` — **no** `internal/core/normalize` owning adapters.

**Dependency direction** (never reverse, never cycle):

```
cmd/shoal                              # composition root — wires all concrete types
  → internal/api, internal/cli
      → internal/discover | internal/observe | internal/deploy
          → internal/core              # AI only
          → internal/common/...        # models, jobport, watchport, redfish, netbox, secrets, telemetry, validate
  internal/core → internal/common only
  internal/common → (nothing under internal/)
  internal/observe ↛ internal/deploy   # FORBIDDEN
  internal/deploy  ↛ internal/observe  # FORBIDDEN
```

**Neutral ports (break sibling coupling):**

| Port package | Interface | Implemented by | Consumed by |
|--------------|-----------|----------------|-------------|
| `internal/common/jobport` | `JobProgress` (`ApplyMarker`, `ReportStall`, `ReportTransportError`) | Deploy adapter that writes progress via `JobStore` + notifies Orchestrator | Observe |
| `internal/common/watchport` | `WatchRegistrar` (`Register`, `Unregister`) | Observe | Deploy Orchestrator |
| `internal/common/models` (or `solproto`) | `SOLMarker` DTO | — (data only) | Observe parser, jobport, Orchestrator |

Composition root example (illustrative):

```go
// cmd/shoal — only place that sees concrete observe + deploy types together
store := jobstore.New(db)                    // pure persistence
orch := deploy.NewOrchestrator(store, rfFactory, /* watchport */ nil)
obs := observe.NewService(/* jobport */ orch.ProgressPort(), telemetry)
orch.SetWatchRegistrar(obs)                  // inject WatchRegistrar after both exist
// api/cli receive orch + obs as interfaces only
```

### 3.2 Process / concurrency model

```mermaid
sequenceDiagram
    participant CLI as CLI / HTTP
    participant API as api (net/http)
    participant DEP as deploy.Orchestrator
    participant JS as JobStore
    participant WR as watchport
    participant OBS as observe
    participant RF as redfish client
    participant BMC as BMC / libvirt serial

    CLI->>API: POST /v1/jobs
    API->>DEP: StartAsync(ctx, req) returns immediately with job ID
    DEP->>JS: Insert PROVISIONING job
    DEP-->>API: 201 + job
    Note over DEP: goroutine: runStart(jobCtx)
    DEP->>RF: InsertVirtualMedia / BootOverride / PowerOn
    RF->>BMC: Redfish HTTP
    DEP->>WR: Register(WatchSession)
    WR->>OBS: start SOL tail
    OBS->>BMC: Own SOL / serial tail
    BMC-->>OBS: SHOAL|... markers
    OBS->>DEP: jobport.ApplyMarker (progress write + notify)
    Note over DEP: terminal? → HandleTerminal async
    DEP->>JS: UpdateProgress or Transition
    DEP->>RF: Cleanup (eject + clear override)
```

**Composition-root lifecycle** (`cmd/shoal` / `serve`):

1. Load config from env (`AGENTS.md` §3.3). Construct clients (DB, NetBox,
   Redfish factory, secrets, AI).
2. Root `ctx, stop := signal.NotifyContext(parent, SIGINT, SIGTERM)`.
3. Construct `JobStore` → `Orchestrator` → Observe service; inject
   `jobport.JobProgress` into Observe and `watchport.WatchRegistrar` into
   Orchestrator (**no sibling imports**).
4. `http.Server` runs in a goroutine; handlers use `r.Context()` only for
   request-scoped work.
5. **`POST /v1/jobs` does not block on provisioning** (implemented
   2026-08-25, `internal/deploy/job/orchestrator.go` /
   `internal/api/jobs.go`; landed after an earlier session shipped it
   synchronous and a real deprovision then timed out client-side on a job
   that had in fact started fine). The handler calls
   `Orchestrator.StartAsync`, which:
   - Runs `prepareStart` (resolve, validate, credentials, CD probe, persist
     the job as `PROVISIONING`) synchronously, on the caller's context
   - Backgrounds `go runStart(bg, p)` — the actual BMC bring-up (SOL attach,
     media insert, boot override, power cycle) — on
     `bg := context.WithoutCancel(ctx)`, a **detached** context so a client
     giving up mid-request can't abort BMC work already committed to
   - Returns `201` + job JSON (an earlier sketch of this diagram used `202`;
     the shipped handler predates this note and already used `201`, so
     that's what stayed)
   - `Orchestrator.Start` (synchronous: `prepareStart` + `runStart` inline,
     no goroutine) still exists and is what the CLI uses — its in-process
     orchestrator would otherwise exit before bring-up finished on a
     `deploy run` invoked without `-wait`
6. **`Orchestrator`** holds:
   - `mu sync.Mutex`
   - `active map[string]context.CancelFunc` (per-job cancel)
   - `JobStore` (durable CRUD + progress fields only)
   - `terminalCh` (or per-job notify) for DONE/ERROR/stall/cancel handling
7. **Per-job cancel:** `Cancel(jobID)` → `HandleTerminal(jobID, ReasonCancel)`:
   cancel context, cleanup with bounded timeout (e.g. 60s), transition
   `PROVISIONING → FAILED` (or intermediate cleanup bookkeeping) → `READY`
   when cleanup succeeds.
8. **Graceful shutdown:** `stop()` → `Server.Shutdown` → for each active job,
   cancel + cleanup with overall deadline → close DB.
9. **Panic isolation:** `runJob` wraps body in `defer recover()` that marks
   job `FAILED`, logs, and runs cleanup — panics must not kill the process.
10. **Per-BMC semaphore** lives on the Redfish client (capacity 1–2).
    Multiple jobs targeting the same BMC serialize HTTP; different BMCs run
    in parallel.
11. **SOL ownership:** at most one Observe SOL goroutine per node;
    `WatchRegistrar.Register` rejects dual ownership.
12. **HTTP timeouts:** `ReadHeaderTimeout` set; long operations never hold
    the request. Job status is poll-based (`GET /v1/jobs/{id}`); no
    long-poll requirement for MVP.
13. **WaitGroup vs errgroup:** use `sync.WaitGroup` + error logging for
    worker sets in MVP (no new dep). Add `golang.org/x/sync/errgroup` only
    if multi-error orchestration becomes painful (allow-list amendment).

---

## 4. Detailed Component Designs

### 4.1 Shoal Core (AI only — not the hybrid orchestrator)

**Technology**:
- Package: `internal/core`
- AI transport: thin client in `internal/core/ai` using `net/http` +
  `encoding/json`
- Structured I/O: shared types in `internal/common/models` with JSON tags;
  decode/validate pipeline in `internal/core/ai/decode` +
  `internal/common/validate`

**Key Interfaces** (illustrative):

```go
// internal/core/reconcile/reconcile.go
package reconcile

import (
    "context"
    "github.com/mattcburns/shoal/internal/common/models"
)

// Reconciler performs AI-only reconciliation when Discover's deterministic
// path is incomplete/ambiguous/spec-deviant. Core never imports Discover.
type Reconciler interface {
    ReconcileAsset(ctx context.Context, in ReconcileAssetInput) (models.NormalizationResult, error)
    ReconcileEvent(ctx context.Context, in models.RawEventInput) (models.NormalizedEvent, error)
}

type ReconcileAssetInput struct {
    // Already redacted — no passwords/tokens.
    RedactedRaw    map[string]any
    Partial        *models.NormalizedAsset // deterministic partial, may be nil
    PartialSources []models.FieldConfidence
}
```

```go
// internal/core/ai — LLM transport only
type LLM interface {
    Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
    CompleteVision(ctx context.Context, req VisionRequest) (CompletionResponse, error)
}

type CompletionRequest struct {
    Model       string
    System      string
    User        string
    // SchemaName selects a versioned schema blob under prompts/schemas/
    // (e.g. "normalization_result.v1") that is inlined into the prompt.
    SchemaName  string
    Temperature float64
    MaxTokens   int
}

type VisionRequest struct {
    CompletionRequest
    // ImageJPEG or ImagePNG bytes; max 4 MiB after compression for MVP.
    Image     []byte
    MediaType string // "image/jpeg" | "image/png"
}

type CompletionResponse struct {
    Content      string // raw model text (may include markdown fences)
    Model        string
    PromptTokens int
    OutputTokens int
    LatencyMS    int64
}
```

```go
// internal/core — profile generation (AI-assisted; Deploy still requires human
// approval for destructive steps)
type Profiler interface {
    GenerateProvisioningProfile(
        ctx context.Context,
        asset models.NormalizedAsset,
        requirements models.ProfileRequirements,
    ) (models.ProvisioningProfile, error)
}
```

#### AI structured-output pipeline (replaces Pydantic)

Enforced path for every AI call that returns domain data:

```
prompt (role + few-shot + schema text from prompts/schemas/<name>.json)
  → LLM.Complete / CompleteVision
  → decode.StripCodeFences(content)     // remove ```json ... ```
  → decode.ExtractJSONObject(content)   // first top-level { ... } if needed
  → json.Unmarshal into T
  → validate.T(...)
  → (Discover) conflict policy if reconciling assets
```

Helpers (stdlib only):

| Helper | Location | Behavior |
|--------|----------|----------|
| `StripCodeFences` | `internal/core/ai/decode` | Trim markdown fences |
| `ExtractJSONObject` | `internal/core/ai/decode` | Best-effort first JSON object |
| `DecodeJSON[T]` | `internal/core/ai/decode` | Unmarshal into `T` |
| `validate.NormalizationResult` | `internal/common/validate` | Asset + confidences + bounds |
| `validate.ProvisioningProfile` | `internal/common/validate` | Required fields; no secret keys |
| `redact.Map` | `internal/common/redact` | Drop password/token/secret keys before prompt |

**Validation rules:**
- `json.Unmarshal` into concrete structs (unknown JSON fields **ignored** by
  default — Go zero-value behavior; do not use a decoder
  `DisallowUnknownFields` for LLM output, which is often noisy; **do** use it
  for operator-facing API requests if desired).
- Confidence scores must be in `[0.0, 1.0]`.
- AI-sourced `FieldConfidence` requires non-empty `Evidence` (raw excerpt).
- `NeedsReview` must be true if any AI field conflicts with a deterministic
  value (Discover sets this after merge) or any confidence &lt; 0.6 on a
  required field.
- Reject payloads that still contain password-like keys after redaction
  (test).
- Malformed JSON → error returned to caller; no partial silent accept.

**Golden-test workflow:** for each prompt+schema version under `prompts/`,
store `testdata/golden/<name>/{input.json, partial.json, expected.json}`.
Changing a prompt or schema requires deliberate golden updates in the same
PR.

**Prompt Strategy**:
- All prompts live in repo-root `prompts/` (language-agnostic); schemas in
  `prompts/schemas/`; optionally `//go:embed` from `internal/core`.
- Few-shot examples as JSONL under `prompts/fewshot/`.
- Every prompt must include: task, output schema (inline schema text),
  examples, "think step by step", "only valid JSON", per-field confidence +
  evidence. (Full prompt-writing conventions live in `AGENTS.md` §6.)

### 4.2 Shoal Discover (hybrid pipeline owner)

**Why hybrid**: Vendor Redfish implementations deviate from spec. Pure
deterministic parsing breaks on deviations; pure-AI parsing pays
nondeterminism/latency/cost on the easy 90%.

**Pipeline (Discover-owned orchestration):**
1. **Deterministic fast path** — `internal/discover/adapters` parse Redfish
   JSON / CSV into partial `NormalizedAsset` + confidences
   (`source=deterministic`).
2. **Confidence gate** — accept only if required fields present, sanity
   checks pass (serial/MAC/IP, known model set), adapter matched.
3. **AI reconciliation (fallback)** — call `core.Reconciler.ReconcileAsset`
   with **redacted** raw + partial + schema. Core returns
   `NormalizationResult` from AI only.
4. **Merge & conflict policy (Discover)** — prefer deterministic values on
   conflict; set `NeedsReview=true` rather than silently trusting AI; union
   confidences.
5. **Vision path** — photos: no deterministic adapter; call `CompleteVision`
   via Reconciler; still schema-validated (cloud-preferred).
6. **Learning loop** — confirmed reconciliations append to few-shot store;
   stable patterns graduate into new Discover adapters later.

**Responsibilities**:
- Accept photo, Redfish dump, or CSV (`RawAssetInput`)
- Run hybrid pipeline; stash BMC credentials via `secrets.Backend` →
  `credential_ref` only on asset
- Write identity + `lifecycle_state` to NetBox
- Return device ID, confidences, `needs_review`

```go
// internal/discover/service.go (illustrative)
type Service struct {
    Adapters   []Adapter
    Reconciler reconcile.Reconciler // from core — AI only
    Secrets    secrets.Backend
    NetBox     netbox.ClientAPI
}

func (s *Service) Ingest(ctx context.Context, in models.RawAssetInput) (IngestResult, error) {
    // deterministic → gate → optional ReconcileAsset → merge → secrets → NetBox
    return IngestResult{}, nil
}
```

### 4.3 Shoal Observe

**Responsibilities**:
- Background polling of SEL and sensors → telemetry store
- Own SOL session during provisioning; parse status markers
- Call Core `Reconciler.ReconcileEvent` for messy SEL/sensor text when needed
- Expose status via API + CLI (`shoal observe status <device>`)
- OCR of graphics-only failure screens (approach: `Core.CompleteVision`, see
  §6)
- Watch mode: higher-frequency polling + live SOL tail

**SOL Status Marker Protocol** (live image → Observe):

```
SHOAL|<schema_ver>|<seq>|<iso8601_utc>|<phase>|<percent>|<state>|<detail>
```

- `schema_ver`: start at `1`
- `seq`: strictly increasing per job
- `phase`: `BOOT | DISK_PREP | IMAGE_WRITE | POSTINSTALL | VERIFY | DONE | ERROR`
- `percent`: `0`–`100`, or `-` when unknown
- `state`: `OK | WARN | ERROR | HEARTBEAT`
- `detail`: free text — **never secrets**

Example:
```
SHOAL|1|41|2026-06-19T04:10:11Z|IMAGE_WRITE|65|OK|writing rootfs to /dev/nvme0n1
SHOAL|1|42|2026-06-19T04:10:21Z|IMAGE_WRITE|-|HEARTBEAT|
SHOAL|1|43|2026-06-19T04:11:02Z|DONE|100|OK|reboot pending
```

**Parser rules**: accept only `SHOAL|1|...`; ignore console noise; treat
heartbeat gap beyond stall timeout (e.g. 90s) as stall. **Observe owns the
single SOL session** per node during a job.

#### Job progress (boundary-safe, no Deploy import)

Observe depends only on `internal/common/jobport` and `models.SOLMarker`. It
**never** imports `deploy`.

```go
// internal/common/jobport (illustrative) — Observe depends on this only
package jobport

type JobProgress interface {
    // ApplyMarker persists progress fields and, for terminal markers, notifies
    // the Orchestrator asynchronously. MUST NOT run Redfish cleanup inline.
    ApplyMarker(ctx context.Context, jobID string, m models.SOLMarker) error
    ReportStall(ctx context.Context, jobID string, reason string) error
    ReportTransportError(ctx context.Context, jobID string, err error) error
}
```

| Observe call | Synchronous effect (JobStore) | Async handoff (Orchestrator) |
|--------------|------------------------------|------------------------------|
| `ApplyMarker` (progress: BOOT…VERIFY, HEARTBEAT, OK/WARN) | Update `phase` / `percent` / `last_marker_seq` / `updated_at` only | none |
| `ApplyMarker` (`phase=DONE`, `state=OK`) | Update progress fields | enqueue `HandleTerminal(jobID, ReasonDoneOK)` |
| `ApplyMarker` (`phase=ERROR` or `state=ERROR`) | Update progress + soft error text | enqueue `HandleTerminal(jobID, ReasonMarkerError)` |
| `ReportStall` | optional progress note | enqueue `HandleTerminal(jobID, ReasonStall)` |
| `ReportTransportError` | optional progress note | enqueue `HandleTerminal(jobID, ReasonTransport)` |

Fields Observe may **propose** via progress writes: `phase`, `percent`,
`last_marker_seq`, soft `error` text.
Fields/transitions **only Orchestrator commits**: `state` (`LifecycleState`),
`attempt`, `sol_session_id`, cleanup completion, NetBox lifecycle writes.

**Marker-driven state machine (two phases):**

1. **Progress-only** — any non-terminal marker → `JobStore.UpdateProgress`
   only; job remains `PROVISIONING`.
2. **Terminal** — DONE/OK, ERROR, stall, transport failure, or cancel →
   `Orchestrator.HandleTerminal`:
   - cancel job context if needed
   - post-checks (DONE path only)
   - **always-run cleanup** (eject media + clear boot override)
   - `JobStore.Transition` to terminal state
   - unregister watch

| From | To | Trigger |
|------|----|---------|
| READY | PROVISIONING | `Start`: media inserted, boot override set, power on, watch registered |
| PROVISIONING | PROVISIONING | progress-only markers (phase/percent/seq) |
| PROVISIONING | PROVISIONED | `HandleTerminal(ReasonDoneOK)` after post-checks + cleanup succeed |
| PROVISIONING | FAILED | `HandleTerminal(ReasonMarkerError\|Stall\|Transport\|BMC\|Panic)` after cleanup |
| PROVISIONING | FAILED | **cancel** → `HandleTerminal(ReasonCancel)` after cleanup |
| FAILED | READY | cleanup complete + operator/reset path (automatic after successful cleanup bookkeeping may leave FAILED visible; Ready when device re-queued) |
| PROVISIONED | READY | re-provision request (deprovision) |

Explicit cancel: `PROVISIONING --cancel--> (cleanup) --> FAILED` (with
`error=canceled`), then device may return to READY when re-enqueued. There is
deliberately **no** separate long-lived `CLEANUP` lifecycle enum; cleanup is
a **mandatory finalizer** inside `HandleTerminal`, not a NetBox-facing state.

**Transport**: lab uses libvirt guest serial. Real hardware uses
`redfish_sol` → `BMC.OpenSOL` (line-oriented WS if actually SOL, else SSH
attach — Dell `console com2` even when `SerialConsole` is empty — else IPMI
2.0 SOL last resort, stdlib client, cipher suite 3 then 17). Field details
and vendor-specific findings live in `docs/sol-transports-design.md` and
`docs/real-hardware-sol-runbook.md`.

```go
// internal/observe/sol — parser returns models.SOLMarker (common), not deploy types
type Transport interface {
    Open(ctx context.Context, target string) (<-chan string, error)
    Close() error
}

func ParseLine(line string) (models.SOLMarker, bool) { return models.SOLMarker{}, false }
```

**BMC session management**: session-token auth (production), reuse/close,
cap concurrency ≈1–2, backoff on 4xx/5xx (`Retry-After`). Lab may use basic
auth (§4.5). Watch mode raises frequency but never exceeds per-BMC cap.

**Watch Mode Contract**: Orchestrator calls
`watchport.WatchRegistrar.Register(WatchSession)` (interface in `common`;
implementation in Observe). Observe tails SOL and calls
`jobport.JobProgress`. Progress for operators: **poll** `GET /v1/jobs/{id}`.
SSE/WebSocket remain out of scope until a real UX need appears.

### 4.4 Shoal Deploy

**Responsibilities**:
- Accept target binding + optional profile (CLI flags or NetBox device id)
- Optionally call `core.Profiler` (destructive steps require human approval
  before execution)
- Build live ISO (`os/exec` for `dracut`/`xorriso`); publish via lab ISO HTTP
  (§4.4.1)
- Redfish: insert Virtual Media, one-time boot override, power on/reboot
- Own **`JobStore` (persistence)** and **`Orchestrator` (state machine +
  cleanup)**; register watches via `watchport` only
- Success path: eject media, clear override, reboot final OS, update NetBox
  (when used)

**Reliability contract** (required):
- Idempotent steps (read BMC state, converge)
- Per-step timeouts + SOL stall detection
- Mandatory cleanup on success, failure, **and** cancel
- Cancel + **startup reconcile of durable orphan jobs**

#### JobStore vs Orchestrator (split roles)

**`JobStore`** = pure durable repository. **No Redfish. No Observe imports.
No cleanup side effects.**

```go
// internal/deploy/jobstore (illustrative) — pure persistence
type JobStore interface {
    Insert(ctx context.Context, job models.ProvisioningJob) error
    Get(ctx context.Context, id string) (models.ProvisioningJob, error)
    ListByState(ctx context.Context, state models.LifecycleState) ([]models.ProvisioningJob, error)
    UpdateProgress(ctx context.Context, jobID string, phase string, percent *int, seq int, errSoft string) error
    Transition(ctx context.Context, jobID string, to models.LifecycleState, errMsg string) error
}
```

**`Orchestrator`** owns lifecycle transitions, post-checks, Redfish cleanup,
and implements `jobport.JobProgress` by:
1. calling `JobStore.UpdateProgress` synchronously
2. on terminal conditions, non-blocking send to `terminalCh` /
   `go HandleTerminal(...)` — **never** runs cleanup inside the Observe call
   stack beyond the quick DB write + notify

```go
// internal/deploy/job (illustrative)
type Orchestrator interface {
    Start(ctx context.Context, req models.StartJobRequest) (models.ProvisioningJob, error)
    Cancel(ctx context.Context, jobID string) error
    HandleTerminal(ctx context.Context, jobID string, reason TerminalReason) error
    ReconcileOrphans(ctx context.Context) error
    Get(ctx context.Context, jobID string) (models.ProvisioningJob, error)
    ProgressPort() jobport.JobProgress // adapter for Observe injection
}

type TerminalReason string
const (
    ReasonDoneOK       TerminalReason = "done_ok"
    ReasonMarkerError  TerminalReason = "marker_error"
    ReasonStall        TerminalReason = "stall"
    ReasonTransport    TerminalReason = "transport"
    ReasonCancel       TerminalReason = "cancel"
    ReasonBMC          TerminalReason = "bmc"
    ReasonPanic        TerminalReason = "panic"
)
```

**Startup reconcile algorithm:**
1. On process start, `ListByState(PROVISIONING)`.
2. For each orphan: attempt to re-attach watch if BMC still mid-job **or**
   `HandleTerminal(ReasonBMC)` fail+cleanup if unrecoverable / flag
   `SHOAL_RECONCILE_FAIL_ORPHANS=true` (default true).
3. Log each reconcile decision.

#### 4.4.1 ISO artifact placement

Lab serves files via nginx on **`:8080`** from `shoal_iso_server_dir`
(typically `/srv/iso`).

**Default path:**
1. Build minimal marker ISO via lab scripts — **primary:** Ansible role on
   lab VM; **alternate:** developer workstation (see `AGENTS.md` §12).
2. **Copy/rsync** artifact into the lab ISO directory: e.g.
   `/srv/iso/shoal-marker.iso` (primary path may build in-place on the lab
   host).
3. BMC-reachable URL:
   - VM-hosted from L2 node path: `http://192.168.124.1:8080/shoal-marker.iso`
     (lab gateway)
   - Operator/API default host view:
     `http://192.168.122.100:8080/shoal-marker.iso`
   - Direct mode: `http://127.0.0.1:8080/shoal-marker.iso` (BMC nodes use
     lab-net gateway equivalent)
4. Pass that URL as `-iso-url` / `StartJobRequest.ISOURL`.

An in-process `net/http` file server remains optional/unused; ISO serving is
always via the lab's nginx or an equivalent BMC-reachable HTTP server.

#### 4.4.2 Device binding

Deploy can run **before** hybrid Discover/NetBox ingest — NetBox is not
required to start a job.

**Canonical lab CLI:**

```bash
shoal deploy run \
  -device-id lab-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -bmc-user "$SHOAL_BMC_USERNAME" \
  -bmc-pass "$SHOAL_BMC_PASSWORD" \
  -serial-target shoal-node-1 \
  -iso-url http://192.168.122.100:8080/shoal-marker.iso
```

| Flag | Maps to | Notes |
|------|---------|-------|
| `-device-id` | `ProvisioningJob.DeviceID` | Opaque string for correlation; need not exist in NetBox |
| `-bmc-url` | `ProvisioningJob.BMCEndpoint` | sushy-tools root (multi-system under one emulator is OK; client discovers Systems) |
| `-bmc-user` / `-bmc-pass` | `secrets.Backend` under a generated `credential_ref` | **Never log password**; may default from `SHOAL_BMC_*` env if flags omitted |
| `-serial-target` | `WatchSession.Target` | libvirt domain name for `virsh ttyconsole` / PTY path resolution |
| `-iso-url` | `ProvisioningJob.ISOURL` | Must be BMC-reachable; see §4.4.1 |
| `-system-id` | optional Redfish system id | Default first/only system if omitted |
| `-profile-ref` | optional | Empty/`spike` when no profile store entry is needed |

`StartJobRequest` carries these binding fields for API parity. An alternate
path may resolve `DeviceID` from NetBox + `credential_ref` only; that path is
optional, not required.

**Operator poll:** `shoal deploy status -job <id>` or `GET /v1/jobs/{id}`.

### 4.5 Shared infrastructure packages

#### Ports vs models

- **`internal/common/models`**: data structs + JSON tags only (including
  `SOLMarker`).
- **Neutral ports under `common`:** `jobport.JobProgress`,
  `watchport.WatchRegistrar` — **no** implementations that import
  observe/deploy.
- **Implementations:** `secrets.Backend`, `netbox.ClientAPI`, `redfish.BMC`,
  `telemetry.Store`, `deploy/jobstore.JobStore`, Deploy `Orchestrator`,
  Observe service.
- Discover/Deploy/Observe take interfaces as struct fields; **only
  `cmd/shoal` wires concrete types across siblings**.

#### Redfish client (gofish-backed, Shoal interfaces)

Shoal uses **`github.com/stmcginnis/gofish`** for Redfish sessions, Virtual
Media, power, and boot override — this is the one deliberate exception to
stdlib-first (§7.3(a) discusses the alternatives that were considered and
rejected).

**Boundary rule:** gofish types **must not** leak outside
`internal/common/redfish`. Call sites (Deploy, Observe) depend only on Shoal
interfaces and models. Implementation wraps gofish and maps to Shoal structs.

Lab smoke uses **HTTP basic auth** against sushy-tools (`force_basic_auth`).
SessionService is **not** proven by current smoke. Production BMCs typically
need sessions + often HTTPS with bad/self-signed certs. Configure
gofish/client for basic (lab) vs session (prod) via `Config.AuthMode`.

```go
// internal/common/redfish — illustrative API surface (gofish stays inside this package)
type Config struct {
    BaseURL            string
    Username           string
    Password           string // never logged
    AuthMode           string // "basic" | "session" — lab default basic
    TLSMode            string // "off" | "insecure" | "custom_ca"
    CAFile             string // when TLSMode=custom_ca
    MaxConcurrent      int    // default 1–2
    RequestTimeout     time.Duration
}

// BMC is the only Redfish surface Deploy/Observe import.
// Implementation uses gofish under the hood.
type BMC interface {
    Open(ctx context.Context) error
    Close(ctx context.Context) error
    ServiceRoot(ctx context.Context) (ServiceRoot, error)
    GetSystem(ctx context.Context, systemID string) (SystemInfo, error)
    GetBoot(ctx context.Context, systemID string) (BootInfo, error)
    SetBootOverrideOnceCD(ctx context.Context, systemID string) error
    ClearBootOverride(ctx context.Context, systemID string) error
    ListVirtualMedia(ctx context.Context, managerID string) ([]VirtualMedia, error)
    InsertVirtualMedia(ctx context.Context, mediaURI, imageURL string) error
    EjectVirtualMedia(ctx context.Context, mediaURI string) error
    Power(ctx context.Context, systemID, resetType string) error
    // Observe: GetSEL, GetSensors as needed
}

// NewBMC constructs the gofish-backed implementation.
func NewBMC(cfg Config) (BMC, error) { /* gofish connect + session/basic */ return nil, nil }
```

**Minimum Redfish operations (via gofish + wrappers):**

| Step | Redfish intent | Notes |
|------|----------------|-------|
| Connect | gofish client to ServiceRoot | Discover `@odata.id` links via library |
| Auth | Basic (lab) / session (prod) | On 401: re-auth once; then fail |
| System | Systems collection / by ID | PowerState, serial/model for adapters |
| Virtual Media | InsertMedia / EjectMedia | sushy-tools path may differ; capture fixtures; wrap gofish APIs |
| Boot override | one-time CD/DVD + clear | Read-before-write idempotency in wrapper |
| Power | Reset On / ForceRestart | Idempotent check PowerState first |
| Errors | Retry with backoff; honor `Retry-After` where exposed | Cap retries; per-BMC semaphore still in wrapper |

**TLS policy (`crypto/tls` / gofish HTTP client config):**
- Lab sushy-tools: plain HTTP (`TLSMode=off`).
- Real BMC HTTPS with self-signed: `TLSMode=insecure` **only** when
  explicitly configured (`SHOAL_REDFISH_TLS_MODE=insecure`); never default
  for cloud-facing clients.
- Preferred production: `custom_ca` with BMC enterprise CA file.
- Document risk of MITM on management network when using insecure mode.

**Idempotency:** every Deploy action reads current VirtualMedia.Inserted /
Boot override / PowerState (via gofish) and only mutates when drift exists.

**Fixture-first development:** record sushy-tools / gofish-visible JSON
under `testdata/redfish/sushy-tools/`; unit-test wrappers with fakes of
`BMC` and/or recorded HTTP where practical. Integration tests hit live
sushy-tools.

#### Secret backend

```go
package secrets

type Backend interface {
    Put(ctx context.Context, ref string, cred Credential) error
    Get(ctx context.Context, ref string) (Credential, error)
    Delete(ctx context.Context, ref string) error
}

type Credential struct {
    Username string
    Password string
}
```

File backend mode `0600` or env map for lab.

#### NetBox client

```go
package netbox

type ClientAPI interface {
    UpsertDevice(ctx context.Context, d models.DeviceIdentity) (models.DeviceIdentity, error)
    GetDevice(ctx context.Context, id string) (models.DeviceIdentity, error)
    SetLifecycleState(ctx context.Context, id string, state models.LifecycleState) error
}
```

#### Telemetry + jobs SQL store (Postgres-primary)

Lab runs **telemetry Postgres** on host port **5433**, DB `shoal_telemetry`,
user `shoal` (see `defaults.yml` / Compose). **This is the default app
database** for events, sensors, job logs, **and jobs**.

```sql
-- jobs: durable provisioning state (JobStore)
CREATE TABLE IF NOT EXISTS jobs (
  id               TEXT PRIMARY KEY,
  device_id        TEXT NOT NULL,
  profile_ref      TEXT NOT NULL DEFAULT '',
  state            TEXT NOT NULL,
  attempt          INT  NOT NULL DEFAULT 0,
  phase            TEXT,
  percent          INT,
  last_marker_seq  INT  NOT NULL DEFAULT 0,
  started_at       TIMESTAMPTZ,
  updated_at       TIMESTAMPTZ NOT NULL,
  error            TEXT,
  sol_session_id   TEXT,
  iso_url          TEXT,
  bmc_endpoint     TEXT
);

CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  type TEXT, severity TEXT, component TEXT,
  message TEXT, raw_ref TEXT
);

CREATE TABLE IF NOT EXISTS sensor_readings (
  device_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  sensor TEXT, value DOUBLE PRECISION, unit TEXT
);

CREATE TABLE IF NOT EXISTS job_log (
  job_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  line TEXT NOT NULL
);
```

- **Default driver:** `github.com/jackc/pgx/v5` via `database/sql`
  (`pgx/stdlib`) or pgx pool behind the store interface.
- **SQLite (`modernc.org/sqlite`):** optional **demo-only** offline path; not
  the lab default.
- DSN env: `SHOAL_TELEMETRY_DATABASE_URL`
  Example lab: `postgres://shoal:PASSWORD@192.168.122.100:5433/shoal_telemetry?sslmode=disable`

```go
package telemetry

type Store interface {
    // WriteEvent requires e.DeviceID (maps to events.device_id NOT NULL).
    WriteEvent(ctx context.Context, e models.NormalizedEvent) error
    WriteSensor(ctx context.Context, r SensorReading) error
    WriteJobLog(ctx context.Context, jobID string, ts time.Time, line string) error
    ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]Event, error)
}
// JobStore (deploy/jobstore) may share the same *sql.DB for the jobs table.
```

#### HTTP API (stdlib)

```go
// Routes (Go 1.22+ ServeMux patterns)
// GET  /healthz
// GET  /readyz          // optional dependency checks (DB ping)
// POST /v1/discover/ingest
// GET  /v1/devices/{id}/status
// POST /v1/jobs
// GET  /v1/jobs/{id}
// POST /v1/jobs/{id}/cancel
```

**API auth:** optional Bearer token (`SHOAL_API_TOKEN`); see §8. Bind to
management interface only (`SHOAL_HTTP_ADDR` e.g. `192.168.122.100:8088` or
`127.0.0.1:8088` in direct mode).

#### CLI (stdlib)

Subcommands: `serve`, `version`, `discover ingest`, `observe status`,
`deploy run`, `deploy status`, `deploy cancel`.

---

## 5. Data Models & Cross-Component Contracts

**Package:** `internal/common/models` (data only).

```go
package models

import "time"

// NormalizedAsset is identity + access metadata only.
// BMC username/password are NEVER stored here and NEVER sent to an LLM.
type NormalizedAsset struct {
    Serial        string `json:"serial"`
    Model         string `json:"model"`
    Vendor        string `json:"vendor"`
    BMCIP         string `json:"bmc_ip"`
    CredentialRef string `json:"credential_ref"`
}

type FieldConfidence struct {
    Field      string  `json:"field"`
    Confidence float64 `json:"confidence"` // 0.0–1.0
    Source     string  `json:"source"`     // "deterministic" | "ai"
    Evidence   string  `json:"evidence,omitempty"`
}

type NormalizationResult struct {
    Asset       NormalizedAsset   `json:"asset"`
    Confidences []FieldConfidence `json:"confidences"`
    NeedsReview bool              `json:"needs_review"`
}

type NormalizedEvent struct {
    DeviceID  string         `json:"device_id"` // required for telemetry.events.device_id correlation
    EventType string         `json:"event_type"`
    Severity  string         `json:"severity"`
    Component string         `json:"component"`
    Message   string         `json:"message"`
    Timestamp time.Time      `json:"timestamp"`
    // Raw must be redacted before any LLM call; may be empty in API responses.
    Raw map[string]any `json:"raw,omitempty"`
}

// SOLMarker is the parsed SHOAL|... protocol record (shared DTO; not observe-package-private).
type SOLMarker struct {
    SchemaVer int       `json:"schema_ver"`
    Seq       int       `json:"seq"`
    Timestamp time.Time `json:"timestamp"`
    Phase     string    `json:"phase"`
    Percent   *int      `json:"percent,omitempty"` // nil when "-"
    State     string    `json:"state"`             // OK | WARN | ERROR | HEARTBEAT
    Detail    string    `json:"detail,omitempty"`
}

type LifecycleState string

const (
    StateDiscovered   LifecycleState = "discovered"
    StateReady        LifecycleState = "ready"
    StateProvisioning LifecycleState = "provisioning"
    StateProvisioned  LifecycleState = "provisioned"
    StateFailed       LifecycleState = "failed"
)

type ProvisioningJob struct {
    ID            string         `json:"id"`
    DeviceID      string         `json:"device_id"`
    ProfileRef    string         `json:"profile_ref"`
    State         LifecycleState `json:"state"`
    Attempt       int            `json:"attempt"`
    Phase         string         `json:"phase,omitempty"`
    Percent       *int           `json:"percent,omitempty"`
    LastMarkerSeq int            `json:"last_marker_seq"`
    StartedAt     *time.Time     `json:"started_at,omitempty"`
    UpdatedAt     *time.Time     `json:"updated_at,omitempty"`
    Error         string         `json:"error,omitempty"`
    SOLSessionID  string         `json:"sol_session_id,omitempty"`
    ISOURL        string         `json:"iso_url,omitempty"`
    BMCEndpoint   string         `json:"bmc_endpoint,omitempty"`
}

// RawAssetInput — Discover ingest (API/CLI).
type RawAssetInput struct {
    Kind string `json:"kind"` // "redfish_json" | "csv" | "photo"

    // Exactly one payload depending on Kind:
    RedfishJSON map[string]any `json:"redfish_json,omitempty"`
    CSVRow      map[string]string `json:"csv_row,omitempty"`
    // PhotoBase64 is JPEG/PNG; max decoded size 4 MiB.
    PhotoBase64 string `json:"photo_base64,omitempty"`

    // Optional operator-supplied BMC access for first ingest (stored in secrets,
    // never copied into NormalizedAsset password fields).
    BMCUsername string `json:"bmc_username,omitempty"`
    BMCPassword string `json:"bmc_password,omitempty"` // accepted once; not logged; not returned
    BMCIP       string `json:"bmc_ip,omitempty"`
}

// RawEventInput — Observe → Core event reconciliation.
type RawEventInput struct {
    DeviceID  string         `json:"device_id"`
    Source    string         `json:"source"` // "sel" | "sensor" | "sol" | "ocr"
    Timestamp time.Time      `json:"timestamp"`
    Message   string         `json:"message"`
    Raw       map[string]any `json:"raw,omitempty"` // redact before LLM
}

// ProfileRequirements — non-secret operator intent for profile generation.
type ProfileRequirements struct {
    OSFamily     string            `json:"os_family"` // e.g. "ubuntu"
    OSVersion    string            `json:"os_version,omitempty"`
    Hostname     string            `json:"hostname,omitempty"`
    Extra        map[string]string `json:"extra,omitempty"` // no password keys allowed
    AllowDestruct bool             `json:"allow_destruct"`  // human gate
}

// ProvisioningProfile — schema-validated AI or operator profile.
type ProvisioningProfile struct {
    Ref              string   `json:"ref"`
    ISOBase          string   `json:"iso_base"`
    EmbeddedPayload  string   `json:"embedded_payload,omitempty"`
    PostInstallSteps []string `json:"post_install_steps,omitempty"`
    // DestructSteps require AllowDestruct + explicit operator approval before Deploy runs them.
    DestructSteps []string `json:"destruct_steps,omitempty"`
    NeedsApproval bool     `json:"needs_approval"`
}

// DeviceStatus — Observe aggregate view.
type DeviceStatus struct {
    DeviceID       string         `json:"device_id"`
    LifecycleState LifecycleState `json:"lifecycle_state"`
    PowerState     string         `json:"power_state,omitempty"`
    LastEvent      string         `json:"last_event,omitempty"`
    ActiveJobID    string         `json:"active_job_id,omitempty"`
    Phase          string         `json:"phase,omitempty"`
    Percent        *int           `json:"percent,omitempty"`
    UpdatedAt      time.Time      `json:"updated_at"`
}

// WatchSession — Observe registration during a job.
type WatchSession struct {
    ID           string    `json:"id"`
    JobID        string    `json:"job_id"`
    DeviceID     string    `json:"device_id"`
    Transport    string    `json:"transport"` // "libvirt" | "redfish_sol"
    Target       string    `json:"target"`    // console path or BMC URI
    StartedAt    time.Time `json:"started_at"`
    StallTimeout time.Duration `json:"stall_timeout"` // e.g. 90s
}

// DeviceIdentity — NetBox-facing identity fields.
type DeviceIdentity struct {
    ID             string         `json:"id,omitempty"`
    Name           string         `json:"name,omitempty"`
    Serial         string         `json:"serial"`
    Vendor         string         `json:"vendor,omitempty"` // Redfish Manufacturer; empty → lab virtual defaults
    Model          string         `json:"model,omitempty"`  // Redfish Model → NetBox device type
    LifecycleState LifecycleState `json:"lifecycle_state"`
    CredentialRef  string         `json:"credential_ref"`
    BMCIP          string         `json:"bmc_ip"`
}

// API DTOs
type StartJobRequest struct {
    DeviceID     string `json:"device_id"`
    ProfileRef   string `json:"profile_ref,omitempty"`
    ISOURL       string `json:"iso_url"`
    BMCEndpoint  string `json:"bmc_endpoint"`            // Redfish base URL
    BMCUsername  string `json:"bmc_username,omitempty"`  // stored to secrets; not logged
    BMCPassword  string `json:"bmc_password,omitempty"`  // stored to secrets; never returned
    SerialTarget string `json:"serial_target"`           // libvirt domain or SOL target
    SystemID     string `json:"system_id,omitempty"`
    CredentialRef string `json:"credential_ref,omitempty"` // alt: pre-seeded secret (skip user/pass)
    // ApproveDestruct is operator consent for NeedsApproval / DestructSteps.
    // Does not bypass a missing profile store entry; only supplies consent at Start.
    ApproveDestruct bool `json:"approve_destruct,omitempty"`
}

type CancelJobRequest struct {
    JobID string `json:"job_id"`
}
```

**Redaction rules for raw maps:** before LLM or slog debug of bodies, run
`redact.Map` which recursively strips keys matching case-insensitive
`password`, `passwd`, `secret`, `token`, `authorization`, `api_key`,
`bmc_password`. `RawAssetInput.BMCPassword` / `StartJobRequest.BMCPassword`
are written only to `secrets.Backend` and never to logs or NetBox.

**Validation helpers (non-exhaustive):** `validate.NormalizedAsset`,
`validate.NormalizationResult`, `validate.NormalizedEvent` (requires
`DeviceID`), `validate.ProvisioningProfile`, `validate.RawAssetInput`,
`validate.StartJobRequest` (requires `BMCEndpoint`, `ISOURL`,
`SerialTarget`).

**Provisioning state machine** (summary; detail + cancel in §4.3):

| From | To | Trigger |
|------|----|---------|
| DISCOVERED | READY | Asset normalized + stored, credentials resolvable |
| READY | PROVISIONING | `Start`: media inserted, one-time boot override set, power on, watch registered |
| PROVISIONING | PROVISIONING | Progress-only markers |
| PROVISIONING | PROVISIONED | `HandleTerminal(DoneOK)` after post-checks + cleanup |
| PROVISIONING | FAILED | `HandleTerminal` on ERROR/stall/transport/BMC/panic/**cancel** after cleanup |
| FAILED | READY | Device re-queued / operator reset after cleanup |

**Only Deploy Orchestrator commits `LifecycleState` transitions** (via
`JobStore.Transition`). Observe proposes progress via `jobport.JobProgress`
only.

**NetBox Integration:** SoT for device identity + current `lifecycle_state`
only. Custom fields: `shoal_lifecycle_state`, `shoal_credential_ref`. No
time-series in NetBox.

---

## 6. AI Layer & Prompt Engineering (Design)

For prompt-writing conventions (schema-first JSON, few-shot examples,
redaction, the decode/validate pipeline) see `AGENTS.md` §6 — this section
covers the model-routing design those conventions run on top of.

**Lab / host model strategy:**

Reference operator host may be modest (e.g. older Xeon, ~32 GB RAM, mobile
Quadro ~4 GB VRAM). Nested VM lab Ollama is often **CPU-bound** (no GPU
passthrough). Prefer small text models; vision OCR needs a real OCR VLM (not
a caption-only toy).

| Role | Env var | Lab default | Notes |
|------|---------|-------------|--------|
| **Text / structured JSON** | `SHOAL_AI_MODEL` | `llama3.2:3b` | Hybrid `ReconcileAsset` / text paths; instruct/completion model |
| **Vision / asset-label photos** | `SHOAL_AI_VISION_MODEL` | **`deepseek-ocr`** | Discover photo path; Free OCR on labels. Empty env → no local vision. **`moondream` is not AC-grade** |
| **Graphics failure-screen OCR** | — | `Core.CompleteVision`, no separate model default | Distinct prompt from asset-label OCR; candidates were Tesseract vs cloud/VLM vision — resolved in favor of `CompleteVision` |

Optional text upgrade if operators want stronger JSON adherence:
`qwen2.5:3b` (same size class). **Do not** use `deepseek-ocr` as the **text**
hybrid default — it is OCR-first, not a general JSON reconciler.

**Call routing** (`internal/core/ai`):

```
Complete(ctx, req)
  → model = req.Model if non-empty, else SHOAL_AI_MODEL
  → Ollama: POST {SHOAL_OLLAMA_URL}/api/chat (format=json for structured text)
  → Cloud:  POST {SHOAL_CLOUD_AI_BASE_URL}/chat/completions + Bearer token

CompleteVision(ctx, req)
  → model = req.Model if non-empty, else SHOAL_AI_VISION_MODEL if non-empty, else error
  → if photo path and no usable vision model → clear error (do not silently drop the image)
  → Ollama: prefer POST {url}/api/generate with images (OCR VLMs); fall back to /api/chat
  → Do not force format=json on vision (breaks small VLMs)
  → Cloud: OpenAI-compatible chat with image content blocks

Discover text (redfish_json / csv AI fallback)  → Complete only
Discover photo                                 → CompleteVision ("Free OCR.") then parse SERIAL/VENDOR/MODEL
  (never send photo bytes through text-only Complete)
  If serial cannot be extracted from OCR → fail (no synthetic photo-unknown serials)
```

Operators may use cloud multimodal models by setting provider/model env
vars. Local photo AC is validated with **`deepseek-ocr`** Free OCR, not
caption models.

**AI client configuration** (`internal/core/ai`), rendered by Ansible
`compose_stack` / app env:

| Env var | Purpose |
|---------|---------|
| `SHOAL_AI_PROVIDER` | `ollama` \| `cloud` |
| `SHOAL_AI_MODEL` | Text / default model name (lab: `llama3.2:3b`) |
| `SHOAL_AI_VISION_MODEL` | Vision/OCR model for `CompleteVision` (lab: `deepseek-ocr`; empty allowed to skip photo) |
| `SHOAL_OLLAMA_URL` | Local Ollama base URL |
| `SHOAL_CLOUD_AI_BASE_URL` | OpenAI-compatible base when cloud |
| `SHOAL_CLOUD_AI_API_KEY` | Vault secret; never log |

Implementation notes:
- Ollama text: `POST {url}/api/chat` with `format=json` when structuring
- Ollama vision: prefer `POST {url}/api/generate` with `images` + short OCR
  prompt (`Free OCR.`); chat fallback; **no** vision `format=json`
- Cloud: `POST {base}/chat/completions` + Bearer token; vision uses image
  content parts
- Log: prompt hash/version, **resolved model name**, tokens, latency — **no
  secrets**, no full raw photo bytes
- `http.Client` with timeouts; `NewRequestWithContext`
- Lab Ansible: `shoal_ai_model` + `shoal_ai_vision_model` pulled by
  `compose_stack`; smoke asserts both appear in `/api/tags` when vision is
  non-empty

---

## 7. Technology Stack & Library Choices

### 7.1 External dependency allow-list

**Rule**: if it is not on this list, do not add it without updating this
section in the same PR (and updating `NOTICE` /
`docs/third-party-licenses.md` per `AGENTS.md` §9.1).

| Dependency | Kind | Why not avoidable with stdlib alone |
|------------|------|-------------------------------------|
| `github.com/stmcginnis/gofish` | runtime (required) | Redfish sessions, Virtual Media, boot override, power — adopted **day one** so Phase 2 is not blocked on a greenfield Redfish client. Wrapped behind `internal/common/redfish`; types do not leak to call sites. |
| `github.com/jackc/pgx/v5` | runtime (default) | Postgres driver for lab telemetry/jobs DB on `:5433` |
| `modernc.org/sqlite` | runtime (optional demo) | Pure-Go SQLite if someone runs without Compose; not required for lab ACs |
| `github.com/coder/websocket` | runtime (required) | Real Redfish SOL transport: native WebSocket SOL dial for recognized BMC vendors (`internal/common/redfish/sol.go`); gofish has no client-side streaming/WebSocket support. Context-native `Read`/`Close` fits the cancellation-bounded `sol.Transport` contract. |
| `golang.org/x/crypto` (`ssh` subpackage) | runtime (required) | Real-hardware SOL: SSH attach for `OpenSOL` when Redfish SerialConsole advertises SSH **or** (Dell) NetworkProtocol/OEM serial-redirection is enabled. Password and keyboard-interactive auth against BMC credentials (iDRAC offers KI only). Already an indirect transitive dep (gofish/pgx); used in `internal/common/redfish/sol.go`. Never used for IPMI or BMC control. IPMI 2.0 SOL is stdlib-only (`net`, `crypto/sha1`, `crypto/sha256`, `crypto/aes`, …) in `internal/common/redfish/internal/ipmi` — **no new module**. |
| `honnef.co/go/tools/cmd/staticcheck` | toolchain | Static analysis beyond `go vet`; not linked into binary |

**Toolchain install (see `AGENTS.md` §3.2 for the pinned version):**
```bash
go install honnef.co/go/tools/cmd/staticcheck@<pinned-version>
# or module tools.go pattern:
#   //go:build tools
#   import _ "honnef.co/go/tools/cmd/staticcheck"
```

**Explicitly rejected:**

| Rejected | Reason |
|----------|--------|
| Cobra | Few subcommands; `flag` enough |
| Gin / Echo / Chi / Fiber | ServeMux enough |
| LiteLLM / official OpenAI SDKs | Thin `net/http` client |
| Greenfield thin Redfish `net/http` client | gofish is the chosen Redfish stack (§4.5) |
| ORMs | Hand-written SQL for four tables |
| Celery / Redis as app queue | Single-process goroutines; lab Redis is for NetBox |
| Python app packages | App is Go; sushy-tools is lab-only |
| `go-playground/validator` / JSON Schema libs | Hand validation sufficient for MVP domain surface |

**Optional later (require allow-list amendment):**
- `golang.org/x/sync/errgroup` — multi-error worker orchestration
- `golang.org/x/term` — interactive secret prompts
- Vector DB client — only if file few-shot RAG fails

### 7.2 Stack table

| Layer | Choice | Why |
|-------|--------|-----|
| Language | Go 1.22+ | Static binary, concurrency, stdlib |
| Module | `github.com/mattcburns/shoal` | Confirmed product path |
| Web / API | `net/http` ServeMux | Stdlib |
| CLI | `flag` + switch | Zero deps |
| Models | Structs + `encoding/json` | Stdlib |
| Validation | `internal/common/validate` + `core/ai/decode` | No Pydantic equivalent needed |
| Redfish | **gofish** behind `internal/common/redfish` | Day-one Virtual Media/session parity; interfaces hide library |
| AI | `internal/core/ai` | Ollama + OpenAI-compatible |
| Image building | `os/exec` → dracut/xorriso | Host tools |
| NetBox | REST `net/http` | Small API subset |
| Jobs + telemetry | Postgres via pgx + `database/sql` | Matches lab `telemetry-db` |
| Secrets | File/env vault | MVP |
| Background | Goroutines + context | §3.2 lifecycle |
| Logging | `log/slog` | Stdlib; level via `SHOAL_LOG_LEVEL` |
| Testing | `testing` | Table-driven |
| OCR (graphics failure screens) | `Core.CompleteVision` | See §6 |
| BMC TLS | `crypto/tls` + gofish client config | Policy §4.5 |
| App listen | **`:8088`** | Confirmed |

### 7.3 Alternatives considered

#### (a) Redfish: thin client vs gofish day one

| Option | Pros | Cons |
|--------|------|------|
| Thin `net/http` client | Zero Redfish deps; full control | Delays Phase 2 on VM/session edge cases |
| **gofish from day 1 (chosen)** | Faster Virtual Media/session coverage; less greenfield risk on critical path | Extra dep; must wrap to avoid type leakage |
| Hybrid wrap gofish later | Same call-site interface eventually | Dual maintenance; spike schedule risk |

**Decision:** **gofish day one**, wrapped behind Shoal `BMC` interfaces. An
earlier draft of the design had deferred gofish behind a thin-client
timebox; that plan was reversed because the Redfish session/Virtual Media
surface was the Phase 2 critical path. Stdlib-first remains the default for
everything except Redfish.

#### (b) Postgres-only vs SQLite dual-path

| Option | Pros | Cons |
|--------|------|------|
| Postgres-primary (chosen) | Matches lab `telemetry-db :5433`; one schema path; durable jobs | Requires Compose/lab or external PG |
| SQLite default | Zero ops for laptop | Diverges from lab; another driver; reconcile semantics differ |
| Dual first-class | Flexible | Double CI matrix; agent confusion |

**Decision:** **Postgres-primary**. SQLite is optional demo-only behind the
same `JobStore`/`Store` interfaces.

#### (c) Watch progress: poll vs SSE/WebSocket

| Option | Pros | Cons |
|--------|------|------|
| Poll `GET /v1/jobs/{id}` (chosen) | Trivial; stdlib; enough for CLI | Slight lag |
| SSE | Push with one response stream | More handler code; still single-process |
| WebSocket | Bi-directional | Overkill |

**Decision:** poll-only; push transports only if UX demands it later.

#### (d) Validation: hand-rolled vs libraries

| Option | Pros | Cons |
|--------|------|------|
| Hand-rolled (chosen) | Zero deps; domain-specific rules | More code |
| go-playground/validator | Tags | Dep + less control over AI messy JSON |
| JSON Schema lib | Shared with prompts | Heavier; still need fence stripping |

**Decision:** hand-rolled decode+validate pipeline (§4.1).

Current repository layout is documented in `AGENTS.md` §2 (kept there so
there is exactly one copy that tracks the tree as it evolves).

---

## 8. Operational Architecture Notes

Day-to-day security/operational conventions (secrets, logging, auth,
licensing) live in `AGENTS.md` §9. The items below are architecture-level
rationale that don't fit as a convention statement:

- **No HTTPS-everywhere for ISO.** Virtual Media is served over plain HTTP
  on the management segment. Many BMCs reject self-signed Virtual Media
  URLs outright, so "just add TLS" isn't a free move; a BMC-trusted CA
  (`custom_ca` mode, §4.5) is the real fix and remains a hardening item, not
  a default.
- **Trust boundary for the ISO host.** The management segment itself is the
  trust boundary — anything that can reach the ISO HTTP server or the
  unauthenticated (or token-protected) `/v1/*` API is assumed to be a
  legitimate operator or automation on that segment. Operators must not
  expose ISO HTTP or the API beyond that segment.
- **Liveness/readiness:** `/healthz` is a pure liveness check; `/readyz`
  optionally pings the telemetry DB. Metrics (`/metrics`, stdlib Prometheus
  text exposition) and the optional `SHOAL_API_TOKEN` bearer-auth gate are
  the extent of hardening scoped for now; full mTLS/OIDC and distributed
  tracing are explicitly out of scope until a real need appears.

---

## 9. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Virtual Media throughput | Medium | Medium | Lean live image |
| Spec-deviant Redfish | High | Medium | gofish wrappers + hybrid AI + fixtures |
| gofish quirks / API drift | Medium | Medium | Thin Shoal `BMC` facade; fixtures; pin gofish version in `go.mod` |
| BMC session lockout | High | Medium | Caps + backoff + reuse |
| SOL transport variance | Medium | Medium | `sol.Transport`; lab libvirt; HW stretch |
| Secrets → LLM | Medium | High | Redact + tests |
| Vision VRAM | High | Medium | Cloud vision |
| sushy-tools fidelity | High | Medium | Document gaps; validate on real hardware where possible |
| Prompt drift | Medium | High | Versioned prompts + goldens |
| Dependency creep | Medium | Medium | Allow-list |
| Job loss on restart | Medium | High | Durable `jobs` table + reconcile |
| Unauthenticated/under-authenticated API abuse on mgmt | Medium | Medium | Bind interface; optional bearer token |

---

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Language | Go 1.22+ | Binary, concurrency, stdlib |
| Module path | **`github.com/mattcburns/shoal`** | Confirmed |
| HTTP | `net/http` ServeMux | No framework needed |
| CLI | `flag` + switch | Few commands |
| Hybrid ownership | **Discover adapters+gate+merge; Core Reconciler AI-only** | Prevents core↔discover import cycle; matches boundaries |
| Validation | Hand decode+validate pipeline | Replaces Pydantic without new deps |
| AI client | Thin `net/http` | Ollama + OpenAI-compatible |
| Redfish | **gofish day one** behind `internal/common/redfish` | Phase 2 critical path; stdlib-first elsewhere |
| Job mutation | **Orchestrator sole lifecycle writer**; Observe → `jobport` only | Clear boundary; no Observe↔Deploy imports |
| JobStore role | **Pure persistence** (CRUD/progress/transition SQL) | Cleanup/Redfish stay in Orchestrator.HandleTerminal |
| Sibling wiring | **Neutral ports + composition root** | Avoids Go import cycles |
| Device binding | **CLI flags** (bmc-url, creds, serial-target, iso-url) | Discover/NetBox optional |
| ISO serving | **Lab nginx `:8080`** after copy to ISO dir | Matches existing lab; no in-process server required |
| Live image build host | **Lab VM Ansible primary**; workstation alternate | Reproducibility + local iteration |
| Persistence | **Postgres jobs+telemetry** on lab `:5433` | Durable orphans; match Compose |
| Watch UX | Poll job status | Simplicity |
| App port | **`:8088`** | Confirmed `shoal_app_http_port` / `SHOAL_HTTP_ADDR` |
| API auth | Optional Bearer token; open if unset | Lab default; explicit threat model |
| Graphics OCR | `Core.CompleteVision` | Not Tesseract-first; distinct from asset-label OCR |
| Concurrency | Root ctx, per-job child ctx, mutex map, cleanup on shutdown | Avoid races/leaks |
| Packaging | Single binary | Distribution goal |
| Lab | Ansible + sushy-tools | Language-agnostic, already proven |
| UI | Deferred | API+CLI only |

---

## Roadmap

See [`docs/plans/roadmap.md`](../plans/roadmap.md) for current phase status
and links to the executable per-phase plans. This document does not restate
phase-by-phase task lists — those live in the phase plan docs so there is one
place to track progress.

---

## References

- Lab: `docs/lab-runbook.md`, `infra/ansible/`, README endpoints
- Compose telemetry DB: port `5433`, DB `shoal_telemetry`
- Module: `github.com/mattcburns/shoal`
- gofish: `github.com/stmcginnis/gofish`
- Go ServeMux 1.22+ routing enhancements
- DMTF Redfish; Ollama / OpenAI-compatible HTTP APIs
- Working conventions: [`AGENTS.md`](../../AGENTS.md)
- Phase status: [`docs/plans/roadmap.md`](../plans/roadmap.md)

---

## Appendix: Document History

Condensed from the original monolith's version-by-version changelog (full
detail lives in git history):

- **v1.1 → v2.0 (Go rewrite):** full application rewrite from Python to Go,
  maximizing stdlib except for Redfish (gofish adopted day one). Module path
  `github.com/mattcburns/shoal`; app HTTP port `:8088`; single static binary
  replaces FastAPI/Typer/Pydantic/LiteLLM/sushy.
- **v2.0.1–v2.0.2 (review revisions):** fixed hybrid ownership (Discover
  deterministic + Core AI-only reconciler) so Core never imports Discover;
  broke the Observe↔Deploy import cycle via neutral `jobport`/`watchport`
  ports; established JobStore-as-pure-persistence with Orchestrator as sole
  lifecycle writer; Phase 2 device binding via CLI flags (NetBox optional).
- **v2.0.3:** gofish adopted day one (no thin-client-first timebox); app
  port `:8088` confirmed; live-image build host documented as lab-VM
  (primary) + workstation (alternate).
- **v2.0.4–v2.0.5:** dual-model AI contract — separate text
  (`SHOAL_AI_MODEL`) and vision (`SHOAL_AI_VISION_MODEL`) routing; lab vision
  default settled on `deepseek-ocr` (rejecting `moondream` as not AC-grade
  for inventory OCR).
- **v2.0.6 (Phase 6c):** multi-platform CGO-free release binaries; macOS
  documented as operator-only (not an L0 hypervisor host); L0 VM-hosted lab
  gained a Fedora secureblue/Atomic profile alongside classic Linux.
- **v2.0.7 (Phase 6d):** optional Compose `shoal` service; optional Bearer
  `SHOAL_API_TOKEN` auth for `/v1/*`; stdlib Prometheus text `/metrics`;
  record/replay fixture tests under `testdata/redfish/`.
- **v2.0.8:** promoted full distro autoinstall to a numbered Phase 7 (7a
  Ubuntu E2E → 7b profile/artifact model → 7c second family), explicitly
  distinct from Phase 6a's bounded payload-write MVP.
- **v2.0.9:** Phase 7a closed via the nested-lab Ubuntu cloud-image-write
  path (cloud image → customize → gzip → marker ISO → Virtual Media → SOL
  markers → `gunzip|dd` → reboot). 7b/7c deferred pending a separate
  multi-stage-provisioning design (now
  [`docs/multi-stage-provisioning-design.md`](../multi-stage-provisioning-design.md)).
