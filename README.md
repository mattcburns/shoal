# Shoal

> **Experimental / not production-ready.**  
> This project is **highly experimental**. It has been developed and exercised primarily
> against a **nested lab** (sushy-tools, libvirt, local Ollama). It is **largely untested
> on real hardware** and should **not** be relied on to manage production fleets without
> your own validation. Expect breaking changes, incomplete features, and lab-only paths.
> Use at your own risk.

## What is Shoal

Shoal is a BMC-centric bare-metal lifecycle tool, one Go binary with three
phases: **Discover** normalizes asset data from Redfish/CSV/photos,
**Observe** tracks device health and job progress via Redfish polling and
serial (SOL) markers, and **Deploy** drives provisioning over Redfish
Virtual Media + SOL. NetBox stays the identity/lifecycle-of-record; Shoal
owns time-series telemetry (events, sensors, job logs) in Postgres and
exposes it over a small `net/http` API (`:8088`) and a `flag`-based CLI —
see [`AGENTS.md`](./AGENTS.md) §3.2–3.3 for the command/env reference.

Architecture, data models, and the phased implementation plan are the
[`AGENTS.md`](./AGENTS.md) working conventions and
[`SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md`](./SHOAL_COMPREHENSIVE_DESIGN_AND_IMPLEMENTATION_PLAN.md)
design doc — read those before making architectural changes; this README
covers running the app and the lab.

### Running the app
```bash
go run ./cmd/shoal serve -addr "${SHOAL_HTTP_ADDR:-:8088}"
```
CLI subcommands: `deploy` (run/status/cancel/iso), `discover`
(ingest/confirm), `observe` (status/poll/ocr), `profile`
(generate/save/show/list/approve). Quality gate before any change is
considered done:
```bash
gofmt -w . && go vet ./... && staticcheck ./... && go test ./...
```

### NetBox plugin
[`extras/netbox-plugin-shoal/`](extras/netbox-plugin-shoal/README.md) is a
NetBox plugin (Python/Django — the one place in this repo Go isn't used;
see the "Stack" note in `AGENTS.md`) that adds read-only **Shoal Status**,
**Events**, **Jobs**, and **Sensors** tabs to the NetBox device page, reading
the API above server-side. It's baked into the lab's NetBox image by default
(`shoal_netbox_plugin: true`); see
[`docs/netbox-telemetry-ui-design.md`](docs/netbox-telemetry-ui-design.md)
for the design and its own README for plugin development.

---

This repository also contains the **lab automation** used to develop and
exercise Shoal, for a **VM-hosted lab mode** (nested virtualization). The
lab is driven entirely by **Ansible** — there is no Makefile.

The lab topology is:

- **L0**: Linux hypervisor (libvirt/KVM) — classic distros or **Fedora secureblue**; **not macOS**
- **L1**: dedicated lab VM (runs Ansible target host, Docker, libvirt, sushy-tools)
- **L2**: virtual Redfish/BMC nodes (emulated by libvirt + exposed via sushy-tools)

**macOS operators** use a prebuilt/darwin binary (or `go build`) against a remote
Linux lab — see [docs/operator-macos.md](docs/operator-macos.md). Multi-platform
release builds: `./scripts/build-release.sh` (see Phase 6c).

## What's in this repo
- `cmd/shoal`, `internal/` - the Shoal Go application (composition root, HTTP API, CLI, Discover/Observe/Deploy, Core AI, common libs) — see `AGENTS.md` §2 for the full layout
- `extras/netbox-plugin-shoal/` - NetBox plugin (Python/Django) adding Shoal device tabs to NetBox; see its own README
- `ansible.cfg` - project-local Ansible config
- `infra/ansible/requirements.yml` - required Ansible collections
- `infra/ansible/inventory/` - file inventories (`lab-vm.yml`, `lab.yml`) and `group_vars/` (YAML config + vault secrets)
- `infra/ansible/playbooks/` - lab lifecycle playbooks. Primary entrypoints: `up.yml` (set up services) and `down.yml` (tear down to a clean slate), plus `smoke.yml`. These import the phase playbooks (`vm_provision.yml`, `preflight.yml`, `lab_up.yml`, `bootstrap_netbox.yml`, `lab_down.yml`, `vm_destroy.yml`), which can also be run individually.
- `infra/ansible/roles/` - `lab_vm` (VM + L0 management network), plus `host_prereqs`, `libvirt_lab`, `sushy_tools`, `compose_stack`, `netbox_bootstrap`
- `infra/ansible/roles/compose_stack/templates/docker-compose.lab.yml.j2` - Ansible template for the lab service stack (rendered on the target host during `lab_up.yml`)

