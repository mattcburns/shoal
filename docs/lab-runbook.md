# Shoal Lab Runbook (Quick Ops)
Day-to-day operation of the Shoal lab. All lifecycle actions are Ansible playbooks; there is no Makefile.

## Scope
- Primary workflow: VM-hosted lab mode (direct host mode supported too)
- Goal: get the lab up quickly, verify health, recover fast when something breaks

## Network model
Two separate libvirt networks on **different subnets** (do not merge them):
- **L0 management network** (`shoal-mgmt-net`, `192.168.122.0/24`, bridge `shoalbr0`): the host network the lab VM attaches to. Created automatically by `up.yml` (via the `lab_vm` role) before the VM is created — you do NOT need libvirt's `default` network. The lab VM has the static IP `shoal_lab_vm_host` (default `192.168.122.100`) here, and all lab service endpoints are published on it.
- **Lab BMC network** (`shoal-lab-net`, `192.168.124.0/24`, bridge `virbr1`): an isolated NAT network created *inside* the lab VM for the virtual BMC nodes (`shoal-node-*`). Nodes reach lab services via the gateway `192.168.124.1` (e.g. ISO at `http://192.168.124.1:8080`).

## 0) One-time setup
```bash
# Install required Ansible collections
ansible-galaxy collection install -r infra/ansible/requirements.yml

# Configure secrets
cp infra/ansible/inventory/group_vars/all/vault.yml.example \
   infra/ansible/inventory/group_vars/all/vault.yml
# edit vault.yml, then optionally encrypt it:
ansible-vault encrypt infra/ansible/inventory/group_vars/all/vault.yml
```
Non-secret config lives in `infra/ansible/inventory/group_vars/all/defaults.yml` (and `vm_mode.yml` / `direct_mode.yml` for mode-specific values). Edit those YAML files to change VM name/IP, networks, ports, etc.

If `vault.yml` is encrypted, add `--ask-vault-pass` (or `--vault-password-file <file>`) to every command below.

## 1) Fast start (happy path)
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
```
`up.yml` ends by building/publishing the Phase 2 marker ISO to `/srv/iso/shoal-marker.iso`
(via `build_marker_iso.yml`). Rebuild ISO only:

```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/build_marker_iso.yml
```

### Phase 2 app smoke (from L0 against VM lab)
```bash
export SHOAL_BMC_USERNAME=...   # vault
export SHOAL_BMC_PASSWORD=...
export SHOAL_TELEMETRY_DATABASE_URL='postgres://shoal:…@192.168.122.100:5433/shoal_telemetry?sslmode=disable'
export SHOAL_SERIAL_SSH_HOST=192.168.122.100
export SHOAL_SERIAL_SSH_KEY=$HOME/.ssh/shoal_lab_vm

go test ./internal/deploy/job ./internal/common/redfish ./internal/deploy/jobstore \
  -tags=integration -count=1 -v

go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -serial-target shoal-node-1 \
  -iso-url http://192.168.124.1:8080/shoal-marker.iso
```

See also [phase2-live-image.md](./phase2-live-image.md).

### Ollama models (design §6 / v2.0.4 dual-model contract)

`up.yml` / `compose_stack` pulls configured Ollama models into the `shoal-ollama`
container after the stack is healthy:

| Ansible var | App env | Lab default | Role |
|-------------|---------|-------------|------|
| `shoal_ai_model` | `SHOAL_AI_MODEL` | `llama3.2:3b` | Text / hybrid JSON (required) |
| `shoal_ai_vision_model` | `SHOAL_AI_VISION_MODEL` | `deepseek-ocr` | Photo OCR via `Free OCR.` (optional; empty skips pull + smoke). Prefer over moondream for real serials. |

`smoke.yml` asserts `/api/tags` includes the text model, and the vision model when
`shoal_ai_vision_model` is non-empty. First vision pull can take several minutes.

Re-pull without full stack rebuild (on L1 / lab VM):

```bash
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 \
  'docker exec shoal-ollama ollama pull llama3.2:3b &&
   docker exec shoal-ollama ollama pull deepseek-ocr'
