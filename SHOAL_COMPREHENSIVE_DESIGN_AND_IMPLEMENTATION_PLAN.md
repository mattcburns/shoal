# Shoal: Comprehensive Design Document & Phased Implementation Plan

**Version:** 2.0.9  
**Date:** July 2026  
**Status:** Draft (post-review; open questions resolved)  
**Author:** (architect / AI agent)  
**Intended Audience:** Human architect + team of AI coding agents

**Purpose of this document**  
This is a self-contained, implementation-ready design and plan. It is written to be directly consumable by AI coding agents (Claude, Cursor, Devin-style agents, etc.). It contains:

- Clear principles and constraints  
- Modular architecture with well-defined interfaces  
- Data models and API contracts  
- Technology decisions with rationale (**stdlib-first except Redfish → gofish**)  
- Explicit external dependency allow-list  
- Detailed phased plan with acceptance criteria  
- Prompt engineering and RAG guidelines  
- Testing, security, and operational requirements  
- Instructions on how agents should use this document  

**Product continuity**  
This is a **language/stack revision** of the v1.1 product design — not a greenfield product redesign. Vision, component boundaries, hybrid normalization, SOL marker protocol, provisioning state machine, lab environment (Ansible + sushy-tools), security principles, and phased plan structure are preserved. What changes is the application implementation language, package layout, interfaces, tooling, and agent conventions.

**UI note (v2.0 delta vs v1.1):** v1.1 mentioned “API + simple UI” for Observe status. **MVP ships API + CLI only**; any browser UI is deferred past Phase 6 unless product prioritizes it. Operators use `shoal observe status`, `shoal deploy run`, and HTTP JSON.

**Code samples in this document** are **illustrative sketches** (may omit imports or use abbreviated names). Implementers must produce compiling code; do not copy sketches as-is.

---

## Changes in v2.0 / v2.0.1 / v2.0.2 / v2.0.3 / v2.0.4 / v2.0.5 / v2.0.6 / v2.0.7 / v2.0.8 / v2.0.9

Full stack rewrite of the **application** from Python to **Go (Golang)**, maximizing the Go standard library **except where Redfish complexity justifies gofish**.

| Area | v1.1 (Python) | v2.0.9 (Go) |
|------|---------------|-------------|
| Language | Python 3.11+ | Go 1.22+ (prefer latest stable) |
| Module path | n/a | **`github.com/mattcburns/shoal`** |
| Process model | One FastAPI process + asyncio tasks | One Go process + goroutines + `context.Context` |
| HTTP API | FastAPI | `net/http` + Go 1.22+ `ServeMux` patterns |
| CLI | Typer | `flag` + small stdlib-style subcommand dispatcher |
| Models / validation | Pydantic | Exported structs + `encoding/json` + validation helpers |
| AI abstraction | LiteLLM | Thin provider client over `net/http` (Ollama + OpenAI-compatible cloud) |
| Redfish client | `sushy` (Python); **gofish rejected** (wrong language) | **`gofish` from day one**, wrapped behind `internal/common/redfish` interfaces (stdlib-first elsewhere; deliberate exception for Redfish — see §4.5 / §7) |
| NetBox client | pynetbox / httpx | NetBox REST via `net/http` |
| Telemetry / jobs DB | Postgres/SQLite via Python | `database/sql` + **Postgres-primary** (lab `:5433` / `shoal_telemetry`); SQLite demo-only |
| Logging | (Python logging) | `log/slog` |
| Testing | pytest + pytest-asyncio | `testing` package; table-driven tests |
| Format / lint | ruff | `gofmt`, `go vet`, `staticcheck` |
| Packaging | Docker + optional PyInstaller | Single (mostly) static Go binary + Docker |
| Layout | `shoal_core` / `shoal_common` Python packages | Go module monorepo: `cmd/`, `internal/` |
| Operator UI | API + simple UI (mentioned) | **API + CLI only** for MVP (UI deferred) |
| App HTTP port | n/a | **`:8088`** (`shoal_app_http_port` / `SHOAL_HTTP_ADDR`) |

**v2.0.1 (review revision):** hybrid ownership fixed (Discover deterministic + Core AI-only reconciler); architecture diagram matches import direction; Deploy-owned job lifecycle with Observe → progress ports; durable `jobs` table + app config contract; Phase-2-ready Redfish detail; full AI decode/validate pipeline; complete shared models; PR plan reordered (AI off spike critical path; live-image PR); concurrency lifecycle; alternatives tables; resolved open questions that the body already answered.

**v2.0.2 (residual review):** break Observe↔Deploy import cycle — ports/DTOs in neutral `common` packages; composition root wires interfaces; `JobStore` = pure persistence; Orchestrator owns transitions/cleanup via notify channel; Phase 2 device binding via CLI flags (Discover optional); marker-driven state table + explicit cancel; Phase 2 ISO via lab nginx `:8080`; `DeviceID` on `NormalizedEvent`; PR3/Phase 1 naming clarity.

**v2.0.3 (user decisions):** module path `github.com/mattcburns/shoal`; **gofish adopted day one** (no thin-client-first / 5-day exit); app port **`:8088` confirmed**; Phase 6 OCR approach **deferred** (Tesseract vs cloud vision evaluated in Phase 6); live-image build host **both** paths documented (lab VM Ansible **primary**, workstation alternate).

**v2.0.4 (Phase 3 AI contract):** dual-model local AI — text (`SHOAL_AI_MODEL`) vs vision (`SHOAL_AI_VISION_MODEL`); explicit `Complete` / `CompleteVision` routing; nested-lab-friendly defaults.

**v2.0.5 (photo vision model):** lab vision default is **`deepseek-ocr`** (Free OCR on asset labels; parse SERIAL/VENDOR/MODEL). Rejects placeholder serials. **`moondream` is not AC-grade** for inventory OCR. Text hybrid remains `llama3.2:3b` — do **not** use `deepseek-ocr` as the text model. Phase 6 graphics failure-screen OCR remains deferred (Tesseract vs cloud vision).

**v2.0.6 (Phase 6c packaging + L0 hosts):** multi-platform **CGO-free** release binaries (linux/darwin amd64/arm64) via scripts + GHA; ship `LICENSE`/`NOTICE`/`docs/third-party-licenses.md` with artifacts; **macOS is an operator host** (binary + remote/Linux lab) — not an L0 nested hypervisor. **L0 VM-hosted lab** supports classic Linux and **Fedora secureblue** (detect profile, modular libvirt, firewalld; keep ufw path). Direct-host lab on secureblue and nested lab on macOS remain **out of scope**. Compose `shoal` service image, API auth, metrics, record/replay CI → **Phase 6d+**.

**v2.0.7 (Phase 6d ops packaging):** optional **Compose `shoal` service** in the lab stack (binary image, env from Ansible); **Bearer API token** auth for `/v1/*` when `SHOAL_API_TOKEN` set (health/ready/metrics remain open); **stdlib Prometheus text `/metrics`** (job + HTTP counters, no new deps); **record/replay fixture tests** under `testdata/redfish/` wired into unit CI. Extra OEM screenshot adapters remain hardware-driven.

**v2.0.8 (Phase 7 full OS autoinstall):** promote former stretch **full distro autoinstall** to numbered **Phase 7** (7a Ubuntu autoinstall E2E → 7b profile/artifact model → 7c second family or generalized image-write + NetBox binding polish). Phase **6a** remains the **bounded payload-write MVP** (`SHOAL_INSTALL_MODE=write`); it is **not** replaced. Phase **6e+** stays optional polish (more OEM screenshots, registry publish, tracing). Still BMC-only (Virtual Media + SOL); no PXE.

**v2.0.9 (Phase 7a closed — nested-lab Ubuntu image-write):** **7a complete** via preferred nested-lab path: Ubuntu **cloud image** → customize → gzip → marker ISO (`payload.gz` on ISO root, not initrd) → Virtual Media → SOL markers → `gunzip|dd` to disk → reboot into Ubuntu. Live-server **autoinstall remaster** remains an alternate/stretch (unreliable under nested sushy). Phase **7b/7c deferred**. Multi-stage prep + scripted ISO matrix (Ubuntu autoinstall / Flatcar Ignition / ESXi kickstart / Windows) tracked in a **separate design document**, not as remaining 7.x checklist items.

**Preserved (product decisions, not language):**

- Hybrid normalization (deterministic first, AI reconciliation fallback)
- SOL status marker protocol as primary progress channel
- NetBox = identity + `lifecycle_state` only; telemetry separate
- Secrets never to LLM; opaque `credential_ref` pattern
- Plain HTTP ISO serving on the management segment for MVP
- Single-process MVP
- Phase 0 lab prerequisite + Phase 2 thesis spike
- Component boundaries: Core never calls Redfish/NetBox; Discover/Observe/Deploy never call LLM directly
- Lab infra: Ansible playbooks, sushy-tools, NetBox, Ollama, libvirt (language-agnostic)

**Changes in v1.1** (carried forward):

- SOL primary feedback channel; OCR demoted to graphics-only failure screens
- Hybrid normalization + learning loop
- Secrets never sent to cloud LLM; identity/credential split
- Provisioning state machine + mandatory cleanup + session management
- NetBox identity + lifecycle only
- Phase 0 lab + Phase 2 thesis spike; single-process MVP

---

## 1. Executive Summary & Vision

**Shoal** is a modern, BMC-centric bare-metal lifecycle platform focused on **Redfish/Swordfish** ecosystems.

**Core Differentiator**: The entire **provisioning path runs over the BMC management network only** — no provisioning VLAN, no PXE, no extra transit network. Status flows back over **Serial-over-LAN (SOL)**, and provisioning uses **Redfish Virtual Media + one custom live image** whose payload is embedded (written to local disk). Strong multimodal observability (SEL + sensors + SOL, with OCR for graphics-only failure screens).

**Target Users**: Bare-metal operators, colo/AI cluster teams, and organizations that value isolation and simplicity.

**Key Outcomes (MVP)**:
- Ingest messy hardware data → clean NetBox records (deterministic-first, AI for the messy edges)
- Real-time provisioning visibility via **SOL status markers** (SEL + sensors for health)
- Provision bare metal using only Redfish Virtual Media + one custom live image
- Dead-simple switch between local Ollama and cloud AI providers

