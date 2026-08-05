# Shoal Lab Setup Checklist (First-Time)
First-time setup of the Shoal lab. All steps use Ansible directly; there is no Makefile.

**Debian laptop/desktop as L0:** step-by-step guide in
[lab-setup-debian.md](./lab-setup-debian.md) (packages, nested KVM, vault, `up.yml`).

## 1) Host prerequisites (L0)

L0 is the **Linux hypervisor** that runs the lab VM. **macOS is not a valid L0**
(operator-only — see [operator-macos.md](./operator-macos.md)). Classic Linux and
**Fedora secureblue** are supported for VM-hosted mode.

### All Linux L0
- [ ] `libvirt` / `virsh` usable with **`qemu:///system`** (system session)
- [ ] `qemu-img` installed; QEMU/KVM stack present
- [ ] Seed ISO tool: `genisoimage` **or** `mkisofs` **or** `xorriso`
- [ ] `ansible-playbook` installed (on L0 or a controller that SSHs to L0)
- [ ] Python deps for `community.libvirt` on the controller/L0: `python3-lxml`
      and `python3-libvirt` (Debian/Ubuntu: `apt install python3-lxml python3-libvirt`)
- [ ] **Go 1.22+** on the controller (`go version`): `compose_stack` builds the
      Shoal binary with `go build` on localhost when `shoal_compose_app` is true
      (default). Debian/Ubuntu: `apt install golang-go` if ≥ 1.22, else install
      from [go.dev/dl](https://go.dev/dl/)
- [ ] `ssh` / `scp` installed
- [ ] `/dev/kvm` exists on host
- [ ] CPU virtualization flags present (`vmx` or `svm`)
- [ ] **Nested virtualization** enabled on L0 (required for L2 sushy nodes)
- [ ] After `up.yml`: nested `shoal-node-*` domains include **empty CD-ROM** for
      sushy Virtual Media (`virsh dumpxml shoal-node-1 | grep cdrom`; smoke.yml checks this)

Quick checks:
```bash
ls -l /dev/kvm
grep -E '(vmx|svm)' /proc/cpuinfo | head
cat /sys/module/kvm_intel/parameters/nested 2>/dev/null \
  || cat /sys/module/kvm_amd/parameters/nested 2>/dev/null
virsh -c qemu:///system version
```

> The L0 management network (`shoal-mgmt-net`, `192.168.122.0/24`) is created
> automatically by `up.yml`. You do **not** need to pre-create libvirt's
> `default` network.

### L0 on Fedora secureblue (optional profile)

secureblue ships libvirt/QEMU/virt-manager on desktop images. Before `up.yml`:

- [ ] Desktop secureblue image (virt stack preinstalled)
- [ ] Enable **system** libvirt daemons:
  ```bash
  ujust set-libvirt-daemons
  ```
  Prefer the QEMU/KVM **system** session (bridge/NAT for the lab network). Do
  **not** rely on the user session alone. Avoid adding your user to the
  `libvirt` group for passwordless root-equivalent access; authenticate as needed.
- [ ] Nested KVM enabled (same `modprobe.d` steps as classic Linux if nested is off)
- [ ] Seed ISO tool available (`brew install xorriso` / `cdrtools`, or layer RPM)
- [ ] Ansible available (`brew install ansible` or run playbooks from another host)
- [ ] Optional: expect **SMT** may be disabled (secureblue mitigations) → slower nested lab / Ollama

The `lab_vm` role detects secureblue/Atomic, tries modular libvirt sockets, opens
**firewalld** for the management bridge (and still supports **ufw** on classic hosts).

Direct-host lab mode (`lab.yml`) on secureblue is **not** a supported target.

## 2) Install Ansible collections
- [ ] Run once:
  ```bash
  ansible-galaxy collection install -r infra/ansible/requirements.yml
  ```

## 3) Configure variables and secrets
- [ ] Copy the secrets template:
  ```bash
  cp infra/ansible/inventory/group_vars/all/vault.yml.example \
     infra/ansible/inventory/group_vars/all/vault.yml
  ```
- [ ] Fill in `infra/ansible/inventory/group_vars/all/vault.yml`:
  - [ ] `shoal_netbox_api_token`
  - [ ] `shoal_netbox_postgres_password`
  - [ ] `shoal_redis_password`
  - [ ] `shoal_bmc_username` / `shoal_bmc_password`
  - [ ] `shoal_telemetry_db_password`
- [ ] (Optional) encrypt the vault:
  ```bash
  ansible-vault encrypt infra/ansible/inventory/group_vars/all/vault.yml
  ```
- [ ] Review non-secret defaults in `infra/ansible/inventory/group_vars/all/defaults.yml`:
  - [ ] `shoal_lab_vm_name`, `shoal_lab_vm_host`, `shoal_lab_vm_user`
  - [ ] `shoal_mgmt_network*` (L0 mgmt, default `192.168.122.0/24`) — lab VM attaches here
  - [ ] `shoal_lab_network*` (BMC nodes, default `192.168.124.0/24`) — created inside the VM
  - [ ] `shoal_lab_vm_host` is inside `shoal_mgmt_network`, BMC node IPs are inside `shoal_lab_network`
- [ ] If you changed the VM IP, update `ansible_host` in `infra/ansible/inventory/lab-vm.yml` to match.

## 4) Bring up VM-hosted lab
- [ ] Run:
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
  ```
  (add `--ask-vault-pass` if your vault is encrypted)
- [ ] Confirm VM exists/running:
  ```bash
  virsh -c qemu:///system list --all
  virsh -c qemu:///system dominfo shoal-lab
  ```
- [ ] Confirm VM SSH access:
  ```bash
  ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100
  ```

## 5) Validate lab health
- [ ] Run smoke tests:
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
  ```
- [ ] If smoke fails, inspect service status/logs in the VM:
  ```bash
  ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker ps"
  ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker logs shoal-netbox --tail 200"
  ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker logs shoal-ollama --tail 200"
  ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "docker logs shoal-sushy-tools --tail 200"
  ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100 "LIBVIRT_DEFAULT_URI=qemu:///system virsh ttyconsole shoal-node-1"
  ```

## 6) Re-bootstrap NetBox (if needed)
- [ ] Run:
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/bootstrap_netbox.yml
  ```

## 7) Ready-to-start criteria
- [ ] `up.yml` completes without errors
- [ ] `smoke.yml` passes
- [ ] NetBox endpoint responds: `http://192.168.122.100:8000`
- [ ] NetBox API is reachable with token auth (unauthenticated `/api/` requests return `403` by design)
- [ ] Redfish root responds: `http://192.168.122.100:8001/redfish/v1`
- [ ] You can SSH into the lab VM using the configured key

## 8) Shutdown / clean slate
`down.yml` stops the stack, then removes the VM, overlay disk, cached base image, L0 management network, and generated cloud-init artifacts. Destroying the VM also tears down everything that lived inside it (Docker compose stack, sushy-tools, nested BMC nodes, `shoal-lab-net`). The SSH keypair is kept by default.
- [ ] Stop and remove the VM-hosted lab:
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml
  ```
  (To also discard the SSH keypair, add `-e shoal_remove_ssh_key=true`.)
- [ ] Rebuild from scratch (teardown + bring up + smoke):
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml
  ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml
  ```
- [ ] Direct-host teardown:
  ```bash
  ansible-playbook -i infra/ansible/inventory/lab.yml     infra/ansible/playbooks/down.yml
  ```