curl -s http://192.168.122.100:11434/api/tags | jq '.models[].name'
```

To skip vision in a constrained lab, set `shoal_ai_vision_model: ""` in
`defaults.yml` (or inventory override) and re-run the compose/ollama portion of
`up.yml`. Photo AC requires a working OCR vision model (lab default
`deepseek-ocr`); moondream is not sufficient for reliable serial extraction.

### Phase 3 discover smoke (from L0 against VM lab)

```bash
export SHOAL_AI_PROVIDER=ollama
export SHOAL_AI_MODEL=llama3.2:3b
export SHOAL_AI_VISION_MODEL=deepseek-ocr
export SHOAL_OLLAMA_URL=http://192.168.122.100:11434
export SHOAL_NETBOX_URL=http://192.168.122.100:8000
export SHOAL_NETBOX_TOKEN=…   # vault / bootstrap token

# Clean dump → deterministic (no LLM)
go run ./cmd/shoal discover ingest \
  -kind redfish_json -file /tmp/clean-system.json \
  -bmc-ip 192.168.122.100

# Spec-deviant / incomplete dump → AI reconcile via Ollama
go run ./cmd/shoal discover ingest \
  -kind redfish_json -file /tmp/messy.json \
  -bmc-ip 10.0.0.5
```

Also: `POST /v1/discover/ingest` when `shoal serve` is started with the same AI/NetBox env.

### Phase 3b: confirm → few-shot learning

Operator-confirmed normalizations append to a durable few-shot store (not NetBox).

**Lab default (Ansible):** `shoal_fewshot_dir` is `/var/lib/shoal/fewshot` on the
service host. `compose_stack` creates the directory and writes
`SHOAL_FEWSHOT_DIR` into `/opt/shoal/infra/.env`. Override or disable with
`-e shoal_fewshot_dir=""` (empty skips the env var and mkdir).

**Workstation `go run` against the lab:** the lab path is on the remote host —
use a local durable dir (outside git), or source a local untracked `.env`:

```bash
export SHOAL_FEWSHOT_DIR=./.shoal/fewshot
mkdir -p "$SHOAL_FEWSHOT_DIR"
```

```bash
# After reviewing an ingest JSON (stdout from discover ingest), confirm it:
# confirm.json shape:
# { "kind":"redfish_json", "input":{...redacted raw...}, "result":{ "asset":{...}, "confidences":[...], "needs_review":false } }
# Confidence source must be "deterministic" or "ai".
go run ./cmd/shoal discover confirm -file /tmp/confirm.json

# Or split files:
go run ./cmd/shoal discover confirm \
  -kind redfish_json \
  -input /tmp/messy.json \
  -result /tmp/ingest-out.json
```

API: `POST /v1/discover/confirm` with the same JSON body. Ingest still works if
`SHOAL_FEWSHOT_DIR` is unset; confirm returns an error until the dir is set.

### Phase 4: Observe status + SEL/sensor poll

Telemetry events/sensors go to Postgres (`SHOAL_TELEMETRY_DATABASE_URL`, lab
`:5433/shoal_telemetry`) — **not** NetBox. **No silent memory fallback:** poll
and `-events` require a working DSN; if the DSN is set but open fails, commands
error out.

```bash
export SHOAL_TELEMETRY_DATABASE_URL="postgres://shoal:…@192.168.122.100:5433/shoal_telemetry?sslmode=disable"
export SHOAL_BMC_USERNAME=… SHOAL_BMC_PASSWORD=…

# Aggregate device status (job phase + last event)
go run ./cmd/shoal observe status -device-id shoal-node-1
go run ./cmd/shoal observe status -device-id shoal-node-1 -events 10

# One-shot Redfish SEL + sensor poll → durable telemetry only
go run ./cmd/shoal observe poll \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001

# Multi-system sushy: pass -system-id when using -bmc-url for power state
go run ./cmd/shoal observe status -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 -system-id <uuid-or-name>