## Lab docs
- [Lab Runbook (Quick Ops)](docs/lab-runbook.md)
- [Lab Setup Checklist (First-Time)](docs/lab-setup-checklist.md)
- [Lab on Debian (VM-hosted L0)](docs/lab-setup-debian.md)
- [Operator host: macOS](docs/operator-macos.md)
- [Phase 6c plan (packaging + L0 hosts)](docs/phase-6c-plan.md)
- [Phase 6d plan (Compose app + auth + metrics)](docs/phase-6d-plan.md)
- [Phase 7 plan (full OS autoinstall)](docs/phase-7-plan.md)
- [Multi-stage provisioning design (M1–M6 implemented)](docs/multi-stage-provisioning-design.md)
- [NetBox telemetry UI design (backend API + plugin N1–N6)](docs/netbox-telemetry-ui-design.md)
- [Real-hardware SOL runbook](docs/real-hardware-sol-runbook.md)

## Prerequisites (L0 host)
Install and verify:

- `libvirt` / `virsh`
- `qemu-system-x86`, `qemu-img`, `genisoimage`
- `ansible-playbook`
- `ssh`, `scp`

Also ensure nested virtualization is enabled and `/dev/kvm` is present on the host.

## Initial setup (one-time)
1. Install Ansible collections:
   ```bash
   ansible-galaxy collection install -r infra/ansible/requirements.yml
   ```
2. Configure secrets:
   ```bash
   cp infra/ansible/inventory/group_vars/all/vault.yml.example \
      infra/ansible/inventory/group_vars/all/vault.yml
   # edit vault.yml, then optionally encrypt it:
   ansible-vault encrypt infra/ansible/inventory/group_vars/all/vault.yml
   ```
3. Review non-secret defaults in `infra/ansible/inventory/group_vars/all/defaults.yml` (VM name/IP, networks, ports) and mode overrides in `vm_mode.yml` / `direct_mode.yml`.

If your vault is encrypted, add `--ask-vault-pass` (or `--vault-password-file <file>`) to every command below.

## VM-hosted lab lifecycle
### Bring up lab
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
```
This runs:
1. VM provisioning + L0 management network (`vm_provision.yml`, via the `lab_vm` role)
2. Ansible preflight checks (`preflight.yml`)
3. Lab deployment (`lab_up.yml`)
4. NetBox bootstrap (`bootstrap_netbox.yml`)

### Run smoke tests
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
```

