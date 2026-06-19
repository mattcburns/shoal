# AGENTS.md — Working Conventions for Shoal

This file is the canonical guide for how to work in this repository. It covers
project conventions, commands, and style. For **architecture, data models, and
the phased plan**, the source of truth is
[`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](./SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md)
(v1.1). When this file and the design doc disagree, fix one of them in the same
change — they must stay consistent.

> **Read order for any task:** (1) this file, (2) the relevant Chapter 4 section
> of the design doc for the component you're touching, (3) the code.

---

## 1. Golden Rules (non-negotiable invariants)

These encode the core design decisions. Violating one is a bug, even if tests pass.

1. **All LLM calls go through `shoal_core` via LiteLLM.** No provider SDKs
   (`openai`, `anthropic`, …) are imported outside `shoal_core/ai.py`.
2. **Deterministic-first.** Parse structured inputs (Redfish JSON, CSV) with code;
   use AI only to reconcile incomplete/ambiguous/spec-deviant data. Never route
   already-structured data straight to an LLM.
3. **Secrets never reach an LLM or a log.** BMC credentials live in the secret
   backend and are referenced by an opaque `credential_ref`. Strip password-like
   fields from any payload **before** it touches a model. `NormalizedAsset` must
   never contain a password.
4. **NetBox stores identity + current `lifecycle_state` only.** Time-series and
   events (SEL, sensors, job logs) go to the telemetry store — never into NetBox
   custom fields.
5. **SOL is the primary provisioning feedback channel.** Progress comes from the
   `SHOAL|...` serial marker protocol. OCR is only for graphics-only failure
   screens, never the progress loop.
6. **Redfish only via `sushy`.** No `gofish` (it's Go) and no ad-hoc HTTP to BMCs.
   Reuse sessions, cap per-BMC concurrency, and back off on throttling.
7. **Artifact/ISO serving is plain HTTP on the management segment for MVP.** Do not
   add TLS to the ISO server (many BMCs reject self-signed certs); TLS is Phase 6.
8. **Pydantic everywhere AI or cross-component data flows.** Structured output is
   enforced; validate all model output against the schema.
9. **Respect component boundaries** (Section 4). Core never calls Redfish or writes
   NetBox; Discover/Observe/Deploy never call an LLM directly — they delegate to Core.
10. **MVP runs as one process.** Don't introduce Celery/Redis/microservices without
    a demonstrated bottleneck.

---

## 2. Repository Layout

```
shoal/
  core/       # shoal_core: AI brain — LiteLLM, prompts, hybrid normalization, RAG
  discover/   # ingestion, deterministic parsers/adapters, NetBox writes
  observe/    # SEL/sensor polling, SOL session + marker parsing, telemetry, OCR
  deploy/     # ISO build/serve, sushy Virtual Media orchestration, job state machine
  common/     # shoal_common: shared Pydantic models, secret backend, utilities
prompts/                  # versioned prompt files (also referenced as shoal_core/prompts/)
tests/                    # unit / integration / e2e
SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md
AGENTS.md
```

**Dependency direction:** `discover`, `observe`, `deploy` may import `core` and
`common`. `core` may import `common` only. `common` imports nothing internal.
Never create a cycle.

---

## 3. Commands

The lab and app are driven through `make` targets (see design doc §8). Phase 0
wires the lab; Phase 1 wires the app.

```bash
make lab-up          # start infra: sushy-tools + NetBox + HTTP ISO server + Ollama
make lab-down        # tear the lab down
make dev-up          # run the Shoal app (single process)
make demo-provision  # end-to-end demo against virtual nodes
make test            # full test suite
make lint            # ruff check
make fmt             # ruff format
make typecheck       # static type checking
```

Direct invocations (when not using make):

```bash
ruff check . && ruff format --check .
pytest                                  # all tests
pytest -m "not integration and not e2e" # fast unit-only loop
pytest tests/core/test_normalize.py -k spec_deviant
```

- **Python 3.11+.** Use the project virtualenv; dependencies are declared in
  `pyproject.toml`.
- Always run `make lint` and `make test` before declaring a task done.

---

## 4. Coding Style

- **Formatting/linting:** `ruff` (format + lint). Run `make fmt` before committing;
  CI runs `ruff check` and will fail on violations. Don't hand-format around it.
- **Typing:** full type hints on all public functions. Prefer modern syntax
  (`str | None`, `list[X]`). Code should pass `make typecheck`.
- **Async:** I/O (Redfish, NetBox, LLM, HTTP) is `async`. Don't block the event
  loop; wrap unavoidable blocking calls (e.g. subprocess for `dracut`/`xorriso`)
  appropriately.
- **Naming:** `snake_case` for functions/vars, `PascalCase` for Pydantic models,
  `UPPER_SNAKE` for constants. Modules are lowercase.
- **Errors:** raise typed exceptions with actionable messages. Never swallow
  exceptions silently; never log secrets in the message.
- **Docstrings:** one-line summary for every public function; document the contract
  (inputs, outputs, side effects), not the obvious.
- **Config:** read configuration from environment (`.env`), never hard-code
  endpoints, tokens, or model names. Keep `.env.example` current.

---

## 5. Data Models & State

- All shared models live in `shoal_common/models.py` (see design doc §5):
  `NormalizedAsset`, `FieldConfidence`, `NormalizationResult`, `NormalizedEvent`,
  `LifecycleState`, `ProvisioningJob`, `ProvisioningProfile`, `DeviceStatus`,
  `WatchSession`.
- Changing a shared model is a cross-component change: update all consumers and
  the design doc §5 in the same change.
- The provisioning lifecycle follows the state machine in §5. Only Deploy mutates
  job state; transitions must be explicit and logged.

---

## 6. AI & Prompt Conventions

- Prompts are **versioned files** under `shoal_core/prompts/` (`.md`/`.txt`).
  Treat a prompt change like an API change: bump it and update golden tests.
- Every prompt requests JSON matching a Pydantic schema, includes 2–4 few-shot
  examples, states the model's role, and asks for **per-field confidence + a raw
  excerpt as evidence**.
- **Redact secrets** from every payload before the call.
- Log every AI call: prompt hash, model, token counts, latency, and output. These
  logs must contain no secrets.
- Local vs cloud is selected via env (`SHOAL_AI_PROVIDER`, `SHOAL_AI_MODEL`).
  Don't assume a GPU: vision is cloud-preferred (the reference GPU has 2 GB VRAM).

---

## 7. Redfish / BMC Conventions

- Use `sushy` for all Redfish interaction; isolate per-vendor quirks in adapters,
  not in call sites.
- **Sessions:** authenticate with session tokens, reuse and close them, cap
  concurrency per BMC (~1–2), and back off on 4xx/5xx (respect `Retry-After`).
- **Idempotency:** every Deploy action reads current state and converges; re-runs
  must be safe.
- **Cleanup is mandatory:** always eject Virtual Media and clear the one-time boot
  override on success, failure, and cancel. Leaving them set bricks the next boot.
- **SOL ownership:** Observe holds the single SOL session for a node during a job.
- Capture real Redfish responses into the **record/replay corpus** when you hit a
  new vendor/firmware shape, and add a fixture-based test.

---

## 8. Testing

- Framework: `pytest` + `pytest-asyncio`. Mark slow/external tests with
  `@pytest.mark.integration` (needs the lab) and `@pytest.mark.e2e`.
- **Unit:** every public Core function, especially the hybrid normalizer and the
  deterministic/AI conflict policy.
- **Integration:** against `sushy-tools` lab nodes (`make lab-up` first).
- **E2E:** full Discover → Observe → Deploy against the lab.
- **Prompt regression:** golden inputs/outputs for normalization; update
  deliberately when a prompt changes.
- **SOL protocol:** validate producer/consumer/marker parsing against the libvirt
  serial console.
- **Record/replay:** vendor Redfish fixtures for adapter + reconciliation tests.
- Know the **lab fidelity gap** (design doc §10): sushy-tools cannot prove SOL
  transport on real BMCs, vendor Virtual Media quirks, graphics OCR, or realistic
  SEL/sensor variety. Don't claim those are validated from lab runs alone.

---

## 9. Security & Secrets

- Never commit secrets. `.env` is git-ignored; keep `.env.example` as the
  documented template (placeholders only).
- BMC credentials go to the secret backend keyed by device/`bmc_ip`; code holds a
  `credential_ref`, never the password.
- Don't log credentials, tokens, or full raw payloads that may contain them.
- The Shoal host holds fleet BMC credentials — treat it as high-value; keep
  credential access auditable.

---

## 10. Git & Pull Requests

- **Branches:** `feature/<phase>-<short-desc>`, `fix/<short-desc>`.
- **Commits:** Conventional Commits (`feat:`, `fix:`, `refactor:`, `docs:`,
  `test:`, `chore:`). Keep them scoped and reference the design doc section or
  phase when relevant. Include a `Co-Authored-By:` trailer identifying the agent.
- **Don't commit unless asked** to by the human driving the work.
- **PR description:** what changed, which phase/acceptance criteria it advances,
  how it was tested (note lab vs real-hardware), and any design-doc updates.

---

## 11. Definition of Done

A change is done when:
- It satisfies the acceptance criteria of its phase in the design doc.
- `make lint`, `make typecheck`, and `make test` pass.
- New/changed behavior has tests (unit at minimum; integration/e2e where relevant).
- No secret can reach a log or an LLM (add a redaction test if you touched that path).
- The design doc is updated if you changed architecture, models, prompts, or scope.
- Component boundaries and the Golden Rules (§1) are intact.