**Success Metric for v1 (adjusted for Go rewrite):** A solo developer or small team can stand up a working **lab + Phase 2 thesis spike** (Virtual Media → SOL markers → DONE → cleanup) in **&lt; 2 weeks**, using fixture-backed or sushy-tools Redfish. Real-hardware SOL transport is a **stretch** inside that window, not a hard gate. Full Discover hybrid + polished Deploy remain Phases 3–5 and may land after the two-week demo.

**Scope note on the thesis**: "BMC-only" refers to the **provisioning path**. After install, the node reboots into its final OS and uses its normal production network. The provisioning loop needs no host data network because (a) the OS payload is embedded in the live image and written locally, and (b) status returns over SOL.

**Why Go (v2.0)**: A single static binary, excellent concurrency for concurrent BMC polling/SOL tails, strong typing without a heavy runtime, and a stdlib that already covers HTTP, JSON, SQL interfaces, logging, testing, and subprocess control.

---

## 2. Goals, Non-Goals, Constraints & Principles

### 2.1 Goals
- Fast iteration for a solo developer or small team
- Excellent observability during provisioning
- Minimal infrastructure footprint
- Easy binary / container distribution (single Go binary)
- Clean separation of concerns (so AI agents can work in parallel)
- Maximize Go standard library; minimize external dependencies

### 2.2 Non-Goals (MVP scope)
- Multi-tenancy, RBAC, enterprise features
- Full replacement of Ironic / MAAS / Foreman
- Support for every obscure vendor quirk
- High-scale orchestration (thousands of nodes)
- TLS everywhere / full PKI (deferred — see Section 11)
- Authenticated multi-user HTTP API (MVP API is unauthenticated on the management segment — see §11)
- Browser UI (deferred; API + CLI only)
- Python application code (abandoned for the app; lab remains Ansible/Python where tools require it)
- Heavy frameworks (Cobra, Gin/Echo/Chi, ORM frameworks) unless a measured need appears

### 2.3 Constraints
- Must work well on modest hardware. **Reference GPU reality:** a Quadro P600 has only **2 GB VRAM** — the recommended local text models barely fit and `*-vision:11b` does **not** fit. Treat local vision as a slow CPU fallback; prefer cloud for vision. The hybrid normalizer (Section 4.2) is designed so most inputs never need a heavy model.
- Must support both fully local (air-gapped) and cloud-assisted AI
- **Must be Go-based** for the entire application (CLI, API, Core, Discover, Observe, Deploy, shared models)
- Prefer stdlib; every external module must be on the allow-list (§7.1) with justification
- Must produce single-binary or small-container deliverables where practical
- Prefer **CGO-free** builds for the main binary so cross-compile and static linking stay simple

### 2.4 Guiding Principles (for all agents)
1. **Modularity first** — Every major component must be independently testable and replaceable.
2. **AI abstraction** — All LLM calls go through `internal/core/ai` over `net/http`. No provider SDKs outside that package.
3. **Deterministic-first** — Use AI where the world is messy, not where data is already structured.
4. **Observability by default** — Every important action produces structured events (`log/slog` + telemetry store).
5. **Human-in-the-loop friendly** — Prompts and outputs should be inspectable and debuggable.
6. **Secrets stay local** — Credentials are never placed in models that get logged or sent to an LLM.
7. **Simplicity over cleverness** — Prefer boring, readable code over complex abstractions. MVP runs as a single process.
8. **Document decisions** — Any significant choice must be recorded in this document or linked issues.
9. **Stdlib-first (with documented exceptions)** — Reach for the standard library before a dependency. **Exception:** Redfish uses **gofish** from day one (§4.5). Any other new module must update §7.1 in the same change.
10. **Interfaces at boundaries** — Data structs live in `models` (and small protocol packages under `common`). **Cross-component ports live in neutral `common` packages** so siblings never import each other. Composition root (`cmd/shoal`) is the only place that constructs concrete types and injects interfaces.

---

## 3. High-Level Architecture

### 3.1 Component Diagram (import-faithful)

Wiring flows **from the composition root outward**. Core is a **dependency** of Discover/Observe/Deploy — not their orchestrator. **Observe and Deploy never import each other.**

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

**MVP topology**: packages under one Go module, compiled into **one binary** (`cmd/shoal`). The process hosts:

- HTTP API (`net/http` ServeMux) for machine clients
- CLI subcommands for operator workflows
- Background workers as goroutines (polling, SOL tail, job state machine) cancelled via `context.Context`

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
- Core exposes `Reconciler` (and `LLM` / `Profiler`), **not** a full-pipeline `Normalizer` that owns deterministic code.
- Layout: `internal/discover/adapters/`, `internal/core/reconcile/`, `internal/core/ai/` — **no** `internal/core/normalize` owning adapters.

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
    API->>DEP: Start(ctx, req) returns immediately with job ID
    DEP->>JS: Insert PROVISIONING job
    DEP-->>API: 202 + job
    Note over DEP: goroutine: runJob(jobCtx)
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

1. Load config from env (§8.1). Construct clients (DB, NetBox, Redfish factory, secrets, AI).
2. Root `ctx, stop := signal.NotifyContext(parent, SIGINT, SIGTERM)`.
3. Construct `JobStore` → `Orchestrator` → Observe service; inject `jobport.JobProgress` into Observe and `watchport.WatchRegistrar` into Orchestrator (**no sibling imports**).
4. `http.Server` runs in a goroutine; handlers use `r.Context()` only for request-scoped work.
5. **`POST /v1/jobs` does not block on provisioning.** Handler calls `Orchestrator.Start`, which:
   - Persists the job (`PROVISIONING`)
   - Spawns `go o.runJob(jobCtx, job)` with a **child context** derived from root (or from a cancel registry)
   - Returns `202` + job JSON
6. **`Orchestrator`** holds:
   - `mu sync.Mutex`
   - `active map[string]context.CancelFunc` (per-job cancel)
   - `JobStore` (durable CRUD + progress fields only)
   - `terminalCh` (or per-job notify) for DONE/ERROR/stall/cancel handling
7. **Per-job cancel:** `Cancel(jobID)` → `HandleTerminal(jobID, ReasonCancel)`: cancel context, cleanup with bounded timeout (e.g. 60s), transition `PROVISIONING → FAILED` (or intermediate cleanup bookkeeping) → `READY` when cleanup succeeds.
8. **Graceful shutdown:** `stop()` → `Server.Shutdown` → for each active job, cancel + cleanup with overall deadline → close DB.
9. **Panic isolation:** `runJob` wraps body in `defer recover()` that marks job `FAILED`, logs, and runs cleanup — panics must not kill the process.
10. **Per-BMC semaphore** lives on the Redfish client (capacity 1–2). Multiple jobs targeting the same BMC serialize HTTP; different BMCs run in parallel.
11. **SOL ownership:** at most one Observe SOL goroutine per node; `WatchRegistrar.Register` rejects dual ownership.
12. **HTTP timeouts:** `ReadHeaderTimeout` set; long operations never hold the request. Job status is poll-based (`GET /v1/jobs/{id}`); no long-poll requirement for MVP.
13. **WaitGroup vs errgroup:** use `sync.WaitGroup` + error logging for worker sets in MVP (no new dep). Add `golang.org/x/sync/errgroup` only if multi-error orchestration becomes painful (allow-list amendment).

---

## 4. Detailed Component Designs

### 4.1 Shoal Core (AI only — not the hybrid orchestrator)

**Technology**:
- Package: `internal/core`
- AI transport: thin client in `internal/core/ai` using `net/http` + `encoding/json`
- Structured I/O: shared types in `internal/common/models` with JSON tags; decode/validate pipeline in `internal/core/ai/decode` + `internal/common/validate`

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

**Validation rules (MVP):**
- `json.Unmarshal` into concrete structs (unknown JSON fields **ignored** by default — Go zero-value behavior; do not use a decoder `DisallowUnknownFields` for LLM output, which is often noisy; **do** use it for operator-facing API requests if desired).
- Confidence scores must be in `[0.0, 1.0]`.
- AI-sourced `FieldConfidence` requires non-empty `Evidence` (raw excerpt).
- `NeedsReview` must be true if any AI field conflicts with a deterministic value (Discover sets this after merge) or any confidence &lt; 0.6 on a required field.
- Reject payloads that still contain password-like keys after redaction (test).
- Malformed JSON → error returned to caller; no partial silent accept.

**Golden-test workflow:** for each prompt+schema version under `prompts/`, store `testdata/golden/<name>/{input.json, partial.json, expected.json}`. Changing a prompt or schema requires deliberate golden updates in the same PR.

**Prompt Strategy**:
- All prompts live in repo-root `prompts/` (language-agnostic); schemas in `prompts/schemas/`; optionally `//go:embed` from `internal/core`.
- Few-shot examples as JSONL under `prompts/fewshot/`.
- Every prompt must include: task, output schema (inline schema text), examples, “think step by step”, “only valid JSON”, per-field confidence + evidence.

### 4.2 Shoal Discover (hybrid pipeline owner)

**Why hybrid**: Vendor Redfish implementations deviate from spec. Pure deterministic parsing breaks on deviations; pure-AI parsing pays nondeterminism/latency/cost on the easy 90%.

**Pipeline (Discover-owned orchestration):**
1. **Deterministic fast path** — `internal/discover/adapters` parse Redfish JSON / CSV into partial `NormalizedAsset` + confidences (`source=deterministic`).
2. **Confidence gate** — accept only if required fields present, sanity checks pass (serial/MAC/IP, known model set), adapter matched.
3. **AI reconciliation (fallback)** — call `core.Reconciler.ReconcileAsset` with **redacted** raw + partial + schema. Core returns `NormalizationResult` from AI only.
4. **Merge & conflict policy (Discover)** — prefer deterministic values on conflict; set `NeedsReview=true` rather than silently trusting AI; union confidences.
5. **Vision path** — photos: no deterministic adapter; call `CompleteVision` via Reconciler; still schema-validated (cloud-preferred).
6. **Learning loop** — confirmed reconciliations append to few-shot store; stable patterns graduate into new Discover adapters later.

**Responsibilities**:
- Accept photo, Redfish dump, or CSV (`RawAssetInput`)
- Run hybrid pipeline; stash BMC credentials via `secrets.Backend` → `credential_ref` only on asset
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
- OCR of graphics-only failure screens (Phase 6; **approach TBD** — evaluate `os/exec` Tesseract vs cloud vision when that phase starts)
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

**Parser rules**: accept only `SHOAL|1|...`; ignore console noise; treat heartbeat gap beyond stall timeout (e.g. 90s) as stall. **Observe owns the single SOL session** per node during a job.

#### Job progress (boundary-safe, no Deploy import)