# With serve: GET /v1/devices/{id}/status and /v1/devices/{id}/events
# Background poller runs only when DSN is healthy; seeds jobs with bmc_endpoint;
# interval elevates while a SOL watch is active (15s vs 60s idle).
```

**What “pass” means on lab:**
- Poll exit 0 with `sel_new:0, sensors_written:0` is **valid** on sushy (no logs)
  — it means Redfish open + list succeeded with empty data, **not** that rich SEL
  was ingested. Write/dedup is covered by unit tests with Fake BMC.
- Poll exit non-zero if Redfish fails or any normalize/write fails.
- `ActiveJobID` is only set for `provisioning` jobs (failed jobs expose lifecycle
  + error, not an “active” id).

**Lab fidelity:** rich SEL/sensors need real BMC hardware.

### Phase 5a: NetBox lifecycle on deploy

When `SHOAL_NETBOX_URL` + `SHOAL_NETBOX_TOKEN` are set, Deploy best-effort
syncs NetBox `lifecycle_state`:

| Job event | NetBox state |
|-----------|----------------|
| Start (job row inserted) | `provisioning` |
| Terminal DONE (post-check OK) | `provisioned` |
| Terminal fail/cancel/stall/… | `failed` |

**Custom fields** (created by `bootstrap_netbox` / `netbox_bootstrap`):
`lifecycle_state`, `credential_ref`, `bmc_ip` on `dcim.device`. After adding
them on an existing lab, re-run bootstrap (or create CFs once) and re-ingest
devices so values land in custom fields instead of comments.

NetBox down does **not** block BMC actions (warn log only). Device key is the
job `device_id` (serial preferred after Discover ingest).

**Cross-process cancel/cleanup:** jobs persist `system_id` + `credential_ref`.
Use a **shared** secrets dir so the cancel process can resolve BMC credentials:

```bash
export SHOAL_SECRETS_DIR=./.shoal/secrets   # same path for deploy run and cancel
mkdir -p "$SHOAL_SECRETS_DIR"
# deploy run … then later:
go run ./cmd/shoal deploy cancel -job <id>
```

Prefer cancel via the same `shoal serve` process (`POST /v1/jobs/{id}/cancel`)
when the job was started under serve.

Reliability contract: always cleanup Virtual Media + boot override;
cancel/stall/orphan fail; DONE post-check verifies media ejected.

### Phase 5b: provisioning profiles + approval

Profiles are **not** stored in NetBox. Use a durable directory.

**Lab default (Ansible):** `shoal_profile_dir` is `/var/lib/shoal/profiles` on the
service host. `compose_stack` creates the directory and writes
`SHOAL_PROFILE_DIR` into `/opt/shoal/infra/.env`. Override or disable with
`-e shoal_profile_dir=""` (empty skips the env var and mkdir).

**Workstation `go run` against the lab:** the lab path is on the remote host —
use a local durable dir (outside git), or source a local untracked `.env`:

```bash
export SHOAL_PROFILE_DIR=./.shoal/profiles
mkdir -p "$SHOAL_PROFILE_DIR"
export SHOAL_AI_PROVIDER=ollama SHOAL_AI_MODEL=llama3.2:3b
export SHOAL_OLLAMA_URL=http://192.168.122.100:11434

# AI generate (optional -save writes the store)
go run ./cmd/shoal profile generate \
  -os-family ubuntu -hostname lab-1 -serial SN1 -bmc-ip 10.0.0.5 -save

# Or save a hand-written profile JSON
go run ./cmd/shoal profile save -file /tmp/profile.json

# Approve destructive / needs_approval profiles
go run ./cmd/shoal profile approve -ref lab-1-ubuntu -by matt

