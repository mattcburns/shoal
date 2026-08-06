# netbox-shoal

A NetBox plugin that adds **Shoal Status**, **Shoal Events**, **Shoal Jobs**,
and **Shoal Sensors** tabs to the device detail page, reading
[Shoal](https://github.com/mattcburns/shoal)'s HTTP API server-side. See
[`docs/netbox-telemetry-ui-design.md`](../../docs/netbox-telemetry-ui-design.md)
in the main repo for the full design. This package covers slices **N4–N6**
plus demo polish: progress bars, stage list, job log panel, auto-refresh while
provisioning, and a **Start provision / Cancel** control on the Status tab
(v0.3). Grafana and a full profile wizard remain optional follow-ons.

This is the first Python code in the Shoal repo. It runs entirely inside the
lab's NetBox container — it is never linked into the Shoal Go binary and
follows AGENTS.md's "lab automation... and whatever Python the lab tools
require" carve-out from the project's Go-only application stack rule, not an
exception to it.

## Identity convention

Shoal's `device_id` **is** the NetBox device's own numeric primary key
(`str(device.pk)`) — established by `internal/common/netbox/client.go`'s
`UpsertDevice`, which looks devices up by serial but keys everything
downstream (jobs, events, telemetry) by the NetBox device ID it gets back.
No separate `shoal_device_id` custom field is needed or used; each view here
just reads `instance.pk` off the `Device` it's already rendering.

**Lab demo:** bootstrap sets each `shoal-node-*` **serial equal to its name**.
With `SHOAL_NETBOX_URL` + token set, Deploy remaps
`-device-id shoal-node-1` → that device's pk so these tabs light up without
operators looking up numeric ids. See `docs/lab-runbook.md` § NetBox demo.

## Configuration

Set in NetBox's `configuration.py` (or, in the lab, the Ansible-rendered
`plugins.py` mounted alongside it — see `infra/ansible/roles/compose_stack`):

```python
PLUGINS = ["netbox_shoal"]

PLUGINS_CONFIG = {
    "netbox_shoal": {
        "SHOAL_BASE_URL": "http://192.168.122.100:8088",  # empty = "not configured" state in the UI
        "SHOAL_API_TOKEN": "",                             # optional Bearer token; empty = no header sent
        "SHOAL_REQUEST_TIMEOUT": 30,                        # seconds (Start can be slow)
        "SHOAL_ENABLE_ACTIONS": True,                       # Status Start/Cancel forms
        "SHOAL_DEFAULT_BMC_ENDPOINT": "http://127.0.0.1:8001",
        "SHOAL_DEFAULT_ISO_URL": "http://192.168.124.1:8080/shoal-marker.iso",
        "SHOAL_DEFAULT_PROFILE_REF": "spike",
    }
}
```

Start/cancel requires NetBox `dcim.change_device`. BMC passwords are **not**
stored in NetBox: leave user/pass blank so Shoal uses `SHOAL_BMC_*` env defaults.

## Development

```bash
cd extras/netbox-plugin-shoal
python -m unittest discover -s tests
```

The `client.py` module is deliberately Django-settings-only (no models, no
migrations), so it's unit-testable with stdlib `unittest` + `unittest.mock`
without a full NetBox test app/database. View-level (`ObjectView`) behavior
is verified by manual lab smoke test (see `docs/lab-runbook.md`), not
automated here yet — building full NetBox Django test-app scaffolding is a
larger, separate piece of work.

## Building into the lab image

```bash
docker build -t netbox-shoal:local extras/netbox-plugin-shoal
```

In the lab, this happens automatically via
`infra/ansible/roles/compose_stack` (the `netbox` service builds from
`extras/netbox-plugin-shoal/Dockerfile` instead of pulling the upstream
image directly).
