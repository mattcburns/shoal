# Phase 2 live image (marker producer)

Phase 2 needs a minimal live image that emits `SHOAL|…` markers over serial
(`console=ttyS0,115200n8`) and is published to the lab ISO HTTP tree (`:8080`).

## Protocol

```
SHOAL|<schema_ver>|<seq>|<iso8601_utc>|<phase>|<percent>|<state>|<detail>
```

See design §4.3. Reference shell emitter (non-bootable harness):
[`infra/scripts/marker-producer.sh`](../infra/scripts/marker-producer.sh).

## Build + publish

### Primary: Ansible on lab VM (L1)

```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/build_marker_iso.yml
```

This installs build deps on the lab host, runs
`infra/scripts/build-marker-iso.sh` (busybox initramfs + host kernel + isolinux),
and writes `{{ shoal_iso_server_dir }}/shoal-marker.iso` (default `/srv/iso/`).

### Alternate: workstation

```bash
# needs busybox, cpio, gzip, xorriso, isolinux/syslinux-common, a kernel
./infra/scripts/build-marker-iso.sh ./shoal-marker.iso
scp -i ~/.ssh/shoal_lab_vm ./shoal-marker.iso lab@192.168.122.100:/tmp/
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 \
  'sudo cp /tmp/shoal-marker.iso /srv/iso/shoal-marker.iso'
```

## URLs

| Viewer | URL |
|--------|-----|
| Operator (VM mode L0) | `http://192.168.122.100:8080/shoal-marker.iso` |
| Nested BMC nodes | `http://192.168.124.1:8080/shoal-marker.iso` |
| Direct mode | `http://127.0.0.1:8080/shoal-marker.iso` |

Pass the **BMC-reachable** URL as `-iso-url` to `shoal deploy run`.

## Serial (VM mode)

Nested domains live on L1. From L0 set:

```bash
export SHOAL_SERIAL_SSH_HOST=192.168.122.100
export SHOAL_SERIAL_SSH_USER=lab
export SHOAL_SERIAL_SSH_KEY=$HOME/.ssh/shoal_lab_vm
```

Or flags: `-serial-ssh-host`, `-serial-ssh-user`, `-serial-ssh-key`.

## Example deploy (VM lab)

```bash
export SHOAL_BMC_USERNAME=admin   # vault
export SHOAL_BMC_PASSWORD=...
export SHOAL_TELEMETRY_DATABASE_URL='postgres://shoal:…@192.168.122.100:5433/shoal_telemetry?sslmode=disable'
export SHOAL_SERIAL_SSH_HOST=192.168.122.100
export SHOAL_SERIAL_SSH_KEY=$HOME/.ssh/shoal_lab_vm

go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -serial-target shoal-node-1 \
  -iso-url http://192.168.124.1:8080/shoal-marker.iso \
  -wait-timeout 5m
```

`device-id` is also used as the Redfish system **name** lookup when `-system-id`
is omitted (sushy-tools multi-system).

## Spike without a full ISO

Unit tests inject markers via `ReaderTransport`. Lab integration tests
(`-tags=integration`) cover:

| Test | Path |
|------|------|
| `TestLabDeployRealBMCInjectedSOL` | Live BMC + injected SOL → DONE |
| `TestLabDeployCancel` | Live BMC + cancel → FAILED + cleanup |
| `TestLabDeployStall` | Live BMC + SOL silence → stall FAILED + cleanup |
| `TestLabDeployRealSOLSSH` | Live BMC + SSH serial + bootable ISO → DONE |

```bash
export SHOAL_BMC_USERNAME=... SHOAL_BMC_PASSWORD=...
export SHOAL_TELEMETRY_DATABASE_URL='postgres://shoal:…@192.168.122.100:5433/shoal_telemetry?sslmode=disable'
export SHOAL_SERIAL_SSH_HOST=192.168.122.100   # for RealSOLSSH
export SHOAL_SERIAL_SSH_KEY=$HOME/.ssh/shoal_lab_vm

go test ./internal/deploy/job -tags=integration -count=1 -v
```

## API

With `shoal serve` (job store + orchestrator wired):

- `GET /v1/jobs/{id}` — poll status
- `POST /v1/jobs/{id}/cancel` — request cancel (async cleanup → `failed`)

## Lab quirks (sushy-tools)

- Boot override “Once” may appear as `Continuous` + `Cd`; cleanup sets `Hdd`.
- Marker ISO powers off when DONE; power state may be `Off` after success.
- Nested serial PTYs are on L1 only — use SSH serial from L0.