# Deploy with a stored profile (spike = no store, Phase 2 path)
go run ./cmd/shoal deploy run -profile-ref lab-1-ubuntu ...
# or one-shot consent without prior approve:
go run ./cmd/shoal deploy run -profile-ref wipe-me -approve-destruct ...
```

`NeedsApproval` or non-empty `destruct_steps` block Start until approved or
`-approve-destruct` is set. AI never auto-executes destruct. Spike / empty
`profile-ref` still works with `SHOAL_PROFILE_DIR` unset.

### Phase 5c: ISO build / publish / resolve

**Lab default (Ansible):** `SHOAL_ISO_PUBLISH_DIR` = `shoal_iso_server_dir`
(`/srv/iso`); `SHOAL_ISO_BASE_URL` = BMC-reachable
`http://{{ shoal_lab_network_gateway }}:8080` (VM nested: `http://192.168.124.1:8080`).
The `marker_iso` role remains the primary producer of `shoal-marker.iso`.

**Go path (Phase 5c):** wrap the same `build-marker-iso.sh` and publish:

```bash
# On L1 (or with publish dir reachable) after sourcing lab env:
export SHOAL_ISO_PUBLISH_DIR=/srv/iso
export SHOAL_ISO_BASE_URL=http://192.168.124.1:8080

# Build marker ISO (+ optional non-secret payload inject)
go run ./cmd/shoal deploy iso build -name shoal-marker.iso -publish
# Or publish a prebuilt file:
go run ./cmd/shoal deploy iso publish -file ./shoal-marker.iso

# Deploy: omit -iso-url when profile.iso_base resolves via SHOAL_ISO_BASE_URL
export SHOAL_PROFILE_DIR=./.shoal/profiles
go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -serial-target shoal-node-1 \
  -profile-ref lab-1-ubuntu \
  # iso_base e.g. "shoal-marker" → http://192.168.124.1:8080/shoal-marker.iso
```

Operator-supplied `-iso-url` still wins over profile resolve. Spike profile always
requires `-iso-url`. Payload inject uses `SHOAL_EMBEDDED_PAYLOAD` / `-payload`
(never secrets).

### Phase 6a: real payload write (install MVP)

Default marker ISO still **simulates** `IMAGE_WRITE`. For a real write of an
embedded payload (bounded blob — not a full Ubuntu autoinstall product):

```bash
# Build write-mode ISO with a payload file (binary-safe)
printf 'golden-rootfs-bytes' > /tmp/payload.bin
export SHOAL_ISO_PUBLISH_DIR=/srv/iso   # on L1
export SHOAL_ISO_BASE_URL=http://192.168.124.1:8080
# Kernel on L1 is often 0600 root — stage a readable copy:
#   sudo cp /boot/vmlinuz-$(uname -r) /tmp/vmlinuz && sudo chmod 644 /tmp/vmlinuz
#   export SHOAL_KERNEL=/tmp/vmlinuz

go run ./cmd/shoal deploy iso build \
  -name shoal-install.iso \
  -install-mode write \
  -install-target /tmp/shoal-install.out \
  -payload-file /tmp/payload.bin \
  -publish

# Or build at Start time:
go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -serial-target shoal-node-1 \
  -build-iso \
  -iso-install-mode write \
  -iso-payload-file /tmp/payload.bin \
  -iso-install-target /tmp/shoal-install.out
```

Write mode emits real `IMAGE_WRITE` percent over SOL. If no block device is
present, the image falls back to a file target (`/tmp/shoal-install.out` or
`-install-target`). Nested sushy disks may be limited — document fidelity gaps.

`SHOAL_ISO_DYNAMIC=true` also builds when `-iso-url` is empty and publish
dir/base URL are set.

### Phase 7a: Ubuntu full OS on nested lab (**complete**)

Phase **6a** writes a bounded `/payload` only. Phase **7a** installs a **real
Ubuntu root** on the nested guest disk over Virtual Media + SOL markers, then
reboots. Job ends **`provisioned`**.

**Preferred path (nested sushy lab):** Ubuntu **cloud image** → customize →
gzip → marker ISO with `payload.gz` on the ISO root → guest
`gunzip|dd` to `/dev/vda`. Live-server **autoinstall remaster** is alternate/stretch
(often fails to boot/progress under nested sushy).

