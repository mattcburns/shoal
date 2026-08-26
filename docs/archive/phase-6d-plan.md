# Phase 6d — Compose shoal + auth + metrics + replay CI

Executable checklist for design **v2.0.7** § Phase 6d.

## Goals

1. Optional lab **Compose service** running the Shoal binary on `:8088`.
2. Optional **Bearer API token** for `/v1/*`.
3. **`/metrics`** Prometheus text (stdlib only).
4. **Redfish fixture tests** in unit CI (`testdata/redfish/`).

## Non-goals

- OIDC/mTLS, OpenTelemetry, public image registry, new OEM screenshot vendors without hardware.

## Workstreams

### A — Design hygiene
- [x] Design v2.0.7 Phase 6d + this plan

### B — API auth + metrics
- [x] `SHOAL_API_TOKEN` + Bearer middleware
- [x] `/metrics` counters
- [x] Config + unit tests
- [x] AGENTS env table

### C — Compose shoal
- [x] Dockerfile (binary copy)
- [x] compose service + Ansible stage binary/build (controller build → lab host)
- [x] env wiring (host network → published lab ports)
- [x] smoke check when enabled

### D — Record/replay fixtures
- [x] Fixture corpus + parse tests
- [x] Covered by `go test ./...` / GHA CI

## Verify

```bash
go test ./...
# with lab + shoal_compose_app:
curl -sf "http://${SHOAL_LAB:-192.168.122.100}:8088/healthz"
curl -sf "http://${SHOAL_LAB:-192.168.122.100}:8088/metrics"
```
