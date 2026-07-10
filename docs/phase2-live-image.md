# Phase 2 live image (marker producer)

Phase 2 needs a minimal live image that emits `SHOAL|…` markers over serial
(`console=ttyS0,115200n8`) and is published to the lab ISO HTTP tree (`:8080`).

## Protocol

```
SHOAL|<schema_ver>|<seq>|<iso8601_utc>|<phase>|<percent>|<state>|<detail>
```

See design §4.3. Reference emitter: [`infra/scripts/marker-producer.sh`](../infra/scripts/marker-producer.sh).

## Publish contract

1. Build ISO (primary: Ansible on lab VM; alternate: workstation).
2. Copy into lab ISO dir (e.g. `/srv/iso/shoal-marker.iso`).
3. BMC-reachable URL examples:
   - VM-hosted: `http://192.168.122.100:8080/shoal-marker.iso`
   - Direct: `http://127.0.0.1:8080/shoal-marker.iso`
4. Pass as `-iso-url` to `shoal deploy run`.

In-process ISO serving is **not** required for Phase 2.

## Primary build host (lab VM Ansible)

Documented path (to be role-ified): install `dracut`/`xorriso` (or equivalent) on
the L1 lab VM, build the marker image, write into `shoal_iso_server_dir`, print URL.

## Alternate (workstation)

Build locally, then `scp`/`rsync` into the lab ISO directory so sushy-tools nodes
can fetch the image over the management network.

## Spike without a full ISO

For app-level tests, feed marker lines into a pipe/PTY using `marker-producer.sh`
and point `-serial-target` at that console path. Unit tests use an in-process
`ReaderTransport` instead of libvirt.