**Prerequisites**

1. Nested guest with a **real disk** (lab nodes: 20G qcow2) and ≥2 GiB RAM.
2. Build on **L1** (needs loop mounts / sudo for cloud prep; kernel modules for marker ISO).
3. Publish dir served at BMC-reachable URL:

```bash
export SHOAL_ISO_PUBLISH_DIR=/srv/iso
export SHOAL_ISO_BASE_URL=http://192.168.124.1:8080
```

**Prepare cloud payload (L1)**

```bash
wget -O /var/tmp/ubuntu-22.04-server-cloudimg-amd64.img \
  https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img
export SHOAL_UBUNTU_CLOUD_IMG=/var/tmp/ubuntu-22.04-server-cloudimg-amd64.img
export SHOAL_AUTOINSTALL_HOSTNAME=lab-node-1
export SHOAL_AUTOINSTALL_USERNAME=ubuntu
export SHOAL_AUTOINSTALL_PASSWORD=shoal-lab   # lab only
sudo -E ./infra/scripts/prepare-ubuntu-cloud-payload.sh /tmp/ubuntu-cloud-payload.raw
# produces /tmp/ubuntu-cloud-payload.raw.gz
```

**Build marker install ISO (payload on ISO root, small initrd)**

```bash
sudo env \
  SHOAL_INSTALL_MODE=autoinstall \
  SHOAL_PAYLOAD_FILE=/tmp/ubuntu-cloud-payload.raw.gz \
  SHOAL_INSTALL_TARGET=/dev/vda \
  SHOAL_INSTALL_REBOOT=1 \
  SHOAL_ISO_NAME=shoal-ubuntu-cloud-v5.iso \
  ./infra/scripts/build-marker-iso.sh /srv/iso/shoal-ubuntu-cloud-v5.iso
```

**Deploy**

```bash
# Use a new ISO basename when content changes (sushy caches by download path).
go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -bmc-user admin -bmc-pass password \
  -serial-target shoal-node-1 \
  -serial-ssh-host 192.168.122.100 \
  -serial-ssh-user lab \
  -serial-ssh-key "$HOME/.ssh/shoal_lab_vm" \
  -iso-url http://192.168.124.1:8080/shoal-ubuntu-cloud-v5.iso \
  -stall-timeout 15m \
  -wait-timeout 25m \
  -wait
# Expect: state=provisioned, phase=DONE
```

**Lab login (cloud prepare defaults):** user `ubuntu`, password `shoal-lab` (lab-only).

**Alternate — live-server autoinstall remaster**

```bash
export SHOAL_UBUNTU_ISO=/path/to/ubuntu-22.04.5-live-server-amd64.iso
go run ./cmd/shoal deploy iso build \
  -name shoal-ubuntu-autoinstall.iso \
  -install-mode autoinstall \
  -ubuntu-iso "$SHOAL_UBUNTU_ISO" \
  -hostname lab-node-1 \
  -publish
```

**Fidelity:** sushy-tools emulates BMC only; write runs on the nested libvirt disk.
Cloud image-write typically finishes in a few minutes once media is attached;
live autoinstall (if used) can take 20–60+ minutes on nested CPU.

### Multi-stage M2: prep wipe then OS install

One job can run a **prep** stage (wipe disk + `PREP_*` SOL markers) then advance to
**os_install** (cloud image-write ISO). Requires M2 binary and two media URLs.

**Build prep ISO (L1):**

```bash
sudo env SHOAL_INSTALL_MODE=prep SHOAL_INSTALL_TARGET=/dev/vda \
  SHOAL_ISO_NAME=shoal-prep.iso \
  ./infra/scripts/build-marker-iso.sh /srv/iso/shoal-prep.iso
```

**Deploy wipe + install** (operator host; approve wipe):

