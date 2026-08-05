# Shoal lab on Debian (VM-hosted L0)

How to bring up the **VM-hosted** lab when your machine is a **Debian** laptop
or desktop acting as **L0** (hypervisor). Written against **Debian 13 (trixie)**;
the same steps apply to recent Debian 12+ and Ubuntu as Debian-family hosts.

For the full first-time checklist and day-to-day ops, see also:

- [Lab Setup Checklist](./lab-setup-checklist.md)
- [Lab Runbook](./lab-runbook.md)
- [Operator host: macOS](./operator-macos.md) (macOS is **not** a valid L0)

---

## What you are building

You do **not** install NetBox, sushy, Ollama, and nested BMC nodes on the Debian
host OS. The laptop is the **hypervisor (L0)**. Ansible creates an **L1 lab VM**
and runs the stack inside it.

```text
Debian laptop (L0)
  libvirt: shoal-mgmt-net  192.168.122.0/24  (bridge shoalbr0)
  └── VM shoal-lab @ 192.168.122.100  (L1)
        Docker: NetBox, Postgres, Redis, Ollama, ISO nginx, Shoal app…
        libvirt inside L1: shoal-lab-net  192.168.124.0/24
        └── shoal-node-*  (L2 “BMCs” via sushy-tools)
```

Inventory: `infra/ansible/inventory/lab-vm.yml`

| Group | Role |
|-------|------|
| `localhost` | L0 — connection local |
| `shoal-lab-vm` (`lab@192.168.122.100`) | L1 — SSH after first provision |

Use **`lab-vm.yml`**, not `lab.yml`. Direct-host mode (`lab.yml`) puts the stack
on the bare metal OS and is a different path.

---

## What you need on the laptop

