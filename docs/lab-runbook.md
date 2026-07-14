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
syncs NetBox `lifecycle_state` (custom field or comments fallback):

| Job event | NetBox state |
|-----------|----------------|
| Start (job row inserted) | `provisioning` |
| Terminal DONE (post-check OK) | `provisioned` |
| Terminal fail/cancel/stall/… | `failed` |

NetBox down does **not** block BMC actions (warn log only). Device key is the
job `device_id` (serial preferred after Discover ingest).

Reliability contract (unchanged + polish): always cleanup Virtual Media + boot
override; cancel/stall/orphan fail; DONE post-check verifies media ejected.

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
```
If missing, enable nested virtualization and reboot/reload KVM modules.

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