Observe depends only on `internal/common/jobport` and `models.SOLMarker`. It **never** imports `deploy`.

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

Fields Observe may **propose** via progress writes: `phase`, `percent`, `last_marker_seq`, soft `error` text.  
Fields/transitions **only Orchestrator commits**: `state` (`LifecycleState`), `attempt`, `sol_session_id`, cleanup completion, NetBox lifecycle writes.

**Marker-driven state machine (two phases):**

1. **Progress-only** — any non-terminal marker → `JobStore.UpdateProgress` only; job remains `PROVISIONING`.
2. **Terminal** — DONE/OK, ERROR, stall, transport failure, or cancel → `Orchestrator.HandleTerminal`:
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
| FAILED | READY | cleanup complete + operator/reset path (MVP: automatic after successful cleanup bookkeeping may leave FAILED visible; Ready when device re-queued) |
| PROVISIONED | READY | re-provision request (later) |

Explicit cancel: `PROVISIONING --cancel--> (cleanup) --> FAILED` (with `error=canceled`), then device may return to READY when re-enqueued. Do **not** invent a separate long-lived `CLEANUP` lifecycle enum for MVP; cleanup is a **mandatory finalizer** inside `HandleTerminal`, not a NetBox-facing state.

**Transport**: real hardware uses Redfish `SerialConsole` / IPMI SOL; lab uses libvirt guest serial (Section 8).

```go
// internal/observe/sol — parser returns models.SOLMarker (common), not deploy types
type Transport interface {
    Open(ctx context.Context, target string) (<-chan string, error)
    Close() error
}

func ParseLine(line string) (models.SOLMarker, bool) { return models.SOLMarker{}, false }
```

**BMC session management**: session-token auth (production), reuse/close, cap concurrency ≈1–2, backoff on 4xx/5xx (`Retry-After`). Lab may use basic auth (§4.5). Watch mode raises frequency but never exceeds per-BMC cap.

**Watch Mode Contract**: Orchestrator calls `watchport.WatchRegistrar.Register(WatchSession)` (interface in `common`; implementation in Observe). Observe tails SOL and calls `jobport.JobProgress`. Progress for operators: **poll** `GET /v1/jobs/{id}` (MVP). SSE/WebSocket deferred.

### 4.4 Shoal Deploy

**Responsibilities**:
- Accept target binding + optional profile (Phase 2: CLI flags; later: NetBox device id)
- Optionally call `core.Profiler` (destructive steps require human approval before execution)
- Build live ISO (`os/exec` for `dracut`/`xorriso`); **Phase 2 serves via lab ISO HTTP** (§4.4.1)
- Redfish: insert Virtual Media, one-time boot override, power on/reboot
- Own **`JobStore` (persistence)** and **`Orchestrator` (state machine + cleanup)**; register watches via `watchport` only
- Success path: eject media, clear override, reboot final OS, update NetBox (when used)

**Reliability contract** (required):
- Idempotent steps (read BMC state, converge)
- Per-step timeouts + SOL stall detection
- Mandatory cleanup on success, failure, **and** cancel
- Cancel + **startup reconcile of durable orphan jobs**

#### JobStore vs Orchestrator (split roles)

**`JobStore`** = pure durable repository. **No Redfish. No Observe imports. No cleanup side effects.**

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

**`Orchestrator`** owns lifecycle transitions, post-checks, Redfish cleanup, and implements `jobport.JobProgress` by:
1. calling `JobStore.UpdateProgress` synchronously
2. on terminal conditions, non-blocking send to `terminalCh` / `go HandleTerminal(...)` — **never** runs cleanup inside the Observe call stack beyond the quick DB write + notify

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
2. For each orphan: attempt to re-attach watch if BMC still mid-job **or** `HandleTerminal(ReasonBMC)` fail+cleanup if unrecoverable / flag `SHOAL_RECONCILE_FAIL_ORPHANS=true` (default true for MVP safety).
3. Log each reconcile decision.

#### 4.4.1 ISO artifact placement (Phase 2 default)

Lab already serves files via nginx on **`:8080`** from `shoal_iso_server_dir` (typically `/srv/iso`).

**Phase 2 default path (required for PR6/PR8):**
1. Build minimal marker ISO via PR6 scripts — **primary:** Ansible role on lab VM; **alternate:** developer workstation (§8.2).
2. **Copy/rsync** artifact into the lab ISO directory: e.g. `/srv/iso/shoal-marker.iso` (primary path may build in-place on the lab host).
3. BMC-reachable URL:  
   - VM-hosted from L2 node path: `http://192.168.124.1:8080/shoal-marker.iso` (lab gateway)  
   - Operator/API default host view: `http://192.168.122.100:8080/shoal-marker.iso`  
   - Direct mode: `http://127.0.0.1:8080/shoal-marker.iso` (BMC nodes use lab-net gateway equivalent)
4. Pass that URL as `-iso-url` / `StartJobRequest.ISOURL`.

In-process `net/http` file server is **optional later** (not Phase 2 default). PR6 AC must document the build host path used, the copy/publish step, and print the resulting URL.

#### 4.4.2 Phase 2 device binding (Discover optional)

Phase 2 runs **before** hybrid Discover/NetBox ingest. **Do not require NetBox** for the thesis spike.

**Canonical lab CLI (PR8):**

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
| `-device-id` | `ProvisioningJob.DeviceID` | Opaque string for correlation; need not exist in NetBox in Phase 2 |
| `-bmc-url` | `ProvisioningJob.BMCEndpoint` | sushy-tools root (multi-system under one emulator is OK; client discovers Systems) |
| `-bmc-user` / `-bmc-pass` | `secrets.Backend` under a generated `credential_ref` | **Never log password**; may default from `SHOAL_BMC_*` env if flags omitted |
| `-serial-target` | `WatchSession.Target` | libvirt domain name for `virsh ttyconsole` / PTY path resolution |
| `-iso-url` | `ProvisioningJob.ISOURL` | Must be BMC-reachable; see §4.4.1 |
| `-system-id` | optional Redfish system id | Default first/only system if omitted |
| `-profile-ref` | optional | Empty/`spike` for Phase 2 |

`StartJobRequest` carries these binding fields for API parity. Post-Phase 3, an alternate path may resolve `DeviceID` from NetBox + `credential_ref` only; that is **not** required for Phase 2 ACs.

**Operator poll:** `shoal deploy status -job <id>` or `GET /v1/jobs/{id}`.

### 4.5 Shared infrastructure packages

#### Ports vs models

- **`internal/common/models`**: data structs + JSON tags only (including `SOLMarker`).
- **Neutral ports under `common`:** `jobport.JobProgress`, `watchport.WatchRegistrar` — **no** implementations that import observe/deploy.
- **Implementations:** `secrets.Backend`, `netbox.ClientAPI`, `redfish.BMC`, `telemetry.Store`, `deploy/jobstore.JobStore`, Deploy `Orchestrator`, Observe service.
- Discover/Deploy/Observe take interfaces as struct fields; **only `cmd/shoal` wires concrete types across siblings**.

#### Redfish client (gofish-backed, Shoal interfaces)

**Decision (v2.0.3):** Use **`github.com/stmcginnis/gofish` from day one** for Redfish sessions, Virtual Media, power, and boot override. Stdlib-first still applies to HTTP API, CLI, AI, NetBox, logging, etc. — Redfish is the deliberate exception because session/Virtual Media/vendor path complexity is the Phase 2 critical path.

**Boundary rule:** gofish types **must not** leak outside `internal/common/redfish`. Call sites (Deploy, Observe) depend only on Shoal interfaces and models. Implementation wraps gofish and maps to Shoal structs.

Lab smoke today uses **HTTP basic auth** against sushy-tools (`force_basic_auth`). SessionService is **not** proven by current smoke. Production BMCs typically need sessions + often HTTPS with bad/self-signed certs. Configure gofish/client for basic (lab) vs session (prod) via `Config.AuthMode`.

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
    // Observe later: GetSEL, GetSensors as needed
}

// NewBMC constructs the gofish-backed implementation.
func NewBMC(cfg Config) (BMC, error) { /* gofish connect + session/basic */ return nil, nil }
```

**Minimum Phase 2 operations (via gofish + wrappers):**

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
- Real BMC HTTPS with self-signed: `TLSMode=insecure` **only** when explicitly configured (`SHOAL_REDFISH_TLS_MODE=insecure`); never default for cloud-facing clients.
- Preferred production: `custom_ca` with BMC enterprise CA file.
- Document risk of MITM on management network when using insecure mode.

**Idempotency:** every Deploy action reads current VirtualMedia.Inserted / Boot override / PowerState (via gofish) and only mutates when drift exists.

**Fixture-first development:** record sushy-tools / gofish-visible JSON under `testdata/redfish/sushy-tools/`; unit-test wrappers with fakes of `BMC` and/or recorded HTTP where practical. Integration tests hit live sushy-tools.

**Not chosen:** a greenfield thin `net/http` Redfish client with a timebox to adopt gofish later. That path is rejected for MVP to avoid delaying the Phase 2 spike on protocol edge cases.

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

MVP: file backend mode `0600` or env map for lab.

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

Lab already runs **telemetry Postgres** on host port **5433**, DB `shoal_telemetry`, user `shoal` (see `defaults.yml` / Compose). **This is the default app database** for events, sensors, job logs, **and jobs**.

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

- **Default driver:** `github.com/jackc/pgx/v5` via `database/sql` (`pgx/stdlib`) or pgx pool behind the store interface.
- **SQLite (`modernc.org/sqlite`):** optional **demo-only** offline path; not the lab default; not required for Phase 2 ACs.
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

**MVP API auth:** **unauthenticated**. Bind to management interface only (`SHOAL_HTTP_ADDR` e.g. `192.168.122.100:8088` or `127.0.0.1:8088` in direct mode). Documented threat: anyone on the mgmt segment can trigger jobs — acceptable for lab/MVP; Phase 6+ auth.

#### CLI (stdlib)

Subcommands: `serve`, `version`, `discover ingest`, `observe status`, `deploy run`, `deploy status`, `deploy cancel`.

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
    Transport    string    `json:"transport"` // "libvirt" | "redfish_sol" | "ipmi_sol"
    Target       string    `json:"target"`    // console path or BMC URI
    StartedAt    time.Time `json:"started_at"`
    StallTimeout time.Duration `json:"stall_timeout"` // e.g. 90s
}

// DeviceIdentity — NetBox-facing identity fields.
type DeviceIdentity struct {
    ID             string         `json:"id,omitempty"`
    Name           string         `json:"name,omitempty"`
    Serial         string         `json:"serial"`
    LifecycleState LifecycleState `json:"lifecycle_state"`
    CredentialRef  string         `json:"credential_ref"`
    BMCIP          string         `json:"bmc_ip"`
}

// API DTOs — Phase 2 binding fields required; NetBox-only resolution is post-Phase 3 optional.
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
    // ApproveDestruct is operator consent for NeedsApproval / DestructSteps (Phase 5b).
    // Does not bypass a missing profile store entry; only supplies consent at Start.
    ApproveDestruct bool `json:"approve_destruct,omitempty"`
}

type CancelJobRequest struct {
    JobID string `json:"job_id"`
}
```

