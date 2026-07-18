# Phase 7 — Full OS autoinstall

Executable checklist for design **v2.0.8** § Phase 7. Design SoT:
[`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](../SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md).

## Goal

Install a **real OS** onto local disk over the **BMC-only** path (Redfish Virtual
Media + SOL markers), reboot into the installed system, and keep cleanup +
lifecycle rules intact.

**Not this phase:** Phase **6a** bounded `/payload` write (still supported).
**Not required first:** Phase **6e+** polish (OEM screenshots, registry, tracing).

## Principles

- No PXE / provisioning VLAN for the install loop
- SOL primary progress (`DISK_PREP` → `IMAGE_WRITE` → `POSTINSTALL` → `VERIFY` → `DONE` / `ERROR`)
- Orchestrator sole lifecycle writer; JobStore pure persistence
- Secrets never in published ISO, SOL logs, slog, or LLM payloads
- Mandatory Virtual Media eject + boot override clear on all terminals

## Sub-phases

### 7a — Ubuntu autoinstall E2E

| # | Task | AC |
|---|------|----|
| A1 | Install/autoinstall media pipeline (Ubuntu Server train) | Published ISO on lab `:8080` |
| A2 | SOL producer markers through install + heartbeats | Parseable by existing SOL path |
| A3 | Deploy job: attach media, boot, watch markers, cleanup | Terminal + BMC cleanup |
| A4 | Lab: nested libvirt guest with real disk | Bootable Ubuntu after job |
| A5 | Regression: `simulate` + 6a `write` still pass | No break |

### 7b — Profile + artifact model

| # | Task | AC |
|---|------|----|
| B1 | Extend `ProvisioningProfile` for install fields | Schema + validate |
| B2 | Profile → ISO resolve / build (5c/6a patterns) | Start without hand `-iso-url` happy path |
| B3 | Install mode distinct from `simulate` / `write` | Documented contract |
| B4 | Approval gates for destruct/wipe (5b) | Still enforced |

### 7c — Second path + identity polish

| # | Task | AC |
|---|------|----|
| C1 | Second family (kickstart) **or** image-write path | Lab-demonstrated or design defer note |
| C2 | NetBox device identity binding after success | Lifecycle + identity consistent |
| C3 | Failure paths still cleanup | No stuck media/override |

## Non-goals (7.0)

- Windows
- PXE-required topology
- Full distro matrix on day one
- OCR as install progress loop

## Suggested PR split

1. **PR18 (docs):** design v2.0.8 + this plan (this change)
2. **PR19+:** 7a implementation
3. Later: 7b, 7c

## Verify (after implementation)

```bash
# Unit / lint
gofmt -l .
go vet ./...
staticcheck ./...
go test ./...

# Lab (shape — exact CLI flags land in 7a PR)
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/smoke.yml
# then Phase 7a deploy autoinstall job against a nested guest with a disk
```

## Done when

Phase 7 section ACs in the design doc are met for the slice under implementation;
6a and Phase 2 spike paths remain green; Golden Rules §1 intact.
