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
| `shoal_ai_vision_model` | `SHOAL_AI_VISION_MODEL` | `moondream` | Photo / `CompleteVision` (optional; empty skips pull + smoke) |

`smoke.yml` asserts `/api/tags` includes the text model, and the vision model when
`shoal_ai_vision_model` is non-empty. First vision pull can take several minutes.

Re-pull without full stack rebuild (on L1 / lab VM):

```bash
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 \
  'docker exec shoal-ollama ollama pull llama3.2:3b &&
   docker exec shoal-ollama ollama pull moondream'
curl -s http://192.168.122.100:11434/api/tags | jq '.models[].name'
```

To skip vision in a constrained lab, set `shoal_ai_vision_model: ""` in
`defaults.yml` (or inventory override) and re-run the compose/ollama portion of
`up.yml`.

### Phase 3 discover smoke (from L0 against VM lab)

```bash
export SHOAL_AI_PROVIDER=ollama
export SHOAL_AI_MODEL=llama3.2:3b
export SHOAL_AI_VISION_MODEL=moondream
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