```bash
go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -bmc-user admin -bmc-pass password \
  -serial-target shoal-node-1 \
  -serial-ssh-host 192.168.122.100 \
  -serial-ssh-user lab \
  -serial-ssh-key "$HOME/.ssh/shoal_lab_vm" \
  -prep wipe_only \
  -approve-destruct \
  -prep-iso-url http://192.168.124.1:8080/shoal-prep.iso \
  -iso-url http://192.168.124.1:8080/shoal-ubuntu-m1-test.iso \
  -install-strategy image_write \
  -stall-timeout 15m -wait-timeout 30m -wait
```

`PREP_DONE` does **not** complete the job; only the final install `DONE` yields
`provisioned`. Status JSON shows `current_stage` moving from `prep` → `os_install`.

### Multi-stage M3: offline NoCloud seed (second_media)

Guest must **not** fetch user-data over the network. M3 delivers a tiny CIDATA ISO
on a **second Virtual Media** slot when the BMC has ≥2 CD devices.

**Build seed ISO (any host with xorriso/genisoimage):**

```bash
SHOAL_SEED_HOSTNAME=lab-node-1 \
  ./infra/scripts/build-nocloud-seed-iso.sh /srv/iso/shoal-cidata.iso
# BMC-reachable URL (nested lab):
#   http://192.168.124.1:8080/shoal-cidata.iso
```

**Deploy with dual media** (requires dual-CD BMC or `redfish.NewFakeDualCD` in unit tests).
Nested sushy-tools typically has **one** CD — second_media will fail with an actionable
error; for image_write offline seed, bake cloud-init into the payload with
`prepare-ubuntu-cloud-payload.sh` instead (`seed_delivery=none`).

```bash
go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -bmc-user admin -bmc-pass password \
  -serial-target shoal-node-1 \
  -iso-url http://192.168.124.1:8080/ubuntu-live.iso \
  -seed-delivery second_media \
  -seed-iso-url http://192.168.124.1:8080/shoal-cidata.iso \
  -os-family ubuntu \
  -wait
```

| Mode | Notes |
|------|--------|
| `none` (default) | No runtime seed (7a prepare-time seed is fine) |
| `second_media` | Install ISO on CD1 + seed ISO on CD2; needs ≥2 CD slots |
| `auto` | Prefer second_media when ≥2 CDs + `seed_iso_url`; else error if image_write (no config_drive) |
| `config_drive` | **Forbidden** with `image_write` (full-disk dd wipes partition); write path not implemented yet |

### Phase 6b: graphics failure-screen OCR

SOL remains the primary progress channel. Graphics OCR is **diagnostic only**.

```bash
export SHOAL_AI_PROVIDER=ollama
export SHOAL_AI_VISION_MODEL=deepseek-ocr   # or other vision model
export SHOAL_OLLAMA_URL=http://192.168.122.100:11434
export SHOAL_TELEMETRY_DATABASE_URL='postgres://…@192.168.122.100:5433/shoal_telemetry?sslmode=disable'

# Lab / fixtures: operator or fixture PNG/JPEG
go run ./cmd/shoal observe ocr \
  -device-id shoal-node-1 \
  -file ./testdata/ocr/failure_screen.png

# Real hardware: OEM screenshot capture (Dell / Supermicro first)
# Emits detailed debug steps (URL, status, body preview) on failure — no secrets.
go run ./cmd/shoal observe ocr \
  -device-id pe-r640 \
  -bmc-url https://idrac.example \
  -bmc-user root -bmc-pass '…' \
  -screenshot-kind current   # or last_crash
```

sushy-tools has no screenshot API — expect **unsupported** with a multi-step
debug trace (system pick, manufacturer, managers listed) and use `-file`.
Events with `event_type=graphics_ocr` land in telemetry Postgres when
`-persist` (default) and DSN is set.

**Smoke (unit + lab):**

```bash
go test ./internal/observe/ -run OCR -count=1
go test ./internal/observe/ -tags=integration -count=1 -timeout 10m \
  -run 'TestLabOCR'
# CLI file path (needs lab Ollama vision):
go run ./cmd/shoal observe ocr -device-id lab-ocr-smoke \
  -file testdata/ocr/failure_screen.png
```

