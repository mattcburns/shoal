# Shoal - Redfish Aggregator

Shoal is a Go-based Redfish aggregator that discovers and manages multiple Baseboard Management Controllers (BMCs) through a single, unified, Redfish‑compliant API. It ships as a single binary with a web UI and REST API for power control and settings discovery.

## Key Features

- Redfish-compliant API and proxy to downstream BMCs
- Aggregates many BMCs into one interface and API
- Web UI for management, status, power control, and users
- Settings discovery for BIOS, NICs, and storage (009)
- Virtual Media management for remote ISO/disk image mounting (020)
- Optional image proxy server for BMCs without direct internet access
- **Cloud-init ISO generation** for automated provisioning via OEM extension
- **Kickstart/Preseed provisioning** - Redfish OEM extension endpoints serving installation configs with dynamic variable substitution


## Documentation

- Getting Started: [docs/1_getting_started.md](docs/1_getting_started.md)
- Usage Guide: [docs/2_usage.md](docs/2_usage.md)
- API Guide: [docs/3_api.md](docs/3_api.md)
- Development: [docs/4_development.md](docs/4_development.md)
- Deployment & Ops: [docs/5_deployment.md](docs/5_deployment.md)

## Quick Start

```bash
# Build from source
git clone https://github.com/mattcburns/shoal.git
cd shoal
go run build.go validate
./build/shoal
```

Open http://localhost:8080 and log in with `admin` / `admin` (change immediately).

Alternatively, download a prebuilt binary from [Releases](https://github.com/mattcburns/shoal/releases), make it executable, and run it.

## Shoal-Provision

There is a fork of this project that is in the process of implementing an experimental provisioner, you can
find it at [Shoal-Provision](https://github.com/mattcburns/shoal-provision)

## License

AGPL-3.0. See `LICENSE`.