### Tear down lab (clean slate)
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml
```
This stops the stack, then removes the VM, overlay disk, cached base image, L0 management network, and generated cloud-init artifacts. Destroying the VM also tears down everything that lived inside it (Docker compose stack, sushy-tools, nested BMC nodes, `shoal-lab-net`). The SSH keypair is kept by default; to remove it too, add `-e shoal_remove_ssh_key=true`.

To rebuild from scratch, run `down.yml` then `up.yml`.

## Direct-host lab lifecycle (optional)
Run the lab stack directly on the L0 host (no nested VM) — the same two playbooks, just a different inventory:
```bash
ansible-playbook -i infra/ansible/inventory/lab.yml infra/ansible/playbooks/up.yml
ansible-playbook -i infra/ansible/inventory/lab.yml infra/ansible/playbooks/smoke.yml
# teardown:
ansible-playbook -i infra/ansible/inventory/lab.yml infra/ansible/playbooks/down.yml
```

## Useful operational commands
### Re-bootstrap NetBox only
```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/bootstrap_netbox.yml
```

### Run individual phase playbooks directly
`up.yml` / `down.yml` import these; run one alone for debugging:
```bash
# VM-hosted mode
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/vm_provision.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/preflight.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/lab_up.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/bootstrap_netbox.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/lab_down.yml
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/vm_destroy.yml
```

### Inspect VM state from L0
```bash
virsh -c qemu:///system list --all
virsh -c qemu:///system dominfo shoal-lab
virsh -c qemu:///system console shoal-lab
```

### SSH into the lab VM
```bash
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100
```

## Service endpoints (default values)
VM-hosted mode (lab VM IP, default `192.168.122.100`):
- Shoal app: `http://192.168.122.100:8088` (`/healthz`, `/v1/*`; open by default in the lab — see `SHOAL_API_TOKEN` in `AGENTS.md` §3.3)
- NetBox: `http://192.168.122.100:8000` (open a `shoal-node-*` device page for the Shoal Status / Events / Jobs / Sensors tabs; enabled by default via `shoal_netbox_plugin`)
- Ollama: `http://192.168.122.100:11434`
- ISO HTTP server: `http://192.168.122.100:8080`
- sushy-tools Redfish root: `http://192.168.122.100:8001/redfish/v1`
- NetBox API endpoints (`/api/`) require token auth; unauthenticated requests return `403`

From a virtual BMC node (lab network `192.168.124.0/24`), the same services are reachable via the lab gateway `192.168.124.1` (e.g. ISO at `http://192.168.124.1:8080`).

Direct-host mode:
- Shoal app: `http://127.0.0.1:8088`
- NetBox: `http://127.0.0.1:8000`
- Ollama: `http://127.0.0.1:11434`
- ISO HTTP server: `http://127.0.0.1:8080`
- sushy-tools Redfish root: `http://127.0.0.1:8001/redfish/v1`

## Troubleshooting
### 1) Preflight fails on nested virtualization
Symptoms: `/dev/kvm` missing or no `vmx/svm` CPU flags.

Actions:
- Enable nested virtualization on the L0 host (`kvm-intel` or `kvm-amd` nested mode).
- Ensure the VM uses host CPU passthrough.
- Reboot/reload KVM modules and rerun `up.yml`.

### 2) Cannot SSH into lab VM
Actions:
- Verify the SSH key at `~/.ssh/shoal_lab_vm` and the VM IP in `group_vars/all/defaults.yml` (`shoal_lab_vm_host`).
- Check VM status with `virsh -c qemu:///system list --all`.
- Use `virsh -c qemu:///system console shoal-lab` to inspect boot/cloud-init logs.

### 3) Ansible cannot connect to target
Actions:
- Validate the generated SSH key exists and is readable.
- Confirm the inventory resolves the expected host/user:
  ```bash
  ansible-inventory -i infra/ansible/inventory/lab-vm.yml --list
  ansible-inventory -i infra/ansible/inventory/lab-vm.yml --graph
  ```
- Test SSH manually using the same key/user/host.

### 4) Smoke test failures
Actions:
- Verify all containers are running in the lab VM (`docker ps`).
- Check service logs (`docker logs shoal-netbox`, `docker logs shoal-ollama`, etc.).
- Verify serial console lookup inside the lab VM with `LIBVIRT_DEFAULT_URI=qemu:///system virsh ttyconsole shoal-node-1`.
- Re-run bootstrap if API/token data is missing:
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/bootstrap_netbox.yml
  ```

## Current implementation note
Both direct-host and VM-hosted inventories are present. VM-hosted mode remains the recommended default for isolation.

## License

This project is licensed under the **GNU Affero General Public License v3.0**
(AGPL-3.0). See [LICENSE](./LICENSE) for the full text.

Third-party Go libraries linked into the Shoal binary are listed in
[NOTICE](./NOTICE); full license texts are in
[docs/third-party-licenses.md](./docs/third-party-licenses.md). Binary and
source distributions should include `LICENSE`, `NOTICE`, and that notice file.
