# Operator host: macOS

Phase **6c** treats macOS as an **operator** machine: run the Shoal binary (and optionally Ansible as a controller) against a **Linux-hosted lab**. Nested KVM lab on Darwin is **not** supported.

## What works on Mac

| Task | Supported? |
|------|------------|
| Run `shoal` CLI / `serve` against remote lab endpoints | Yes |
| `go build` / download **darwin/amd64** or **darwin/arm64** binary | Yes |
| Drive Ansible playbooks when `shoal-lab-vm` is a remote Linux host | Yes (SSH) |
| Host L0 libvirt + nested L1 lab (`vm_l0` = localhost) | **No** |

You do **not** need Docker Desktop for the default path. Lab services (NetBox, Ollama, Postgres, ISO HTTP, sushy) stay on the Linux lab VM.

## Get a binary

**Release (preferred once tags exist):**

```bash
# Example: Apple Silicon
curl -fsSL -o shoal_darwin_arm64.tar.gz \
  "https://github.com/mattcburns/shoal/releases/download/vX.Y.Z/shoal_darwin_arm64.tar.gz"
tar -xzf shoal_darwin_arm64.tar.gz
./shoal_darwin_arm64 version
```

Archives include `LICENSE`, `NOTICE`, and third-party license texts.

**Local build:**

```bash
# From repo root; or use the multi-platform script
CGO_ENABLED=0 go build -o shoal ./cmd/shoal

# All release targets (including linux) into dist/
./scripts/build-release.sh
```

## Point at a lab

Typical VM-hosted endpoints (Linux L0 nested lab):

| Service | Default URL |
|---------|-------------|
| NetBox | `http://192.168.122.100:8000` |
| Redfish (sushy) | `http://192.168.122.100:8001` |
| ISO | `http://192.168.122.100:8080` |
| Shoal API (if running in lab) | `http://192.168.122.100:8088` |
| Telemetry Postgres | `192.168.122.100:5433` |
| Ollama | `http://192.168.122.100:11434` |

Export the usual env vars (`SHOAL_NETBOX_*`, `SHOAL_TELEMETRY_DATABASE_URL`, `SHOAL_OLLAMA_URL`, BMC creds, etc.) from your vault/docs. Reachability requires VPN, LAN, or SSH tunnels to the lab management network.

Example tunnel (lab VM only reachable on a remote Linux L0):

```bash
ssh -L 8000:192.168.122.100:8000 \
    -L 8001:192.168.122.100:8001 \
    -L 8080:192.168.122.100:8080 \
    -L 5433:192.168.122.100:5433 \
    -L 11434:192.168.122.100:11434 \
    user@linux-l0-host
```

Then use `http://127.0.0.1:…` in env vars.

## Where to run the nested lab

Use a Linux L0 hypervisor:

- Classic: Ubuntu/Fedora/Arch with libvirt + nested KVM (existing path)
- **Fedora secureblue**: supported as L0 for VM-hosted mode — see [lab-setup-checklist.md](./lab-setup-checklist.md) § L0 secureblue

Bring up from a Linux L0 (or from Mac only if inventory SSH targets that Linux host’s libvirt — advanced; default inventory uses `localhost` connection local):

```bash
ansible-playbook -i infra/ansible/inventory/lab-vm.yml \
  infra/ansible/playbooks/up.yml --ask-become-pass
```

## Ansible from macOS

- Install Ansible (Homebrew: `brew install ansible`) and collections:
  `ansible-galaxy collection install -r infra/ansible/requirements.yml`
- Default `lab-vm.yml` sets `vm_l0` → `localhost` with `ansible_connection: local`. That **must** be a Linux KVM host. On a Mac laptop, either:
  1. Run `up.yml` **on** the Linux L0 (SSH in and clone the repo), or
  2. Change inventory so `vm_l0` is the Linux hypervisor over SSH (not documented as primary; keep secrets/paths in mind).

`lab_vm` **fails fast** if it detects Darwin as L0, with a pointer back to this doc.

## Related

- Design Phase 6c: design doc v2.0.6 / [phase-6c-plan.md](./phase-6c-plan.md)
- Lab ops: [lab-runbook.md](./lab-runbook.md)
- License notices: [NOTICE](../NOTICE), [third-party-licenses.md](./third-party-licenses.md)