| Requirement | Why |
|-------------|-----|
| KVM (`/dev/kvm`) + **nested** virtualization | L2 BMC VMs run *inside* the lab VM |
| libvirt **system** session (`qemu:///system`) | Creates mgmt network + lab VM |
| `qemu-img` + seed ISO tool (`genisoimage` / `xorriso` / `mkisofs`) | Disk images + cloud-init seed |
| Ansible + Galaxy collections | All lifecycle is playbooks (no Makefile) |
| **Go 1.22+** on the controller (usually this laptop) | `compose_stack` builds a CGO-free `linux/amd64` binary on *localhost*, then copies it into the lab image (`shoal_compose_app`, default on) |
| Working **sudo** | Playbooks `become` for bridges and libvirt |
| Enough RAM, CPU, and disk for the lab VM | See [Resource defaults](#resource-defaults) |

You do **not** need libvirt’s stock `default` network. `up.yml` creates
`shoal-mgmt-net` (`192.168.122.0/24`) automatically.

### Resource defaults

From `infra/ansible/inventory/group_vars/all/defaults.yml`:

| Variable | Default | Notes |
|----------|---------|--------|
| `shoal_lab_vm_memory` | `16384` MiB | 16 GiB for the L1 VM alone |
| `shoal_lab_vm_cpus` | `8` | |
| `shoal_lab_vm_disk_size` | `100G` | Plus base image cache and Docker/Ollama layers |

Preflight on the lab target expects roughly ≥ 8 GiB RAM and tens of GiB free disk.
Ollama’s vision model (`deepseek-ocr`, ~6.7 GB) is the large optional pull.

**Laptop tip:** On a 16 GiB machine, leave headroom for the host desktop. Lower
memory/CPUs in `defaults.yml` *before* the first `up.yml` if needed, for example:

```yaml
shoal_lab_vm_memory: 8192
shoal_lab_vm_cpus: 4
```

To skip vision model pull on a constrained lab, set `shoal_ai_vision_model: ""`
(photo OCR AC needs a real vision model when you re-enable it).

---

## Step-by-step

### 1. Host packages (Debian 13 L0)

```bash
sudo apt update
sudo apt install -y \
  qemu-system-x86 qemu-utils \
  libvirt-daemon-system libvirt-clients \
  bridge-utils virtinst \
  genisoimage \
  ansible \
  curl wget git openssh-client \
  python3 python3-venv \
  python3-lxml python3-libvirt \
  golang-go
```

`python3-lxml` and `python3-libvirt` are required by Ansible’s
`community.libvirt` modules (`virt_net` / `virt`) when `up.yml` defines the
management network and the lab VM. Without them you get
`The lxml module is not importable` (or a similar libvirt import error).

**Go 1.22+** must be on `PATH` for the user that runs `ansible-playbook`. The
`compose_stack` role runs `go build` **on the controller** (`delegate_to:
localhost`), not inside the lab VM. Check:

```bash
go version   # need go1.22 or newer
```

On Debian 13 (trixie), `golang-go` from apt is usually new enough. On older
Debian/Ubuntu where apt’s Go is below 1.22, install from
[go.dev/dl](https://go.dev/dl/) instead and ensure the Go install `bin`
directory is on `PATH`.

To bring the stack up **without** building/shipping the Shoal app container,
set `shoal_compose_app: false` in group vars (you still need Go later for
`go run` / release builds).

Enable libvirt and optionally add your user to the libvirt group:

```bash
sudo systemctl enable --now libvirtd
sudo usermod -aG libvirt,kvm "$USER"
# Log out and back in (or: newgrp libvirt) so group membership applies
```

### 2. Nested virtualization (required)

```bash
# CPU virtualization flags present?
grep -E '(vmx|svm)' /proc/cpuinfo | head

# Nested currently enabled? Expect Y or 1
cat /sys/module/kvm_intel/parameters/nested 2>/dev/null \
  || cat /sys/module/kvm_amd/parameters/nested 2>/dev/null
```

If nested is not `Y` / `1`:

```bash
# Intel
echo 'options kvm_intel nested=1' | sudo tee /etc/modprobe.d/kvm-intel.conf

# AMD instead:
# echo 'options kvm_amd nested=1' | sudo tee /etc/modprobe.d/kvm-amd.conf

sudo modprobe -r kvm_intel kvm_amd 2>/dev/null || true
sudo modprobe -a kvm_intel || sudo modprobe -a kvm_amd
# If unload fails because VMs are running, reboot after writing the conf file
```

Quick health checks:

```bash
ls -l /dev/kvm
virsh -c qemu:///system version
```

`up.yml` **fails fast** if `/dev/kvm` is missing or nested is off, with an
actionable message from the `lab_vm` role.

### 3. Repo and Ansible collections

From the Shoal checkout:

```bash
cd /path/to/shoal
ansible-galaxy collection install -r infra/ansible/requirements.yml
```

### 4. Secrets and config

```bash
cp infra/ansible/inventory/group_vars/all/vault.yml.example \
   infra/ansible/inventory/group_vars/all/vault.yml
# edit vault.yml — example values are fine for a pure nested lab
```

Minimum fields (see `vault.yml.example`):

- `shoal_netbox_api_token`, `shoal_netbox_postgres_password`
- `shoal_redis_password`
- `shoal_bmc_username` / `shoal_bmc_password` (sushy; examples often `admin` / `password`)
- `shoal_telemetry_db_password`

Optional encrypt:

```bash
ansible-vault encrypt infra/ansible/inventory/group_vars/all/vault.yml
# then pass --ask-vault-pass on every playbook run
```

Review non-secrets if you care about name, IP, or size:

- `infra/ansible/inventory/group_vars/all/defaults.yml`
- Mode overrides: `infra/ansible/inventory/group_vars/vm_mode.yml`
- If you change the lab VM IP, update `ansible_host` in
  `infra/ansible/inventory/lab-vm.yml` (default `192.168.122.100`)

### 5. Bring the lab up

```bash
# From repo root; needs sudo for libvirt / network / VM
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/up.yml \
  --ask-become-pass
# Add --ask-vault-pass if vault.yml is encrypted
```

`up.yml` roughly:

1. Provision L1 VM + L0 management network (`vm_provision`)
2. Preflight inside the VM
3. Docker compose stack + sushy nested nodes (`lab_up`)
4. NetBox bootstrap
5. Build/publish Phase 2 marker ISO to `/srv/iso` on `:8080`

First run is long: Ubuntu base image download, Docker pulls, and **Ollama model
pulls** (`llama3.2:3b` plus optional `deepseek-ocr`).

### 6. Verify

```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/smoke.yml \
  --ask-become-pass

virsh -c qemu:///system list --all
ssh -i ~/.ssh/shoal_lab_vm lab@192.168.122.100
```

The playbooks create the SSH keypair under `~/.ssh/shoal_lab_vm` by default
(kept across `down.yml` unless you set `shoal_remove_ssh_key=true`).

#### Default endpoints (L0 → lab VM)

| Service | URL / address |
|---------|----------------|
| NetBox | http://192.168.122.100:8000 |
| Redfish (sushy) | http://192.168.122.100:8001/redfish/v1 |
| ISO HTTP | http://192.168.122.100:8080 |
| Ollama | http://192.168.122.100:11434 |
| Telemetry Postgres | `192.168.122.100:5433` |
| Shoal app (Compose service when enabled) | http://192.168.122.100:8088 |

NetBox API without a token returns `403` by design. BMC nodes on the **inner**
network use gateway `192.168.124.1` for ISO fetch
(e.g. `http://192.168.124.1:8080/shoal-marker.iso`).

#### Ready criteria

- [ ] `up.yml` completes without errors
- [ ] `smoke.yml` passes
- [ ] NetBox and Redfish URLs above respond
- [ ] You can SSH into the lab VM with the configured key

### 7. Tear down

```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/down.yml \
  --ask-become-pass
```

This stops the stack and removes the VM, overlay disk, cached base image, L0
management network, and generated cloud-init artifacts. Nested services go away
with the VM. Rebuild from scratch:

```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/down.yml --ask-become-pass
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/up.yml --ask-become-pass
ansible-playbook -i infra/ansible/inventory/lab-vm.yml infra/ansible/playbooks/smoke.yml --ask-become-pass
```

---

## Optional: app smoke from L0

With the lab healthy (Go is already required on the controller for `up.yml`
when the Compose app is enabled):

```bash
export SHOAL_BMC_USERNAME=admin          # match vault
export SHOAL_BMC_PASSWORD=password
export SHOAL_TELEMETRY_DATABASE_URL='postgres://shoal:shoal_password@192.168.122.100:5433/shoal_telemetry?sslmode=disable'
export SHOAL_SERIAL_SSH_HOST=192.168.122.100
export SHOAL_SERIAL_SSH_KEY=$HOME/.ssh/shoal_lab_vm

go run ./cmd/shoal deploy run \
  -device-id shoal-node-1 \
  -bmc-url http://192.168.122.100:8001 \
  -serial-target shoal-node-1 \
  -iso-url http://192.168.124.1:8080/shoal-marker.iso
```

Use the DB user/password from your `vault.yml` / compose env, not necessarily the
placeholder above. See [lab-runbook.md](./lab-runbook.md) for Phase 2+ exercises
(Discover, Observe poll, multi-stage deploy, NetBox plugin tabs).

---

## Common laptop gotchas

| Symptom | What to check |
|---------|----------------|
| Nested virt assert fails | `modprobe.d` options + reboot; nested must be `Y`/`1` |
| No `/dev/kvm` | Enable VT-x/AMD-V in firmware; bare metal or properly nested host |
| `lxml` / libvirt not importable | `sudo apt install python3-lxml python3-libvirt` (same Python Ansible uses) |
| `No such file or directory: b'go'` on compose_stack | Install **Go 1.22+** on the controller (`golang-go` or [go.dev](https://go.dev/dl/)); `which go` must work for your user |
| OOM / thrashing | Lower `shoal_lab_vm_memory` / CPUs; close heavy apps on the host |
| Disk full mid-pull | Free space for 100 G disk + images + Ollama; clean Docker on L1 if needed |
| Permission errors on virsh | `--ask-become-pass`; re-login after `libvirt` group add |
| Firewall blocks mgmt bridge | Open traffic on `shoalbr0` / ufw as needed |
| Wrong inventory | Use `lab-vm.yml` for nested lab, not `lab.yml` |
| Smoke CD-tray assertion after jobs | sushy eject removes libvirt CD devices — see runbook “Nested Virtual Media” repair |

**Lab fidelity (not Debian-specific):** nested sushy cannot prove real Redfish SOL
or dual Virtual Media `second_media`. Those need real BMC hardware; see
[real-hardware-sol-runbook.md](./real-hardware-sol-runbook.md) and the runbook
Virtual Media section.

---

## Where packages get installed

| Layer | Packages / services |
|-------|---------------------|
| **L0 (this Debian host)** | QEMU/KVM, libvirt, genisoimage/xorriso, Ansible, Go 1.22+, `python3-lxml` / `python3-libvirt`, SSH client |
| **L1 (lab VM)** | Docker, compose, libvirt for nested nodes, mtools/dosfstools, ISO tooling — via Ansible roles (`host_prereqs`, `compose_stack`, `sushy_tools`, …) |

You only hand-install the L0 stack above; the rest is converged by `up.yml`.

---

## References

- [Lab Setup Checklist](./lab-setup-checklist.md) — generic first-time list  
- [Lab Runbook](./lab-runbook.md) — smoke, app exercises, Virtual Media repair  
- `infra/ansible/inventory/lab-vm.yml` — VM-hosted inventory  
- `infra/ansible/inventory/group_vars/all/defaults.yml` — sizes, ports, AI models  
- `infra/ansible/inventory/group_vars/all/vault.yml.example` — secrets template  
- `AGENTS.md` §3.1 — lab commands  
