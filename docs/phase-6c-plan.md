# Phase 6c — Packaging + L0 host profiles

Executable checklist for design **v2.0.6** § Phase 6c. Design SoT:
[`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](../SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md).

## Goals

1. **Ship CGO-free multi-platform binaries** (linux/darwin × amd64/arm64) with license notices.
2. **Document macOS as operator-only** (binary + remote/Linux lab; no nested L0 on Darwin).
3. **VM-hosted lab on secureblue L0** via Ansible, without breaking classic Linux L0.
4. Keep L1 Ubuntu stack and direct-host Debian path unchanged.

## Non-goals

- Compose `shoal` container image (6d)
- API auth, metrics, record/replay CI (6d)
- Direct-host lab on secureblue or macOS
- Nested KVM lab on macOS

## Workstreams

### A — Release packaging

| # | Task | AC |
|---|------|----|
| A1 | `scripts/build-release.sh` — matrix build, checksums, copy LICENSE/NOTICE/third-party | Four binaries under `dist/` |
| A2 | Version stamp via ldflags (`internal/version` or `main`) | `shoal version` / help shows tag or `dev` |
| A3 | GHA CI: `gofmt` check, `go vet`, `staticcheck`, `go test ./...` | Green on PR |
| A4 | GHA release on `v*` tags: build matrix + attach archives + license bundle | GitHub Release assets |
| A5 | Docs: `docs/operator-macos.md` + README packaging blurb | Mac path clear |

### B — L0 Ansible profiles (`lab_vm`)

| # | Task | AC |
|---|------|----|
| B1 | Detect L0 profile: `classic` \| `secureblue` \| `darwin` | Fact set early in role |
| B2 | Darwin: fail fast with operator docs pointer | No domain define attempted |
| B3 | Seed ISO tool: `genisoimage` \| `mkisofs` \| `xorriso` | First match works |
| B4 | Secureblue: verify virt tools; enable system libvirt (modular preferred); actionable fail if missing | Clear messages / optional enable tasks |
| B5 | Firewall: ufw (existing) + firewalld (new); keep ufw flag as alias | NAT/DNS for mgmt bridge |
| B6 | Do not force `libvirt` group on secureblue | Polkit/auth model preserved |
| B7 | Docs: L0 secureblue section in checklist + runbook | Operator can re-`up.yml` |

### C — Agent / design hygiene

| # | Task | AC |
|---|------|----|
| C1 | AGENTS.md packaging + L0 notes; layout lists scripts/GHA | Agents know the contract |
| C2 | Design v2.0.6 Phase 6c + PR16 | SoT matches code |

## Suggested PR split

1. **PR16a (docs/design):** design v2.0.6 + this plan (may ship with implementation).
2. **PR16b (lab):** `lab_vm` profiles + firewalld + docs.
3. **PR16c (packaging):** build script + version + GHA + Mac docs.

Or one PR if the diff stays reviewable.

## Verify

```bash
# Packaging
./scripts/build-release.sh
ls dist/
CGO_ENABLED=0 go test ./...

# Lab (classic or secureblue L0)
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/up.yml --ask-become-pass
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/smoke.yml --ask-become-pass
```

## Done when

All 6c acceptance criteria in design § Phase 6c are met; classic L0 path not regressed; Mac and secureblue paths documented and automated where feasible.