**Redaction rules for raw maps:** before LLM or slog debug of bodies, run `redact.Map` which recursively strips keys matching case-insensitive `password`, `passwd`, `secret`, `token`, `authorization`, `api_key`, `bmc_password`. `RawAssetInput.BMCPassword` / `StartJobRequest.BMCPassword` are written only to `secrets.Backend` and never to logs or NetBox.

**Validation helpers (non-exhaustive):** `validate.NormalizedAsset`, `validate.NormalizationResult`, `validate.NormalizedEvent` (requires `DeviceID`), `validate.ProvisioningProfile`, `validate.RawAssetInput`, `validate.StartJobRequest` (requires `BMCEndpoint`, `ISOURL`, `SerialTarget` for Phase 2).

**Provisioning state machine** (summary; detail + cancel in §4.3):

| From | To | Trigger |
|------|----|---------|
| DISCOVERED | READY | Asset normalized + stored, credentials resolvable |
| READY | PROVISIONING | `Start`: media inserted, one-time boot override set, power on, watch registered |
| PROVISIONING | PROVISIONING | Progress-only markers |
| PROVISIONING | PROVISIONED | `HandleTerminal(DoneOK)` after post-checks + cleanup |
| PROVISIONING | FAILED | `HandleTerminal` on ERROR/stall/transport/BMC/panic/**cancel** after cleanup |
| FAILED | READY | Device re-queued / operator reset after cleanup |

**Only Deploy Orchestrator commits `LifecycleState` transitions** (via `JobStore.Transition`). Observe proposes progress via `jobport.JobProgress` only.

**NetBox Integration:** SoT for device identity + current `lifecycle_state` only. Custom fields: `shoal_lifecycle_state`, `shoal_credential_ref`. No time-series in NetBox.

---

## 6. AI Layer & Prompt Engineering Guidelines (Critical for Agents)

**Golden Rules for all prompts**:
1. Always request structured JSON matching a named schema under `prompts/schemas/`
2. Include 2–4 high-quality few-shot examples
3. Tell the model its role and constraints explicitly
4. Ask it to "think step by step" before the final answer
5. Always return per-field confidence + raw excerpt as evidence
6. **Redact secrets** from any payload before it reaches the model
7. Run the **decode → unmarshal → validate** pipeline (§4.1); never trust raw `Content`

**Lab / host model strategy (v2.0.5):**

Reference operator host may be modest (e.g. older Xeon, ~32 GB RAM, mobile Quadro ~4 GB VRAM). Nested VM lab Ollama is often **CPU-bound** (no GPU passthrough). Prefer small text models; vision OCR needs a real OCR VLM (not a caption-only toy).

| Role | Env var | Lab default | Notes |
|------|---------|-------------|--------|
| **Text / structured JSON** | `SHOAL_AI_MODEL` | `llama3.2:3b` | Hybrid `ReconcileAsset` / text paths; instruct/completion model |
| **Vision / asset-label photos** | `SHOAL_AI_VISION_MODEL` | **`deepseek-ocr`** | Discover photo path; Free OCR on labels. Empty env → no local vision. **`moondream` is not AC-grade** |
| **Graphics failure-screen OCR** | — | not default | **Phase 6** only; candidates: `os/exec` Tesseract vs cloud vision (or OCR VLMs) — not pre-committed |

Optional text upgrade if operators want stronger JSON adherence: `qwen2.5:3b` (same size class). **Do not** use `deepseek-ocr` as the **text** hybrid default — it is OCR-first, not a general JSON reconciler.

**Call routing** (`internal/core/ai` — implementers must follow):

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

Operators may use cloud multimodal models by setting provider/model env vars. Local photo AC is validated with **`deepseek-ocr`** Free OCR, not caption models.

**AI client configuration** (`internal/core/ai`), rendered by Ansible `compose_stack` / app env:

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
- Ollama vision: prefer `POST {url}/api/generate` with `images` + short OCR prompt (`Free OCR.`); chat fallback; **no** vision `format=json`
- Cloud: `POST {base}/chat/completions` + Bearer token; vision uses image content parts
- Log: prompt hash/version, **resolved model name**, tokens, latency — **no secrets**, no full raw photo bytes
- `http.Client` with timeouts; `NewRequestWithContext`
- Lab Ansible: `shoal_ai_model` + `shoal_ai_vision_model` pulled by `compose_stack`; smoke asserts both appear in `/api/tags` when vision is non-empty

---

## 7. Technology Stack & Library Choices (MVP)

### 7.1 External dependency allow-list

**Rule**: if it is not on this list, do not add it without updating this section in the same PR.

| Dependency | Kind | Why not avoidable with stdlib alone |
|------------|------|-------------------------------------|
| `github.com/stmcginnis/gofish` | runtime (required) | Redfish sessions, Virtual Media, boot override, power — adopted **day one** so Phase 2 is not blocked on a greenfield Redfish client. Wrapped behind `internal/common/redfish`; types do not leak to call sites. |
| `github.com/jackc/pgx/v5` | runtime (default) | Postgres driver for lab telemetry/jobs DB on `:5433` |
| `modernc.org/sqlite` | runtime (optional demo) | Pure-Go SQLite if someone runs without Compose; not required for lab ACs |
| `honnef.co/go/tools/cmd/staticcheck` | toolchain | Static analysis beyond `go vet`; not linked into binary |

**Toolchain install (AGENTS / CI):**
```bash
go install honnef.co/go/tools/cmd/staticcheck@2025.1.1  # pin in AGENTS.md / CI
# or module tools.go pattern:
#   //go:build tools
#   import _ "honnef.co/go/tools/cmd/staticcheck"
```

**Explicitly rejected for MVP:**

| Rejected | Reason |
|----------|--------|
| Cobra | Few subcommands; `flag` enough |
| Gin / Echo / Chi / Fiber | ServeMux enough |
| LiteLLM / official OpenAI SDKs | Thin `net/http` client |
| Greenfield thin Redfish `net/http` client | Rejected for MVP — gofish is the chosen Redfish stack (§4.5) |
| ORMs | Hand-written SQL for four tables |
| Celery / Redis as app queue | Single-process goroutines; lab Redis is for NetBox |
| Python app packages | App is Go; sushy-tools is lab-only |
| `go-playground/validator` / JSON Schema libs | Hand validation sufficient for MVP domain surface |

**Optional later (require allow-list amendment):**
- `golang.org/x/sync/errgroup` — multi-error worker orchestration
- `golang.org/x/term` — interactive secret prompts
- Vector DB client — only if file few-shot RAG fails
- Phase 6 OCR libs or Tesseract packaging — only after OCR approach is chosen

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
| OCR (Phase 6) | **TBD** (Tesseract `os/exec` and cloud vision both candidates) | Decide at Phase 6; not closed now |
| BMC TLS | `crypto/tls` + gofish client config | Policy §4.5 |
| App listen | **`:8088`** | Confirmed |

### 7.3 Alternatives considered

#### (a) Redfish: thin client vs gofish day one

| Option | Pros | Cons |
|--------|------|------|
| Thin `net/http` client | Zero Redfish deps; full control | Delays Phase 2 on VM/session edge cases |
| **gofish from day 1 (chosen)** | Faster Virtual Media/session coverage; less greenfield risk on critical path | Extra dep; must wrap to avoid type leakage |
| Hybrid wrap gofish later | Same call-site interface eventually | Dual maintenance; spike schedule risk |

**Decision (v2.0.3):** **gofish day one**, wrapped behind Shoal `BMC` interfaces. This is a deliberate **reversal of v1.1’s “no gofish”** rule (that rule existed because the app was Python, not because gofish is unsuitable) and a **reversal of earlier v2.0 drafts** that deferred gofish behind a thin-client timebox. Stdlib-first remains the default for everything except Redfish.

#### (b) Postgres-only vs SQLite dual-path

| Option | Pros | Cons |
|--------|------|------|
| Postgres-primary (chosen) | Matches lab `telemetry-db :5433`; one schema path; durable jobs | Requires Compose/lab or external PG |
| SQLite default | Zero ops for laptop | Diverges from lab; another driver; reconcile semantics differ |
| Dual first-class | Flexible | Double CI matrix; agent confusion |

**Decision:** **Postgres-primary**. SQLite is optional demo-only behind the same `JobStore`/`Store` interfaces.

#### (c) Watch progress: poll vs SSE/WebSocket

| Option | Pros | Cons |
|--------|------|------|
| Poll `GET /v1/jobs/{id}` (chosen MVP) | Trivial; stdlib; enough for CLI | Slight lag |
| SSE | Push with one response stream | More handler code; still single-process |
| WebSocket | Bi-directional | Overkill for MVP |

**Decision:** poll-only MVP; push transports post-MVP if UX demands.

#### (d) Validation: hand-rolled vs libraries

| Option | Pros | Cons |
|--------|------|------|
| Hand-rolled (chosen) | Zero deps; domain-specific rules | More code |
| go-playground/validator | Tags | Dep + less control over AI messy JSON |
| JSON Schema lib | Shared with prompts | Heavier; still need fence stripping |

**Decision:** hand-rolled decode+validate pipeline (§4.1).

### 7.4 Repository layout

```
shoal/                                    # module: github.com/mattcburns/shoal
  cmd/shoal/main.go
  internal/
    api/
    cli/
    core/
      ai/           # LLM HTTP + decode
      reconcile/    # AI reconciliation only
      profile/      # provisioning profile generation
    discover/
      adapters/     # deterministic Redfish/CSV — NOT in core
    observe/
      sol/          # parser + transports; returns models.SOLMarker
      poll/
    deploy/
      job/          # Orchestrator + HandleTerminal + jobport adapter
      jobstore/     # pure jobs table CRUD/progress/transition
      iso/
    common/
      models/       # data structs only (incl. SOLMarker)
      jobport/      # JobProgress interface — Observe consumes; Deploy implements
      watchport/    # WatchRegistrar interface — Deploy consumes; Observe implements
      validate/
      redact/
      secrets/
      redfish/
      netbox/
      telemetry/
      config/
  prompts/
    schemas/
    fewshot/
  testdata/
    golden/
    redfish/
  infra/ansible/    # existing lab (kept)
  docs/
  AGENTS.md
  go.mod
```

**Existing Python scaffolding:** repo is lab-only; no application Python packages. Do not add any. **PR0** replaces design SoT + rewrites `AGENTS.md` (Definition of Done must say `gofmt`/`go vet`/`staticcheck`/`go test`, not `make lint` / `make test`).

---

## 8. Lab & Development Environment

**Modes** (unchanged): Direct host; VM-hosted preferred (L0 hypervisor → L1 lab VM → L2 sushy-tools nodes). SOL harness = libvirt serial.

**Quick start:**

```bash
# Lab
ansible-galaxy collection install -r infra/ansible/requirements.yml
cp infra/ansible/inventory/group_vars/all/vault.yml.example \
   infra/ansible/inventory/group_vars/all/vault.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml

# App (Phase 1+)
go test ./...
go run ./cmd/shoal serve -addr "${SHOAL_HTTP_ADDR:-:8088}"
```

Default VM-hosted endpoints: NetBox `:8000`, sushy `:8001`, ISO HTTP `:8080`, Ollama `:11434`, **telemetry Postgres `:5433`**.

### 8.1 App configuration contract

**Lab config** stays in Ansible `group_vars` + vault — **not** a committed repo-level `.env` for lab secrets.  
**App config** is process environment. Compose/`compose_stack` may **render** an env file for the Shoal container/process from Ansible vars. A developer may keep a **local untracked** `.env` for `go run` against a remote lab; agents must not commit secrets or reintroduce lab secrets into git.

| Env var | Required | Source / notes |
|---------|----------|----------------|
| `SHOAL_HTTP_ADDR` | yes | Default `:8088`; bind mgmt interface in lab |
| `SHOAL_LOG_LEVEL` | no | `debug`\|`info`\|`warn`\|`error` (default `info`) |
| `SHOAL_TELEMETRY_DATABASE_URL` | yes | Lab Postgres `…:5433/shoal_telemetry` |
| `SHOAL_NETBOX_URL` | yes | e.g. `http://192.168.122.100:8000` |
| `SHOAL_NETBOX_TOKEN` | yes | From vault / netbox_bootstrap; **add to `env.j2`** when app service lands |
| `SHOAL_AI_PROVIDER` | yes | Already in `env.j2` |
| `SHOAL_AI_MODEL` | yes | Text model; already in `env.j2` (lab default `llama3.2:3b`) |
| `SHOAL_AI_VISION_MODEL` | no | Vision/OCR model for `CompleteVision` (lab default `deepseek-ocr`; empty skips photo) |
| `SHOAL_OLLAMA_URL` | if ollama | Already in `env.j2` |
| `SHOAL_CLOUD_AI_BASE_URL` | if cloud | Already in `env.j2` |
| `SHOAL_CLOUD_AI_API_KEY` | if cloud | Vault; already in `env.j2` |
| `SHOAL_REDFISH_AUTH_MODE` | no | `basic` (lab default) \| `session` |
| `SHOAL_REDFISH_TLS_MODE` | no | `off`\|`insecure`\|`custom_ca` |
| `SHOAL_REDFISH_CA_FILE` | if custom_ca | Path |
| `SHOAL_BMC_USERNAME` / `SHOAL_BMC_PASSWORD` | lab only | Existing vault vars — for lab defaults / smoke; production uses secrets backend per device |
| `SHOAL_ISO_BASE_URL` | no | BMC-reachable ISO HTTP prefix (lab nested: `http://192.168.124.1:8080`). Ansible `env.j2` sets gateway + port. Used for profile `iso_base` resolve + publish URL |
| `SHOAL_ISO_PUBLISH_DIR` | no | Filesystem publish dir (lab: `shoal_iso_server_dir` → `/srv/iso` in `env.j2`) |
| `SHOAL_ISO_BUILD_SCRIPT` | no | Optional path to `build-marker-iso.sh` |
| `SHOAL_ISO_DYNAMIC` | no | If `true`, Start may build+publish when ISOURL empty (Phase 6a; needs publish dir + base URL) |
| `SHOAL_RECONCILE_FAIL_ORPHANS` | no | Default `true` |
| `SHOAL_FEWSHOT_DIR` | no | Append-only learned few-shot JSONL (Phase 3b confirm). Lab default via Ansible `shoal_fewshot_dir` → `/var/lib/shoal/fewshot` in `env.j2` + mkdir. Empty disables confirm |
| `SHOAL_PROFILE_DIR` | no | JSON provisioning profiles + approval records (Phase 5b). Lab default via Ansible `shoal_profile_dir` → `/var/lib/shoal/profiles` in `env.j2` + mkdir. Empty disables non-spike profile load; `spike` profile ref always allowed without a store |
| `SHOAL_API_TOKEN` | no | Phase 6d: Bearer token for `/v1/*` when set; empty = open API (lab default). Never log. Lab Ansible: `shoal_api_token` (vault optional) |
| `shoal_compose_app` (Ansible) | — | Phase 6d: when true (lab default), stage binary + Dockerfile and run Compose service `shoal` (`network_mode: host`, port `shoal_app_http_port` / 8088) |

**Ansible extensions (when packaging app service):**
- `compose_stack` templates: add `shoal` service (static binary image), publish `SHOAL_HTTP_ADDR` port, inject table above into `env.j2`
- `group_vars/all/defaults.yml`: `shoal_app_http_port: 8088`; **`shoal_fewshot_dir: /var/lib/shoal/fewshot`** (Phase 3b); **`shoal_profile_dir: /var/lib/shoal/profiles`** (Phase 5b; both already in `env.j2` + mkdir)
- Secrets: `shoal_netbox_token` already bootstrapped — ensure it is exported into app env

**Phase 0** does not require the Shoal container. **Phase 1** may `go run` with exported env. **Packaging PR** adds Compose service.

### 8.2 Live image build host (primary + alternate)

Phase 2 / PR6 must produce a minimal marker-emitting live ISO and publish it to the lab ISO HTTP tree (`:8080`).

| Path | Who builds | When to use |
|------|------------|-------------|
| **Primary (recommended):** Ansible role on **lab VM (L1)** | Playbook/role installs `dracut`/`xorriso` (or equivalent), builds ISO on the lab host, copies into `shoal_iso_server_dir` | Default for reproducibility and CI-like lab ops; same network as sushy-tools and ISO nginx |
| **Alternate:** Developer **workstation** | Developer installs build packages locally, builds ISO, `scp`/`rsync` into lab ISO dir (or mounts shared path) | Fast local iteration; must still publish to a BMC-reachable `:8080` URL |

**Primary recommendation:** lab VM Ansible role (document under Appendix I / `infra/ansible` when implemented) so “bring up lab + build marker ISO + publish URL” is one documented path. Workstation builds are supported but must end in the same publish contract (§4.4.1).

---

## 9. Phased Implementation Plan (Agent-Ready)

Phase 0 is a hard prerequisite. Phase 2 proves BMC-only + SOL **without requiring AI**.

### Phase 0: Lab Environment Setup (Prerequisite)

**Status:** Lab automation exists under `infra/ansible/` — **verify/converge**, not greenfield.

**Acceptance Criteria:** Redfish (basic auth) readable; NetBox API; Ollama trivial completion; ISO HTTP; serial consoles; `up.yml`+`smoke.yml` pass. Note: smoke proves **basic-auth** Redfish, not SessionService.

### Phase 1: Foundation & Scaffolding (1–3 days)

**Tasks:**
1. `go mod init github.com/mattcburns/shoal`
2. Package tree per §7.4
3. Models + validate + redact + secrets stub
4. Config loader for §8.1 env contract
5. CLI: `version`, `serve`; `GET /healthz`, `GET /readyz` (DB optional ping)
6. **AI: interface + fake/stub only** (real Ollama client is Phase 3 / PR after spike)
7. Config loader accepts `SHOAL_TELEMETRY_DATABASE_URL` (optional ping on `/readyz`); **no jobs migrations in Phase 1** — schema lands in PR3
8. Rewrite `AGENTS.md` (Go DoD; staticcheck install pin)
9. slog with `SHOAL_LOG_LEVEL`; redaction tests for secrets in logs
10. Skeleton packages for `common/jobport` + `common/watchport` (interfaces only)

**Acceptance Criteria:**
- `go build` / `go test ./...` green
- `shoal version`, `shoal serve` + healthz
- Credential resolvable via `credential_ref`; never in `NormalizedAsset` JSON or slog
- Fake `LLM` satisfies interface for unit tests (no live Ollama required in Phase 1)
- No requirement that jobs table exist yet

### Phase 2: Thesis Spike — BMC-only provisioning + SOL feedback

**Goal:** Prove the core bet **without AI** and **without Discover/NetBox**.

**Tasks:**
- Minimal live image: emit `SHOAL|…` markers + heartbeats; `console=ttyS0,115200n8`; **copy into lab `/srv/iso` → URL on `:8080`** (§4.4.1); build via **lab VM Ansible (primary)** or workstation alternate (§8.2)
- **gofish-backed** Redfish wrapper: basic auth (lab), VM insert/eject, boot override, power, cleanup (fixture + sushy-tools)
- Observe: libvirt serial transport, parser → `models.SOLMarker`, `jobport` progress calls
- Deploy: pure `JobStore`, Orchestrator + `HandleTerminal`, watch via `watchport`, orphan reconcile
- CLI: `shoal deploy run` with **Phase 2 binding flags** (§4.4.2); `deploy status`

**Acceptance Criteria:**
- Using only CLI flags (no NetBox device required), node boots live image via Virtual Media from `http://…:8080/….iso`; phase/percent → `DONE` **via SOL only**; media ejected + override cleared
- Heartbeat kill → stall → `FAILED` + cleanup
- Cancel path → cleanup + `FAILED`
- Restart with orphan `PROVISIONING` job reconciles (fail+cleanup default)
- Validated on lab; real-hardware SOL is stretch
- **No LLM and no Discover required** for this phase to pass

### Phase 3: Shoal Discover + Core (Hybrid normalization)

Parallel: Discover adapters/gate; Core Reconciler + real AI client; vision path; NetBox writes; few-shot learning loop.

**Acceptance Criteria:** clean dump deterministic; spec-deviant → AI; conflicts → `needs_review`; photo path extracts **real** serial/vendor/model from label OCR (lab: `deepseek-ocr` Free OCR — not placeholder serials); redaction ensures secrets never reach LLM payloads; lab text hybrid on `SHOAL_AI_MODEL=llama3.2:3b`.

**Prerequisite (lab):** dual-model AI contract (design §6 / v2.0.5) and Ansible that pulls/exports text + vision models (`llama3.2:3b` + `deepseek-ocr`).

### Phase 4: Shoal Observe (Broaden)

SEL/sensor poll, session caps, event normalize, watch mode, CLI status; stretch OCR only if approach chosen early.

### Phase 5: Shoal Deploy (Harden)

Full ISO pipeline, reliability contract polish, profile generation + approval, NetBox lifecycle sync.

**Phase 5b (profiles + approval):** Core `Profiler` generates `ProvisioningProfile` via AI + schema validate. Durable store under `SHOAL_PROFILE_DIR` (not NetBox). CLI `shoal profile generate|save|show|list|approve`. Deploy `Start` rejects non-`spike` refs without store entry; `NeedsApproval` / non-empty `DestructSteps` require prior `profile approve` or `StartJobRequest.ApproveDestruct` / `-approve-destruct`. AI never auto-executes destruct.

**Phase 5c (ISO pipeline):** `internal/deploy/iso` `Builder` wraps `infra/scripts/build-marker-iso.sh` (`os/exec`), optional `EmbeddedPayload` → `/payload` in the image, `Publish` to `SHOAL_ISO_PUBLISH_DIR` + `SHOAL_ISO_BASE_URL`. CLI `shoal deploy iso build|publish`. Start fills empty `ISOURL` from profile `iso_base` + base URL. Lab Ansible still primary producer; serve remains nginx `:8080` plain HTTP.

### Phase 6: Polish

Graphics OCR via **Core `CompleteVision`** (not Tesseract-first), dynamic ISO / real payload write, metrics, TLS/API auth hardening, packaging, record/replay CI.

**Phase 6a (dynamic ISO + write path):** `SHOAL_INSTALL_MODE=write` live image writes `/payload` to `shoal.target` / `SHOAL_INSTALL_TARGET` (or `/tmp/shoal-install.out` fallback) with real `IMAGE_WRITE` progress; `simulate` remains Phase 2 demo. Optional `StartJobRequest.BuildISO` / `SHOAL_ISO_DYNAMIC` builds+publishes before Virtual Media. Not a full multi-distro autoinstall product — bounded payload MVP.

**Phase 6b (graphics OCR):** Core `CompleteVision` + versioned `failure_screen_ocr.v1` prompt. Image sources: operator file (lab AC) or `BMC.CaptureScreenshot` with **Dell** and **Supermicro** OEM adapters (public docs; rich `CaptureDebugStep` traces). sushy has no capture — unsupported error + file path. Telemetry `events.event_type=graphics_ocr`. SOL remains primary; OCR does not commit lifecycle.

**Phase 6c (release packaging + multi-host L0 operators) — detailed plan:**

Operator machines and release artifacts diverge by role. The **lab topology is unchanged** (L0 hypervisor → L1 Ubuntu lab VM → L2 sushy nodes). What changes is (1) how end users obtain a binary and (2) which L0 OSes the Ansible VM-provision path supports cleanly.

#### 6c.1 — Multi-platform binary packaging

| Item | Decision |
|------|----------|
| Targets | `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` |
| Linkage | **`CGO_ENABLED=0`** (stdlib + gofish + pgx; no CGO) |
| Local build | `scripts/build-release.sh` → `dist/shoal_<os>_<arch>[.exe]` + checksums |
| CI | GHA: unit `go test`/`vet`/`staticcheck` on PR; **release** workflow on `v*` tags publishes archives |
| License bundle | Every archive/release asset includes `LICENSE`, `NOTICE`, `docs/third-party-licenses.md` (AGENTS §9.1) |
| Version | Embed via `-ldflags "-X main.version=…"` (or `internal/version`) from git tag / `dev` |
| Compose shoal image | **Deferred to 6d** (optional container wrapping the same binary) |

**macOS operator model (explicit):**

- Supported: download/build **darwin** binary; point env at a **reachable lab** (VM-hosted endpoints on Linux L0, or remote lab host).
- Supported: run Ansible **from** macOS **only** if inventory targets a Linux L0/`shoal-lab-vm` over SSH (controller can be Darwin; `vm_l0` connection local must still be a Linux hypervisor).
- **Not supported:** nested KVM lab on macOS (no `lab_vm` on Darwin). Documented alternative: Linux L0 (including secureblue) or borrow an existing lab.

**Docker Desktop:** not required. Optional Podman/Colima only if the operator chooses containers; default path is binary + remote services.

#### 6c.2 — L0 host profiles for VM-hosted lab

`lab_vm` (play `vm_provision.yml`, hosts `vm_l0`) is the only role that mutates the **outer** hypervisor. Extend it without breaking existing classic Linux L0.

| Profile | Detection (illustrative) | Behavior |
|---------|--------------------------|----------|
| `classic` | Default when not atomic | Require `virsh`, `qemu-img`, seed-ISO tool, nested KVM; **ufw** rules if active (existing) |
| `secureblue` / Fedora Atomic | `/etc/os-release` ID/VARIANT + `rpm-ostree` and/or secureblue markers | Verify preinstalled virt stack; enable **system** libvirt (modular daemons preferred: `virtqemud`/`virtnetworkd`/… or `ujust set-libvirt-daemons` guidance); **firewalld** rules for mgmt bridge (NAT/DNS); do **not** force `libvirt` group membership; clear fail text with operator steps |
| `darwin` | `ansible_system == Darwin` | **Fail fast** with pointer to operator-only docs (do not attempt domain define) |

**Seed ISO tool:** accept any of `genisoimage`, `mkisofs`, `xorriso` (first found) so Atomic/Homebrew hosts without genisoimage still work.

**Firewall:** rename conceptual flag to manage host firewall; keep `shoal_lab_vm_manage_ufw` as compatible alias. Paths: ufw (existing), firewalld (new), neither (no-op).

**L1 unchanged:** `host_prereqs` remains Debian-family install on `shoal-lab-vm` (Ubuntu cloud image). Secureblue is **L0-only**.

**Non-goals (6c):**

- Direct-host lab (`lab.yml`) on secureblue or macOS  
- Nested lab on Darwin  
- Replacing L1 Docker with Podman  
- API auth, metrics, record/replay CI (→ **6d**)

#### 6c acceptance criteria

1. `scripts/build-release.sh` produces four GOOS/GOARCH binaries with `CGO_ENABLED=0`; checksums present.
2. GHA CI runs tests on PR; tag `v*` builds and attaches release assets including license bundle.
3. Docs: operator macOS path; L0 secureblue checklist; README points to both.
4. `lab_vm` on classic Linux L0: existing ufw + nested checks still pass (no regression).
5. `lab_vm` on secureblue profile: detects profile; enables or documents system libvirt; opens firewalld for mgmt bridge when active; fails with actionable messages if virt/nested missing.
6. `lab_vm` on Darwin localhost: fails with operator-only guidance (no partial VM create).
7. AGENTS.md documents packaging + L0 profiles; design §7.1 unchanged unless a new Go dep is added (prefer none).

### Phase 6d (Compose shoal + auth + metrics + replay CI) — detailed plan

#### 6d.1 Compose `shoal` service

| Item | Decision |
|------|----------|
| Enable | `shoal_compose_app: true` (default **true** in lab) |
| Image | Local build: minimal Dockerfile copies CGO-free `shoal` binary (Ansible builds on target or controller and stages binary + Dockerfile under compose dir) |
| Ports | host `shoal_app_http_port` (default **8088**) → container `:8088` |
| Env | Rendered into compose service from lab vars: telemetry DSN (in-network host `telemetry-db`), NetBox URL/token, Ollama, AI models, ISO paths, BMC lab defaults, `SHOAL_API_TOKEN`, fewshot/profile dirs as volumes |
| Volumes | ISO publish dir, fewshot dir, profile dir (rw as needed) |
| Health | `GET /healthz` |

Not a second orchestration model — same binary as release builds; Compose is lab convenience.

#### 6d.2 API auth

| Item | Decision |
|------|----------|
| Env | `SHOAL_API_TOKEN` — empty = **open** (MVP lab default); non-empty = require `Authorization: Bearer <token>` |
| Protected | all `/v1/*` routes |
| Open | `/healthz`, `/readyz`, `/metrics` |
| Compare | constant-time; never log the token |

CLI continues to talk to services without HTTP API for most ops; when calling API, operators pass the token via curl/env docs.

#### 6d.3 Metrics

| Item | Decision |
|------|----------|
| Endpoint | `GET /metrics` Prometheus **text exposition** (hand-written; **no** prometheus client library) |
| Series | `shoal_http_requests_total{method,code,path}`, `shoal_jobs_started_total`, `shoal_jobs_cancel_total` |
| Deps | stdlib only (`sync/atomic`) |

#### 6d.4 Record/replay CI

| Item | Decision |
|------|----------|
| Corpus | `testdata/redfish/*.json` (sushy-shaped System + root samples) |
| Tests | Unit tests map fixtures → `SystemInfo` / parse helpers without live BMC |
| CI | Covered by existing `go test ./...` in GHA |

#### 6d non-goals

- Full mTLS / OAuth OIDC  
- OpenTelemetry traces  
- Pushing images to a public registry (local compose build only)  
- New OEM screenshot vendors without hardware  

#### 6d acceptance criteria

1. With `shoal_compose_app`, `up.yml` deploys `shoal` container; `GET :8088/healthz` succeeds (smoke optional when enabled).  
2. With `SHOAL_API_TOKEN` set, `/v1/*` returns 401 without Bearer; healthz stays 200.  
3. `/metrics` exposes job + HTTP counters.  
4. Fixture tests under `testdata/redfish` pass in CI.  
5. No new Go module deps (allow-list unchanged).  

Executable checklist: [`docs/phase-6d-plan.md`](./docs/phase-6d-plan.md). Phase 6c checklist remains [`docs/phase-6c-plan.md`](./docs/phase-6c-plan.md).

### Phase 6e+ (later / optional polish)

Additional vendor screenshot adapters as hardware is tested; optional image registry publish; richer tracing. **Not a gate for Phase 7.**

### Phase 7: Full OS install (BMC + SOL)

**Intent:** Install a **real operating system** onto local disk over the **BMC-only** path (Redfish Virtual Media + SOL progress), then reboot into the installed OS. This is beyond Phase **6a**’s bounded `/payload` write MVP.

**Status (v2.0.9):**

| Slice | Status | Notes |
|-------|--------|--------|
| **7a** Ubuntu on nested lab disk | **Complete** | Preferred path: **cloud image-write** (see below). Live-server autoinstall remaster kept as alternate/stretch. |
| **7b** Profile + artifact model | **Deferred** | Superseded by upcoming multi-stage / OS-matrix design. |
| **7c** Second family + NetBox identity polish | **Deferred** | Same; do not implement under old 7b/7c checklist without the new design. |

**Relationship to earlier phases:**

| Piece | Role in Phase 7 |
|-------|-----------------|
| Phase 2 `simulate` | Demo markers only — keep for spike/regression |
| Phase 5b profiles | Future install fields; not required for 7a lab E2E flags path |
| Phase 5c / 6a ISO | Build/publish media; marker ISO + payload |
| Phase 6a `write` | Bounded payload inject; **7a reuses write mechanics** at full OS image scale |
| Phase 6b OCR | Graphics failure screens only; does **not** commit lifecycle |
| Phase 6d Compose/auth | Lab/ops packaging; optional for install path |

**Principles (non-negotiable — Golden Rules apply):**

1. **BMC-only provisioning path** — Virtual Media + one-time boot override; **no PXE**, no provisioning VLAN requirement for the install loop.
2. **SOL is the primary progress channel** — marker phases at least: `DISK_PREP` → `IMAGE_WRITE` → `POSTINSTALL` → `VERIFY` → `DONE` / `ERROR` (+ heartbeats). Same `SHOAL|…` protocol family as Phase 2/6a.
3. **Orchestrator is the sole lifecycle writer**; JobStore remains pure persistence; Observe proposes progress via `jobport` only.
4. **Secrets never** in published ISO contents, SOL logs, slog, or LLM payloads (no password fields from vault in autoinstall user-data without secret-backend indirection / redaction policy).
5. **Cleanup is mandatory** — eject Virtual Media and clear boot override on success, failure, and cancel (existing Deploy finalizer). Accept sushy-tools steady state **Continuous/Hdd** as cleared override after cleanup.
6. Prefer **stdlib + existing allow-list**; new deps only with §7.1 update in the same change.

#### 7a — Ubuntu on nested lab (complete)

| Item | Decision (as shipped) |
|------|------------------------|
| First OS | **Ubuntu** (22.04 cloud image train for lab E2E) |
| **Preferred lab path** | Prepare cloud image (`prepare-ubuntu-cloud-payload.sh`) → gzip raw → **marker ISO** with `payload.gz` on ISO root (not in initrd) + matching kernel modules → Virtual Media → busybox `gunzip\|dd` to `/dev/vda` → SOL markers → reboot |
| Alternate / stretch | Remaster Ubuntu **live-server** ISO with autoinstall seed (`build-ubuntu-autoinstall-iso.sh`); nested sushy often fails to boot/progress reliably |
| Lab AC host | Nested **libvirt guest with a real disk** (L2 under L1); sushy-tools Redfish control plane |
| Progress | Marker `/init` emits `SHOAL|…` through write phases; Observe/jobport; Orchestrator terminal + cleanup |
| Outcome | Bootable Ubuntu root on disk; serial login observed in lab E2E; job reaches **`provisioned`** |

**7a acceptance criteria (met):**

1. Lab job: Virtual Media install ISO → SOL markers through install phases → terminal **`provisioned`** → media ejected / boot override cleared (or sushy Continuous/Hdd accepted as clear).
2. Nested guest has a **bootable Ubuntu root on disk** (full OS image write, not 6a demo payload only).
3. `simulate` and 6a `write` still work (no regression).
4. No secrets in ISO build logs beyond documented non-secret lab identity fields.

**Implementation pointers:** `infra/scripts/prepare-ubuntu-cloud-payload.sh`, `infra/scripts/build-marker-iso.sh` (`autoinstall`→write + on-ISO payload), `internal/deploy/iso` cloud-img path, `docs/lab-runbook.md` Phase 7a, `docs/phase-7-plan.md`.

#### 7b / 7c — Deferred

Profile-driven install artifacts, second OS family, and NetBox identity polish from the original 7b/7c tables are **not** acceptance criteria for closing 7a. Product direction for **multi-stage prep** (wipe/RAID/firmware) → **scripted ISO** (autoinstall / kickstart / Ignition / later Windows) + OS support matrix is specified in a **separate design document** (not this Phase 7 checklist). Do not expand 7b/7c implementation until that design is accepted.

#### Phase 7 non-goals (7.0 / remaining)

- Treating Phase 7 as multi-stage prep + full OS matrix (new design)  
- Windows as a Phase 7 deliverable  
- PXE / DHCP provisioning networks as a required path  
- Replacing SOL with OCR as the progress loop  
- Full distro matrix on day one  

Executable checklist: [`docs/phase-7-plan.md`](./docs/phase-7-plan.md).

---

## 10. Testing Strategy

- **Unit:** Core reconcile/decode/validate; SOL parser tables; redaction; JobStore transitions with fake DB or sqlmock-free sqlite demo optional
- **Integration:** `-tags=integration` against lab (sushy-tools, Postgres `:5433`, NetBox)
- **E2E:** `-tags=e2e` Discover→Observe→Deploy
- **Golden prompts:** `testdata/golden/`
- **Record/replay:** `testdata/redfish/`
- **AI audit logs:** hash, model, tokens, latency

```bash
go test ./...
go test ./... -tags=integration
gofmt -l .
go vet ./...
staticcheck ./...
```

**Lab fidelity caveat:** in-lab proves orchestration, state machine, NetBox, VM happy path, SOL protocol via libvirt serial. Real-hardware-only: Redfish/IPMI SOL transport, vendor VM quirks, graphics OCR, rich SEL.

---

## 11. Security & Operational Considerations (MVP)

- Secrets never reach a cloud LLM; `credential_ref` split; redact maps and slog attrs
- **MVP HTTP API is unauthenticated** — bind to management interface only; anyone on that segment can call Deploy (accepted lab/MVP risk)
- Never log BMC passwords or cloud API keys; Phase 1 includes slog redaction tests
- AI calls auditable (model + prompt version/hash)
- Backoff on AI and per-BMC Redfish
- **No HTTPS-everywhere for ISO** — plain HTTP on management segment; BMCs often reject self-signed VM URLs. TLS with BMC-trusted CA is Phase 6
- **BMC client TLS:** see §4.5 (`off` / `insecure` explicit / `custom_ca`)
- **Malicious ISO host on mgmt net:** trust boundary is the management segment; operators must not expose ISO HTTP beyond it
- Shoal host is high-value (fleet BMC creds); file secrets mode 0600; auditable secret access
- Prefer CGO-free builds; no secrets in binary

**Operability (MVP minimal):**
- `SHOAL_LOG_LEVEL` for slog
- `/healthz` liveness; `/readyz` optional DB ping
- **Metrics/tracing/config reload:** Phase 6 (job counts, BMC errors, SOL stalls). Not required for Phases 1–5 ACs beyond structured logs

---

## 12. How AI Agents Should Use This Document

1. Read this file + `AGENTS.md` first
2. Re-read Chapter 4 for the component you touch
3. Propose architecture changes as doc updates first
4. Deterministic code over AI for structured inputs; AI only via Core Reconciler
5. No secrets in models/logs/LLM
6. Tests alongside code
7. **No Python application code**
8. **No deps outside §7.1** without amending this doc
9. Respect import direction: Core never imports Discover; **observe ↛ deploy and deploy ↛ observe**; only Orchestrator commits lifecycle via JobStore; ports live in `common/jobport` and `common/watchport`
10. PR0: land this doc + rewrite AGENTS (fix DoD that still says `make lint`/`make test`)

### 12.1 AGENTS.md implications (Go)

Golden Rules:
1. LLM only via `internal/core/ai`
2. Deterministic-first; Discover owns adapters; Core owns AI reconcile only
3. Secrets never to LLM/log; `credential_ref` only
4. NetBox identity + lifecycle only
5. SOL markers primary progress; OCR diagnostic
6. Redfish only via `internal/common/redfish` (**gofish-backed**; no gofish types outside that package)
7. Plain HTTP ISO on mgmt segment for MVP
8. Structs + validate/decode pipeline for AI I/O
9. Component boundaries §3–4; **no observe↔deploy imports** (use `jobport`/`watchport`)
10. Single-process MVP; Orchestrator commits lifecycle; JobStore is persistence only

```bash
gofmt -w .
go vet ./...
staticcheck ./...
go test ./...
```

---

## 13. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Virtual Media throughput | Medium | Medium | Lean live image |
| Spec-deviant Redfish | High | Medium | gofish wrappers + hybrid AI + fixtures |
| gofish quirks / API drift | Medium | Medium | Thin Shoal `BMC` facade; fixtures; pin gofish version in `go.mod` |
| BMC session lockout | High | Medium | Caps + backoff + reuse |
| SOL transport variance | Medium | Medium | `sol.Transport`; lab libvirt; HW stretch |
| Secrets → LLM | Medium | High | Redact + tests |
| Vision VRAM | High | Medium | Cloud vision |
| sushy-tools fidelity | High | Medium | Document gaps; Phase 2 lab first |
| Prompt drift | Medium | High | Versioned prompts + goldens |
| Dependency creep | Medium | Medium | Allow-list |
| Job loss on restart | Medium | High | Durable `jobs` table + reconcile |
| Unauthenticated API abuse on mgmt | Medium | Medium | Bind interface; Phase 6 auth |
| Two-week demo slip on Redfish/live image | Medium | Medium | AI off critical path; fixture-backed spike |

---

## 14. Appendices (To Be Expanded by Agents)

- A. Starting prompts + `prompts/schemas/*.json`
- B. Sample app env + Compose `shoal` service snippet
- C. NetBox custom fields
- D. Redfish / **gofish** wrapper notes — Phase 2 operations, sushy-tools paths, fixture layout, TLS matrix, version pin
- E. SOL protocol full parser reference
- F. JobStore transitions, timeouts, cleanup, orphan reconcile
- G. Record/replay corpus layout
- H. Dependency allow-list change process
- I. Minimal live image build — packages, scripts, marker producer, **publish to lab ISO dir + URL**; **primary:** Ansible role on lab VM; **alternate:** workstation build + rsync/scp (§8.2)
- J. Composition-root wiring example (`jobport` / `watchport` injection)

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
| Phase 2 binding | **CLI flags** (bmc-url, creds, serial-target, iso-url) | Discover/NetBox optional for spike |
| Phase 2 ISO | **Lab nginx `:8080`** after copy to ISO dir | Matches existing lab; no in-process server required |
| Live image build host | **Lab VM Ansible primary**; workstation alternate | Reproducibility + local iteration (§8.2) |
| Persistence | **Postgres jobs+telemetry** on lab `:5433` | Durable orphans; match Compose |
| Watch UX | Poll job status | MVP simplicity |
| App port | **`:8088`** | Confirmed `shoal_app_http_port` / `SHOAL_HTTP_ADDR` |
| API auth | Unauthenticated on mgmt bind | Lab/MVP; explicit threat model |
| OCR (Phase 6) | **Deferred decision** | Evaluate Tesseract vs cloud vision in Phase 6 |
| Concurrency | Root ctx, per-job child ctx, mutex map, cleanup on shutdown | Avoid races/leaks |
| Packaging | Single binary | Distribution goal |
| Lab | Keep Ansible + sushy-tools | Already Phase 0 |
| UI | Deferred | API+CLI only for MVP |

---

## PR Plan

| # | Title | Affects | Depends on | Description |
|---|-------|---------|------------|-------------|
| PR0 | `docs: adopt design v2.0.3 (Go) + rewrite AGENTS.md` | Design, AGENTS | — | SoT + Go golden rules; fix DoD (`gofmt`/`vet`/`staticcheck`/`go test`, not make); module path `github.com/mattcburns/shoal` |
| PR1 | `chore: go module + serve/healthz skeleton` | `cmd/shoal`, empty internals | PR0 | `go mod init github.com/mattcburns/shoal`; `version`, `serve` on **`:8088`**, `/healthz`, `/readyz` stub, config loader (DSN optional); jobport/watchport interface stubs |
| PR2 | `feat(common): models, validate, redact, secrets` | `common/*` | PR1 | Complete §5 models (incl. SOLMarker, DeviceID on events); redaction tests; secrets file backend |
| PR3 | `feat(deploy+telemetry): Postgres JobStore + events schema` | `common/telemetry`, `deploy/jobstore` | PR2 | Migrations/DDL for `jobs`+telemetry tables; **pure** JobStore CRUD/progress/transition; DSN required for tests |
| PR4 | `feat(common): gofish Redfish wrapper (basic auth + VM/boot/power)` | `common/redfish` | PR2 | Depend on gofish; Shoal `BMC` interface; lab basic auth; TLS modes; fixtures/integration vs sushy-tools |
| PR5 | `feat(observe): SOL parser + libvirt transport` | `observe/sol` | PR2 | Table tests → `models.SOLMarker`; stall timer helpers |
| PR6 | `feat(deploy): live image marker producer + lab ISO publish` | `deploy/iso`, Ansible notes | — (parallel) | Build minimal ISO (**lab VM Ansible primary**, workstation alternate); **copy to lab ISO dir**; print `http://…:8080/<name>.iso` |
| PR7 | `feat(deploy+observe): Orchestrator + jobport/watchport wiring` | `deploy/job`, `observe`, `cmd/shoal` | PR3–PR6 | Composition-root inject; HandleTerminal; progress-only vs terminal; cancel; orphan reconcile; **no observe↔deploy imports** |
| PR8 | `test: Phase 2 thesis spike E2E` | lab tags, CLI `deploy run` flags | PR4–PR7, Phase 0 | Flag-bound BMC+serial+iso-url; VM boot → SOL → DONE → cleanup; **no AI/Discover** |
| PR9 | `feat(core): AI HTTP client + decode pipeline` | `core/ai` | PR2 | Real Ollama/cloud; fence strip; golden harness |
| PR10 | `feat(discover): deterministic adapters + confidence gate` | `discover/adapters` | PR2 | Redfish/CSV; no Core AI required yet |
| PR11 | `feat(core+discover): Reconciler + hybrid merge + NetBox ingest` | `core/reconcile`, `discover`, `netbox` | PR9, PR10, PR3 | Full hybrid; conflict policy; redaction-to-LLM tests |
| PR12 | `feat(observe): SEL/sensor poll + watch mode` | `observe` | PR4, PR5, PR3 | Broaden Observe |
| PR13 | `feat(deploy): full ISO pipeline + profile gen` | `deploy/iso`, `core/profile` | PR7, PR9 | Static base inject; approval gate |
| PR14 | `chore: Compose shoal service + Ansible env contract` | `compose_stack`, group_vars | PR1+ | Binary image; `env.j2` NetBox token + DSN + HTTP port |
| PR15 | `feat: Phase 6 polish` | OCR (choose approach), metrics, API auth, TLS CA | MVP | Evaluate OCR options then implement; other hardening |
| PR16 | `feat: Phase 6c packaging + L0 host profiles` | `scripts/build-release.sh`, GHA, `lab_vm`, docs | Phase 6a–b on master | Multi-platform CGO-free binaries + NOTICE; macOS operator docs; secureblue L0 + firewalld; keep classic L0 |
| PR17 | `feat: Phase 6d compose shoal + auth + metrics + replay` | compose_stack, `api`, config, fixtures | 6c on master | Lab Compose app service; Bearer token; `/metrics`; redfish fixture CI |
| PR18 | `docs: Phase 7 full OS autoinstall plan (v2.0.8)` | design, `docs/phase-7-plan.md`, AGENTS | 6d on master | Numbered Phase 7 (7a–7c); 6a remains payload MVP |
| PR19+ | `feat: Phase 7a Ubuntu autoinstall…` | deploy/iso, live image, profiles, lab | PR18 | Implementation slices per `docs/phase-7-plan.md` |

**High-level order:** Docs → skeleton → models/secrets → **Postgres jobs** → Redfish + SOL + **live image** → Deploy orchestrator → **Phase 2 spike** → **AI** → Discover hybrid → Observe broaden → Deploy harden → Compose package → Phase 6 polish → **Phase 7 full OS autoinstall**.

---

## Open Questions

**All previously open product decisions are resolved through v2.0.8.** Phases **0–6d** are on `master`. Phase **6** polish (6a–6d) is complete; **6e+** is optional. **Phase 7** (full OS autoinstall) is next.

| Topic | Resolution |
|-------|------------|
| SQL default | **Postgres-primary** (lab `telemetry-db :5433`); SQLite demo-only |
| AGENTS dual-stack | **Replace** Python AGENTS; no dual-stack app |
| Watch transport | **Poll-only** MVP (`GET /v1/jobs/{id}`); SSE/WS later |
| Hybrid ownership | Discover deterministic + Core AI Reconciler only |
| Job writers | Orchestrator sole lifecycle writer; JobStore = persistence |
| Observe↔Deploy coupling | Neutral `jobport`/`watchport` + composition root; no sibling imports |
| Phase 2 device binding | CLI flags (`-bmc-url`, creds, `-serial-target`, `-iso-url`); Discover optional |
| Phase 2 ISO serve | Lab nginx `:8080` after publish to ISO dir |
| **Module path** | **`github.com/mattcburns/shoal`** |
| **Redfish client** | **gofish day one**, wrapped in `internal/common/redfish` (no thin-client-first path) |
| **App HTTP port** | **`:8088`** (`shoal_app_http_port` / `SHOAL_HTTP_ADDR`) |
| **Phase 6 graphics OCR** | **CompleteVision** (lab Ollama VLM / cloud) for failure screens; not Tesseract-first; not the same as Phase 3 asset-label OCR |
| **Live image build host** | **Both:** lab VM Ansible role **primary**; developer workstation **alternate** (§8.2 / Appendix I) |
| **Lab AI text model** | **`SHOAL_AI_MODEL=llama3.2:3b`** (instruct; nested-lab friendly) |
| **Lab AI vision model** | **`SHOAL_AI_VISION_MODEL=deepseek-ocr`** for asset-label Free OCR; **moondream not AC**; empty skips photo |
| **Complete vs CompleteVision** | Text Discover → `Complete` + text model; photo → `CompleteVision` (`Free OCR.`) + parse SERIAL/VENDOR/MODEL; missing serial → **fail** (no synthetic IDs) |
| **Phase 6c packaging** | Multi-platform CGO-free binaries + GHA release; license bundle; macOS = **operator only** |
| **Phase 6c L0 hosts** | Classic Linux + **secureblue/Atomic** for VM-hosted lab; Darwin L0 nested lab **unsupported** |
| **Compose shoal / API auth / metrics / replay CI** | **Phase 6d** (v2.0.7) — **done on master** |
| **Phase 6d API auth** | Optional Bearer `SHOAL_API_TOKEN`; open if empty; protects `/v1/*` only |
| **Phase 6d metrics** | Stdlib Prometheus text `/metrics`; no new deps |
| **Full OS autoinstall** | **Phase 7** (v2.0.8): 7a Ubuntu autoinstall E2E → 7b profiles/artifacts → 7c second path + NetBox identity polish |
| **Phase 6a vs 7** | **6a** = bounded `/payload` write MVP; **7** = real OS install + reboot into installed system |

**Deferred until Phase 6e+ (optional; not blocking Phase 7):**

- Additional vendor screenshot adapters; registry image publish; distributed tracing.

---

## References

- Prior design: v1.1 Python stack in repo history / previous SoT
- Lab: `docs/lab-runbook.md`, `infra/ansible/`, README endpoints
- Compose telemetry DB: port `5433`, DB `shoal_telemetry`
- Module: `github.com/mattcburns/shoal`
- gofish: `github.com/stmcginnis/gofish`
- Go ServeMux 1.22+ routing enhancements
- DMTF Redfish; Ollama / OpenAI-compatible HTTP APIs

---

**This document (v2.0.8) is the SoT for agents.** Phases **0–6d** are on `master` (Phase 6 polish complete for 6a–6d).

Next actions:
1. Phase **7**: full OS autoinstall — implement **7a** first ([`docs/phase-7-plan.md`](./docs/phase-7-plan.md); design § Phase 7)
2. Phase **7b–7c**: profile/artifact model; second install path + NetBox identity polish
3. Optional **6e+**: more vendor screenshot adapters; registry image publish; richer tracing
