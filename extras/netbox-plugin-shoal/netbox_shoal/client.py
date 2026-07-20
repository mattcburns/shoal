"""Thin HTTP client for the Shoal API, used by the plugin's views.

Deliberately minimal: no retries, no caching, one request per call. Base URL
and token come only from PLUGINS_CONFIG (admin-configured), never from
request input -- the plugin never proxies an operator-supplied host, so
there's no SSRF surface here by construction.

Returns (data, error) tuples instead of raising so views can render a plain
"Shoal unavailable" message rather than a 500 when Shoal is down, unreachable,
or simply not configured yet.
"""

import requests
from django.conf import settings

PLUGIN_NAME = "netbox_shoal"


def _cfg():
    return settings.PLUGINS_CONFIG.get(PLUGIN_NAME, {})


def _get(path, params=None):
    cfg = _cfg()
    base = (cfg.get("SHOAL_BASE_URL") or "").rstrip("/")
    if not base:
        return None, "Shoal base URL not configured (SHOAL_BASE_URL)"

    headers = {}
    token = cfg.get("SHOAL_API_TOKEN")
    if token:
        headers["Authorization"] = "Bearer " + token

    timeout = cfg.get("SHOAL_REQUEST_TIMEOUT", 10)

    try:
        resp = requests.get(base + path, headers=headers, params=params, timeout=timeout)
        resp.raise_for_status()
        return resp.json(), None
    except requests.RequestException as exc:
        return None, str(exc)


def get_status(device_id):
    """GET /v1/devices/{id}/status -> (models.DeviceStatus dict, error)."""
    return _get("/v1/devices/%s/status" % device_id)


def get_events(device_id, limit=50):
    """GET /v1/devices/{id}/events?limit= -> ({"device_id","events":[...]}, error)."""
    return _get("/v1/devices/%s/events" % device_id, params={"limit": limit})