## 2) Fast stop (teardown / clean slate)
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml
```
This stops the stack, then removes the VM, overlay disk, cached base image, L0 management network, and generated cloud-init artifacts. Destroying the VM also takes down everything that lived inside it (Docker compose stack, sushy-tools, nested BMC nodes, `shoal-lab-net`). The SSH keypair is kept by default; to remove it too, add `-e shoal_remove_ssh_key=true`.

## 2b) Rebuild from scratch
Tear down to a clean slate, then bring everything back up:
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
```

## 3) Daily quick commands
### Check VM exists/running (L0 host)
```bash
virsh -c qemu:///system list --all
virsh -c qemu:///system dominfo shoal-lab
```

### SSH into lab VM (L1)
```bash
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100
```

### Check service containers in lab VM
```bash
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker ps"
```

### Re-run smoke only
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
```

### Re-bootstrap NetBox only
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/bootstrap_netbox.yml
```

## 4) Direct host mode
Run the lab stack directly on the L0 host (no nested VM):
```bash
ansible-playbook -i infra/ansible/inventory/lab.yml infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab.yml infra/ansible/playbooks/smoke.yml
# teardown:
ansible-playbook -i infra/ansible/inventory/lab.yml infra/ansible/playbooks/down.yml
```

## 5) If bring-up fails
### A) Nested virtualization checks (run on L0 host)
```bash
ls -l /dev/kvm
grep -E '(vmx|svm)' /proc/cpuinfo | head
cat /sys/module/kvm_intel/parameters/nested 2>/dev/null \
  || cat /sys/module/kvm_amd/parameters/nested 2>/dev/null
virsh -c qemu:///system version
```
If missing, enable nested virtualization and reboot/reload KVM modules.

**L0 profiles (Phase 6c):** classic Linux (ufw-aware) and **Fedora secureblue**
(firewalld + modular libvirt). **macOS is not L0** — see
[operator-macos.md](./operator-macos.md) and
[lab-setup-checklist.md](./lab-setup-checklist.md) (L0 secureblue section).

### B) VM boots but SSH fails
```bash
virsh -c qemu:///system console shoal-lab
```
Check cloud-init/network/SSH key issues from the console logs.

### C) Ansible connect / inventory issues
```bash
ansible-inventory -i infra/ansible/inventory/lab-vm.yml --list
ansible-inventory -i infra/ansible/inventory/lab-vm.yml --graph
```
Then test SSH manually with the same key/user/host.

### D) Services are up but smoke fails
Inspect logs inside the VM:
```bash
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker logs shoal-netbox --tail 200"
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker logs shoal-ollama --tail 200"
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker logs shoal-sushy-tools --tail 200"
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "LIBVIRT_DEFAULT_URI=qemu:///system virsh ttyconsole shoal-node-1"
```

## 6) Recovery sequence (if state is inconsistent)
Tear down to a clean slate, bring the lab back up, then re-run smoke checks:
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
```

## 7) Individual phase playbook execution
`up.yml` and `down.yml` import these phase playbooks in order; you can also run any of them on its own:
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/vm_provision.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/preflight.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/lab_up.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/bootstrap_netbox.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/lab_down.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/vm_destroy.yml
```

## 8) Endpoints (defaults)
From the L0 host / management network (`shoal_lab_vm_host`, default `192.168.122.100`):
- NetBox: `http://192.168.122.100:8000`
- Ollama: `http://192.168.122.100:11434`
- ISO HTTP server: `http://192.168.122.100:8080`
- Redfish root (sushy-tools): `http://192.168.122.100:8001/redfish/v1`
- NetBox API example (token auth): `curl -H "Authorization: Token <shoal_netbox_api_token>" http://192.168.122.100:8000/api/` (unauthenticated `/api/` requests return `403`)

From a virtual BMC node (lab network `192.168.124.0/24`), the same services are reachable via the lab gateway `192.168.124.1` (e.g. ISO at `http://192.168.124.1:8080`).
